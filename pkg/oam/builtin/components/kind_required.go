package components

import (
	"fmt"
	"maps"
	"strings"
)

// This file holds the kind components whose required list is generated
// (go-kure/launcher#883): the fields the kind's API requires and its Go type
// writes whether or not they were authored, which the kind refuses unauthored
// (refuseUnauthoredRequired). The paths are not kept by hand.
// TestKindComponents_RequiredIsGenerated derives them from the API the kind's
// object belongs to (its CRD where the linked module ships one, the markers of
// its source where it does not) and holds zz_generated_kind_required.go to
// that derivation in both directions; after a bump of a module that adds,
// drops or moves such a field, it fails until the file is regenerated with
// UPDATE_KIND_REQUIRED=1.
//
// The derivation is owner-agnostic: a field of a Kubernetes type the spec
// embeds (the affinity of a pod template, a security context) is listed as
// one of the kind's own API is. It leaves out, each by a named rule, a field
// whose default the API server fills into the null the type writes, a value
// the type writes that the API takes as authored, and a field under a path
// the kind refuses whatever is authored there.

// The sentences a generated path says of itself, by its class, with the name
// of the API: a field the API requires, and the name of what a reference
// refers to (a path whose last field is `name`).
const (
	generatedRequiresField = "the %s API requires this field"
	generatedRequiresName  = "the %s API requires the name of what this refers to"
)

// kindRequiredAPI names, by kind component, the API of each kind whose
// required list is generated, as its sentences name it. A kind is generated
// where it is listed here.
var kindRequiredAPI = map[string]string{
	"certificate":           "cert-manager",
	"clusterissuer":         "cert-manager",
	"issuer":                "cert-manager",
	"cnpg-cluster":          "CloudNativePG",
	"cnpg-database":         "CloudNativePG",
	"cnpg-objectstore":      "Barman Cloud plugin",
	"cnpg-pooler":           "CloudNativePG",
	"clusterexternalsecret": "external-secrets",
	"clustersecretstore":    "external-secrets",
	"externalsecret":        "external-secrets",
	"secretstore":           "external-secrets",
	"alertmanager":          "Prometheus operator",
	"podmonitor":            "Prometheus operator",
	"prometheus":            "Prometheus operator",
	"prometheus-probe":      "Prometheus operator",
	"prometheusrule":        "Prometheus operator",
	"servicemonitor":        "Prometheus operator",
	"thanosruler":           "Prometheus operator",
}

// requiredText holds, by kind component, what a generated path says of itself
// where it says more than its class: the sentences the kind's hand-kept list
// said before the list was generated, kept byte for byte. A path with no
// entry here says its class's sentence.
var requiredText = map[string]map[string]string{
	"certificate":           certificateRequiredText,
	"clusterissuer":         issuerRequiredText,
	"issuer":                issuerRequiredText,
	"cnpg-cluster":          cnpgClusterRequiredText,
	"cnpg-database":         cnpgDatabaseRequiredText,
	"cnpg-objectstore":      cnpgObjectStoreRequiredText,
	"cnpg-pooler":           cnpgPoolerRequiredText,
	"clusterexternalsecret": clusterExternalSecretRequiredText,
	"clustersecretstore":    secretStoreRequiredText,
	"externalsecret":        externalSecretRequiredText,
	"secretstore":           secretStoreRequiredText,
	"alertmanager":          alertmanagerRequiredText,
	"podmonitor":            podMonitorRequiredText,
	"prometheus":            prometheusRequiredText,
	"prometheus-probe":      prometheusProbeRequiredText,
	"prometheusrule":        prometheusRuleRequiredText,
	"servicemonitor":        serviceMonitorRequiredText,
	"thanosruler":           thanosRulerRequiredText,
}

// kindRequiredParents lists, by kind component, the parents a kind refuses
// left out though the API does not require them: a struct the Go type writes
// whether or not it was authored, under which the API requires a field, and
// whose zero value the API server accepts and the operator refuses. Each says
// what the parent holds and why it is asked for. Filling the parent would
// write a value the author did not choose, so the author must author it.
// TestKindComponents_RequiredIsGenerated holds each to the API server's answer
// for its omission (kindRequiredForced).
var kindRequiredParents = map[string]map[string]string{
	"alertmanager":     basicAuthRequired("alertmanagerConfiguration.global.httpConfig.basicAuth"),
	"podmonitor":       basicAuthRequired("podMetricsEndpoints[].basicAuth"),
	"prometheus":       basicAuthRequired("alerting.alertmanagers[].basicAuth", "apiserverConfig.basicAuth", "remoteRead[].basicAuth", "remoteWrite[].basicAuth"),
	"prometheus-probe": basicAuthRequired("basicAuth"),
	"servicemonitor":   basicAuthRequired("endpoints[].basicAuth"),
	"thanosruler":      basicAuthRequired("remoteWrite[].basicAuth"),
}

// basicAuthRequired lists the username and the password of each basic auth
// block at the given paths. The Prometheus operator's BasicAuth writes both
// as a Secret key reference whether or not they were authored, and the API
// server accepts the block with either empty; the operator reads both keys
// and refuses the block where it cannot (pkg/assets in its source).
func basicAuthRequired(paths ...string) map[string]string {
	out := map[string]string{}
	for _, path := range paths {
		out[path+".username"] = "the Secret key that holds the username; the Prometheus operator reads both keys of a basic auth and refuses one without either"
		out[path+".password"] = "the Secret key that holds the password; the Prometheus operator reads both keys of a basic auth and refuses one without either"
	}
	return out
}

// generatedKindRequired is the required list of the generated paths of a kind
// component (kindRequiredPaths), each saying what requiredText holds for it or
// what its class says, and of the parents kindRequiredParents lists for it.
func generatedKindRequired(component string) map[string]string {
	api := kindRequiredAPI[component]
	paths := kindRequiredPaths[component]
	out := make(map[string]string, len(paths)+len(kindRequiredParents[component]))
	maps.Copy(out, kindRequiredParents[component])
	for _, path := range paths {
		says, ok := requiredText[component][path]
		switch {
		case ok:
		case path == "name" || strings.HasSuffix(path, ".name"):
			says = fmt.Sprintf(generatedRequiresName, api)
		default:
			says = fmt.Sprintf(generatedRequiresField, api)
		}
		out[path] = says
	}
	return out
}
