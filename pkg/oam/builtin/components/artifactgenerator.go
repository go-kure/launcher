package components

import (
	swv1beta1 "github.com/fluxcd/source-watcher/api/v2/v1beta1"
	"github.com/go-kure/kure/pkg/kubernetes/fluxcd"
	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// artifactGeneratorType is the component type of the ArtifactGenerator kind.
const artifactGeneratorType = "artifactgenerator"

// ArtifactGeneratorHandler handles OAM artifactgenerator components: the
// kind-named projection of a source.extensions.fluxcd.io/v1beta1
// ArtifactGenerator (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// swv1beta1.ArtifactGeneratorSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the ArtifactGenerator, named after the component
// unless `objectName` names it, in the Flux namespace when one is set and else
// in the build namespace, and nothing else. No environment policy applies: see
// fluxKind, and the README for what `sources` reaches.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type ArtifactGeneratorHandler struct{}

// CanHandle returns true for the artifactgenerator component type.
func (h *ArtifactGeneratorHandler) CanHandle(componentType string) bool {
	return componentType == artifactGeneratorType
}

// ContractMetadata implements oam.ContractDescriber.
func (h *ArtifactGeneratorHandler) ContractMetadata() oam.ContractMetadata {
	return contract(artifactGeneratorType)
}

// ComponentObject declares the artifactgenerator kind's ArtifactGenerator,
// which lands in the Flux namespace when one is set.
func (h *ArtifactGeneratorHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: swv1beta1.GroupVersion.Group, Kind: swv1beta1.ArtifactGeneratorKind}, oam.ObjectScopeFlux
}

// PropertySchema declares every top-level swv1beta1.ArtifactGeneratorSpec
// field by its json name.
func (h *ArtifactGeneratorHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "ArtifactGenerator spec."
	return map[string]oam.PropertySchema{
		"commonMetadata": fluxSourceObject(spec + "commonMetadata: `labels` and `annotations` that the API says are \"applied to all resources\". They are written into the spec, for the controller; the labels and annotations of the ArtifactGenerator object are the `labels` and `annotations` properties."),
		"sources": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. " + spec + "sources: the Flux sources the artifacts are made from. An entry may name another namespace: nothing holds it to the application's own.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One source: `alias` (the name the copies refer to it by), `kind` (Bucket, GitRepository, OCIRepository, HelmChart or ExternalArtifact) and `name`, all required, and `namespace`.",
			},
		},
		"pathPattern": fluxSourceString(spec + "pathPattern: a directory pattern within a source, `@<alias>/<pattern>`; a named capture such as `{app}` may be used as a placeholder in the artifacts."),
		"artifacts": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. " + spec + "artifacts: the artifacts to generate.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One artifact: `name` and `copy` (each entry with `from`, `@<alias>/<glob>`, and `to`, `@artifact/<path>`, both required, and `exclude` and `strategy`: Overwrite, Merge or Extract), both required, and `revision` and `originRevision` (`@<alias>`).",
			},
		},
	}
}

// artifactGeneratorKind is the artifactgenerator kind: see fluxKind. The API
// requires `sources`, of a source its `alias`, `kind` and `name`, and
// `artifacts`, of an artifact its `name` and `copy`, of a copy its `from` and
// `to`; the type would write each one empty.
// TestFluxKinds_RequiredMatchMarkers holds the list to the markers of the
// upstream source. The API's expression rule on the names of the artifacts is
// left to the API server (fluxRulesLeft). An ArtifactGenerator has no duration
// and reads no ConfigMap or Secret: the sources it names hold the addresses and
// the credentials.
var artifactGeneratorKind = &fluxKind[swv1beta1.ArtifactGeneratorSpec]{
	policyFreeKind: policyFreeKind[swv1beta1.ArtifactGeneratorSpec]{
		upstream: "source.extensions.fluxcd.io/v1beta1 ArtifactGeneratorSpec",
		required: map[string]string{
			"sources":                 "the Flux sources the artifacts are made from",
			"sources[].alias":         "the name the copies refer to the source by",
			"sources[].kind":          "the kind of the source, such as GitRepository or OCIRepository",
			"sources[].name":          "the name of the source",
			"artifacts":               "the artifacts to generate",
			"artifacts[].name":        "the name of the generated artifact",
			"artifacts[].copy":        "what is copied into the artifact",
			"artifacts[].copy[].from": "the source and the files to copy, as `@<alias>/<glob>`",
			"artifacts[].copy[].to":   "where in the artifact they are copied, as `@artifact/<path>`",
		},
		build: func(name, namespace string, spec *swv1beta1.ArtifactGeneratorSpec) client.Object {
			generator := fluxcd.CreateArtifactGenerator(name, namespace)
			spec.DeepCopyInto(&generator.Spec)
			return generator
		},
	},
}

// ToApplicationConfig decodes an OAM artifactgenerator component into its
// config.
func (h *ArtifactGeneratorHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return artifactGeneratorKind.config(component)
}
