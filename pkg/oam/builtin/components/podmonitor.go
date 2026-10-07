package components

import (
	"github.com/go-kure/kure/pkg/kubernetes/prometheus"
	"github.com/go-kure/kure/pkg/stack"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// PodMonitorHandler handles OAM podmonitor components: the kind-named
// projection of a monitoring.coreos.com/v1 PodMonitor (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// monitoringv1.PodMonitorSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the PodMonitor, named after the component unless
// `objectName` names it, in the build namespace, and nothing else. `selector`
// is the author's: launcher points it at no component.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type PodMonitorHandler struct{}

// CanHandle returns true for the podmonitor component type.
func (h *PodMonitorHandler) CanHandle(componentType string) bool {
	return componentType == "podmonitor"
}

// PropertySchema declares every top-level monitoringv1.PodMonitorSpec field by
// its json name. Structured fields are open objects whose content is checked
// by the strict decode, not by this schema.
func (h *PodMonitorHandler) PropertySchema() map[string]oam.PropertySchema {
	return monitoringSchema(monitoringScrapeSchema("PodMonitor"), map[string]oam.PropertySchema{
		"jobLabel": {
			Type:        oam.PropertyTypeString,
			Description: "PodMonitor spec.jobLabel: the label of the Pod whose value becomes the `job` label of the metrics. Empty, the `job` label is the PodMonitor's namespace and name (<namespace>/<name>).",
		},
		"podTargetLabels": {
			Type:        oam.PropertyTypeArray,
			Description: "PodMonitor spec.podTargetLabels: the labels copied from the Pod onto the ingested metrics.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "One label name of the Pod."},
		},
		"podMetricsEndpoints": {
			Type:        oam.PropertyTypeArray,
			Description: "PodMonitor spec.podMetricsEndpoints: how the selected pods are scraped, one entry per port. Unset, the object carries the field as null and nothing is scraped.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One endpoint: port or portNumber, path, scheme, params, interval, scrapeTimeout, the relabelings, and the HTTP client settings (authorization, basicAuth, oauth2, bearerTokenSecret, tlsConfig, the proxy fields). An oauth2 block requires clientId, clientSecret and tokenUrl. Decoded strictly into the Prometheus operator's API type: see PodMetricsEndpoint in its API reference.",
			},
		},
		"selector": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true, Required: true,
			Description: "Required. PodMonitor spec.selector: the label query over the Pods to scrape (matchLabels, matchExpressions). {} selects every Pod of the selected namespaces. Decoded strictly into the Kubernetes API type: see LabelSelector in the Kubernetes API reference.",
		},
		"selectorMechanism": {
			Type:        oam.PropertyTypeString,
			Description: "PodMonitor spec.selectorMechanism: how the targets are selected: RelabelConfig (relabel configurations filter the discovered targets) or RoleSelector. Unset, the operator uses relabel configurations. Requires Prometheus v2.17.0 or later.",
		},
		"namespaceSelector": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "PodMonitor spec.namespaceSelector: the namespaces the Pods are discovered in: any (every namespace) or matchNames. Unset, the PodMonitor's own namespace; the object then carries an empty one.",
		},
		"attachMetadata": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "PodMonitor spec.attachMetadata: the metadata added to the discovered targets: node (the node's metadata, which needs the Prometheus service account's permission to read Nodes). Requires Prometheus v2.35.0 or later.",
		},
		"bodySizeLimit": {
			Type:        oam.PropertyTypeString,
			Description: "PodMonitor spec.bodySizeLimit: the limit on the size of an uncompressed response body Prometheus accepts, in powers of two (\"512MB\"). Requires Prometheus v2.28.0 or later.",
		},
	})
}

// podMonitorKind is the podmonitor kind: see policyFreeKind. The API requires
// `selector`, and of an endpoint's `oauth2` its three fields; the type would
// write each one empty, and an empty selector selects every Pod.
// `podMetricsEndpoints` is optional to the API, which the type writes as null
// when it is not authored. Of a match expression of the selector the API
// requires the key and the operator (labelSelectorRequired;
// monitoring_common.go states the ground). The API's value rules, and the
// operator's own checks of an object it has admitted, are left to them.
var podMonitorKind = &policyFreeKind[monitoringv1.PodMonitorSpec]{
	upstream: "monitoring.coreos.com/v1 PodMonitorSpec",
	required: requiredFields(map[string]string{
		"selector": "the label query over the Pods to scrape; no default selector is filled, use {} to select every Pod of the selected namespaces",
	}, oauth2Required("podMetricsEndpoints[].oauth2"), labelSelectorRequired("selector")),
	defaultedZeros: monitoringDefaultedZeros([]string{"podMetricsEndpoints[].metricRelabelings", "podMetricsEndpoints[].relabelings"}, nil),
	build: func(name, namespace string, spec *monitoringv1.PodMonitorSpec) client.Object {
		monitor := prometheus.CreatePodMonitor(name, namespace)
		spec.DeepCopyInto(&monitor.Spec)
		return monitor
	},
}

// ToApplicationConfig decodes an OAM podmonitor component into its config. The
// object takes the namespace of the application it is generated in.
func (h *PodMonitorHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return podMonitorKind.config(component)
}
