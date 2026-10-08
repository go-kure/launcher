package components_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The apiservice kind (go-kure/launcher#943) is no row of policyFreeKinds:
// the API requires an APIService to be named <version>.<group>, which the
// table's shared names ("web", "fast", the Naming hook's "hooked-…") never
// are. This file holds what that table holds of its rows, for this kind.

// apiServiceName is the name the API requires of apiServiceProps' object.
const apiServiceName = "v1beta1.metrics.example.com"

// apiServiceProps is an APIService served by a Service, with fields set over
// it.
func apiServiceProps(fields map[string]any) map[string]any {
	props := map[string]any{
		"group": "metrics.example.com", "version": "v1beta1",
		"groupPriorityMinimum": 100, "versionPriority": 10,
		"service": map[string]any{"namespace": "metrics", "name": "metrics-server"},
	}
	for k, v := range fields {
		if v == nil {
			delete(props, k)
			continue
		}
		props[k] = v
	}
	return props
}

// clusterWideTransform transforms a document of one component, web, of the
// given type and properties, under policy and the Naming hook naming (nil for
// none), with that type's handler alone registered, and returns every
// generated object.
func clusterWideTransform(typ string, h oam.ComponentHandler, props map[string]any, policy oam.Policy, naming func(oam.NameRequest) (string, bool)) ([]client.Object, error) {
	app := &oam.Application{Metadata: oam.Metadata{Name: "shop"}, Spec: oam.ApplicationSpec{Components: []oam.Component{{
		Name: "web", Type: typ, Properties: props,
	}}}}
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{typ: h}, nil)
	cluster, err := tr.Transform(app, oam.TransformContext{Namespace: "demo", Policy: policy, Naming: naming})
	if err != nil {
		return nil, err
	}
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		return nil, err
	}
	var out []client.Object
	for _, a := range apps {
		for _, o := range a.Objects {
			out = append(out, *o)
		}
	}
	return out, nil
}

// TestAPIService_AuthoredValuesArriveTyped: every field of the spec reaches
// the object as authored, the object named by objectName with no namespace,
// and caBundle decodes from base64 as the API's JSON writes bytes.
func TestAPIService_AuthoredValuesArriveTyped(t *testing.T) {
	props := apiServiceProps(map[string]any{
		oam.ObjectNameProperty: apiServiceName,
		"service":              map[string]any{"namespace": "metrics", "name": "metrics-server", "port": 8443},
		"caBundle":             "Y2EtYnVuZGxl",
	})
	objs, err := clusterWideTransform("apiservice", &components.APIServiceHandler{}, props, nil, nil)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("got %d objects, want 1", len(objs))
	}
	svc, ok := objs[0].(*apiregistrationv1.APIService)
	if !ok {
		t.Fatalf("object is %T, want *APIService", objs[0])
	}
	if svc.Name != apiServiceName || svc.Namespace != "" {
		t.Errorf("object is %q in namespace %q, want %q in none", svc.Name, svc.Namespace, apiServiceName)
	}
	port := int32(8443)
	want := apiregistrationv1.APIServiceSpec{
		Group: "metrics.example.com", Version: "v1beta1",
		GroupPriorityMinimum: 100, VersionPriority: 10,
		Service:  &apiregistrationv1.ServiceReference{Namespace: "metrics", Name: "metrics-server", Port: &port},
		CABundle: []byte("ca-bundle"),
	}
	if !reflect.DeepEqual(svc.Spec, want) {
		got, _ := json.Marshal(svc.Spec)
		t.Errorf("spec = %s, want every authored field", got)
	}
}

// TestAPIService_Refusals: what the API requires beyond the type, and what
// the strict decode refuses.
func TestAPIService_Refusals(t *testing.T) {
	h := &components.APIServiceHandler{}
	for _, tt := range []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"no version", apiServiceProps(map[string]any{"version": nil}), "version: required"},
		{"no group", apiServiceProps(map[string]any{"group": nil}), "group: required"},
		{"no group beside v1, the core API's", apiServiceProps(map[string]any{"group": nil, "version": "v1"}), "group: required"},
		{"no group priority", apiServiceProps(map[string]any{"groupPriorityMinimum": nil}), "groupPriorityMinimum"},
		{"no version priority", apiServiceProps(map[string]any{"versionPriority": nil}), "versionPriority"},
		{"service without a namespace", apiServiceProps(map[string]any{"service": map[string]any{"name": "metrics-server"}}), "service.namespace: required"},
		{"service without a name", apiServiceProps(map[string]any{"service": map[string]any{"namespace": "metrics"}}), "service.name: required"},
		{"unknown key", apiServiceProps(map[string]any{"priority": 1}), "apiregistration.k8s.io/v1 APIServiceSpec"},
		{"unknown service key", apiServiceProps(map[string]any{"service": map[string]any{"namespace": "metrics", "name": "m", "url": "https://x"}}), "url"},
		{"caBundle not base64", apiServiceProps(map[string]any{"caBundle": "not base64!"}), "illegal base64 data"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.ToApplicationConfig(&oam.Component{Name: apiServiceName, Type: "apiservice", Properties: tt.props}, "demo")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ToApplicationConfig = %v, want an error naming %q", err, tt.want)
			}
		})
	}
}

// TestAPIService_Name: the object must be named <version>.<group>. The
// component's name, objectName or the Naming hook's answer may give that name,
// and any other is refused at generation, naming both; the author's
// objectName wins over the hook's.
func TestAPIService_Name(t *testing.T) {
	h := &components.APIServiceHandler{}
	hook := func(req oam.NameRequest) (string, bool) {
		if req.Role != oam.NameRoleObject {
			return "", false
		}
		return "hooked-" + req.Default, true
	}
	rightHook := func(req oam.NameRequest) (string, bool) {
		if req.Role != oam.NameRoleObject {
			return "", false
		}
		return apiServiceName, true
	}
	named := func(t *testing.T, objs []client.Object, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("transform: %v", err)
		}
		if len(objs) != 1 || objs[0].GetName() != apiServiceName {
			t.Fatalf("objects = %v, want one named %q", objs, apiServiceName)
		}
	}
	_, err := clusterWideTransform("apiservice", h, apiServiceProps(nil), nil, nil)
	if err == nil || !strings.Contains(err.Error(), `the APIService is named "web", and the API requires the name "`+apiServiceName+`"`) {
		t.Errorf("component name web: %v, want the name refused, naming both", err)
	}
	objs, err := clusterWideTransform("apiservice", h, apiServiceProps(map[string]any{oam.ObjectNameProperty: apiServiceName}), nil, nil)
	named(t, objs, err)
	_, err = clusterWideTransform("apiservice", h, apiServiceProps(nil), nil, hook)
	if err == nil || !strings.Contains(err.Error(), `the APIService is named "hooked-`) {
		t.Errorf("hooked name: %v, want the hook's name refused", err)
	}
	objs, err = clusterWideTransform("apiservice", h, apiServiceProps(nil), nil, rightHook)
	named(t, objs, err)
	objs, err = clusterWideTransform("apiservice", h, apiServiceProps(map[string]any{oam.ObjectNameProperty: apiServiceName}), nil, hook)
	named(t, objs, err)
	// The component's own name gives it too.
	cfg, err := h.ToApplicationConfig(&oam.Component{Name: apiServiceName, Type: "apiservice", Properties: apiServiceProps(nil)}, "demo")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	if _, err := cfg.Generate(stack.NewApplication(apiServiceName, "demo", cfg)); err != nil {
		t.Errorf("Generate under the component name %q = %v, want it built", apiServiceName, err)
	}
}

// TestAPIService_DeclaresItsObject: the handler declares a cluster-scoped
// APIService.
func TestAPIService_DeclaresItsObject(t *testing.T) {
	got, scope := (&components.APIServiceHandler{}).ComponentObject()
	want := schema.GroupKind{Group: "apiregistration.k8s.io", Kind: "APIService"}
	if got != want || scope != oam.ObjectScopeCluster {
		t.Errorf("ComponentObject() = %s, scope %d; want %s, cluster-scoped", got, scope, want)
	}
}

// TestAPIService_ObjectKindPolicy: the object kind rules gate the kind as
// every emitted object; nothing else of the environment policy does.
func TestAPIService_ObjectKindPolicy(t *testing.T) {
	clusterWideObjectKindPolicy(t, "apiservice", &components.APIServiceHandler{},
		apiServiceProps(map[string]any{oam.ObjectNameProperty: apiServiceName}),
		schema.GroupKind{Group: "apiregistration.k8s.io", Kind: "APIService"}, `APIService "`+apiServiceName+`"`)
}

// clusterWideObjectKindPolicy holds one cluster-scoped kind to the object
// kind rules: a policy that does not allow cluster-scoped objects refuses it,
// and so does one that forbids its kind or its group, each with the
// object-kind class and naming the object; one that allows it, and a policy
// with no object kind rules (the restrictive stub), build it.
func clusterWideObjectKindPolicy(t *testing.T, typ string, h oam.ComponentHandler, props map[string]any, gk schema.GroupKind, object string) {
	t.Helper()
	build := func(policy oam.Policy) error {
		_, err := clusterWideTransform(typ, h, props, policy, nil)
		return err
	}
	okWantRefusal(t, build(&okPolicy{}), object, "cluster-scoped")
	okWantRefusal(t, build(&okPolicy{forbidden: []schema.GroupKind{gk}, allowCluster: true}), object, "the object kind policy forbids the kind")
	okWantRefusal(t, build(&okPolicy{forbidden: []schema.GroupKind{{Group: gk.Group, Kind: "*"}}, allowCluster: true}), object, "the object kind policy forbids the kind")
	if err := build(&okPolicy{allowed: []schema.GroupKind{gk}, allowCluster: true}); err != nil {
		t.Errorf("transform under a policy that allows %s = %v, want it built", gk, err)
	}
	one := int32(1)
	if err := build(&stubPolicy{maxReplicas: &one, allowedRegistries: []string{"registry.invalid"}}); err != nil {
		t.Errorf("transform under a policy with no object kind rules = %v, want it built", err)
	}
}
