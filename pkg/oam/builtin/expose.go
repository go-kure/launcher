package builtin

// ExposeRendering defines the platform values for the expose capability.
// All fields are valid rendering keys; unknown fields are rejected at
// ClusterProfile evaluation time via DecodeStrict.
type ExposeRendering struct {
	// ControllerType selects the ingress implementation. Required.
	// Valid values: "ingress", "gateway".
	ControllerType string `yaml:"controllerType" json:"controllerType"`

	// IngressClassName is the Kubernetes IngressClass name.
	// Required when ControllerType is "ingress".
	IngressClassName string `yaml:"ingressClassName,omitempty" json:"ingressClassName,omitempty"`

	// GatewayName is the name of the Gateway API Gateway resource.
	// Required when ControllerType is "gateway".
	GatewayName string `yaml:"gatewayName,omitempty" json:"gatewayName,omitempty"`

	// GatewayNamespace is the namespace of the Gateway resource.
	// Optional when ControllerType is "gateway"; defaults to "gateway-system".
	GatewayNamespace string `yaml:"gatewayNamespace,omitempty" json:"gatewayNamespace,omitempty"`

	// CertManagerClusterIssuer, when set, enables platform-managed TLS on the
	// ingress path: expose emits the cert-manager.io/cluster-issuer annotation and
	// a synthesized spec.tls[] entry derived from the rule hosts. Empty disables
	// managed TLS (plain HTTP). Ingress-only.
	CertManagerClusterIssuer string `yaml:"certManagerClusterIssuer,omitempty" json:"certManagerClusterIssuer,omitempty"`

	// AllowedHostnameWildcard, when set (e.g. "*.apps.example.com"), constrains the
	// user-supplied hostnames on both the ingress and gateway paths. Empty skips
	// hostname validation.
	AllowedHostnameWildcard string `yaml:"allowedHostnameWildcard,omitempty" json:"allowedHostnameWildcard,omitempty"`

	// SSLRedirect is the platform default for the nginx-ingress
	// nginx.ingress.kubernetes.io/ssl-redirect annotation. Ingress-only. A
	// component may override it via the inline sslRedirect property.
	SSLRedirect *bool `yaml:"sslRedirect,omitempty" json:"sslRedirect,omitempty"`

	// ForceSSLRedirect is the platform default for the nginx-ingress
	// nginx.ingress.kubernetes.io/force-ssl-redirect annotation. Ingress-only. A
	// component may override it via the inline forceSslRedirect property.
	ForceSSLRedirect *bool `yaml:"forceSslRedirect,omitempty" json:"forceSslRedirect,omitempty"`
}
