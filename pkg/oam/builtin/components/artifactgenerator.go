package components

import (
	"strings"

	swv1beta1 "github.com/fluxcd/source-watcher/api/v2/v1beta1"
	"github.com/go-kure/kure/pkg/kubernetes/fluxcd"
	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
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
		"pathPattern":        fluxSourceString(spec + "pathPattern: a directory pattern within a source, `@<alias>/<pattern>`; a named capture such as `{app}` may be used as a placeholder in the artifacts."),
		"serviceAccountName": fluxSourceString(spec + "serviceAccountName: the ServiceAccount, of the namespace the object lands in, whose RBAC the controller checks, with a SubjectAccessReview, before it writes, creates or deletes an ExternalArtifact; the writes themselves use the controller's own credentials. Left out, no check is made for an artifact in the generator's own namespace, and one in another namespace is checked against the cluster's default account if one is configured and else not at all."),
		"artifacts": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. " + spec + "artifacts: the artifacts to generate.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One artifact: `name` and `copy` (each entry with `from`, `@<alias>/<glob>`, and `to`, `@artifact/<path>`, both required, and `exclude`, `strategy`: Overwrite, Merge or Extract, and `optional`, which lets a copy that matches no file pass), both required, and `revision` and `originRevision` (`@<alias>`), `namespace`, the namespace the ExternalArtifact is written into (left out, the object's own; it may be a `{capture}` placeholder), and `inputsFrom`, `@<alias>/<path>` of a YAML file whose content the generated ExternalArtifact exports in its status.",
			},
		},
	}
}

// artifactGeneratorKind is the artifactgenerator kind: see fluxKind. The API
// requires `sources`, of a source its `alias`, `kind` and `name`, and
// `artifacts`, of an artifact its `name` and `copy`, of a copy its `from` and
// `to`; the type would write each one empty. Of `managedNamespaces`, which the
// kind refuses (refuseManagedNamespaces), it requires `prune`,
// `allowedNames`, the `path` of `metadata.fromSource` and the `namespace` and
// `strategy` of an entry of `metadata.from`. The list is generated
// (artifactGeneratorRequired) from the markers of the upstream source. The API's expression rules on the names and namespaces of
// the artifacts and on `managedNamespaces.metadata` are left to the API server
// (fluxRulesLeft). An ArtifactGenerator has no duration and reads no ConfigMap
// or Secret: the sources it names hold the addresses and the credentials.
var artifactGeneratorKind = &fluxKind[swv1beta1.ArtifactGeneratorSpec]{
	policyFreeKind: policyFreeKind[swv1beta1.ArtifactGeneratorSpec]{
		upstream: "source.extensions.fluxcd.io/v1beta1 ArtifactGeneratorSpec",
		required: artifactGeneratorRequired,
		validate: refuseManagedNamespaces,
		build: func(name, namespace string, spec *swv1beta1.ArtifactGeneratorSpec) client.Object {
			generator := fluxcd.CreateArtifactGenerator(name, namespace)
			spec.DeepCopyInto(&generator.Spec)
			return generator
		},
	},
}

// artifactGeneratorRequired is the required list of the artifactgenerator
// kind: the list generated (kind_required.go) as fluxcdAlertRequired is, and
// the required fields of `managedNamespaces`. The kind refuses an authored
// `managedNamespaces` before anything else, so the generator leaves the fields
// under it out (requiredWrittenUnauthorable); they stay on the list, held, as
// the list the kind kept by hand had them.
var artifactGeneratorRequired = requiredFields(generatedKindRequired(artifactGeneratorType), map[string]string{
	"managedNamespaces.prune":                     "whether the controller deletes a namespace it created once no artifact targets it",
	"managedNamespaces.allowedNames":              "the namespaces the controller may create",
	"managedNamespaces.metadata.fromSource.path":  "the file in the source the namespace metadata is read from",
	"managedNamespaces.metadata.from[].namespace": "the namespace the metadata applies to, or `*`",
	"managedNamespaces.metadata.from[].strategy":  "how the metadata is applied",
})

// artifactGeneratorRequiredText is what the generated required paths of the
// artifactgenerator kind say of themselves (requiredText): the sentences of
// the list the kind kept by hand before its list was generated, byte for byte.
var artifactGeneratorRequiredText = map[string]string{
	"sources":                 "the Flux sources the artifacts are made from",
	"sources[].alias":         "the name the copies refer to the source by",
	"sources[].kind":          "the kind of the source, such as GitRepository or OCIRepository",
	"sources[].name":          "the name of the source",
	"artifacts":               "the artifacts to generate",
	"artifacts[].name":        "the name of the generated artifact",
	"artifacts[].copy":        "what is copied into the artifact",
	"artifacts[].copy[].from": "the source and the files to copy, as `@<alias>/<glob>`",
	"artifacts[].copy[].to":   "where in the artifact they are copied, as `@artifact/<path>`",
}

// ToApplicationConfig decodes an OAM artifactgenerator component into its
// config.
func (h *ArtifactGeneratorHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	if err := refuseAuthoredManagedNamespaces(component.Properties); err != nil {
		return nil, err
	}
	return artifactGeneratorKind.config(component)
}

// `managedNamespaces`, which source-watcher v2.3 adds to the
// source.extensions.fluxcd.io/v1beta1 ArtifactGenerator, hands the controller
// the lifecycle of cluster-scoped Namespaces: it creates them, adopts one no
// generator owns, takes over another generator's, labels them and, with
// `prune`, deletes them with everything in them, adopted ones included. An
// application's components make namespaced objects, and no environment policy
// could refuse the field short of dropping the kind, so the kind refuses it.
// An authored one is refused whatever its value: a null the typed spec cannot
// tell from an unauthored field is still not the kind's to write. The other
// fields v2.3 adds are written as authored: `serviceAccountName`, an
// artifact's `namespace` and `inputsFrom`, and a copy's `optional`.
const artifactGeneratorManagedNamespaces = "managedNamespaces"

// managedNamespacesReason is why the kind refuses `managedNamespaces`.
const managedNamespacesReason = "it hands the controller the lifecycle of cluster-scoped Namespaces, which it creates, adopts and, with prune, deletes with everything in them"

// managedNamespacesRefusal is the refusal of an authored `managedNamespaces`.
func managedNamespacesRefusal() error {
	return errors.Errorf("%s: refused: %s", artifactGeneratorManagedNamespaces, managedNamespacesReason)
}

// UnsupportedFieldHint returns the reason `managedNamespaces` is refused, for
// the key in any case, and "" for any other key. pkg/oam appends it to the
// unsupported-field error of an authored artifactgenerator component, which
// kurel build raises before the handler sees the properties.
func (h *ArtifactGeneratorHandler) UnsupportedFieldHint(key string) string {
	if !strings.EqualFold(key, artifactGeneratorManagedNamespaces) {
		return ""
	}
	return artifactGeneratorManagedNamespaces + " is refused: " + managedNamespacesReason
}

// refuseManagedNamespaces refuses a spec that sets `managedNamespaces`, for a
// spec decoded or built directly in Go.
func refuseManagedNamespaces(s *swv1beta1.ArtifactGeneratorSpec) error {
	if s.ManagedNamespaces != nil {
		return managedNamespacesRefusal()
	}
	return nil
}

// refuseAuthoredManagedNamespaces refuses an authored `managedNamespaces`,
// whatever its value. The key matches case-insensitively, as the decode's
// do.
func refuseAuthoredManagedNamespaces(authored map[string]any) error {
	for k := range authored {
		if strings.EqualFold(k, artifactGeneratorManagedNamespaces) {
			return managedNamespacesRefusal()
		}
	}
	return nil
}
