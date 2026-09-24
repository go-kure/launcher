package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// containerNameCases lists every workload handler whose main container is
// named after the component, with the minimal properties that build it.
var containerNameCases = []struct {
	typ     string
	handler oam.ComponentHandler
	props   map[string]any
}{
	{"webservice", &components.WebserviceHandler{}, map[string]any{"image": "ghcr.io/org/app:v1", "port": 8080}},
	{"worker", &components.WorkerHandler{}, map[string]any{"image": "ghcr.io/org/app:v1"}},
	{"deployment", &components.DeploymentHandler{}, map[string]any{"image": "ghcr.io/org/app:v1"}},
	{"statefulset", &components.StatefulsetHandler{}, map[string]any{"image": "ghcr.io/org/app:v1"}},
	{"daemonset", &components.DaemonsetHandler{}, map[string]any{"image": "ghcr.io/org/app:v1"}},
	{"cronjob", &components.CronjobHandler{}, map[string]any{"image": "ghcr.io/org/app:v1", "schedule": "*/5 * * * *"}},
	{"job", &components.JobHandler{}, map[string]any{"image": "ghcr.io/org/app:v1"}},
}

// generateWorkload runs a component through ToApplicationConfig and Generate,
// so the name checked is the one actually emitted, not the one parsed.
func generateWorkload(t *testing.T, h oam.ComponentHandler, name, typ string, props map[string]any) ([]*client.Object, error) {
	t.Helper()
	cfg, err := h.ToApplicationConfig(&oam.Component{Name: name, Type: typ, Properties: props}, "default")
	if err != nil {
		t.Fatalf("%s: ToApplicationConfig(%q): %v", typ, name, err)
	}
	return cfg.Generate(stack.NewApplication(name, "default", cfg))
}

// TestWorkloadHandlers_DottedComponentName_Refused pins go-kure/launcher#407.
// A component name is validated as a DNS-1123 *subdomain*, which permits dots,
// and metadata.name and the `app:` label both accept that. The main container
// is named after the component too, and corev1.Container.Name is a DNS-1123
// *label*, which forbids dots — so `batch.worker` used to build a manifest the
// API server refuses at admission, naming a field the author never wrote.
func TestWorkloadHandlers_DottedComponentName_Refused(t *testing.T) {
	for _, tc := range containerNameCases {
		t.Run(tc.typ, func(t *testing.T) {
			_, err := generateWorkload(t, tc.handler, "batch.worker", tc.typ, tc.props)
			if err == nil {
				t.Fatalf("Generate accepted the dotted component name 'batch.worker' for type %s, want a refusal — "+
					"the emitted container name would be rejected at admission", tc.typ)
			}
			for _, want := range []string{"batch.worker", "container name", "DNS-1123 label"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to mention %q", err, want)
				}
			}
		})
	}
}

// TestWorkloadHandlers_UndottedComponentName_EmitsValidContainerName is the
// counterpart: an undotted name still builds, so the refusal above is of the
// dot and not of every name, and the container name that is emitted passes
// the same validator the API server applies.
func TestWorkloadHandlers_UndottedComponentName_EmitsValidContainerName(t *testing.T) {
	for _, tc := range containerNameCases {
		t.Run(tc.typ, func(t *testing.T) {
			objs, err := generateWorkload(t, tc.handler, "batch-worker", tc.typ, tc.props)
			if err != nil {
				t.Fatalf("Generate(%s, batch-worker): %v", tc.typ, err)
			}
			var names []string
			for _, obj := range objs {
				names = append(names, podContainerNames(*obj)...)
			}
			if len(names) == 0 {
				t.Fatalf("%s: no pod template containers found in the generated objects", tc.typ)
			}
			if names[0] != "batch-worker" {
				t.Errorf("%s: main container name = %q, want %q", tc.typ, names[0], "batch-worker")
			}
			for _, n := range names {
				if errs := validation.IsDNS1123Label(n); len(errs) > 0 {
					t.Errorf("%s: container name %q is not a DNS-1123 label: %v", tc.typ, n, errs)
				}
			}
		})
	}
}

// podContainerNames returns the spec.containers names of any workload object's
// pod template, or nil for an object that carries none.
func podContainerNames(obj client.Object) []string {
	var spec *corev1.PodSpec
	switch o := obj.(type) {
	case *appsv1.Deployment:
		spec = &o.Spec.Template.Spec
	case *appsv1.StatefulSet:
		spec = &o.Spec.Template.Spec
	case *appsv1.DaemonSet:
		spec = &o.Spec.Template.Spec
	case *batchv1.Job:
		spec = &o.Spec.Template.Spec
	case *batchv1.CronJob:
		spec = &o.Spec.JobTemplate.Spec.Template.Spec
	default:
		return nil
	}
	names := make([]string, 0, len(spec.Containers))
	for _, c := range spec.Containers {
		names = append(names, c.Name)
	}
	return names
}
