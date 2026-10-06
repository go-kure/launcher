package components

import (
	autov1 "github.com/fluxcd/image-automation-controller/api/v1"
	"github.com/go-kure/kure/pkg/kubernetes/fluxcd"
	"github.com/go-kure/kure/pkg/stack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/internal/fluxduration"
)

// imageUpdateAutomationType is the component type of the
// ImageUpdateAutomation kind.
const imageUpdateAutomationType = "imageupdateautomation"

// ImageUpdateAutomationHandler handles OAM imageupdateautomation components:
// the kind-named projection of an image.toolkit.fluxcd.io/v1
// ImageUpdateAutomation (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// autov1.ImageUpdateAutomationSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the ImageUpdateAutomation, named after the
// component unless `objectName` names it, in the Flux namespace when one is
// set and else in the build namespace, and nothing else. No environment policy
// applies: see fluxKind, and the README for what `sourceRef` reaches and what
// the object makes its controller write. TestCoreKindSchemas_CoverSpec keeps
// the published key set equal to the upstream json tags.
type ImageUpdateAutomationHandler struct{}

// CanHandle returns true for the imageupdateautomation component type.
func (h *ImageUpdateAutomationHandler) CanHandle(componentType string) bool {
	return componentType == imageUpdateAutomationType
}

// PropertySchema declares every top-level autov1.ImageUpdateAutomationSpec
// field by its json name.
func (h *ImageUpdateAutomationHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "ImageUpdateAutomation spec."
	return map[string]oam.PropertySchema{
		"sourceRef": {
			Type: oam.PropertyTypeObject, Required: true, AdditionalProperties: true,
			Description: "Required. " + spec + "sourceRef: the GitRepository that gives access to the repository the automation commits to (`kind`, which is `GitRepository`, and `name`, both required; `apiVersion`, `namespace`). It may name another namespace: nothing holds it to the application's own.",
		},
		"git": fluxSourceObject(spec + "git: how the repository is checked out (`checkout.ref`), committed to (`commit`, with its `author.email`, required once `git` is written; `signingKey`, `messageTemplate`, `messageTemplateValues`) and pushed to (`push.branch`, `push.refspec`, `push.options`). The Secret `commit.signingKey.secretRef` names is one of the namespace the object lands in."),
		"interval": {
			Type: oam.PropertyTypeString, Required: true,
			Description: "Required. " + spec + "interval: the least time between two runs of the automation, as a Flux duration.",
		},
		"policySelector": fluxSourceObject(spec + "policySelector: a label selector over the ImagePolicies of the object's namespace whose selections are applied; every one of them when unset."),
		"update":         fluxSourceObject(spec + "update: how the files are updated: `strategy` (`Setters`, the API's default) and the `path` of the directory to update, the root of the repository when unset."),
		"suspend":        fluxSourceBool(spec + "suspend: stop running this automation."),
	}
}

// imageUpdateAutomationKind is the imageupdateautomation kind: see fluxKind.
// The API requires `sourceRef` with its `kind` and `name`, and `interval`; of
// an authored `git` its `commit` with the author's `email`, of an authored
// checkout its `ref`, and of an authored signing key its `secretRef` with its
// `name`; the type would write each one empty. The API defaults
// `sourceRef.kind`, and it is required here all the same: the type writes it
// empty, which is a value and not the default.
// TestFluxKinds_RequiredMatchMarkers holds the list to the markers of the
// upstream source. `interval` is held to the form its pattern takes. The
// object reads one Secret from its own namespace, the signing key's.
var imageUpdateAutomationKind = &fluxKind[autov1.ImageUpdateAutomationSpec]{
	policyFreeKind: policyFreeKind[autov1.ImageUpdateAutomationSpec]{
		upstream: "image.toolkit.fluxcd.io/v1 ImageUpdateAutomationSpec",
		required: map[string]string{
			"sourceRef":                            "the GitRepository that gives access to the repository the automation commits to",
			"sourceRef.kind":                       "the kind of the source: `GitRepository`, the API's default, which the object would otherwise write empty",
			"sourceRef.name":                       "the name of the GitRepository",
			"interval":                             "the least time between two runs of the automation, such as `30m`",
			"git.checkout.ref":                     "the branch, tag or commit to clone",
			"git.commit":                           "how the automation commits",
			"git.commit.author":                    "the author of the commits",
			"git.commit.author.email":              "the email address of the author of the commits",
			"git.commit.signingKey.secretRef":      "the Secret that holds the signing key",
			"git.commit.signingKey.secretRef.name": "the name of the Secret that holds the signing key, in the namespace the object lands in",
		},
		build: func(name, namespace string, spec *autov1.ImageUpdateAutomationSpec) client.Object {
			automation := fluxcd.CreateImageUpdateAutomation(name, namespace)
			spec.DeepCopyInto(&automation.Spec)
			return automation
		},
	},
	reads: func(spec *autov1.ImageUpdateAutomationSpec, r *fluxReads) {
		if spec.GitSpec != nil && spec.GitSpec.Commit.SigningKey != nil {
			r.secretRef(&spec.GitSpec.Commit.SigningKey.SecretRef)
		}
	},
	durations: []fluxDurationField[autov1.ImageUpdateAutomationSpec]{
		{path: []string{"interval"}, form: fluxduration.Interval, get: func(s *autov1.ImageUpdateAutomationSpec) *metav1.Duration { return &s.Interval }},
	},
}

// ToApplicationConfig decodes an OAM imageupdateautomation component into its
// config.
func (h *ImageUpdateAutomationHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return imageUpdateAutomationKind.config(component)
}
