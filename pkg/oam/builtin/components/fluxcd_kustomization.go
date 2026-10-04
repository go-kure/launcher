package components

import (
	"slices"
	"strconv"
	"strings"
	"time"

	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"
	"github.com/go-kure/kure/pkg/kubernetes/fluxcd"
	"github.com/go-kure/kure/pkg/stack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
	"github.com/go-kure/launcher/pkg/oam/internal/fluxduration"
)

// fluxcdKustomizationType is the component type of the Flux Kustomization
// kind. The bare word "kustomization" would read as the kustomization.yaml
// file, so the type carries the fluxcd- prefix (go-kure/launcher#352).
const fluxcdKustomizationType = "fluxcd-kustomization"

// fluxcdKustomizationDefaultInterval is spec.interval when the component leaves
// it unset. Flux requires the field; 60m matches the Flux source components and
// helmrelease.
const fluxcdKustomizationDefaultInterval = 60 * time.Minute

// FluxcdKustomizationHandler handles the kind-named `fluxcd-kustomization`
// component: a 1:1 projection of Flux's KustomizationSpec. It emits exactly one
// Kustomization and creates no source: spec.sourceRef names an existing one. It
// is an authored Flux object like helmrelease, not how an application is
// delivered. The `oci` component lowers to one of these plus an ocirepository
// (OCIRule).
type FluxcdKustomizationHandler struct{}

// fluxcdKustomizationDurations are KustomizationSpec's duration fields. All
// take the same form.
var fluxcdKustomizationDurations = []fluxDurationField[kustv1.KustomizationSpec]{
	{path: []string{"interval"}, form: fluxduration.Interval, get: func(s *kustv1.KustomizationSpec) *metav1.Duration { return &s.Interval }},
	{path: []string{"retryInterval"}, form: fluxduration.Interval, get: func(s *kustv1.KustomizationSpec) *metav1.Duration { return s.RetryInterval }},
	{path: []string{"timeout"}, form: fluxduration.Interval, get: func(s *kustv1.KustomizationSpec) *metav1.Duration { return s.Timeout }},
}

// fluxcdKustomizationSourceKinds are the kinds Flux's Kustomization sourceRef
// admits (its CRD enum on kind).
var fluxcdKustomizationSourceKinds = []string{"OCIRepository", "GitRepository", "Bucket", "ExternalArtifact"}

// CanHandle returns true for the fluxcd-kustomization component type.
func (h *FluxcdKustomizationHandler) CanHandle(componentType string) bool {
	return componentType == fluxcdKustomizationType
}

// PropertySchema declares every top-level key of kustv1.KustomizationSpec, so
// authored-property validation admits exactly those keys. The nested Flux
// shapes stay open objects here: ToApplicationConfig decodes the whole map
// strictly into KustomizationSpec, which refuses an unknown or wrongly typed
// key at any depth. A test ties this key set to the struct.
func (h *FluxcdKustomizationHandler) PropertySchema() map[string]oam.PropertySchema {
	object := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: desc}
	}
	objects := func(desc, item string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeArray, Description: desc, Items: &oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: item}}
	}
	strs := func(desc, item string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeArray, Description: desc, Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: item}}
	}
	str := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeString, Description: desc}
	}
	boolean := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeBoolean, Description: desc}
	}
	return map[string]oam.PropertySchema{
		"commonMetadata":          object("Kustomization spec.commonMetadata: labels and annotations applied to every object it applies."),
		"dependsOn":               objects("Kustomization spec.dependsOn: Kustomizations that must be ready first.", "One dependency reference (name, namespace, readyExpr)."),
		"decryption":              object("Kustomization spec.decryption: how Secrets in the source are decrypted (provider, serviceAccountName, secretRef)."),
		"interval":                str("Kustomization spec.interval as a Flux duration: unsigned, units ms, s, m, h, e.g. 10m or 1h30m; 0s or at least 1ms. Defaults to 60m when unset or zero."),
		"retryInterval":           str("Kustomization spec.retryInterval after a failed reconciliation, as a Flux duration: unsigned, units ms, s, m, h; 0s or at least 1ms."),
		"kubeConfig":              object("Kustomization spec.kubeConfig: a kubeconfig reference for a remote cluster."),
		"path":                    str("Kustomization spec.path: the directory in the source holding the kustomization.yaml file, or plain manifests. Flux uses the source's root when unset."),
		"postBuild":               object("Kustomization spec.postBuild: variable substitution on the built manifests (substituteStrategy, substitute, substituteFrom)."),
		"prune":                   boolean("Kustomization spec.prune: delete objects that left the source. Flux requires the field; unset, false is emitted."),
		"deletionPolicy":          str("Kustomization spec.deletionPolicy: what happens to the applied objects when the Kustomization is deleted (MirrorPrune, Delete, WaitForTermination, Orphan)."),
		"healthChecks":            objects("Kustomization spec.healthChecks: objects Flux checks for readiness. kustomize-controller ignores them when wait is true.", "One object reference (apiVersion, kind, name, namespace)."),
		"namePrefix":              str("Kustomization spec.namePrefix: a prefix for the name of every applied object."),
		"nameSuffix":              str("Kustomization spec.nameSuffix: a suffix for the name of every applied object."),
		"patches":                 objects("Kustomization spec.patches: strategic merge and JSON patches applied to the built manifests.", "One patch (patch, target)."),
		"images":                  objects("Kustomization spec.images: image name, tag and digest replacements.", "One image replacement (name, newName, newTag, digest)."),
		"serviceAccountName":      str("Kustomization spec.serviceAccountName Flux impersonates when applying."),
		"sourceRef":               {Type: oam.PropertyTypeObject, Required: true, AdditionalProperties: true, Description: "Kustomization spec.sourceRef: the existing source to reconcile (apiVersion, kind, name, namespace). kind is one of OCIRepository, GitRepository, Bucket, ExternalArtifact; kind and name are required."},
		"suspend":                 boolean("Kustomization spec.suspend: stop reconciling."),
		"targetNamespace":         str("Kustomization spec.targetNamespace: sets or overrides the namespace of every namespaced object applied. Never defaulted, with or without a Flux namespace: unset, each object keeps the namespace the source's own kustomize build gives it."),
		"timeout":                 str("Kustomization spec.timeout for build, apply and health checks, as a Flux duration: unsigned, units ms, s, m, h; 0s or at least 1ms."),
		"force":                   boolean("Kustomization spec.force: recreate an object when a patch fails on an immutable field."),
		"wait":                    boolean("Kustomization spec.wait: wait for every applied object to become ready. healthChecks are ignored when true."),
		"buildMetadata":           strs("Kustomization spec.buildMetadata: kustomize build metadata to add to the built objects.", "originAnnotations or transformerAnnotations."),
		"components":              strs("Kustomization spec.components: relative paths of kustomize Components.", "One path."),
		"ignoreMissingComponents": boolean("Kustomization spec.ignoreMissingComponents: skip components paths missing from the source."),
		"healthCheckExprs":        objects("Kustomization spec.healthCheckExprs: custom CEL health checks.", "One custom health check."),
		"ignore":                  objects("Kustomization spec.ignore: changes drift detection and apply leave alone.", "One rule (paths, target)."),
	}
}

// namesUnknownKeyAt reports whether the strict decode's error names, as its
// unknown field, the key path ends in. encoding/json quotes the key alone, and
// a key may itself contain dots ("metadata.name"), so the key is whatever
// follows one of path's dots, or the whole of path.
func namesUnknownKeyAt(err error, path string) bool {
	msg := err.Error()
	for key := path; ; {
		if strings.Contains(msg, "unknown field "+strconv.Quote(key)) {
			return true
		}
		dot := strings.Index(key, ".")
		if dot < 0 {
			return false
		}
		key = key[dot+1:]
	}
}

// ToApplicationConfig decodes the component's properties strictly into a
// kustv1.KustomizationSpec. Any key KustomizationSpec does not declare, at any
// depth, and any wrongly typed value is an error. Checks: sourceRef names a
// kind Flux admits and a name. Every other constraint is left to Flux's own CRD
// admission.
func (h *FluxcdKustomizationHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	spec, _, err := builtin.DecodeStrictJSON[kustv1.KustomizationSpec](component.Properties)
	if err != nil {
		// encoding/json names an unknown key without its path, and
		// KustomizationSpec declares the same key in several places (kind, name,
		// namespace), so the path is added.
		if path := builtin.UnknownJSONFieldPath[kustv1.KustomizationSpec](component.Properties); path != "" && namesUnknownKeyAt(err, path) {
			return nil, errors.Errorf("%s: properties do not decode as a KustomizationSpec: unknown field %q", fluxcdKustomizationType, path)
		}
		return nil, errors.Errorf("%s: properties do not decode as a KustomizationSpec: %w", fluxcdKustomizationType, err)
	}
	if err := checkAuthoredFluxDurations(fluxcdKustomizationType, component.Properties, fluxcdKustomizationDurations); err != nil {
		return nil, err
	}
	cfg := &FluxcdKustomizationConfig{
		Name:      component.Name,
		Namespace: namespace,
		Spec:      *spec,
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// FluxcdKustomizationConfig implements stack.ApplicationConfig for
// fluxcd-kustomization components.
type FluxcdKustomizationConfig struct {
	// Name is the component name, and the Kustomization's name.
	Name string
	// Namespace is the application namespace. The Kustomization lands here
	// unless a Flux namespace is set (SetFluxNamespace).
	Namespace string

	// Spec is the Kustomization spec as authored. Generate copies it and
	// applies the interval default to that copy.
	Spec kustv1.KustomizationSpec

	// fluxNS overrides the Kustomization's namespace. Set by
	// postProcessFluxNamespace via TransformContext.FluxNamespace. Empty means
	// Namespace.
	fluxNS string
}

// ApplyPolicy is a no-op: a Kustomization pulls nothing itself. sourceRef is a
// reference to a source, and the allowed-registries policy is enforced on the
// source component that names the registry.
func (c *FluxcdKustomizationConfig) ApplyPolicy(_ oam.Policy) error { return nil }

// SetFluxNamespace moves the Kustomization to ns. Satisfies
// pkg/oam.fluxNamespaceSettable.
func (c *FluxcdKustomizationConfig) SetFluxNamespace(ns string) { c.fluxNS = ns }

// fluxNamespace returns the namespace the Kustomization lands in.
func (c *FluxcdKustomizationConfig) fluxNamespace() string {
	if c.fluxNS != "" {
		return c.fluxNS
	}
	return c.Namespace
}

// validate holds the checks shared by the parse path and Generate. Generate
// repeats them because this type and its fields are exported: a config built
// directly by a library caller never went through ToApplicationConfig.
func (c *FluxcdKustomizationConfig) validate() error {
	kinds := strings.Join(fluxcdKustomizationSourceKinds, ", ")
	ref := c.Spec.SourceRef
	if ref.Kind == "" {
		return errors.Errorf("%s: sourceRef.kind is required: one of %s", fluxcdKustomizationType, kinds)
	}
	if !slices.Contains(fluxcdKustomizationSourceKinds, ref.Kind) {
		return errors.Errorf("%s: sourceRef.kind %q is not one of %s", fluxcdKustomizationType, ref.Kind, kinds)
	}
	if ref.Name == "" {
		return errors.Errorf("%s: sourceRef.name is required", fluxcdKustomizationType)
	}
	return checkFluxDurations(fluxcdKustomizationType, &c.Spec, fluxcdKustomizationDurations)
}

// Generate emits the Kustomization, in the Flux namespace when one is set, else
// in the application namespace.
func (c *FluxcdKustomizationConfig) Generate(_ *stack.Application) ([]*client.Object, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}

	kz := fluxcd.CreateKustomization(c.Name, c.fluxNamespace())
	// A deep copy, so neither this render nor a later one shares a pointer or
	// slice with the config.
	kz.Spec = *c.Spec.DeepCopy()
	if kz.Spec.Interval.Duration == 0 {
		kz.Spec.Interval = metav1.Duration{Duration: fluxcdKustomizationDefaultInterval}
	}
	// No targetNamespace default under a Flux namespace, unlike helmrelease: a
	// Kustomization's targetNamespace overrides the namespace of every object
	// it applies (go-kure/launcher#622).

	obj := client.Object(kz)
	return []*client.Object{&obj}, nil
}
