package components_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests cover go-kure/launcher#1253: the API server refuses a pod whose
// containers share a name, but nothing compared the names of the init and
// regular containers of a pod spec authored as such (the pod, podtemplate,
// replicaset and replicationcontroller kinds) or carried as an object (a
// manifests source, a passthrough component, a chart's render), so the document
// generated and failed at apply.

// cnrSpec is a pod spec whose init containers and regular containers carry the
// given names.
func cnrSpec(inits, ctrs []string) string {
	var b strings.Builder
	if len(inits) > 0 {
		b.WriteString("initContainers:\n")
		for _, n := range inits {
			fmt.Fprintf(&b, "  - name: %s\n    image: registry.example/team/init:1.0.0\n", n)
		}
	}
	b.WriteString("containers:\n")
	for _, n := range ctrs {
		fmt.Fprintf(&b, "  - name: %s\n    image: registry.example/team/app:1.2.3\n", n)
	}
	return b.String()
}

// cnrCase is a refused name pair: name is the case, inits and ctrs the container
// names, repeat the name used twice, and first and second the labels of the entry
// that repeats and of the one that used the name first, after the pod spec's
// path.
type cnrCase struct {
	name, repeat  string
	inits, ctrs   []string
	first, second string
}

var cnrCases = []cnrCase{
	{"init vs regular", "app", []string{"app"}, []string{"app"},
		`containers[0] "app"`, `initContainers[0] "app"`},
	{"init vs init", "a", []string{"a", "b", "a"}, []string{"app"},
		`initContainers[2] "a"`, `initContainers[0] "a"`},
	{"regular vs regular", "app", nil, []string{"app", "x", "app"},
		`containers[2] "app"`, `containers[0] "app"`},
	{"later init vs later regular", "proxy", []string{"x", "proxy"}, []string{"app", "proxy"},
		`containers[1] "proxy"`, `initContainers[1] "proxy"`},
}

// want is the refusal text of the case for a pod spec at path ("" for the
// spec's own fields, else a path without its final dot).
func (c cnrCase) want(path string) string {
	prefix := ""
	if path != "" {
		prefix = path + "."
	}
	return prefix + c.first + ": container name " + fmt.Sprintf("%q", c.repeat) + " is also used by " + prefix + c.second
}

// spec is the pod spec of the case.
func (c cnrCase) spec() string { return cnrSpec(c.inits, c.ctrs) }

// cnrAccepted is the control: distinct names, an init container and a regular
// container differing only in case included (a different string to the API
// server).
func cnrAccepted() string {
	return cnrSpec([]string{"migrate", "App"}, []string{"app", "proxy"})
}

// cnrBase is the pod spec of w with its containers replaced by the pod spec
// text spec: a Job or CronJob keeps the restartPolicy its pod needs.
func cnrBase(podSpec, spec string) string {
	return strings.TrimSuffix(podSpec, htPlainPod) + spec
}

// TestRawContainerNames_RefusedOnTheKinds: a name used twice in the pod spec of
// a pod, podtemplate, replicaset or replicationcontroller component is refused
// when it is read, with no policy applied, naming both entries and the path of
// the pod spec; the same spec with distinct names builds.
func TestRawContainerNames_RefusedOnTheKinds(t *testing.T) {
	for _, k := range mnKinds {
		for _, c := range cnrCases {
			t.Run(k.typ+"/"+c.name, func(t *testing.T) {
				_, err := pvTransform(k.typ, k.handler, ptObject(t, k.props(c.spec())), nil)
				trWant(t, err, c.want(strings.TrimSuffix(k.prefix, ".")))
			})
		}
		t.Run(k.typ+"/accepted", func(t *testing.T) {
			if objs, err := pvTransform(k.typ, k.handler, ptObject(t, k.props(cnrAccepted())), nil); err != nil || len(objs) != 1 {
				t.Errorf("objects %v, err %v; want the one object", objs, err)
			}
		})
	}
}

// TestRawContainerNames_RefusedUnderAPolicy: the rule is not the policy's: the
// same refusal comes with a policy applied, and it is no policy class.
func TestRawContainerNames_RefusedUnderAPolicy(t *testing.T) {
	for _, k := range mnKinds {
		t.Run(k.typ, func(t *testing.T) {
			c := cnrCases[0]
			_, err := pvTransform(k.typ, k.handler, ptObject(t, k.props(c.spec())), rcStrict())
			trWant(t, err, c.want(strings.TrimSuffix(k.prefix, ".")))
		})
	}
}

// TestRawContainerNames_ManifestsOnEveryWorkloadKind: an inline manifests source
// holding an object of any kind whose pod spec the shared check reads is
// refused on each case, with no policy applied and under one, naming the
// object, both entries and the path of its pod spec; the same object with
// distinct names builds.
func TestRawContainerNames_ManifestsOnEveryWorkloadKind(t *testing.T) {
	for _, w := range ivWorkloads {
		for _, c := range cnrCases {
			t.Run(w.kind+"/"+c.name, func(t *testing.T) {
				doc := ptWorkload(w.kind, w.apiVersion, w.path, cnrBase(w.podSpec, c.spec()))
				for _, policy := range []oam.Policy{nil, rcStrict()} {
					_, err := mfTransform("manifests", mfInline(doc), policy)
					trWant(t, err, "manifest source: object "+w.kind, c.want(w.path))
				}
			})
		}
		t.Run(w.kind+"/accepted", func(t *testing.T) {
			doc := ptWorkload(w.kind, w.apiVersion, w.path, cnrBase(w.podSpec, cnrAccepted()))
			if objs, err := mfTransform("manifests", mfInline(doc), nil); err != nil || len(objs) != 1 {
				t.Errorf("objects %v, err %v; want the one %s", objs, err, w.kind)
			}
		})
	}
}

// TestRawContainerNames_PassthroughAndChartsOnEveryWorkloadKind: the same
// refusal for an object a passthrough component holds and one a chart renders,
// with no policy applied, naming the object and both entries.
func TestRawContainerNames_PassthroughAndChartsOnEveryWorkloadKind(t *testing.T) {
	for _, p := range mcPaths {
		for _, w := range ivWorkloads {
			for _, c := range cnrCases {
				t.Run(p.name+"/"+w.kind+"/"+c.name, func(t *testing.T) {
					doc := ptWorkload(w.kind, w.apiVersion, w.path, cnrBase(w.podSpec, c.spec()))
					err := p.run(t, doc, nil)
					trWant(t, err, p.ref(w.kind)+c.want(w.path))
				})
			}
			t.Run(p.name+"/"+w.kind+"/accepted", func(t *testing.T) {
				doc := ptWorkload(w.kind, w.apiVersion, w.path, cnrBase(w.podSpec, cnrAccepted()))
				if err := p.run(t, doc, nil); err != nil {
					t.Errorf("err = %v, want the object built", err)
				}
			})
		}
	}
}

// TestRawContainerNames_PassthroughAndChartsRefusedUnderAPolicy: the same
// refusal with a policy applied.
func TestRawContainerNames_PassthroughAndChartsRefusedUnderAPolicy(t *testing.T) {
	deployment := ivWorkloads[3]
	c := cnrCases[0]
	for _, p := range mcPaths {
		t.Run(p.name, func(t *testing.T) {
			doc := ptWorkload(deployment.kind, deployment.apiVersion, deployment.path, cnrBase(deployment.podSpec, c.spec()))
			err := p.run(t, doc, rcStrict)
			trWant(t, err, p.ref(deployment.kind)+c.want(deployment.path))
		})
	}
}

// TestRawContainerNames_ChartHookObject: a workload a chart renders as a hook
// (a pre-install Job) is read like any other rendered object.
func TestRawContainerNames_ChartHookObject(t *testing.T) {
	c := cnrCases[0]
	doc := "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: thing\n  annotations:\n    helm.sh/hook: pre-install\nspec:\n  template:\n    spec:\n" +
		"      restartPolicy: Never\n" + htIndent(c.spec(), "      ")
	err := mcPaths[1].run(t, doc, nil)
	trWant(t, err, mcPaths[1].ref("Job")+c.want("spec.template.spec"))
}
