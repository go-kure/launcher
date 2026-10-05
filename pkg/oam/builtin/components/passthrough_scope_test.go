package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The kinds the scope tests are written over, one per way a scope can be
// known. kure's scope table holds the first four: the two built-ins with the
// scope the Kubernetes API defines, the two cert-manager kinds with the scope
// their module declared when kure pinned it. The last is in no table.
const (
	builtinClusterKind    = "PriorityClass"
	builtinNamespacedKind = "ConfigMap"
	customClusterKind     = "ClusterIssuer"
	customNamespacedKind  = "Certificate"
	unknownKind           = "Widget"
)

// scopeObject returns a minimal object of one of the kinds above, with
// metadata.namespace set when namespace is not empty.
func scopeObject(kind, namespace string) map[string]any {
	apiVersions := map[string]string{
		builtinClusterKind:         "scheduling.k8s.io/v1",
		builtinNamespacedKind:      "v1",
		customClusterKind:          "cert-manager.io/v1",
		customNamespacedKind:       "cert-manager.io/v1",
		unknownKind:                "example.com/v1",
		"CustomResourceDefinition": "apiextensions.k8s.io/v1",
		"Namespace":                "v1",
	}
	object := map[string]any{"apiVersion": apiVersions[kind], "kind": kind}
	if namespace != "" {
		object["metadata"] = map[string]any{"namespace": namespace}
	}
	return object
}

// scopeProps returns passthrough properties for object, with clusterScoped
// authored when clusterScoped is not nil.
func scopeProps(object map[string]any, clusterScoped *bool) map[string]any {
	props := map[string]any{"object": object}
	if clusterScoped != nil {
		props["clusterScoped"] = *clusterScoped
	}
	return props
}

// TestPassthrough_Scope_Resolution pins who decides the scope of the object,
// for each kind and each state of clusterScoped: the namespace the emitted
// object carries ("data", the build namespace, when it is namespaced, none
// when it is cluster-scoped), or the refusal.
func TestPassthrough_Scope_Resolution(t *testing.T) {
	yes, no := true, false
	const stamped, bare = "data", ""
	cases := []struct {
		name          string
		kind          string
		clusterScoped *bool
		wantNamespace string
		wantErr       string
	}{
		// A kind whose scope the Kubernetes API defines: the table decides, and
		// the property may only agree.
		{name: "built-in cluster kind, not authored", kind: builtinClusterKind, wantNamespace: bare},
		{name: "built-in cluster kind, true", kind: builtinClusterKind, clusterScoped: &yes, wantNamespace: bare},
		{
			name: "built-in cluster kind, false", kind: builtinClusterKind, clusterScoped: &no,
			wantErr: `passthrough component "my-res": clusterScoped is false, but the Kubernetes API defines scheduling.k8s.io/v1 PriorityClass as cluster-scoped; remove clusterScoped`,
		},
		{name: "Namespace, not authored", kind: "Namespace", wantNamespace: bare},
		{name: "CustomResourceDefinition, not authored", kind: "CustomResourceDefinition", wantNamespace: bare},
		{
			name: "CustomResourceDefinition, false", kind: "CustomResourceDefinition", clusterScoped: &no,
			wantErr: `passthrough component "my-res": clusterScoped is false, but the Kubernetes API defines apiextensions.k8s.io/v1 CustomResourceDefinition as cluster-scoped; remove clusterScoped`,
		},
		{name: "built-in namespaced kind, not authored", kind: builtinNamespacedKind, wantNamespace: stamped},
		{name: "built-in namespaced kind, false", kind: builtinNamespacedKind, clusterScoped: &no, wantNamespace: stamped},
		{
			name: "built-in namespaced kind, true", kind: builtinNamespacedKind, clusterScoped: &yes,
			wantErr: `passthrough component "my-res": clusterScoped is true, but the Kubernetes API defines v1 ConfigMap as namespaced; remove clusterScoped`,
		},

		// A custom resource kure registers: the author decides when the
		// property is authored, either way; the table decides when it is not.
		{name: "registered cluster kind, not authored", kind: customClusterKind, wantNamespace: bare},
		{name: "registered cluster kind, true", kind: customClusterKind, clusterScoped: &yes, wantNamespace: bare},
		{name: "registered cluster kind, false", kind: customClusterKind, clusterScoped: &no, wantNamespace: stamped},
		{name: "registered namespaced kind, not authored", kind: customNamespacedKind, wantNamespace: stamped},
		{name: "registered namespaced kind, false", kind: customNamespacedKind, clusterScoped: &no, wantNamespace: stamped},
		{name: "registered namespaced kind, true", kind: customNamespacedKind, clusterScoped: &yes, wantNamespace: bare},

		// A kind no table knows: the author decides, and unset it is namespaced.
		{name: "unknown kind, not authored", kind: unknownKind, wantNamespace: stamped},
		{name: "unknown kind, false", kind: unknownKind, clusterScoped: &no, wantNamespace: stamped},
		{name: "unknown kind, true", kind: unknownKind, clusterScoped: &yes, wantNamespace: bare},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := scopeProps(scopeObject(tc.kind, ""), tc.clusterScoped)
			if tc.wantErr != "" {
				_, err := (&components.PassthroughHandler{}).ToApplicationConfig(passthroughComponent(props), "data")
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("ToApplicationConfig error:\n got: %v\nwant: %s", err, tc.wantErr)
				}
				return
			}
			u := generatePassthrough(t, props, "data")
			if got := u.GetNamespace(); got != tc.wantNamespace {
				t.Errorf("metadata.namespace = %q, want %q", got, tc.wantNamespace)
			}
			if _, has := u.Object["metadata"].(map[string]any)["namespace"]; has != (tc.wantNamespace != "") {
				t.Errorf("metadata.namespace present = %v, want %v", has, tc.wantNamespace != "")
			}
		})
	}
}

// A null clusterScoped is the property not authored, so it is no explicit
// false: a built-in cluster-scoped kind is not refused over it and gets no
// namespace, and a registered cluster-scoped custom resource follows the table.
func TestPassthrough_Scope_NullIsNotAuthored(t *testing.T) {
	for _, kind := range []string{builtinClusterKind, customClusterKind} {
		t.Run(kind, func(t *testing.T) {
			u := generatePassthrough(t, map[string]any{"clusterScoped": nil, "object": scopeObject(kind, "")}, "data")
			if got := u.GetNamespace(); got != "" {
				t.Errorf("metadata.namespace = %q, want none", got)
			}
		})
	}
}

// An authored metadata.namespace on an object that resolves cluster-scoped is
// refused, whatever resolved it, with the reason named; on an object that
// resolves namespaced it is kept.
func TestPassthrough_Scope_AuthoredNamespace(t *testing.T) {
	yes, no := true, false
	const tail = `; the Kubernetes API rejects a namespace on a cluster-scoped object`
	refused := []struct {
		name          string
		kind          string
		clusterScoped *bool
		because       string
	}{
		{
			name: "the API defines the kind as cluster-scoped", kind: builtinClusterKind,
			because: `the Kubernetes API defines scheduling.k8s.io/v1 PriorityClass as cluster-scoped`,
		},
		{
			name: "the API defines the kind as cluster-scoped, true authored", kind: builtinClusterKind, clusterScoped: &yes,
			because: `the Kubernetes API defines scheduling.k8s.io/v1 PriorityClass as cluster-scoped`,
		},
		{
			name: "the kind is registered as cluster-scoped", kind: customClusterKind,
			because: `cert-manager.io/v1 ClusterIssuer is registered as cluster-scoped (set clusterScoped: false if the cluster serves it namespaced)`,
		},
		{name: "clusterScoped on a registered namespaced kind", kind: customNamespacedKind, clusterScoped: &yes, because: `clusterScoped is true`},
		{name: "clusterScoped on an unknown kind", kind: unknownKind, clusterScoped: &yes, because: `clusterScoped is true`},
	}
	for _, tc := range refused {
		t.Run("refused: "+tc.name, func(t *testing.T) {
			props := scopeProps(scopeObject(tc.kind, "other"), tc.clusterScoped)
			want := `passthrough component "my-res": object.metadata.namespace must not be set ("other"): ` + tc.because + tail
			_, err := (&components.PassthroughHandler{}).ToApplicationConfig(passthroughComponent(props), "data")
			if err == nil || err.Error() != want {
				t.Fatalf("ToApplicationConfig error:\n got: %v\nwant: %s", err, want)
			}
		})
	}

	kept := []struct {
		name          string
		kind          string
		clusterScoped *bool
	}{
		{name: "built-in namespaced kind", kind: builtinNamespacedKind},
		{name: "registered namespaced kind", kind: customNamespacedKind},
		{name: "registered cluster kind, false authored", kind: customClusterKind, clusterScoped: &no},
		{name: "unknown kind", kind: unknownKind},
	}
	for _, tc := range kept {
		t.Run("kept: "+tc.name, func(t *testing.T) {
			u := generatePassthrough(t, scopeProps(scopeObject(tc.kind, "other"), tc.clusterScoped), "data")
			if got := u.GetNamespace(); got != "other" {
				t.Errorf("metadata.namespace = %q, want the authored %q", got, "other")
			}
		})
	}
}

// A config that never went through ToApplicationConfig is held to the same
// resolution at Generate: ClusterScoped is an exported field, and its zero
// value states nothing, so the kind decides.
func TestPassthrough_Scope_ConfigBuiltDirectly(t *testing.T) {
	t.Run("zero value on a built-in cluster kind gets no namespace", func(t *testing.T) {
		cfg := &components.PassthroughConfig{Namespace: "data", Object: scopeObject(builtinClusterKind, "")}
		objs, err := cfg.Generate(nil)
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if got := (*objs[0]).GetNamespace(); got != "" {
			t.Errorf("metadata.namespace = %q, want none", got)
		}
	})
	t.Run("zero value on a registered cluster kind follows the table", func(t *testing.T) {
		cfg := &components.PassthroughConfig{Namespace: "data", Object: scopeObject(customClusterKind, "")}
		objs, err := cfg.Generate(nil)
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if got := (*objs[0]).GetNamespace(); got != "" {
			t.Errorf("metadata.namespace = %q, want none", got)
		}
	})
	t.Run("true on a built-in namespaced kind is refused", func(t *testing.T) {
		cfg := &components.PassthroughConfig{Namespace: "data", ClusterScoped: true, Object: scopeObject(builtinNamespacedKind, "")}
		const want = `passthrough component "": clusterScoped is true, but the Kubernetes API defines v1 ConfigMap as namespaced; remove clusterScoped`
		if _, err := cfg.Generate(nil); err == nil || err.Error() != want {
			t.Fatalf("Generate error:\n got: %v\nwant: %s", err, want)
		}
	})
	t.Run("true with a namespace is refused", func(t *testing.T) {
		cfg := &components.PassthroughConfig{Namespace: "data", ClusterScoped: true, Object: scopeObject(unknownKind, "other")}
		const want = `passthrough component "": object.metadata.namespace must not be set ("other"): clusterScoped is true; the Kubernetes API rejects a namespace on a cluster-scoped object`
		if _, err := cfg.Generate(nil); err == nil || err.Error() != want {
			t.Fatalf("Generate error:\n got: %v\nwant: %s", err, want)
		}
	})
}

// Through the transform: a document that passes a cluster-scoped built-in kind
// through without clusterScoped builds, and the object the bundle generates
// carries no namespace; an explicit false on it fails the build, naming the
// component.
func TestPassthrough_Scope_ThroughTheTransform(t *testing.T) {
	transform := func(props map[string]any) (*stack.Cluster, error) {
		tr := oam.NewTransformer(map[string]oam.ComponentHandler{"passthrough": &components.PassthroughHandler{}}, nil)
		app := &oam.Application{
			Metadata: oam.Metadata{Name: "app", Namespace: "data"},
			Spec: oam.ApplicationSpec{Components: []oam.Component{
				{Name: "batch-low", Type: "passthrough", Properties: props},
			}},
		}
		cluster, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Namespace: "data"})
		return cluster, err
	}

	t.Run("not authored", func(t *testing.T) {
		cluster, err := transform(scopeProps(scopeObject(builtinClusterKind, ""), nil))
		if err != nil {
			t.Fatalf("TransformWithPolicy: %v", err)
		}
		found := false
		walkBundles(cluster.Node, func(b *stack.Bundle) {
			for _, a := range b.Applications {
				objs, err := a.Generate()
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				for _, o := range objs {
					if (*o).GetObjectKind().GroupVersionKind().Kind != builtinClusterKind {
						continue
					}
					found = true
					if (*o).GetName() != "batch-low" || (*o).GetNamespace() != "" {
						t.Errorf("PriorityClass metadata: name=%q namespace=%q, want batch-low and no namespace", (*o).GetName(), (*o).GetNamespace())
					}
				}
			}
		})
		if !found {
			t.Error("the PriorityClass was not emitted by the transform")
		}
	})

	t.Run("false is refused", func(t *testing.T) {
		no := false
		_, err := transform(scopeProps(scopeObject(builtinClusterKind, ""), &no))
		const want = `passthrough component "batch-low": clusterScoped is false, but the Kubernetes API defines scheduling.k8s.io/v1 PriorityClass as cluster-scoped; remove clusterScoped`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("TransformWithPolicy error:\n got: %v\nwant it to contain: %s", err, want)
		}
	})
}

// A non-string metadata.namespace is not an authored namespace: on a
// namespaced object it is replaced by the build namespace, and on a
// cluster-scoped one it is neither refused nor replaced.
func TestPassthrough_Scope_NonStringNamespace(t *testing.T) {
	withNamespace := func(kind string) map[string]any {
		object := scopeObject(kind, "")
		object["metadata"] = map[string]any{"namespace": int64(7)}
		return object
	}
	t.Run("namespaced", func(t *testing.T) {
		u := generatePassthrough(t, scopeProps(withNamespace(unknownKind), nil), "data")
		if got := u.Object["metadata"].(map[string]any)["namespace"]; got != "data" {
			t.Errorf("metadata.namespace = %#v, want the build namespace", got)
		}
	})
	t.Run("cluster-scoped", func(t *testing.T) {
		u := generatePassthrough(t, scopeProps(withNamespace(builtinClusterKind), nil), "data")
		if got := u.Object["metadata"].(map[string]any)["namespace"]; got != int64(7) {
			t.Errorf("metadata.namespace = %#v, want it left as authored", got)
		}
	})
}
