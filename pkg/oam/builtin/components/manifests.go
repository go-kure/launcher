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

func (h *ManifestsHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	src, err := parseManifestSource(component.Properties)
	if err != nil {
		return nil, errors.Errorf("manifests component %q: %w", component.Name, err)
	}
	cfg := &manifestConfig{name: component.Name, namespace: namespace, src: src, process: stampManifestNamespaces}
	if err := cfg.validateInline(); err != nil {
		return nil, errors.Errorf("manifests component %q: %w", component.Name, err)
	}
	return cfg, nil
}

// stampManifestNamespaces resolves each object's scope (built-in maps, plus the
// scope of any CRD in the same source) and stamps the app namespace on
// namespaced objects that omit one. Cluster-scoped objects are left untouched;
// an unknown-scope object with no namespace fails closed rather than be guessed.
func stampManifestNamespaces(namespace string, objs []client.Object) ([]client.Object, error) {
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
		switch manifest.Scope(o, crdScopes) {
		case manifest.ScopeNamespaced:
			if o.GetNamespace() == "" {
				o.SetNamespace(namespace)
			}
		case manifest.ScopeUnknown:
			if o.GetNamespace() == "" {
				gvk := o.GetObjectKind().GroupVersionKind()
				return nil, errors.Errorf("object %s %q has unknown scope and no metadata.namespace; set an explicit namespace (no CRD defining it is present in this source)", gvk.Kind, o.GetName())
			}
		case manifest.ScopeCluster:
			// cluster-scoped: leave as-is
		}
	}
	return objs, nil
}
