package components

import (
	stdjson "encoding/json"
	"maps"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/go-kure/launcher/pkg/oam"
)

// gatewayRouteServicePort is the one expression rule of the route CRDs the
// kinds check (gatewayRouteBackendRefs), by the path of the value it is
// declared on and its text. Both channels declare it.
const gatewayRouteServicePort = `spec.rules[].backendRefs[]: (size(self.group) == 0 && self.kind == 'Service') ? has(self.port) : true`

// gatewayRouteParentsLeft says why the rules of a route's parents are left to
// the API server.
const gatewayRouteParentsLeft = "the two channels of the API hold different rules for the parents of a route: the experimental one tells two references to one parent apart by their port too, which the standard one does not read, so a refusal by the kind would be wider than one of them"

// gatewayRouteParentRules are those rules: in each channel, that several
// references to one parent all name what tells them apart, and that no two are
// the same.
var gatewayRouteParentRules = map[string]string{
	// The experimental channel.
	`spec.parentRefs: self.all(p1, self.all(p2, p1.group == p2.group && p1.kind == p2.kind && p1.name == p2.name && (((!has(p1.__namespace__) || p1.__namespace__ == '') && (!has(p2.__namespace__) || p2.__namespace__ == '')) || (has(p1.__namespace__) && has(p2.__namespace__) && p1.__namespace__ == p2.__namespace__)) ? ((!has(p1.sectionName) || p1.sectionName == '') == (!has(p2.sectionName) || p2.sectionName == '') && (!has(p1.port) || p1.port == 0) == (!has(p2.port) || p2.port == 0)): true))`:                                                                                                                                                       gatewayRouteParentsLeft,
	`spec.parentRefs: self.all(p1, self.exists_one(p2, p1.group == p2.group && p1.kind == p2.kind && p1.name == p2.name && (((!has(p1.__namespace__) || p1.__namespace__ == '') && (!has(p2.__namespace__) || p2.__namespace__ == '')) || (has(p1.__namespace__) && has(p2.__namespace__) && p1.__namespace__ == p2.__namespace__ )) && (((!has(p1.sectionName) || p1.sectionName == '') && (!has(p2.sectionName) || p2.sectionName == '')) || ( has(p1.sectionName) && has(p2.sectionName) && p1.sectionName == p2.sectionName)) && (((!has(p1.port) || p1.port == 0) && (!has(p2.port) || p2.port == 0)) || (has(p1.port) && has(p2.port) && p1.port == p2.port))))`: gatewayRouteParentsLeft,
	// The standard channel.
	`spec.parentRefs: self.all(p1, self.all(p2, p1.group == p2.group && p1.kind == p2.kind && p1.name == p2.name && (((!has(p1.__namespace__) || p1.__namespace__ == '') && (!has(p2.__namespace__) || p2.__namespace__ == '')) || (has(p1.__namespace__) && has(p2.__namespace__) && p1.__namespace__ == p2.__namespace__ )) ? ((!has(p1.sectionName) || p1.sectionName == '') == (!has(p2.sectionName) || p2.sectionName == '')) : true))`:                                                                                        gatewayRouteParentsLeft,
	`spec.parentRefs: self.all(p1, self.exists_one(p2, p1.group == p2.group && p1.kind == p2.kind && p1.name == p2.name && (((!has(p1.__namespace__) || p1.__namespace__ == '') && (!has(p2.__namespace__) || p2.__namespace__ == '')) || (has(p1.__namespace__) && has(p2.__namespace__) && p1.__namespace__ == p2.__namespace__ )) && (((!has(p1.sectionName) || p1.sectionName == '') && (!has(p2.sectionName) || p2.sectionName == '')) || (has(p1.sectionName) && has(p2.sectionName) && p1.sectionName == p2.sectionName))))`: gatewayRouteParentsLeft,
}

// gatewayRouteHostnamesLeft says why the rules of a TLSRoute's host names are
// left to the API server.
const gatewayRouteHostnamesLeft = "the rule holds the form of a value that is authored, which the kinds leave to the API server, as they leave a pattern or a length"

// gatewayRouteTLSRules are the rules a tlsroute leaves to the API server:
// those of the parents, and the three that hold the form of a host name. Both
// channels declare the three alike.
var gatewayRouteTLSRules = func() map[string]string {
	left := maps.Clone(gatewayRouteParentRules)
	for _, rule := range []string{
		`self.all(h, !isIP(h))`,
		`self.all(h, !h.contains('*') ? h.matches('^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*)$') : true)`,
		`self.all(h, h.contains('*') ? (h.startsWith('*.') && h.substring(2).matches('^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*)$')) : true)`,
	} {
		left["spec.hostnames: "+rule] = gatewayRouteHostnamesLeft
	}
	return left
}()

// gatewayRouteKinds lists the kind components of the Gateway API's routes that
// carry no HTTP, with the CRD each emits an object of.
//
// base is what the API requires of the route beside its rules, and left names
// the expression rules of the CRD, in either channel, that the kind leaves to
// the API server, each with the reason. A rule that is neither there nor
// gatewayRouteServicePort fails TestGatewayRouteKinds_ExpressionRules.
var gatewayRouteKinds = []struct {
	component string
	handler   oam.ComponentHandler
	crd       string
	base      map[string]any
	left      map[string]string
}{
	{component: "tcproute", handler: &TCPRouteHandler{}, crd: "tcproutes", left: gatewayRouteParentRules},
	{component: "udproute", handler: &UDPRouteHandler{}, crd: "udproutes", left: gatewayRouteParentRules},
	{
		component: "tlsroute", handler: &TLSRouteHandler{}, crd: "tlsroutes",
		base: map[string]any{"hostnames": []any{"db.example.com"}},
		left: gatewayRouteTLSRules,
	},
}

// gatewayRouteProperties is the properties of a route of the kind with base
// and one rule that holds the backends.
func gatewayRouteProperties(base map[string]any, backends ...any) map[string]any {
	props := maps.Clone(base)
	if props == nil {
		props = map[string]any{}
	}
	props["rules"] = []any{map[string]any{"backendRefs": append([]any{}, backends...)}}
	return props
}

// gatewayRouteChecked prepares the route CRD of one channel as the API server
// serves it, for a kind whose properties are the object's spec.
func gatewayRouteChecked(t *testing.T, component string, handler oam.ComponentHandler, crd *apiextensionsv1.CustomResourceDefinition) checkedRules {
	t.Helper()
	return checkedRules{
		create: crdCreateOf(t, crd, "v1"), component: component, handler: handler,
		document: func(props map[string]any) map[string]any {
			return crdDocument(crd, "v1", map[string]any{"spec": props})
		},
	}
}

// TestGatewayRouteKinds_ExpressionRules: every expression rule a route CRD
// declares, in either channel and anywhere in the object, is the one the kinds
// check or one a kind leaves to the API server with a reason. A dependency
// bump that adds or rewords a rule fails here until it is classified.
//
// The checked rule is the API server's (checkedRules.show), in both channels:
// on a backend that breaks it the API server, serving the CRD the module ships,
// refuses the route by that rule and the kind refuses the properties; on the
// backends next to it, which differ in what the rule reads, both accept, and
// the API server accepts the object the kind emits. The rule reads a backend's
// group and kind after the API server has filled their defaults, so a backend
// that authors neither, or authors a null, is among the cases.
func TestGatewayRouteKinds_ExpressionRules(t *testing.T) {
	for _, kind := range gatewayRouteKinds {
		declaredInAChannel := map[string]bool{}
		for _, channel := range gatewayAPIChannels {
			t.Run(kind.component+"/"+channel, func(t *testing.T) {
				crd, root := gatewayAPICRD(t, channel, kind.crd)
				checked := false
				walkCiliumBGPSchema(root, "", func(path string, s apiextensionsv1.JSONSchemaProps) {
					for _, declared := range s.XValidations {
						rule := path + ": " + declared.Rule
						declaredInAChannel[rule] = true
						why, left := kind.left[rule]
						switch {
						case rule == gatewayRouteServicePort && !left:
							checked = true
						case !left:
							t.Errorf("the CRD declares the rule %q, which the kind neither checks nor leaves to the API server", rule)
						case rule == gatewayRouteServicePort || strings.TrimSpace(why) == "":
							t.Errorf("rule %q is left to the API server with the reason %q, and checked: %v", rule, why, rule == gatewayRouteServicePort)
						}
					}
				})
				if !checked {
					t.Fatalf("the CRD does not declare the rule %q, and the kind refuses by it on every cluster", gatewayRouteServicePort)
				}

				with := func(backends ...map[string]any) []map[string]any {
					var out []map[string]any
					for _, backend := range backends {
						out = append(out, gatewayRouteProperties(kind.base, backend))
					}
					return out
				}
				gatewayRouteChecked(t, kind.component, kind.handler, crd).show(t, crdRuleOf(t, gatewayRouteServicePort),
					"rules[0].backendRefs[0].port: required",
					with(
						map[string]any{"name": "db"},
						map[string]any{"name": "db", "port": nil},
						map[string]any{"name": "db", "group": "", "kind": "Service"},
						map[string]any{"name": "db", "group": nil, "kind": nil},
						map[string]any{"name": "db", "namespace": "data", "weight": 1},
					),
					with(
						map[string]any{"name": "db", "port": 5432},
						map[string]any{"name": "db", "group": "", "kind": "Service", "port": 5432},
						map[string]any{"name": "db", "group": "multicluster.x-k8s.io", "kind": "ServiceImport"},
						map[string]any{"name": "db", "group": "example.com"},
						map[string]any{"name": "db", "kind": "Backend"},
					),
				)
			})
		}
		for rule := range kind.left {
			if !declaredInAChannel[rule] {
				t.Errorf("%s leaves the rule %q to the API server, and the CRD declares it in no channel", kind.component, rule)
			}
		}
	}
}

// TestGatewayRouteKinds_OmissionsAreTheAPIServers: each field a route kind
// refuses the omission of (the row's refused list in apiSetKinds, all of them
// lists) is one the API server, serving the CRD of either channel, refuses the
// route without: left out, authored as null, which it drops, and authored
// empty. The kind refuses the same three, under the field's path, and both
// accept the properties that hold it.
func TestGatewayRouteKinds_OmissionsAreTheAPIServers(t *testing.T) {
	for _, kind := range gatewayRouteKinds {
		refused := gatewayAPIRefused(t, kind.component)
		if len(refused) == 0 {
			t.Fatalf("the row of %s in apiSetKinds names no refused field", kind.component)
		}
		whole := gatewayRouteProperties(kind.base, map[string]any{"name": "db", "port": 5432})
		for _, channel := range gatewayAPIChannels {
			crd, _ := gatewayAPICRD(t, channel, kind.crd)
			shown := gatewayRouteChecked(t, kind.component, kind.handler, crd)
			t.Run(kind.component+"/"+channel, func(t *testing.T) {
				shown.create.create(t, shown.document(whole)).accepted(t, "the whole route")
				if _, err := shown.build(t, whole); err != nil {
					t.Fatalf("the kind refuses the whole route: %v", err)
				}
				for _, path := range refused {
					at := strings.ReplaceAll(path, "[]", "[0]")
					for how, props := range gatewayRouteWithout(t, whole, path) {
						answer := shown.create.create(t, shown.document(props))
						onField := false
						for _, err := range answer.schema {
							onField = onField || err.Field == field.NewPath("spec").Child(at).String()
						}
						if !onField || len(answer.rules) > 0 {
							t.Errorf("%s %s: the API server answers %q, want a refusal by the schema on spec.%s", path, how, answer, at)
						}
						if _, err := shown.build(t, props); err == nil || !strings.Contains(err.Error(), at+": required") {
							t.Errorf("%s %s: the kind answers %v, want a refusal mentioning %q", path, how, err, at+": required")
						}
					}
				}
			})
		}
	}
}

// gatewayRouteWithout returns three copies of props in which the list at path
// is left out, null and empty. A list element on the way, under [], is the
// first.
func gatewayRouteWithout(t *testing.T, props map[string]any, path string) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for how, change := range map[string]func(parent map[string]any, key string){
		"left out": func(parent map[string]any, key string) { delete(parent, key) },
		"null":     func(parent map[string]any, key string) { parent[key] = nil },
		"empty":    func(parent map[string]any, key string) { parent[key] = []any{} },
	} {
		raw, err := stdjson.Marshal(props)
		if err != nil {
			t.Fatalf("encode the properties: %v", err)
		}
		var copied map[string]any
		if err := stdjson.Unmarshal(raw, &copied); err != nil {
			t.Fatalf("decode the properties: %v", err)
		}
		parent := copied
		steps := strings.Split(path, ".")
		for _, step := range steps[:len(steps)-1] {
			name, isList := strings.CutSuffix(step, "[]")
			list, _ := parent[name].([]any)
			if !isList || len(list) == 0 {
				t.Fatalf("%s: %s is no list with an element in the properties; update this test", path, step)
			}
			element, ok := list[0].(map[string]any)
			if !ok {
				t.Fatalf("%s: the first element of %s is no object", path, name)
			}
			parent = element
		}
		last := steps[len(steps)-1]
		if _, isList := parent[last].([]any); !isList {
			t.Fatalf("%s is no list in the properties; update this test", path)
		}
		change(parent, last)
		out[how] = copied
	}
	return out
}
