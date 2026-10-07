package components_test

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func TestWorkerRule_ComponentType(t *testing.T) {
	if got := (components.WorkerRule{}).ComponentType(); got != "worker" {
		t.Errorf("ComponentType() = %q, want worker", got)
	}
}

// TestWorkerRule_PropertySchemaUnchanged pins worker's published property
// schema byte for byte. testdata/worker-property-schema.json was captured from
// the former WorkerHandler.PropertySchema before worker became a lowering rule;
// the move must not change what HandlerSchemas publishes for "worker". Every
// PropertySchema field carries a json tag, so the encoding covers all of it. A
// deliberate change to a schema worker shares updates the capture with it:
// go-kure/launcher#660 closed the sidecar port entry and added its protocol enum;
// go-kure/launcher#790 added the shared container fields (schemaContainerFields)
// to the main container, to an init container and to a sidecar,
// securityContext.windowsOptions to every container, and an init container's
// restartPolicy and restartPolicyRules, and later the exclusive groups of a
// volume, an envFrom entry and a resourceClaims entry; go-kure/launcher#787 added deploymentObjectName and serviceAccountObjectName
// (schemaRoleObjectNames).
func TestWorkerRule_PropertySchemaUnchanged(t *testing.T) {
	want, err := os.ReadFile("testdata/worker-property-schema.json")
	if err != nil {
		t.Fatalf("reading the captured schema: %v", err)
	}
	got, err := json.MarshalIndent(components.WorkerRule{}.PropertySchema(), "", "  ")
	if err != nil {
		t.Fatalf("encoding the schema: %v", err)
	}
	got = append(got, '\n')
	if !bytes.Equal(got, want) {
		t.Errorf("worker's property schema changed (%d bytes, want %d); it must stay byte-identical to the former handler's", len(got), len(want))
	}
}

// lowerWorker runs WorkerRule.LowerComponent and returns its deployment
// member.
func lowerWorker(t *testing.T, comp *oam.Component) oam.Component {
	t.Helper()
	return lowerWorkerMembers(t, comp)[0]
}

// lowerWorkerMembers runs WorkerRule.LowerComponent and returns every member:
// deployment and, unless serviceAccountName was authored, serviceaccount
// (go-kure/launcher#702).
func lowerWorkerMembers(t *testing.T, comp *oam.Component) []oam.Component {
	t.Helper()
	res, err := components.WorkerRule{}.LowerComponent(comp, oam.LoweringContext{})
	if err != nil {
		t.Fatalf("LowerComponent: %v", err)
	}
	want := 2
	if _, authored := comp.Properties["serviceAccountName"]; authored {
		want = 1
	}
	if len(res.Components) != want || len(res.Policies) != 0 || len(res.Traits) != 0 || len(res.Documents) != 0 {
		t.Fatalf("LowerComponent emitted %+v, want exactly %d components", res, want)
	}
	return res.Components
}

// Unless serviceAccountName is authored, the rule emits the component's
// ServiceAccount as a serviceaccount member, carrying the object decorators
// among the authored traits, and hands the deployment member its name, so the
// deployment emits none of its own (go-kure/launcher#702).
func TestWorkerRule_EmitsServiceAccountMember(t *testing.T) {
	annotations := map[string]string{"launcher.gokure.dev/tier": "infra"}
	members := lowerWorkerMembers(t, &oam.Component{Name: "app", Type: "worker",
		Properties:  map[string]any{"image": "ghcr.io/org/app:v1"},
		Traits:      []oam.Trait{{Type: "force-replace"}, {Type: "scaler"}, {Type: "prune-protection"}},
		Annotations: annotations})
	sa := members[1]
	if sa.Name != "app" || sa.Type != "serviceaccount" {
		t.Fatalf("second member is %s/%s, want app/serviceaccount", sa.Name, sa.Type)
	}
	if want := map[string]any{"automountServiceAccountToken": false}; !reflect.DeepEqual(sa.Properties, want) {
		t.Errorf("serviceaccount properties = %v, want %v", sa.Properties, want)
	}
	if !reflect.DeepEqual(sa.Annotations, annotations) {
		t.Errorf("serviceaccount annotations = %v, want the authored %v", sa.Annotations, annotations)
	}
	if len(sa.Traits) != 2 || sa.Traits[0].Type != "force-replace" || sa.Traits[1].Type != "prune-protection" {
		t.Errorf("serviceaccount traits = %+v, want force-replace then prune-protection", sa.Traits)
	}
	if got := members[0].Properties["serviceAccountName"]; got != "app" {
		t.Errorf("deployment serviceAccountName = %v, want the component name", got)
	}

	members = lowerWorkerMembers(t, &oam.Component{Name: "app", Type: "worker",
		Properties: map[string]any{"image": "ghcr.io/org/app:v1", "serviceAccountName": "shared"}})
	if got := members[0].Properties["serviceAccountName"]; got != "shared" {
		t.Errorf("deployment serviceAccountName = %v, want the authored %q", got, "shared")
	}
}

// The emitted component is a deployment of the same name carrying the authored
// properties, with the two opinions replaced: topologySpread removed and the
// affinity shorthand evaluated into the raw corev1 shape. Annotations are
// forwarded, the authored map is not written to, and a key worker does not
// declare is dropped rather than handed to deployment.
func TestWorkerRule_EmitsDeployment(t *testing.T) {
	authored := map[string]any{
		"image":          "ghcr.io/org/app:v1",
		"replicas":       2,
		"topologySpread": true,
		"affinity": map[string]any{
			"enablePodAntiAffinity": true,
			"nodeSelector":          map[string]any{"disk": "ssd", "arch": "amd64"},
		},
		"env":         []any{map[string]any{"name": "A", "value": "b"}},
		"tolerations": []any{map[string]any{"operator": "Exists"}},
	}
	before := maps.Clone(authored)
	comp := &oam.Component{
		Name: "app", Type: "worker", Properties: authored,
		Annotations: map[string]string{"launcher.gokure.dev/tier": "infra"},
	}
	got := lowerWorker(t, comp)

	if got.Name != "app" || got.Type != "deployment" {
		t.Errorf("emitted %q (type %q), want app (type deployment)", got.Name, got.Type)
	}
	if !reflect.DeepEqual(got.Annotations, comp.Annotations) {
		t.Errorf("annotations = %v, want the authored %v", got.Annotations, comp.Annotations)
	}
	if !reflect.DeepEqual(authored, before) {
		t.Errorf("the authored properties were modified: %v", authored)
	}
	for _, k := range []string{"topologySpread", "tolerations"} {
		if _, ok := got.Properties[k]; ok {
			t.Errorf("emitted properties carry %q; want it removed", k)
		}
	}
	for _, k := range []string{"image", "replicas", "env"} {
		if !reflect.DeepEqual(got.Properties[k], authored[k]) {
			t.Errorf("emitted %s = %v, want the authored %v", k, got.Properties[k], authored[k])
		}
	}
	wantAffinity := map[string]any{
		"nodeAffinity": map[string]any{
			"requiredDuringSchedulingIgnoredDuringExecution": map[string]any{
				"nodeSelectorTerms": []any{map[string]any{"matchExpressions": []any{
					map[string]any{"key": "arch", "operator": "In", "values": []any{"amd64"}},
					map[string]any{"key": "disk", "operator": "In", "values": []any{"ssd"}},
				}}},
			},
		},
		"podAntiAffinity": map[string]any{
			"preferredDuringSchedulingIgnoredDuringExecution": []any{map[string]any{
				"weight": int64(100),
				"podAffinityTerm": map[string]any{
					"labelSelector": map[string]any{"matchLabels": map[string]any{"app": "app"}},
					"topologyKey":   "kubernetes.io/hostname",
				},
			}},
		},
	}
	if !reflect.DeepEqual(got.Properties["affinity"], wantAffinity) {
		t.Errorf("emitted affinity = %#v\nwant %#v", got.Properties["affinity"], wantAffinity)
	}
}

// A shorthand that evaluates to nothing (an absent block, or one that neither
// enables anti-affinity nor selects nodes) emits no affinity at all.
func TestWorkerRule_EmptyAffinityOmitted(t *testing.T) {
	for name, props := range map[string]map[string]any{
		"absent":   {"image": "ghcr.io/org/app:v1"},
		"null":     {"image": "ghcr.io/org/app:v1", "affinity": nil},
		"disabled": {"image": "ghcr.io/org/app:v1", "affinity": map[string]any{"enablePodAntiAffinity": false}},
	} {
		got := lowerWorker(t, &oam.Component{Name: "app", Type: "worker", Properties: props})
		if _, ok := got.Properties["affinity"]; ok {
			t.Errorf("%s: emitted affinity %v, want none", name, got.Properties["affinity"])
		}
	}
}

// topologySpread (absent, null or true) becomes a synthesized topology-spread
// trait placed in front of the authored traits, which follow unchanged and in
// order. topologySpread: false attaches nothing and forwards the authored trait
// slice itself.
func TestWorkerRule_TopologySpreadBecomesTrait(t *testing.T) {
	authoredTraits := []oam.Trait{
		{Type: "configmap", Properties: map[string]any{"name": "a"}},
		{Type: "topology-spread", Properties: map[string]any{}},
	}
	for name, ts := range map[string]any{"absent": "absent", "null": nil, "true": true} {
		props := map[string]any{"image": "ghcr.io/org/app:v1"}
		if ts != "absent" {
			props["topologySpread"] = ts
		}
		got := lowerWorker(t, &oam.Component{Name: "app", Type: "worker", Properties: props, Traits: authoredTraits})
		if len(got.Traits) != 3 || got.Traits[0].Type != "topology-spread" || len(got.Traits[0].Properties) != 0 {
			t.Fatalf("%s: traits = %+v, want a propertyless topology-spread followed by the two authored traits", name, got.Traits)
		}
		for i := range authoredTraits {
			if !reflect.DeepEqual(got.Traits[i+1], authoredTraits[i]) {
				t.Errorf("%s: trait %d = %+v, want the authored %+v", name, i+1, got.Traits[i+1], authoredTraits[i])
			}
		}
	}

	got := lowerWorker(t, &oam.Component{Name: "app", Type: "worker",
		Properties: map[string]any{"image": "ghcr.io/org/app:v1", "topologySpread": false}, Traits: authoredTraits})
	if len(got.Traits) != len(authoredTraits) || &got.Traits[0] != &authoredTraits[0] {
		t.Errorf("topologySpread false: traits = %+v, want the authored slice forwarded as is", got.Traits)
	}
}

// Every refusal is the former handler's, with the same cause text: the rule
// runs worker's own parse before it emits anything.
func TestWorkerRule_RefusesAsTheHandlerDid(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"missing image", map[string]any{}, "required property 'image' missing or not a string"},
		{"bad image", map[string]any{"image": "UPPER CASE"}, `image "UPPER CASE" rejected: could not parse reference: UPPER CASE`},
		{"topologySpread not a bool", map[string]any{"image": "nginx:1", "topologySpread": "false"}, "topologySpread: must be a boolean, got string"},
		{"bad anti-affinity type", map[string]any{"image": "nginx:1", "affinity": map[string]any{"podAntiAffinityType": "sometimes"}},
			`affinity.podAntiAffinityType: invalid value "sometimes": must be "preferred" or "required"`},
	}
	for _, tc := range cases {
		_, err := components.WorkerRule{}.LowerComponent(&oam.Component{Name: "app", Type: "worker", Properties: tc.props}, oam.LoweringContext{})
		if err == nil || err.Error() != tc.want {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
}

// The raw affinity shape the rule emits is validated the way the deployment
// component validates it, which is stricter than the shorthand's own parse: a
// label key or value the API server would refuse is refused here, naming the
// shorthand, with the same text the former handler's check used.
func TestWorkerRule_RefusesAnAffinityTheAPIServerWould(t *testing.T) {
	cases := map[string]map[string]any{
		"topologyKey":        {"enablePodAntiAffinity": true, "topologyKey": "not a key!"},
		"nodeSelector key":   {"nodeSelector": map[string]any{"bad key!": "x"}},
		"nodeSelector value": {"nodeSelector": map[string]any{"ok": "va lue"}},
	}
	for name, affinity := range cases {
		_, err := components.WorkerRule{}.LowerComponent(&oam.Component{Name: "app", Type: "worker",
			Properties: map[string]any{"image": "nginx:1", "affinity": affinity}}, oam.LoweringContext{})
		if err == nil || !strings.HasPrefix(err.Error(), "affinity: the shorthand evaluates to an affinity the API server would refuse: ") {
			t.Errorf("%s: err = %v, want the shorthand refusal", name, err)
		}
	}
}

func TestWorker_RequiredImage_Missing(t *testing.T) {
	h := workerViaRule{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name:       "app",
		Type:       "worker",
		Properties: map[string]any{},
	}, "default")
	if err == nil {
		t.Fatal("expected error for missing image")
	}
}

func TestWorker_Generate_NoService(t *testing.T) {
	h := workerViaRule{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "backend",
		Type: "worker",
		Properties: map[string]any{
			"image": "ghcr.io/org/backend:v1.0.0",
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("backend", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var foundDeployment, foundService, foundSA bool
	for _, obj := range objects {
		switch (*obj).(type) {
		case *appsv1.Deployment:
			foundDeployment = true
		case *corev1.Service:
			foundService = true
		case *corev1.ServiceAccount:
			foundSA = true
		}
	}
	if !foundDeployment {
		t.Error("expected Deployment")
	}
	if foundService {
		t.Error("worker must not generate a Service")
	}
	if !foundSA {
		t.Error("expected ServiceAccount")
	}
}

func TestWorker_ApplyPolicy_MaxReplicas(t *testing.T) {
	h := workerViaRule{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image":    "ghcr.io/org/app:v1",
			"replicas": 3,
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	enforceable := cfg.(oam.Enforceable)
	p := &stubPolicy{maxReplicas: int32ptr(2)}
	if err := enforceable.ApplyPolicy(p); err == nil {
		t.Error("expected error when replicas exceed max")
	}
}

func TestWorker_ApplyPolicy_NilPolicy(t *testing.T) {
	h := workerViaRule{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	enforceable := cfg.(oam.Enforceable)
	if err := enforceable.ApplyPolicy(nil); err != nil {
		t.Errorf("nil policy should be a no-op, got: %v", err)
	}
}

func TestWorker_WithSharedPodFields(t *testing.T) {
	h := workerViaRule{}
	component := &oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image":      "ghcr.io/org/app:v1",
			"workingDir": "/app",
			"envFrom": []any{
				map[string]any{"configMapRef": map[string]any{"name": "cfg"}},
			},
			"lifecycle": map[string]any{
				"preStop": map[string]any{
					"exec": map[string]any{"command": []any{"/bin/sh", "-c", "sleep 1"}},
				},
			},
			"securityContext": map[string]any{
				"readOnlyRootFilesystem": true,
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("app", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, obj := range objects {
		if dep, ok := (*obj).(*appsv1.Deployment); ok {
			c := dep.Spec.Template.Spec.Containers[0]
			if c.WorkingDir != "/app" {
				t.Errorf("expected workingDir=/app, got %q", c.WorkingDir)
			}
			if len(c.EnvFrom) != 1 {
				t.Errorf("expected 1 envFrom entry, got %d", len(c.EnvFrom))
			}
			if c.Lifecycle == nil || c.Lifecycle.PreStop == nil {
				t.Error("expected preStop lifecycle hook")
			}
			if c.SecurityContext == nil || c.SecurityContext.ReadOnlyRootFilesystem == nil || !*c.SecurityContext.ReadOnlyRootFilesystem {
				t.Error("expected readOnlyRootFilesystem=true")
			}
			return
		}
	}
	t.Error("Deployment not found in output")
}

// TestWorker_NamedLifecyclePort_Error covers go-kure/launcher#278 wave-11
// finding 5: worker's main container never declares any port (there is no
// `port` property at all — see PropertySchema above), so a named httpGet
// port in `lifecycle`/`probes` can never resolve against it and is rejected
// at parse time instead of authoring a hook guaranteed to fail at runtime.
// worker is the representative test for this fix — cronjob shares the same
// portless shape and the same shared parsing path (parseProbes/
// parseLifecycle's namedPortsAllowed parameter, common.go).
func TestWorker_NamedLifecyclePort_Error(t *testing.T) {
	h := workerViaRule{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"lifecycle": map[string]any{
				"preStop": map[string]any{
					"httpGet": map[string]any{"port": "http", "path": "/shutdown"},
				},
			},
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for a named lifecycle port on a portless worker")
	}
}

func TestWorker_NamedProbePort_Error(t *testing.T) {
	h := workerViaRule{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"probes": map[string]any{
				"liveness": map[string]any{
					"httpGet": map[string]any{"port": "http", "path": "/healthz"},
				},
			},
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for a named probe port on a portless worker")
	}
}

func TestWorker_NumericLifecyclePort_Accepted(t *testing.T) {
	h := workerViaRule{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"lifecycle": map[string]any{
				"preStop": map[string]any{
					"httpGet": map[string]any{"port": 8080, "path": "/shutdown"},
				},
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("app", "default", cfg)
	if _, err := cfg.Generate(app); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

func TestWorker_ApplyPolicy_PrivilegedDenied(t *testing.T) {
	h := workerViaRule{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"securityContext": map[string]any{
				"privileged": true,
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)
	if err := enforceable.ApplyPolicy(&stubPolicy{allowPrivileged: false}); err == nil {
		t.Error("expected error when privileged=true and policy disallows it")
	}
}

// TestWorker_ApplyPolicy_HostPathDenied is worker's sibling of
// TestWebserviceConfig_ApplyPolicy_HostPathDenied (go-kure/launcher#284, P1) — the
// same shared ApplyPolicy gap, same shared enforceHostPathVolumes fix.
func TestWorker_ApplyPolicy_HostPathDenied(t *testing.T) {
	h := workerViaRule{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"volumes": []any{
				map[string]any{
					"name":      "logs",
					"type":      "hostPath",
					"mountPath": "/var/log",
					"path":      "/var/log/app",
				},
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)
	if err := enforceable.ApplyPolicy(&stubPolicy{allowHostPathVols: false}); err == nil {
		t.Error("expected error when a hostPath volume is authored and policy disallows it")
	}
}

// TestWorker_ApplyPolicy_CapabilityAddDenied is worker's sibling of
// TestWebserviceConfig_ApplyPolicy_CapabilityAddDenied (go-kure/launcher#305)
// — the same shared ApplyPolicy gap, same shared enforceContainerCapabilities
// fix.
func TestWorker_ApplyPolicy_CapabilityAddDenied(t *testing.T) {
	h := workerViaRule{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"securityContext": map[string]any{
				"capabilities": map[string]any{
					"add": []any{"NET_ADMIN"},
				},
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)
	if err := enforceable.ApplyPolicy(&stubPolicy{forbiddenContainerCaps: []string{"NET_ADMIN"}}); err == nil {
		t.Error("expected error when capabilities.add includes a forbidden capability")
	}
}

// TestWorker_ApplyPolicy_MaxResources_AgainstIntrinsicDefault is
// worker's sibling of the two webservice
// TestWebserviceConfig_ApplyPolicy_Max{CPU,Memory}_AgainstIntrinsicDefault
// cases (go-kure/launcher#251) — proving enforceMaxResources is actually wired into
// the ApplyPolicy a worker gets (DeploymentConfig's, since worker lowers into a
// deployment component), not just added to enforce.go.
func TestWorker_ApplyPolicy_MaxResources_AgainstIntrinsicDefault(t *testing.T) {
	cases := []struct {
		name   string
		policy stubPolicy
	}{
		{"cpu", stubPolicy{maxCPU: "50m"}},
		{"memory", stubPolicy{maxMemory: "64Mi"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := workerViaRule{}
			cfg, err := h.ToApplicationConfig(&oam.Component{
				Name: "app",
				Type: "worker",
				Properties: map[string]any{
					"image": "ghcr.io/org/app:v1",
				},
			}, "default")
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			enforceable := cfg.(oam.Enforceable)
			err = enforceable.ApplyPolicy(&tc.policy)
			if err == nil {
				t.Fatal("expected error when the intrinsic default exceeds the enforced maximum")
			}
			if !strings.Contains(err.Error(), "generated default") {
				t.Errorf("expected error to mark the value as a generated default, got %q", err.Error())
			}
		})
	}
}

// The eight tests below are worker's siblings of the
// TestWebserviceConfig_ApplyPolicy_InitContainer*/Sidecar* tests
// (go-kure/launcher#312) — same shared ApplyPolicy gap, same
// enforceExtraContainer fix.

func TestWorker_ApplyPolicy_InitContainerResourcesDenied(t *testing.T) {
	h := workerViaRule{}
	component := &oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image":     "ghcr.io/org/app:v1",
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"initContainers": []any{
				map[string]any{"name": "init", "image": "ghcr.io/org/init:v1"},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)

	err = enforceable.ApplyPolicy(&stubPolicy{maxCPU: "50m"})
	if err == nil {
		t.Fatal("expected error when the init container's intrinsic default CPU request exceeds the enforced maximum")
	}
	if !strings.Contains(err.Error(), "initContainers[0]") {
		t.Errorf("expected error to name initContainers[0], got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "generated default") {
		t.Errorf("expected error to mark the value as a generated default, got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{}); err != nil {
		t.Errorf("expected no error under a permissive policy, got %v", err)
	}
}

func TestWorker_ApplyPolicy_InitContainerRegistryDenied(t *testing.T) {
	h := workerViaRule{}
	component := &oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image":     "ghcr.io/org/app:v1",
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"initContainers": []any{
				map[string]any{"name": "init", "image": "docker.io/x/y:v1"},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)

	err = enforceable.ApplyPolicy(&stubPolicy{allowedRegistries: []string{"ghcr.io"}})
	if err == nil {
		t.Fatal("expected error when the init container's image is not from an allowed registry")
	}
	if !strings.Contains(err.Error(), "initContainers[0]") {
		t.Errorf("expected error to name initContainers[0], got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{}); err != nil {
		t.Errorf("expected no error under a permissive policy, got %v", err)
	}
}

func TestWorker_ApplyPolicy_InitContainerPrivilegedDenied(t *testing.T) {
	h := workerViaRule{}
	component := &oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image":     "ghcr.io/org/app:v1",
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"initContainers": []any{
				map[string]any{
					"name": "init", "image": "ghcr.io/org/init:v1",
					"securityContext": map[string]any{"privileged": true},
				},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)

	if err := enforceable.ApplyPolicy(&stubPolicy{allowPrivileged: false}); err == nil {
		t.Error("expected error when the init container is privileged and policy disallows it")
	} else if !strings.Contains(err.Error(), "initContainers[0]") {
		t.Errorf("expected error to name initContainers[0], got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{allowPrivileged: true}); err != nil {
		t.Errorf("expected no error when policy allows privileged, got %v", err)
	}
}

func TestWorker_ApplyPolicy_InitContainerCapabilitiesDenied(t *testing.T) {
	h := workerViaRule{}
	component := &oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image":     "ghcr.io/org/app:v1",
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"initContainers": []any{
				map[string]any{
					"name": "init", "image": "ghcr.io/org/init:v1",
					"securityContext": map[string]any{
						"capabilities": map[string]any{"add": []any{"NET_ADMIN"}},
					},
				},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)

	if err := enforceable.ApplyPolicy(&stubPolicy{forbiddenContainerCaps: []string{"NET_ADMIN"}}); err == nil {
		t.Error("expected error when the init container adds a forbidden capability")
	} else if !strings.Contains(err.Error(), "initContainers[0]") {
		t.Errorf("expected error to name initContainers[0], got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{}); err != nil {
		t.Errorf("expected no error under the default-allow NoopPolicy-equivalent stub, got %v", err)
	}
}

func TestWorker_ApplyPolicy_SidecarResourcesDenied(t *testing.T) {
	h := workerViaRule{}
	component := &oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image":     "ghcr.io/org/app:v1",
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"sidecars": []any{
				map[string]any{"name": "sidecar", "image": "ghcr.io/org/sidecar:v1"},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)

	err = enforceable.ApplyPolicy(&stubPolicy{maxCPU: "50m"})
	if err == nil {
		t.Fatal("expected error when the sidecar's intrinsic default CPU request exceeds the enforced maximum")
	}
	if !strings.Contains(err.Error(), "sidecars[0]") {
		t.Errorf("expected error to name sidecars[0], got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "generated default") {
		t.Errorf("expected error to mark the value as a generated default, got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{}); err != nil {
		t.Errorf("expected no error under a permissive policy, got %v", err)
	}
}

func TestWorker_ApplyPolicy_SidecarRegistryDenied(t *testing.T) {
	h := workerViaRule{}
	component := &oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image":     "ghcr.io/org/app:v1",
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"sidecars": []any{
				map[string]any{"name": "sidecar", "image": "docker.io/x/y:v1"},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)

	err = enforceable.ApplyPolicy(&stubPolicy{allowedRegistries: []string{"ghcr.io"}})
	if err == nil {
		t.Fatal("expected error when the sidecar's image is not from an allowed registry")
	}
	if !strings.Contains(err.Error(), "sidecars[0]") {
		t.Errorf("expected error to name sidecars[0], got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{}); err != nil {
		t.Errorf("expected no error under a permissive policy, got %v", err)
	}
}

func TestWorker_ApplyPolicy_SidecarPrivilegedDenied(t *testing.T) {
	h := workerViaRule{}
	component := &oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image":     "ghcr.io/org/app:v1",
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"sidecars": []any{
				map[string]any{
					"name": "sidecar", "image": "ghcr.io/org/sidecar:v1",
					"securityContext": map[string]any{"privileged": true},
				},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)

	if err := enforceable.ApplyPolicy(&stubPolicy{allowPrivileged: false}); err == nil {
		t.Error("expected error when the sidecar is privileged and policy disallows it")
	} else if !strings.Contains(err.Error(), "sidecars[0]") {
		t.Errorf("expected error to name sidecars[0], got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{allowPrivileged: true}); err != nil {
		t.Errorf("expected no error when policy allows privileged, got %v", err)
	}
}

func TestWorker_ApplyPolicy_SidecarCapabilitiesDenied(t *testing.T) {
	h := workerViaRule{}
	component := &oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image":     "ghcr.io/org/app:v1",
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"sidecars": []any{
				map[string]any{
					"name": "sidecar", "image": "ghcr.io/org/sidecar:v1",
					"securityContext": map[string]any{
						"capabilities": map[string]any{"add": []any{"NET_ADMIN"}},
					},
				},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)

	if err := enforceable.ApplyPolicy(&stubPolicy{forbiddenContainerCaps: []string{"NET_ADMIN"}}); err == nil {
		t.Error("expected error when the sidecar adds a forbidden capability")
	} else if !strings.Contains(err.Error(), "sidecars[0]") {
		t.Errorf("expected error to name sidecars[0], got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{}); err != nil {
		t.Errorf("expected no error under the default-allow NoopPolicy-equivalent stub, got %v", err)
	}
}
