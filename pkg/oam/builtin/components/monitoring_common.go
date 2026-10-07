package components

import (
	"maps"

	"github.com/go-kure/launcher/pkg/oam"
)

// This file holds what the kind components of the Prometheus operator's
// monitoring.coreos.com/v1 API share (go-kure/launcher#790): servicemonitor,
// podmonitor, prometheus-probe and prometheusrule. Each is a policyFreeKind:
// the object runs no pod, holds no image, requests no storage and has no
// replica count.
//
// The API's types publish no field descriptions and the module ships no CRD,
// so what the types cannot be asked is read from their source, in tests:
// TestMonitoringKinds_DefaultedZeros holds the claim that a CRD default, read
// from its marker, turns an authored 0, false or "" into another value only on
// the fields of a kind's defaultedZeros (monitoringDefaultedZeros), where the
// kind refuses one, and TestMonitoringKinds_RequiredMatchMarkers
// holds each kind's required list to the fields the source marks required.
//
// One pair of required fields is of a Kubernetes type, which carries no
// +required marker: the key and the operator of a match expression, in the
// label selector of the three scrape kinds (labelSelectorRequired). No CRD is
// linked to read them from, so the same test derives them from the source of
// metav1.LabelSelectorRequirement by the schema generators' rule: a field with
// no optional marker whose json tag keeps it when empty is required.
//
// A host these objects name is one Prometheus reaches, not an artifact source:
// a prober, a proxy, an OAuth2 token endpoint, a static probe target. None is
// held to the environment policy's allowed registries.

// oauth2Required is the required list of one OAuth2 block under the path at
// ("endpoints[].oauth2", or "oauth2" on a Probe): the three fields the API
// requires of it, each of which the Go type would write empty.
func oauth2Required(at string) map[string]string {
	return map[string]string{
		at + ".clientId":     "the Secret or ConfigMap key that holds the OAuth2 client ID",
		at + ".clientSecret": "the Secret key that holds the OAuth2 client secret",
		at + ".tokenUrl":     "the URL the token is fetched from",
	}
}

// monitoringDefaultedZeros is a monitoring kind's defaulted-zero list for
// refuseUncarriedSpecValues: the fields of its spec type that the encoding
// omits when empty and to which the CRD gives another default, each mapped to
// that default as its JSON literal. The API's defaults are the action of a
// relabeling rule at each of the given lists of rules, and any of extra.
// TestMonitoringKinds_DefaultedZeros holds each kind's list to the default
// markers of the linked module's source.
func monitoringDefaultedZeros(relabelings []string, extra map[string]string) defaultedZeroFields {
	fields := maps.Clone(extra)
	if fields == nil {
		fields = map[string]string{}
	}
	for _, at := range relabelings {
		fields[at+"[].action"] = `"replace"`
	}
	return defaultedZeroFields{api: "Prometheus operator", defaulter: "API server", fields: fields}
}

// requiredFields merges the required lists of one kind.
func requiredFields(lists ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, list := range lists {
		maps.Copy(out, list)
	}
	return out
}

// monitoringScrapeSchema returns the properties the three scrape kinds share
// under one name and one meaning: the per-scrape limits, the scrape protocols,
// the native histogram settings and the scrape class. kind names the object in
// each description ("ServiceMonitor").
func monitoringScrapeSchema(kind string) map[string]oam.PropertySchema {
	spec := kind + " spec."
	return map[string]oam.PropertySchema{
		"sampleLimit": {
			Type:        oam.PropertyTypeInteger,
			Description: spec + "sampleLimit: the per-scrape limit on the number of scraped samples that are accepted. At least 0.",
		},
		"targetLimit": {
			Type:        oam.PropertyTypeInteger,
			Description: spec + "targetLimit: the limit on the number of scraped targets that are accepted. At least 0.",
		},
		"scrapeProtocols": {
			Type:        oam.PropertyTypeArray,
			Description: spec + "scrapeProtocols: the protocols to negotiate during a scrape, most preferred first. Unset, Prometheus uses its default. Requires Prometheus v2.49.0 or later.",
			Items: &oam.PropertySchema{
				Type:        oam.PropertyTypeString,
				Description: "One protocol: PrometheusProto, OpenMetricsText0.0.1, OpenMetricsText1.0.0, PrometheusText0.0.4 or PrometheusText1.0.0.",
			},
		},
		"fallbackScrapeProtocol": {
			Type:        oam.PropertyTypeString,
			Description: spec + "fallbackScrapeProtocol: the protocol to use when a scrape returns a blank, unparseable or otherwise invalid Content-Type; one of the scrapeProtocols values. Requires Prometheus v3.0.0 or later.",
		},
		"labelLimit": {
			Type:        oam.PropertyTypeInteger,
			Description: spec + "labelLimit: the per-scrape limit on the number of labels accepted for a sample. At least 0. Requires Prometheus v2.27.0 or later.",
		},
		"labelNameLengthLimit": {
			Type:        oam.PropertyTypeInteger,
			Description: spec + "labelNameLengthLimit: the per-scrape limit on the length of a label name accepted for a sample. At least 0. Requires Prometheus v2.27.0 or later.",
		},
		"labelValueLengthLimit": {
			Type:        oam.PropertyTypeInteger,
			Description: spec + "labelValueLengthLimit: the per-scrape limit on the length of a label value accepted for a sample. At least 0. Requires Prometheus v2.27.0 or later.",
		},
		"scrapeNativeHistograms": {
			Type:        oam.PropertyTypeBoolean,
			Description: spec + "scrapeNativeHistograms: whether native histograms are scraped. Requires Prometheus v3.8.0 or later.",
		},
		"scrapeClassicHistograms": {
			Type:        oam.PropertyTypeBoolean,
			Description: spec + "scrapeClassicHistograms: whether a classic histogram that is also exposed as a native histogram is scraped (always_scrape_classic_histograms in the Prometheus configuration). Requires Prometheus v2.45.0 or later.",
		},
		"nativeHistogramBucketLimit": {
			Type:        oam.PropertyTypeInteger,
			Description: spec + "nativeHistogramBucketLimit: a native histogram with more buckets than this has its buckets merged to stay within the limit. At least 0. Requires Prometheus v2.45.0 or later.",
		},
		"nativeHistogramMinBucketFactor": {
			Types:       []oam.PropertyType{oam.PropertyTypeNumber, oam.PropertyTypeString},
			Description: spec + "nativeHistogramMinBucketFactor: where the growth factor from one bucket to the next is smaller than this, buckets are merged to raise it. A Kubernetes quantity, as a number or a string (1.1, \"1.1\"). Requires Prometheus v2.50.0 or later.",
		},
		"convertClassicHistogramsToNHCB": {
			Type:        oam.PropertyTypeBoolean,
			Description: spec + "convertClassicHistogramsToNHCB: whether every scraped classic histogram is converted into a native histogram with custom buckets. Requires Prometheus v3.0.0 or later.",
		},
		"keepDroppedTargets": {
			Type:        oam.PropertyTypeInteger,
			Description: spec + "keepDroppedTargets: the per-scrape limit on the number of targets dropped by relabeling that are kept in memory; 0 means no limit. Requires Prometheus v2.47.0 or later.",
		},
		"scrapeClass": {
			Type:        oam.PropertyTypeString,
			Description: spec + "scrapeClass: the name of the scrape class to apply, one the selecting Prometheus defines. Not empty.",
		},
	}
}

// monitoringSchema merges a kind's own properties over the shared ones.
func monitoringSchema(shared, own map[string]oam.PropertySchema) map[string]oam.PropertySchema {
	out := maps.Clone(shared)
	maps.Copy(out, own)
	return out
}

// relabelItems is the item schema of a list of relabeling rules.
func relabelItems() *oam.PropertySchema {
	return &oam.PropertySchema{
		Type: oam.PropertyTypeObject, AdditionalProperties: true,
		Description: "One relabeling rule: sourceLabels, separator, targetLabel, regex, modulus, replacement, action. An unset action is read as replace; an empty one is refused, since the API server would replace it. Decoded strictly into the Prometheus operator's API type: see RelabelConfig in its API reference.",
	}
}
