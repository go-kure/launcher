package components_test

import (
	"reflect"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// TestPostgresql_InheritedMetadataSharesNoMap is go-kure/launcher#396 part 2. The
// Cluster's spec.inheritedMetadata.labels/annotations were the component config's
// own maps, so a caller editing a generated Cluster edited the config, and a second
// Generate from the same config returned different output than the first. Pointer
// identity, not content: equal maps and one map read twice look the same by value.
func TestPostgresql_InheritedMetadataSharesNoMap(t *testing.T) {
	h := &components.PostgresqlHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "db", Type: "postgresql",
		Properties: map[string]any{
			"inheritedMetadata": map[string]any{
				"labels":      map[string]any{"team": "backend"},
				"annotations": map[string]any{"owner": "dba"},
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	pc := cfg.(*components.PostgresqlConfig)

	generate := func() *cnpgv1.Cluster {
		t.Helper()
		objs, err := cfg.Generate(stack.NewApplication("db", "default", cfg))
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		for _, o := range objs {
			if c, ok := (*o).(*cnpgv1.Cluster); ok {
				return c
			}
		}
		t.Fatal("no Cluster generated")
		return nil
	}

	first := generate()
	meta := first.Spec.InheritedMetadata
	if meta == nil {
		t.Fatal("spec.inheritedMetadata not rendered")
	}
	same := func(a, b map[string]string) bool {
		return a != nil && b != nil && reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
	}
	if same(meta.Labels, pc.InheritedLabels) {
		t.Error("spec.inheritedMetadata.labels is the config's InheritedLabels map")
	}
	if same(meta.Annotations, pc.InheritedAnnotations) {
		t.Error("spec.inheritedMetadata.annotations is the config's InheritedAnnotations map")
	}

	// The consequence the issue names: an edit to one generated Cluster must not
	// reach the next Generate.
	meta.Labels["stamped"] = "by-caller"
	meta.Annotations["stamped"] = "by-caller"
	second := generate().Spec.InheritedMetadata
	if _, leaked := second.Labels["stamped"]; leaked {
		t.Error("a label stamped on one generated Cluster appeared on the next Generate")
	}
	if _, leaked := second.Annotations["stamped"]; leaked {
		t.Error("an annotation stamped on one generated Cluster appeared on the next Generate")
	}
}
