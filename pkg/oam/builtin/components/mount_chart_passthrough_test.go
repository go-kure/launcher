package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// mcPath is a way an object reaches the build without being authored as a kind
// component: one a passthrough component holds as its `object`, one a chart
// renders. ref is how a refusal names the object, in front of the path of its
// pod spec; run builds doc, one object, under the policy (none when nil).
type mcPath struct {
	name string
	ref  func(kind string) string
	run  func(t *testing.T, doc string, policy rcPolicy) error
}

// mcApply is the policy of a case: none for a nil rcPolicy.
func mcApply(policy rcPolicy, extra ...string) oam.Policy {
	if policy == nil {
		return nil
	}
	return policy(extra...)
}

var mcPaths = []mcPath{
	{
		"passthrough",
		func(kind string) string { return `passthrough component "web": object ` + kind + ` "demo/thing": ` },
		func(t *testing.T, doc string, policy rcPolicy) error {
			_, err := ptTransform(ptObject(t, doc), mcApply(policy))
			return err
		},
	},
	{
		"helmtemplate",
		func(kind string) string { return `helmtemplate "web": rendered ` + kind + ` "demo/thing": ` },
		func(t *testing.T, doc string, policy rcPolicy) error {
			srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", map[string]string{"object.yaml": doc})
			_, err := htTransform(srvURL, mcApply(policy, htServerHost(t, srvURL)))
			return err
		},
	},
}

// mcDoc is the YAML of a workload of the kind in ivWorkloads w whose pod spec
// is w's own with the mount (written by mnMount) added on placement pl.
func mcDoc(w struct{ kind, apiVersion, path, podSpec string }, pl int, extra ...string) string {
	spec := w.podSpec + strings.TrimPrefix(mnPlacements[pl].spec(mnMount("    ", extra...)), htPlainPod)
	return ptWorkload(w.kind, w.apiVersion, w.path, spec)
}

// TestMountRules_PassthroughAndChartsOnEveryWorkloadKind: an object of each
// kind whose pod spec the shared check reads is refused when a mount breaks a
// single-mount rule, on a container and on an init container, with no policy
// applied; the same object builds when the mount is allowed. The refusal names
// the object and the path of the mount. Every kind is read as its Go type, so
// the table settles that the typed view the rule reads is the same one the
// manifests component yields.
func TestMountRules_PassthroughAndChartsOnEveryWorkloadKind(t *testing.T) {
	for _, p := range mcPaths {
		for _, w := range ivWorkloads {
			for i, pl := range mnPlacements {
				t.Run(p.name+"/"+w.kind+"/"+pl.name, func(t *testing.T) {
					err := p.run(t, mcDoc(w, i, "subPath: /abs"), nil)
					trWant(t, err, p.ref(w.kind)+w.path+"."+pl.label+`volumeMounts[0].subPath: must be a relative path, got "/abs"`)

					if err := p.run(t, mcDoc(w, i, "subPath: sub"), nil); err != nil {
						t.Errorf("an allowed mount: %v", err)
					}
				})
			}
		}
	}
}

// TestMountRules_PassthroughAndChartsEveryRule: each rule the app.yaml
// containers apply, on a Deployment, on both placements.
func TestMountRules_PassthroughAndChartsEveryRule(t *testing.T) {
	deployment := ivWorkloads[3]
	for _, p := range mcPaths {
		for i, pl := range mnPlacements {
			for _, tc := range mnRefused {
				t.Run(p.name+"/"+pl.name+"/"+tc.name, func(t *testing.T) {
					err := p.run(t, mcDoc(deployment, i, tc.extra...), nil)
					trWant(t, err, p.ref("Deployment")+deployment.path+"."+pl.label+tc.tail)
				})
			}
			for _, tc := range mnAccepted {
				t.Run(p.name+"/"+pl.name+"/accepted: "+tc.name, func(t *testing.T) {
					if err := p.run(t, mcDoc(deployment, i, tc.extra...), nil); err != nil {
						t.Errorf("err = %v, want the object built", err)
					}
				})
			}
		}
	}
}

// TestMountRules_PassthroughAndChartsRefusedUnderAPolicy: the rule is not the
// policy's: the same refusal comes with a policy applied, and it is no policy
// class.
func TestMountRules_PassthroughAndChartsRefusedUnderAPolicy(t *testing.T) {
	deployment := ivWorkloads[3]
	for _, p := range mcPaths {
		t.Run(p.name, func(t *testing.T) {
			err := p.run(t, mcDoc(deployment, 0, "subPath: ../x"), rcStrict)
			trWant(t, err, p.ref("Deployment")+deployment.path+"."+mnPlacements[0].label+`volumeMounts[0].subPath: must not contain a '..' element, got "../x"`)

			if err := p.run(t, mcDoc(deployment, 0, "subPath: x"), rcStrict); err != nil {
				t.Errorf("an allowed mount under the policy: %v", err)
			}
		})
	}
}

// TestMountRules_PassthroughRefusedWhenParsed: a passthrough component is
// refused when it is read, before any policy or generation, as the kind
// components are.
func TestMountRules_PassthroughRefusedWhenParsed(t *testing.T) {
	deployment := ivWorkloads[3]
	_, err := (&components.PassthroughHandler{}).ToApplicationConfig(&oam.Component{
		Name:       "web",
		Type:       "passthrough",
		Properties: map[string]any{"object": ptObject(t, mcDoc(deployment, 0, "mountPropagation: Foo"))},
	}, "demo")
	trWant(t, err, `passthrough component "web": object Deployment "demo/thing": `+deployment.path+"."+mnPlacements[0].label+
		`volumeMounts[0].mountPropagation: must be one of None, HostToContainer, Bidirectional, got "Foo"`)
}

// mcBadMountBody is the rest of a pod spec, under spec.template.spec, whose
// container mounts at an absolute subPath.
const mcBadMountBody = "      containers:\n        - name: app\n          volumeMounts:\n            - name: data\n              mountPath: /data\n              subPath: /abs\n"

// TestMountRules_PassthroughAndChartsLeaveOtherObjectsAlone: an object that
// runs no pod, and one of a kind the rule does not know, are not read.
func TestMountRules_PassthroughAndChartsLeaveOtherObjectsAlone(t *testing.T) {
	docs := []struct{ name, doc string }{
		{"a ConfigMap", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: thing\ndata:\n  subPath: /abs\n"},
		{"a custom resource shaped like a workload", "apiVersion: example.io/v1\nkind: Widget\nmetadata:\n  name: thing\nspec:\n  template:\n    spec:\n" + mcBadMountBody},
	}
	for _, p := range mcPaths {
		for _, d := range docs {
			t.Run(p.name+"/"+d.name, func(t *testing.T) {
				if err := p.run(t, d.doc, nil); err != nil {
					t.Errorf("err = %v, want the object built", err)
				}
			})
		}
	}
}

// TestMountRules_UnregisteredWorkloadVersion: a workload in an API version
// kure's scheme does not register is not read by the rule, which has no typed
// pod spec to read. The transform always applies the environment policy check
// (a nil policy becomes the no-op policy), and that check refuses such an
// object as unreadable, so the mount is never the reason; read without that
// check, a passthrough component builds it.
func TestMountRules_UnregisteredWorkloadVersion(t *testing.T) {
	doc := "apiVersion: apps/v1beta2\nkind: Deployment\nmetadata:\n  name: thing\nspec:\n  template:\n    spec:\n" + mcBadMountBody
	for _, p := range mcPaths {
		t.Run(p.name, func(t *testing.T) {
			err := p.run(t, doc, nil)
			if err == nil || !strings.Contains(err.Error(), `apiVersion "apps/v1beta2" is not one whose pod spec this build can read`) {
				t.Fatalf("err = %v, want it refused as unreadable", err)
			}
			if strings.Contains(err.Error(), "subPath") {
				t.Errorf("err = %v, want the mount not named", err)
			}
		})
	}
	t.Run("passthrough read without the policy check", func(t *testing.T) {
		cfg, err := (&components.PassthroughHandler{}).ToApplicationConfig(&oam.Component{
			Name: "web", Type: "passthrough", Properties: map[string]any{"object": ptObject(t, doc)},
		}, "demo")
		if err != nil {
			t.Fatalf("ToApplicationConfig: %v", err)
		}
		if objs, err := cfg.Generate(nil); err != nil || len(objs) != 1 {
			t.Errorf("objects %v, err %v; want the one object", objs, err)
		}
	})
}

// TestMountRules_ChartHooks: an object of a hook phase the build keeps is
// held to the rule as any rendered object is; one of a phase it drops is never
// emitted and is not read.
func TestMountRules_ChartHooks(t *testing.T) {
	job := func(hook string) string {
		spec := "restartPolicy: Never\n" + htPlainPod + strings.TrimPrefix(mnPlacements[0].spec(mnMount("    ", "subPath: /abs")), htPlainPod)
		return "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: probe\n  annotations:\n    helm.sh/hook: " + hook + "\nspec:\n  template:\n    spec:\n" + htIndent(spec, "      ")
	}
	run := func(t *testing.T, hook string) error {
		srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", map[string]string{
			"deployment.yaml": htPolicyDeployment(htPlainPod),
			"job.yaml":        job(hook),
		})
		_, err := htTransform(srvURL, nil)
		return err
	}
	t.Run("pre-install is emitted", func(t *testing.T) {
		trWant(t, run(t, "pre-install"), `helmtemplate "web": rendered Job "demo/probe": spec.template.spec.containers[0] "app": volumeMounts[0].subPath: must be a relative path, got "/abs"`)
	})
	t.Run("test is dropped", func(t *testing.T) {
		if err := run(t, "test"); err != nil {
			t.Errorf("err = %v, want the chart built", err)
		}
	})
}
