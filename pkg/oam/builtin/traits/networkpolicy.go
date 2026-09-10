package traits

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// NetworkPolicyHandler handles OAM networkpolicy traits.
type NetworkPolicyHandler struct{}

// CanHandle returns true for networkpolicy trait type.
func (h *NetworkPolicyHandler) CanHandle(traitType string) bool {
	return traitType == "networkpolicy"
}

// ValidateAndApplyDefaults rejects any rendering key for this no-rendering trait.
func (h *NetworkPolicyHandler) ValidateAndApplyDefaults(rendering map[string]any) (map[string]any, error) {
	if _, err := builtin.DecodeStrict[builtin.NetworkPolicyRendering](rendering); err != nil {
		return nil, errors.Wrap(err, "networkpolicy rendering")
	}
	return rendering, nil
}

// PropertySchema declares the networkpolicy trait's user-facing properties.
func (h *NetworkPolicyHandler) PropertySchema() map[string]oam.PropertySchema {
	labelSelector := oam.PropertySchema{
		Type:        oam.PropertyTypeObject,
		Description: "Label selector matching the pods or namespaces this peer applies to.",
		Properties: map[string]oam.PropertySchema{
			"matchLabels": {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Label key/value pairs a pod or namespace must carry to match."},
		},
	}
	peer := oam.PropertySchema{
		Type:        oam.PropertyTypeObject,
		Description: "A network peer selected by pod/namespace label selectors or an IP block.",
		Properties: map[string]oam.PropertySchema{
			"podSelector":       labelSelector,
			"namespaceSelector": labelSelector,
			"ipBlock": {
				Type:        oam.PropertyTypeObject,
				Description: "An IP block (CIDR with optional exceptions) this rule applies to.",
				Properties: map[string]oam.PropertySchema{
					"cidr":   {Type: oam.PropertyTypeString, Required: true, Description: "CIDR range the rule applies to."},
					"except": {Type: oam.PropertyTypeArray, Description: "CIDR ranges to exclude from the block.", Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A CIDR range excluded from the block."}},
				},
			},
		},
	}
	// port is an int-or-string union, so the port item is kept open beyond `protocol`.
	port := oam.PropertySchema{
		Type:                 oam.PropertyTypeObject,
		AdditionalProperties: true,
		Description:          "A port (number or named port) with its protocol.",
		Properties: map[string]oam.PropertySchema{
			"protocol": {Type: oam.PropertyTypeString, Default: "TCP", Enum: []any{"TCP", "UDP", "SCTP"}, Description: "IP protocol for the port (TCP, UDP, or SCTP)."},
		},
	}
	// peerList is a helper: the direction key and the surrounding descriptions differ
	// between ingress and egress, so each accurate description is passed in.
	peerList := func(dir, listDesc, ruleDesc, peersDesc, portsDesc string) oam.PropertySchema {
		return oam.PropertySchema{
			Type:        oam.PropertyTypeArray,
			Description: listDesc,
			Items: &oam.PropertySchema{
				Type:        oam.PropertyTypeObject,
				Description: ruleDesc,
				Properties: map[string]oam.PropertySchema{
					dir:     {Type: oam.PropertyTypeArray, Description: peersDesc, Items: &peer},
					"ports": {Type: oam.PropertyTypeArray, Description: portsDesc, Items: &port},
				},
			},
		}
	}
	return map[string]oam.PropertySchema{
		"ingress": peerList("from",
			"Ingress rules allowing inbound traffic to the workload.",
			"A single ingress rule pairing allowed peers with ports.",
			"Peers allowed to connect to the workload.",
			"Ports on the workload the peers may connect to."),
		"egress": peerList("to",
			"Egress rules allowing outbound traffic from the workload.",
			"A single egress rule pairing allowed peers with ports.",
			"Peers the workload is allowed to connect to.",
			"Destination ports the workload may connect to."),
	}
}

// Apply creates a NetworkPolicy resource appended to the bundle.
func (h *NetworkPolicyHandler) Apply(trait *oam.Trait, app *stack.Application, bundle *stack.Bundle) error {
	config, err := h.parseProperties(trait.Properties, app)
	if err != nil {
		return err
	}

	npApp := stack.NewApplication(
		app.Name+"-networkpolicy",
		app.Namespace,
		config,
	)
	bundle.Applications = append(bundle.Applications, npApp)
	return nil
}

func (h *NetworkPolicyHandler) parseProperties(props map[string]any, app *stack.Application) (*NetworkPolicyConfig, error) {
	config := &NetworkPolicyConfig{
		componentName: app.Name,
	}

	rawIngress, hasIngress := props["ingress"]
	rawEgress, hasEgress := props["egress"]

	// A null reads as absence before the guard below, not after it. Both keys are
	// optional individually and required jointly, so this is the one guard in the
	// file a null can satisfy while contributing nothing: a TYPED nil satisfies the
	// `.([]any)` assertion with ok=true and a nil slice, whose range body never
	// runs, so `ingress:` with no value used to produce an empty NetworkPolicy that
	// selects the component's pods and permits nothing -- a default-deny, silently,
	// from a document that asked for no such thing. An UNTYPED nil failed the same
	// assertion and reported "'ingress' must be an array", which is the wrong
	// diagnostic for a key that is absent rather than mistyped. Clearing the flags
	// makes both shapes absent, so the joint requirement is what actually reports
	// (go-kure/launcher#430, predicate in go-kure/launcher#465).
	if hasIngress && oam.IsNullValue(rawIngress) {
		hasIngress = false
	}
	if hasEgress && oam.IsNullValue(rawEgress) {
		hasEgress = false
	}

	if !hasIngress && !hasEgress {
		return nil, errors.New("at least one of 'ingress' or 'egress' must be specified")
	}

	if hasIngress {
		ingressRules, ok := rawIngress.([]any)
		if !ok {
			return nil, errors.New("'ingress' must be an array")
		}
		for i, rawRule := range ingressRules {
			rule, err := parseNPIngressRule(rawRule, i)
			if err != nil {
				return nil, err
			}
			config.Ingress = append(config.Ingress, rule)
		}
	}

	if hasEgress {
		egressRules, ok := rawEgress.([]any)
		if !ok {
			return nil, errors.New("'egress' must be an array")
		}
		for i, rawRule := range egressRules {
			rule, err := parseNPEgressRule(rawRule, i)
			if err != nil {
				return nil, err
			}
			config.Egress = append(config.Egress, rule)
		}
	}

	return config, nil
}

// validNPIngressRuleKeys and validNPEgressRuleKeys are the two rule key sets
// PropertySchema declares. They are separate maps rather than one shared set
// because `from` and `to` are direction-specific: an ingress rule carrying `to` is
// a document that meant something else, and accepting it silently would leave the
// rule with no source constraint at all.
var validNPIngressRuleKeys = map[string]bool{
	"from":  true,
	"ports": true,
}

var validNPEgressRuleKeys = map[string]bool{
	"to":    true,
	"ports": true,
}

func parseNPIngressRule(raw any, index int) (npIngressRule, error) {
	// parseNPPeer's envelope guard, one level up and for a sharper reason. A TYPED
	// nil satisfies the assertion below with a nil map, which has no keys: the
	// `from` and `ports` reads both miss and the rule is accepted with neither —
	// and networking.k8s.io/v1 defines a rule with an empty `from` as matching ALL
	// sources and an empty `ports` as matching ALL ports (k8s.io/api
	// networking/v1/types.go:112-130), so a null element widened the policy to
	// allow-all. An UNTYPED nil failed the same assertion and was rejected, so the
	// two shapes disagreed here exactly as they did at the peer, but in the
	// fail-OPEN direction. A rule sits in a list, so absence has no meaning for it
	// and an authored `- {}` already expresses the empty rule
	// (go-kure/launcher#430).
	if oam.IsNullValue(raw) {
		return npIngressRule{}, errors.Errorf("ingress[%d]: expected object", index)
	}
	ruleMap, ok := raw.(map[string]any)
	if !ok {
		return npIngressRule{}, errors.Errorf("ingress[%d]: expected object", index)
	}

	var rule npIngressRule
	path := fmt.Sprintf("ingress[%d]", index)

	// The rule's own key set, closed for the same reason as the peer's and with a
	// sharper consequence: `from` is the rule's only source constraint, so a
	// misspelt one used to be dropped in silence and leave a rule that matches ALL
	// sources. Sorted for a deterministic diagnostic.
	for _, k := range slices.Sorted(maps.Keys(ruleMap)) {
		if !validNPIngressRuleKeys[k] {
			return npIngressRule{}, errors.Errorf("%s: unsupported key %q", path, k)
		}
	}

	rawFrom, _, err := nonNullArray(ruleMap, "from", path)
	if err != nil {
		return npIngressRule{}, err
	}
	for j, rawPeer := range rawFrom {
		peer, err := parseNPPeer(rawPeer, fmt.Sprintf("%s.from[%d]", path, j))
		if err != nil {
			return npIngressRule{}, err
		}
		rule.From = append(rule.From, peer)
	}

	rawPorts, _, err := nonNullArray(ruleMap, "ports", path)
	if err != nil {
		return npIngressRule{}, err
	}
	for j, rawPort := range rawPorts {
		port, err := parseNPPort(rawPort, fmt.Sprintf("%s.ports[%d]", path, j))
		if err != nil {
			return npIngressRule{}, err
		}
		rule.Ports = append(rule.Ports, port)
	}

	return rule, nil
}

func parseNPEgressRule(raw any, index int) (npEgressRule, error) {
	// The egress half of parseNPIngressRule's null guard; see the reasoning there.
	if oam.IsNullValue(raw) {
		return npEgressRule{}, errors.Errorf("egress[%d]: expected object", index)
	}
	ruleMap, ok := raw.(map[string]any)
	if !ok {
		return npEgressRule{}, errors.Errorf("egress[%d]: expected object", index)
	}

	var rule npEgressRule
	path := fmt.Sprintf("egress[%d]", index)

	// The egress half of the ingress rule's key set; see the reasoning there. `to`
	// is this rule's only destination constraint.
	for _, k := range slices.Sorted(maps.Keys(ruleMap)) {
		if !validNPEgressRuleKeys[k] {
			return npEgressRule{}, errors.Errorf("%s: unsupported key %q", path, k)
		}
	}

	rawTo, _, err := nonNullArray(ruleMap, "to", path)
	if err != nil {
		return npEgressRule{}, err
	}
	for j, rawPeer := range rawTo {
		peer, err := parseNPPeer(rawPeer, fmt.Sprintf("%s.to[%d]", path, j))
		if err != nil {
			return npEgressRule{}, err
		}
		rule.To = append(rule.To, peer)
	}

	rawPorts, _, err := nonNullArray(ruleMap, "ports", path)
	if err != nil {
		return npEgressRule{}, err
	}
	for j, rawPort := range rawPorts {
		port, err := parseNPPort(rawPort, fmt.Sprintf("%s.ports[%d]", path, j))
		if err != nil {
			return npEgressRule{}, err
		}
		rule.Ports = append(rule.Ports, port)
	}

	return rule, nil
}

var validNPPeerKeys = map[string]bool{
	"podSelector":       true,
	"namespaceSelector": true,
	"ipBlock":           true,
}

// validNPIPBlockKeys closes the ipBlock object over the two fields
// networking.k8s.io/v1 gives it (k8s.io/api networking/v1/types.go:182-194), for
// the same reason validNPPeerKeys closes the peer: `except` is an EXCLUSION, so a
// misspelt key dropped it and rendered a block WIDER than the document authored.
var validNPIPBlockKeys = map[string]bool{
	"cidr":   true,
	"except": true,
}

// nonNullObject reads an optional object-valued key, treating an explicit null —
// untyped (a YAML `key:` with no value) or typed (map[string]any(nil), what an
// uninitialized Go map in a lowering rule produces) — as an ABSENT key, per the
// null contract oam.IsNullValue carries.
//
// A bare `m[key].(map[string]any)` cannot do this: a typed nil satisfies the
// assertion with ok=true and a nil map, so the key reads as an authored empty
// object. For a peer selector that is not a cosmetic difference — an empty
// metav1.LabelSelector matches EVERY namespace in networking.k8s.io/v1, while a
// nil one applies no namespace constraint at all, which alongside a podSelector
// leaves the peer scoped to the policy's own namespace (k8s.io/api
// networking/v1/types.go:199-222). The other
// metav1.LabelSelector reader in this repo — parseLabelSelector
// (components/volumeclaim_spec.go), which takes its `selector` and `matchLabels`
// through optionalObject — calls the same value absence, so the widest and the
// narrowest reading of one input differed by a Go type no document can express
// (go-kure/launcher#430).
//
// A wrong-typed value is an ERROR, not a third flavour of absence. Silently
// discarding it is what made `namespaceSelector: {matchLabels: "prod"}` produce a
// selector with no labels — an EMPTY selector, which matches every namespace, so a
// malformed constraint widened the peer to the maximum instead of failing. The
// peer envelope and the peer key set are both already rejected by name two callers
// up, and the auto-synthesis parser in this same package rejects both halves of
// the identical shape by name — a wrong-typed podSelector at
// networkpolicy_auto.go:82-86, a wrong-typed matchLabels at :110-113 — so silence
// here was the odd one out rather than a contract.
func nonNullObject(m map[string]any, key, path string) (map[string]any, bool, error) {
	value, present := m[key]
	if !present || oam.IsNullValue(value) {
		return nil, false, nil
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, false, errors.Errorf("%s.%s: expected object, got %T", path, key, value)
	}
	return obj, true, nil
}

// nonNullArray is nonNullObject for a list-valued key: null and absent are the same
// answer, and a wrong-typed value is an error rather than a third flavour of
// absence.
//
// The error half is the load-bearing one. Every list this parser reads is a
// CONSTRAINT, so discarding a mistyped one renders something WIDER than the
// document asked for: a rule whose `from` was dropped matches all sources and one
// whose `ports` was dropped matches all ports (k8s.io/api networking/v1/types.go
// :112-130), and an ipBlock whose `except` was dropped keeps the exclusions the
// author wrote out of the rendered block. A bare `m[key].([]any)` comma-ok gave
// exactly that: `from: "web"` parsed to a rule with no peers, silently, at render
// time (go-kure/launcher#430).
func nonNullArray(m map[string]any, key, path string) ([]any, bool, error) {
	value, present := m[key]
	if !present || oam.IsNullValue(value) {
		return nil, false, nil
	}
	arr, ok := value.([]any)
	if !ok {
		return nil, false, errors.Errorf("%s.%s: expected array, got %T", path, key, value)
	}
	return arr, true, nil
}

// validNPSelectorKeys is the key set of a peer selector. metav1.LabelSelector has
// exactly two fields, matchLabels and matchExpressions, and this parser implements
// the first; the second is listed nowhere, so it is rejected as unsupported rather
// than accepted and dropped.
//
// Rejecting by name is not a lint here, it is the same fail-open the rest of this
// file closes. An unrecognized key used to leave the selector ALLOCATED with no
// labels, and a non-nil empty metav1.LabelSelector matches EVERY namespace (or
// every pod) — so `namespaceSelector: {matchExpressions: [...]}`, a perfectly
// ordinary Kubernetes selector, widened the peer to the maximum instead of failing.
// A wrong-TYPED selector was already rejected for exactly that reason
// (nonNullObject above); an unrecognized KEY took the same silent path.
// PropertySchema declares this selector closed over matchLabels already, but
// NetworkPolicyHandler.Apply does not run the schema preflight, so the parser is
// the only guard on the authored path (go-kure/launcher#430).
var validNPSelectorKeys = map[string]bool{
	"matchLabels": true,
}

// npLabelValue renders one matchLabels value, and reports whether the value is
// something a label can hold at all.
//
// %v renders anything, which is the defect: a map value renders as "map[a:1]" and
// a slice as "[x y]" — strings no label value may contain, so the document renders
// and the API server rejects it one layer away from the cause. That is the reason
// the null guard beside this call already gives, and a null is not the only value
// it covers. Scalars are unaffected: a YAML/JSON decoder produces string, bool and
// one of the numeric kinds for an ordinary label value, and each keeps its existing
// %v rendering (go-kure/launcher#430).
func npLabelValue(value any) (string, bool) {
	switch value.(type) {
	case string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return fmt.Sprintf("%v", value), true
	default:
		return "", false
	}
}

// parseNPLabelSelector reads one optional selector-shaped key of a peer —
// podSelector or namespaceSelector — and returns nil when it is absent or null.
//
// The nil return is the whole point: a nil *metav1.LabelSelector does not constrain
// the peer on that axis, while a non-nil empty one matches EVERY namespace (or
// every pod), so "absent" must never be represented by an allocated selector
// (go-kure/launcher#430).
func parseNPLabelSelector(peerMap map[string]any, key, path string) (*metav1.LabelSelector, error) {
	raw, present, err := nonNullObject(peerMap, key, path)
	if err != nil || !present {
		return nil, err
	}
	// Sorted, so a selector carrying two unrecognized keys reports the same one
	// every run; map iteration order would make the diagnostic a coin flip.
	for _, k := range slices.Sorted(maps.Keys(raw)) {
		if !validNPSelectorKeys[k] {
			return nil, errors.Errorf("%s.%s: unsupported key %q", path, key, k)
		}
	}
	labels := make(map[string]string)
	ml, present, err := nonNullObject(raw, "matchLabels", path+"."+key)
	if err != nil {
		return nil, err
	}
	if present {
		for _, k := range slices.Sorted(maps.Keys(ml)) {
			// The last depth the contract reaches, and %v does not respect it: a
			// null label VALUE formatted as "<nil>" (or "map[]"/"[]" for a typed
			// nil map/slice) is not a label value at all, it is a string the API
			// server rejects — after the document has already rendered. Dropping
			// the entry instead would remove an authored constraint and WIDEN the
			// selector, so a null here is an error, like a null list element
			// (go-kure/launcher#430).
			if oam.IsNullValue(ml[k]) {
				return nil, errors.Errorf("%s.%s.matchLabels: %q has no value", path, key, k)
			}
			value, ok := npLabelValue(ml[k])
			if !ok {
				return nil, errors.Errorf("%s.%s.matchLabels: %q must be a string, number or boolean, got %T", path, key, k, ml[k])
			}
			labels[k] = value
		}
	}
	return &metav1.LabelSelector{MatchLabels: labels}, nil
}

func parseNPPeer(raw any, path string) (npPeer, error) {
	// The null check precedes the assertion because a TYPED nil satisfies it with
	// ok=true and a nil map, which then has no keys: the unknown-key loop finds
	// nothing to reject, all three selector reads miss, and the peer is accepted as
	// an empty one. An UNTYPED nil fails the same assertion and is rejected — so
	// without this, the two nil shapes disagree at the envelope while agreeing
	// inside it, which is the divergence the selectors below exist to remove. No
	// document can express the difference between them (go-kure/launcher#430).
	//
	// A null peer is an ERROR rather than an absent one: it sits in a list, and
	// dropping an element silently is the failure class this parser is being moved
	// away from. That matches what an untyped nil already did.
	if oam.IsNullValue(raw) {
		return npPeer{}, errors.Errorf("%s: expected object", path)
	}
	peerMap, ok := raw.(map[string]any)
	if !ok {
		return npPeer{}, errors.Errorf("%s: expected object", path)
	}

	// Sorted, so a peer carrying two unrecognized keys names the same one every
	// run; map iteration order would make the diagnostic a coin flip.
	for _, key := range slices.Sorted(maps.Keys(peerMap)) {
		if !validNPPeerKeys[key] {
			return npPeer{}, errors.Errorf("%s: unsupported key %q", path, key)
		}
	}

	var peer npPeer

	podSelector, err := parseNPLabelSelector(peerMap, "podSelector", path)
	if err != nil {
		return npPeer{}, err
	}
	peer.PodSelector = podSelector

	namespaceSelector, err := parseNPLabelSelector(peerMap, "namespaceSelector", path)
	if err != nil {
		return npPeer{}, err
	}
	peer.NamespaceSelector = namespaceSelector

	rawIB, hasIB, err := nonNullObject(peerMap, "ipBlock", path)
	if err != nil {
		return npPeer{}, err
	}
	if hasIB {
		// Sorted, for the same reason as the peer key loop above.
		for _, key := range slices.Sorted(maps.Keys(rawIB)) {
			if !validNPIPBlockKeys[key] {
				return npPeer{}, errors.Errorf("%s.ipBlock: unsupported key %q", path, key)
			}
		}
		rawCIDR, hasCIDR := rawIB["cidr"]
		cidr, isString := rawCIDR.(string)
		switch {
		case !hasCIDR || oam.IsNullValue(rawCIDR) || (isString && cidr == ""):
			return npPeer{}, errors.Errorf("%s.ipBlock: 'cidr' is required", path)
		case !isString:
			// Was folded into the required-cidr message, which reads as "you
			// forgot it" for a key that is present and mistyped.
			return npPeer{}, errors.Errorf("%s.ipBlock.cidr: expected string, got %T", path, rawCIDR)
		}
		ipBlock := &networkingv1.IPBlock{CIDR: cidr}
		rawExcept, _, err := nonNullArray(rawIB, "except", path+".ipBlock")
		if err != nil {
			return npPeer{}, err
		}
		for _, e := range rawExcept {
			s, ok := e.(string)
			if !ok {
				return npPeer{}, errors.Errorf("%s.ipBlock.except: expected string values", path)
			}
			ipBlock.Except = append(ipBlock.Except, s)
		}
		peer.IPBlock = ipBlock
	}

	return peer, nil
}

var validNPProtocols = map[string]corev1.Protocol{
	"TCP":  corev1.ProtocolTCP,
	"UDP":  corev1.ProtocolUDP,
	"SCTP": corev1.ProtocolSCTP,
}

// validNPPortKeys closes the port item over the two fields this parser
// implements. It is the one key set in this file the SCHEMA cannot back up:
// PropertySchema keeps the port item open (AdditionalProperties, line 64-71)
// because `port` is an int-or-string union PropertySchema has no way to express,
// so `protcol: UDP` passes every schema check and reaches here — where, before
// this set existed, it was dropped and the port rendered TCP. `endPort` is the
// other name worth rejecting explicitly: it is a real NetworkPolicyPort field
// (k8s.io/api networking/v1/types.go:171-176) that this parser does not
// implement, so accepting it silently would render a single port where the
// document asked for a range — the `matchExpressions` case one list down.
var validNPPortKeys = map[string]bool{
	"port":     true,
	"protocol": true,
}

// npPortNumber renders a numeric `port` as the int32 the wire type carries
// (intstr.FromInt32), reporting whether the value was numeric at all so a named
// port string can still take the other branch.
//
// The previous bare `int32(v)` lost two shapes silently, and a port is a
// constraint like every other value in this trait: one this parser cannot carry
// EXACTLY is an error, never a different port.
//
//   - a fractional float TRUNCATED — `port: 80.9` rendered port 80.
//   - a value outside int32 was implementation-defined (Go spec, Conversions:
//     "the behavior is implementation-dependent" when a float overflows the
//     target). Measured on this host: `port: 4294967376` rendered port 80.
//
// The 1-65535 bound is the API server's own (k8s.io/apimachinery
// pkg/util/validation IsValidPortNum), applied here so the document fails at the
// line that wrote it rather than at apply time. Every integer kind a YAML/JSON
// decode or a lowering rule assembling properties in Go can produce is accepted,
// matching npLabelValue's reach rather than the previous int/float64 pair.
func npPortNumber(value any, path string) (int32, bool, error) {
	var n int64
	switch v := value.(type) {
	case int:
		n = int64(v)
	case int8:
		n = int64(v)
	case int16:
		n = int64(v)
	case int32:
		n = int64(v)
	case int64:
		n = v
	case uint:
		if uint64(v) > math.MaxInt64 {
			return 0, true, errors.Errorf("%s: 'port' %v is out of range (1-65535)", path, value)
		}
		n = int64(v)
	case uint8:
		n = int64(v)
	case uint16:
		n = int64(v)
	case uint32:
		n = int64(v)
	case uint64:
		if v > math.MaxInt64 {
			return 0, true, errors.Errorf("%s: 'port' %v is out of range (1-65535)", path, value)
		}
		n = int64(v)
	case float32:
		return npPortFromFloat(float64(v), value, path)
	case float64:
		return npPortFromFloat(v, value, path)
	default:
		return 0, false, nil
	}
	if n < 1 || n > 65535 {
		return 0, true, errors.Errorf("%s: 'port' %v is out of range (1-65535)", path, value)
	}
	return int32(n), true, nil
}

// npPortFromFloat is npPortNumber's float half: a port has to be a whole number,
// and the range check has to happen in float64 — converting first is the
// implementation-defined step this exists to avoid.
func npPortFromFloat(f float64, original any, path string) (int32, bool, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
		return 0, true, errors.Errorf("%s: 'port' must be a whole number, got %v", path, original)
	}
	if f < 1 || f > 65535 {
		return 0, true, errors.Errorf("%s: 'port' %v is out of range (1-65535)", path, original)
	}
	return int32(f), true, nil
}

func parseNPPort(raw any, path string) (npPort, error) {
	// The third and last list element in this file, so the null guard is here too
	// and every element of every list the trait parses now reports the same thing
	// for a null. Both nil shapes were already REJECTED here — a typed nil reached
	// the `port` switch with a nil map and failed it — so this changes only the
	// diagnostic, from a missing-`port` complaint to the accurate "the element
	// itself is null" (go-kure/launcher#430).
	if oam.IsNullValue(raw) {
		return npPort{}, errors.Errorf("%s: expected object", path)
	}
	portMap, ok := raw.(map[string]any)
	if !ok {
		return npPort{}, errors.Errorf("%s: expected object", path)
	}

	var port npPort

	// The port item's key set; see validNPPortKeys. Sorted, like every other
	// unknown-key loop in this file, so a document with two bad keys reports the
	// same one on every run.
	for _, k := range slices.Sorted(maps.Keys(portMap)) {
		if !validNPPortKeys[k] {
			return npPort{}, errors.Errorf("%s: unsupported key %q", path, k)
		}
	}

	num, numeric, err := npPortNumber(portMap["port"], path)
	switch {
	case err != nil:
		return npPort{}, err
	case numeric:
		port.Port = intstr.FromInt32(num)
	default:
		// A null `port` lands here and reports the same thing an absent one
		// does. NetworkPolicyPort.Port is optional upstream (an absent port
		// matches all ports on the protocol), but this parser has always
		// required it, and widening that is a behaviour change this fix does
		// not make.
		name, ok := portMap["port"].(string)
		if !ok || name == "" {
			return npPort{}, errors.Errorf("%s: 'port' must be a number or named port string", path)
		}
		port.Port = intstr.FromString(name)
	}

	// `protocol` was read through a bare comma-ok assertion, so a non-string —
	// `protocol: [UDP]`, or a lowering rule that assembled a corev1.Protocol
	// rather than a string — was DISCARDED and the port rendered TCP, which is
	// the fail-open shape this trait's whole null/type contract exists to
	// prevent: a policy that permits a protocol the document did not author and
	// denies the one it did. A null is absence, as everywhere else here, and
	// absence is the upstream default of TCP (k8s.io/api
	// networking/v1/types.go:159-162).
	port.Protocol = corev1.ProtocolTCP
	if rawProto, present := portMap["protocol"]; present && !oam.IsNullValue(rawProto) {
		proto, ok := rawProto.(string)
		if !ok {
			return npPort{}, errors.Errorf("%s.protocol: expected string, got %T", path, rawProto)
		}
		p, valid := validNPProtocols[strings.ToUpper(proto)]
		if !valid {
			return npPort{}, errors.Errorf("%s: invalid protocol %q (must be TCP, UDP, or SCTP)", path, proto)
		}
		port.Protocol = p
	}

	return port, nil
}

// NetworkPolicyConfig implements stack.ApplicationConfig for networkpolicy traits.
type NetworkPolicyConfig struct {
	componentName string
	Ingress       []npIngressRule
	Egress        []npEgressRule
}

// ComponentName returns the OAM component this sub-app belongs to, for resource
// provenance attribution.
func (c *NetworkPolicyConfig) ComponentName() string { return c.componentName }

type npIngressRule struct {
	From  []npPeer
	Ports []npPort
}

type npEgressRule struct {
	To    []npPeer
	Ports []npPort
}

type npPeer struct {
	PodSelector       *metav1.LabelSelector
	NamespaceSelector *metav1.LabelSelector
	IPBlock           *networkingv1.IPBlock
}

type npPort struct {
	Port     intstr.IntOrString
	Protocol corev1.Protocol
}

// Generate creates a Kubernetes NetworkPolicy resource.
func (c *NetworkPolicyConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	np := kubernetes.CreateNetworkPolicy(c.componentName+"-allow", app.Namespace)
	np.Labels = map[string]string{"app": c.componentName}
	np.Annotations = nil
	kubernetes.SetNetworkPolicyPodSelector(np, metav1.LabelSelector{
		MatchLabels: map[string]string{"app": c.componentName},
	})

	if len(c.Ingress) > 0 {
		kubernetes.AddNetworkPolicyPolicyType(np, networkingv1.PolicyTypeIngress)
		for _, rule := range c.Ingress {
			ingressRule := networkingv1.NetworkPolicyIngressRule{}
			for _, peer := range rule.From {
				p := networkingv1.NetworkPolicyPeer{}
				if peer.PodSelector != nil {
					p.PodSelector = peer.PodSelector
				}
				if peer.NamespaceSelector != nil {
					p.NamespaceSelector = peer.NamespaceSelector
				}
				if peer.IPBlock != nil {
					p.IPBlock = peer.IPBlock
				}
				kubernetes.AddNetworkPolicyIngressPeer(&ingressRule, p)
			}
			for _, port := range rule.Ports {
				proto := port.Protocol
				portVal := port.Port
				kubernetes.AddNetworkPolicyIngressPort(&ingressRule, networkingv1.NetworkPolicyPort{
					Protocol: &proto,
					Port:     &portVal,
				})
			}
			kubernetes.AddNetworkPolicyIngressRule(np, ingressRule)
		}
	}

	if len(c.Egress) > 0 {
		kubernetes.AddNetworkPolicyPolicyType(np, networkingv1.PolicyTypeEgress)
		for _, rule := range c.Egress {
			egressRule := networkingv1.NetworkPolicyEgressRule{}
			for _, peer := range rule.To {
				p := networkingv1.NetworkPolicyPeer{}
				if peer.PodSelector != nil {
					p.PodSelector = peer.PodSelector
				}
				if peer.NamespaceSelector != nil {
					p.NamespaceSelector = peer.NamespaceSelector
				}
				if peer.IPBlock != nil {
					p.IPBlock = peer.IPBlock
				}
				kubernetes.AddNetworkPolicyEgressPeer(&egressRule, p)
			}
			for _, port := range rule.Ports {
				proto := port.Protocol
				portVal := port.Port
				kubernetes.AddNetworkPolicyEgressPort(&egressRule, networkingv1.NetworkPolicyPort{
					Protocol: &proto,
					Port:     &portVal,
				})
			}
			kubernetes.AddNetworkPolicyEgressRule(np, egressRule)
		}
	}

	obj := client.Object(np)
	return []*client.Object{&obj}, nil
}
