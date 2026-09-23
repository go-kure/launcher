package components_test

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// These tests cover go-kure/launcher#321: an `initContainers`/`sidecars` entry
// read the keys its parser knew and dropped the rest, so an authored
// `workingDir`, `envFrom`, `probes` or `lifecycle` built cleanly and reached
// no container. The entry is now a closed key set: the four fields render
// where Kubernetes accepts them, `probes`/`lifecycle` on an init container are
// refused by name (Kubernetes forbids both there), and any other unrecognized
// key is an error instead of a silent drop.

// sidecarKinds are the workload kinds that publish a `sidecars` key at all.
var sidecarKinds = map[string]bool{"webservice": true, "worker": true, "statefulset": true, "deployment": true}

func containerNamed(t *testing.T, list []corev1.Container, name string) corev1.Container {
	t.Helper()
	for _, c := range list {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no container named %q in %d containers", name, len(list))
	return corev1.Container{}
}

// TestWorkloadKinds_InitContainer_WorkingDirAndEnvFromRender: both fields reach
// the rendered init container on every kind.
func TestWorkloadKinds_InitContainer_WorkingDirAndEnvFromRender(t *testing.T) {
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			props := withProps(k.props, map[string]any{
				"initContainers": []any{map[string]any{
					"name":       "init",
					"image":      "ghcr.io/org/init:v1",
					"workingDir": "/work",
					"envFrom": []any{
						map[string]any{"prefix": "CFG_", "configMapRef": map[string]any{"name": "settings"}},
					},
				}},
			})
			ps := podTemplateSpec(t, generateKind(t, k.handler, k.name, props))
			ic := containerNamed(t, ps.InitContainers, "init")
			if ic.WorkingDir != "/work" {
				t.Errorf("init WorkingDir = %q, want /work", ic.WorkingDir)
			}
			if len(ic.EnvFrom) != 1 || ic.EnvFrom[0].Prefix != "CFG_" ||
				ic.EnvFrom[0].ConfigMapRef == nil || ic.EnvFrom[0].ConfigMapRef.Name != "settings" {
				t.Errorf("init EnvFrom = %+v, want one configMapRef settings with prefix CFG_", ic.EnvFrom)
			}
		})
	}
}

// sidecarAllFields authors every one of the four fields on one sidecar, with a
// probe and a lifecycle hook addressing the sidecar's own named port.
func sidecarAllFields() map[string]any {
	return map[string]any{
		"name":       "proxy",
		"image":      "ghcr.io/org/proxy:v1",
		"ports":      []any{map[string]any{"name": "admin", "containerPort": 9901}},
		"workingDir": "/srv",
		"envFrom":    []any{map[string]any{"secretRef": map[string]any{"name": "proxy-creds", "optional": true}}},
		"probes": map[string]any{
			"readiness": map[string]any{"httpGet": map[string]any{"port": "admin", "path": "/ready"}},
			"liveness":  map[string]any{"tcpSocket": map[string]any{"port": 9901}},
			"startup":   map[string]any{"exec": map[string]any{"command": []any{"true"}}, "failureThreshold": 30},
		},
		"lifecycle": map[string]any{
			"preStop": map[string]any{"httpGet": map[string]any{"port": "admin", "path": "/drain"}},
		},
	}
}

// TestWorkloadKinds_Sidecar_AllFourFieldsRender: workingDir, envFrom, probes and
// lifecycle reach the rendered sidecar on every kind that has sidecars.
func TestWorkloadKinds_Sidecar_AllFourFieldsRender(t *testing.T) {
	for _, k := range workloadKinds {
		if !sidecarKinds[k.name] {
			continue
		}
		t.Run(k.name, func(t *testing.T) {
			props := withProps(k.props, map[string]any{"sidecars": []any{sidecarAllFields()}})
			ps := podTemplateSpec(t, generateKind(t, k.handler, k.name, props))
			sc := containerNamed(t, ps.Containers, "proxy")
			if sc.WorkingDir != "/srv" {
				t.Errorf("sidecar WorkingDir = %q, want /srv", sc.WorkingDir)
			}
			if len(sc.EnvFrom) != 1 || sc.EnvFrom[0].SecretRef == nil || sc.EnvFrom[0].SecretRef.Name != "proxy-creds" ||
				sc.EnvFrom[0].SecretRef.Optional == nil || !*sc.EnvFrom[0].SecretRef.Optional {
				t.Errorf("sidecar EnvFrom = %+v, want one optional secretRef proxy-creds", sc.EnvFrom)
			}
			if sc.ReadinessProbe == nil || sc.ReadinessProbe.HTTPGet == nil ||
				sc.ReadinessProbe.HTTPGet.Port != intstr.FromString("admin") || sc.ReadinessProbe.HTTPGet.Path != "/ready" {
				t.Errorf("sidecar ReadinessProbe = %+v, want httpGet admin /ready", sc.ReadinessProbe)
			}
			if sc.LivenessProbe == nil || sc.LivenessProbe.TCPSocket == nil || sc.LivenessProbe.TCPSocket.Port != intstr.FromInt32(9901) {
				t.Errorf("sidecar LivenessProbe = %+v, want tcpSocket 9901", sc.LivenessProbe)
			}
			if sc.StartupProbe == nil || sc.StartupProbe.Exec == nil || sc.StartupProbe.FailureThreshold != 30 {
				t.Errorf("sidecar StartupProbe = %+v, want exec with failureThreshold 30", sc.StartupProbe)
			}
			if sc.Lifecycle == nil || sc.Lifecycle.PreStop == nil || sc.Lifecycle.PreStop.HTTPGet == nil ||
				sc.Lifecycle.PreStop.HTTPGet.Port != intstr.FromString("admin") {
				t.Errorf("sidecar Lifecycle = %+v, want preStop httpGet on admin", sc.Lifecycle)
			}
			// The main container keeps its own (unauthored) probe and hook state.
			if main := ps.Containers[0]; main.ReadinessProbe != nil || main.Lifecycle != nil || main.WorkingDir != "" {
				t.Errorf("sidecar fields leaked onto the main container: %+v", main)
			}
		})
	}
}

// TestSidecar_UnauthoredFieldsStayZero: a sidecar authoring none of the four
// renders none of them — no zero-value probe, hook or empty slice.
func TestSidecar_UnauthoredFieldsStayZero(t *testing.T) {
	props := map[string]any{
		"image":    "ghcr.io/org/app:v1",
		"sidecars": []any{map[string]any{"name": "proxy", "image": "ghcr.io/org/proxy:v1"}},
	}
	ps := podTemplateSpec(t, generateKind(t, &components.WebserviceHandler{}, "webservice", props))
	sc := containerNamed(t, ps.Containers, "proxy")
	if sc.WorkingDir != "" || sc.EnvFrom != nil || sc.ReadinessProbe != nil || sc.LivenessProbe != nil ||
		sc.StartupProbe != nil || sc.Lifecycle != nil {
		t.Errorf("unauthored sidecar carries fields: %+v", sc)
	}
}

// TestExtraContainer_NullFieldIsAbsent: an explicit null on any of the four is
// absence, per the package-wide null contract, not an error and not a value.
func TestExtraContainer_NullFieldIsAbsent(t *testing.T) {
	entry := func() map[string]any {
		return map[string]any{"name": "x", "image": "ghcr.io/org/x:v1", "workingDir": nil, "envFrom": nil}
	}
	sidecar := entry()
	sidecar["probes"] = nil
	sidecar["lifecycle"] = nil
	props := map[string]any{
		"image":          "ghcr.io/org/app:v1",
		"initContainers": []any{entry()},
		"sidecars":       []any{sidecar},
	}
	ps := podTemplateSpec(t, generateKind(t, &components.WebserviceHandler{}, "webservice", props))
	for _, c := range []corev1.Container{ps.InitContainers[0], containerNamed(t, ps.Containers, "x")} {
		if c.WorkingDir != "" || c.EnvFrom != nil || c.ReadinessProbe != nil || c.Lifecycle != nil {
			t.Errorf("null-authored container carries fields: %+v", c)
		}
	}
}

// TestExtraContainer_Errors: every refusal names the list position and the
// container, so the author can find the line.
func TestExtraContainer_Errors(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		entry map[string]any
		want  []string
	}{
		{
			name:  "init probes forbidden",
			key:   "initContainers",
			entry: map[string]any{"probes": map[string]any{"liveness": map[string]any{"exec": map[string]any{"command": []any{"true"}}}}},
			want:  []string{`initContainers[0] "c"`, "probes", "init container"},
		},
		{
			name:  "init lifecycle forbidden",
			key:   "initContainers",
			entry: map[string]any{"lifecycle": map[string]any{"preStop": map[string]any{"sleep": map[string]any{"seconds": 5}}}},
			want:  []string{`initContainers[0] "c"`, "lifecycle", "init container"},
		},
		{
			name:  "init unknown key",
			key:   "initContainers",
			entry: map[string]any{"imagePullPolicy": "Always"},
			want:  []string{`initContainers[0] "c"`, `unrecognized key "imagePullPolicy"`},
		},
		{
			name:  "init ports are not an init container key",
			key:   "initContainers",
			entry: map[string]any{"ports": []any{map[string]any{"containerPort": 80}}},
			want:  []string{`initContainers[0] "c"`, `unrecognized key "ports"`},
		},
		{
			name:  "sidecar unknown key",
			key:   "sidecars",
			entry: map[string]any{"workdir": "/x"},
			want:  []string{`sidecars[0] "c"`, `unrecognized key "workdir"`},
		},
		{
			name:  "init workingDir wrong type",
			key:   "initContainers",
			entry: map[string]any{"workingDir": 3},
			want:  []string{`initContainers[0] "c"`, "workingDir"},
		},
		{
			name:  "sidecar envFrom needs exactly one source",
			key:   "sidecars",
			entry: map[string]any{"envFrom": []any{map[string]any{"prefix": "X_"}}},
			want:  []string{`sidecars[0] "c"`, "envFrom[0]", "exactly one of configMapRef or secretRef"},
		},
		{
			name: "sidecar probe names an undeclared port",
			key:  "sidecars",
			entry: map[string]any{
				"ports":  []any{map[string]any{"name": "admin", "containerPort": 9901}},
				"probes": map[string]any{"readiness": map[string]any{"httpGet": map[string]any{"port": "http"}}},
			},
			want: []string{`sidecars[0] "c"`, `named port "http"`, "admin"},
		},
		{
			name:  "sidecar probe names a port on a sidecar declaring none",
			key:   "sidecars",
			entry: map[string]any{"probes": map[string]any{"liveness": map[string]any{"tcpSocket": map[string]any{"port": "admin"}}}},
			want:  []string{`sidecars[0] "c"`, `named port "admin"`},
		},
		{
			name: "sidecar lifecycle names an undeclared port",
			key:  "sidecars",
			entry: map[string]any{
				"ports":     []any{map[string]any{"name": "admin", "containerPort": 9901}},
				"lifecycle": map[string]any{"preStop": map[string]any{"httpGet": map[string]any{"port": "metrics"}}},
			},
			want: []string{`sidecars[0] "c"`, `named port "metrics"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := map[string]any{"name": "c", "image": "ghcr.io/org/c:v1"}
			for k, v := range tc.entry {
				entry[k] = v
			}
			_, err := (&components.WebserviceHandler{}).ToApplicationConfig(&oam.Component{
				Name: "app", Type: "webservice",
				Properties: map[string]any{"image": "ghcr.io/org/app:v1", tc.key: []any{entry}},
			}, "default")
			if err == nil {
				t.Fatalf("expected an error, got none")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err.Error(), w)
				}
			}
		})
	}
}

// TestSidecar_NamedPortResolvesAgainstAnyDeclaredPort: a sidecar declaring
// several named ports may point each probe at any one of them.
func TestSidecar_NamedPortResolvesAgainstAnyDeclaredPort(t *testing.T) {
	props := map[string]any{
		"image": "ghcr.io/org/app:v1",
		"sidecars": []any{map[string]any{
			"name":  "proxy",
			"image": "ghcr.io/org/proxy:v1",
			"ports": []any{
				map[string]any{"name": "admin", "containerPort": 9901},
				map[string]any{"name": "stats", "containerPort": 9902},
			},
			"probes": map[string]any{
				"readiness": map[string]any{"httpGet": map[string]any{"port": "stats"}},
				"liveness":  map[string]any{"httpGet": map[string]any{"port": "admin"}},
			},
		}},
	}
	ps := podTemplateSpec(t, generateKind(t, &components.WebserviceHandler{}, "webservice", props))
	sc := containerNamed(t, ps.Containers, "proxy")
	if sc.ReadinessProbe.HTTPGet.Port != intstr.FromString("stats") || sc.LivenessProbe.HTTPGet.Port != intstr.FromString("admin") {
		t.Errorf("probe ports = %v / %v, want stats / admin", sc.ReadinessProbe.HTTPGet.Port, sc.LivenessProbe.HTTPGet.Port)
	}
}

// TestWorkloadKinds_ContainerEntrySchemaIsClosed: every kind publishes a closed
// `initContainers` entry declaring workingDir and envFrom but not probes or
// lifecycle, and — where it has sidecars — a closed `sidecars` entry declaring
// all four.
func TestWorkloadKinds_ContainerEntrySchemaIsClosed(t *testing.T) {
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			schema := k.handler.(oam.PropertySchemaProvider).PropertySchema()
			check := func(key string, want, notWant []string) {
				t.Helper()
				field, ok := schema[key]
				if !ok || field.Items == nil {
					t.Fatalf("%s: no item schema", key)
				}
				if field.Items.AdditionalProperties {
					t.Errorf("%s entry schema is open (AdditionalProperties: true)", key)
				}
				for _, w := range want {
					if _, ok := field.Items.Properties[w]; !ok {
						t.Errorf("%s entry schema does not declare %q", key, w)
					}
				}
				for _, n := range notWant {
					if _, ok := field.Items.Properties[n]; ok {
						t.Errorf("%s entry schema declares %q", key, n)
					}
				}
			}
			check("initContainers", []string{"workingDir", "envFrom", "securityContext"}, []string{"probes", "lifecycle", "ports"})
			if sidecarKinds[k.name] {
				check("sidecars", []string{"workingDir", "envFrom", "probes", "lifecycle", "ports", "securityContext"}, nil)
			} else if _, ok := schema["sidecars"]; ok {
				t.Errorf("%s publishes sidecars", k.name)
			}
		})
	}
}

// TestSidecar_RenderingTwiceIsUnaffectedByEditingTheFirstRender: the probe,
// hook and envFrom a sidecar renders are copies, per the reuse contract in
// render_reuse_aliasing_test.go — editing them through the first render's
// nested pointers must not reach the second.
func TestSidecar_RenderingTwiceIsUnaffectedByEditingTheFirstRender(t *testing.T) {
	initEntry := map[string]any{
		"name":    "init",
		"image":   "ghcr.io/org/init:v1",
		"envFrom": []any{map[string]any{"configMapRef": map[string]any{"name": "settings", "optional": false}}},
	}
	props := map[string]any{
		"image":          "ghcr.io/org/app:v1",
		"initContainers": []any{initEntry},
		"sidecars":       []any{sidecarAllFields()},
	}
	second := renderTwice(t, &components.WebserviceHandler{}, "webservice", props, func(objects []*client.Object) {
		ps := podTemplateSpec(t, objects)
		sc := containerNamed(t, ps.Containers, "proxy")
		sc.ReadinessProbe.HTTPGet.Path = "/edited"
		sc.Lifecycle.PreStop.HTTPGet.Path = "/edited"
		*sc.EnvFrom[0].SecretRef.Optional = false
		*ps.InitContainers[0].EnvFrom[0].ConfigMapRef.Optional = true
	})
	ps := podTemplateSpec(t, second)
	sc := containerNamed(t, ps.Containers, "proxy")
	if sc.ReadinessProbe.HTTPGet.Path != "/ready" {
		t.Errorf("readiness path = %q after editing the first render, want /ready", sc.ReadinessProbe.HTTPGet.Path)
	}
	if sc.Lifecycle.PreStop.HTTPGet.Path != "/drain" {
		t.Errorf("preStop path = %q after editing the first render, want /drain", sc.Lifecycle.PreStop.HTTPGet.Path)
	}
	if !*sc.EnvFrom[0].SecretRef.Optional {
		t.Error("sidecar envFrom optional flipped by editing the first render")
	}
	if *ps.InitContainers[0].EnvFrom[0].ConfigMapRef.Optional {
		t.Error("init envFrom optional flipped by editing the first render")
	}
}

// authoredApp wraps one webservice component for the authored-property check
// `kurel build` runs before any handler sees the document.
func authoredApp(props map[string]any) *oam.Application {
	return &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{
		{Name: "web", Type: "webservice", Properties: props},
	}}}
}

func authoredTransformer() *oam.Transformer {
	return oam.NewTransformer(map[string]oam.ComponentHandler{"webservice": &components.WebserviceHandler{}}, nil)
}

// TestContainerEntry_AuthoredCheckAcceptsTheFullSurface is the control for the
// closed entry schemas: every key the parsers read, in the shapes they accept,
// still passes the authored-property check — including a bare numeric cpu and
// an integer containerPort.
func TestContainerEntry_AuthoredCheckAcceptsTheFullSurface(t *testing.T) {
	common := func(name string) map[string]any {
		return map[string]any{
			"name":            name,
			"image":           "ghcr.io/org/" + name + ":v1",
			"command":         []any{"/bin/run"},
			"args":            []any{"--flag"},
			"env":             []any{map[string]any{"name": "A", "value": "b"}},
			"envFrom":         []any{map[string]any{"configMapRef": map[string]any{"name": "settings"}}},
			"resources":       map[string]any{"requests": map[string]any{"cpu": 0.5, "memory": "64Mi"}},
			"volumeMounts":    []any{map[string]any{"name": "data", "mountPath": "/data", "readOnly": true, "subPath": "x"}},
			"securityContext": map[string]any{"runAsNonRoot": true},
			"workingDir":      "/work",
		}
	}
	sidecar := common("proxy")
	for k, v := range sidecarAllFields() {
		if k != "name" && k != "image" {
			sidecar[k] = v
		}
	}
	sidecar["ports"] = []any{map[string]any{"name": "admin", "containerPort": 9901, "protocol": "TCP"}}
	app := authoredApp(map[string]any{
		"image":          "ghcr.io/org/app:v1",
		"initContainers": []any{common("init")},
		"sidecars":       []any{sidecar},
	})
	if err := authoredTransformer().ValidateAuthoredProperties(app); err != nil {
		t.Fatalf("a document using only declared entry keys must be accepted, got: %v", err)
	}
}

// TestContainerEntry_AuthoredCheckRejectsUndeclaredKeys: the closed entry
// schemas surface an undeclared key at the authored layer, before any parser.
func TestContainerEntry_AuthoredCheckRejectsUndeclaredKeys(t *testing.T) {
	for _, tc := range []struct {
		name, list, key string
		value           any
	}{
		{"init probes", "initContainers", "probes", map[string]any{}},
		{"init lifecycle", "initContainers", "lifecycle", map[string]any{}},
		{"init typo", "initContainers", "workDir", "/x"},
		{"sidecar typo", "sidecars", "envfrom", []any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := map[string]any{"name": "c", "image": "ghcr.io/org/c:v1", tc.key: tc.value}
			app := authoredApp(map[string]any{"image": "ghcr.io/org/app:v1", tc.list: []any{entry}})
			err := authoredTransformer().ValidateAuthoredProperties(app)
			if err == nil {
				t.Fatalf("undeclared %s key %q must be rejected", tc.list, tc.key)
			}
			if !strings.Contains(err.Error(), `unsupported field "`+tc.key+`"`) || !strings.Contains(err.Error(), tc.list) {
				t.Errorf("error = %v, want it to name %s and the key %q", err, tc.list, tc.key)
			}
		})
	}
}
