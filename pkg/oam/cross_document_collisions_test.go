package oam

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func collisionObject(obj client.Object) *client.Object { return new(obj) }

func configMap(namespace, name string) *client.Object {
	return collisionObject(&corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
	})
}

func generatedDoc(namespace, name string, objects ...*client.Object) GeneratedDocument {
	return GeneratedDocument{Namespace: namespace, Name: name, Kind: "WebApplication", Objects: objects}
}

func TestCheckCrossDocumentCollisions(t *testing.T) {
	deployment := func(apiVersion, namespace, name string) *client.Object {
		return collisionObject(&appsv1.Deployment{
			TypeMeta:   metav1.TypeMeta{APIVersion: apiVersion, Kind: "Deployment"},
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		})
	}
	clusterRole := func(name string) *client.Object {
		return collisionObject(&rbacv1.ClusterRole{
			TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRole"},
			ObjectMeta: metav1.ObjectMeta{Name: name},
		})
	}
	cases := []struct {
		name    string
		docs    []GeneratedDocument
		wantErr []string
	}{
		{
			name: "same object in one namespace from two documents",
			docs: []GeneratedDocument{
				generatedDoc("prod", "shop", configMap("prod", "shop"), configMap("prod", "shared-source")),
				generatedDoc("prod", "cart", configMap("prod", "cart"), configMap("prod", "shared-source")),
			},
			wantErr: []string{`ConfigMap "prod/shared-source"`, `WebApplication "prod/shop"`, `WebApplication "prod/cart"`},
		},
		{
			name: "same name in two namespaces",
			docs: []GeneratedDocument{
				generatedDoc("prod", "shop", configMap("prod", "shared-source")),
				generatedDoc("stage", "shop", configMap("stage", "shared-source")),
			},
		},
		{
			name: "same name, different kinds",
			docs: []GeneratedDocument{
				generatedDoc("prod", "shop", configMap("prod", "web")),
				generatedDoc("prod", "cart", deployment("apps/v1", "prod", "web")),
			},
		},
		{
			name: "same group and kind under two versions",
			docs: []GeneratedDocument{
				generatedDoc("prod", "shop", deployment("apps/v1", "prod", "web")),
				generatedDoc("prod", "cart", deployment("apps/v1beta2", "prod", "web")),
			},
			wantErr: []string{`Deployment.apps "prod/web"`},
		},
		{
			name: "cluster-scoped object from documents in two namespaces",
			docs: []GeneratedDocument{
				generatedDoc("prod", "shop", clusterRole("reader")),
				generatedDoc("stage", "cart", clusterRole("reader")),
			},
			wantErr: []string{`ClusterRole.rbac.authorization.k8s.io "reader"`, `WebApplication "prod/shop"`, `WebApplication "stage/cart"`},
		},
		{
			name: "one object emitted once within one document",
			docs: []GeneratedDocument{
				generatedDoc("prod", "shop", configMap("prod", "shared-source"), configMap("prod", "shop")),
				generatedDoc("prod", "cart", configMap("prod", "cart")),
			},
		},
		{
			name: "an object repeated within one document is left to the allocator",
			docs: []GeneratedDocument{
				generatedDoc("prod", "shop", configMap("prod", "a"), configMap("prod", "a")),
				generatedDoc("prod", "cart", configMap("prod", "b")),
			},
		},
		{
			name: "every collision is reported",
			docs: []GeneratedDocument{
				generatedDoc("prod", "shop", configMap("prod", "a"), configMap("prod", "b")),
				generatedDoc("prod", "cart", configMap("prod", "a"), configMap("prod", "b")),
			},
			wantErr: []string{"2 generated objects", `ConfigMap "prod/a"`, `ConfigMap "prod/b"`},
		},
		{
			name: "an object without a kind is refused",
			docs: []GeneratedDocument{
				generatedDoc("prod", "shop", collisionObject(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "x"}})),
			},
			wantErr: []string{`WebApplication "prod/shop"`, `object "prod/x" has no kind`},
		},
		{
			name: "one document listed twice is refused",
			docs: []GeneratedDocument{
				generatedDoc("prod", "shop", configMap("prod", "a")),
				generatedDoc("prod", "shop", configMap("prod", "b")),
			},
			wantErr: []string{`WebApplication "prod/shop" is listed more than once`},
		},
		{
			name: "nil objects are skipped",
			docs: []GeneratedDocument{
				generatedDoc("prod", "shop", nil, configMap("prod", "a")),
				generatedDoc("prod", "cart", configMap("prod", "b")),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckCrossDocumentCollisions(tc.docs)
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error containing %q, got none", tc.wantErr)
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

// namedConfigMapHandler generates one ConfigMap per stack application, named after
// the application, so a component's generated name becomes an object identity.
type namedConfigMapHandler struct{ typ string }

func (h *namedConfigMapHandler) CanHandle(t string) bool { return t == h.typ }

func (h *namedConfigMapHandler) ToApplicationConfig(c *Component, namespace string) (stack.ApplicationConfig, error) {
	return &namedConfigMapConfig{name: c.Name, namespace: namespace}, nil
}

type namedConfigMapConfig struct{ name, namespace string }

func (c *namedConfigMapConfig) Generate(_ *stack.Application) ([]*client.Object, error) {
	return []*client.Object{configMap(c.namespace, c.name)}, nil
}

// TestCheckCrossDocumentCollisions_AfterTransform is the case the helper exists for:
// two same-namespace documents whose in-transform rules each emit "shared-source"
// through EmitOrAdopt. Each Transform runs with its own allocator, so both succeed;
// the helper, given both documents' generated objects, reports the collision.
func TestCheckCrossDocumentCollisions_AfterTransform(t *testing.T) {
	tr := NewTransformer(map[string]ComponentHandler{
		"webservice": &namedConfigMapHandler{typ: "webservice"},
		"worker":     &namedConfigMapHandler{typ: "worker"},
	}, nil)
	tr.RegisterRawDocumentLowering(testRawRule{kind: "WebApplication", compType: "image-source-user"})
	tr.RegisterComponentLowering(imageSourceRule{})

	out, err := tr.LowerRaws([]json.RawMessage{rawWebApplication("shop"), rawWebApplication("cart")}, TransformContext{})
	if err != nil {
		t.Fatalf("LowerRaws: %v", err)
	}
	var docs []GeneratedDocument
	for _, raw := range out {
		app := parseLoweredOutput(t, tr, raw)
		cluster, err := tr.Transform(app, TransformContext{Namespace: "prod"})
		if err != nil {
			t.Fatalf("Transform %q: %v", app.Metadata.Name, err)
		}
		doc := GeneratedDocument{Namespace: app.Metadata.Namespace, Name: app.Metadata.Name, Kind: app.Kind}
		for _, a := range cluster.Node.Bundle.Applications {
			objs, err := a.Config.Generate(a)
			if err != nil {
				t.Fatalf("Generate %q: %v", a.Name, err)
			}
			doc.Objects = append(doc.Objects, objs...)
		}
		docs = append(docs, doc)
	}
	err = CheckCrossDocumentCollisions(docs)
	if err == nil {
		t.Fatal("two documents emitting shared-source in one namespace were not reported")
	}
	// The raw rule emits documents "shop-1" and "cart-1", each also carrying a leaf
	// component named "web" — a second, authored-name collision the helper reports too.
	for _, want := range []string{
		`ConfigMap "prod/shared-source" is generated by both Application "shop-1" and Application "cart-1"`,
		`ConfigMap "prod/web" is generated by both`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %s", err, want)
		}
	}
}
