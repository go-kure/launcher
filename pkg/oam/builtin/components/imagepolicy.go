package components

import (
	imagev1 "github.com/fluxcd/image-reflector-controller/api/v1"
	"github.com/go-kure/kure/pkg/kubernetes/fluxcd"
	"github.com/go-kure/kure/pkg/stack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/internal/fluxduration"
)

// imagePolicyType is the component type of the ImagePolicy kind.
const imagePolicyType = "imagepolicy"

// ImagePolicyHandler handles OAM imagepolicy components: the kind-named
// projection of an image.toolkit.fluxcd.io/v1 ImagePolicy
// (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of imagev1.ImagePolicySpec,
// under their json names, decoded strictly (decodeKindSpec). It emits the
// ImagePolicy, named after the component unless `objectName` names it, in the
// Flux namespace when one is set and else in the build namespace, and nothing
// else. No environment policy applies: see fluxKind, and the README for what
// `imageRepositoryRef` reaches. TestCoreKindSchemas_CoverSpec keeps the
// published key set equal to the upstream json tags.
type ImagePolicyHandler struct{}

// CanHandle returns true for the imagepolicy component type.
func (h *ImagePolicyHandler) CanHandle(componentType string) bool {
	return componentType == imagePolicyType
}

// ContractMetadata implements oam.ContractDescriber.
func (h *ImagePolicyHandler) ContractMetadata() oam.ContractMetadata {
	return contract(imagePolicyType)
}

// ComponentObject declares the imagepolicy kind's ImagePolicy, which lands in
// the Flux namespace when one is set.
func (h *ImagePolicyHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: imagev1.GroupVersion.Group, Kind: imagev1.ImagePolicyKind}, oam.ObjectScopeFlux
}

// PropertySchema declares every top-level imagev1.ImagePolicySpec field by its
// json name.
func (h *ImagePolicyHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "ImagePolicy spec."
	return map[string]oam.PropertySchema{
		"imageRepositoryRef": {
			Type: oam.PropertyTypeObject, Required: true, AdditionalProperties: true,
			Description: "Required. " + spec + "imageRepositoryRef: the ImageRepository whose scanned tags the policy selects from (`name`, required; `namespace`). It may name another namespace: nothing holds it to the application's own.",
		},
		"policy": {
			Type: oam.PropertyTypeObject, Required: true, AdditionalProperties: true,
			Description: "Required. " + spec + "policy: how the latest image is selected: `semver` (`range`, required), `alphabetical` (`order`) or `numerical` (`order`).",
		},
		"filterTags":             fluxSourceObject(spec + "filterTags: the tags the policy considers: a regular expression `pattern`, and a capture group to `extract` before the tags are compared."),
		"digestReflectionPolicy": fluxSourceString(spec + "digestReflectionPolicy: when the digest of the selected image is recorded: `Always`, `IfNotPresent` or `Never` (the API's default)."),
		"interval":               fluxSourceString(spec + "interval: how often the digest of the selected tag is refreshed, as a Flux duration. The API accepts it with `digestReflectionPolicy: Always` only, and requires it there; the kind does not check that."),
		"suspend":                fluxSourceBool(spec + "suspend: stop reconciling this policy."),
	}
}

// imagePolicyKind is the imagepolicy kind: see fluxKind. The API requires
// `imageRepositoryRef` with its `name` and `policy`, and of a `semver` policy
// its `range`; the type would write each one empty.
// TestFluxKinds_RequiredMatchMarkers holds the list to the markers of the
// upstream source. `interval` is held to the form its pattern takes; the two
// expression rules that tie it to `digestReflectionPolicy` are left to the API
// server (fluxRulesLeft). An ImagePolicy reads no ConfigMap or Secret: the
// ImageRepository it names holds the credentials.
var imagePolicyKind = &fluxKind[imagev1.ImagePolicySpec]{
	policyFreeKind: policyFreeKind[imagev1.ImagePolicySpec]{
		upstream: "image.toolkit.fluxcd.io/v1 ImagePolicySpec",
		defaultedZeros: fluxDefaultedZeros(map[string]string{
			"digestReflectionPolicy":    `"Never"`,
			"policy.alphabetical.order": `"asc"`,
			"policy.numerical.order":    `"asc"`,
		}),
		required: map[string]string{
			"imageRepositoryRef":      "the ImageRepository whose tags the policy selects from",
			"imageRepositoryRef.name": "the name of the ImageRepository",
			"policy":                  "how the latest image is selected: `semver`, `alphabetical` or `numerical`",
			"policy.semver.range":     "the semantic version range the selected tag is the highest of",
		},
		build: func(name, namespace string, spec *imagev1.ImagePolicySpec) client.Object {
			policy := fluxcd.CreateImagePolicy(name, namespace)
			spec.DeepCopyInto(&policy.Spec)
			return policy
		},
	},
	durations: []fluxDurationField[imagev1.ImagePolicySpec]{
		{path: []string{"interval"}, form: fluxduration.Interval, get: func(s *imagev1.ImagePolicySpec) *metav1.Duration { return s.Interval }},
	},
}

// ToApplicationConfig decodes an OAM imagepolicy component into its config.
func (h *ImagePolicyHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return imagePolicyKind.config(component)
}
