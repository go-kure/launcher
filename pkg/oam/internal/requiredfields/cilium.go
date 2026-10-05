package requiredfields

import (
	"maps"
	"slices"
	"strings"
)

// CiliumSelector is the required list of one Cilium label selector under the
// path at ("nodeSelector", "advertisements[].selector"): the two fields the
// API requires of a match expression, each of which the Go type would write
// empty.
func CiliumSelector(at string) map[string]string {
	return map[string]string{
		at + ".matchExpressions[].key":      "the label key the expression applies to",
		at + ".matchExpressions[].operator": "the expression's operator: In, NotIn, Exists or DoesNotExist",
	}
}

// CiliumRule is the required list of one Cilium policy rule under the path at
// ("spec", "specs[]"; "" where the rule is the authored tree itself): every
// field the API requires inside a rule that the Go type writes whether or not
// it was authored, so that the object would not show the omission. Each must be
// authored wherever its parent is. The component kinds' tests hold the list to
// the CRDs of the linked module and to the Cilium rule type
// (TestCiliumNetworkPolicy_RequiredMatchCRD,
// TestCiliumClusterwideNetworkPolicy_RequiredMatchCRD).
//
// Two more are optional to the API and listed all the same, a listener's
// `priority` and the `kind` of its Envoy configuration: the type writes each
// when it is not authored, as 0 and as the empty string, and the API refuses
// both values (a priority is 1 to 100, a kind one of two names). A listener
// that leaves one out cannot be emitted, so it is refused here.
//
// Two of the required ones never reach this list: Cilium's own decoding
// refuses an `icmps` field without its `type` and a label without its `key`
// before the list is read. They are listed all the same, so that the list is
// the CRD's and stays so if that decoding changes.
func CiliumRule(at string) map[string]string {
	const (
		icmpType = "the ICMP type, a number or a name Cilium knows"
		secret   = "the Secret the value is read from"
		name     = "the Secret's name"
	)
	if at != "" {
		at += "."
	}
	out := map[string]string{at + "labels[].key": "the label's key"}
	maps.Copy(out, CiliumSelector(at+"endpointSelector"))
	maps.Copy(out, CiliumSelector(at+"nodeSelector"))
	for _, list := range []string{"ingress", "ingressDeny"} {
		entry := at + list + "[]"
		maps.Copy(out, CiliumSelector(entry+".fromEndpoints[]"))
		maps.Copy(out, CiliumSelector(entry+".fromNodes[]"))
		maps.Copy(out, CiliumSelector(entry+".fromCIDRSet[].cidrGroupSelector"))
		out[entry+".icmps[].fields[].type"] = icmpType
	}
	for _, list := range []string{"egress", "egressDeny"} {
		entry := at + list + "[]"
		services := entry + ".toServices[].k8sServiceSelector.selector"
		maps.Copy(out, CiliumSelector(entry+".toEndpoints[]"))
		maps.Copy(out, CiliumSelector(entry+".toNodes[]"))
		maps.Copy(out, CiliumSelector(entry+".toCIDRSet[].cidrGroupSelector"))
		maps.Copy(out, CiliumSelector(services))
		out[entry+".icmps[].fields[].type"] = icmpType
		out[services] = "the label query over the Services; an empty one selects every Service"
	}
	// What only an allow list holds: a deny entry has no authentication, and
	// its ports carry no listener, no TLS context and no layer 7 rule.
	for _, list := range []string{"ingress", "egress"} {
		entry := at + list + "[]"
		ports := entry + ".toPorts[]"
		headers := ports + ".rules.http[].headerMatches[]"
		maps.Copy(out, map[string]string{
			entry + ".authentication.mode":        "the authentication mode: disabled, required or test-always-fail",
			ports + ".listener.envoyConfig":       "the CiliumEnvoyConfig or CiliumClusterwideEnvoyConfig that defines the listener",
			ports + ".listener.envoyConfig.kind":  "CiliumEnvoyConfig or CiliumClusterwideEnvoyConfig; the API does not require it, but an unauthored one is written empty, which the API refuses",
			ports + ".listener.envoyConfig.name":  "the name of that Envoy configuration",
			ports + ".listener.name":              "the listener's name in that Envoy configuration",
			ports + ".listener.priority":          "1 to 100; the API does not require it, but an unauthored one is written as 0, which the API refuses",
			ports + ".originatingTLS.secret":      secret,
			ports + ".originatingTLS.secret.name": name,
			ports + ".terminatingTLS.secret":      secret,
			ports + ".terminatingTLS.secret.name": name,
			headers + ".name":                     "the header's name",
			headers + ".secret.name":              name,
		})
	}
	return out
}

// CiliumTraitFields names the fields of a Cilium rule the cilium-networkpolicy
// trait publishes as properties, beside its own `name`. The trait's tests hold
// its published properties to this list.
func CiliumTraitFields() []string {
	return []string{"endpointSelector", "ingress", "egress"}
}

// CiliumTraitRule is the required list of the rule a cilium-networkpolicy
// trait authors: CiliumRule's entries under the fields the trait publishes, by
// their paths from the rule itself ("ingress[].authentication.mode"). A deny
// list, a rule label and a node selector are not properties of the trait, and
// nothing under them is listed.
// TestCiliumNetworkPolicyTrait_RequiredMatchCRD, beside the kinds' tests, holds
// it to the CiliumNetworkPolicy CRD of the linked module.
func CiliumTraitRule() map[string]string {
	published := CiliumTraitFields()
	out := map[string]string{}
	for path, says := range CiliumRule("") {
		field, _, _ := strings.Cut(path, ".")
		if slices.Contains(published, strings.TrimSuffix(field, "[]")) {
			out[path] = says
		}
	}
	return out
}
