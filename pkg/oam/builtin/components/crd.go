package components

import (
	"github.com/go-kure/kure/pkg/manifest"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// CRDHandler handles the `crd` OAM component type: it emits CustomResourceDefinition
// manifests from an inline or url source and rejects any non-CRD document. The
// emitted CRDs are auto-staged early by stack-compile's CRD inference.
type CRDHandler struct{}

func (h *CRDHandler) CanHandle(componentType string) bool { return componentType == "crd" }

// PropertySchema declares the crd component's properties. Exactly one of
// `inline` (raw CRD YAML) / `url` is required — the one-of rule is enforced in
// parseManifestSource and is not expressible in the schema vocabulary.
func (h *CRDHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"inline": {Type: oam.PropertyTypeString, Description: "Raw CRD YAML emitted inline (mutually exclusive with url)."},
		"url":    {Type: oam.PropertyTypeString, Description: "URL of the CRD YAML source (mutually exclusive with inline)."},
	}
}

func (h *CRDHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	src, err := parseManifestSource(component.Properties)
	if err != nil {
		return nil, errors.Errorf("crd component %q: %w", component.Name, err)
	}
	cfg := &manifestConfig{name: component.Name, namespace: namespace, src: src, process: requireAllCRDs}
	if err := cfg.validateInline(); err != nil {
		return nil, errors.Errorf("crd component %q: %w", component.Name, err)
	}
	return cfg, nil
}

// requireAllCRDs fails closed if any resolved object is not a CRD, and rejects
// a CRD document that authors metadata.namespace: a CustomResourceDefinition is
// always cluster-scoped, and the Kubernetes API rejects a namespace on a
// cluster-scoped object (apimachinery's ValidateObjectMetaAccessor: "not
// allowed on this type"), so an authored namespace here would only defer that
// failure to apply time with a less legible error. Same reasoning and same
// rejection shape as stampManifestNamespaces' manifest.ScopeCluster case
// (manifests.go), for the same object kind emitted through the sibling `crd`
// component.
func requireAllCRDs(_ string, objs []client.Object) ([]client.Object, error) {
	if len(objs) == 0 {
		return nil, errors.Errorf("source resolved to no manifests")
	}
	for _, o := range objs {
		if !manifest.IsCRD(o) {
			gvk := o.GetObjectKind().GroupVersionKind()
			return nil, errors.Errorf("object %s %q is not a CustomResourceDefinition (the crd component emits only CRDs; use the manifests component for other kinds)", gvk.Kind, o.GetName())
		}
		if ns := o.GetNamespace(); ns != "" {
			return nil, errors.Errorf("object %s %q: a CustomResourceDefinition is cluster-scoped and must not carry metadata.namespace %q", o.GetObjectKind().GroupVersionKind().Kind, o.GetName(), ns)
		}
	}
	return objs, nil
}
