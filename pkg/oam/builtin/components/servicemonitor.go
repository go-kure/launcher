package components

import (
	"github.com/go-kure/kure/pkg/kubernetes/prometheus"
	"github.com/go-kure/kure/pkg/stack"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// ServiceMonitorHandler handles OAM servicemonitor components: the kind-named
// projection of a monitoring.coreos.com/v1 ServiceMonitor
// (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// monitoringv1.ServiceMonitorSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the ServiceMonitor, named after the component
// unless `objectName` names it, in the build namespace, and nothing else.
// `selector` is the author's: launcher points it at no component.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type ServiceMonitorHandler struct{}

// CanHandle returns true for the servicemonitor component type.
func (h *ServiceMonitorHandler) CanHandle(componentType string) bool {
	return componentType == "servicemonitor"
}

// PropertySchema declares every top-level monitoringv1.ServiceMonitorSpec
// field by its json name. Structured fields are open objects whose content is
// checked by the strict decode, not by this schema.
func (h *ServiceMonitorHandler) PropertySchema() map[string]oam.PropertySchema {
	return monitoringSchema(monitoringScrapeSchema("ServiceMonitor"), map[string]oam.PropertySchema{
		"jobLabel": {
			Type:        oam.PropertyTypeString,
			Description: "ServiceMonitor spec.jobLabel: the label of the Service whose value becomes the `job` label of the metrics. Empty, or absent on the Service, the `job` label is the Service's name.",
		},
		"targetLabels": {
			Type:        oam.PropertyTypeArray,
			Description: "ServiceMonitor spec.targetLabels: the labels copied from the Service onto the ingested metrics.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "One label name of the Service."},
		},
		"podTargetLabels": {
			Type:        oam.PropertyTypeArray,
			Description: "ServiceMonitor spec.podTargetLabels: the labels copied from the Pod behind the Service onto the ingested metrics.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "One label name of the Pod."},
		},
		"endpoints": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. ServiceMonitor spec.endpoints: how the endpoints of the selected Services are scraped, one entry per port.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One endpoint: port or targetPort, path, scheme, params, interval, scrapeTimeout, the relabelings, and the HTTP client settings (authorization, basicAuth, oauth2, bearerTokenSecret, tlsConfig, the proxy fields). An oauth2 block requires clientId, clientSecret and tokenUrl. Decoded strictly into the Prometheus operator's API type: see Endpoint in its API reference.",
			},
		},
		"selector": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true, Required: true,
			Description: "Required. ServiceMonitor spec.selector: the label query over the Services whose endpoints are scraped (matchLabels, matchExpressions). {} selects every Service of the selected namespaces. Decoded strictly into the Kubernetes API type: see LabelSelector in the Kubernetes API reference.",
		},
		"selectorMechanism": {
			Type:        oam.PropertyTypeString,
			Description: "ServiceMonitor spec.selectorMechanism: how the targets are selected: RelabelConfig (relabel configurations filter the discovered targets) or RoleSelector. Unset, the operator uses relabel configurations. Requires Prometheus v2.17.0 or later.",
		},
		"namespaceSelector": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "ServiceMonitor spec.namespaceSelector: the namespaces the Services are discovered in: any (every namespace) or matchNames. Unset, the ServiceMonitor's own namespace; the object then carries an empty one.",
		},
		"attachMetadata": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "ServiceMonitor spec.attachMetadata: the metadata added to the discovered targets: node (the node's metadata, which needs the Prometheus service account's permission to read Nodes). Requires Prometheus v2.37.0 or later.",
		},
		"bodySizeLimit": {
			Type:        oam.PropertyTypeString,
			Description: "ServiceMonitor spec.bodySizeLimit: the limit on the size of an uncompressed response body Prometheus accepts, in powers of two (\"512MB\"). Requires Prometheus v2.28.0 or later.",
		},
		"serviceDiscoveryRole": {
			Type:        oam.PropertyTypeString,
			Description: "ServiceMonitor spec.serviceDiscoveryRole: the service discovery role the targets are discovered with: Endpoints or EndpointSlice. Unset, the one the selecting Prometheus defines.",
		},
	})
}

// serviceMonitorKind is the servicemonitor kind: see policyFreeKind. The API
// requires `endpoints` and `selector`, and of an endpoint's `oauth2` its three
// fields; the type would write each one empty, and an empty selector selects
// every Service. Of a match expression of the selector it requires the key and
// the operator (labelSelectorRequired; monitoring_common.go states the ground).
// The API's value rules, and the operator's own checks of an object it has
// admitted, are left to them.
var serviceMonitorKind = &policyFreeKind[monitoringv1.ServiceMonitorSpec]{
	upstream: "monitoring.coreos.com/v1 ServiceMonitorSpec",
	required: requiredFields(map[string]string{
		"endpoints": "how the endpoints of the selected Services are scraped; [] is an authored empty list",
		"selector":  "the label query over the Services to scrape; no default selector is filled, use {} to select every Service of the selected namespaces",
	}, oauth2Required("endpoints[].oauth2"), labelSelectorRequired("selector")),
	defaultedZeros: monitoringDefaultedZeros([]string{"endpoints[].metricRelabelings", "endpoints[].relabelings"}, nil),
	build: func(name, namespace string, spec *monitoringv1.ServiceMonitorSpec) client.Object {
		monitor := prometheus.CreateServiceMonitor(name, namespace)
		spec.DeepCopyInto(&monitor.Spec)
		return monitor
	},
}

// ToApplicationConfig decodes an OAM servicemonitor component into its config.
// The object takes the namespace of the application it is generated in.
func (h *ServiceMonitorHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return serviceMonitorKind.config(component)
}
