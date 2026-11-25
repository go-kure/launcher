package components_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// pvcBinding is a ClusterProfile capability set binding `pvc` to rendering.
func pvcBinding(rendering map[string]any) map[string]oam.CapabilityBinding {
	return map[string]oam.CapabilityBinding{"pvc": {Rendering: rendering}}
}

// stsClaimSpec returns the spec of volumeClaimTemplates[i] of props, and whether
// the entry has one.
func stsClaimSpec(props map[string]any, i int) (map[string]any, bool) {
	spec, ok := props["volumeClaimTemplates"].([]any)[i].(map[string]any)["spec"].(map[string]any)
	return spec, ok
}

// A volumeClaimTemplates entry that leaves spec.storageClassName unauthored
// (absent or null) takes the `pvc` capability's storageClassName
// (go-kure/launcher#761); an authored value, "" included, wins, and a binding
// that supplies no class fills nothing.
func TestStatefulsetHandler_FillCapabilityDefaults(t *testing.T) {
	platform := pvcBinding(map[string]any{"storageClassName": "platform-ssd"})
	const absent = "<absent>"
	// spec builds the entry's spec afresh, so the input and the copy kept to check
	// it was not mutated share nothing.
	withClass := func(v any) func() map[string]any {
		return func() map[string]any {
			return map[string]any{"metadata": map[string]any{"name": "data"}, "spec": map[string]any{"storageClassName": v, "resources": map[string]any{"requests": map[string]any{"storage": "1Gi"}}}}
		}
	}
	noClass := func() map[string]any {
		return map[string]any{"metadata": map[string]any{"name": "data"}, "spec": map[string]any{"resources": map[string]any{"requests": map[string]any{"storage": "1Gi"}}}}
	}
	noSpec := func() map[string]any { return map[string]any{"metadata": map[string]any{"name": "data"}} }
	cases := []struct {
		name  string
		entry func() map[string]any
		caps  map[string]oam.CapabilityBinding
		want  any // the entry's spec.storageClassName after the fill; absent means no key
	}{
		{name: "unset takes the platform class", entry: noClass, caps: platform, want: "platform-ssd"},
		{name: "no spec takes the platform class", entry: noSpec, caps: platform, want: "platform-ssd"},
		{name: "null takes the platform class", entry: withClass(nil), caps: platform, want: "platform-ssd"},
		{name: "authored class wins", entry: withClass("slow"), caps: platform, want: "slow"},
		{name: "authored empty string wins", entry: withClass(""), caps: platform, want: ""},
		{name: "no binding", entry: noClass, caps: nil, want: absent},
		{name: "rendering without the key", entry: noClass, caps: pvcBinding(map[string]any{"accessModes": []any{"ReadWriteOnce"}}), want: absent},
		{name: "rendering null", entry: withClass(nil), caps: pvcBinding(map[string]any{"storageClassName": nil}), want: nil},
		{name: "scoped binding is not read", entry: noClass, caps: map[string]oam.CapabilityBinding{"pvc.fast": {Rendering: map[string]any{"storageClassName": "fast"}}}, want: absent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := map[string]any{"volumeClaimTemplates": []any{tc.entry()}}
			before := map[string]any{"volumeClaimTemplates": []any{tc.entry()}}

			got, err := (&components.StatefulsetHandler{}).FillCapabilityDefaults(props, oam.LoweringContext{}.WithCapabilities(tc.caps))
			if err != nil {
				t.Fatalf("FillCapabilityDefaults: %v", err)
			}
			if !reflect.DeepEqual(props, before) {
				t.Errorf("input mutated: %#v, was %#v", props, before)
			}
			v := any(absent)
			if spec, ok := stsClaimSpec(got, 0); ok {
				if class, present := spec["storageClassName"]; present {
					v = class
				}
			}
			if v != tc.want {
				t.Errorf("spec.storageClassName = %#v, want %#v", v, tc.want)
			}
			if name := got["volumeClaimTemplates"].([]any)[0].(map[string]any)["metadata"]; !reflect.DeepEqual(name, map[string]any{"name": "data"}) {
				t.Errorf("metadata = %#v, want it untouched", name)
			}
			if spec, ok := stsClaimSpec(got, 0); ok && tc.name != "no spec takes the platform class" {
				if !reflect.DeepEqual(spec["resources"], map[string]any{"requests": map[string]any{"storage": "1Gi"}}) {
					t.Errorf("resources = %#v, want them untouched", spec["resources"])
				}
			}
		})
	}
}

// Each entry is filled on its own: one entry's authored class does not stop
// the next entry from taking the platform class.
func TestStatefulsetHandler_FillCapabilityDefaults_PerEntry(t *testing.T) {
	entry := func(name string, class ...any) map[string]any {
		spec := map[string]any{}
		if len(class) > 0 {
			spec["storageClassName"] = class[0]
		}
		return map[string]any{"metadata": map[string]any{"name": name}, "spec": spec}
	}
	props := map[string]any{"volumeClaimTemplates": []any{
		entry("data", "slow"),
		entry("wal"),
		entry("logs", ""),
	}}
	got, err := (&components.StatefulsetHandler{}).FillCapabilityDefaults(props, oam.LoweringContext{}.WithCapabilities(pvcBinding(map[string]any{"storageClassName": "platform-ssd"})))
	if err != nil {
		t.Fatalf("FillCapabilityDefaults: %v", err)
	}
	var classes []any
	for i := range got["volumeClaimTemplates"].([]any) {
		spec, _ := stsClaimSpec(got, i)
		classes = append(classes, spec["storageClassName"])
	}
	if want := []any{"slow", "platform-ssd", ""}; !reflect.DeepEqual(classes, want) {
		t.Errorf("storageClassName per entry = %#v, want %#v", classes, want)
	}
}

// The strict decode matches keys without regard to case, so `Spec` and
// `StorageClassName` are authored spellings. The fill works under the spelling
// the document used: it never adds a second spelling of a field the document
// has (the decode refuses two), and an authored value in any spelling wins.
func TestStatefulsetHandler_FillCapabilityDefaults_AnySpelling(t *testing.T) {
	caps := pvcBinding(map[string]any{"storageClassName": "platform-ssd"})
	storage := map[string]any{"requests": map[string]any{"storage": "1Gi"}}
	cases := map[string]struct {
		entry     map[string]any
		wantSpec  string // the key the entry's spec ends up under
		wantClass string // the key the class ends up under
		want      any
	}{
		"Spec, class unset": {
			entry:    map[string]any{"Spec": map[string]any{"resources": storage}},
			wantSpec: "Spec", wantClass: "storageClassName", want: "platform-ssd",
		},
		"StorageClassName null": {
			entry:    map[string]any{"spec": map[string]any{"StorageClassName": nil}},
			wantSpec: "spec", wantClass: "StorageClassName", want: "platform-ssd",
		},
		"StorageClassName authored empty": {
			entry:    map[string]any{"spec": map[string]any{"StorageClassName": ""}},
			wantSpec: "spec", wantClass: "StorageClassName", want: "",
		},
		"Spec and StorageClassName authored": {
			entry:    map[string]any{"Spec": map[string]any{"STORAGECLASSNAME": "slow"}},
			wantSpec: "Spec", wantClass: "STORAGECLASSNAME", want: "slow",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tc.entry["metadata"] = map[string]any{"name": "data"}
			props := map[string]any{"volumeClaimTemplates": []any{tc.entry}}
			got, err := (&components.StatefulsetHandler{}).FillCapabilityDefaults(props, oam.LoweringContext{}.WithCapabilities(caps))
			if err != nil {
				t.Fatalf("FillCapabilityDefaults: %v", err)
			}
			out := got["volumeClaimTemplates"].([]any)[0].(map[string]any)
			for k := range out {
				if k != "metadata" && k != tc.wantSpec {
					t.Errorf("entry has key %q, want only metadata and %q", k, tc.wantSpec)
				}
			}
			spec, _ := out[tc.wantSpec].(map[string]any)
			if v, ok := spec[tc.wantClass]; !ok || v != tc.want {
				t.Errorf("spec[%q] = %#v (present %v), want %#v", tc.wantClass, v, ok, tc.want)
			}
			for k := range spec {
				if strings.EqualFold(k, "storageClassName") && k != tc.wantClass {
					t.Errorf("spec has a second spelling %q of storageClassName", k)
				}
			}
		})
	}
}

// A null spelling beside a set one is accepted by the strict decode, which
// skips the null. The fill must read the set spelling as the authored value and
// leave the entry as it is, never write the platform class under the null one:
// that would set the field twice and the decode would refuse the author for a
// value the fill wrote.
func TestStatefulsetHandler_FillCapabilityDefaults_NullBesideASetSpelling(t *testing.T) {
	caps := pvcBinding(map[string]any{"storageClassName": "platform-ssd"})
	for name, tc := range map[string]struct {
		spec     map[string]any
		wantFill bool
	}{
		"exact name null, other spelling set": {map[string]any{"storageClassName": nil, "STORAGECLASSNAME": "fast"}, false},
		"capitalised null, lowercase set":     {map[string]any{"StorageClassName": nil, "storageclassname": "fast"}, false},
		"other spelling null, exact name set": {map[string]any{"StorageClassName": nil, "storageClassName": "fast"}, false},
		"both spellings null":                 {map[string]any{"StorageClassName": nil, "storageclassname": nil}, true},
	} {
		t.Run(name, func(t *testing.T) {
			props := map[string]any{"volumeClaimTemplates": []any{map[string]any{
				"metadata": map[string]any{"name": "data"}, "spec": tc.spec,
			}}}
			got, err := (&components.StatefulsetHandler{}).FillCapabilityDefaults(props, oam.LoweringContext{}.WithCapabilities(caps))
			if err != nil {
				t.Fatalf("FillCapabilityDefaults: %v", err)
			}
			out := got["volumeClaimTemplates"].([]any)[0].(map[string]any)["spec"].(map[string]any)
			var set []string
			for k, v := range out {
				if strings.EqualFold(k, "storageClassName") && v != nil {
					set = append(set, k)
				}
			}
			if len(set) != 1 {
				t.Fatalf("spec sets storageClassName under %v, want exactly one spelling: %#v", set, out)
			}
			if filled := out[set[0]] == "platform-ssd"; filled != tc.wantFill {
				t.Errorf("platform class written = %v, want %v: %#v", filled, tc.wantFill, out)
			}
		})
	}
}

func TestStatefulsetHandler_FillCapabilityDefaults_ClassMustBeAString(t *testing.T) {
	props := map[string]any{"volumeClaimTemplates": []any{map[string]any{"metadata": map[string]any{"name": "data"}}}}
	_, err := (&components.StatefulsetHandler{}).FillCapabilityDefaults(props, oam.LoweringContext{}.WithCapabilities(pvcBinding(map[string]any{"storageClassName": 3})))
	if err == nil || !strings.Contains(err.Error(), `capability "pvc" storageClassName: expected string, got int`) {
		t.Fatalf("err = %v, want the non-string storageClassName refused", err)
	}
	// The refusal carries the capability key it names (go-kure/launcher#1687), and
	// its text is what it was.
	var ce *oam.CapabilityError
	if !errors.As(err, &ce) || ce.Capability != "pvc" {
		t.Fatalf("want an *oam.CapabilityError with key %q in %T: %v", "pvc", err, err)
	}
	if want := `capability "pvc" storageClassName: expected string, got int`; err.Error() != want {
		t.Errorf("text = %q, want %q", err.Error(), want)
	}
}

// The same refusal, reached through Transform: the *oam.CapabilityError survives the
// engine's own wrapping of a component handler's fill, with the text it had
// (go-kure/launcher#1687).
func TestStatefulsetHandler_FillCapabilityDefaults_ClassMustBeAString_ThroughTransform(t *testing.T) {
	props := ptObject(t, stsPlain(htPlainPod))
	props["volumeClaimTemplates"] = []any{stsClaim("data", "1Gi")}
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: []oam.Component{{Name: "db", Type: "statefulset", Properties: props}}},
	}
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{"statefulset": &components.StatefulsetHandler{}}, nil)
	_, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Capabilities: pvcBinding(map[string]any{"storageClassName": 3})})
	if err == nil {
		t.Fatal("want the non-string storageClassName refused")
	}
	var ce *oam.CapabilityError
	if !errors.As(err, &ce) || ce.Capability != "pvc" {
		t.Fatalf("want an *oam.CapabilityError with key %q in %T: %v", "pvc", err, err)
	}
	if !strings.Contains(err.Error(), `capability "pvc" storageClassName: expected string, got int`) {
		t.Errorf("text = %q, want it to keep the refusal's own text", err.Error())
	}
}

// A shape the parser refuses is left alone by the fill, whatever the binding, and
// the platform's capability is not read for it: the parse refuses it in its own
// words, and `pvc` is not recorded as consumed for a template that is not one.
func TestStatefulsetHandler_FillCapabilityDefaults_LeavesRefusedShapesToTheParse(t *testing.T) {
	platform := oam.LoweringContext{}.WithCapabilities(pvcBinding(map[string]any{"storageClassName": 3})) // a binding that would fail if it were read
	for name, props := range map[string]map[string]any{
		"no claim templates":         {},
		"null claim templates":       {"volumeClaimTemplates": nil},
		"claim templates not a list": {"volumeClaimTemplates": "data"},
		"an entry that is no map":    {"volumeClaimTemplates": []any{"data"}},
		"a spec that is no map":      {"volumeClaimTemplates": []any{map[string]any{"metadata": map[string]any{"name": "data"}, "spec": "fast"}}},
		"an empty list":              {"volumeClaimTemplates": []any{}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := (&components.StatefulsetHandler{}).FillCapabilityDefaults(props, platform)
			if err != nil {
				t.Fatalf("FillCapabilityDefaults: %v", err)
			}
			if !reflect.DeepEqual(got, props) {
				t.Errorf("props changed: %#v, was %#v", got, props)
			}
		})
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
		"claim template":    {templates: []any{stsClaim("data", "1Gi")}, wantConsumed: []string{"pvc"}, wantClass: []string{"platform-ssd"}},
		"no claim template": {templates: nil, wantConsumed: nil, wantClass: nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			props := ptObject(t, stsPlain(htPlainPod))
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

// An authored `spec.storageClassName: ""` on a claim template requests no class:
// the template's storageClassName is a pointer to "", not nil, as on a pvc volume
// and the persistentvolumeclaim kind (go-kure/launcher#761). Absent, it stays
// nil and the cluster's default class applies.
func TestStatefulsetHandler_ClaimTemplateExplicitEmptyStorageClass(t *testing.T) {
	cases := map[string]struct {
		spec map[string]any
		want *string
	}{
		"explicit empty": {spec: map[string]any{"storageClassName": ""}, want: new("")},
		"absent":         {spec: map[string]any{}, want: nil},
		"null":           {spec: map[string]any{"storageClassName": nil}, want: nil},
		"authored":       {spec: map[string]any{"storageClassName": "fast"}, want: new("fast")},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			spec := map[string]any{"resources": map[string]any{"requests": map[string]any{"storage": "1Gi"}}}
			for k, v := range tc.spec {
				spec[k] = v
			}
			props := ptObject(t, stsPlain(htPlainPod))
			props["volumeClaimTemplates"] = []any{map[string]any{"metadata": map[string]any{"name": "data"}, "spec": spec}}
			sts := stsGenerate(t, "db", props)
			if len(sts.Spec.VolumeClaimTemplates) != 1 {
				t.Fatalf("got %d claim templates, want 1", len(sts.Spec.VolumeClaimTemplates))
			}
			if got := sts.Spec.VolumeClaimTemplates[0].Spec.StorageClassName; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("storageClassName = %v, want %v", ptrString(got), ptrString(tc.want))
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
