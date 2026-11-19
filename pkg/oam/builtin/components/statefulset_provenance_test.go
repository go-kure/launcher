package components_test

import (
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The statefulset's nested fill writes volumeClaimTemplates[i].spec.storageClassName,
// which the provenance report attributes to the capability the filler read, while
// the sibling fields of the same authored list stay authored. Two statefulsets both
// get the entry: the second reads a key the first already consumed.
//
// The raw kind fills nothing else, so the capability is the only non-authored
// source in the report: the former `storageClass`, `accessModes`, `resources` and
// `replicas` fills the reshaped form reported are gone with the form.
func TestStatefulsetHandler_ProvenanceNestedFill(t *testing.T) {
	statefulset := func(name string, spec map[string]any) oam.Component {
		props := ptObject(t, stsPlain(htPlainPod))
		props["volumeClaimTemplates"] = []any{map[string]any{"metadata": map[string]any{"name": "data"}, "spec": spec}}
		return oam.Component{Name: name, Type: "statefulset", Properties: props}
	}
	request := func() map[string]any {
		return map[string]any{"resources": map[string]any{"requests": map[string]any{"storage": "1Gi"}}}
	}
	mine := request()
	mine["storageClassName"] = "mine"
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{
			statefulset("db-a", request()),
			statefulset("db-b", request()),
			statefulset("db-c", mine),
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
	const class = "volumeClaimTemplates[0].spec.storageClassName"
	for _, name := range []string{"db-a", "db-b"} {
		got := bySource[name]
		if got[class] != capability {
			t.Errorf("%s: storageClassName = %+v, want %+v", name, got[class], capability)
		}
		for _, p := range []string{"selector.matchLabels.tier", "volumeClaimTemplates[0].metadata.name", "volumeClaimTemplates[0].spec.resources.requests.storage"} {
			if got[p] != authored {
				t.Errorf("%s: %s = %+v, want authored", name, p, got[p])
			}
		}
		for p, s := range got {
			if s != authored && p != class {
				t.Errorf("%s: %s = %+v, want authored: the capability fills only the class", name, p, s)
			}
		}
	}
	if got := bySource["db-c"][class]; got != authored {
		t.Errorf("db-c: an authored storageClassName = %+v, want authored", got)
	}
}
