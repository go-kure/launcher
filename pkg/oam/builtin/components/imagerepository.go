package components

import (
	imagev1 "github.com/fluxcd/image-reflector-controller/api/v1"
	"github.com/go-kure/kure/pkg/kubernetes/fluxcd"
	"github.com/go-kure/kure/pkg/stack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/internal/fluxduration"
)

// imageRepositoryType is the component type of the ImageRepository kind.
const imageRepositoryType = "imagerepository"

// ImageRepositoryHandler handles OAM imagerepository components: the
// kind-named projection of an image.toolkit.fluxcd.io/v1 ImageRepository
// (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// imagev1.ImageRepositorySpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the ImageRepository, named after the component
// unless `objectName` names it, in the Flux namespace when one is set and else
// in the build namespace, and nothing else. One dimension of the environment
// policy reaches it: the registry of `image` is held to the allowed
// registries (see fluxKind's enforce, and the README for what else the object
// reaches). TestCoreKindSchemas_CoverSpec keeps the published key set equal to
// the upstream json tags.
type ImageRepositoryHandler struct{}

// CanHandle returns true for the imagerepository component type.
func (h *ImageRepositoryHandler) CanHandle(componentType string) bool {
	return componentType == imageRepositoryType
}

// ContractMetadata implements oam.ContractDescriber.
func (h *ImageRepositoryHandler) ContractMetadata() oam.ContractMetadata {
	return contract(imageRepositoryType)
}

// ComponentObject declares the imagerepository kind's ImageRepository, which
// lands in the Flux namespace when one is set.
func (h *ImageRepositoryHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: imagev1.GroupVersion.Group, Kind: imagev1.ImageRepositoryKind}, oam.ObjectScopeFlux
}

// PropertySchema declares every top-level imagev1.ImageRepositorySpec field by
// its json name.
func (h *ImageRepositoryHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "ImageRepository spec."
	return map[string]oam.PropertySchema{
		"image": fluxSourceRequiredString("Required. " + spec + "image: the name of the image repository whose tags are scanned, such as `ghcr.io/org/app`. Its registry is held to the environment policy's allowed registries; a name with no registry is one of `docker.io`."),
		"interval": {
			Type: oam.PropertyTypeString, Required: true,
			Description: "Required. " + spec + "interval: the time to wait between two scans of the repository, as a Flux duration.",
		},
		"timeout":            fluxSourceString(spec + "timeout: the timeout of a scan, as a Flux duration in the units `ms`, `s` and `m`; the API's default is the interval."),
		"secretRef":          fluxSourceObject(spec + "secretRef: the Secret (`name`) that holds the credentials of the registry, in the namespace the object lands in. The API asks for one as `kubectl create secret docker-registry` creates it."),
		"proxySecretRef":     fluxSourceObject(spec + "proxySecretRef: the Secret (`name`) that holds the configuration of the proxy the registry is reached through, in the namespace the object lands in."),
		"certSecretRef":      fluxSourceObject(spec + "certSecretRef: the Secret (`name`) that holds a client certificate (`tls.crt`, `tls.key`), a CA certificate (`ca.crt`) or both, used to connect to the registry, in the namespace the object lands in."),
		"serviceAccountName": fluxSourceString(spec + "serviceAccountName: the ServiceAccount, of the namespace the object lands in, whose attached pull secrets authenticate the scan."),
		"suspend":            fluxSourceBool(spec + "suspend: stop scanning this repository."),
		"accessFrom":         fluxSourceObject(spec + "accessFrom: which namespaces may refer to this ImageRepository from an object of theirs: `namespaceSelectors`, a list of label selectors (`matchLabels`) over namespaces; an entry with no `matchLabels` selects every namespace."),
		"exclusionList": {
			Type: oam.PropertyTypeArray, Description: spec + "exclusionList: tags that match one of these are not stored; at most 25. The API's default is `^.*\\.sig$`.",
			Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "One regular expression."},
		},
		"provider": fluxSourceString(spec + "provider: how the scan authenticates: `generic` (the API's default), `aws`, `azure` or `gcp`."),
		"insecure": fluxSourceBool(spec + "insecure: connect to a registry that serves plain HTTP, without TLS."),
	}
}

// imageRepositoryKind is the imagerepository kind: see fluxKind. The API
// requires `image` and `interval`. The type omits an empty `image`, so its
// omission is refused here (validate); it writes an unauthored `interval` as
// 0s, the unauthored `name` of an authored Secret reference as empty and the
// unauthored `namespaceSelectors` of an authored `accessFrom` as null, so
// those are on the required list, which TestFluxKinds_RequiredMatchMarkers
// holds to the markers of the upstream source. `interval` and `timeout` are each held to the form their pattern
// takes: `timeout` takes no h, and one of an hour or more is written in
// minutes (emitFluxKind). The registry of `image` is held to the allowed
// registries of the environment policy, by the rule an image of a pod is held
// to; no tag rule applies, since the field names a repository. The object
// reads up to three Secrets from its own namespace.
var imageRepositoryKind = &fluxKind[imagev1.ImageRepositorySpec]{
	policyFreeKind: policyFreeKind[imagev1.ImageRepositorySpec]{
		upstream:       "image.toolkit.fluxcd.io/v1 ImageRepositorySpec",
		defaultedZeros: fluxDefaultedZeros(map[string]string{"provider": `"generic"`}),
		validate: func(spec *imagev1.ImageRepositorySpec) error {
			if spec.Image == "" {
				return errors.New("image: required (the name of the image repository whose tags are scanned, such as ghcr.io/org/app)")
			}
			return nil
		},
		required: map[string]string{
			"interval":            "the time to wait between two scans of the repository, such as `10m`",
			"secretRef.name":      "the name of the Secret that holds the credentials of the registry",
			"proxySecretRef.name": "the name of the Secret that holds the configuration of the proxy",
			"certSecretRef.name":  "the name of the Secret that holds the certificates the registry is connected to with",
			"accessFrom.namespaceSelectors": "the selectors of the namespaces that may refer to this ImageRepository; " +
				"an entry with no `matchLabels` selects every namespace",
		},
		build: func(name, namespace string, spec *imagev1.ImageRepositorySpec) client.Object {
			repo := fluxcd.CreateImageRepository(name, namespace)
			spec.DeepCopyInto(&repo.Spec)
			return repo
		},
	},
	reads: func(spec *imagev1.ImageRepositorySpec, r *fluxReads) {
		r.secretRef(spec.SecretRef)
		r.secretRef(spec.ProxySecretRef)
		r.secretRef(spec.CertSecretRef)
	},
	durations: []fluxDurationField[imagev1.ImageRepositorySpec]{
		{path: []string{"interval"}, form: fluxduration.Interval, get: func(s *imagev1.ImageRepositorySpec) *metav1.Duration { return &s.Interval }},
		{path: []string{"timeout"}, form: fluxduration.SourceTimeout, get: func(s *imagev1.ImageRepositorySpec) *metav1.Duration { return s.Timeout }},
	},
	enforce: func(spec *imagev1.ImageRepositorySpec, p oam.Policy) error {
		return enforceAllowedRegistries(spec.Image, p.AllowedRegistries())
	},
}

// ToApplicationConfig decodes an OAM imagerepository component into its config.
func (h *ImageRepositoryHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return imageRepositoryKind.config(component)
}
