package components

import (
	"github.com/go-kure/kure/pkg/manifest"
	"github.com/go-kure/kure/pkg/stack"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// ManifestsHandler handles the `manifests` OAM component type: it emits arbitrary
// Kubernetes manifests from an inline or url source. CRDs in the payload are
// auto-staged early by stack-compile's CRD inference.
type ManifestsHandler struct{}

func (h *ManifestsHandler) CanHandle(componentType string) bool { return componentType == "manifests" }

// PropertySchema declares the manifests component's properties. Exactly one of
// `inline` (raw multi-doc YAML) / `url` is required (enforced in
// parseManifestSource); `scopeOverrides` supplies scope for unknown kinds.
func (h *ManifestsHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"inline": {Type: oam.PropertyTypeString, Description: "Raw multi-document manifest YAML emitted inline (mutually exclusive with url)."},
		"url":    {Type: oam.PropertyTypeString, Description: "URL of the manifest YAML source (mutually exclusive with inline)."},
		"scopeOverrides": {
			Type:        oam.PropertyTypeArray,
			Description: "Explicit scope entries for kinds whose scope is otherwise unknown.",
			Items: &oam.PropertySchema{
				Type:        oam.PropertyTypeObject,
				Description: "A single scope override for one apiVersion/kind.",
				Properties: map[string]oam.PropertySchema{
					"apiVersion": {Type: oam.PropertyTypeString, Required: true, Description: "API version of the kind whose scope is being overridden."},
					"kind":       {Type: oam.PropertyTypeString, Required: true, Description: "Kind whose scope is being overridden."},
					"scope":      {Type: oam.PropertyTypeString, Required: true, Enum: []any{"Cluster", "Namespaced"}, Description: "Whether the kind is cluster-scoped or namespaced."},
				},
			},
		},
	}
}

func (h *ManifestsHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	overrides, srcProps, err := parseScopeOverrides(component.Properties)
	if err != nil {
		return nil, errors.Errorf("manifests component %q: %w", component.Name, err)
	}
	src, err := parseManifestSource(srcProps)
	if err != nil {
		return nil, errors.Errorf("manifests component %q: %w", component.Name, err)
	}
	cfg := &manifestConfig{name: component.Name, namespace: namespace, src: src, process: stampManifestNamespaces(overrides)}
	if err := cfg.validateInline(); err != nil {
		return nil, errors.Errorf("manifests component %q: %w", component.Name, err)
	}
	return cfg, nil
}

// parseScopeOverrides extracts the optional `scopeOverrides` property and returns
// the parsed overrides plus the remaining properties. It splits the property out
// so the shared parseManifestSource (which rejects unknown keys, and is also used
// by the crd component) never sees it. Each entry is {apiVersion, kind, scope}
// where scope is "Cluster" or "Namespaced"; an override only takes effect for a
// kind whose scope is otherwise unknown (no built-in mapping and no CRD in the
// same source).
func parseScopeOverrides(props map[string]any) (map[schema.GroupVersionKind]manifest.ScopeResult, map[string]any, error) {
	raw, ok := props["scopeOverrides"]
	if !ok {
		return nil, props, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, nil, errors.Errorf("scopeOverrides must be a list of {apiVersion, kind, scope} objects")
	}
	overrides := make(map[schema.GroupVersionKind]manifest.ScopeResult, len(list))
	for i, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			return nil, nil, errors.Errorf("scopeOverrides[%d]: expected an object", i)
		}
		apiVersion, _ := m["apiVersion"].(string)
		kind, _ := m["kind"].(string)
		scopeStr, _ := m["scope"].(string)
		if apiVersion == "" || kind == "" {
			return nil, nil, errors.Errorf("scopeOverrides[%d]: apiVersion and kind are required", i)
		}
		var scope manifest.ScopeResult
		switch scopeStr {
		case "Cluster":
			scope = manifest.ScopeCluster
		case "Namespaced":
			scope = manifest.ScopeNamespaced
		default:
			return nil, nil, errors.Errorf("scopeOverrides[%d]: scope %q is invalid; must be \"Cluster\" or \"Namespaced\"", i, scopeStr)
		}
		overrides[schema.FromAPIVersionAndKind(apiVersion, kind)] = scope
	}
	srcProps := make(map[string]any, len(props))
	for k, v := range props {
		if k == "scopeOverrides" {
			continue
		}
		srcProps[k] = v
	}
	return overrides, srcProps, nil
}

// stampManifestNamespaces returns the per-type process hook that resolves each
// object's scope (built-in maps, plus the scope of any CRD in the same source)
// and stamps the app namespace on namespaced objects that omit one. Cluster-scoped
// objects are left untouched; an unknown-scope object with no namespace fails
// closed rather than be guessed.
//
// overrides supplies an explicit scope for kinds whose scope is otherwise unknown
// — e.g. a cluster-scoped custom resource (a namespace-less ClusterIssuer) whose
// CRD is installed out of band rather than bundled in this source. Overrides are
// consulted only for ScopeUnknown objects, so they can never silently contradict
// a known built-in or same-source-CRD scope; the fail-closed default is preserved
// for unknown kinds with no override.
func stampManifestNamespaces(overrides map[schema.GroupVersionKind]manifest.ScopeResult) func(string, []client.Object) ([]client.Object, error) {
	return func(namespace string, objs []client.Object) ([]client.Object, error) {
		if len(objs) == 0 {
			return nil, errors.Errorf("source resolved to no manifests")
		}
		crdScopes := map[schema.GroupKind]apiextv1.ResourceScope{}
		for _, o := range objs {
			if gk, scope, ok := manifest.CRDScope(o); ok {
				crdScopes[gk] = scope
			}
		}
		for _, o := range objs {
			scope := manifest.Scope(o, crdScopes)
			if scope == manifest.ScopeUnknown {
				if ov, ok := overrides[o.GetObjectKind().GroupVersionKind()]; ok {
					scope = ov
				}
			}
			switch scope {
			case manifest.ScopeNamespaced:
				if o.GetNamespace() == "" {
					o.SetNamespace(namespace)
				}
			case manifest.ScopeUnknown:
				if o.GetNamespace() == "" {
					gvk := o.GetObjectKind().GroupVersionKind()
					return nil, errors.Errorf("object %s %q has unknown scope and no metadata.namespace; set an explicit namespace or a scopeOverrides entry (no CRD defining it is present in this source)", gvk.Kind, o.GetName())
				}
			case manifest.ScopeCluster:
				// cluster-scoped: leave as-is
			}
		}
		return objs, nil
	}
}
