package registry_test

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/probe"
	"github.com/go-kure/launcher/pkg/oam/builtin/registry"
)

// traitRulesWithoutKeyMap is rulesWithoutKeyMap for the built-in trait lowering
// rules. It is empty: a rule that cannot publish a map needs a row here.
var traitRulesWithoutKeyMap = map[string]string{}

// keyedTraitRules returns the built-in trait lowering rules that publish a key
// map, by the trait type they claim.
func keyedTraitRules() map[string]oam.LoweringKeyDeclarer {
	out := map[string]oam.LoweringKeyDeclarer{}
	for name, r := range registry.TraitLoweringRules() {
		if d, ok := r.(oam.LoweringKeyDeclarer); ok {
			out[name] = d
		}
	}
	return out
}

// Every built-in trait lowering rule publishes a key map or is named in
// traitRulesWithoutKeyMap, never both and never neither.
func TestLoweringKeys_EveryBuiltinTraitRuleHasAMapOrAReason(t *testing.T) {
	keyed := keyedTraitRules()
	for name := range registry.TraitLoweringRules() {
		_, hasMap := keyed[name]
		reason, exempt := traitRulesWithoutKeyMap[name]
		switch {
		case hasMap && exempt:
			t.Errorf("rule %q publishes a key map and is listed as publishing none; remove its row", name)
		case !hasMap && !exempt:
			t.Errorf("trait rule %q publishes no key map (oam.LoweringKeyDeclarer) and has no row in traitRulesWithoutKeyMap", name)
		case exempt && strings.TrimSpace(reason) == "":
			t.Errorf("rule %q is listed with no reason", name)
		}
	}
	for name := range traitRulesWithoutKeyMap {
		if _, registered := registry.TraitLoweringRules()[name]; !registered {
			t.Errorf("traitRulesWithoutKeyMap names %q, which is not a built-in trait lowering rule", name)
		}
	}
}

// exposeRoutes is the trait the expose rule emits under each controllerType.
var exposeRoutes = map[string]string{"ingress": "ingress", "gateway": "httproute"}

// The expose map and schema hold the same keys. Every place has no member, names
// a trait the rule declares it lowers into, and is conditioned on the
// controllerType that emits that trait and on nothing else.
func TestLoweringKeys_EveryTraitSchemaKeyHasAnEntry(t *testing.T) {
	d, ok := keyedTraitRules()["expose"]
	if !ok {
		t.Fatal(`trait rule "expose" does not implement oam.LoweringKeyDeclarer`)
	}
	schema := d.(oam.PropertySchemaProvider).PropertySchema()
	keys := d.LoweringKeys()
	if got, want := slices.Sorted(maps.Keys(keys)), slices.Sorted(maps.Keys(schema)); !slices.Equal(got, want) {
		t.Errorf("key map keys = %v\nschema keys  = %v", got, want)
	}
	targets := d.(oam.LoweringTargetDeclarer).LoweringTargets()
	for key, places := range keys {
		for _, p := range places {
			if p.Member != "" {
				t.Errorf("%s: a trait place names the member %q, want none", key, p.Member)
			}
			if !slices.Contains(targets.TraitTypes, p.Trait) {
				t.Errorf("%s: trait %q is not a trait type the rule lowers into (%v)", key, p.Trait, targets.TraitTypes)
			}
			if len(p.When) != 1 || p.When[0].Key != "controllerType" || len(p.When[0].Values) != 1 ||
				exposeRoutes[p.When[0].Values[0]] != p.Trait {
				t.Errorf("%s: %v is not conditioned on the controllerType that emits %q alone", key, p, p.Trait)
			}
			// The rule hands a value on as written only under the key's own name.
			if p.Unchanged != (p.Path == key) {
				t.Errorf("%s: %v: Unchanged = %v, want %v", key, p, p.Unchanged, p.Path == key)
			}
		}
	}
	// "controllerType" chooses the trait and "allowedHostnameWildcard" is only
	// checked against the hostnames: neither lands anywhere.
	for _, key := range []string{"controllerType", "allowedHostnameWildcard"} {
		if places := keys[key]; len(places) != 0 {
			t.Errorf("%q declares %v, want no place", key, places)
		}
	}
	if got := newTransformer().TraitLoweringKeys()["expose"]; !reflect.DeepEqual(got, keys) {
		t.Errorf("Transformer.TraitLoweringKeys()[\"expose\"] = %v, the rule declares %v", got, keys)
	}
}

// exposeLeaves lowers an expose trait with the merged properties props through
// the rule alone, and returns every leaf of the trait it emits. The leaves have
// no member: the emitted trait stays on whichever member holds it.
func exposeLeaves(t *testing.T, props map[string]any) (map[reportedAt]any, map[string]map[string]any) {
	t.Helper()
	ctx := lowerContext()
	ctx.Component = &oam.Component{Name: "app", Type: "webservice"}
	res, err := registry.TraitLoweringRules()["expose"].LowerTrait(&oam.Trait{Type: "expose", Properties: props}, ctx)
	if err != nil {
		t.Fatalf("LowerTrait(%v): %v", props, err)
	}
	if len(res.Traits) != 1 || len(res.Components) != 0 {
		t.Fatalf("LowerTrait(%v) emits %d traits and %d components, want one trait", props, len(res.Traits), len(res.Components))
	}
	leaves := map[reportedAt]any{}
	flattenLeaves(leaves, "", res.Traits[0].Type, "", res.Traits[0].Properties)
	return leaves, map[string]map[string]any{"": res.Traits[0].Properties}
}

func exposeLoweredType() loweredType {
	return loweredType{
		lower:    exposeLeaves,
		variants: exposeKeyVariants(),
		switches: map[string]string{"controllerType": "chooses the trait the rule emits"},
	}
}

// exposeKeyVariants holds the variants of every expose schema key that is no
// switch. The properties are the merged ones the rule is handed.
func exposeKeyVariants() map[string][]keyVariant {
	with := func(base map[string]any, more ...any) map[string]any {
		out := maps.Clone(base)
		for i := 0; i < len(more); i += 2 {
			out[more[i].(string)] = more[i+1]
		}
		return out
	}
	var (
		ing     = map[string]any{"controllerType": "ingress"}
		gw      = map[string]any{"controllerType": "gateway", "gatewayName": "gw", "gatewayNamespace": "gateways"}
		hosts   = []any{"a.example.org"}
		rules   = []any{map[string]any{"host": "a.example.org", "paths": []any{map[string]any{}}}}
		routes  = []any{map[string]any{"backendRefs": []any{map[string]any{"name": "other", "port": 80}}}}
		issued  = with(ing, "certManagerClusterIssuer", "issuer")
		groups  = []any{"ops"}
		authed  = with(ing, "allowedGroups", groups, "authURL", "https://auth.example.org/oauth2/auth")
		netpol  = map[string]any{"trafficSources": []any{map[string]any{"namespace": "edge"}}}
		ingress = func(path string) reportedAt { return reportedAt{trait: "ingress", path: path} }
		route   = func(path string) reportedAt { return reportedAt{trait: "httproute", path: path} }
	)
	// A key the rule hands to either trait under its own name.
	both := func(key string, value any) []keyVariant {
		return []keyVariant{
			{name: "ingress", base: ing, value: value, moves: []reportedAt{ingress(key)}},
			{name: "gateway", base: gw, value: value, moves: []reportedAt{route(key)}},
		}
	}
	// A typed property or capability value the rule turns into an annotation.
	annotation := func(base map[string]any, value any) []keyVariant {
		return []keyVariant{{name: "ingress", base: base, value: value, moves: []reportedAt{ingress("platformAnnotations")}}}
	}
	// An external-auth value, which writes nothing without allowedGroups.
	auth := func(value any) []keyVariant {
		return append(annotation(authed, value), keyVariant{name: "ingress without allowedGroups", base: ing,
			value: value, idle: []reportedAt{ingress("platformAnnotations")}})
	}
	return map[string][]keyVariant{
		"annotations":   both("annotations", map[string]any{"example.org/a": "b"}),
		"servicePort":   both("servicePort", 8080),
		"serviceName":   both("serviceName", "other"),
		"name":          both("name", "web"),
		"scope":         both("scope", "public"),
		"networkPolicy": both("networkPolicy", netpol),
		"rules": {
			{name: "ingress with an issuer", base: issued, value: rules,
				moves: []reportedAt{ingress("rules"), ingress("managedTLS.hosts")}},
			{name: "ingress without one", base: ing, value: rules,
				moves: []reportedAt{ingress("rules")}, idle: []reportedAt{ingress("managedTLS.hosts")}},
			{name: "gateway", base: gw, value: routes, moves: []reportedAt{route("rules")}},
		},
		"hostnames": {
			{name: "ingress with an issuer", base: issued, value: hosts,
				moves: []reportedAt{ingress("rules"), ingress("managedTLS.hosts")}},
			{name: "ingress without one", base: ing, value: hosts,
				moves: []reportedAt{ingress("rules")}, idle: []reportedAt{ingress("managedTLS.hosts")}},
			{name: "ingress beside rules", base: with(issued, "rules", rules), value: hosts,
				idle: []reportedAt{ingress("rules"), ingress("managedTLS.hosts")}},
			{name: "gateway", base: gw, value: hosts, moves: []reportedAt{route("hostnames"), route("rules")}},
			{name: "gateway beside rules", base: with(gw, "rules", routes), value: hosts,
				moves: []reportedAt{route("hostnames")}, idle: []reportedAt{route("rules")}},
		},
		"certManagerClusterIssuer": {{name: "ingress", base: with(ing, "rules", rules), value: "issuer",
			moves: []reportedAt{ingress("platformAnnotations"), ingress("managedTLS")}}},
		"secretName": {{name: "ingress", base: with(issued, "rules", rules), value: "tls-a",
			moves: []reportedAt{ingress("managedTLS.secretName")}}},
		"ingressClassName": {{name: "ingress", base: ing, value: "nginx", moves: []reportedAt{ingress("ingressClassName")}}},
		"sslRedirect":      annotation(ing, true),
		"forceSslRedirect": annotation(ing, true),
		"allowedGroups":    annotation(with(ing, "authURL", "https://auth.example.org/oauth2/auth"), groups),
		// The base holds authURL already: allowedGroups without it is refused.
		"authURL":                 auth("https://other.example.org/oauth2/auth"),
		"authSigninURL":           auth("https://auth.example.org/signin"),
		"authResponseHeaders":     auth("X-User"),
		"gatewayName":             {{name: "gateway", base: gw, value: "other", moves: []reportedAt{route("parentRefs")}}},
		"gatewayNamespace":        {{name: "gateway", base: gw, value: "other", moves: []reportedAt{route("parentRefs")}}},
		"allowedHostnameWildcard": {{name: "ingress", base: with(ing, "rules", rules), value: "*.example.org"}},
	}
}

// A key with no place under a controllerType reaches no trait of a document that
// builds: handed the key there, the rule emits the same trait, or refuses, or
// keeps a property the emitted trait's schema does not have, which the transform
// refuses. The last group is listed, so a key that joins or leaves it fails.
func TestLoweringKeys_ExposeKeyWithNoPlaceReachesNoTrait(t *testing.T) {
	d, ok := keyedTraitRules()["expose"]
	if !ok {
		t.Fatal(`trait rule "expose" does not implement oam.LoweringKeyDeclarer`)
	}
	keys := d.LoweringKeys()
	bindings := map[string]map[string]any{
		"ingress": {"controllerType": "ingress", "ingressClassName": "nginx"},
		"gateway": {"controllerType": "gateway", "gatewayName": "gw", "gatewayNamespace": "gateways"},
	}
	refusedByTransform := map[string][]string{
		"ingress": {"gatewayName", "gatewayNamespace"},
		"gateway": {"authResponseHeaders", "authURL", "ingressClassName"},
	}
	for _, controllerType := range slices.Sorted(maps.Keys(bindings)) {
		binding := bindings[controllerType]
		var kept []string
		for _, key := range slices.Sorted(maps.Keys(keys)) {
			if key == "controllerType" || slices.ContainsFunc(keys[key], func(p oam.LoweredKey) bool { return p.LiveFor(binding) }) {
				continue
			}
			variants := exposeKeyVariants()[key]
			if len(variants) == 0 {
				t.Fatalf("no variant gives a value for %q", key)
			}
			// A value the binding does not hold already.
			var value any = "other-value"
			if _, isString := variants[0].value.(string); !isString {
				value = variants[0].value
			}
			with := maps.Clone(binding)
			with[key] = value
			ctx := lowerContext()
			ctx.Component = &oam.Component{Name: "app", Type: "webservice"}
			rule := registry.TraitLoweringRules()["expose"]
			got, err := rule.LowerTrait(&oam.Trait{Type: "expose", Properties: with}, ctx)
			if err != nil {
				continue
			}
			base, err := rule.LowerTrait(&oam.Trait{Type: "expose", Properties: binding}, ctx)
			if err != nil {
				t.Fatalf("LowerTrait(%v): %v", binding, err)
			}
			if reflect.DeepEqual(got.Traits[0].Properties, base.Traits[0].Properties) {
				continue
			}
			kept = append(kept, key)
			comp := oam.Component{Name: "app", Type: "webservice", Properties: map[string]any{"image": "ghcr.io/org/app:v1"},
				Traits: []oam.Trait{{Type: "expose", Properties: map[string]any{"hostnames": []any{"a.example.org"}}}}}
			_, _, err = probe.RunWith(newTransformer(), []oam.Component{comp}, probe.NoPolicy(),
				map[string]oam.CapabilityBinding{"expose": {Rendering: with}})
			if want := `unsupported field "` + key + `"`; err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s: %q is kept on the emitted trait and has no place; the transform answers %v, want %s", controllerType, key, err, want)
			}
		}
		if !slices.Equal(kept, refusedByTransform[controllerType]) {
			t.Errorf("%s: keys the rule keeps with no place = %v, want %v", controllerType, kept, refusedByTransform[controllerType])
		}
	}
}

// TestLoweringKeys_AuthoredValueReachesTheDeclaredPlaces for the trait rule: the
// expose variants run through the same loweredVariants.
func TestLoweringKeys_TraitValueReachesTheDeclaredPlaces(t *testing.T) {
	d, ok := keyedTraitRules()["expose"]
	if !ok {
		t.Fatal(`trait rule "expose" does not implement oam.LoweringKeyDeclarer`)
	}
	keys := d.LoweringKeys()
	for _, key := range slices.Sorted(maps.Keys(d.(oam.PropertySchemaProvider).PropertySchema())) {
		t.Run("expose/"+key, func(t *testing.T) {
			loweredVariants(t, "expose", key, exposeLoweredType(), keys[key])
		})
	}
}

// The report names an expose trait's places under whichever member of the
// component holds the emitted trait, so a trait place is matched by component,
// trait and path. When is read on the properties after the capability merge:
// controllerType comes from the binding's rendering.
func TestLoweringKeys_ExposeInTheReport(t *testing.T) {
	d, ok := keyedTraitRules()["expose"]
	if !ok {
		t.Fatal(`trait rule "expose" does not implement oam.LoweringKeyDeclarer`)
	}
	keys := d.LoweringKeys()
	const image = "ghcr.io/org/app:v1"
	annotations := map[string]any{"example.org/a": "b"}
	bindings := map[string]struct {
		rendering map[string]any
		// landed are keys whose every live place holds an entry in this document.
		landed []string
	}{
		"ingress": {
			rendering: map[string]any{"controllerType": "ingress", "ingressClassName": "nginx", "certManagerClusterIssuer": "issuer"},
			landed:    []string{"annotations", "certManagerClusterIssuer", "hostnames", "ingressClassName"},
		},
		"gateway": {
			rendering: map[string]any{"controllerType": "gateway", "gatewayName": "gw", "gatewayNamespace": "gateways"},
			landed:    []string{"annotations", "gatewayName", "gatewayNamespace", "hostnames"},
		},
	}
	components := map[string]struct {
		typ    string
		props  map[string]any
		expose map[string]any
		member string
	}{
		"webservice": {typ: "webservice", props: map[string]any{"image": image}, member: "service"},
		"worker": {typ: "worker", props: map[string]any{"image": image},
			expose: map[string]any{"servicePort": 80}, member: "role-deployment"},
		"worker with an account": {typ: "worker", props: map[string]any{"image": image, "serviceAccountName": "existing"},
			expose: map[string]any{"servicePort": 80}, member: ""},
	}
	for _, bindingName := range slices.Sorted(maps.Keys(bindings)) {
		binding := bindings[bindingName]
		for _, name := range slices.Sorted(maps.Keys(components)) {
			c := components[name]
			t.Run(bindingName+"/"+name, func(t *testing.T) {
				authored := map[string]any{"hostnames": []any{"a.example.org"}, "annotations": annotations}
				maps.Copy(authored, c.expose)
				merged := maps.Clone(binding.rendering)
				maps.Copy(merged, authored)
				comp := oam.Component{Name: "app", Type: c.typ, Properties: c.props,
					Traits: []oam.Trait{{Type: "expose", Properties: authored}}}
				prov, _, err := probe.RunWith(newTransformer(), []oam.Component{comp}, probe.NoPolicy(),
					map[string]oam.CapabilityBinding{"expose": {Rendering: binding.rendering}})
				if err != nil {
					t.Fatalf("transform: %v", err)
				}
				trait := exposeRoutes[bindingName]
				for _, e := range prov.Entries {
					for _, other := range exposeRoutes {
						if e.Trait == other && other != trait {
							t.Errorf("the report holds %q under %q, which this binding does not emit", e.Path, e.Trait)
						}
					}
					if e.Trait == trait && e.Member != c.member {
						t.Errorf("entry %q of %q is under member %q, want %q", e.Path, e.Trait, e.Member, c.member)
					}
				}
				landed := slices.Clone(binding.landed)
				for key := range c.expose {
					landed = append(landed, key)
				}
				for _, key := range landed {
					var live int
					for _, p := range keys[key] {
						// Without the merge no condition can be met.
						if p.LiveFor(authored) {
							t.Errorf("%q: %v is live for the authored properties alone", key, p)
						}
						if !p.LiveFor(merged) {
							continue
						}
						live++
						if p.Trait != trait {
							t.Errorf("%q: %v is live under the %s binding", key, p, bindingName)
						}
						// The entry's member is left out of the match: it was
						// checked above, and the place names none.
						var found, copied bool
						for _, e := range prov.Entries {
							if e.Component == "app" && within(reportedAt{p.Member, e.Trait, e.Path}, p) {
								found = true
								copied = copied || e.Source.Kind == oam.SourceCopied
							}
						}
						switch {
						case !found:
							t.Errorf("%q: the report holds no entry at the live place %v", key, p)
						case p.Unchanged && !copied:
							t.Errorf("%q: no entry at the unchanged place %v is copied", key, p)
						}
					}
					if live == 0 {
						t.Errorf("%q has no live place under the %s binding: %v", key, bindingName, keys[key])
					}
				}
			})
		}
	}
}
