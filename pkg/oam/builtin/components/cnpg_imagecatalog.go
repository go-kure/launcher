package components

import (
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	kurecnpg "github.com/go-kure/kure/pkg/kubernetes/cnpg"
	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// CnpgImageCatalogHandler handles OAM cnpg-imagecatalog components: the
// kind-named projection of a CloudNativePG postgresql.cnpg.io/v1 ImageCatalog
// (go-kure/launcher#790), the images the Clusters of its namespace may run,
// by PostgreSQL major version.
//
// Its properties are exactly the top-level fields of cnpgv1.ImageCatalogSpec,
// under their json names, decoded strictly (decodeKindSpec). It emits the
// ImageCatalog, named after the component unless `objectName` names it, in the
// build namespace, and nothing else. TestCoreKindSchemas_CoverSpec keeps the
// published key set equal to the upstream json tags.
type CnpgImageCatalogHandler struct{}

// CanHandle returns true for the cnpg-imagecatalog component type.
func (h *CnpgImageCatalogHandler) CanHandle(componentType string) bool {
	return componentType == "cnpg-imagecatalog"
}

// PropertySchema declares every top-level cnpgv1.ImageCatalogSpec field by its
// json name. Each is a list of open objects whose content is checked by the
// strict decode, not by this schema.
func (h *CnpgImageCatalogHandler) PropertySchema() map[string]oam.PropertySchema {
	return cnpgImageCatalogSchema("ImageCatalog")
}

// cnpgImageCatalogKind is the cnpg-imagecatalog kind: see policyHeldKind and
// cnpgImageCatalogRequired, validateCnpgImageCatalog (the CRD's expression
// rules and the tag rule) and enforceCnpgImageCatalogPolicy, which it shares
// with cnpg-clusterimagecatalog.
var cnpgImageCatalogKind = &policyHeldKind[cnpgv1.ImageCatalogSpec]{
	policyFreeKind: policyFreeKind[cnpgv1.ImageCatalogSpec]{
		upstream: "postgresql.cnpg.io/v1 ImageCatalogSpec",
		required: cnpgImageCatalogRequired,
		validate: validateCnpgImageCatalog,
		build: func(name, namespace string, spec *cnpgv1.ImageCatalogSpec) client.Object {
			catalog := kurecnpg.CreateImageCatalog(name, namespace)
			spec.DeepCopyInto(&catalog.Spec)
			return catalog
		},
	},
	enforce: enforceCnpgImageCatalogPolicy,
}

// ToApplicationConfig decodes an OAM cnpg-imagecatalog component into its
// config. The object takes the namespace of the application it is generated in.
func (h *CnpgImageCatalogHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return cnpgImageCatalogKind.config(component)
}

// cnpgImageCatalogSchema returns the properties of the cnpg-imagecatalog and
// cnpg-clusterimagecatalog kinds: the top-level fields of
// cnpgv1.ImageCatalogSpec, which the two objects share. kind names the object
// in each description ("ImageCatalog").
func cnpgImageCatalogSchema(kind string) map[string]oam.PropertySchema {
	spec := kind + " spec."
	const decoded = " Decoded strictly into the CloudNativePG API type: see "
	return map[string]oam.PropertySchema{
		"images": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. " + spec + "images: the PostgreSQL images of the catalog, one per major version; the API takes one to eight. Every image, and every extension image under it, is held to the EnvironmentPolicy allowed registries and, with or without a policy, to the tag rule (a tag or a digest, no :latest)." + decoded + "ImageCatalogSpec in its API reference.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One image: image (required: the image reference), major (required: the PostgreSQL major version, 10 or more, once in the catalog) and extensions (the extensions the image offers: name, required; image, an image volume source whose reference names the extension's image; extension_control_path, dynamic_library_path, ld_library_path, bin_path; env, each with a name and a value).",
			},
		},
		"componentImages": {
			Type:        oam.PropertyTypeArray,
			Description: spec + "componentImages: images of other components a Cluster resolves from the catalog by key; the API takes at most 32. Every image is held to the EnvironmentPolicy allowed registries and, with or without a policy, to the tag rule (a tag or a digest, no :latest)." + decoded + "CatalogComponentImage in its API reference.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One component image: key (required: the name it is resolved by, once in the catalog) and image (required: the image reference).",
			},
		},
	}
}

// cnpgImageCatalogRequired is the required list of the cnpg-imagecatalog and
// cnpg-clusterimagecatalog kinds: the fields the ImageCatalog and
// ClusterImageCatalog CRDs require that the Go types write whether or not they
// were authored. TestCnpgFurtherKinds_RequiredMatchCRD holds the list to the
// CRDs.
var cnpgImageCatalogRequired = requiredFields(map[string]string{
	"images":                            "the PostgreSQL images of the catalog",
	"images[].image":                    "the image reference",
	"images[].major":                    "the PostgreSQL major version of the image",
	"images[].extensions[].name":        "the name of the extension",
	"images[].extensions[].env[].name":  "the name of the environment variable",
	"images[].extensions[].env[].value": "the value of the environment variable",
	"componentImages[].key":             "the name the component image is resolved by",
	"componentImages[].image":           "the image reference",
})

// validateCnpgImageCatalog refuses a catalog the CRD's two expression rules
// refuse: a major version held by two images, and a key held by two component
// images. Both compare authored fields of one document. The CRD's bounds (one
// to eight images, a major version of 10 or more, the forms of a key and of an
// extension's name) are value rules, left to the API server. It then holds
// the images the catalog names to the tag rule
// (validateCnpgImageCatalogImageRefs).
func validateCnpgImageCatalog(spec *cnpgv1.ImageCatalogSpec) error {
	majors := make(map[int]int, len(spec.Images))
	for i, image := range spec.Images {
		if first, dup := majors[image.Major]; dup {
			return errors.Errorf("images[%d].major: %d is also the major version of images[%d]; the API takes each major version once", i, image.Major, first)
		}
		majors[image.Major] = i
	}
	keys := make(map[string]int, len(spec.ComponentImages))
	for i, component := range spec.ComponentImages {
		if first, dup := keys[component.Key]; dup {
			return errors.Errorf("componentImages[%d].key: %q is also the key of componentImages[%d]; the API takes each key once", i, component.Key, first)
		}
		keys[component.Key] = i
	}
	return validateCnpgImageCatalogImageRefs(spec)
}

// validateCnpgImageCatalogImageRefs holds the images an ImageCatalog's or a
// ClusterImageCatalog's spec names to ValidateImageRef, with or without an
// environment policy: the three fields enforceCnpgImageCatalogPolicy holds to
// the allowed registries, under the same paths. No untagged image and no
// :latest, as for a container's image: a Cluster that takes its image from the
// catalog runs what the entry names. An extension that names no reference
// names no image and is not checked, as the registry rule does not check it.
// An image and a component image are required, so an empty one is refused
// here as a reference that does not parse.
//
// A digest without a tag passes, as it does everywhere the rule runs. What
// CloudNativePG itself requires of a catalog's image is the operator's rule
// and is left to it.
func validateCnpgImageCatalogImageRefs(spec *cnpgv1.ImageCatalogSpec) error {
	for i, image := range spec.Images {
		if err := ValidateImageRef(image.Image); err != nil {
			return errors.Wrap(err, fmt.Sprintf("images[%d].image", i))
		}
		for j, extension := range image.Extensions {
			reference := extension.ImageVolumeSource.Reference
			if reference == "" {
				continue
			}
			if err := ValidateImageRef(reference); err != nil {
				return errors.Wrap(err, fmt.Sprintf("images[%d].extensions[%d].image.reference", i, j))
			}
		}
	}
	for i, component := range spec.ComponentImages {
		if err := ValidateImageRef(component.Image); err != nil {
			return errors.Wrap(err, fmt.Sprintf("componentImages[%d].image", i))
		}
	}
	return nil
}

// enforceCnpgImageCatalogPolicy holds an ImageCatalog's or a
// ClusterImageCatalog's spec to the environment policy. A catalog runs no pod,
// but every image a Cluster takes from it is one the operator runs, so each of
// the three fields that name an image is held to the allowed registries: an
// image, a component image, and the reference of an extension's image volume.
// An extension that names no reference names no image, and nothing is checked
// for it.
func enforceCnpgImageCatalogPolicy(spec *cnpgv1.ImageCatalogSpec, p oam.Policy) error {
	allowed := p.AllowedRegistries()
	for i, image := range spec.Images {
		if err := enforceAllowedRegistries(image.Image, allowed); err != nil {
			return errors.Wrap(err, fmt.Sprintf("images[%d].image", i))
		}
		for j, extension := range image.Extensions {
			reference := extension.ImageVolumeSource.Reference
			if reference == "" {
				continue
			}
			if err := enforceAllowedRegistries(reference, allowed); err != nil {
				return errors.Wrap(err, fmt.Sprintf("images[%d].extensions[%d].image.reference", i, j))
			}
		}
	}
	for i, component := range spec.ComponentImages {
		if err := enforceAllowedRegistries(component.Image, allowed); err != nil {
			return errors.Wrap(err, fmt.Sprintf("componentImages[%d].image", i))
		}
	}
	return nil
}

// ContractMetadata implements oam.ContractDescriber.
func (h *CnpgImageCatalogHandler) ContractMetadata() oam.ContractMetadata {
	return contract("cnpg-imagecatalog")
}

// ComponentObject declares the cnpg-imagecatalog kind's ImageCatalog.
func (h *CnpgImageCatalogHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return cnpgKind(cnpgv1.ImageCatalogKind), oam.ObjectScopeNamespaced
}
