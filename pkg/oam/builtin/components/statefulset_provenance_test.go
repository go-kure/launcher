package components_test

import (
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The statefulset's nested fill writes volumeClaimTemplates[i].storageClass, which
// the provenance report attributes to the capability the filler read, while the
// sibling fields of the same authored list stay authored. Two statefulsets both get
// the entry: the second reads a key the first already consumed.
func TestStatefulsetHandler_ProvenanceNestedFill(t *testing.T) {
	statefulset := func(name string, template map[string]any) oam.Component {
		return oam.Component{Name: name, Type: "statefulset", Properties: map[string]any{
			"image":                "ghcr.io/org/db:v1",
			"volumeClaimTemplates": []any{template},
		}}
	}
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{
			statefulset("db-a", map[string]any{"name": "data", "mountPath": "/data", "size": "1Gi"}),
			statefulset("db-b", map[string]any{"name": "data", "mountPath": "/data", "size": "1Gi"}),
			statefulset("db-c", map[string]any{"name": "data", "mountPath": "/data", "size": "1Gi", "storageClass": "mine"}),
		}},
	}
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{"statefulset": &components.StatefulsetHandler{}}, nil)
	_, result, prov, err := tr.TransformWithProvenance(app, oam.TransformContext{Capabilities: pvcBinding(map[string]any{"storageClassName": "platform-ssd"})})
	if err != nil {
		t.Fatalf("TransformWithProvenance: %v", err)
	}
	if got := result.ConsumedCapabilities; len(got) != 1 || got[0] != "pvc" {
		t.Errorf("consumed capabilities %v, want [pvc]", got)
	}
	bySource := map[string]map[string]oam.Source{}
	for _, e := range prov.Entries {
		if e.Trait != "" || e.Member != "" {
			t.Errorf("unexpected entry %+v", e)
		}
		if bySource[e.Component] == nil {
			bySource[e.Component] = map[string]oam.Source{}
		}
		bySource[e.Component][e.Path] = e.Source
	}
	capability := oam.Source{Kind: oam.SourceCapability, Ref: "pvc"}
	authored := oam.Source{Kind: oam.SourceAuthored}
	for _, name := range []string{"db-a", "db-b"} {
		got := bySource[name]
		if got["volumeClaimTemplates[0].storageClass"] != capability {
			t.Errorf("%s: storageClass = %+v, want %+v", name, got["volumeClaimTemplates[0].storageClass"], capability)
		}
		for _, p := range []string{"image", "volumeClaimTemplates[0].name", "volumeClaimTemplates[0].mountPath", "volumeClaimTemplates[0].size"} {
			if got[p] != authored {
				t.Errorf("%s: %s = %+v, want authored", name, p, got[p])
			}
		}
	}
	if got := bySource["db-c"]["volumeClaimTemplates[0].storageClass"]; got != authored {
		t.Errorf("db-c: an authored storageClass = %+v, want authored", got)
	}
}
