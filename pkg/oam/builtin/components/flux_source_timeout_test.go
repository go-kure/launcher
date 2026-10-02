package components_test

import (
	"regexp"
	"strings"
	"testing"
	"time"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	kio "github.com/go-kure/kure/pkg/io"
	"github.com/go-kure/kure/pkg/stack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// sourceTimeoutPattern is the CRD pattern of spec.timeout on the
// source-controller v1 kinds: no h unit.
var sourceTimeoutPattern = regexp.MustCompile(`^([0-9]+(\.[0-9]+)?(ms|s|m))+$`)

// generateFluxSource parses the kind's full properties with timeout set, and
// generates the source.
func generateFluxSource(t *testing.T, k fluxSourceKind, timeout string) []*client.Object {
	t.Helper()
	props := fluxSrcProps(t, k.full)
	props["timeout"] = timeout
	cfg := fluxSrcConfig(t, k, props)
	objs, err := cfg.Generate(stack.NewApplication("src", "demo", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("Generate returned %d objects, want 1", len(objs))
	}
	return objs
}

func encodeObjects(t *testing.T, objs []*client.Object) string {
	t.Helper()
	out, err := kio.EncodeObjectsToYAML(objs)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return string(out)
}

// TestFluxSourceComponents_LongTimeoutEmittedInMinutes: a source timeout of an
// hour or more is emitted in minutes, which the field's pattern takes, and the
// rest of the source is emitted exactly as for a timeout below an hour
// (go-kure/launcher#619).
func TestFluxSourceComponents_LongTimeoutEmittedInMinutes(t *testing.T) {
	cases := []struct{ authored, emitted string }{
		{"60m", "60m0s"},
		{"90m", "90m0s"},
		{"3600s", "60m0s"},
		{"120m30.5s", "120m30.5s"},
	}
	for _, k := range fluxSourceKinds() {
		if _, ok := k.handler.PropertySchema()["timeout"]; !ok {
			continue // a HelmChart has no timeout
		}
		// The same source with a timeout below an hour: the typed object, as
		// every source was emitted before.
		base := encodeObjects(t, generateFluxSource(t, k, "10m"))
		if !strings.Contains(base, "\n  timeout: 10m0s\n") {
			t.Fatalf("%s: baseline does not carry spec.timeout 10m0s:\n%s", k.typ, base)
		}
		for _, tc := range cases {
			t.Run(k.typ+"/"+tc.authored, func(t *testing.T) {
				if !sourceTimeoutPattern.MatchString(tc.emitted) {
					t.Fatalf("test premise wrong: %q is outside the CRD pattern", tc.emitted)
				}
				got := encodeObjects(t, generateFluxSource(t, k, tc.authored))
				want := strings.Replace(base, "\n  timeout: 10m0s\n", "\n  timeout: "+tc.emitted+"\n", 1)
				if got != want {
					t.Fatalf("emitted:\n%s\nwant:\n%s", got, want)
				}
			})
		}
		t.Run(k.typ+"/below an hour stays typed", func(t *testing.T) {
			objs := generateFluxSource(t, k, "59m59s")
			if _, ok := (*objs[0]).(*unstructured.Unstructured); ok {
				t.Fatal("a timeout below an hour is emitted as an unstructured object, want the typed source")
			}
		})
	}
}

// TestFluxSourceConfigs_BuiltLongTimeoutEmittedInMinutes: a config built
// directly, whose timeout has no authored text, is emitted in minutes too.
func TestFluxSourceConfigs_BuiltLongTimeoutEmittedInMinutes(t *testing.T) {
	cfg := &components.GitRepositoryConfig{Name: "src", Namespace: "demo", Spec: sourcev1.GitRepositorySpec{
		URL:     "https://github.com/org/repo",
		Timeout: &metav1.Duration{Duration: 2*time.Hour + 30*time.Second},
	}}
	objs, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	u, ok := (*objs[0]).(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("emitted %T, want *unstructured.Unstructured", *objs[0])
	}
	if got, _, _ := unstructured.NestedString(u.Object, "spec", "timeout"); got != "120m30s" {
		t.Fatalf("spec.timeout = %q, want 120m30s", got)
	}
	if u.GetAPIVersion() != sourcev1.GroupVersion.String() || u.GetKind() != "GitRepository" || u.GetName() != "src" || u.GetNamespace() != "demo" {
		t.Fatalf("identity = %s %s %s/%s", u.GetAPIVersion(), u.GetKind(), u.GetNamespace(), u.GetName())
	}
}
