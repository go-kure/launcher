package kurel

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// reservedKeyCarriers is one document per way a label or annotation key reaches
// the output today, through the built-in handlers and rules. %[1]s is the key
// under test, and %[2]s the chart repository's URL where the document has one.
// Each names the component, the object and the place the key lands in.
var reservedKeyCarriers = map[string]struct {
	component string // the document's one component, named "carrier"
	object    string
	what      string
}{
	"a kind component's label": {
		component: `    - name: carrier
      type: configmap
      properties:
        data:
          k: v
        labels:
          %[1]s: a
`,
		object: `ConfigMap "carrier"`, what: "label",
	},
	"a kind component's annotation, on a kind that writes none": {
		component: `    - name: carrier
      type: storageclass
      properties:
        provisioner: csi.example.com
        annotations:
          %[1]s: a
`,
		object: `StorageClass "carrier"`, what: "annotation",
	},
	"a passthrough object's label": {
		component: `    - name: carrier
      type: passthrough
      properties:
        object:
          apiVersion: v1
          kind: ConfigMap
          metadata:
            name: settings
            labels:
              %[1]s: a
`,
		object: `ConfigMap "settings"`, what: "label",
	},
	"a passthrough workload's pod template annotation": {
		component: `    - name: carrier
      type: passthrough
      properties:
        object:
          apiVersion: apps/v1
          kind: Deployment
          metadata:
            name: worker
          spec:
            selector:
              matchLabels:
                role: worker
            template:
              metadata:
                labels:
                  role: worker
                annotations:
                  %[1]s: a
              spec:
                containers:
                  - name: app
                    image: ghcr.io/example/worker:v1.0.0
`,
		object: `Deployment "worker"`, what: "pod template annotation",
	},
	"a manifests object's annotation": {
		component: `    - name: carrier
      type: manifests
      properties:
        inline: |
          apiVersion: v1
          kind: ConfigMap
          metadata:
            name: plain
            namespace: default
          ---
          apiVersion: v1
          kind: ConfigMap
          metadata:
            name: tagged
            namespace: default
            annotations:
              %[1]s: a
`,
		object: `ConfigMap "tagged"`, what: "annotation",
	},
	"a rendered chart's pod template label": {
		component: `    - name: carrier
      type: helm
      properties:
        delivery: template
        chart: testchart
        version: "0.1.0"
        source:
          url: %[2]s
        values:
          key: %[1]s
`,
		object: `Deployment "web"`, what: "pod template label",
	},
	"an ingress trait's annotation": {
		component: `    - name: carrier
      type: webservice
      properties:
        image: ghcr.io/example/web:v1.0.0
        port: 8080
      traits:
        - type: ingress
          properties:
            rules:
              - host: shop.example.com
                paths:
                  - path: /
            annotations:
              %[1]s: a
`,
		object: `Ingress "`, what: "annotation",
	},
	"an httproute trait's annotation": {
		component: `    - name: carrier
      type: webservice
      properties:
        image: ghcr.io/example/web:v1.0.0
        port: 8080
      traits:
        - type: httproute
          properties:
            parentRefs:
              - name: my-gateway
            rules:
              - matches:
                  - path:
                      type: PathPrefix
                      value: /
            annotations:
              %[1]s: a
`,
		object: `HTTPRoute "`, what: "annotation",
	},
	"an expose trait's annotation": {
		component: `    - name: carrier
      type: webservice
      properties:
        image: ghcr.io/example/web:v1.0.0
        port: 8080
      traits:
        - type: expose
          properties:
            hostnames: [shop.example.com]
            annotations:
              %[1]s: a
`,
		object: `Ingress "`, what: "annotation",
	},
	"a postgresql component's inherited label": {
		component: `    - name: carrier
      type: postgresql
      properties:
        version: "16"
        storageSize: 10Gi
        inheritedMetadata:
          labels:
            %[1]s: a
`,
		object: `Cluster "carrier"`, what: "spec.inheritedMetadata label",
	},
	"a cnpg-cluster component's inherited annotation": {
		component: `    - name: carrier
      type: cnpg-cluster
      properties:
        instances: 1
        imageName: ghcr.io/cloudnative-pg/postgresql:17.2
        storage:
          size: 1Gi
        inheritedMetadata:
          annotations:
            %[1]s: a
`,
		object: `Cluster "carrier"`, what: "spec.inheritedMetadata annotation",
	},
}

// TestBuiltinTraits_SubApplicationDecoratorsLeaveTheConfig: the built-in traits
// that also cover a component's sub-applications (oam.SubApplicationDecorator)
// are prune-protection and force-replace, and neither puts a config of its own
// around the application it is handed. So the Ingress sub-application of an
// `ingress` or `expose` trait keeps the config that states the platform's
// annotations to the reserved-key check, with nothing between it and the
// ownership wrapper. A built-in added to that set fails here, to be held to the
// same: a config it wraps must stay reachable (oam.ConfigWrapper).
func TestBuiltinTraits_SubApplicationDecoratorsLeaveTheConfig(t *testing.T) {
	var decorating []string
	for name, h := range builtinTraitHandlers() {
		d, ok := h.(oam.SubApplicationDecorator)
		if !ok || !d.DecoratesSubApplications() {
			continue
		}
		decorating = append(decorating, name)

		cfg := &traits.IngressConfig{}
		sub := stack.NewApplication("web-ingress", "default", cfg)
		if err := h.Apply(&oam.Trait{Type: name}, sub, &stack.Bundle{}); err != nil {
			t.Errorf("%s: Apply on a sub-application: %v", name, err)
		}
		if sub.Config != stack.ApplicationConfig(cfg) {
			t.Errorf("%s: the sub-application's config is %T after Apply, want the one it was handed", name, sub.Config)
		}
	}
	slices.Sort(decorating)
	if want := []string{"force-replace", "prune-protection"}; !slices.Equal(decorating, want) {
		t.Errorf("built-in traits that cover sub-applications = %v, want %v", decorating, want)
	}
}

// TestReservedMetadataKeys_EveryCarrier is the one rule of
// go-kure/launcher#790 held against each way a key reaches the output: a key
// the consumer reserved (TransformContext.ReservedMetadataKeys) is refused
// whatever component type or trait carries it, naming the component, the
// object, the key and the reserved entry. The same document with a key that is
// not reserved builds, so the refusal is the key's and not the fixture's.
func TestReservedMetadataKeys_EveryCarrier(t *testing.T) {
	// The chart puts the key its values name on the pod template of its
	// Deployment: a key no launcher code wrote and no property of the document
	// states as metadata.
	const pod = "      containers:\n        - name: app\n          image: ghcr.io/example/web:v1.0.0\n"
	chart := buildMinimalChartTar(t, "testchart", "0.1.0", map[string]string{
		"testchart/templates/deployment.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n" +
			"  selector:\n    matchLabels:\n      app: web\n  template:\n    metadata:\n      labels:\n        app: web\n" +
			"        {{ .Values.key }}: a\n    spec:\n" + pod,
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.yaml":
			fmt.Fprint(w, helmIndexYAML("testchart", "0.1.0", "http://"+r.Host+"/testchart-0.1.0.tgz"))
		case "/testchart-0.1.0.tgz":
			_, _ = w.Write(chart)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	generate := func(t *testing.T, component, key string) error {
		t.Helper()
		doc := "apiVersion: launcher.gokure.dev/v1alpha1\nkind: Application\nmetadata:\n  name: shop\n  namespace: default\nspec:\n  components:\n" +
			fmt.Sprintf(component, key, srv.URL)
		transformer := newBuiltinTransformer()
		app, err := oam.ParseWithExtraTypes([]byte(doc), nil, transformer.LowerableTypes())
		if err != nil {
			t.Fatalf("parsing: %v\n%s", err, doc)
		}
		if err := transformer.ValidateAuthoredProperties(app); err != nil {
			t.Fatalf("validating: %v", err)
		}
		cluster, err := transformer.Transform(app, oam.TransformContext{
			Domain:               kurelDomain,
			ReservedMetadataKeys: []string{"platform.example/", "example.org/tenant"},
			Capabilities: map[string]oam.CapabilityBinding{"expose": {Rendering: map[string]any{
				"controllerType": "ingress", "ingressClassName": "nginx",
			}}},
		})
		if err != nil {
			return err
		}
		_, err = oam.GenerateApplications(cluster)
		return err
	}

	for name, tc := range reservedKeyCarriers {
		t.Run(name, func(t *testing.T) {
			if err := generate(t, tc.component, "example.com/owner"); err != nil {
				t.Fatalf("with a key that is not reserved: %v", err)
			}
			for key, reserved := range map[string]struct{ entry, reason string }{
				"platform.example/zone": {"platform.example/", `the prefix "platform.example/" is reserved for the platform`},
				"example.org/tenant":    {"example.org/tenant", "the key is reserved for the platform"},
			} {
				err := generate(t, tc.component, key)
				if !errors.Is(err, oam.ErrReservedMetadataKey) {
					t.Fatalf("with %s: %v, want ErrReservedMetadataKey", key, err)
				}
				for _, want := range []string{`component "carrier"`, tc.object, tc.what + ` "` + key + `"`, reserved.reason} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("with %s: refusal %q does not say %s", key, err, want)
					}
				}
				if strings.Contains(err.Error(), "TransformContext") {
					t.Errorf("with %s: refusal %q names a Go field", key, err)
				}

				// The same, as errors.As finds it: what tc.what says in words.
				var got *oam.ReservedMetadataKeyError
				if !errors.As(err, &got) {
					t.Fatalf("with %s: %v, want a *oam.ReservedMetadataKeyError", key, err)
				}
				field, annotation := strings.CutSuffix(tc.what, "annotation")
				holder := oam.ReservedKeyHolder(strings.TrimSpace(strings.TrimSuffix(field, "label")))
				if got.Component != "carrier" || !strings.HasPrefix(got.Object, tc.object) ||
					got.Holder != holder || got.Annotation != annotation || got.Key != key || got.Entry != reserved.entry {
					t.Errorf("with %s: the refusal is %+v, want component carrier, %s, holder %q, annotation %v and entry %s",
						key, *got, tc.object, holder, annotation, reserved.entry)
				}
				if got.Kind.Kind == "" || got.Name == "" || !strings.HasPrefix(got.Object, got.Kind.Kind+` "`+got.Name) {
					t.Errorf("with %s: the refusal's kind %v and name %q are not the object %s", key, got.Kind, got.Name, got.Object)
				}
				if !strings.HasSuffix(err.Error(), got.Error()) {
					t.Errorf("with %s: refusal %q does not end with what errors.As finds: %s", key, err, got)
				}
			}
		})
	}

	// The whole text, as the document's author reads it, for a key on an object's
	// own metadata and for one on a pod template's.
	const reason = ` may not be set: the prefix "platform.example/" is reserved for the platform: oam: metadata key is reserved`
	for carrier, want := range map[string]string{
		"a kind component's label":                         `component "carrier": ConfigMap "carrier": label "platform.example/team"` + reason,
		"a passthrough workload's pod template annotation": `component "carrier": Deployment "worker": pod template annotation "platform.example/team"` + reason,
	} {
		t.Run("the whole text of "+carrier, func(t *testing.T) {
			err := generate(t, reservedKeyCarriers[carrier].component, "platform.example/team")
			if err == nil || err.Error() != want {
				t.Errorf("refusal = %v\nwant      %s", err, want)
			}
		})
	}
}

// TestReservedMetadataKeys_NullEntryIsAbsent: an annotation authored with a
// null value is absent (go-kure/launcher#790), under a key the consumer
// reserved like under any other. The object carries no such key, so nothing is
// refused; the same key with a value is. Held for the two traits whose
// annotations map is read entry by entry.
func TestReservedMetadataKeys_NullEntryIsAbsent(t *testing.T) {
	const reserved = "platform.example/zone"
	carriers := map[string]struct {
		kind  string
		trait string // %s is the value authored under the reserved key
	}{
		"an ingress trait's annotation": {kind: "Ingress", trait: `        - type: ingress
          properties:
            rules:
              - host: shop.example.com
                paths:
                  - path: /
            annotations:
              platform.example/zone: %s
              example.com/team: checkout
`},
		"an httproute trait's annotation": {kind: "HTTPRoute", trait: `        - type: httproute
          properties:
            parentRefs:
              - name: my-gateway
            rules:
              - matches:
                  - path:
                      type: PathPrefix
                      value: /
            annotations:
              platform.example/zone: %s
              example.com/team: checkout
`},
	}

	generate := func(t *testing.T, trait, value string) ([]oam.GeneratedApplication, error) {
		t.Helper()
		doc := "apiVersion: launcher.gokure.dev/v1alpha1\nkind: Application\nmetadata:\n  name: shop\n  namespace: default\nspec:\n  components:\n" +
			"    - name: carrier\n      type: webservice\n      properties:\n        image: ghcr.io/example/web:v1.0.0\n        port: 8080\n      traits:\n" +
			fmt.Sprintf(trait, value)
		transformer := newBuiltinTransformer()
		app, err := oam.ParseWithExtraTypes([]byte(doc), nil, transformer.LowerableTypes())
		if err != nil {
			t.Fatalf("parsing: %v\n%s", err, doc)
		}
		if err := transformer.ValidateAuthoredProperties(app); err != nil {
			t.Fatalf("validating: %v", err)
		}
		cluster, err := transformer.Transform(app, oam.TransformContext{
			Domain:               kurelDomain,
			ReservedMetadataKeys: []string{"platform.example/"},
		})
		if err != nil {
			return nil, err
		}
		return oam.GenerateApplications(cluster)
	}

	for name, tc := range carriers {
		t.Run(name, func(t *testing.T) {
			if _, err := generate(t, tc.trait, "a"); !errors.Is(err, oam.ErrReservedMetadataKey) {
				t.Fatalf("with a value under %s: %v, want ErrReservedMetadataKey", reserved, err)
			}

			generated, err := generate(t, tc.trait, "null")
			if err != nil {
				t.Fatalf("with a null under %s: %v, want the entry absent and nothing refused", reserved, err)
			}
			found := false
			for _, app := range generated {
				for _, obj := range app.Objects {
					if (*obj).GetObjectKind().GroupVersionKind().Kind != tc.kind {
						continue
					}
					found = true
					annotations := (*obj).GetAnnotations()
					if v, written := annotations[reserved]; written {
						t.Errorf("%s carries %s: %q; a null entry is absent", tc.kind, reserved, v)
					}
					if v := annotations["example.com/team"]; v != "checkout" {
						t.Errorf("%s carries example.com/team: %q, want %q", tc.kind, v, "checkout")
					}
				}
			}
			if !found {
				t.Fatalf("no %s among the generated objects", tc.kind)
			}
		})
	}
}
