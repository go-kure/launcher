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

// handlerBuiltKinds are the kinds whose own handler builds the pod.
var handlerBuiltKinds = map[string]map[string]any{
	"deployment":  nil,
	"statefulset": nil,
	"daemonset":   nil,
	"job":         nil,
	"cronjob":     {"schedule": "*/5 * * * *"},
}

// TestGenerateTimeFills_AreReported: a workload that authors none of them has
// each reported as built-in, under the component.
func TestGenerateTimeFills_AreReported(t *testing.T) {
	for typ, extra := range handlerBuiltKinds {
		t.Run(typ, func(t *testing.T) {
			prov, _ := policyProvenanceFor(t, &defaultsPolicy{}, workload("web", typ, extra))
			entries := entriesOf(prov, owner{component: "web"})
			for _, path := range generateTimeFillPaths {
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
	prov, _ := policyProvenanceFor(t, policy, workload("web", "deployment", nil))
	entries := entriesOf(prov, owner{component: "web"})
	for _, path := range generateTimeFillPaths[:3] {
		if src := entries[path]; src.Kind != oam.SourcePolicy {
			t.Errorf("entry at %q = %+v, want the policy's", path, src)
		}
	}
	if src := entries["automountServiceAccountToken"]; src.Kind != oam.SourceBuiltin {
		t.Errorf("entry at automountServiceAccountToken = %+v, want a built-in source", src)
	}
}

// TestGenerateTimeFills_AuthoredValueIsNotReported: what the author wrote has
// no built-in entry. The workload leaves other values to the parser (replicas
// among them), which reports its own fills, so only the generate-time paths are
// read here.
func TestGenerateTimeFills_AuthoredValueIsNotReported(t *testing.T) {
	prov, _ := policyProvenanceFor(t, &defaultsPolicy{}, workload("web", "deployment", map[string]any{
		"resources": map[string]any{
			"requests": map[string]any{"cpu": "50m", "memory": "64Mi"},
			"limits":   map[string]any{"memory": "128Mi"},
		},
		"serviceAccountName": "shared",
	}))
	for path, src := range entriesOf(prov, owner{component: "web"}) {
		generated := path == "automountServiceAccountToken" || strings.HasPrefix(path, "resources.")
		if generated && src.Kind == oam.SourceBuiltin {
			t.Errorf("entry at %q is built-in (%+v) on a workload that authors every such value", path, src)
		}
	}
}

// TestGenerateTimeFills_ReportDoesNotChangeTheOutput: the objects are those of
// a transform that keeps no report.
func TestGenerateTimeFills_ReportDoesNotChangeTheOutput(t *testing.T) {
	for typ, extra := range handlerBuiltKinds {
		t.Run(typ, func(t *testing.T) {
			_, withReport := policyProvenanceFor(t, &defaultsPolicy{}, workload("web", typ, extra))
			app := &oam.Application{
				APIVersion: oam.SupportedAPIVersion,
				Kind:       "Application",
				Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
				Spec:       oam.ApplicationSpec{Components: []oam.Component{workload("web", typ, extra)}},
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
