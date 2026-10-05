package components

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// policyFreeType is the type one policyFreeKind decodes its properties into.
type policyFreeType struct {
	typ         reflect.Type
	wholeObject bool
}

func policyFreeTypeOf[T any](k *policyFreeKind[T]) policyFreeType {
	return policyFreeType{typ: reflect.TypeFor[T](), wholeObject: k.wholeObject}
}

// policyFreeTypes lists every policyFreeKind of the package whose type
// publishes its field comments (SwaggerDoc). A kind added without a row here
// is not held by the tests below. The kinds of the Prometheus operator's API
// publish none and are held by TestMonitoringKinds_NoDefaultedZeros instead,
// from the markers of their source.
var policyFreeTypes = []policyFreeType{
	policyFreeTypeOf(storageClassKind),
	policyFreeTypeOf(volumeAttributesClassKind),
	policyFreeTypeOf(priorityClassKind),
	policyFreeTypeOf(runtimeClassKind),
	policyFreeTypeOf(ingressClassKind),
	policyFreeTypeOf(csiDriverKind),
	policyFreeTypeOf(serviceCIDRKind),
	policyFreeTypeOf(podDisruptionBudgetKind),
}

// TestPolicyFreeKinds_NoDefaultedZeros: policyFreeKind.config passes no
// defaulted-zero list to refuseUncarriedSpecValues, so no type it decodes may
// hold a field on which an authored 0 or false would be silently replaced: a
// non-pointer omitempty number or boolean whose field comment states a default
// that is not zero (TestPodSpecDefaultedZeros_MatchFieldDocs reads the same
// source). A kind with such a field fails here, naming it, as does a
// dependency bump that adds one. An object's metadata is not walked: it is
// not authorable.
func TestPolicyFreeKinds_NoDefaultedZeros(t *testing.T) {
	walked := map[string]string{}
	for _, kind := range policyFreeTypes {
		var skip []string
		if kind.wholeObject {
			skip = []string{"metadata"}
		}
		for path, doc := range omitemptyScalarDocs(t, kind.typ, skip...) {
			walked[kind.typ.String()+": "+path] = doc
		}
	}
	// Vacuity guard: the walk reads field comments. PriorityClass.globalDefault
	// is an omitempty boolean, so it is among what the walk finds.
	if _, ok := walked["v1.PriorityClass: globalDefault"]; !ok {
		t.Fatalf("the walk found %v, want PriorityClass globalDefault among them; the reflection walk is broken", slices.Sorted(maps.Keys(walked)))
	}
	for _, field := range slices.Sorted(maps.Keys(walked)) {
		m := docDefault.FindStringSubmatch(walked[field])
		if m == nil {
			continue
		}
		if def := strings.ToLower(m[1]); !crdDefaultIsZero(def) {
			t.Errorf("%s is omitted when zero and documents the default %s: an authored zero would be replaced; the kind needs a defaulted-zero list, which policyFreeKind does not carry", field, def)
		}
	}
}

// TestRefuseObjectIdentityKeys: a whole-object kind refuses the object's
// kind, apiVersion and metadata under any spelling the decode would match and
// whatever the value, and nothing else.
func TestRefuseObjectIdentityKeys(t *testing.T) {
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"no properties":          {nil, ""},
		"other fields":           {map[string]any{"provisioner": "p", "parameters": map[string]any{"kind": "gp3"}}, ""},
		"kind":                   {map[string]any{"kind": "StorageClass"}, "kind: not authorable"},
		"apiVersion":             {map[string]any{"apiVersion": "storage.k8s.io/v1"}, "apiVersion: not authorable"},
		"metadata":               {map[string]any{"metadata": map[string]any{"labels": map[string]any{"a": "b"}}}, "metadata: not authorable"},
		"null metadata":          {map[string]any{"metadata": nil}, "metadata: not authorable"},
		"another spelling":       {map[string]any{"Kind": "StorageClass"}, "Kind: not authorable"},
		"upper case":             {map[string]any{"APIVERSION": "v1"}, "APIVERSION: not authorable"},
		"first in sorted order":  {map[string]any{"metadata": nil, "kind": "x", "provisioner": "p"}, "kind: not authorable"},
		"a name that only holds": {map[string]any{"kinds": "x", "metadataPolicy": "y"}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			err := refuseObjectIdentityKeys(tc.props)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("err = %v, want none", err)
			case tc.want != "" && (err == nil || !strings.HasPrefix(err.Error(), tc.want)):
				t.Fatalf("err = %v, want one starting %q", err, tc.want)
			}
		})
	}
}
