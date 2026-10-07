package components

import (
	"strings"

	fluxoperatorv1 "github.com/controlplaneio-fluxcd/flux-operator/api/v1"
	"github.com/go-kure/kure/pkg/kubernetes/fluxcd"
	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/internal/fluxduration"
)

// resourceSetInputProviderType is the component type of the
// ResourceSetInputProvider kind.
const resourceSetInputProviderType = "resourcesetinputprovider"

// ResourceSetInputProviderHandler handles OAM resourcesetinputprovider
// components: the kind-named projection of a fluxcd.controlplane.io/v1
// ResourceSetInputProvider (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// fluxoperatorv1.ResourceSetInputProviderSpec, under their json names, decoded
// strictly (decodeKindSpec). It emits the ResourceSetInputProvider, named after
// the component unless `objectName` names it, in the Flux namespace when one
// is set and else in the build namespace, and nothing else. One dimension of
// the environment policy reaches it: the host of `url` is held to the allowed
// registries (see fluxKind's enforce, and the README for what else the object
// reaches). TestCoreKindSchemas_CoverSpec keeps the published key set equal to
// the upstream json tags.
type ResourceSetInputProviderHandler struct{}

// CanHandle returns true for the resourcesetinputprovider component type.
func (h *ResourceSetInputProviderHandler) CanHandle(componentType string) bool {
	return componentType == resourceSetInputProviderType
}

// ContractMetadata implements oam.ContractDescriber.
func (h *ResourceSetInputProviderHandler) ContractMetadata() oam.ContractMetadata {
	return contract(resourceSetInputProviderType)
}

// ComponentObject declares the resourcesetinputprovider kind's
// ResourceSetInputProvider, which lands in the Flux namespace when one is set.
func (h *ResourceSetInputProviderHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: fluxoperatorv1.GroupVersion.Group, Kind: fluxoperatorv1.ResourceSetInputProviderKind}, oam.ObjectScopeFlux
}

// PropertySchema declares every top-level
// fluxoperatorv1.ResourceSetInputProviderSpec field by its json name.
func (h *ResourceSetInputProviderHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "ResourceSetInputProvider spec."
	return map[string]oam.PropertySchema{
		"type": fluxSourceRequiredString("Required. " + spec + "type: where the inputs come from, such as `Static`, `GitHubPullRequest`, `GitLabBranch`, `OCIArtifactTag`, `ExternalService` or `ExternalArtifact`; the API lists the values it takes."),
		"url":  fluxSourceString(spec + "url: the `http://`, `https://` or `oci://` address of the provider's API: the repository of a Git provider, the OCI repository of an OCI provider. The API requires it for every type but `Static` and `ExternalArtifact`, which must not set it. Its host is held to the environment policy's allowed registries, whatever the type; a user or password in it is refused."),
		"serviceAccountName": fluxSourceString(spec + "serviceAccountName: the ServiceAccount, of the namespace the object lands in, that authenticates to a cloud provider by workload identity, or that lists the ExternalArtifacts of an `ExternalArtifact` provider; " +
			"without one, the operator's default ServiceAccount is used when it is started with one, and its own otherwise."),
		"secretRef":     fluxSourceObject(spec + "secretRef: the Secret (`name`) that holds the credentials of the provider, in the namespace the object lands in."),
		"certSecretRef": fluxSourceObject(spec + "certSecretRef: the Secret (`name`) that holds a CA certificate (`ca.crt`), a client certificate (`tls.crt`, `tls.key`) or both, used to connect to the provider, in the namespace the object lands in."),
		"insecure":      fluxSourceBool(spec + "insecure: connect to an `ExternalService` or `OCIArtifactTag` provider over plain HTTP, without TLS."),
		"defaultValues": fluxSourceObject(spec + "defaultValues: the values each input set starts from, by key; launcher writes them as authored."),
		"filter":        fluxSourceObject(spec + "filter: which of the provider's answers become inputs: `includeBranch`, `excludeBranch`, `includeTag`, `excludeTag`, `includeEnvironment` and `excludeEnvironment` (regular expressions), `labels`, `semver` and `limit`, the most input sets returned, which the API defaults to 100 and which cannot be authored as 0."),
		"skip":          fluxSourceObject(spec + "skip: `labels`, a list of labels on which an answer is skipped; one that starts with `!` skips an answer that lacks the label."),
		"schedule": {
			Type: oam.PropertyTypeArray, Description: spec + "schedule: when the provider runs.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One schedule: `cron`, a cron expression, required, `timeZone` (the API's default is UTC) and `window`, a Flux duration (the API's default is 0s).",
			},
		},
		"selectors": {
			Type: oam.PropertyTypeArray, Description: spec + "selectors: the ExternalArtifacts an `ExternalArtifact` provider reads. The API requires them for that type and refuses them for every other. A selector's `namespace` may name another namespace, and `*` every namespace: nothing holds it to the application's own namespace.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One selector: `name`, or `matchLabels` and `matchExpressions`, and `namespace`, the namespace listed (`*` for every namespace; the object's own namespace when unset).",
			},
		},
	}
}

// resourceSetInputProviderURLRemedy completes the refusal of a user or
// password in `url`.
const resourceSetInputProviderURLRemedy = ", which would be written in plain text into the ResourceSetInputProvider; use secretRef, whose Secret holds the provider's credentials"

// refuseResourceSetInputProviderUserinfo refuses a user or a password in `url`
// (refuseHostedFieldUserinfo), with or without a policy.
func refuseResourceSetInputProviderUserinfo(spec *fluxoperatorv1.ResourceSetInputProviderSpec) error {
	return refuseHostedFieldUserinfo(resourceSetInputProviderType, "url", spec.URL, resourceSetInputProviderURLRemedy)
}

// enforceResourceSetInputProviderURL holds the host of `url` to the allowed
// registries, whatever the type: the answer of that host decides what a
// ResourceSet deploys, the ExternalService call sends the referenced
// credential to it, and one rule holds a type a later operator version adds.
// An oci:// url is held by the OCI host rule, as an OCIRepository's is, and
// must name a repository after its registry, as the API requires of an OCI
// provider's; every other url by the host rule of the Flux sources. An unset
// url, which a `Static` or `ExternalArtifact` provider has, names no host.
func enforceResourceSetInputProviderURL(spec *fluxoperatorv1.ResourceSetInputProviderSpec, p oam.Policy) error {
	switch {
	case spec.URL == "":
		return nil
	case strings.HasPrefix(spec.URL, "oci://"):
		return enforceFluxSourceOCIHost(resourceSetInputProviderType, "url", spec.URL, true, p)
	default:
		return enforceFluxSourceHost(resourceSetInputProviderType, "url", spec.URL, p)
	}
}

// resourceSetInputProviderKind is the resourcesetinputprovider kind: see
// fluxKind. The API requires `type`, of an authored Secret reference its
// `name` and of a schedule its `cron`; the type would write each one empty.
// Of a match expression of a selector it requires the key and the operator
// (labelSelectorRequired), fields of a Kubernetes type with no +required
// marker. TestFluxKinds_RequiredMatchMarkers holds the list to the markers of
// the upstream source, and the two of an expression to the source of
// metav1.LabelSelectorRequirement by the schema generators' rule. `filter.limit` is omitted when 0, and the API server then
// defaults it to 100, so an authored 0 is refused (defaultedZeros); a
// schedule's `timeZone` is omitted when empty and defaulted to UTC, so an
// authored "" is refused the same way. A
// schedule's `window` is held to its pattern by its authored text: it is a
// duration under a list (fluxDurationField). The API's expression rules, which
// tie `url`, the Secret references, `serviceAccountName`, `insecure` and
// `selectors` to `type`, are left to the API server (fluxRulesLeft). A user
// or a password in `url` is refused, and its host is held to the allowed
// registries (enforceResourceSetInputProviderURL). The object reads up to two
// Secrets from its own namespace.
var resourceSetInputProviderKind = &fluxKind[fluxoperatorv1.ResourceSetInputProviderSpec]{
	policyFreeKind: policyFreeKind[fluxoperatorv1.ResourceSetInputProviderSpec]{
		upstream: "fluxcd.controlplane.io/v1 ResourceSetInputProviderSpec",
		validate: refuseResourceSetInputProviderUserinfo,
		required: requiredFields(map[string]string{
			"type":               "where the inputs come from, such as `Static`, `GitHubPullRequest` or `OCIArtifactTag`",
			"secretRef.name":     "the name of the Secret that holds the credentials of the provider",
			"certSecretRef.name": "the name of the Secret that holds the certificates the provider is connected to with",
			"schedule[].cron":    "the cron expression of the schedule, such as `0 * * * *`",
		}, labelSelectorRequired("selectors[]")),
		defaultedZeros: defaultedZeroFields{api: "Flux Operator", defaulter: "API server", fields: map[string]string{
			"filter.limit":        "100",
			"schedule[].timeZone": `"UTC"`,
		}},
		build: func(name, namespace string, spec *fluxoperatorv1.ResourceSetInputProviderSpec) client.Object {
			provider := fluxcd.CreateResourceSetInputProvider(name, namespace)
			spec.DeepCopyInto(&provider.Spec)
			return provider
		},
	},
	reads: func(spec *fluxoperatorv1.ResourceSetInputProviderSpec, r *fluxReads) {
		r.secretRef(spec.SecretRef)
		r.secretRef(spec.CertSecretRef)
	},
	durations: []fluxDurationField[fluxoperatorv1.ResourceSetInputProviderSpec]{
		{path: []string{"schedule[]", "window"}, form: fluxduration.Interval},
	},
	enforce: enforceResourceSetInputProviderURL,
}

// ToApplicationConfig decodes an OAM resourcesetinputprovider component into its config.
func (h *ResourceSetInputProviderHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return resourceSetInputProviderKind.config(component)
}
