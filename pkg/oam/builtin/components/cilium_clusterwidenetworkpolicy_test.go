package components_test

import (
	"encoding/json"
	"strings"
	"testing"

	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	ciliumapi "github.com/cilium/cilium/pkg/policy/api"
	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

const ciliumClusterwideType = "cilium-clusterwidenetworkpolicy"

func TestCiliumClusterwideNetworkPolicyHandler_CanHandle(t *testing.T) {
	h := &components.CiliumClusterwideNetworkPolicyHandler{}
	if !h.CanHandle(ciliumClusterwideType) {
		t.Error("CanHandle(cilium-clusterwidenetworkpolicy) = false")
	}
	if h.CanHandle("cilium-networkpolicy") {
		t.Error("CanHandle(cilium-networkpolicy) = true")
	}
}

// generateClusterwidePolicy generates one cilium-clusterwidenetworkpolicy
// component, checks the object's identity and returns it with its manifest
// form.
func generateClusterwidePolicy(t *testing.T, props map[string]any) (*ciliumv2.CiliumClusterwideNetworkPolicy, map[string]any) {
	t.Helper()
	ccnp := generateCoreKind(t, &components.CiliumClusterwideNetworkPolicyHandler{}, ciliumClusterwideType, "host-firewall", props).(*ciliumv2.CiliumClusterwideNetworkPolicy)
	if ccnp.APIVersion != "cilium.io/v2" || ccnp.Kind != "CiliumClusterwideNetworkPolicy" {
		t.Errorf("GVK = %s %s, want cilium.io/v2 CiliumClusterwideNetworkPolicy", ccnp.APIVersion, ccnp.Kind)
	}
	if ccnp.Namespace != "" {
		t.Errorf("namespace = %q, want none: the object is cluster-scoped", ccnp.Namespace)
	}
	data, err := json.Marshal(ccnp)
	if err != nil {
		t.Fatalf("encode the CiliumClusterwideNetworkPolicy: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode the encoded CiliumClusterwideNetworkPolicy: %v", err)
	}
	return ccnp, manifest
}

// ciliumNodeRule is a rule that selects nodes.
func ciliumNodeRule(role string) map[string]any {
	return map[string]any{
		"nodeSelector": map[string]any{"matchLabels": map[string]any{"node-role": role}},
		"ingress": []any{map[string]any{
			"fromCIDRSet": []any{map[string]any{"cidr": "192.0.2.0/24"}},
			"toPorts":     []any{map[string]any{"ports": []any{map[string]any{"port": "22", "protocol": "TCP"}}}},
		}},
	}
}

func clusterwideErr(props map[string]any) error {
	return coreKindErr(&components.CiliumClusterwideNetworkPolicyHandler{}, ciliumClusterwideType, "host-firewall", props)
}

// TestCiliumClusterwideNetworkPolicyHandler_EmitsAuthoredSpec: a rule that
// selects nodes is emitted as authored, under `spec`. The selector that was not
// authored is not written: the API takes exactly one of the two, so an
// `endpointSelector: {}` beside the node selector would have the object refused.
func TestCiliumClusterwideNetworkPolicyHandler_EmitsAuthoredSpec(t *testing.T) {
	authored := ciliumNodeRule("edge")
	authored["description"] = "edge nodes accept SSH from the bastion network"
	authored["egressDeny"] = []any{map[string]any{"toCIDR": []any{"169.254.169.254/32"}}}
	authored["enableDefaultDeny"] = map[string]any{"egress": false}

	ccnp, manifest := generateClusterwidePolicy(t, map[string]any{"spec": authored})
	if ccnp.Spec == nil || len(ccnp.Specs) != 0 {
		t.Fatalf("spec = %v, specs = %v; want one rule under spec and no specs", ccnp.Spec, ccnp.Specs)
	}
	sameJSON(t, "spec", manifest["spec"], authored)
	if _, ok := manifest["specs"]; ok {
		t.Error("an unauthored specs is emitted")
	}
	if _, ok := manifest["metadata"].(map[string]any)["namespace"]; ok {
		t.Error("the manifest carries a namespace; the object is cluster-scoped")
	}
}

// TestCiliumClusterwideNetworkPolicyHandler_EndpointRule: a rule that selects
// endpoints carries no node selector.
func TestCiliumClusterwideNetworkPolicyHandler_EndpointRule(t *testing.T) {
	_, manifest := generateClusterwidePolicy(t, map[string]any{"spec": ciliumRule("db")})
	sameJSON(t, "spec", manifest["spec"], ciliumRule("db"))
}

// TestCiliumClusterwideNetworkPolicyHandler_EmitsSpecs: `specs` alone, and
// beside `spec`. The rules keep their order, and a policy may hold a rule for
// nodes beside one for endpoints.
func TestCiliumClusterwideNetworkPolicyHandler_EmitsSpecs(t *testing.T) {
	t.Run("specs only", func(t *testing.T) {
		rules := []any{ciliumRule("db"), ciliumNodeRule("edge"), ciliumRule("cache")}
		ccnp, manifest := generateClusterwidePolicy(t, map[string]any{"specs": rules})
		if ccnp.Spec != nil {
			t.Errorf("spec = %v, want unset", ccnp.Spec)
		}
		sameJSON(t, "specs", manifest["specs"], rules)
		if _, ok := manifest["spec"]; ok {
			t.Error("an unauthored spec is emitted")
		}
	})
	t.Run("spec and specs", func(t *testing.T) {
		ccnp, _ := generateClusterwidePolicy(t, map[string]any{"spec": ciliumNodeRule("edge"), "specs": []any{ciliumRule("cache")}})
		if ccnp.Spec == nil || len(ccnp.Specs) != 1 {
			t.Fatalf("spec = %v, specs = %v; want both", ccnp.Spec, ccnp.Specs)
		}
	})
	t.Run("empty specs beside spec", func(t *testing.T) {
		_, manifest := generateClusterwidePolicy(t, map[string]any{"spec": ciliumRule("db"), "specs": []any{}})
		if _, ok := manifest["specs"]; ok {
			t.Error("an empty specs is emitted")
		}
	})
}

// TestCiliumClusterwideNetworkPolicyHandler_SelectAllIsAuthored: an empty
// selector selects every endpoint of the cluster, or every node, and is carried
// as written. Neither is ever filled in: a rule without a selector is refused
// (TestCiliumClusterwideNetworkPolicyHandler_Refusals).
func TestCiliumClusterwideNetworkPolicyHandler_SelectAllIsAuthored(t *testing.T) {
	for _, key := range []string{"endpointSelector", "nodeSelector"} {
		t.Run(key, func(t *testing.T) {
			_, manifest := generateClusterwidePolicy(t, map[string]any{"spec": map[string]any{
				key:          map[string]any{},
				"egressDeny": []any{map[string]any{"toCIDR": []any{"169.254.169.254/32"}}},
			}})
			spec := manifest["spec"].(map[string]any)
			sel, ok := spec[key].(map[string]any)
			if !ok || len(sel) != 0 {
				t.Errorf("%s = %v (present %v), want {} in the manifest", key, spec[key], ok)
			}
			for _, other := range []string{"endpointSelector", "nodeSelector"} {
				if _, ok := spec[other]; ok && other != key {
					t.Errorf("%s is emitted beside the authored %s", other, key)
				}
			}
		})
	}
}

// TestCiliumClusterwideNetworkPolicyHandler_AnyDirectionCounts: a rule needs
// one entry in one of the four lists, a deny list as much as an allow list.
func TestCiliumClusterwideNetworkPolicyHandler_AnyDirectionCounts(t *testing.T) {
	for _, key := range []string{"ingress", "ingressDeny", "egress", "egressDeny"} {
		t.Run(key, func(t *testing.T) {
			generateClusterwidePolicy(t, map[string]any{"spec": map[string]any{
				"nodeSelector": map[string]any{}, key: []any{map[string]any{}},
			}})
		})
	}
}

func TestCiliumClusterwideNetworkPolicyHandler_Refusals(t *testing.T) {
	const (
		notAPolicy = "properties do not decode into a cilium.io/v2 CiliumClusterwideNetworkPolicy"
		noRule     = "at least one of 'spec' and 'specs' is required"
		noEntry    = "at least one of 'ingress', 'ingressDeny', 'egress' and 'egressDeny' is required"
		both       = "endpointSelector and nodeSelector are both set; the API takes exactly one"
		neither    = "one of endpointSelector and nodeSelector is required"
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
	nullSelector := ciliumRule("db")
	nullSelector["endpointSelector"] = nil
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"no properties":           {nil, noRule},
		"empty properties":        {map[string]any{}, noRule},
		"null fields":             {map[string]any{"spec": nil, "specs": nil}, noRule},
		"empty specs":             {map[string]any{"specs": []any{}}, noRule},
		"a rule as properties":    {ciliumRule("db"), notAPolicy},
		"metadata":                {map[string]any{"metadata": map[string]any{"labels": map[string]any{"a": "b"}}, "spec": ciliumRule("db")}, notAPolicy},
		"status":                  {map[string]any{"status": map[string]any{}, "spec": ciliumRule("db")}, notAPolicy},
		"spec a list":             {map[string]any{"spec": []any{ciliumRule("db")}}, notAPolicy},
		"specs a map":             {map[string]any{"specs": ciliumRule("db")}, notAPolicy},
		"unknown rule key":        {map[string]any{"spec": with("podSelector", map[string]any{})}, notAPolicy},
		"unknown ingress key":     {map[string]any{"spec": with("ingress", []any{map[string]any{"from": []any{}}})}, notAPolicy},
		"null specs entry":        {map[string]any{"specs": []any{ciliumRule("db"), nil}}, "specs[1]"},
		"typed nil specs entry":   {map[string]any{"specs": []any{map[string]any(nil)}}, "specs[0]"},
		"both selectors":          {map[string]any{"spec": with("nodeSelector", map[string]any{"matchLabels": map[string]any{"node-role": "edge"}})}, "spec: " + both},
		"both selectors, empty":   {map[string]any{"spec": with("nodeSelector", map[string]any{})}, "spec: " + both},
		"both selectors, specs":   {map[string]any{"specs": []any{ciliumRule("db"), with("nodeSelector", map[string]any{})}}, "specs[1]: " + both},
		"no selector":             {map[string]any{"spec": with("endpointSelector", nil)}, "spec: " + neither},
		"null selector":           {map[string]any{"spec": nullSelector}, "spec: " + neither},
		"no selector, specs":      {map[string]any{"specs": []any{ciliumNodeRule("edge"), with("endpointSelector", nil)}}, "specs[1]: " + neither},
		"no entry":                {map[string]any{"spec": with("ingress", nil)}, "spec: " + noEntry},
		"empty entry list":        {map[string]any{"spec": with("ingress", []any{})}, "spec: " + noEntry},
		"no entry, specs":         {map[string]any{"spec": ciliumRule("db"), "specs": []any{with("ingress", nil)}}, "specs[0]: " + noEntry},
		"two spellings":           {map[string]any{"spec": ciliumRule("db"), "Spec": ciliumRule("db")}, "sets the same field as"},
		"two spellings in a rule": {map[string]any{"spec": with("enableDefaultDeny", map[string]any{"Egress": true, "egress": false})}, "spec.enableDefaultDeny.egress: sets the same field as spec.enableDefaultDeny.Egress"},
	} {
		t.Run(name, func(t *testing.T) {
			err := clusterwideErr(tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestCiliumClusterwideNetworkPolicyHandler_RefusesUnknownSelectorKey: Cilium's
// selector unmarshals itself, so the strict decode does not reach inside it,
// and a misspelt key would leave the empty selector, which here selects every
// endpoint or every node of the cluster. The key is refused by its path, in a
// node selector as in an endpoint selector.
func TestCiliumClusterwideNetworkPolicyHandler_RefusesUnknownSelectorKey(t *testing.T) {
	misspelt := map[string]any{"matchLabel": map[string]any{"role": "db"}}
	node := func(key string, value any) map[string]any {
		rule := ciliumNodeRule("edge")
		rule[key] = value
		return rule
	}
	endpoint := func(key string, value any) map[string]any {
		rule := ciliumRule("db")
		rule[key] = value
		return rule
	}
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"nodeSelector":      {map[string]any{"spec": node("nodeSelector", misspelt)}, "spec.nodeSelector.matchLabel"},
		"endpointSelector":  {map[string]any{"spec": endpoint("endpointSelector", misspelt)}, "spec.endpointSelector.matchLabel"},
		"fromNodes":         {map[string]any{"spec": node("ingress", []any{map[string]any{"fromNodes": []any{misspelt}}})}, "spec.ingress[0].fromNodes[0].matchLabel"},
		"cidrGroupSelector": {map[string]any{"spec": node("egress", []any{map[string]any{"toCIDRSet": []any{map[string]any{"cidrGroupSelector": misspelt}}}})}, "spec.egress[0].toCIDRSet[0].cidrGroupSelector.matchLabel"},
		"specs":             {map[string]any{"specs": []any{ciliumRule("db"), node("nodeSelector", misspelt)}}, "specs[1].nodeSelector.matchLabel"},
		"icmps field":       {map[string]any{"spec": node("egress", []any{map[string]any{"icmps": []any{map[string]any{"fields": []any{map[string]any{"type": 8, "code": 0}}}}}})}, "spec.egress[0].icmps[0].fields[0].code"},
		"rule label":        {map[string]any{"spec": node("labels", []any{map[string]any{"key": "team", "sources": "k8s"}})}, "spec.labels[0].sources"},
	} {
		t.Run(name, func(t *testing.T) {
			err := clusterwideErr(tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want+": unknown field") {
				t.Fatalf("err = %v, want the unknown field %q", err, tc.want)
			}
		})
	}
}

// ciliumCompleteRule is a rule that authors every object under which the API
// requires a field, with those fields: the control of
// TestCiliumClusterwideNetworkPolicyHandler_RequiredFields.
//
// Every object in it is its own value, so that a test can take a field out of
// one position without taking it out of the others.
func ciliumCompleteRule() map[string]any {
	selector := func() map[string]any {
		return map[string]any{"matchExpressions": []any{map[string]any{"key": "tier", "operator": "Exists"}}}
	}
	secret := func() map[string]any {
		return map[string]any{"secret": map[string]any{"name": "edge-tls"}}
	}
	ports := func() map[string]any {
		return map[string]any{
			"ports":          []any{map[string]any{"port": "443", "protocol": "TCP"}},
			"terminatingTLS": secret(),
			"originatingTLS": secret(),
			"listener": map[string]any{
				"name":        "edge",
				"priority":    1,
				"envoyConfig": map[string]any{"kind": "CiliumClusterwideEnvoyConfig", "name": "edge-listener"},
			},
			"rules": map[string]any{"http": []any{map[string]any{"headerMatches": []any{
				map[string]any{"name": "X-Token", "secret": map[string]any{"name": "edge-token"}},
			}}}},
		}
	}
	icmps := func() []any {
		return []any{map[string]any{"fields": []any{map[string]any{"type": 8}}}}
	}
	return map[string]any{
		"endpointSelector": selector(),
		"labels":           []any{map[string]any{"key": "team", "source": "k8s"}},
		"ingress": []any{map[string]any{
			"fromEndpoints":  []any{selector()},
			"fromNodes":      []any{selector()},
			"fromCIDRSet":    []any{map[string]any{"cidrGroupSelector": selector()}},
			"authentication": map[string]any{"mode": "required"},
			"toPorts":        []any{ports()},
		}, map[string]any{"icmps": icmps()}},
		"ingressDeny": []any{map[string]any{"fromEndpoints": []any{selector()}}, map[string]any{"icmps": icmps()}},
		"egress": []any{map[string]any{
			"toEndpoints":    []any{selector()},
			"toNodes":        []any{selector()},
			"toCIDRSet":      []any{map[string]any{"cidrGroupSelector": selector()}},
			"authentication": map[string]any{"mode": "required"},
			"toPorts":        []any{ports()},
		}, map[string]any{
			"toServices": []any{map[string]any{"k8sServiceSelector": map[string]any{"selector": selector()}}},
		}, map[string]any{"icmps": icmps()}},
		"egressDeny": []any{map[string]any{
			"toServices": []any{map[string]any{"k8sServiceSelector": map[string]any{"selector": map[string]any{}}}},
		}},
	}
}

// at walks a decoded document by map keys and list indices.
func at(t *testing.T, doc any, path ...any) map[string]any {
	t.Helper()
	for _, step := range path {
		switch s := step.(type) {
		case string:
			doc = doc.(map[string]any)[s]
		case int:
			doc = doc.([]any)[s]
		}
	}
	m, ok := doc.(map[string]any)
	if !ok {
		t.Fatalf("no object at %v", path)
	}
	return m
}

// TestCiliumClusterwideNetworkPolicyHandler_RequiredFields: a field the API
// requires that the Cilium type would write empty when it is not authored is
// refused where its parent is authored, by its path with the indices. The
// control is the same rule with every such field authored, which is emitted as
// written. The list is held to the CRD by
// TestCiliumClusterwideNetworkPolicy_RequiredMatchCRD; these are positions of
// each shape in it.
func TestCiliumClusterwideNetworkPolicyHandler_RequiredFields(t *testing.T) {
	_, manifest := generateClusterwidePolicy(t, map[string]any{"spec": ciliumCompleteRule(), "specs": []any{ciliumCompleteRule()}})
	sameJSON(t, "the complete rule under spec", manifest["spec"], ciliumCompleteRule())
	sameJSON(t, "the complete rule under specs", manifest["specs"], []any{ciliumCompleteRule()})

	for name, tc := range map[string]struct {
		parent []any
		drop   string
		want   string
	}{
		"selector expression's operator": {[]any{"endpointSelector", "matchExpressions", 0}, "operator", "endpointSelector.matchExpressions[0].operator"},
		"selector expression's key":      {[]any{"ingress", 0, "fromNodes", 0, "matchExpressions", 0}, "key", "ingress[0].fromNodes[0].matchExpressions[0].key"},
		"deny list's selector":           {[]any{"ingressDeny", 0, "fromEndpoints", 0, "matchExpressions", 0}, "operator", "ingressDeny[0].fromEndpoints[0].matchExpressions[0].operator"},
		"CIDR group selector":            {[]any{"egress", 0, "toCIDRSet", 0, "cidrGroupSelector", "matchExpressions", 0}, "key", "egress[0].toCIDRSet[0].cidrGroupSelector.matchExpressions[0].key"},
		"authentication mode":            {[]any{"ingress", 0, "authentication"}, "mode", "ingress[0].authentication.mode"},
		"TLS context's secret":           {[]any{"ingress", 0, "toPorts", 0, "terminatingTLS"}, "secret", "ingress[0].toPorts[0].terminatingTLS.secret"},
		"TLS secret's name":              {[]any{"egress", 0, "toPorts", 0, "originatingTLS", "secret"}, "name", "egress[0].toPorts[0].originatingTLS.secret.name"},
		"listener's name":                {[]any{"egress", 0, "toPorts", 0, "listener"}, "name", "egress[0].toPorts[0].listener.name"},
		"listener's Envoy configuration": {[]any{"ingress", 0, "toPorts", 0, "listener"}, "envoyConfig", "ingress[0].toPorts[0].listener.envoyConfig"},
		"Envoy configuration's name":     {[]any{"ingress", 0, "toPorts", 0, "listener", "envoyConfig"}, "name", "ingress[0].toPorts[0].listener.envoyConfig.name"},
		// Optional to the API; the type would write them as "" and 0, which the
		// API refuses.
		"Envoy configuration's kind":    {[]any{"egress", 0, "toPorts", 0, "listener", "envoyConfig"}, "kind", "egress[0].toPorts[0].listener.envoyConfig.kind"},
		"listener's priority":           {[]any{"ingress", 0, "toPorts", 0, "listener"}, "priority", "ingress[0].toPorts[0].listener.priority"},
		"header match's name":           {[]any{"egress", 0, "toPorts", 0, "rules", "http", 0, "headerMatches", 0}, "name", "egress[0].toPorts[0].rules.http[0].headerMatches[0].name"},
		"header match's secret name":    {[]any{"ingress", 0, "toPorts", 0, "rules", "http", 0, "headerMatches", 0, "secret"}, "name", "ingress[0].toPorts[0].rules.http[0].headerMatches[0].secret.name"},
		"Service selector":              {[]any{"egressDeny", 0, "toServices", 0, "k8sServiceSelector"}, "selector", "egressDeny[0].toServices[0].k8sServiceSelector.selector"},
		"Service selector's expression": {[]any{"egress", 1, "toServices", 0, "k8sServiceSelector", "selector", "matchExpressions", 0}, "operator", "egress[1].toServices[0].k8sServiceSelector.selector.matchExpressions[0].operator"},
	} {
		for _, position := range []string{"spec", "specs[0]"} {
			t.Run(name+" in "+position, func(t *testing.T) {
				rule := ciliumCompleteRule()
				parent := at(t, rule, tc.parent...)
				if _, ok := parent[tc.drop]; !ok {
					t.Fatalf("the complete rule has no %s to leave out", tc.want)
				}
				delete(parent, tc.drop)
				props := map[string]any{"spec": rule}
				if position != "spec" {
					props = map[string]any{"specs": []any{rule}}
				}
				err := clusterwideErr(props)
				if want := position + "." + tc.want + ": required ("; err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %v, want one mentioning %q", err, want)
				}
			})
		}
	}
}

// TestCiliumClusterwideNetworkPolicyHandler_RefusedByCiliumsDecoding: two
// fields the API requires never reach the required list, because Cilium's own
// decoding refuses the object first: an ICMP field without its `type`, whose
// decoding dereferences a nil pointer (the build does not crash), and a rule
// label in its object form without its `key`.
func TestCiliumClusterwideNetworkPolicyHandler_RefusedByCiliumsDecoding(t *testing.T) {
	with := func(key string, value any) map[string]any {
		rule := ciliumNodeRule("edge")
		rule[key] = value
		return rule
	}
	icmp := func(field map[string]any) []any {
		return []any{map[string]any{"icmps": []any{map[string]any{"fields": []any{field}}}}}
	}
	for name, props := range map[string]map[string]any{
		"ICMP field without type":       {"spec": with("egress", icmp(map[string]any{"family": "IPv4"}))},
		"ICMP field with a null type":   {"spec": with("ingressDeny", icmp(map[string]any{"family": "IPv6", "type": nil}))},
		"ICMP field without type, list": {"specs": []any{ciliumRule("db"), with("egressDeny", icmp(map[string]any{}))}},
		"label without key":             {"spec": with("labels", []any{map[string]any{"value": "db", "source": "k8s"}})},
	} {
		t.Run(name, func(t *testing.T) {
			err := clusterwideErr(props)
			if err == nil || !strings.Contains(err.Error(), "properties do not decode into a cilium.io/v2 CiliumClusterwideNetworkPolicy") {
				t.Fatalf("err = %v, want the decode refusal", err)
			}
		})
	}
}

// TestCiliumClusterwideNetworkPolicyHandler_ICMPFamilyIsDefaulted: an ICMP
// field's `family` is left out when it is not authored, and the API fills
// IPv4. An authored empty one reads the same, to Cilium too, and is left out as
// well.
func TestCiliumClusterwideNetworkPolicyHandler_ICMPFamilyIsDefaulted(t *testing.T) {
	rule := ciliumNodeRule("edge")
	rule["egress"] = []any{map[string]any{"icmps": []any{map[string]any{"fields": []any{
		map[string]any{"type": 8},
		map[string]any{"type": "EchoRequest", "family": ""},
		map[string]any{"type": 128, "family": "IPv6"},
	}}}}}
	_, manifest := generateClusterwidePolicy(t, map[string]any{"spec": rule})
	sameJSON(t, "icmps fields", at(t, manifest, "spec", "egress", 0, "icmps", 0)["fields"], []any{
		map[string]any{"type": 8},
		map[string]any{"type": "EchoRequest"},
		map[string]any{"type": 128, "family": "IPv6"},
	})
}

// TestCiliumClusterwideNetworkPolicyHandler_LabelSourceIsWritten: a rule label
// in its object form without a `source` carries `source: ""`, which the Cilium
// type writes whether or not it was authored. The API admits it.
func TestCiliumClusterwideNetworkPolicyHandler_LabelSourceIsWritten(t *testing.T) {
	rule := ciliumNodeRule("edge")
	rule["labels"] = []any{map[string]any{"key": "team", "value": "edge"}, "k8s:owner=platform"}
	_, manifest := generateClusterwidePolicy(t, map[string]any{"spec": rule})
	sameJSON(t, "labels", at(t, manifest, "spec")["labels"], []any{
		map[string]any{"key": "team", "value": "edge", "source": ""},
		map[string]any{"key": "owner", "value": "platform", "source": "k8s"},
	})
}

// TestCiliumClusterwideNetworkPolicyConfig_GenerateRefuses: the config is
// exported, so Generate repeats the refusals for one built without the handler.
func TestCiliumClusterwideNetworkPolicyConfig_GenerateRefuses(t *testing.T) {
	all := ciliumapi.NewESFromLabels()
	for name, tc := range map[string]struct {
		cfg  *components.CiliumClusterwideNetworkPolicyConfig
		want string
	}{
		"no rule":        {&components.CiliumClusterwideNetworkPolicyConfig{}, "at least one of 'spec' and 'specs' is required"},
		"nil specs":      {&components.CiliumClusterwideNetworkPolicyConfig{Specs: ciliumapi.Rules{nil}}, "specs[0]: expected a rule, got null"},
		"zero rule":      {&components.CiliumClusterwideNetworkPolicyConfig{Spec: &ciliumapi.Rule{}}, "spec: one of endpointSelector and nodeSelector is required"},
		"both selectors": {&components.CiliumClusterwideNetworkPolicyConfig{Spec: &ciliumapi.Rule{EndpointSelector: all, NodeSelector: all}}, "spec: endpointSelector and nodeSelector are both set"},
		"selector only":  {&components.CiliumClusterwideNetworkPolicyConfig{Spec: &ciliumapi.Rule{NodeSelector: all}}, "spec: at least one of"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tc.cfg.Generate(stack.NewApplication("host-firewall", coreKindNamespace, tc.cfg))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestCiliumClusterwideNetworkPolicyConfig_ReportsNoTraffic: the config is an
// authored object, not a trait's.
func TestCiliumClusterwideNetworkPolicyConfig_ReportsNoTraffic(t *testing.T) {
	var cfg any = &components.CiliumClusterwideNetworkPolicyConfig{}
	if _, ok := cfg.(interface{ TargetComponentName() string }); ok {
		t.Error("CiliumClusterwideNetworkPolicyConfig reports a target component; the NetworkPolicy synthesis would read it as a routing trait")
	}
	if _, ok := cfg.(interface{ ComponentName() string }); ok {
		t.Error("CiliumClusterwideNetworkPolicyConfig names an owning component; it is a component's own config, not a trait's")
	}
}
