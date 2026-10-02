package components_test

import (
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// pvcBinding is a ClusterProfile capability set binding `pvc` to rendering.
func pvcBinding(rendering map[string]any) map[string]oam.CapabilityBinding {
	return map[string]oam.CapabilityBinding{"pvc": {Rendering: rendering}}
}

// A volumeClaimTemplates entry that leaves storageClass unauthored (absent or
// null) takes the `pvc` capability's storageClassName (go-kure/launcher#761);
// an authored value, "" included, wins, and a binding that supplies no class
// fills nothing.
func TestStatefulsetHandler_FillCapabilityDefaults(t *testing.T) {
	platform := pvcBinding(map[string]any{"storageClassName": "platform-ssd"})
	const absent = "<absent>"
	cases := []struct {
		name  string
		entry map[string]any
		caps  map[string]oam.CapabilityBinding
		want  any // the entry's storageClass after the fill; absent means no key
	}{
		{name: "unset takes the platform class", entry: map[string]any{}, caps: platform, want: "platform-ssd"},
		{name: "null takes the platform class", entry: map[string]any{"storageClass": nil}, caps: platform, want: "platform-ssd"},
		{name: "authored class wins", entry: map[string]any{"storageClass": "slow"}, caps: platform, want: "slow"},
		{name: "authored empty string wins", entry: map[string]any{"storageClass": ""}, caps: platform, want: ""},
		{name: "no binding", entry: map[string]any{}, caps: nil, want: absent},
		{name: "rendering without the key", entry: map[string]any{}, caps: pvcBinding(map[string]any{"accessModes": []any{"ReadWriteOnce"}}), want: absent},
		{name: "rendering null", entry: map[string]any{"storageClass": nil}, caps: pvcBinding(map[string]any{"storageClassName": nil}), want: nil},
		{name: "scoped binding is not read", entry: map[string]any{}, caps: map[string]oam.CapabilityBinding{"pvc.fast": {Rendering: map[string]any{"storageClassName": "fast"}}}, want: absent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := map[string]any{"name": "data", "mountPath": "/data", "size": "1Gi"}
			for k, v := range tc.entry {
				entry[k] = v
			}
			props := map[string]any{"image": "db:1", "volumeClaimTemplates": []any{entry}}
			before := map[string]any{"image": "db:1", "volumeClaimTemplates": []any{maps.Clone(entry)}}

			got, err := (&components.StatefulsetHandler{}).FillCapabilityDefaults(props, oam.LoweringContext{}.WithCapabilities(tc.caps))
			if err != nil {
				t.Fatalf("FillCapabilityDefaults: %v", err)
			}
			if !reflect.DeepEqual(props, before) {
				t.Errorf("input mutated: %#v, was %#v", props, before)
			}
			filled := got["volumeClaimTemplates"].([]any)[0].(map[string]any)
			v, present := filled["storageClass"]
			if !present {
				v = absent
			}
			if v != tc.want {
				t.Errorf("storageClass = %#v, want %#v", v, tc.want)
			}
		})
	}
}

// Each entry is filled on its own: one entry's authored class does not stop
// the next entry from taking the platform class.
func TestStatefulsetHandler_FillCapabilityDefaults_PerEntry(t *testing.T) {
	props := map[string]any{"volumeClaimTemplates": []any{
		map[string]any{"name": "data", "storageClass": "slow"},
		map[string]any{"name": "wal"},
		map[string]any{"name": "logs", "storageClass": ""},
	}}
	got, err := (&components.StatefulsetHandler{}).FillCapabilityDefaults(props, oam.LoweringContext{}.WithCapabilities(pvcBinding(map[string]any{"storageClassName": "platform-ssd"})))
	if err != nil {
		t.Fatalf("FillCapabilityDefaults: %v", err)
	}
	var classes []any
	for _, e := range got["volumeClaimTemplates"].([]any) {
		classes = append(classes, e.(map[string]any)["storageClass"])
	}
	if want := []any{"slow", "platform-ssd", ""}; !reflect.DeepEqual(classes, want) {
		t.Errorf("storageClass per entry = %#v, want %#v", classes, want)
	}
}

func TestStatefulsetHandler_FillCapabilityDefaults_ClassMustBeAString(t *testing.T) {
	props := map[string]any{"volumeClaimTemplates": []any{map[string]any{"name": "data"}}}
	_, err := (&components.StatefulsetHandler{}).FillCapabilityDefaults(props, oam.LoweringContext{}.WithCapabilities(pvcBinding(map[string]any{"storageClassName": 3})))
	if err == nil || !strings.Contains(err.Error(), `capability "pvc" storageClassName: expected string, got int`) {
		t.Fatalf("err = %v, want the non-string storageClassName refused", err)
	}
}

// The engine calls FillCapabilityDefaults (ComponentCapabilityFiller), so
// TransformWithPolicy lists `pvc` as consumed exactly when the statefulset
// builds a claim template, and the template carries the platform class.
func TestStatefulsetHandler_PVCCapabilityIsConsumed(t *testing.T) {
	caps := pvcBinding(map[string]any{"storageClassName": "platform-ssd"})
	cases := map[string]struct {
		templates    any
		wantConsumed []string
		wantClass    []string
	}{
		"claim template":    {templates: []any{map[string]any{"name": "data", "mountPath": "/data", "size": "1Gi"}}, wantConsumed: []string{"pvc"}, wantClass: []string{"platform-ssd"}},
		"no claim template": {templates: nil, wantConsumed: nil, wantClass: nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			props := map[string]any{"image": "ghcr.io/org/db:v1"}
			if tc.templates != nil {
				props["volumeClaimTemplates"] = tc.templates
			}
			app := &oam.Application{
				APIVersion: oam.SupportedAPIVersion,
				Kind:       "Application",
				Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
				Spec:       oam.ApplicationSpec{Components: []oam.Component{{Name: "db", Type: "statefulset", Properties: props}}},
			}
			tr := oam.NewTransformer(map[string]oam.ComponentHandler{"statefulset": &components.StatefulsetHandler{}}, nil)
			cluster, result, err := tr.TransformWithPolicy(app, oam.TransformContext{Capabilities: caps})
			if err != nil {
				t.Fatalf("transform: %v", err)
			}
			if got := result.ConsumedCapabilities; !reflect.DeepEqual(got, tc.wantConsumed) {
				t.Errorf("consumed capabilities %v, want %v", got, tc.wantConsumed)
			}
			var classes []string
			for _, b := range leafBundles(cluster.Node) {
				for _, app := range b.Applications {
					objs, err := app.Generate()
					if err != nil {
						t.Fatalf("generate: %v", err)
					}
					for _, obj := range objs {
						sts, ok := (*obj).(*appsv1.StatefulSet)
						if !ok {
							continue
						}
						for _, vct := range sts.Spec.VolumeClaimTemplates {
							if vct.Spec.StorageClassName == nil {
								t.Errorf("template %q has no storageClassName", vct.Name)
								continue
							}
							classes = append(classes, *vct.Spec.StorageClassName)
						}
					}
				}
			}
			if !reflect.DeepEqual(classes, tc.wantClass) {
				t.Errorf("template classes %v, want %v", classes, tc.wantClass)
			}
		})
	}
}

// An authored `storageClass: ""` on a claim template requests no class: the
// template's storageClassName is a pointer to "", not nil, as on a pvc volume
// and the persistentvolumeclaim kind (go-kure/launcher#761). Absent, it stays
// nil and the cluster's default class applies.
func TestStatefulsetHandler_ClaimTemplateExplicitEmptyStorageClass(t *testing.T) {
	cases := map[string]struct {
		entry map[string]any
		want  *string
	}{
		"explicit empty": {entry: map[string]any{"storageClass": ""}, want: new("")},
		"absent":         {entry: map[string]any{}, want: nil},
		"null":           {entry: map[string]any{"storageClass": nil}, want: nil},
		"authored":       {entry: map[string]any{"storageClass": "fast"}, want: new("fast")},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			entry := map[string]any{"name": "data", "mountPath": "/data", "size": "1Gi"}
			for k, v := range tc.entry {
				entry[k] = v
			}
			cfg, err := (&components.StatefulsetHandler{}).ToApplicationConfig(&oam.Component{
				Name: "db", Type: "statefulset",
				Properties: map[string]any{"image": "db:1", "volumeClaimTemplates": []any{entry}},
			}, "default")
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			objects, err := cfg.Generate(stack.NewApplication("db", "default", cfg))
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			var got *corev1.PersistentVolumeClaim
			for _, obj := range objects {
				if s, ok := (*obj).(*appsv1.StatefulSet); ok && len(s.Spec.VolumeClaimTemplates) == 1 {
					got = &s.Spec.VolumeClaimTemplates[0]
				}
			}
			if got == nil {
				t.Fatal("no StatefulSet with one claim template")
			}
			if !reflect.DeepEqual(got.Spec.StorageClassName, tc.want) {
				t.Errorf("storageClassName = %v, want %v", ptrString(got.Spec.StorageClassName), ptrString(tc.want))
			}
		})
	}
}

func ptrString(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return "&" + *p
}
