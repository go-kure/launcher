package registry_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests hold the provenance report to the values a workload kind fills
// when it generates its object (go-kure/launcher#1580): the cpu and memory
// requests, the memory limit that mirrors the memory request, and the token
// automount. They run the registry's whole pipeline, so the owner and the path
// are the ones a consumer reads. They share the helpers of
// policy_default_provenance_test.go.

// generateTimeFillPaths are the paths of the main container's fills and the
// pod's.
var generateTimeFillPaths = []string{
	"resources.requests.cpu",
	"resources.requests.memory",
	"resources.limits.memory",
	"automountServiceAccountToken",
}

// handlerBuiltKind is a kind whose handler builds the pod with launcher's fills,
// the owner its report is under, and the fills it makes. The raw `deployment`,
// `daemonset`, `statefulset`, `job` and `cronjob` fill nothing (go-kure/launcher#1629);
// the worker stands for the reshaped deployment it lowers into, which reports on
// its role member. The worker
// generates its own ServiceAccount and names it, so the pod never meets the
// token automount fill and the worker's row leaves that path out.
type handlerBuiltKind struct {
	typ   string
	extra map[string]any
	owner owner
	paths []string
}

var handlerBuiltKinds = []handlerBuiltKind{
	{"worker", nil, owner{component: "web", member: "role-deployment"}, generateTimeFillPaths[:3]},
}

// TestGenerateTimeFills_AreReported: a workload that authors none of them has
// each reported as built-in, under the component.
func TestGenerateTimeFills_AreReported(t *testing.T) {
	for _, k := range handlerBuiltKinds {
		t.Run(k.typ, func(t *testing.T) {
			prov, _ := policyProvenanceFor(t, &defaultsPolicy{}, workload("web", k.typ, k.extra))
			entries := entriesOf(prov, k.owner)
			for _, path := range k.paths {
				src, ok := entries[path]
				if !ok {
					t.Errorf("no entry at %q; entries: %v", path, entries)
					continue
				}
				if src.Kind != oam.SourceBuiltin || src.Ref == "" {
					t.Errorf("entry at %q = %+v, want a built-in source that names its fill", path, src)
				}
			}
		})
	}
}

// TestGenerateTimeFills_ExtraContainersAtTheirOwnPaths: the fills of an init
// container and of a sidecar are reported at the entry's own path.
func TestGenerateTimeFills_ExtraContainersAtTheirOwnPaths(t *testing.T) {
	for _, tc := range extraContainerCases() {
		t.Run(tc.typ, func(t *testing.T) {
			prov, _ := policyProvenanceFor(t, &defaultsPolicy{}, extraContainerComponent(tc.typ, tc.extra, tc.sidecars))
			entries := entriesOf(prov, tc.owner)
			prefixes := []string{"initContainers[0]."}
			if tc.sidecars {
				prefixes = append(prefixes, "sidecars[0].")
			}
			for _, prefix := range prefixes {
				for _, path := range generateTimeFillPaths[:3] {
					if src := entries[prefix+path]; src.Kind != oam.SourceBuiltin {
						t.Errorf("entry at %q = %+v, want a built-in source; entries: %v", prefix+path, src, entries)
					}
				}
			}
		})
	}
}

// TestGenerateTimeFills_PolicyValueKeepsItsSource: a value the policy filled is
// the policy's, and no built-in entry replaces it.
func TestGenerateTimeFills_PolicyValueKeepsItsSource(t *testing.T) {
	policy := &defaultsPolicy{cpuRequest: "200m", memoryRequest: "256Mi", memoryLimit: "512Mi"}
	for _, k := range handlerBuiltKinds {
		t.Run(k.typ, func(t *testing.T) {
			prov, _ := policyProvenanceFor(t, policy, workload("web", k.typ, k.extra))
			entries := entriesOf(prov, k.owner)
			for _, path := range generateTimeFillPaths[:3] {
				if src := entries[path]; src.Kind != oam.SourcePolicy {
					t.Errorf("entry at %q = %+v, want the policy's", path, src)
				}
			}
			if len(k.paths) == len(generateTimeFillPaths) {
				if src := entries["automountServiceAccountToken"]; src.Kind != oam.SourceBuiltin {
					t.Errorf("entry at automountServiceAccountToken = %+v, want a built-in source", src)
				}
			}
		})
	}
}

// TestGenerateTimeFills_AuthoredValueIsNotReported: what the author wrote has
// no built-in entry. The workload leaves other values to the parser (replicas
// among them), which reports its own fills, so only the generate-time paths are
// read here.
func TestGenerateTimeFills_AuthoredValueIsNotReported(t *testing.T) {
	// The worker names an existing account, so it generates none and reports on
	// the component itself.
	prov, _ := policyProvenanceFor(t, &defaultsPolicy{}, workload("web", "worker", map[string]any{
		"resources": map[string]any{
			"requests": map[string]any{"cpu": "50m", "memory": "64Mi"},
			"limits":   map[string]any{"memory": "128Mi"},
		},
		"serviceAccountName": "shared",
	}))
	entries := entriesOf(prov, owner{component: "web"})
	if !authoredOnTheMember(entries["resources.requests.cpu"]) {
		t.Fatalf("control: resources.requests.cpu = %+v, want authored; entries: %v", entries["resources.requests.cpu"], entries)
	}
	for path, src := range entries {
		generated := path == "automountServiceAccountToken" || strings.HasPrefix(path, "resources.")
		if generated && src.Kind == oam.SourceBuiltin {
			t.Errorf("entry at %q is built-in (%+v) on a workload that authors every such value", path, src)
		}
	}
}

// TestGenerateTimeFills_ReportDoesNotChangeTheOutput: the objects are those of
// a transform that keeps no report.
func TestGenerateTimeFills_ReportDoesNotChangeTheOutput(t *testing.T) {
	for _, k := range handlerBuiltKinds {
		t.Run(k.typ, func(t *testing.T) {
			_, withReport := policyProvenanceFor(t, &defaultsPolicy{}, workload("web", k.typ, k.extra))
			app := &oam.Application{
				APIVersion: oam.SupportedAPIVersion,
				Kind:       "Application",
				Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
				Spec:       oam.ApplicationSpec{Components: []oam.Component{workload("web", k.typ, k.extra)}},
			}
			cluster, _, err := newTransformer().TransformWithPolicy(app, oam.TransformContext{Namespace: "default", Policy: &defaultsPolicy{}})
			if err != nil {
				t.Fatalf("TransformWithPolicy: %v", err)
			}
			if plain := manifestsOf(t, cluster); string(plain) != string(withReport) {
				t.Errorf("objects differ with the report kept:\n%s\nwithout:\n%s", withReport, plain)
			}
		})
	}
}
