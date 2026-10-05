package traits_test

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
	"github.com/go-kure/launcher/pkg/oam/internal/requiredfields"
)

// The cilium-networkpolicy trait refuses a field the CiliumNetworkPolicy CRD
// requires, under what the trait publishes, that the document left out. The
// Cilium rule type writes such a field whether or not it was authored, so the
// policy would carry an empty value nobody wrote. The list is
// requiredfields.CiliumTraitRule, held to the CRD of the linked module beside
// the component kinds' lists (TestCiliumNetworkPolicyTrait_RequiredMatchCRD).

// ciliumTraitExpression is a complete match expression on key.
func ciliumTraitExpression(key string) map[string]any {
	return map[string]any{"matchExpressions": []any{map[string]any{"key": key, "operator": "Exists"}}}
}

// ciliumCompleteTrait is a trait's properties with every object that holds a
// required field in it, each of them authored: what the
// cilium-networkpolicy-required-fields fixture authors.
func ciliumCompleteTrait() map[string]any {
	port := func(number string) []any {
		return []any{map[string]any{"port": number, "protocol": "TCP"}}
	}
	return map[string]any{
		"name":             "api-edge",
		"endpointSelector": ciliumTraitExpression("tier"),
		"ingress": []any{
			map[string]any{
				"fromEndpoints":  []any{ciliumTraitExpression("role")},
				"fromNodes":      []any{ciliumTraitExpression("node-role")},
				"fromCIDRSet":    []any{map[string]any{"cidrGroupSelector": ciliumTraitExpression("zone")}},
				"authentication": map[string]any{"mode": "required"},
				"toPorts": []any{
					map[string]any{
						"ports":          port("8443"),
						"terminatingTLS": map[string]any{"secret": map[string]any{"name": "api-edge-tls"}},
						"rules": map[string]any{"http": []any{map[string]any{
							"method": "GET",
							"headerMatches": []any{map[string]any{
								"name": "X-Token", "secret": map[string]any{"name": "api-edge-token"},
							}},
						}}},
					},
					map[string]any{
						"ports": port("9443"),
						"listener": map[string]any{
							"name": "edge", "priority": 1,
							"envoyConfig": map[string]any{"kind": "CiliumEnvoyConfig", "name": "api-edge-listener"},
						},
					},
				},
			},
			map[string]any{"icmps": []any{map[string]any{"fields": []any{map[string]any{"type": 8}}}}},
		},
		"egress": []any{
			map[string]any{
				"toEndpoints":    []any{ciliumTraitExpression("role")},
				"toNodes":        []any{ciliumTraitExpression("node-role")},
				"toCIDRSet":      []any{map[string]any{"cidrGroupSelector": ciliumTraitExpression("zone")}},
				"authentication": map[string]any{"mode": "disabled"},
				"toPorts": []any{map[string]any{
					"ports":          port("443"),
					"originatingTLS": map[string]any{"secret": map[string]any{"name": "api-upstream-ca"}},
				}},
			},
			map[string]any{"toServices": []any{map[string]any{
				"k8sServiceSelector": map[string]any{"selector": ciliumTraitExpression("role")},
			}}},
		},
	}
}

// ciliumTraitAt walks a property tree by map keys and list indexes and returns
// the object there.
func ciliumTraitAt(t *testing.T, node any, path ...any) map[string]any {
	t.Helper()
	for _, step := range path {
		switch s := step.(type) {
		case string:
			node = node.(map[string]any)[s]
		case int:
			node = node.([]any)[s]
		}
	}
	object, ok := node.(map[string]any)
	if !ok {
		t.Fatalf("%v holds %T, want an object", path, node)
	}
	return object
}

// buildCiliumTrait builds a trait as a document's is built: the handler's
// Apply, then the Generate of the application it appended.
func buildCiliumTrait(props map[string]any) (*ciliumv2.CiliumNetworkPolicy, error) {
	app := stack.NewApplication("api", "production", nil)
	bundle := &stack.Bundle{}
	trait := &oam.Trait{Type: "cilium-networkpolicy", Properties: props}
	if err := (&traits.CiliumNetworkPolicyHandler{}).Apply(trait, app, bundle); err != nil {
		return nil, err
	}
	objs, err := bundle.Applications[0].Generate()
	if err != nil {
		return nil, err
	}
	return (*objs[0]).(*ciliumv2.CiliumNetworkPolicy), nil
}

// wantCiliumTraitRefusal fails unless err refuses the required field at path.
func wantCiliumTraitRefusal(t *testing.T, err error, path string) {
	t.Helper()
	if want := `cilium-networkpolicy "api-edge": ` + path + ": required ("; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want one mentioning %q", err, want)
	}
}

// TestCiliumNetworkPolicyTrait_PublishesTheListedFields: the trait's required
// list is cut to requiredfields.CiliumTraitFields, so the trait must publish
// those fields of a rule and its own `name`, and nothing else.
func TestCiliumNetworkPolicyTrait_PublishesTheListedFields(t *testing.T) {
	got := slices.Sorted(maps.Keys((&traits.CiliumNetworkPolicyHandler{}).PropertySchema()))
	want := slices.Sorted(slices.Values(append([]string{"name"}, requiredfields.CiliumTraitFields()...)))
	if !slices.Equal(got, want) {
		t.Errorf("the trait publishes %v, its required list is cut to %v", got, want)
	}
}

// TestCiliumNetworkPolicyTrait_CompleteTraitIsEmittedAsWritten is the control:
// a trait that authors every required field builds, and the policy's spec is
// the three properties as they were written.
func TestCiliumNetworkPolicyTrait_CompleteTraitIsEmittedAsWritten(t *testing.T) {
	props := ciliumCompleteTrait()
	cnp, err := buildCiliumTrait(props)
	if err != nil {
		t.Fatalf("a complete trait is refused: %v", err)
	}
	authored := map[string]any{}
	for _, field := range requiredfields.CiliumTraitFields() {
		authored[field] = props[field]
	}
	tree := func(v any) any {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var out any
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got, want := tree(cnp.Spec), tree(authored); !reflect.DeepEqual(got, want) {
		t.Errorf("spec = %v\nauthored %v", got, want)
	}
}

// TestCiliumNetworkPolicyTrait_RefusesTheReproducingTrait: the trait that
// showed the defect. It built, and its policy carried five values the document
// does not hold: `operator: ""`, `mode: ""`, `secret: null`, `kind: ""` and
// `priority: 0`. The whole trait is refused at the first of them in path
// order, and each is refused alone, by its path, when the others are authored.
func TestCiliumNetworkPolicyTrait_RefusesTheReproducingTrait(t *testing.T) {
	reproducing := func() map[string]any {
		return map[string]any{
			"name":             "api-edge",
			"endpointSelector": map[string]any{"matchExpressions": []any{map[string]any{"key": "tier"}}},
			"ingress": []any{map[string]any{
				"authentication": map[string]any{},
				"toPorts": []any{map[string]any{
					"ports":          []any{map[string]any{"port": "8443", "protocol": "TCP"}},
					"terminatingTLS": map[string]any{},
					"listener": map[string]any{
						"name":        "edge",
						"envoyConfig": map[string]any{"name": "api-edge-listener"},
					},
				}},
			}},
		}
	}
	t.Run("the whole trait", func(t *testing.T) {
		_, err := buildCiliumTrait(reproducing())
		wantCiliumTraitRefusal(t, err, "endpointSelector.matchExpressions[0].operator")
	})

	for _, tc := range []struct {
		path   string
		parent []any
		field  string
		value  any
	}{
		{"endpointSelector.matchExpressions[0].operator", []any{"endpointSelector", "matchExpressions", 0}, "operator", "Exists"},
		{"ingress[0].authentication.mode", []any{"ingress", 0, "authentication"}, "mode", "required"},
		{"ingress[0].toPorts[0].terminatingTLS.secret", []any{"ingress", 0, "toPorts", 0, "terminatingTLS"}, "secret", map[string]any{"name": "api-edge-tls"}},
		{"ingress[0].toPorts[0].listener.envoyConfig.kind", []any{"ingress", 0, "toPorts", 0, "listener", "envoyConfig"}, "kind", "CiliumEnvoyConfig"},
		{"ingress[0].toPorts[0].listener.priority", []any{"ingress", 0, "toPorts", 0, "listener"}, "priority", 1},
	} {
		t.Run(tc.path, func(t *testing.T) {
			// Author the four others, so this one is the only field left out.
			props := reproducing()
			for _, other := range []struct {
				parent []any
				field  string
				value  any
			}{
				{[]any{"endpointSelector", "matchExpressions", 0}, "operator", "Exists"},
				{[]any{"ingress", 0, "authentication"}, "mode", "required"},
				{[]any{"ingress", 0, "toPorts", 0, "terminatingTLS"}, "secret", map[string]any{"name": "api-edge-tls"}},
				{[]any{"ingress", 0, "toPorts", 0, "listener", "envoyConfig"}, "kind", "CiliumEnvoyConfig"},
				{[]any{"ingress", 0, "toPorts", 0, "listener"}, "priority", 1},
			} {
				ciliumTraitAt(t, props, other.parent...)[other.field] = other.value
			}
			if _, err := buildCiliumTrait(props); err != nil {
				t.Fatalf("the trait with all five authored is refused: %v", err)
			}
			delete(ciliumTraitAt(t, props, tc.parent...), tc.field)
			_, err := buildCiliumTrait(props)
			wantCiliumTraitRefusal(t, err, tc.path)
		})
	}
}

// TestCiliumNetworkPolicyTrait_RefusesUnauthoredRequired leaves one required
// field out of the complete trait at a time: under the endpoint selector, an
// ingress entry and an egress entry.
func TestCiliumNetworkPolicyTrait_RefusesUnauthoredRequired(t *testing.T) {
	for name, tc := range map[string]struct {
		parent []any
		drop   string
		want   string
	}{
		"selector expression's key":         {[]any{"endpointSelector", "matchExpressions", 0}, "key", "endpointSelector.matchExpressions[0].key"},
		"selector expression's operator":    {[]any{"endpointSelector", "matchExpressions", 0}, "operator", "endpointSelector.matchExpressions[0].operator"},
		"ingress peer's operator":           {[]any{"ingress", 0, "fromEndpoints", 0, "matchExpressions", 0}, "operator", "ingress[0].fromEndpoints[0].matchExpressions[0].operator"},
		"ingress node peer's key":           {[]any{"ingress", 0, "fromNodes", 0, "matchExpressions", 0}, "key", "ingress[0].fromNodes[0].matchExpressions[0].key"},
		"ingress CIDR group selector":       {[]any{"ingress", 0, "fromCIDRSet", 0, "cidrGroupSelector", "matchExpressions", 0}, "operator", "ingress[0].fromCIDRSet[0].cidrGroupSelector.matchExpressions[0].operator"},
		"ingress authentication mode":       {[]any{"ingress", 0, "authentication"}, "mode", "ingress[0].authentication.mode"},
		"terminating TLS secret":            {[]any{"ingress", 0, "toPorts", 0, "terminatingTLS"}, "secret", "ingress[0].toPorts[0].terminatingTLS.secret"},
		"terminating TLS secret's name":     {[]any{"ingress", 0, "toPorts", 0, "terminatingTLS", "secret"}, "name", "ingress[0].toPorts[0].terminatingTLS.secret.name"},
		"header match's name":               {[]any{"ingress", 0, "toPorts", 0, "rules", "http", 0, "headerMatches", 0}, "name", "ingress[0].toPorts[0].rules.http[0].headerMatches[0].name"},
		"header match's secret name":        {[]any{"ingress", 0, "toPorts", 0, "rules", "http", 0, "headerMatches", 0, "secret"}, "name", "ingress[0].toPorts[0].rules.http[0].headerMatches[0].secret.name"},
		"listener's name":                   {[]any{"ingress", 0, "toPorts", 1, "listener"}, "name", "ingress[0].toPorts[1].listener.name"},
		"listener's priority":               {[]any{"ingress", 0, "toPorts", 1, "listener"}, "priority", "ingress[0].toPorts[1].listener.priority"},
		"listener's Envoy configuration":    {[]any{"ingress", 0, "toPorts", 1, "listener"}, "envoyConfig", "ingress[0].toPorts[1].listener.envoyConfig"},
		"Envoy configuration's kind":        {[]any{"ingress", 0, "toPorts", 1, "listener", "envoyConfig"}, "kind", "ingress[0].toPorts[1].listener.envoyConfig.kind"},
		"Envoy configuration's name":        {[]any{"ingress", 0, "toPorts", 1, "listener", "envoyConfig"}, "name", "ingress[0].toPorts[1].listener.envoyConfig.name"},
		"egress peer's key":                 {[]any{"egress", 0, "toEndpoints", 0, "matchExpressions", 0}, "key", "egress[0].toEndpoints[0].matchExpressions[0].key"},
		"egress node peer's operator":       {[]any{"egress", 0, "toNodes", 0, "matchExpressions", 0}, "operator", "egress[0].toNodes[0].matchExpressions[0].operator"},
		"egress CIDR group selector":        {[]any{"egress", 0, "toCIDRSet", 0, "cidrGroupSelector", "matchExpressions", 0}, "key", "egress[0].toCIDRSet[0].cidrGroupSelector.matchExpressions[0].key"},
		"egress authentication mode":        {[]any{"egress", 0, "authentication"}, "mode", "egress[0].authentication.mode"},
		"originating TLS secret":            {[]any{"egress", 0, "toPorts", 0, "originatingTLS"}, "secret", "egress[0].toPorts[0].originatingTLS.secret"},
		"originating TLS secret's name":     {[]any{"egress", 0, "toPorts", 0, "originatingTLS", "secret"}, "name", "egress[0].toPorts[0].originatingTLS.secret.name"},
		"Service selector":                  {[]any{"egress", 1, "toServices", 0, "k8sServiceSelector"}, "selector", "egress[1].toServices[0].k8sServiceSelector.selector"},
		"Service selector's expression key": {[]any{"egress", 1, "toServices", 0, "k8sServiceSelector", "selector", "matchExpressions", 0}, "key", "egress[1].toServices[0].k8sServiceSelector.selector.matchExpressions[0].key"},
	} {
		t.Run(name, func(t *testing.T) {
			props := ciliumCompleteTrait()
			parent := ciliumTraitAt(t, props, tc.parent...)
			if _, ok := parent[tc.drop]; !ok {
				t.Fatalf("the complete trait has no %s to leave out", tc.want)
			}
			delete(parent, tc.drop)
			_, err := buildCiliumTrait(props)
			wantCiliumTraitRefusal(t, err, tc.want)
		})
	}
}

// TestCiliumNetworkPolicyTrait_NullIsUnauthored: a required field written as
// null is one left out, and a null entry of a list is an entry with nothing
// authored in it: the type builds the empty entry from it all the same.
func TestCiliumNetworkPolicyTrait_NullIsUnauthored(t *testing.T) {
	for name, tc := range map[string]struct {
		parent []any
		field  string
		value  any
		want   string
	}{
		"null operator":         {[]any{"endpointSelector", "matchExpressions", 0}, "operator", nil, "endpointSelector.matchExpressions[0].operator"},
		"null mode":             {[]any{"egress", 0, "authentication"}, "mode", nil, "egress[0].authentication.mode"},
		"null secret":           {[]any{"ingress", 0, "toPorts", 0, "terminatingTLS"}, "secret", nil, "ingress[0].toPorts[0].terminatingTLS.secret"},
		"null priority":         {[]any{"ingress", 0, "toPorts", 1, "listener"}, "priority", nil, "ingress[0].toPorts[1].listener.priority"},
		"null match expression": {[]any{"endpointSelector"}, "matchExpressions", []any{nil}, "endpointSelector.matchExpressions[0].key"},
		"null header match":     {[]any{"ingress", 0, "toPorts", 0, "rules", "http", 0}, "headerMatches", []any{nil}, "ingress[0].toPorts[0].rules.http[0].headerMatches[0].name"},
		"null peer after a complete one": {[]any{"egress", 0}, "toEndpoints", []any{ciliumTraitExpression("role"), map[string]any{"matchExpressions": []any{nil}}},
			"egress[0].toEndpoints[1].matchExpressions[0].key"},
	} {
		t.Run(name, func(t *testing.T) {
			props := ciliumCompleteTrait()
			ciliumTraitAt(t, props, tc.parent...)[tc.field] = tc.value
			_, err := buildCiliumTrait(props)
			wantCiliumTraitRefusal(t, err, tc.want)
		})
	}
}

// TestCiliumNetworkPolicyTrait_ReadsTypedCollections: a consumer that builds
// the properties in Go may hold them in typed maps and slices, which marshal as
// a document's do. The required check reads what is marshalled, not the Go
// shape, so the omission is refused there too.
func TestCiliumNetworkPolicyTrait_ReadsTypedCollections(t *testing.T) {
	t.Run("typed selector", func(t *testing.T) {
		props := ciliumCompleteTrait()
		props["endpointSelector"] = map[string][]map[string]string{"matchExpressions": {{"key": "tier"}}}
		_, err := buildCiliumTrait(props)
		wantCiliumTraitRefusal(t, err, "endpointSelector.matchExpressions[0].operator")
	})
	t.Run("typed rule list", func(t *testing.T) {
		props := ciliumCompleteTrait()
		props["ingress"] = []map[string]any{{"authentication": map[string]string{}}}
		_, err := buildCiliumTrait(props)
		wantCiliumTraitRefusal(t, err, "ingress[0].authentication.mode")
	})
	t.Run("complete typed selector", func(t *testing.T) {
		props := ciliumCompleteTrait()
		props["endpointSelector"] = map[string][]map[string]string{"matchExpressions": {{"key": "tier", "operator": "Exists"}}}
		if _, err := buildCiliumTrait(props); err != nil {
			t.Fatalf("a complete trait held in typed collections is refused: %v", err)
		}
	})
}

// TestCiliumNetworkPolicyTrait_EverySpellingIsHeldToTheList: field names match
// case-insensitively in the decode, so a rule may write one field in two
// spellings, of which the decode keeps one or merges both. The required check
// cannot tell which, so each spelling is held to the list. Before, the check
// read the first spelling only: `FROMENDPOINTS` here, complete, while the
// policy was built from `FromEndpoints` and carried an `operator: ""` nobody
// wrote.
func TestCiliumNetworkPolicyTrait_EverySpellingIsHeldToTheList(t *testing.T) {
	expression := func(fields map[string]any) []any {
		return []any{map[string]any{"matchExpressions": []any{fields}}}
	}
	complete := map[string]any{"key": "role", "operator": "Exists"}
	trait := func(entry map[string]any) map[string]any {
		return map[string]any{
			"name":             "api-edge",
			"endpointSelector": map[string]any{},
			"ingress":          []any{entry},
		}
	}

	t.Run("the spelling the decode keeps leaves the operator out", func(t *testing.T) {
		_, err := buildCiliumTrait(trait(map[string]any{
			"FROMENDPOINTS": expression(complete),
			"FromEndpoints": expression(map[string]any{"key": "role"}),
		}))
		wantCiliumTraitRefusal(t, err, "ingress[0].FromEndpoints[0].matchExpressions[0].operator")
	})
	t.Run("the other spelling leaves it out", func(t *testing.T) {
		_, err := buildCiliumTrait(trait(map[string]any{
			"FROMENDPOINTS": expression(map[string]any{"key": "role"}),
			"FromEndpoints": expression(complete),
		}))
		wantCiliumTraitRefusal(t, err, "ingress[0].FROMENDPOINTS[0].matchExpressions[0].operator")
	})
	// The decode merges the two spellings of a struct, so this policy would
	// carry the mode. The spelling that leaves it out is refused all the same:
	// the check does not follow the decode's choice, it holds each spelling.
	t.Run("a struct in two spellings", func(t *testing.T) {
		_, err := buildCiliumTrait(trait(map[string]any{
			"AUTHENTICATION": map[string]any{},
			"authentication": map[string]any{"mode": "required"},
		}))
		wantCiliumTraitRefusal(t, err, "ingress[0].AUTHENTICATION.mode")
	})
	t.Run("both spellings complete", func(t *testing.T) {
		if _, err := buildCiliumTrait(trait(map[string]any{
			"FROMENDPOINTS": expression(complete),
			"FromEndpoints": expression(complete),
		})); err != nil {
			t.Fatalf("a trait whose two spellings both author the field is refused by the list: %v", err)
		}
	})
	t.Run("one spelling, not the json name", func(t *testing.T) {
		if _, err := buildCiliumTrait(trait(map[string]any{"FROMENDPOINTS": expression(complete)})); err != nil {
			t.Fatalf("refused: %v", err)
		}
		_, err := buildCiliumTrait(trait(map[string]any{"FROMENDPOINTS": expression(map[string]any{"key": "role"})}))
		wantCiliumTraitRefusal(t, err, "ingress[0].FROMENDPOINTS[0].matchExpressions[0].operator")
	})
}

// TestCiliumNetworkPolicyTrait_ParentLeftOutRequiresNothing: a required field
// is required where its parent is authored. A trait that holds none of those
// parents builds as it did.
func TestCiliumNetworkPolicyTrait_ParentLeftOutRequiresNothing(t *testing.T) {
	for name, props := range map[string]map[string]any{
		"labels only": {
			"name":             "api-edge",
			"endpointSelector": map[string]any{"matchLabels": map[string]any{"tier": "edge"}},
			"egress":           []any{map[string]any{"toEndpoints": []any{map[string]any{}}}},
		},
		"select-all and ports": {
			"name":             "api-edge",
			"endpointSelector": map[string]any{},
			"ingress": []any{map[string]any{"toPorts": []any{map[string]any{
				"ports": []any{map[string]any{"port": "8443", "protocol": "TCP"}},
				"rules": map[string]any{"http": []any{map[string]any{"method": "GET", "path": "/healthz"}}},
			}}}},
		},
		"null list of expressions": {
			"name":             "api-edge",
			"endpointSelector": map[string]any{"matchLabels": map[string]any{"tier": "edge"}, "matchExpressions": nil},
			"egress":           []any{map[string]any{"toEndpoints": []any{map[string]any{}}, "authentication": nil}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := buildCiliumTrait(props); err != nil {
				t.Fatalf("refused: %v", err)
			}
		})
	}
}
