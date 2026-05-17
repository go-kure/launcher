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
}
