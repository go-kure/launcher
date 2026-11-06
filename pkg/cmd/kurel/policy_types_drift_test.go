package kurel

import (
	"sort"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/registry"
)

// pkg/oam cannot import the registry, so its set of policy types a document may name
// (validPolicyTypes) is a copy of the registered handlers' types plus the delivery
// types. This holds the copy to the registry: a policy handler added there without a
// line in that set makes a document naming it fail to parse, and fails here first.
func TestPolicyTypes_EveryRegisteredHandlerParses(t *testing.T) {
	types := make([]string, 0)
	for typ := range registry.PolicyHandlers() {
		types = append(types, typ)
	}
	sort.Strings(types)
	if len(types) == 0 {
		t.Fatal("the registry has no policy handlers; the check below would prove nothing")
	}
	for _, typ := range types {
		doc := []byte("apiVersion: launcher.gokure.dev/v1alpha1\nkind: Application\nmetadata:\n  name: myapp\nspec:\n  components:\n    - name: web\n      type: webservice\n      properties:\n        image: nginx\n  policies:\n    - name: pol\n      type: " + typ + "\n")
		if _, err := oam.Parse(doc); err != nil {
			t.Errorf("a document naming the registered policy type %q does not parse: %v", typ, err)
		}
		if _, err := oam.ParseWithExtraTypes(doc, nil, newBuiltinTransformer().LowerableTypes()); err != nil {
			t.Errorf("a document naming the registered policy type %q does not parse with the builtin transformer's types: %v", typ, err)
		}
	}
}
