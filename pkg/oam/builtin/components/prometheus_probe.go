package components

import (
	"github.com/go-kure/kure/pkg/kubernetes/prometheus"
	"github.com/go-kure/kure/pkg/stack"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// PrometheusProbeHandler handles OAM prometheus-probe components: the
// projection of a monitoring.coreos.com/v1 Probe (go-kure/launcher#790). The
// type name carries the prefix because a probe, in this package, is a
// container's.
//
// Its properties are exactly the top-level fields of monitoringv1.ProbeSpec,
// under their json names, decoded strictly (decodeKindSpec). It emits the
// Probe, named after the component unless `objectName` names it, in the build
// namespace, and nothing else. The prober and the targets are the author's:
// launcher points neither at a component, and holds neither host to the
// environment policy's allowed registries. TestCoreKindSchemas_CoverSpec keeps
// the published key set equal to the upstream json tags.
type PrometheusProbeHandler struct{}

// CanHandle returns true for the prometheus-probe component type.
func (h *PrometheusProbeHandler) CanHandle(componentType string) bool {
	return componentType == "prometheus-probe"
}

// PropertySchema declares every top-level monitoringv1.ProbeSpec field by its
// json name, the HTTP client settings the type embeds included. Structured
// fields are open objects whose content is checked by the strict decode, not
// by this schema.
func (h *PrometheusProbeHandler) PropertySchema() map[string]oam.PropertySchema {
	return monitoringSchema(monitoringScrapeSchema("Probe"), map[string]oam.PropertySchema{
		"jobName": {
			Type:        oam.PropertyTypeString,
			Description: "Probe spec.jobName: the job name assigned to the scraped metrics by default.",
		},
		"prober": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true, Required: true,
			Description: "Required. Probe spec.prober: the prober that probes the targets: url (required: its address as address:port, without a scheme), scheme, path and the proxy fields (proxyUrl, noProxy, proxyFromEnvironment, proxyConnectHeader). An unset or empty path is read as /probe. Decoded strictly into the Prometheus operator's API type: see ProberSpec in its API reference.",
		},
		"module": {
			Type:        oam.PropertyTypeString,
			Description: "Probe spec.module: the prober's module that says how a target is probed (http_2xx, for the blackbox exporter). It takes precedence over a `module` entry of params.",
		},
		"targets": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "Probe spec.targets: the targets to probe: staticConfig (static, the list of hosts; labels; relabelingConfigs) or ingress (selector, namespaceSelector, relabelingConfigs); staticConfig takes precedence. The operator rejects a Probe with neither. Unset, the object carries an empty one. Decoded strictly into the Prometheus operator's API type: see ProbeTargets in its API reference.",
		},
		"interval": {
			Type:        oam.PropertyTypeString,
			Description: "Probe spec.interval: the interval at which the targets are probed, a Prometheus duration (\"30s\"). Unset, Prometheus' global scrape interval.",
		},
		"scrapeTimeout": {
			Type:        oam.PropertyTypeString,
			Description: "Probe spec.scrapeTimeout: the timeout of a scrape of the prober, a Prometheus duration. Unset, Prometheus' global scrape timeout. The operator rejects one greater than the interval.",
		},
		"metricRelabelings": {
			Type:        oam.PropertyTypeArray,
			Description: "Probe spec.metricRelabelings: the relabeling rules applied to the samples before ingestion.",
			Items:       relabelItems(),
		},
		"params": {
			Type:        oam.PropertyTypeArray,
			Description: "Probe spec.params: the HTTP query parameters of the scrape, each named once; at least one entry when set. The value is written to the object as authored.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One parameter: name (required by the API) and values.",
			},
		},
		"authorization": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "Probe spec.authorization: the Authorization header credentials of the scrape: type (Bearer when unset) and credentials, a key of a Secret in the Probe's namespace. Not together with basicAuth, bearerTokenSecret or oauth2.",
		},
		"basicAuth": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "Probe spec.basicAuth: the Basic Authentication credentials of the scrape: username and password, each a key of a Secret in the Probe's namespace. Not together with authorization, bearerTokenSecret or oauth2.",
		},
		"oauth2": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "Probe spec.oauth2: the OAuth2 client settings of the scrape: clientId, clientSecret and tokenUrl (all three required), scopes, endpointParams, tlsConfig and the proxy fields. Not together with authorization, basicAuth or bearerTokenSecret. Decoded strictly into the Prometheus operator's API type: see OAuth2 in its API reference.",
		},
		"bearerTokenSecret": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "Probe spec.bearerTokenSecret: a key of a Secret in the Probe's namespace that holds the bearer token of the scrape (name, key). Deprecated upstream in favour of authorization, and not together with it, basicAuth or oauth2.",
		},
		"followRedirects": {
			Type:        oam.PropertyTypeBoolean,
			Description: "Probe spec.followRedirects: whether the scrape follows HTTP 3xx redirects.",
		},
		"enableHttp2": {
			Type:        oam.PropertyTypeBoolean,
			Description: "Probe spec.enableHttp2: false disables HTTP/2 for the scrape.",
		},
		"tlsConfig": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "Probe spec.tlsConfig: the TLS settings of the scrape: ca and cert (each a Secret or ConfigMap key), keySecret, serverName, insecureSkipVerify, minVersion, maxVersion. Decoded strictly into the Prometheus operator's API type: see SafeTLSConfig in its API reference.",
		},
	})
}

// prometheusProbeKind is the prometheus-probe kind: see policyFreeKind. The
// API requires the three fields of an `oauth2`, each of which the type would
// write empty, and of `prober` its `url`. `prober` itself is optional to the
// API, but the type always writes it, so a Probe launcher emits holds one and
// the API server refuses it without a url: a Probe without a prober is not one
// launcher can emit. It also requires the `name` of a `params` entry, which the
// type leaves out when it is empty, so that the object would show the
// omission. The API's value rules, and the operator's own checks of an object
// it has admitted (that it has targets, that its timeout is no longer than its
// interval, that it authenticates one way), are left to them.
var prometheusProbeKind = &policyFreeKind[monitoringv1.ProbeSpec]{
	upstream: "monitoring.coreos.com/v1 ProbeSpec",
	required: oauth2Required("oauth2"),
	validate: func(spec *monitoringv1.ProbeSpec) error {
		if spec.ProberSpec.URL == "" {
			return errors.New("prober.url: required (the address of the prober, as address:port; the object always holds a prober, and the API server refuses one without a url)")
		}
		for i, param := range spec.Params {
			if param.Name == "" {
				return errors.Errorf("params[%d].name: required (the name of the query parameter)", i)
			}
		}
		return nil
	},
	build: func(name, namespace string, spec *monitoringv1.ProbeSpec) client.Object {
		probe := prometheus.CreateProbe(name, namespace)
		spec.DeepCopyInto(&probe.Spec)
		return probe
	},
}

// ToApplicationConfig decodes an OAM prometheus-probe component into its
// config. The object takes the namespace of the application it is generated
// in.
func (h *PrometheusProbeHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return prometheusProbeKind.config(component)
}
