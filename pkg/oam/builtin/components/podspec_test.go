package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// workloadKinds enumerates the seven kinds that share the pod-level property
// surface, with the minimum properties each needs to generate.
var workloadKinds = []struct {
	name    string
	handler oam.ComponentHandler
	props   map[string]any
}{
	{"webservice", webserviceViaRule{}, map[string]any{"image": "ghcr.io/org/app:v1", "port": 8080}},
	{"worker", workerViaRule{}, map[string]any{"image": "ghcr.io/org/app:v1"}},
	{"statefulset", &components.StatefulsetHandler{}, map[string]any{"image": "ghcr.io/org/app:v1"}},
	{"daemonset", &components.DaemonsetHandler{}, map[string]any{"image": "ghcr.io/org/app:v1"}},
	{"cronjob", &components.CronjobHandler{}, map[string]any{"image": "ghcr.io/org/app:v1", "schedule": "*/5 * * * *"}},
	{"deployment", &components.DeploymentHandler{}, map[string]any{"image": "ghcr.io/org/app:v1"}},
	{"job", &components.JobHandler{}, map[string]any{"image": "ghcr.io/org/app:v1"}},
}

func withProps(base map[string]any, extra map[string]any) map[string]any {
	m := make(map[string]any, len(base)+len(extra))
	for k, v := range base {
		m[k] = v
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func generateKind(t *testing.T, h oam.ComponentHandler, kind string, props map[string]any) []*client.Object {
	t.Helper()
	cfg, err := h.ToApplicationConfig(&oam.Component{Name: "app", Type: kind, Properties: props}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("app", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return objects
}

// podTemplateSpec extracts the pod template from whichever workload kind the
// generated object list carries.
func podTemplateSpec(t *testing.T, objects []*client.Object) corev1.PodSpec {
	t.Helper()
	switch w := (*objects[0]).(type) {
	case *appsv1.Deployment:
		return w.Spec.Template.Spec
	case *appsv1.StatefulSet:
		return w.Spec.Template.Spec
	case *appsv1.DaemonSet:
		return w.Spec.Template.Spec
	case *batchv1.CronJob:
		return w.Spec.JobTemplate.Spec.Template.Spec
	case *batchv1.Job:
		return w.Spec.Template.Spec
	default:
		t.Fatalf("first object is %T, want a workload", *objects[0])
		return corev1.PodSpec{}
	}
}

func hasServiceAccount(objects []*client.Object) bool {
	for _, o := range objects {
		if _, ok := (*o).(*corev1.ServiceAccount); ok {
			return true
		}
	}
	return false
}

// TestWorkloadKinds_DefaultServiceAccount: without an authored
// serviceAccountName the role kinds (webservice, worker) keep their
// per-component ServiceAccount, emitted by a `serviceaccount` member, and the
// pod runs as it. The pod kinds generate none (go-kure/launcher#702): the pod
// names no account and its token stays unmounted, and the namer reports a
// config that runs pods under no account of its own.
func TestWorkloadKinds_DefaultServiceAccount(t *testing.T) {
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			role := k.name == "webservice" || k.name == "worker"
			wantSA := ""
			if role {
				wantSA = "app"
			}
			objects := generateKind(t, k.handler, k.name, k.props)
			if got := hasServiceAccount(objects); got != role {
				t.Errorf("ServiceAccount generated = %v, want %v", got, role)
			}
			ps := podTemplateSpec(t, objects)
			if ps.ServiceAccountName != wantSA {
				t.Errorf("pod ServiceAccountName = %q, want %q", ps.ServiceAccountName, wantSA)
			}
			if role && ps.AutomountServiceAccountToken != nil {
				t.Errorf("pod AutomountServiceAccountToken = %v, want unset beside the role's account", *ps.AutomountServiceAccountToken)
			}
			if !role && (ps.AutomountServiceAccountToken == nil || *ps.AutomountServiceAccountToken) {
				t.Errorf("pod AutomountServiceAccountToken = %v, want false", ps.AutomountServiceAccountToken)
			}
			namer, ok := objects2Config(t, k.handler, k.name, k.props).(oam.ServiceAccountNamer)
			if !ok {
				t.Fatal("config does not implement oam.ServiceAccountNamer")
			}
			if got, pods := namer.ServiceAccountName(); got != wantSA || !pods {
				t.Errorf("ServiceAccountName() = (%q, %v), want (%q, true)", got, pods, wantSA)
			}
		})
	}
}

// TestWorkloadKinds_AuthoredServiceAccount: with an authored
// serviceAccountName no kind generates a ServiceAccount (a role kind no
// longer emits its member), the pod template runs as the authored account, and the
// oam.ServiceAccountNamer contract (read by the rbac trait) reports it.
func TestWorkloadKinds_AuthoredServiceAccount(t *testing.T) {
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			props := withProps(k.props, map[string]any{"serviceAccountName": "shared-sa"})
			objects := generateKind(t, k.handler, k.name, props)
			if hasServiceAccount(objects) {
				t.Error("ServiceAccount generated although serviceAccountName was authored")
			}
			if got := podTemplateSpec(t, objects).ServiceAccountName; got != "shared-sa" {
				t.Errorf("pod ServiceAccountName = %q, want shared-sa", got)
			}
			namer := objects2Config(t, k.handler, k.name, props).(oam.ServiceAccountNamer)
			if got, _ := namer.ServiceAccountName(); got != "shared-sa" {
				t.Errorf("ServiceAccountName() = %q, want shared-sa", got)
			}
		})
	}
}

func objects2Config(t *testing.T, h oam.ComponentHandler, kind string, props map[string]any) stack.ApplicationConfig {
	t.Helper()
	cfg, err := h.ToApplicationConfig(&oam.Component{Name: "app", Type: kind, Properties: props}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	return cfg
}

// TestWorkloadKinds_PodLevelFieldsReachTemplate checks a representative set of
// pod-level properties lands in every kind's pod template.
func TestWorkloadKinds_PodLevelFieldsReachTemplate(t *testing.T) {
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			props := withProps(k.props, map[string]any{
				"terminationGracePeriodSeconds": 15,
				"nodeSelector":                  map[string]any{"tier": "edge"},
				"priorityClassName":             "critical",
				"hostAliases":                   []any{map[string]any{"ip": "10.1.1.1", "hostnames": []any{"db"}}},
				"podSecurityContext":            map[string]any{"fsGroup": 2000},
				"imagePullSecrets":              []any{map[string]any{"name": "regcred"}},
			})
			ps := podTemplateSpec(t, generateKind(t, k.handler, k.name, props))
			if ps.TerminationGracePeriodSeconds == nil || *ps.TerminationGracePeriodSeconds != 15 {
				t.Errorf("TerminationGracePeriodSeconds = %v, want 15", ps.TerminationGracePeriodSeconds)
			}
			if ps.NodeSelector["tier"] != "edge" {
				t.Errorf("NodeSelector = %v, want tier=edge", ps.NodeSelector)
			}
			if ps.PriorityClassName != "critical" {
				t.Errorf("PriorityClassName = %q, want critical", ps.PriorityClassName)
			}
			if len(ps.HostAliases) != 1 || ps.HostAliases[0].IP != "10.1.1.1" {
				t.Errorf("HostAliases = %v, want one alias for 10.1.1.1", ps.HostAliases)
			}
			if ps.SecurityContext == nil || ps.SecurityContext.FSGroup == nil || *ps.SecurityContext.FSGroup != 2000 {
				t.Errorf("SecurityContext = %v, want fsGroup 2000", ps.SecurityContext)
			}
			if len(ps.ImagePullSecrets) != 1 || ps.ImagePullSecrets[0].Name != "regcred" {
				t.Errorf("ImagePullSecrets = %v, want regcred", ps.ImagePullSecrets)
			}
			// The kind-owned parts of the template must survive the shared builder.
			if len(ps.Containers) == 0 || ps.Containers[0].Name != "app" {
				t.Errorf("Containers = %v, want the main container first", ps.Containers)
			}
		})
	}
}

// TestWorkloadKinds_ApplyPolicy_HostNamespacesDenied: the default-deny policy
// (stubPolicy and NoopPolicy alike) rejects hostNetwork, hostPID and hostIPC
// on every kind, and the explicit-false form passes.
func TestWorkloadKinds_ApplyPolicy_HostNamespacesDenied(t *testing.T) {
	for _, k := range workloadKinds {
		for _, key := range []string{"hostNetwork", "hostPID", "hostIPC"} {
			t.Run(k.name+"/"+key, func(t *testing.T) {
				cfg := objects2Config(t, k.handler, k.name, withProps(k.props, map[string]any{key: true}))
				enforceable := cfg.(oam.Enforceable)
				err := enforceable.ApplyPolicy(&stubPolicy{})
				if err == nil || !strings.Contains(err.Error(), key+" is not allowed") {
					t.Fatalf("ApplyPolicy error = %v, want %q denied", err, key)
				}
				if err := enforceable.ApplyPolicy(&oam.NoopPolicy{}); err == nil || !strings.Contains(err.Error(), key+" is not allowed") {
					t.Fatalf("ApplyPolicy(NoopPolicy) error = %v, want %q denied", err, key)
				}
				allowed := objects2Config(t, k.handler, k.name, withProps(k.props, map[string]any{key: false})).(oam.Enforceable)
				if err := allowed.ApplyPolicy(&stubPolicy{}); err != nil {
					t.Fatalf("ApplyPolicy with %s=false: unexpected error %v", key, err)
				}
			})
		}
	}
}

// TestWorkloadKinds_PodActiveDeadlineSeconds: only the Job-pod kinds (cronjob
// and job) accept and emit the pod-level activeDeadlineSeconds; apps/v1 kinds
// reject it.
func TestWorkloadKinds_PodActiveDeadlineSeconds(t *testing.T) {
	jobPodKinds := map[string]bool{"cronjob": true, "job": true}
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			props := withProps(k.props, map[string]any{"podActiveDeadlineSeconds": 120})
			cfg, err := k.handler.ToApplicationConfig(&oam.Component{Name: "app", Type: k.name, Properties: props}, "default")
			if !jobPodKinds[k.name] {
				if err == nil || !strings.Contains(err.Error(), "only Job pods may set activeDeadlineSeconds") {
					t.Fatalf("ToApplicationConfig error = %v, want the Job-only rejection", err)
				}
				if _, ok := k.handler.(oam.PropertySchemaProvider).PropertySchema()["podActiveDeadlineSeconds"]; ok {
					t.Error("PropertySchema publishes podActiveDeadlineSeconds on a non-Job kind")
				}
				return
			}
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			app := stack.NewApplication("app", "default", cfg)
			objects, err := cfg.Generate(app)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			ps := podTemplateSpec(t, objects)
			if ps.ActiveDeadlineSeconds == nil || *ps.ActiveDeadlineSeconds != 120 {
				t.Errorf("pod ActiveDeadlineSeconds = %v, want 120", ps.ActiveDeadlineSeconds)
			}
			// The job-level property keeps its own field: the pod-level key
			// must not have written through to the JobSpec of either kind.
			switch w := (*objects[0]).(type) {
			case *batchv1.CronJob:
				if w.Spec.JobTemplate.Spec.ActiveDeadlineSeconds != nil {
					t.Errorf("job ActiveDeadlineSeconds = %v, want unset when only the pod-level key is authored", w.Spec.JobTemplate.Spec.ActiveDeadlineSeconds)
				}
			case *batchv1.Job:
				if w.Spec.ActiveDeadlineSeconds != nil {
					t.Errorf("job ActiveDeadlineSeconds = %v, want unset when only the pod-level key is authored", w.Spec.ActiveDeadlineSeconds)
				}
			default:
				t.Fatalf("first object is %T, want a Job-pod workload", *objects[0])
			}
		})
	}
}

// TestWorkloadKinds_RejectedPodKeys: the deliberately unsupported
// corev1.PodSpec fields fail loudly on every kind instead of being ignored.
func TestWorkloadKinds_RejectedPodKeys(t *testing.T) {
	rejected := map[string]any{
		"ephemeralContainers": []any{},
		"priority":            1000,
		"overhead":            map[string]any{"cpu": "100m"},
		"serviceAccount":      "legacy",
	}
	for _, k := range workloadKinds {
		for key, val := range rejected {
			t.Run(k.name+"/"+key, func(t *testing.T) {
				_, err := k.handler.ToApplicationConfig(&oam.Component{Name: "app", Type: k.name, Properties: withProps(k.props, map[string]any{key: val})}, "default")
				if err == nil || !strings.HasPrefix(err.Error(), key+":") {
					t.Fatalf("ToApplicationConfig error = %v, want it to start with %q", err, key+":")
				}
			})
		}
	}
}
