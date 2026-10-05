package components_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	ciliumapi "github.com/cilium/cilium/pkg/policy/api"
	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func TestCiliumNetworkPolicyHandler_CanHandle(t *testing.T) {
	h := &components.CiliumNetworkPolicyHandler{}
	if !h.CanHandle("cilium-networkpolicy") {
		t.Error("CanHandle(cilium-networkpolicy) = false")
	}
	if h.CanHandle("networkpolicy") {
		t.Error("CanHandle(networkpolicy) = true")
	}
}

// ciliumPolicyJSON is the emitted object as the manifest carries it.
func ciliumPolicyJSON(t *testing.T, cnp *ciliumv2.CiliumNetworkPolicy) map[string]any {
	t.Helper()
	data, err := json.Marshal(cnp)
	if err != nil {
		t.Fatalf("encode the CiliumNetworkPolicy: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode the encoded CiliumNetworkPolicy: %v", err)
	}
	return out
}

// generateCiliumPolicy generates one cilium-networkpolicy component and checks
// the object's identity.
func generateCiliumPolicy(t *testing.T, props map[string]any) *ciliumv2.CiliumNetworkPolicy {
	t.Helper()
	cnp := generateCoreKind(t, &components.CiliumNetworkPolicyHandler{}, "cilium-networkpolicy", "db-allow", props).(*ciliumv2.CiliumNetworkPolicy)
	if cnp.APIVersion != "cilium.io/v2" || cnp.Kind != "CiliumNetworkPolicy" {
		t.Errorf("GVK = %s %s, want cilium.io/v2 CiliumNetworkPolicy", cnp.APIVersion, cnp.Kind)
	}
	if cnp.Namespace != coreKindNamespace {
		t.Errorf("namespace = %q, want the build namespace %q", cnp.Namespace, coreKindNamespace)
	}
	return cnp
}

// ciliumRule is a rule the kind accepts: a selector and one ingress entry.
func ciliumRule(role string) map[string]any {
	return map[string]any{
		"endpointSelector": map[string]any{"matchLabels": map[string]any{"role": role}},
		"ingress": []any{map[string]any{"fromEndpoints": []any{
			map[string]any{"matchLabels": map[string]any{"role": "api"}},
		}}},
	}
}

// TestCiliumNetworkPolicyHandler_EmitsAuthoredSpec: one rule under `spec`, with
// the fields of the Cilium rule the trait of the same name does not publish.
func TestCiliumNetworkPolicyHandler_EmitsAuthoredSpec(t *testing.T) {
	cnp := generateCiliumPolicy(t, map[string]any{"spec": map[string]any{
		"description":      "database access",
		"endpointSelector": map[string]any{"matchLabels": map[string]any{"role": "db"}},
		"ingress": []any{map[string]any{
			"fromEndpoints": []any{map[string]any{"matchLabels": map[string]any{"role": "api"}}},
			"toPorts":       []any{map[string]any{"ports": []any{map[string]any{"port": "5432", "protocol": "TCP"}}}},
		}},
		"egressDeny":        []any{map[string]any{"toCIDR": []any{"169.254.169.254/32"}}},
		"enableDefaultDeny": map[string]any{"egress": false},
	}})

	if cnp.Spec == nil || len(cnp.Specs) != 0 {
		t.Fatalf("spec = %v, specs = %v; want one rule under spec and no specs", cnp.Spec, cnp.Specs)
	}
	got := ciliumPolicyJSON(t, cnp)["spec"]
	want := map[string]any{
		"description":      "database access",
		"endpointSelector": map[string]any{"matchLabels": map[string]any{"role": "db"}},
		"ingress": []any{map[string]any{
			"fromEndpoints": []any{map[string]any{"matchLabels": map[string]any{"role": "api"}}},
			"toPorts":       []any{map[string]any{"ports": []any{map[string]any{"port": "5432", "protocol": "TCP"}}}},
		}},
		"egressDeny":        []any{map[string]any{"toCIDR": []any{"169.254.169.254/32"}}},
		"enableDefaultDeny": map[string]any{"egress": false},
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("spec = %s\nwant   %s", gotJSON, wantJSON)
	}
	if _, ok := ciliumPolicyJSON(t, cnp)["specs"]; ok {
		t.Error("an unauthored specs is emitted")
	}
}

// TestCiliumNetworkPolicyHandler_EmitsSpecs: `specs` alone, and beside `spec`.
// The rules keep their order.
func TestCiliumNetworkPolicyHandler_EmitsSpecs(t *testing.T) {
	t.Run("specs only", func(t *testing.T) {
		cnp := generateCiliumPolicy(t, map[string]any{"specs": []any{ciliumRule("db"), ciliumRule("cache")}})
		if cnp.Spec != nil {
			t.Errorf("spec = %v, want unset", cnp.Spec)
		}
		if len(cnp.Specs) != 2 {
			t.Fatalf("specs holds %d rules, want 2", len(cnp.Specs))
		}
		for i, role := range []string{"db", "cache"} {
			if got := cnp.Specs[i].EndpointSelector.MatchLabels["role"]; got != role {
				t.Errorf("specs[%d] selects role %q, want %q", i, got, role)
			}
		}
		if _, ok := ciliumPolicyJSON(t, cnp)["spec"]; ok {
			t.Error("an unauthored spec is emitted")
		}
	})
	t.Run("spec and specs", func(t *testing.T) {
		cnp := generateCiliumPolicy(t, map[string]any{"spec": ciliumRule("db"), "specs": []any{ciliumRule("cache")}})
		if cnp.Spec == nil || len(cnp.Specs) != 1 {
			t.Fatalf("spec = %v, specs = %v; want both", cnp.Spec, cnp.Specs)
		}
	})
}

// TestCiliumNetworkPolicyHandler_SelectAllIsAuthored: `endpointSelector: {}`
// selects every endpoint of the namespace and is carried as written. It is
// never filled in: an absent or null selector is refused
// (TestCiliumNetworkPolicyHandler_Refusals).
func TestCiliumNetworkPolicyHandler_SelectAllIsAuthored(t *testing.T) {
	rule := ciliumRule("db")
	rule["endpointSelector"] = map[string]any{}
	cnp := generateCiliumPolicy(t, map[string]any{"spec": rule})
	spec := ciliumPolicyJSON(t, cnp)["spec"].(map[string]any)
	sel, ok := spec["endpointSelector"].(map[string]any)
	if !ok || len(sel) != 0 {
		t.Errorf("endpointSelector = %v (present %v), want {} in the manifest", spec["endpointSelector"], ok)
	}
}

// TestCiliumNetworkPolicyHandler_RefusesUnknownSelectorKey: Cilium's endpoint
// selector unmarshals itself, so the strict decode does not reach inside it, and
// a misspelt key would leave the empty selector, which selects everything. The
// kind refuses the key by its path at every position that holds a selector, in
// `spec` and in `specs`, and inside an ICMP field and a rule label, the other
// types under a rule that unmarshal themselves. The positions are held to the
// Cilium types by the tests of builtin.UnknownCiliumKeyPath.
func TestCiliumNetworkPolicyHandler_RefusesUnknownSelectorKey(t *testing.T) {
	misspelt := map[string]any{"matchLabel": map[string]any{"role": "db"}}
	with := func(key string, value any) map[string]any {
		rule := ciliumRule("db")
		rule[key] = value
		return rule
	}
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"endpointSelector":     {map[string]any{"spec": with("endpointSelector", misspelt)}, "spec.endpointSelector.matchLabel"},
		"beside a valid key":   {map[string]any{"spec": with("endpointSelector", map[string]any{"matchLabels": map[string]any{"role": "db"}, "matchExpression": []any{}})}, "spec.endpointSelector.matchExpression"},
		"null-valued key":      {map[string]any{"spec": with("endpointSelector", map[string]any{"matchLabel": nil})}, "spec.endpointSelector.matchLabel"},
		"fromEndpoints":        {map[string]any{"spec": with("ingress", []any{map[string]any{"fromEndpoints": []any{map[string]any{}, misspelt}}})}, "spec.ingress[0].fromEndpoints[1].matchLabel"},
		"toEndpoints":          {map[string]any{"spec": with("egress", []any{map[string]any{"toEndpoints": []any{misspelt}}})}, "spec.egress[0].toEndpoints[0].matchLabel"},
		"fromNodes":            {map[string]any{"spec": with("ingress", []any{map[string]any{"fromNodes": []any{misspelt}}})}, "spec.ingress[0].fromNodes[0].matchLabel"},
		"toNodes":              {map[string]any{"spec": with("egress", []any{map[string]any{"toNodes": []any{misspelt}}})}, "spec.egress[0].toNodes[0].matchLabel"},
		"cidrGroupSelector":    {map[string]any{"spec": with("egress", []any{map[string]any{"toCIDRSet": []any{map[string]any{"cidrGroupSelector": misspelt}}}})}, "spec.egress[0].toCIDRSet[0].cidrGroupSelector.matchLabel"},
		"deny list":            {map[string]any{"spec": with("ingressDeny", []any{map[string]any{"fromEndpoints": []any{misspelt}}})}, "spec.ingressDeny[0].fromEndpoints[0].matchLabel"},
		"expression":           {map[string]any{"spec": with("endpointSelector", map[string]any{"matchExpressions": []any{map[string]any{"key": "role", "operator": "Exists", "value": "x"}}})}, "spec.endpointSelector.matchExpressions[0].value"},
		"specs":                {map[string]any{"specs": []any{ciliumRule("db"), with("ingress", []any{map[string]any{"fromEndpoints": []any{misspelt}}})}}, "specs[1].ingress[0].fromEndpoints[0].matchLabel"},
		"icmps field":          {map[string]any{"spec": with("egress", []any{map[string]any{"icmps": []any{map[string]any{"fields": []any{map[string]any{"family": "IPv4", "type": 8, "code": 0}}}}}})}, "spec.egress[0].icmps[0].fields[0].code"},
		"rule label":           {map[string]any{"spec": with("labels", []any{map[string]any{"key": "team", "value": "db", "sources": "k8s"}})}, "spec.labels[0].sources"},
		"node selector's key":  {map[string]any{"spec": with("nodeSelector", misspelt)}, "spec.nodeSelector"},
		"selector under Spec":  {map[string]any{"Spec": with("endpointSelector", misspelt)}, "Spec.endpointSelector.matchLabel"},
		"selector under SPECS": {map[string]any{"SPECS": []any{with("endpointSelector", misspelt)}}, "SPECS[0].endpointSelector.matchLabel"},
	} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(&components.CiliumNetworkPolicyHandler{}, "cilium-networkpolicy", "db-allow", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

// TestCiliumNetworkPolicyHandler_ICMPFieldWithoutType: Cilium's own decoding of
// an ICMP field dereferences a nil pointer when `type` is absent or null. The
// component is refused, in `spec` and in `specs`; the build does not crash.
func TestCiliumNetworkPolicyHandler_ICMPFieldWithoutType(t *testing.T) {
	rule := func(field map[string]any) map[string]any {
		r := ciliumRule("db")
		r["egress"] = []any{map[string]any{"icmps": []any{map[string]any{"fields": []any{field}}}}}
		return r
	}
	for name, props := range map[string]map[string]any{
		"absent":    {"spec": rule(map[string]any{"family": "IPv4"})},
		"null":      {"spec": rule(map[string]any{"family": "IPv4", "type": nil})},
		"empty":     {"spec": rule(map[string]any{})},
		"in specs":  {"specs": []any{ciliumRule("db"), rule(map[string]any{"family": "IPv6"})}},
		"with typo": {"spec": rule(map[string]any{"family": "IPv4", "typ": 8})},
	} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(&components.CiliumNetworkPolicyHandler{}, "cilium-networkpolicy", "db-allow", props)
			if err == nil {
				t.Fatal("the component was accepted, want the ICMP field without a type refused")
			}
			for _, want := range []string{"properties do not decode into a cilium.io/v2 CiliumNetworkPolicy", "panicked"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v, want it to contain %q", err, want)
				}
			}
		})
	}
}

// TestCiliumNetworkPolicyHandler_KeepsValidSelectors is the control for the
// refusal above: every key a selector, an ICMP field and a rule label declare
// is accepted and emitted, a label in its short form too.
func TestCiliumNetworkPolicyHandler_KeepsValidSelectors(t *testing.T) {
	selector := map[string]any{
		"matchLabels":      map[string]any{"role": "db"},
		"matchExpressions": []any{map[string]any{"key": "tier", "operator": "In", "values": []any{"data"}}},
	}
	cnp := generateCiliumPolicy(t, map[string]any{"spec": map[string]any{
		"endpointSelector": selector,
		"labels":           []any{map[string]any{"key": "team", "value": "db", "source": "k8s"}, "k8s:owner=platform"},
		"ingress":          []any{map[string]any{"fromEndpoints": []any{selector, map[string]any{}}}},
		"egress": []any{
			map[string]any{"toCIDRSet": []any{map[string]any{"cidrGroupSelector": selector}}},
			map[string]any{"icmps": []any{map[string]any{"fields": []any{map[string]any{"family": "IPv4", "type": 8}, map[string]any{"type": "EchoRequest"}}}}},
		},
	}})
	spec := ciliumPolicyJSON(t, cnp)["spec"].(map[string]any)
	if got := spec["endpointSelector"]; !reflect.DeepEqual(got, selector) {
		t.Errorf("endpointSelector = %v, want %v", got, selector)
	}
	if got := spec["ingress"].([]any)[0].(map[string]any)["fromEndpoints"]; !reflect.DeepEqual(got, []any{selector, map[string]any{}}) {
		t.Errorf("fromEndpoints = %v, want the authored selector and the empty one", got)
	}
	// A label is emitted in its object form, the short one parsed into it.
	sameJSON(t, "labels", spec["labels"], []any{
		map[string]any{"key": "team", "value": "db", "source": "k8s"},
		map[string]any{"key": "owner", "value": "platform", "source": "k8s"},
	})
	egress := spec["egress"].([]any)
	if got := egress[0].(map[string]any)["toCIDRSet"].([]any)[0].(map[string]any)["cidrGroupSelector"]; !reflect.DeepEqual(got, selector) {
		t.Errorf("cidrGroupSelector = %v, want %v", got, selector)
	}
	sameJSON(t, "icmps fields", egress[1].(map[string]any)["icmps"].([]any)[0].(map[string]any)["fields"], []any{
		map[string]any{"family": "IPv4", "type": 8},
		map[string]any{"type": "EchoRequest"},
	})
}

// sameJSON compares two values by their JSON encoding, which sorts an object's
// keys and writes a number the same whether it was authored or decoded.
func sameJSON(t *testing.T, what string, got, want any) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("encode %s: %v", what, err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("encode the wanted %s: %v", what, err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("%s = %s\nwant %s", what, gotJSON, wantJSON)
	}
}

// TestCiliumNetworkPolicyHandler_EmptySpecsBesideSpec: an empty `specs` holds
// no rule. Alone it is refused (TestCiliumNetworkPolicyHandler_Refusals); beside
// a `spec` the policy has its rule, and the empty list is not emitted.
func TestCiliumNetworkPolicyHandler_EmptySpecsBesideSpec(t *testing.T) {
	cnp := generateCiliumPolicy(t, map[string]any{"spec": ciliumRule("db"), "specs": []any{}})
	if cnp.Spec == nil {
		t.Fatal("spec is unset, want the authored rule")
	}
	if _, ok := ciliumPolicyJSON(t, cnp)["specs"]; ok {
		t.Error("an empty specs is emitted")
	}
}

// TestCiliumNetworkPolicyHandler_TwoSpellingsInAnOmittedObject: Cilium omits a
// rule's `log` when it is empty. Two spellings of its one field, the second of
// them empty, decode to an empty log, so the authored value would be dropped
// with the whole object. It is refused as two spellings are anywhere else.
func TestCiliumNetworkPolicyHandler_TwoSpellingsInAnOmittedObject(t *testing.T) {
	ruleWith := func(key string, value map[string]any) map[string]any {
		rule := ciliumRule("db")
		rule[key] = value
		return rule
	}
	with := func(log map[string]any) map[string]any {
		return map[string]any{"spec": ruleWith("log", log)}
	}
	h := &components.CiliumNetworkPolicyHandler{}

	// Controls: one spelling is carried, and an empty value alone emits no log.
	cnp := generateCiliumPolicy(t, with(map[string]any{"value": "audit"}))
	sameJSON(t, "log", ciliumPolicyJSON(t, cnp)["spec"].(map[string]any)["log"], map[string]any{"value": "audit"})
	cnp = generateCiliumPolicy(t, with(map[string]any{"value": ""}))
	if log, ok := ciliumPolicyJSON(t, cnp)["spec"].(map[string]any)["log"]; ok {
		t.Errorf("log = %v, want an empty log omitted", log)
	}

	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"the empty one wins": {with(map[string]any{"Value": "audit", "value": ""}), "spec.log.value: sets the same field as spec.log.Value"},
		"both empty":         {with(map[string]any{"Value": "", "value": ""}), "spec.log.value: sets the same field as spec.log.Value"},
		"in specs":           {map[string]any{"specs": []any{ruleWith("log", map[string]any{"VALUE": "audit", "value": ""})}}, "specs[0].log.value: sets the same field as specs[0].log.VALUE"},
		// The object is emitted in these two, and the refusal reads the same.
		"the other one wins":  {with(map[string]any{"Value": "", "value": "audit"}), "spec.log.value: sets the same field as spec.log.Value"},
		"default-deny switch": {map[string]any{"spec": ruleWith("enableDefaultDeny", map[string]any{"Egress": true, "egress": false})}, "spec.enableDefaultDeny.egress: sets the same field as spec.enableDefaultDeny.Egress"},
	} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(h, "cilium-networkpolicy", "db-allow", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestCiliumNetworkPolicyHandler_AnyDirectionCounts: a rule needs one entry in
// one of the four lists, a deny list as much as an allow list.
func TestCiliumNetworkPolicyHandler_AnyDirectionCounts(t *testing.T) {
	entry := []any{map[string]any{}}
	for _, key := range []string{"ingress", "ingressDeny", "egress", "egressDeny"} {
		t.Run(key, func(t *testing.T) {
			generateCiliumPolicy(t, map[string]any{"spec": map[string]any{
				"endpointSelector": map[string]any{}, key: entry,
			}})
		})
	}
}

func TestCiliumNetworkPolicyHandler_Refusals(t *testing.T) {
	const (
		notAPolicy = "properties do not decode into a cilium.io/v2 CiliumNetworkPolicy"
		noRule     = "at least one of 'spec' and 'specs' is required"
		noEntry    = "at least one of 'ingress', 'ingressDeny', 'egress' and 'egressDeny' is required"
	)
	with := func(key string, value any) map[string]any {
		rule := ciliumRule("db")
		if value == nil {
			delete(rule, key)
		} else {
			rule[key] = value
		}
		return rule
	}
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"no properties":        {nil, noRule},
		"empty properties":     {map[string]any{}, noRule},
		"null fields":          {map[string]any{"spec": nil, "specs": nil}, noRule},
		"empty specs":          {map[string]any{"specs": []any{}}, noRule},
		"trait shape":          {ciliumRule("db"), notAPolicy},
		"trait name":           {map[string]any{"name": "db-allow", "spec": ciliumRule("db")}, notAPolicy},
		"metadata":             {map[string]any{"metadata": map[string]any{"labels": map[string]any{"a": "b"}}, "spec": ciliumRule("db")}, notAPolicy},
		"status":               {map[string]any{"status": map[string]any{}, "spec": ciliumRule("db")}, notAPolicy},
		"spec a list":          {map[string]any{"spec": []any{ciliumRule("db")}}, notAPolicy},
		"specs a map":          {map[string]any{"specs": ciliumRule("db")}, notAPolicy},
		"unknown rule key":     {map[string]any{"spec": with("podSelector", map[string]any{})}, notAPolicy},
		"unknown ingress key":  {map[string]any{"spec": with("ingress", []any{map[string]any{"from": []any{}}})}, notAPolicy},
		"null specs entry":     {map[string]any{"specs": []any{ciliumRule("db"), nil}}, "specs[1]"},
		"null ingress entry":   {map[string]any{"spec": with("ingress", []any{nil})}, "spec.ingress[0]"},
		"null peer":            {map[string]any{"spec": with("ingress", []any{map[string]any{"fromEndpoints": []any{nil}}})}, "spec.ingress[0].fromEndpoints[0]"},
		"node selector":        {map[string]any{"spec": with("nodeSelector", map[string]any{"matchLabels": map[string]any{"role": "worker"}})}, "spec.nodeSelector: not allowed in a CiliumNetworkPolicy"},
		"empty node selector":  {map[string]any{"spec": with("nodeSelector", map[string]any{})}, "spec.nodeSelector: not allowed in a CiliumNetworkPolicy"},
		"node selector, specs": {map[string]any{"specs": []any{ciliumRule("db"), with("nodeSelector", map[string]any{})}}, "specs[1].nodeSelector: not allowed"},
		"no selector":          {map[string]any{"spec": with("endpointSelector", nil)}, "spec.endpointSelector: required"},
		"no selector, specs":   {map[string]any{"specs": []any{with("endpointSelector", nil)}}, "specs[0].endpointSelector: required"},
		"no entry":             {map[string]any{"spec": with("ingress", nil)}, "spec: " + noEntry},
		"empty entry list":     {map[string]any{"spec": with("ingress", []any{})}, "spec: " + noEntry},
		"no entry, specs":      {map[string]any{"spec": ciliumRule("db"), "specs": []any{with("ingress", nil)}}, "specs[0]: " + noEntry},
		"two spellings":        {map[string]any{"spec": ciliumRule("db"), "Spec": ciliumRule("db")}, "sets the same field as"},
	} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(&components.CiliumNetworkPolicyHandler{}, "cilium-networkpolicy", "db-allow", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestCiliumNetworkPolicyHandler_NullSelector: a null selector is dropped by
// the null contract, so it is the absent one and refused, not the select-all
// one.
func TestCiliumNetworkPolicyHandler_NullSelector(t *testing.T) {
	rule := ciliumRule("db")
	rule["endpointSelector"] = nil
	err := coreKindErr(&components.CiliumNetworkPolicyHandler{}, "cilium-networkpolicy", "db-allow", map[string]any{"spec": rule})
	if err == nil || !strings.Contains(err.Error(), "spec.endpointSelector: required") {
		t.Fatalf("err = %v, want the missing-selector refusal", err)
	}
}

// TestCiliumNetworkPolicyHandler_TypedNilElement: a rule that is a typed nil (a
// document built in Go, not parsed from YAML) is refused as a null one is.
func TestCiliumNetworkPolicyHandler_TypedNilElement(t *testing.T) {
	err := coreKindErr(&components.CiliumNetworkPolicyHandler{}, "cilium-networkpolicy", "db-allow", map[string]any{
		"specs": []any{map[string]any(nil)},
	})
	if err == nil || !strings.Contains(err.Error(), "specs[0]") {
		t.Fatalf("err = %v, want one naming specs[0]", err)
	}
}

// TestCiliumNetworkPolicyConfig_GenerateRefuses: the config is exported, so
// Generate repeats the refusals for one built without the handler.
func TestCiliumNetworkPolicyConfig_GenerateRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg  *components.CiliumNetworkPolicyConfig
		want string
	}{
		"no rule":       {&components.CiliumNetworkPolicyConfig{}, "at least one of 'spec' and 'specs' is required"},
		"nil specs":     {&components.CiliumNetworkPolicyConfig{Specs: ciliumapi.Rules{nil}}, "specs[0]: expected a rule, got null"},
		"zero rule":     {&components.CiliumNetworkPolicyConfig{Spec: &ciliumapi.Rule{}}, "spec.endpointSelector: required"},
		"selector only": {&components.CiliumNetworkPolicyConfig{Spec: &ciliumapi.Rule{EndpointSelector: ciliumapi.NewESFromLabels()}}, "spec: at least one of"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tc.cfg.Generate(stack.NewApplication("db-allow", coreKindNamespace, tc.cfg))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestCiliumNetworkPolicyConfig_ReportsNoTraffic: the config is an authored
// object, not a trait's.
func TestCiliumNetworkPolicyConfig_ReportsNoTraffic(t *testing.T) {
	var cfg any = &components.CiliumNetworkPolicyConfig{}
	if _, ok := cfg.(interface{ TargetComponentName() string }); ok {
		t.Error("CiliumNetworkPolicyConfig reports a target component; the NetworkPolicy synthesis would read it as a routing trait")
	}
	if _, ok := cfg.(interface{ ComponentName() string }); ok {
		t.Error("CiliumNetworkPolicyConfig names an owning component; it is a component's own config, not a trait's")
	}
}
