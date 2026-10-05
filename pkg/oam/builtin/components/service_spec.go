package components

import (
	"maps"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file projects the corev1.ServiceSpec fields beyond the four service.go
// reads itself (type, clusterIP, selector, ports), for the `service` kind
// (go-kure/launcher#790). Every one is presence-gated: an unauthored key
// writes nothing, so the API server's own defaults apply (sessionAffinity
// None, internalTrafficPolicy Cluster, and the rest).
//
// The rules are the ones validateService applies to the same fields
// (k8s.io/kubernetes v1.37.1 pkg/apis/core/validation/validation.go), so a
// document that builds here is not refused for them on apply. Where this
// parser refuses something the API accepts, the comment at the check says so.

// ServiceSpecFields are a service component's authored ServiceSpec fields
// beyond type, clusterIP, selector and ports. Each is its zero value unless
// authored, and a zero value emits nothing: every corresponding
// corev1.ServiceSpec field is `omitempty`.
type ServiceSpecFields struct {
	// ExternalName is the DNS name an ExternalName Service aliases, as
	// authored (a trailing dot is kept). "" on every other type.
	ExternalName string

	ExternalTrafficPolicy corev1.ServiceExternalTrafficPolicy
	InternalTrafficPolicy *corev1.ServiceInternalTrafficPolicy
	TrafficDistribution   *string

	SessionAffinity       corev1.ServiceAffinity
	SessionAffinityConfig *corev1.SessionAffinityConfig

	// PublishNotReadyAddresses is written only when true: false is the API's
	// own default and the field is `omitempty`.
	PublishNotReadyAddresses bool

	IPFamilies     []corev1.IPFamily
	IPFamilyPolicy *corev1.IPFamilyPolicy

	// The fields below are accepted on a LoadBalancer Service only.
	LoadBalancerClass             *string
	LoadBalancerSourceRanges      []string
	LoadBalancerIP                string
	AllocateLoadBalancerNodePorts *bool
	HealthCheckNodePort           int32
}

// apply writes the authored fields onto spec, copying every pointer and slice
// so an edit to one rendered Service cannot reach the next render.
func (f ServiceSpecFields) apply(spec *corev1.ServiceSpec) {
	spec.ExternalName = f.ExternalName
	spec.ExternalTrafficPolicy = f.ExternalTrafficPolicy
	spec.InternalTrafficPolicy = clonePtr(f.InternalTrafficPolicy)
	spec.TrafficDistribution = clonePtr(f.TrafficDistribution)
	spec.SessionAffinity = f.SessionAffinity
	spec.SessionAffinityConfig = f.SessionAffinityConfig.DeepCopy()
	spec.PublishNotReadyAddresses = f.PublishNotReadyAddresses
	spec.IPFamilies = slices.Clone(f.IPFamilies)
	spec.IPFamilyPolicy = clonePtr(f.IPFamilyPolicy)
	spec.LoadBalancerClass = clonePtr(f.LoadBalancerClass)
	spec.LoadBalancerSourceRanges = slices.Clone(f.LoadBalancerSourceRanges)
	spec.LoadBalancerIP = f.LoadBalancerIP
	spec.AllocateLoadBalancerNodePorts = clonePtr(f.AllocateLoadBalancerNodePorts)
	spec.HealthCheckNodePort = f.HealthCheckNodePort
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// serviceSpecPropertyKeys are the keys parseServiceSpec reads, and the nested
// lists the keys it accepts one and two levels into sessionAffinityConfig.
// TestServiceSpecSchemaMatchesParser pins schemaServiceSpec to all three.
var (
	serviceSpecPropertyKeys = []string{
		"externalName",
		"externalTrafficPolicy",
		"internalTrafficPolicy",
		"trafficDistribution",
		"sessionAffinity",
		"sessionAffinityConfig",
		"publishNotReadyAddresses",
		"ipFamilies",
		"ipFamilyPolicy",
		"loadBalancerClass",
		"loadBalancerSourceRanges",
		"loadBalancerIP",
		"allocateLoadBalancerNodePorts",
		"healthCheckNodePort",
	}
	serviceSessionAffinityConfigKeys = []string{"clientIP"}
	serviceClientIPConfigKeys        = []string{"timeoutSeconds"}
)

// serviceRejectedKeys are ServiceSpec fields a service component does not
// accept; each maps to the error saying why. They are refused by name rather
// than left to the unknown-key refusal so the author learns the reason.
var serviceRejectedKeys = map[string]string{
	"externalIPs": "externalIPs: not supported; the field routes traffic for addresses the cluster does not manage to this Service, and the API marks it deprecated. Use a LoadBalancer Service or a routing trait",
	"clusterIPs":  "clusterIPs: not supported; a Service's cluster IPs are allocated by the cluster. Use clusterIP: None for a headless Service, and ipFamilies or ipFamilyPolicy to choose the address families",
}

var (
	serviceExternalTrafficPolicies = []corev1.ServiceExternalTrafficPolicy{
		corev1.ServiceExternalTrafficPolicyCluster,
		corev1.ServiceExternalTrafficPolicyLocal,
	}
	serviceInternalTrafficPolicies = []corev1.ServiceInternalTrafficPolicy{
		corev1.ServiceInternalTrafficPolicyCluster,
		corev1.ServiceInternalTrafficPolicyLocal,
	}
	// serviceTrafficDistributions are the three values validateServiceTrafficDistribution
	// accepts since PreferSameTrafficDistribution was locked on (Kubernetes 1.35).
	serviceTrafficDistributions = []string{
		corev1.ServiceTrafficDistributionPreferClose,
		corev1.ServiceTrafficDistributionPreferSameZone,
		corev1.ServiceTrafficDistributionPreferSameNode,
	}
	serviceSessionAffinities = []corev1.ServiceAffinity{
		corev1.ServiceAffinityNone,
		corev1.ServiceAffinityClientIP,
	}
	serviceIPFamilies = []corev1.IPFamily{corev1.IPv4Protocol, corev1.IPv6Protocol}
	// serviceIPFamilyPolicies is in the API's own order, which is not alphabetical.
	serviceIPFamilyPolicies = []corev1.IPFamilyPolicy{
		corev1.IPFamilyPolicySingleStack,
		corev1.IPFamilyPolicyPreferDualStack,
		corev1.IPFamilyPolicyRequireDualStack,
	}
)

// schemaServiceSpec publishes the keys parseServiceSpec reads
// (serviceSpecPropertyKeys).
func schemaServiceSpec() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"externalName": {
			Type:        oam.PropertyTypeString,
			Description: "DNS name the Service aliases (a CNAME record in cluster DNS); a trailing dot is allowed. Required with type ExternalName and refused with any other type. Accepted with no policy check.",
		},
		"externalTrafficPolicy": {
			Type:        oam.PropertyTypeString,
			Enum:        enumValues(serviceExternalTrafficPolicies),
			Description: "How traffic arriving from outside the cluster is routed: Cluster (any ready endpoint) or Local (endpoints on the receiving node only, keeping the client source IP). Only with type NodePort or LoadBalancer. The API default is Cluster.",
		},
		"internalTrafficPolicy": {
			Type:        oam.PropertyTypeString,
			Enum:        enumValues(serviceInternalTrafficPolicies),
			Description: "How traffic from inside the cluster is routed: Cluster (any ready endpoint) or Local (endpoints on the sending node only). The API default is Cluster.",
		},
		"trafficDistribution": {
			Type:        oam.PropertyTypeString,
			Enum:        enumValues(serviceTrafficDistributions),
			Description: "Preference for which endpoints receive traffic: PreferSameZone, PreferSameNode, or PreferClose (the deprecated name of PreferSameZone). Unset, traffic is spread over all endpoints.",
		},
		"sessionAffinity": {
			Type:        oam.PropertyTypeString,
			Enum:        enumValues(serviceSessionAffinities),
			Description: "ClientIP keeps one client's connections on one pod; None does not. The API default is None.",
		},
		"sessionAffinityConfig": {
			Type:        oam.PropertyTypeObject,
			Description: "Session affinity settings. Only with sessionAffinity ClientIP; without it the API server applies a three-hour timeout.",
			Properties: map[string]oam.PropertySchema{
				"clientIP": {
					Type:        oam.PropertyTypeObject,
					Required:    true,
					Description: "Settings of ClientIP session affinity.",
					Properties: map[string]oam.PropertySchema{
						"timeoutSeconds": {Type: oam.PropertyTypeInteger, Required: true, Description: "Seconds a client's affinity lasts, 1-86400."},
					},
				},
			},
		},
		"publishNotReadyAddresses": {
			Type:        oam.PropertyTypeBoolean,
			Description: "Publish the addresses of pods that are not ready, as a headless Service for peer discovery needs. The API default is false.",
		},
		"ipFamilies": {
			Type:        oam.PropertyTypeArray,
			Description: "IP families of the Service, IPv4 and/or IPv6, at most two and each once; the first is the primary. Refused with type ExternalName. Unset, the cluster chooses.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Enum: enumValues(serviceIPFamilies), Description: "One IP family."},
		},
		"ipFamilyPolicy": {
			Type:        oam.PropertyTypeString,
			Enum:        enumValues(serviceIPFamilyPolicies),
			Description: "SingleStack, PreferDualStack or RequireDualStack. SingleStack is refused with two ipFamilies. Refused with type ExternalName. The API default is SingleStack.",
		},
		"loadBalancerClass": {
			Type:        oam.PropertyTypeString,
			Description: "Load balancer implementation the Service is for, a qualified name such as example.com/internal. Only with type LoadBalancer. Immutable once set.",
		},
		"loadBalancerSourceRanges": {
			Type:        oam.PropertyTypeArray,
			Description: "Client CIDRs the load balancer admits, each in canonical form (e.g. 10.0.0.0/8). Only with type LoadBalancer; whether it is enforced depends on the provider.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "One CIDR."},
		},
		"loadBalancerIP": {
			Type:        oam.PropertyTypeString,
			Description: "Address requested for the load balancer. Only with type LoadBalancer. Deprecated by the API: its meaning varies by provider, which usually offers an annotation instead.",
		},
		"allocateLoadBalancerNodePorts": {
			Type:        oam.PropertyTypeBoolean,
			Description: "Whether node ports are allocated for the load balancer. Only with type LoadBalancer. The API default is true.",
		},
		"healthCheckNodePort": {
			Type:        oam.PropertyTypeInteger,
			Description: "Node port of the load balancer's health check, 1-65535. Only with type LoadBalancer and externalTrafficPolicy Local; unset, the cluster allocates one.",
		},
	}
}

// parseServiceSpec reads the keys schemaServiceSpec publishes into c.Spec. It
// runs after `type` is read, since most of the rules turn on it.
func parseServiceSpec(props map[string]any, c *ServiceConfig) error {
	for _, key := range slices.Sorted(maps.Keys(serviceRejectedKeys)) {
		if _, authored := authoredValue(props, key); authored {
			return errors.New(serviceRejectedKeys[key])
		}
	}

	f := &c.Spec
	isLoadBalancer := c.Type == corev1.ServiceTypeLoadBalancer
	isExternalName := c.Type == corev1.ServiceTypeExternalName
	// The API calls a Service externally accessible when it is a NodePort or a
	// LoadBalancer, or carries externalIPs, which this kind refuses.
	externallyAccessible := c.Type == corev1.ServiceTypeNodePort || isLoadBalancer

	// "" is kept as a value: with type ExternalName it must meet the
	// requirement below rather than read as an omitted key.
	externalName, present, err := parseRawStringField(props, "externalName", "externalName")
	if err != nil {
		return err
	}
	switch {
	case isExternalName:
		// A CNAME, so one trailing dot marks it fully qualified.
		cname := strings.TrimSuffix(externalName, ".")
		if cname == "" {
			return errors.Errorf("externalName: required with type %s", corev1.ServiceTypeExternalName)
		}
		if errs := validation.IsDNS1123Subdomain(cname); len(errs) > 0 {
			return errors.Errorf("externalName: %q is not a valid DNS name: %s", externalName, strings.Join(errs, "; "))
		}
		f.ExternalName = externalName
	case present:
		// This parser's own refusal: the API accepts the field on another
		// type and ignores it, with a warning.
		return errors.Errorf("externalName: may only be set with type %s, got %s", corev1.ServiceTypeExternalName, c.Type)
	}

	if v, present, err := parseServiceEnum(props, "externalTrafficPolicy", serviceExternalTrafficPolicies); err != nil {
		return err
	} else if present {
		if !externallyAccessible {
			return errors.Errorf("externalTrafficPolicy: may only be set with type %s or %s, got %s",
				corev1.ServiceTypeNodePort, corev1.ServiceTypeLoadBalancer, c.Type)
		}
		f.ExternalTrafficPolicy = v
	}

	if v, present, err := parseServiceEnum(props, "internalTrafficPolicy", serviceInternalTrafficPolicies); err != nil {
		return err
	} else if present {
		f.InternalTrafficPolicy = &v
	}

	if v, present, err := parseServiceEnum(props, "trafficDistribution", serviceTrafficDistributions); err != nil {
		return err
	} else if present {
		f.TrafficDistribution = &v
	}

	if v, present, err := parseServiceEnum(props, "sessionAffinity", serviceSessionAffinities); err != nil {
		return err
	} else if present {
		f.SessionAffinity = v
	}
	if f.SessionAffinityConfig, err = parseSessionAffinityConfig(props, f.SessionAffinity); err != nil {
		return err
	}

	if b, err := parseBoolField(props, "publishNotReadyAddresses", "publishNotReadyAddresses"); err != nil {
		return err
	} else if b != nil {
		f.PublishNotReadyAddresses = *b
	}

	if err := parseServiceIPFamilies(props, c.Type, f); err != nil {
		return err
	}

	if class, present, err := parseRawStringField(props, "loadBalancerClass", "loadBalancerClass"); err != nil {
		return err
	} else if present {
		if !isLoadBalancer {
			return loadBalancerOnly("loadBalancerClass", c.Type)
		}
		if errs := validation.IsQualifiedName(class); len(errs) > 0 {
			return errors.Errorf("loadBalancerClass: %q is not a qualified name: %s", class, strings.Join(errs, "; "))
		}
		f.LoadBalancerClass = &class
	}

	if ranges, present, err := parseStringList(props, "loadBalancerSourceRanges", "loadBalancerSourceRanges"); err != nil {
		return err
	} else if present && len(ranges) > 0 {
		if !isLoadBalancer {
			return loadBalancerOnly("loadBalancerSourceRanges", c.Type)
		}
		for i, r := range ranges {
			label := indexedLabel("loadBalancerSourceRanges", i)
			// Narrower than the API on purpose: it tolerates surrounding
			// spaces and, with a warning, some non-canonical forms. Refusing
			// them here keeps the emitted value the one the cluster reads.
			if errs := validation.IsValidCIDR(field.NewPath(label), r); len(errs) > 0 {
				return errors.Errorf("%s: %q is not a valid CIDR: %s", label, r, errs[0].Detail)
			}
		}
		f.LoadBalancerSourceRanges = ranges
	}

	// The API validates nothing about the value, so neither does this parser.
	if ip, present, err := parseStringField(props, "loadBalancerIP", "loadBalancerIP"); err != nil {
		return err
	} else if present {
		// This parser's own refusal: the API ignores the field elsewhere.
		if !isLoadBalancer {
			return loadBalancerOnly("loadBalancerIP", c.Type)
		}
		f.LoadBalancerIP = ip
	}

	if b, err := parseBoolField(props, "allocateLoadBalancerNodePorts", "allocateLoadBalancerNodePorts"); err != nil {
		return err
	} else if b != nil {
		if !isLoadBalancer {
			return loadBalancerOnly("allocateLoadBalancerNodePorts", c.Type)
		}
		f.AllocateLoadBalancerNodePorts = b
	}

	if port, present, err := parsePortField(props, "healthCheckNodePort", "healthCheckNodePort", 1); err != nil {
		return err
	} else if present {
		if !isLoadBalancer || f.ExternalTrafficPolicy != corev1.ServiceExternalTrafficPolicyLocal {
			return errors.Errorf("healthCheckNodePort: may only be set with type %s and externalTrafficPolicy %s",
				corev1.ServiceTypeLoadBalancer, corev1.ServiceExternalTrafficPolicyLocal)
		}
		f.HealthCheckNodePort = port
	}

	return nil
}

func loadBalancerOnly(key string, got corev1.ServiceType) error {
	return errors.Errorf("%s: may only be set with type %s, got %s", key, corev1.ServiceTypeLoadBalancer, got)
}

// parseServiceEnum reads an optional string property that must be one of set.
// An authored "" is refused, not read as absence.
func parseServiceEnum[T ~string](props map[string]any, key string, set []T) (T, bool, error) {
	s, present, err := parseRawStringField(props, key, key)
	if err != nil || !present {
		return "", false, err
	}
	if !containsValue(set, T(s)) {
		return "", false, errors.Errorf("%s: must be one of %s, got %q", key, joinValues(set), s)
	}
	return T(s), true, nil
}

// maxClientIPAffinitySeconds is the API's MaxClientIPServiceAffinitySeconds.
const maxClientIPAffinitySeconds = 86400

// parseSessionAffinityConfig reads sessionAffinityConfig, which the API
// accepts only beside sessionAffinity ClientIP. An authored config must carry
// its one leaf, clientIP.timeoutSeconds: the API server would fill an empty
// one with its default, and an author who wrote the key meant a value.
func parseSessionAffinityConfig(props map[string]any, affinity corev1.ServiceAffinity) (*corev1.SessionAffinityConfig, error) {
	raw, present, err := parseObjectField(props, "sessionAffinityConfig", "sessionAffinityConfig")
	if err != nil || !present {
		return nil, err
	}
	if affinity != corev1.ServiceAffinityClientIP {
		return nil, errors.Errorf("sessionAffinityConfig: requires sessionAffinity %s", corev1.ServiceAffinityClientIP)
	}
	if err := rejectUnknownKeys(raw, serviceSessionAffinityConfigKeys, "sessionAffinityConfig"); err != nil {
		return nil, err
	}
	clientIP, present, err := parseObjectField(raw, "clientIP", "sessionAffinityConfig.clientIP")
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, errors.New("sessionAffinityConfig.clientIP: required")
	}
	if err := rejectUnknownKeys(clientIP, serviceClientIPConfigKeys, "sessionAffinityConfig.clientIP"); err != nil {
		return nil, err
	}
	const label = "sessionAffinityConfig.clientIP.timeoutSeconds"
	timeout, present, err := parseInt32Field(clientIP, "timeoutSeconds", label)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, errors.Errorf("%s: required", label)
	}
	if timeout <= 0 || timeout > maxClientIPAffinitySeconds {
		return nil, errors.Errorf("%s: must be between 1 and %d, got %d", label, maxClientIPAffinitySeconds, timeout)
	}
	return &corev1.SessionAffinityConfig{ClientIP: &corev1.ClientIPConfig{TimeoutSeconds: &timeout}}, nil
}

// parseServiceIPFamilies reads ipFamilies and ipFamilyPolicy. An empty
// ipFamilies list reads as an omitted one.
func parseServiceIPFamilies(props map[string]any, serviceType corev1.ServiceType, f *ServiceSpecFields) error {
	families, present, err := parseStringList(props, "ipFamilies", "ipFamilies")
	if err != nil {
		return err
	}
	if present && len(families) > 0 {
		if serviceType == corev1.ServiceTypeExternalName {
			return errors.Errorf("ipFamilies: may not be set with type %s", corev1.ServiceTypeExternalName)
		}
		if len(families) > len(serviceIPFamilies) {
			return errors.Errorf("ipFamilies: at most %d families, got %d", len(serviceIPFamilies), len(families))
		}
		for i, family := range families {
			label := indexedLabel("ipFamilies", i)
			if !containsValue(serviceIPFamilies, corev1.IPFamily(family)) {
				return errors.Errorf("%s: must be one of %s, got %q", label, joinValues(serviceIPFamilies), family)
			}
			if slices.Contains(f.IPFamilies, corev1.IPFamily(family)) {
				return errors.Errorf("%s: duplicate family %q", label, family)
			}
			f.IPFamilies = append(f.IPFamilies, corev1.IPFamily(family))
		}
	}

	policy, present, err := parseServiceEnum(props, "ipFamilyPolicy", serviceIPFamilyPolicies)
	if err != nil || !present {
		return err
	}
	if serviceType == corev1.ServiceTypeExternalName {
		return errors.Errorf("ipFamilyPolicy: may not be set with type %s", corev1.ServiceTypeExternalName)
	}
	// Not in validateService: the API server's allocator refuses it
	// (pkg/registry/core/service/storage/alloc.go).
	if policy == corev1.IPFamilyPolicySingleStack && len(f.IPFamilies) > 1 {
		return errors.Errorf("ipFamilyPolicy: must be %s or %s when two ipFamilies are set, got %s",
			corev1.IPFamilyPolicyPreferDualStack, corev1.IPFamilyPolicyRequireDualStack, policy)
	}
	f.IPFamilyPolicy = &policy
	return nil
}
