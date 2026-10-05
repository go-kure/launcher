package kurel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	kureio "github.com/go-kure/kure/pkg/io"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// helm secretValues (go-kure/launcher#786), through the whole built-in
// pipeline: under delivery: flux a Secret emitted by a secret trait on the
// helmrelease and read through valuesFrom; under delivery: template values
// merged into the client-side render. Either way the sensitive values are in
// the output only where that says, and in no error.

// secretSentinel is the sensitive value the tests look for.
const secretSentinel = "s3cr3t-sentinel-value"

// noExplicitSecrets is NoopPolicy forbidding explicit secrets.
type noExplicitSecrets struct{ *oam.NoopPolicy }

func (noExplicitSecrets) AllowExplicitSecrets() bool { return false }

// helmSecretComponent is a helm component referencing an existing
// HelmRepository, with plain values, a sensitive value and one authored
// valuesFrom entry; mutate adjusts its properties.
func helmSecretComponent(password string, mutate func(props map[string]any)) oam.Component {
	props := map[string]any{
		"chart":        "podinfo",
		"source":       map[string]any{"kind": "HelmRepository", "name": "podinfo"},
		"values":       map[string]any{"replicaCount": 2, "auth": map[string]any{"user": "admin"}},
		"secretValues": map[string]any{"auth": map[string]any{"password": password}},
		"valuesFrom":   []any{map[string]any{"kind": "Secret", "name": "creds"}},
	}
	if mutate != nil {
		mutate(props)
	}
	return oam.Component{Name: "podinfo", Type: "helm", Properties: props}
}

// generateSecretApp transforms one component under policy and fluxNS and
// generates every application once.
func generateSecretApp(fluxNS string, policy oam.Policy, comp oam.Component) ([]oam.GeneratedApplication, error) {
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "app", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: []oam.Component{comp}},
	}
	cluster, _, err := newBuiltinTransformer().TransformWithPolicy(app, oam.TransformContext{FluxNamespace: fluxNS, Domain: kurelDomain, Policy: policy})
	if err != nil {
		return nil, err
	}
	return oam.GenerateApplications(cluster)
}

// componentLabels are the labels of an object the named component owns: the
// `app` label its handler or trait writes and the component label the
// transform adds.
func componentLabels(name string) map[string]string {
	v := oam.ComponentLabelValue(name)
	return map[string]string{"app": v, kurelComponentLabel: v}
}

// objectYAML is obj as it is written into a manifest file.
func objectYAML(t *testing.T, obj client.Object) string {
	t.Helper()
	data, err := kureio.EncodeObjectsToYAML([]*client.Object{&obj})
	if err != nil {
		t.Fatalf("EncodeObjectsToYAML: %v", err)
	}
	return string(data)
}

// assertNoSentinel fails when err is nil or carries the sensitive value.
func assertNoSentinel(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("got no error")
	}
	if strings.Contains(err.Error(), secretSentinel) {
		t.Errorf("the error carries the sensitive value: %v", err)
	}
}

// assertViolation fails unless err is a policy violation naming component.
func assertViolation(t *testing.T, err error, component string) {
	t.Helper()
	var v *oam.ViolationError
	if !errors.As(err, &v) {
		t.Fatalf("error is %T, want it to wrap *oam.ViolationError: %v", err, err)
	}
	if v.Component != component {
		t.Errorf("violation names component %q, want %q", v.Component, component)
	}
	if !strings.Contains(err.Error(), "the environment policy forbids explicit secrets") {
		t.Errorf("error %q is not the explicit-secret refusal", err)
	}
}

// TestHelmSecretValues_Emitted: under delivery: flux the output holds a Secret
// with the secretValues and a valuesFrom entry of kind Secret, after the values
// ConfigMap's and before the authored one. The sensitive value is in the Secret
// object, base64-encoded under data, and nowhere else: not in the HelmRelease,
// not in the values ConfigMap, not in any GeneratedApplication field. Under a
// Flux namespace the Secret follows the HelmRelease there.
func TestHelmSecretValues_Emitted(t *testing.T) {
	for _, mode := range []string{"inline", "configMap"} {
		for _, fluxNS := range []string{"", fluxNSTarget} {
			t.Run(mode+"/"+fluxNS, func(t *testing.T) {
				apps, err := generateSecretApp(fluxNS, nil, helmSecretComponent(secretSentinel, func(p map[string]any) { p["valuesMode"] = mode }))
				if err != nil {
					t.Fatalf("transform: %v", err)
				}

				var hr *helmv2.HelmRelease
				var cm *corev1.ConfigMap
				var secret *corev1.Secret
				var order []string
				for _, a := range apps {
					if strings.Contains(a.Name, secretSentinel) || strings.Contains(a.Component, secretSentinel) || strings.Contains(a.String(), secretSentinel) {
						t.Errorf("GeneratedApplication %s names the sensitive value", a)
					}
					if a.Component != "podinfo" {
						t.Errorf("application %q belongs to component %q, want podinfo", a.Name, a.Component)
					}
					for _, o := range a.Objects {
						order = append(order, (*o).GetObjectKind().GroupVersionKind().Kind)
						switch obj := (*o).(type) {
						case *helmv2.HelmRelease:
							hr = obj
						case *corev1.ConfigMap:
							cm = obj
						case *corev1.Secret:
							secret = obj
							continue
						}
						if written := objectYAML(t, *o); strings.Contains(written, secretSentinel) {
							t.Errorf("%T carries the sensitive value:\n%s", *o, written)
						}
					}
				}
				wantOrder := []string{"HelmRelease", "Secret"}
				if mode == "configMap" {
					wantOrder = []string{"HelmRelease", "ConfigMap", "Secret"}
				}
				if !reflect.DeepEqual(order, wantOrder) {
					t.Fatalf("generated %v, want %v", order, wantOrder)
				}

				// The Secret: one values.json entry holding the secretValues, named for
				// the digest of those bytes, labelled as the component's.
				stored := secret.Data["values.json"]
				sum := sha256.Sum256(stored)
				if len(secret.Data) != 1 || secret.Name != "podinfo-secret-values-"+hex.EncodeToString(sum[:])[:10] {
					t.Errorf("Secret %s with %d entries, want one values.json named for its digest", secret.Name, len(secret.Data))
				}
				var got map[string]any
				if err := json.Unmarshal(stored, &got); err != nil {
					t.Fatalf("stored secretValues: %v", err)
				}
				if want := map[string]any{"auth": map[string]any{"password": secretSentinel}}; !reflect.DeepEqual(got, want) {
					t.Errorf("the Secret does not hold the authored secretValues")
				}
				if secret.StringData != nil {
					t.Error("the Secret carries stringData; its entries belong under data")
				}
				if written := objectYAML(t, secret); strings.Contains(written, secretSentinel) {
					t.Errorf("the written Secret carries the value outside base64")
				}
				if want := componentLabels("podinfo"); !maps.Equal(secret.Labels, want) {
					t.Errorf("Secret labels %v, want %v", secret.Labels, want)
				}

				// The HelmRelease: reads the Secret after the values ConfigMap and
				// before the authored entry, and keeps only the plain values.
				var from []string
				for _, vf := range hr.Spec.ValuesFrom {
					from = append(from, vf.Kind+"/"+vf.Name+"/"+vf.ValuesKey)
				}
				wantFrom := []string{"Secret/" + secret.Name + "/values.json", "Secret/creds/"}
				if mode == "configMap" {
					wantFrom = append([]string{"ConfigMap/" + cm.Name + "/values.json"}, wantFrom...)
					if hr.Spec.Values != nil {
						t.Errorf("HelmRelease still carries inline values %s", hr.Spec.Values.Raw)
					}
				} else if hr.Spec.Values == nil || !strings.Contains(string(hr.Spec.Values.Raw), `"user":"admin"`) {
					t.Errorf("HelmRelease inline values = %v, want the plain values", hr.Spec.Values)
				}
				if !reflect.DeepEqual(from, wantFrom) {
					t.Errorf("valuesFrom = %v, want %v", from, wantFrom)
				}

				wantNS := "default"
				if fluxNS != "" {
					wantNS = fluxNS
				}
				if hr.Namespace != wantNS || secret.Namespace != wantNS {
					t.Errorf("HelmRelease in %q, Secret in %q, want both in %q", hr.Namespace, secret.Namespace, wantNS)
				}
			})
		}
	}
}

// TestHelmSecretValues_ChangeMovesSecretNameAndRelease: an edit of secretValues
// alone changes the Secret's name and the HelmRelease's spec, which is what
// makes Flux reconcile it.
func TestHelmSecretValues_ChangeMovesSecretNameAndRelease(t *testing.T) {
	build := func(password string) (string, helmv2.HelmReleaseSpec) {
		apps, err := generateSecretApp("", nil, helmSecretComponent(password, nil))
		if err != nil {
			t.Fatalf("transform: %v", err)
		}
		var name string
		var spec helmv2.HelmReleaseSpec
		for _, a := range apps {
			for _, o := range a.Objects {
				switch obj := (*o).(type) {
				case *helmv2.HelmRelease:
					spec = obj.Spec
				case *corev1.Secret:
					name = obj.Name
				}
			}
		}
		return name, spec
	}
	n1, s1 := build("one")
	n2, s2 := build("two")
	n3, s3 := build("one")
	if n1 == "" || n1 == n2 {
		t.Errorf("Secret names %q and %q, want two different names", n1, n2)
	}
	if reflect.DeepEqual(s1, s2) {
		t.Error("the HelmRelease spec did not change with secretValues")
	}
	if n1 != n3 || !reflect.DeepEqual(s1, s3) {
		t.Error("the same secretValues did not build the same Secret name and HelmRelease")
	}
}

// TestHelmSecretValues_PolicyForbidsExplicitSecrets: a policy that forbids
// explicit secrets refuses secretValues under either delivery, and an authored
// secret trait, with a violation naming the component and no value. A policy
// that does not implement the optional interface allows all three.
func TestHelmSecretValues_PolicyForbidsExplicitSecrets(t *testing.T) {
	template := func(p map[string]any) {
		p["delivery"] = "template"
		// Nothing listens here: the refusal comes before any fetch.
		p["source"] = map[string]any{"url": "http://127.0.0.1:1"}
		delete(p, "valuesFrom")
	}
	authored := oam.Component{Name: "settings", Type: "configmap",
		Properties: map[string]any{"data": map[string]any{"k": "v"}},
		Traits: []oam.Trait{{Type: "secret", Properties: map[string]any{
			"name": "settings-creds", "stringData": map[string]any{"password": secretSentinel}}}}}
	cases := map[string]oam.Component{
		"flux delivery":         helmSecretComponent(secretSentinel, nil),
		"template delivery":     helmSecretComponent(secretSentinel, template),
		"authored secret trait": authored,
	}
	for name, comp := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := generateSecretApp("", noExplicitSecrets{&oam.NoopPolicy{}}, comp)
			assertNoSentinel(t, err)
			assertViolation(t, err, comp.Name)
		})
	}

	for _, name := range []string{"flux delivery", "authored secret trait"} {
		if _, err := generateSecretApp("", permissivePolicy{&oam.NoopPolicy{}}, cases[name]); err != nil {
			t.Errorf("%s: a policy without the optional interface refused it: %v", name, err)
		}
	}
}

// TestHelmSecretValues_AuthoredSecretTrait: the secret trait is authorable on
// its own, and emits the component's labelled Secret beside the component.
func TestHelmSecretValues_AuthoredSecretTrait(t *testing.T) {
	apps, err := generateSecretApp("", nil, oam.Component{Name: "settings", Type: "configmap",
		Properties: map[string]any{"data": map[string]any{"k": "v"}},
		Traits: []oam.Trait{{Type: "secret", Properties: map[string]any{
			"name": "settings-creds", "type": "kubernetes.io/basic-auth",
			"stringData": map[string]any{"username": "admin", "password": secretSentinel}}}}})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	var secret *corev1.Secret
	for _, a := range apps {
		for _, o := range a.Objects {
			if s, ok := (*o).(*corev1.Secret); ok {
				secret = s
			}
		}
	}
	if secret == nil {
		t.Fatal("no Secret generated")
	}
	if secret.Name != "settings-creds" || secret.Namespace != "default" || secret.Type != "kubernetes.io/basic-auth" {
		t.Errorf("Secret %s/%s type %q", secret.Namespace, secret.Name, secret.Type)
	}
	if string(secret.Data["password"]) != secretSentinel || string(secret.Data["username"]) != "admin" {
		t.Error("the Secret does not hold the authored entries")
	}
	if want := componentLabels("settings"); !maps.Equal(secret.Labels, want) {
		t.Errorf("Secret labels %v, want %v", secret.Labels, want)
	}
}

// TestHelmSecretValues_AuthoredSecretTraitRefusalsCarryNoValue: a wrongly
// shaped secret trait is refused by the pipeline, property validation included,
// without quoting what it holds.
func TestHelmSecretValues_AuthoredSecretTraitRefusalsCarryNoValue(t *testing.T) {
	cases := map[string]map[string]any{
		"stringData not an object":  {"stringData": secretSentinel},
		"stringData value a list":   {"stringData": map[string]any{"password": []any{secretSentinel}}},
		"stringData value a number": {"stringData": map[string]any{"password": 12345}},
		"data not base64":           {"data": map[string]any{"token": secretSentinel + "!"}},
		"data not an object":        {"data": secretSentinel},
		"key in both": {"stringData": map[string]any{"password": secretSentinel},
			"data": map[string]any{"password": "cGxhaW4="}},
	}
	for name, props := range cases {
		t.Run(name, func(t *testing.T) {
			p := map[string]any{"name": "settings-creds"}
			maps.Copy(p, props)
			_, err := generateSecretApp("", nil, oam.Component{Name: "settings", Type: "configmap",
				Properties: map[string]any{"data": map[string]any{"k": "v"}},
				Traits:     []oam.Trait{{Type: "secret", Properties: p}}})
			assertNoSentinel(t, err)
			if !strings.Contains(err.Error(), "stringData") && !strings.Contains(err.Error(), "data") {
				t.Errorf("error %q names neither property", err)
			}
		})
	}
}

// TestHelmSecretValues_RefusalsCarryNoValue: what the pipeline refuses about
// secretValues it refuses without quoting them: a path shared with values, a
// value that is not an object, and the secret trait's own size limit, which the
// synthesized trait is held to.
func TestHelmSecretValues_RefusalsCarryNoValue(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(map[string]any)
		wantErr string
	}{
		{"shared path", func(p map[string]any) {
			p["values"] = map[string]any{"auth": map[string]any{"password": "plain"}}
		}, "helm: auth.password is set in both values and secretValues"},
		{"shared path, values in a ConfigMap", func(p map[string]any) {
			p["valuesMode"] = "configMap"
			p["values"] = map[string]any{"auth": map[string]any{"password": "plain"}}
		}, "helm: auth.password is set in both values and secretValues"},
		{"not an object", func(p map[string]any) { p["secretValues"] = secretSentinel }, "helm: secretValues: must be an object, got string"},
		{"over the Secret size limit", func(p map[string]any) {
			p["secretValues"] = map[string]any{"password": secretSentinel, "blob": strings.Repeat("x", 1<<20)}
		}, "byte limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := generateSecretApp("", nil, helmSecretComponent(secretSentinel, tc.mutate))
			assertNoSentinel(t, err)
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q lacks %q", err, tc.wantErr)
			}
		})
	}
}

// TestHelmSecretValues_ParsedDocument: the properties pass authored-property
// validation as a document carries them, and a wrongly typed secretValues is
// refused there without its content.
func TestHelmSecretValues_ParsedDocument(t *testing.T) {
	const doc = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: demo
spec:
  components:
    - name: podinfo
      type: helm
      properties:
        chart: podinfo
        source:
          kind: HelmRepository
          name: podinfo
        values:
          replicaCount: 2
        secretValues: %s
      traits:
        - type: secret
          properties:
            name: extra-creds
            stringData:
              token: ` + secretSentinel + `
`
	transformer := newBuiltinTransformer()
	app, err := oam.ParseWithExtraTypes([]byte(fmt.Sprintf(doc, "{auth: {password: "+secretSentinel+"}}")), nil, transformer.LowerableTypes())
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	cluster, err := transformer.Transform(app, oam.TransformContext{Domain: kurelDomain})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	secrets := 0
	for _, a := range apps {
		for _, o := range a.Objects {
			if _, ok := (*o).(*corev1.Secret); ok {
				secrets++
			}
		}
	}
	if secrets != 2 {
		t.Errorf("generated %d Secrets, want the values Secret and the authored one", secrets)
	}

	app, err = oam.ParseWithExtraTypes([]byte(fmt.Sprintf(doc, secretSentinel)), nil, transformer.LowerableTypes())
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	_, err = transformer.Transform(app, oam.TransformContext{Domain: kurelDomain})
	assertNoSentinel(t, err)
	if !strings.Contains(err.Error(), "secretValues") {
		t.Errorf("error %q does not name secretValues", err)
	}
}

// secretChartRepo serves a one-chart Helm repository local to t and returns
// its URL. files are the chart's files beside its Chart.yaml, keyed by their
// path in the chart (templates/cm.yaml, charts/child/Chart.yaml).
func secretChartRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	url, _ := countingSecretChartRepo(t, files)
	return url
}

// countingSecretChartRepo is secretChartRepo, and also returns the count of the
// requests the repository has received.
func countingSecretChartRepo(t *testing.T, files map[string]string) (string, *atomic.Int64) {
	t.Helper()
	archived := map[string]string{}
	for name, content := range files {
		archived["secretchart/"+name] = content
	}
	chart := buildMinimalChartTar(t, "secretchart", "0.1.0", archived)
	requests := &atomic.Int64{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/index.yaml":
			fmt.Fprint(w, helmIndexYAML("secretchart", "0.1.0", "http://"+r.Host+"/secretchart-0.1.0.tgz"))
		case "/secretchart-0.1.0.tgz":
			_, _ = w.Write(chart)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, requests
}

// templateSecretComponent is a helm component under delivery: template on the
// chart repository at url, with the password in secretValues, or in the plain
// values when plain is set.
func templateSecretComponent(url string, plain bool) oam.Component {
	return helmSecretComponent(secretSentinel, func(p map[string]any) {
		p["delivery"] = "template"
		p["chart"] = "secretchart"
		p["version"] = "0.1.0"
		p["source"] = map[string]any{"url": url}
		delete(p, "valuesFrom")
		if plain {
			p["values"] = map[string]any{"auth": map[string]any{"user": "admin", "password": secretSentinel}}
			delete(p, "secretValues")
		}
	})
}

// TestHelmSecretValues_TemplateDelivery renders a real chart with secretValues:
// the values reach the render, merged with the plain ones, and are in the
// output exactly where the chart puts them. The component emits no object of
// its own, so the ConfigMap the chart renders from plain values carries none.
func TestHelmSecretValues_TemplateDelivery(t *testing.T) {
	url := secretChartRepo(t, map[string]string{
		"templates/cm.yaml":     "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\ndata:\n  user: {{ .Values.auth.user }}\n  replicas: {{ .Values.replicaCount | quote }}\n",
		"templates/secret.yaml": "apiVersion: v1\nkind: Secret\nmetadata:\n  name: login\nstringData:\n  password: {{ .Values.auth.password }}\n",
	})
	apps, err := generateSecretApp("", nil, templateSecretComponent(url, false))
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	byKind := map[string]client.Object{}
	for _, a := range apps {
		for _, o := range a.Objects {
			byKind[(*o).GetObjectKind().GroupVersionKind().Kind] = *o
		}
	}
	if len(byKind) != 2 {
		t.Fatalf("generated kinds %v, want the chart's ConfigMap and Secret only", slicesOfKeys(byKind))
	}
	secret, ok := byKind["Secret"].(*corev1.Secret)
	if !ok || secret.StringData["password"] != secretSentinel {
		t.Errorf("the rendered Secret does not carry the secretValues password: the value did not reach the render")
	}
	cm, ok := byKind["ConfigMap"].(*corev1.ConfigMap)
	if !ok {
		t.Fatalf("ConfigMap is %T", byKind["ConfigMap"])
	}
	if want := map[string]string{"user": "admin", "replicas": "2"}; !maps.Equal(cm.Data, want) {
		t.Errorf("ConfigMap data = %v, want %v from the plain values", cm.Data, want)
	}
	if written := objectYAML(t, cm); strings.Contains(written, secretSentinel) {
		t.Errorf("the rendered ConfigMap carries the sensitive value:\n%s", written)
	}
}

func slicesOfKeys(m map[string]client.Object) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// TestHelmSecretValues_TemplateRenderFailureWithholdsTheValue: a chart whose
// template fails quoting the password reports it when the password is a plain
// value (the control: Helm's error does repeat the value), and does not when it
// is in secretValues.
func TestHelmSecretValues_TemplateRenderFailureWithholdsTheValue(t *testing.T) {
	url := secretChartRepo(t, map[string]string{
		"templates/cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n" +
			"{{- if .Values.auth.password }}\n{{ fail (printf \"rejected password %s\" .Values.auth.password) }}\n{{- end }}\n",
	})

	_, err := generateSecretApp("", nil, templateSecretComponent(url, true))
	if err == nil || !strings.Contains(err.Error(), secretSentinel) {
		t.Fatalf("control: err = %v, want Helm's error repeating the plain value", err)
	}

	_, err = generateSecretApp("", nil, templateSecretComponent(url, false))
	assertNoSentinel(t, err)
	for _, want := range []string{`helmtemplate "podinfo": rendering chart with secretValues failed, and without them it renders`, "the cause is withheld"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

// lockedBuffer is a buffer several goroutines may write to.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *lockedBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

// captureProcessLog sends the standard logger and the default slog logger to
// one buffer for the rest of t and returns it, empty. Helm prints its values
// warnings through the first and logs through the second. It checks that a line
// written through each arrives, so an empty capture later means nothing was
// logged.
//
// The limit: it does not see what a dependency writes to the standard error
// stream directly. Nothing on the render path does so today.
func captureProcessLog(t *testing.T) *lockedBuffer {
	t.Helper()
	captured := &lockedBuffer{}
	writer, flags, logger := log.Writer(), log.Flags(), slog.Default()
	t.Cleanup(func() {
		slog.SetDefault(logger)
		log.SetOutput(writer)
		log.SetFlags(flags)
	})
	// SetDefault points the standard logger at the slog handler; SetOutput then
	// points it at the buffer itself, so each writes there on its own.
	slog.SetDefault(slog.New(slog.NewTextHandler(captured, &slog.HandlerOptions{Level: slog.LevelDebug})))
	log.SetOutput(captured)

	log.Print("probe-standard-logger")
	slog.Debug("probe-slog-logger")
	for _, probe := range []string{"probe-standard-logger", "probe-slog-logger"} {
		if !strings.Contains(captured.String(), probe) {
			t.Fatalf("the capture does not see %s", probe)
		}
	}
	captured.Reset()
	return captured
}

// subchartFiles are the files of a chart with one subchart, child, each
// rendering a ConfigMap that reads no value. childDefaults, when set, is the
// subchart's values.yaml.
func subchartFiles(childDefaults string) map[string]string {
	files := map[string]string{
		"templates/cm.yaml":              "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: parent\n",
		"charts/child/Chart.yaml":        "apiVersion: v2\nname: child\nversion: 0.1.0\n",
		"charts/child/templates/cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: child\n",
	}
	if childDefaults != "" {
		files["charts/child/values.yaml"] = childDefaults
	}
	return files
}

// authPassword is the values object {auth: {password: leaf}}.
func authPassword(leaf any) map[string]any {
	return map[string]any{"auth": map[string]any{"password": leaf}}
}

// templateOnChart points a helm component's properties at the chart served at
// url, under delivery: template, with the given values and secretValues; a nil
// tree removes the property.
func templateOnChart(url string, values, secretValues map[string]any) func(map[string]any) {
	return func(p map[string]any) {
		p["delivery"] = "template"
		p["chart"] = "secretchart"
		p["version"] = "0.1.0"
		p["source"] = map[string]any{"url": url}
		delete(p, "valuesFrom")
		for key, tree := range map[string]map[string]any{"values": values, "secretValues": secretValues} {
			if tree == nil {
				delete(p, key)
				continue
			}
			p[key] = tree
		}
	}
}

// TestHelmSecretValues_NestedGlobalRefused: secretValues that give a subchart a
// `global` entry are refused before any render, for the helm component under
// either delivery and for the helmtemplate component (go-kure/launcher#794,
// item 9). Helm would drop such an entry where its shape conflicts with the
// parent's `global` and print it in a warning; the control case of
// TestHelmSecretValues_AllowedShapesPrintNoValue shows that line. The refusal
// names the path, the chart repository is never asked for anything, and neither
// the error nor the process's log carries the value.
func TestHelmSecretValues_NestedGlobalRefused(t *testing.T) {
	url, requests := countingSecretChartRepo(t, subchartFiles(""))
	values := map[string]any{"global": authPassword(map[string]any{"placeholder": true})}
	nested := map[string]any{"child": map[string]any{"global": authPassword(secretSentinel)}}

	cases := []struct {
		name  string
		owner string
		comp  oam.Component
	}{
		{"helm, delivery: template", "helm", helmSecretComponent(secretSentinel, templateOnChart(url, values, nested))},
		{"helm, delivery: flux", "helm", helmSecretComponent(secretSentinel, func(p map[string]any) {
			p["values"] = values
			p["secretValues"] = nested
		})},
		{"helm, delivery: flux, values in a ConfigMap", "helm", helmSecretComponent(secretSentinel, func(p map[string]any) {
			p["valuesMode"] = "configMap"
			p["values"] = values
			p["secretValues"] = nested
		})},
		{"helmtemplate", "helmtemplate", oam.Component{Name: "podinfo", Type: "helmtemplate", Properties: map[string]any{
			"chart":        "secretchart",
			"version":      "0.1.0",
			"source":       map[string]any{"url": url},
			"values":       values,
			"secretValues": nested,
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logged := captureProcessLog(t)
			_, err := generateSecretApp("", nil, tc.comp)
			assertNoSentinel(t, err)
			want := tc.owner + ": secretValues: child.global: a key named global is allowed only at the top level"
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q lacks %q", err, want)
			}
			if strings.Contains(logged.String(), secretSentinel) {
				t.Errorf("the process's log carries the sensitive value")
			}
		})
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("the chart repository served %d requests, want none: the refusal comes before any render", n)
	}
}

// TestHelmSecretValues_AllowedShapesPrintNoValue: the two shapes of
// secretValues that can meet a Helm values conflict and stay allowed, a
// top-level `global` and a subchart's ordinary key, never have their value
// printed, whichever side of the conflict holds the table
// (go-kure/launcher#794, item 9). Helm's warning quotes the value it drops,
// which in each case is the plain one: a subchart's `global` from values, or
// the subchart's own default. Each case checks that the warning was printed and
// quotes the plain value, so the conflict did occur.
//
// The control is the shape launcher refuses in secretValues
// (TestHelmSecretValues_NestedGlobalRefused), given in plain values instead:
// there Helm prints the subchart's `global` entry, which is why it is refused.
//
// The log is read as captureProcessLog captures it, with that helper's limit:
// a dependency's direct write to the standard error stream is not seen, and
// none exists on this path today.
func TestHelmSecretValues_AllowedShapesPrintNoValue(t *testing.T) {
	const plain = "plain-marker-value"
	cases := []struct {
		name          string
		childDefaults string
		values        map[string]any
		secretValues  map[string]any
		wantLogged    string
		control       bool
	}{
		{
			name: "control: a subchart's global in plain values is printed",
			values: map[string]any{
				"global": authPassword(map[string]any{"placeholder": true}),
				"child":  map[string]any{"global": authPassword(secretSentinel)},
			},
			wantLogged: "is a table. Ignoring non-table value (" + secretSentinel + ")",
			control:    true,
		},
		{
			name:         "top-level global, a plain value against the subchart's table",
			values:       map[string]any{"child": map[string]any{"global": authPassword(map[string]any{"marker": plain})}},
			secretValues: map[string]any{"global": authPassword(secretSentinel)},
			wantLogged:   "cannot overwrite table with non table for",
		},
		{
			name:         "top-level global, a table against the subchart's plain value",
			values:       map[string]any{"child": map[string]any{"global": authPassword(plain)}},
			secretValues: map[string]any{"global": authPassword(map[string]any{"value": secretSentinel})},
			wantLogged:   "is a table. Ignoring non-table value (" + plain + ")",
		},
		{
			name:          "a subchart's key, a plain value against its default table",
			childDefaults: "auth:\n  password:\n    marker: " + plain + "\n",
			secretValues:  map[string]any{"child": authPassword(secretSentinel)},
			wantLogged:    "cannot overwrite table with non table for",
		},
		{
			name:          "a subchart's key, a table against its default plain value",
			childDefaults: "auth:\n  password: " + plain + "\n",
			secretValues:  map[string]any{"child": authPassword(map[string]any{"value": secretSentinel})},
			wantLogged:    "is a table. Ignoring non-table value (" + plain + ")",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url := secretChartRepo(t, subchartFiles(tc.childDefaults))
			logged := captureProcessLog(t)
			apps, err := generateSecretApp("", nil, helmSecretComponent(secretSentinel, templateOnChart(url, tc.values, tc.secretValues)))
			if err != nil {
				if strings.Contains(err.Error(), secretSentinel) {
					t.Fatal("the build failed with an error carrying the sensitive value")
				}
				t.Fatalf("transform: %v", err)
			}
			output := logged.String()
			if !strings.Contains(output, tc.wantLogged) {
				t.Fatalf("the log lacks Helm's warning %q, so the conflict did not occur:\n%s", tc.wantLogged, strings.ReplaceAll(output, secretSentinel, "<value>"))
			}
			if tc.control {
				return
			}
			if !strings.Contains(output, plain) {
				t.Errorf("Helm's warning does not quote the plain value:\n%s", strings.ReplaceAll(output, secretSentinel, "<value>"))
			}
			if strings.Contains(output, secretSentinel) {
				t.Errorf("the process's log carries the sensitive value:\n%s", strings.ReplaceAll(output, secretSentinel, "<value>"))
			}
			for _, a := range apps {
				if strings.Contains(a.String(), secretSentinel) {
					t.Errorf("GeneratedApplication %s names the sensitive value", a)
				}
				for _, o := range a.Objects {
					if written := objectYAML(t, *o); strings.Contains(written, secretSentinel) {
						t.Errorf("%T carries the sensitive value:\n%s", *o, written)
					}
				}
			}
		})
	}
}
