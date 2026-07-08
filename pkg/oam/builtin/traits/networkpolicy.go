package traits

import (
	"fmt"
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
		ComponentName: app.Name,
	}

	rawIngress, hasIngress := props["ingress"]
	rawEgress, hasEgress := props["egress"]

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

func parseNPIngressRule(raw any, index int) (npIngressRule, error) {
	ruleMap, ok := raw.(map[string]any)
	if !ok {
		return npIngressRule{}, errors.Errorf("ingress[%d]: expected object", index)
	}

	var rule npIngressRule

	if rawFrom, ok := ruleMap["from"].([]any); ok {
		for j, rawPeer := range rawFrom {
			peer, err := parseNPPeer(rawPeer, fmt.Sprintf("ingress[%d].from[%d]", index, j))
			if err != nil {
				return npIngressRule{}, err
			}
			rule.From = append(rule.From, peer)
		}
	}

	if rawPorts, ok := ruleMap["ports"].([]any); ok {
		for j, rawPort := range rawPorts {
			port, err := parseNPPort(rawPort, fmt.Sprintf("ingress[%d].ports[%d]", index, j))
			if err != nil {
				return npIngressRule{}, err
			}
			rule.Ports = append(rule.Ports, port)
		}
	}

	return rule, nil
}

func parseNPEgressRule(raw any, index int) (npEgressRule, error) {
	ruleMap, ok := raw.(map[string]any)
	if !ok {
		return npEgressRule{}, errors.Errorf("egress[%d]: expected object", index)
	}

	var rule npEgressRule

	if rawTo, ok := ruleMap["to"].([]any); ok {
		for j, rawPeer := range rawTo {
			peer, err := parseNPPeer(rawPeer, fmt.Sprintf("egress[%d].to[%d]", index, j))
			if err != nil {
				return npEgressRule{}, err
			}
			rule.To = append(rule.To, peer)
		}
	}

	if rawPorts, ok := ruleMap["ports"].([]any); ok {
		for j, rawPort := range rawPorts {
			port, err := parseNPPort(rawPort, fmt.Sprintf("egress[%d].ports[%d]", index, j))
			if err != nil {
				return npEgressRule{}, err
			}
			rule.Ports = append(rule.Ports, port)
		}
	}

	return rule, nil
}

var validNPPeerKeys = map[string]bool{
	"podSelector":       true,
	"namespaceSelector": true,
	"ipBlock":           true,
}

func parseNPPeer(raw any, path string) (npPeer, error) {
	peerMap, ok := raw.(map[string]any)
	if !ok {
		return npPeer{}, errors.Errorf("%s: expected object", path)
	}

	for key := range peerMap {
		if !validNPPeerKeys[key] {
			return npPeer{}, errors.Errorf("%s: unsupported key %q", path, key)
		}
	}

	var peer npPeer

	if rawPS, ok := peerMap["podSelector"].(map[string]any); ok {
		labels := make(map[string]string)
		if ml, ok := rawPS["matchLabels"].(map[string]any); ok {
			for k, v := range ml {
				labels[k] = fmt.Sprintf("%v", v)
			}
		}
		peer.PodSelector = &metav1.LabelSelector{MatchLabels: labels}
	}

	if rawNS, ok := peerMap["namespaceSelector"].(map[string]any); ok {
		labels := make(map[string]string)
		if ml, ok := rawNS["matchLabels"].(map[string]any); ok {
			for k, v := range ml {
				labels[k] = fmt.Sprintf("%v", v)
			}
		}
		peer.NamespaceSelector = &metav1.LabelSelector{MatchLabels: labels}
	}

	if rawIB, ok := peerMap["ipBlock"].(map[string]any); ok {
		cidr, ok := rawIB["cidr"].(string)
		if !ok || cidr == "" {
			return npPeer{}, errors.Errorf("%s.ipBlock: 'cidr' is required", path)
		}
		ipBlock := &networkingv1.IPBlock{CIDR: cidr}
		if rawExcept, ok := rawIB["except"].([]any); ok {
			for _, e := range rawExcept {
				s, ok := e.(string)
				if !ok {
					return npPeer{}, errors.Errorf("%s.ipBlock.except: expected string values", path)
				}
				ipBlock.Except = append(ipBlock.Except, s)
			}
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

func parseNPPort(raw any, path string) (npPort, error) {
	portMap, ok := raw.(map[string]any)
	if !ok {
		return npPort{}, errors.Errorf("%s: expected object", path)
	}

	var port npPort

	switch v := portMap["port"].(type) {
	case float64:
		port.Port = intstr.FromInt32(int32(v)) //nolint:gosec
	case int:
		port.Port = intstr.FromInt32(int32(v)) //nolint:gosec
	case string:
		if v == "" {
			return npPort{}, errors.Errorf("%s: 'port' must be a number or named port string", path)
		}
		port.Port = intstr.FromString(v)
	default:
		return npPort{}, errors.Errorf("%s: 'port' must be a number or named port string", path)
	}

	port.Protocol = corev1.ProtocolTCP
	if proto, ok := portMap["protocol"].(string); ok {
		upper := strings.ToUpper(proto)
		p, valid := validNPProtocols[upper]
		if !valid {
			return npPort{}, errors.Errorf("%s: invalid protocol %q (must be TCP, UDP, or SCTP)", path, proto)
		}
		port.Protocol = p
	}

	return port, nil
}

// NetworkPolicyConfig implements stack.ApplicationConfig for networkpolicy traits.
type NetworkPolicyConfig struct {
	ComponentName string
	Ingress       []npIngressRule
	Egress        []npEgressRule
}

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
	np := kubernetes.CreateNetworkPolicy(c.ComponentName+"-allow", app.Namespace)
	np.Labels = map[string]string{"app": c.ComponentName}
	np.Annotations = nil
	kubernetes.SetNetworkPolicyPodSelector(np, metav1.LabelSelector{
		MatchLabels: map[string]string{"app": c.ComponentName},
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
