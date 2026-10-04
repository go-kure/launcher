package kurel

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// fluxObjectKeyPrefix is the prefix of the keys kustomize-controller reads on
// an object, written out literally. kurel's output carries none for the
// prune-protection and force-replace traits: they set a delivery intent on the
// application, which kurel's flat output has no place for
// (go-kure/launcher#782).
const fluxObjectKeyPrefix = "kustomize.toolkit.fluxcd.io/"

// deliveryIntents transforms appYAML with kurel's builtin transformer and
// returns the delivery intent of every application in the cluster, keyed by
// application name.
func deliveryIntents(t *testing.T, appYAML string) map[string]stack.DeliveryIntent {
	t.Helper()
	cluster, _, err := transformWithBuiltins(t, appYAML)
	if err != nil {
		t.Fatalf("transforming: %v", err)
	}
	out := map[string]stack.DeliveryIntent{}
	for _, b := range leafBundles(cluster.Node) {
		for _, app := range b.Applications {
			if _, dup := out[app.Name]; dup {
				t.Fatalf("two applications are named %q; their intents cannot be keyed by name", app.Name)
			}
			out[app.Name] = app.Delivery
		}
	}
	return out
}

// assertNoFluxObjectKeys fails for each decoded output document that carries a
// Flux annotation or label.
func assertNoFluxObjectKeys(t *testing.T, docs ...map[string]any) {
	t.Helper()
	if len(docs) == 0 {
		t.Fatal("no documents to check; the assertion would be vacuous")
	}
	for _, d := range docs {
		md, _ := d["metadata"].(map[string]any)
		for _, field := range []string{"annotations", "labels"} {
			keys, _ := md[field].(map[string]any)
			for k := range keys {
				if strings.HasPrefix(k, fluxObjectKeyPrefix) {
					t.Errorf("%v %v carries %s key %s; the delivery traits set an intent on the application and write nothing on the object",
						d["kind"], md["name"], field, k)
				}
			}
		}
	}
}

// deliveryIntentOf returns the intent the named delivery traits state together.
func deliveryIntentOf(t *testing.T, traitTypes []string) stack.DeliveryIntent {
	t.Helper()
	var intent stack.DeliveryIntent
	for _, typ := range traitTypes {
		switch typ {
		case "prune-protection":
			intent.PruneProtection = true
		case "force-replace":
			intent.ForceReplace = true
		default:
			t.Fatalf("no delivery intent recorded for trait %q", typ)
		}
	}
	return intent
}

// assertTwinDeliveryIntent checks, on the kind path and on the trait path of a
// twin, that the application named name exists and that every application of
// the document carries the intent ownerTraits state: the object's own
// application on the kind path, the owner and its sub-application on the trait
// path.
func assertTwinDeliveryIntent(t *testing.T, name string, ownerTraits []string, kindApp, traitApp string) {
	t.Helper()
	want := deliveryIntentOf(t, ownerTraits)
	for _, p := range []struct{ path, app string }{{"kind", kindApp}, {"trait", traitApp}} {
		intents := deliveryIntents(t, p.app)
		if _, ok := intents[name]; !ok {
			t.Errorf("%s path: no application named %q (have %v)", p.path, name, slices.Sorted(maps.Keys(intents)))
		}
		for app, got := range intents {
			if got != want {
				t.Errorf("%s path: application %q Delivery = %+v, want %+v", p.path, app, got, want)
			}
		}
	}
}
