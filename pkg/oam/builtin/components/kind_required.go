package components

import (
	"fmt"
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
	"clusterexternalsecret": "external-secrets",
	"clustersecretstore":    "external-secrets",
	"externalsecret":        "external-secrets",
	"secretstore":           "external-secrets",
}

// requiredText holds, by kind component, what a generated path says of itself
// where it says more than its class: the sentences the kind's hand-kept list
// said before the list was generated, kept byte for byte. A path with no
// entry here says its class's sentence.
var requiredText = map[string]map[string]string{
	"certificate":           certificateRequiredText,
	"clusterissuer":         issuerRequiredText,
	"issuer":                issuerRequiredText,
	"clusterexternalsecret": clusterExternalSecretRequiredText,
	"clustersecretstore":    secretStoreRequiredText,
	"externalsecret":        externalSecretRequiredText,
	"secretstore":           secretStoreRequiredText,
}

// generatedKindRequired is the required list of the generated paths of a kind
// component (kindRequiredPaths), each saying what requiredText holds for it or
// what its class says.
func generatedKindRequired(component string) map[string]string {
	api := kindRequiredAPI[component]
	paths := kindRequiredPaths[component]
	out := make(map[string]string, len(paths))
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
