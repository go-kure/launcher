package components

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
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
		"pathPattern": fluxSourceString(spec + "pathPattern: a directory pattern within a source, `@<alias>/<pattern>`; a named capture such as `{app}` may be used as a placeholder in the artifacts."),
		"artifacts": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. " + spec + "artifacts: the artifacts to generate.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One artifact: `name` and `copy` (each entry with `from`, `@<alias>/<glob>`, and `to`, `@artifact/<path>`, both required, and `exclude`, `strategy`: Overwrite, Merge or Extract, and `optional`, which lets a copy that matches no file pass), both required, and `revision` and `originRevision` (`@<alias>`), and `inputsFrom`, `@<alias>/<path>` of a YAML file whose content the generated ExternalArtifact exports in its status.",
			},
		},
	}
}

// artifactGeneratorKind is the artifactgenerator kind: see fluxKind. The API
// requires `sources`, of a source its `alias`, `kind` and `name`, and
// `artifacts`, of an artifact its `name` and `copy`, of a copy its `from` and
// `to`; the type would write each one empty. Of `managedNamespaces`, which the
// kind refuses (artifactGeneratorDeferredFields), it requires `prune`,
// `allowedNames`, the `path` of `metadata.fromSource` and the `namespace` and
// `strategy` of an entry of `metadata.from`.
// TestFluxKinds_RequiredMatchMarkers holds the list to the markers of the
// upstream source. The API's expression rules on the names and namespaces of
// the artifacts and on `managedNamespaces.metadata` are left to the API server
// (fluxRulesLeft). An ArtifactGenerator has no duration and reads no ConfigMap
// or Secret: the sources it names hold the addresses and the credentials.
var artifactGeneratorKind = &fluxKind[swv1beta1.ArtifactGeneratorSpec]{
	policyFreeKind: policyFreeKind[swv1beta1.ArtifactGeneratorSpec]{
		upstream: "source.extensions.fluxcd.io/v1beta1 ArtifactGeneratorSpec",
		required: map[string]string{
			"sources":                                     "the Flux sources the artifacts are made from",
			"sources[].alias":                             "the name the copies refer to the source by",
			"sources[].kind":                              "the kind of the source, such as GitRepository or OCIRepository",
			"sources[].name":                              "the name of the source",
			"artifacts":                                   "the artifacts to generate",
			"artifacts[].name":                            "the name of the generated artifact",
			"artifacts[].copy":                            "what is copied into the artifact",
			"artifacts[].copy[].from":                     "the source and the files to copy, as `@<alias>/<glob>`",
			"artifacts[].copy[].to":                       "where in the artifact they are copied, as `@artifact/<path>`",
			"managedNamespaces.prune":                     "whether the controller deletes a namespace it created once no artifact targets it",
			"managedNamespaces.allowedNames":              "the namespaces the controller may create",
			"managedNamespaces.metadata.fromSource.path":  "the file in the source the namespace metadata is read from",
			"managedNamespaces.metadata.from[].namespace": "the namespace the metadata applies to, or `*`",
			"managedNamespaces.metadata.from[].strategy":  "how the metadata is applied",
		},
		validate: refuseDeferredArtifactGeneratorSpec,
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
	if err := refuseAuthoredDeferredArtifactGeneratorFields(component.Properties); err != nil {
		return nil, err
	}
	return artifactGeneratorKind.config(component)
}

// The fields of the source.extensions.fluxcd.io/v1beta1 ArtifactGenerator that
// source-watcher v2.3 adds, which the kind refuses until it models what they
// reach (go-kure/launcher#1073): `managedNamespaces` creates, labels and prunes
// Namespaces, `serviceAccountName` names the account the controller acts as,
// and an artifact's `namespace` writes it into another namespace. An authored
// one is refused whatever its value: a "" or a null the typed spec cannot tell
// from an unauthored field is still not the kind's to write. An artifact's
// `inputsFrom` and a copy's `optional` are written as authored: the one reads
// a file of the generator's own sources or artifact, the other lets a copy
// that matches nothing pass.
var (
	artifactGeneratorDeferredFields         = []string{"managedNamespaces", "serviceAccountName"}
	artifactGeneratorDeferredArtifactFields = []string{"namespace"}
)

// artifactGeneratorDeferredRefusal is the refusal of a deferred field at path.
func artifactGeneratorDeferredRefusal(path string) error {
	return errors.Errorf("%s: not supported by this component yet (go-kure/launcher#1073)", path)
}

// refuseDeferredArtifactGeneratorSpec refuses a deferred field a spec sets,
// for a spec decoded or built directly in Go.
func refuseDeferredArtifactGeneratorSpec(s *swv1beta1.ArtifactGeneratorSpec) error {
	switch {
	case s.ManagedNamespaces != nil:
		return artifactGeneratorDeferredRefusal("managedNamespaces")
	case s.ServiceAccountName != "":
		return artifactGeneratorDeferredRefusal("serviceAccountName")
	}
	for i, a := range s.OutputArtifacts {
		if a.Namespace != "" {
			return artifactGeneratorDeferredRefusal(fmt.Sprintf("artifacts[%d].namespace", i))
		}
	}
	return nil
}

// refuseAuthoredDeferredArtifactGeneratorFields refuses a deferred field an
// author wrote, whatever its value. Keys match case-insensitively, as the
// decode's do; the properties are read in their JSON form, as the decode
// reads them (numbers kept exact). A shape the decode refuses is left to it.
func refuseAuthoredDeferredArtifactGeneratorFields(authored map[string]any) error {
	raw, err := json.Marshal(authored)
	if err != nil {
		return errors.Wrap(err, "artifactgenerator: properties do not encode as JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var props map[string]any
	if err := dec.Decode(&props); err != nil {
		return errors.Wrap(err, "artifactgenerator: properties do not read as a JSON object")
	}
	if err := refuseDeferredKeys(props, "", artifactGeneratorDeferredFields); err != nil {
		return err
	}
	k, err := foldedKey(props, "artifacts", nil)
	if err != nil || k == "" {
		return err
	}
	artifacts, _ := props[k].([]any)
	for i, item := range artifacts {
		if artifact, ok := item.(map[string]any); ok {
			if err := refuseDeferredKeys(artifact, fmt.Sprintf("artifacts[%d].", i), artifactGeneratorDeferredArtifactFields); err != nil {
				return err
			}
		}
	}
	return nil
}

// refuseDeferredKeys refuses the first key of node, in key order, that matches
// one of fields case-insensitively (artifactGeneratorDeferredRefusal, at
// prefix and the field's json name).
func refuseDeferredKeys(node map[string]any, prefix string, fields []string) error {
	for _, k := range slices.Sorted(maps.Keys(node)) {
		for _, field := range fields {
			if strings.EqualFold(k, field) {
				return artifactGeneratorDeferredRefusal(prefix + field)
			}
		}
	}
	return nil
}
