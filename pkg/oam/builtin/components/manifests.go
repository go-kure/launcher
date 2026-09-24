package components

import (
	"fmt"

	"github.com/go-kure/kure/pkg/kubernetes"
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
// parseManifestSource); `scopeOverrides` states a kind's scope explicitly and
// outranks kure's own table, but not the Kubernetes API's own scoping
// (isAPIGovernedScope) and not a CRD bundled in the same source, which it may
// not contradict (stampManifestNamespaces).
func (h *ManifestsHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"inline": {Type: oam.PropertyTypeString, Description: "Raw multi-document manifest YAML emitted inline (mutually exclusive with url)."},
		"url":    {Type: oam.PropertyTypeString, Description: "URL of the manifest YAML source (mutually exclusive with inline)."},
		"scopeOverrides": {
			Type:        oam.PropertyTypeArray,
			Description: "Explicit scope entries, taking precedence over kure's own guess (not over a kind the Kubernetes API itself scopes; contradicting a CRD in this same source is an error).",
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
// where scope is "Cluster" or "Namespaced"; an override takes effect for any
// kind except one whose scope the Kubernetes API itself governs (see
// isAPIGovernedScope) and one a CRD in the same source defines, which it must
// agree with rather than override (see stampManifestNamespaces).
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
		label := fmt.Sprintf("scopeOverrides[%d]", i)
		apiVersion, err := requiredStringField(m, "apiVersion", label)
		if err != nil {
			return nil, nil, err
		}
		kind, err := requiredStringField(m, "kind", label)
		if err != nil {
			return nil, nil, err
		}
		scopeStr, _ := m["scope"].(string)
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
// overrides supplies an explicit scope for a kind, taking precedence over
// kure's own non-API-governed table (e.g. a cluster-scoped custom resource — a
// namespace-less ClusterIssuer — whose CRD is installed out of band rather than
// bundled in this source). This preserves scopeOverrides' documented meaning as
// the author's explicit statement: it must not be silently outvoted by a kure
// built-in guess. Two things still outrank it, and for the same reason — they
// describe what the cluster will actually serve, which an override cannot
// change:
//
//   - a kind whose scope the Kubernetes API itself governs (isAPIGovernedScope);
//     a manifest cannot redefine that, override or not, so such an override is
//     ignored.
//   - a CustomResourceDefinition bundled in this same source. It *is* the
//     definition being applied, so an override that disagrees with it cannot be
//     honoured by any cluster: emitting either shape would produce a manifest
//     that lands somewhere the author did not ask for (a Cluster override on a
//     Namespaced CRD drops the namespace, and the object is then created in
//     whatever namespace the applying client defaults to). Kure's own
//     manifest.Scope ranks a same-context CRD above its table for this reason
//     ("the CRD names the scope the target cluster will actually serve").
//     Rather than silently discard one of the two statements, a disagreement is
//     rejected here with both values named.
//
// The fail-closed default for a kind with no override and no other scope source
// is unchanged.
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
			gvk := o.GetObjectKind().GroupVersionKind()
			scope, overridden := overrides[gvk]
			if !overridden || isAPIGovernedScope(o) {
				scope = manifest.Scope(o, crdScopes)
			} else if declared, defined := crdScopes[gvk.GroupKind()]; defined && crdDeclaredScope(declared) != scope {
				return nil, errors.Errorf("object %s %q: scopeOverrides says %s but the CustomResourceDefinition for %s in this source declares %s; the bundled CRD defines the scope the cluster will serve, so drop the override or correct the CRD", gvk.Kind, o.GetName(), scopeName(scope), gvk.GroupKind().String(), declared)
			}
			switch scope {
			case manifest.ScopeNamespaced:
				if o.GetNamespace() == "" {
					o.SetNamespace(namespace)
				}
			case manifest.ScopeUnknown:
				if o.GetNamespace() == "" {
					return nil, errors.Errorf("object %s %q has unknown scope and no metadata.namespace; set an explicit namespace or a scopeOverrides entry (no CRD defining it is present in this source)", gvk.Kind, o.GetName())
				}
			case manifest.ScopeCluster:
				if ns := o.GetNamespace(); ns != "" {
					return nil, errors.Errorf("object %s %q: scope is Cluster but the manifest carries metadata.namespace %q; the Kubernetes API rejects a namespace on a cluster-scoped object (apimachinery's ValidateObjectMetaAccessor: \"not allowed on this type\"), so remove the namespace from the source or correct the scope", gvk.Kind, o.GetName(), ns)
				}
			}
		}
		return objs, nil
	}
}

// crdDeclaredScope maps a CRD's declared spec.scope onto the ScopeResult an
// override is expressed in, so the two can be compared. manifest.CRDScope has
// already applied Kubernetes' own default for an absent spec.scope
// (NamespaceScoped), so anything that is not ClusterScoped is namespaced.
func crdDeclaredScope(s apiextv1.ResourceScope) manifest.ScopeResult {
	if s == apiextv1.ClusterScoped {
		return manifest.ScopeCluster
	}
	return manifest.ScopeNamespaced
}

// scopeName renders a ScopeResult with the spelling scopeOverrides uses, for
// error messages that quote the author's own value back at them. Only the two
// values parseScopeOverrides accepts can reach it.
func scopeName(s manifest.ScopeResult) string {
	if s == manifest.ScopeCluster {
		return "Cluster"
	}
	return "Namespaced"
}

// isAPIGovernedScope reports whether o's scope is fixed by the Kubernetes API
// itself, not by any manifest or table: a CustomResourceDefinition document is
// always cluster-scoped, and a kind kure's table sources from
// kubernetes.ScopeSourceBuiltin has its scope defined by the generated
// upstream types, not by kure's own guess. Both are non-negotiable — a
// scopeOverrides entry naming one of these kinds is ignored, matching the
// same reasoning manifest.Scope itself documents for ScopeSourceBuiltin.
func isAPIGovernedScope(o client.Object) bool {
	if manifest.IsCRD(o) {
		return true
	}
	gvk := o.GetObjectKind().GroupVersionKind()
	k, registered := kubernetes.KindForAnyVersion(gvk.GroupVersion().String(), gvk.Kind)
	if registered {
		return k.ScopeSource == kubernetes.ScopeSourceBuiltin
	}
	// Kure fixes the scope of a few cluster-scoped API built-ins it registers no
	// builders for — PriorityClass, APIService and the two webhook
	// configurations. manifest.Scope's own documentation puts them under the
	// same rule this function implements ("The Kubernetes API defines those
	// scopes and no manifest can redefine them"), but they are absent from the
	// generated table, so KindForAnyVersion does not report them and the check
	// above alone would let a scopeOverrides entry stamp a namespace onto one.
	// Kure keeps that set unexported, so ask manifest.Scope itself with no CRD
	// context: for an unregistered non-CRD object every other arm of Scope needs
	// either registration or a crdScopes entry, so ScopeCluster can only have
	// come from that residual set. Probing instead of copying the list keeps
	// kure the single answer — the set only shrinks, and a kind that gains a
	// builder moves to the registered branch above on its own.
	return manifest.Scope(o, nil) == manifest.ScopeCluster
}
