package patch

import (
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// nestedDeployment has two containers, and the first holds two ports.
func nestedDeployment() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "web"},
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []any{
			map[string]any{"name": "main", "image": "nginx", "ports": []any{
				map[string]any{"containerPort": int64(80), "name": "http"},
				map[string]any{"containerPort": int64(81), "name": "alt"},
			}},
			map[string]any{"name": "side", "image": "busybox", "ports": []any{
				map[string]any{"containerPort": int64(90)},
			}},
		}}}},
	}}
}

func containersOf(t *testing.T, obj *unstructured.Unstructured) []any {
	t.Helper()
	cs, found, err := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "containers")
	if err != nil || !found {
		t.Fatalf("containers: found=%v err=%v", found, err)
	}
	return cs
}

// portsOf returns the ports of container i, so a row can edit its expected copy.
func portsOf(cs []any, i int) []any { return cs[i].(map[string]any)["ports"].([]any) }

func setPorts(cs []any, i int, ports []any) { cs[i].(map[string]any)["ports"] = ports }

// A key with a list selector inside an item of another list applies to the item the first
// selector picks. Each row compares the whole containers slice, so a neighbour that changed is
// seen too.
func TestApply_ListSelectorInsideListItem(t *testing.T) {
	twoKeys := map[string]any{"containerPort": 8080, "name": "web"}
	rows := []struct {
		name  string
		key   string
		value any
		want  func(cs []any)
	}{
		{"index_index", "spec.template.spec.containers[0].ports[0].containerPort", 8080,
			func(cs []any) { portsOf(cs, 0)[0].(map[string]any)["containerPort"] = int64(8080) }},
		{"name_index", "spec.template.spec.containers[name=main].ports[0].containerPort", 8080,
			func(cs []any) { portsOf(cs, 0)[0].(map[string]any)["containerPort"] = int64(8080) }},
		{"name_name", "spec.template.spec.containers[name=main].ports[name=alt].containerPort", 8080,
			func(cs []any) { portsOf(cs, 0)[1].(map[string]any)["containerPort"] = int64(8080) }},
		{"second_container", "spec.template.spec.containers[name=side].ports[0].containerPort", 8080,
			func(cs []any) { portsOf(cs, 1)[0].(map[string]any)["containerPort"] = int64(8080) }},
		{"whole_item", "spec.template.spec.containers[0].ports[0]", twoKeys,
			func(cs []any) {
				portsOf(cs, 0)[0] = map[string]any{"containerPort": int64(8080), "name": "web"}
			}},
		{"delete_item", "spec.template.spec.containers[0].ports[delete=name=alt]", nil,
			func(cs []any) { setPorts(cs, 0, portsOf(cs, 0)[:1]) }},
		{"append_item", "spec.template.spec.containers[0].ports[-]", twoKeys,
			func(cs []any) {
				setPorts(cs, 0, append(portsOf(cs, 0), map[string]any{"containerPort": int64(8080), "name": "web"}))
			}},
		{"insert_after_last_selector", "spec.template.spec.containers[0].ports[+0]", twoKeys,
			func(cs []any) {
				ports := portsOf(cs, 0)
				setPorts(cs, 0, []any{ports[0], map[string]any{"containerPort": int64(8080), "name": "web"}, ports[1]})
			}},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			op, err := ParsePatchLine(r.key, r.value)
			if err != nil {
				t.Fatalf("ParsePatchLine(%q): %v", r.key, err)
			}
			obj := nestedDeployment()
			if err := (&ResourceWithPatches{Name: "web", Base: obj, Patches: []PatchOp{op}}).Apply(); err != nil {
				t.Fatalf("Apply(%q): %v", r.key, err)
			}
			want := containersOf(t, nestedDeployment())
			r.want(want)
			if got := containersOf(t, obj); !reflect.DeepEqual(got, want) {
				t.Errorf("containers after %q:\n got %v\nwant %v", r.key, got, want)
			}
		})
	}
}

// Three selectors in one key: each one picks an item in the list the one before it picked.
func TestApply_ThreeListSelectorsInOneKey(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{"a": []any{map[string]any{"b": []any{map[string]any{"c": []any{
			map[string]any{"d": int64(1)}, map[string]any{"d": int64(2)},
		}}}}}},
	}}
	op, err := ParsePatchLine("spec.a[0].b[0].c[1].d", 9)
	if err != nil {
		t.Fatalf("ParsePatchLine: %v", err)
	}
	if err := (&ResourceWithPatches{Name: "x", Base: obj, Patches: []PatchOp{op}}).Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	c, _, _ := unstructured.NestedSlice(obj.Object, "spec", "a")
	got := c[0].(map[string]any)["b"].([]any)[0].(map[string]any)["c"]
	want := []any{map[string]any{"d": int64(1)}, map[string]any{"d": int64(9)}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("c = %v, want %v", got, want)
	}
}

// Keys that still fail, and fail as before: the object is left as it was and the error says why.
func TestApply_ListSelectorInsideListItem_Refusals(t *testing.T) {
	for _, r := range []struct{ name, key, want string }{
		{"first_index_out_of_range", "spec.template.spec.containers[5].ports[0].containerPort", "out of bounds"},
		{"inner_list_missing", "spec.template.spec.containers[0].volumes[0].name", "array not found"},
		// An insert selector can only be the last one, so a key that opens with one is not split.
		{"insert_selector_first", "spec.template.spec.containers[+0].ports[0].containerPort", "array not found"},
	} {
		t.Run(r.name, func(t *testing.T) {
			op, err := ParsePatchLine(r.key, 8080)
			if err != nil {
				t.Fatalf("ParsePatchLine(%q): %v", r.key, err)
			}
			obj := nestedDeployment()
			err = (&ResourceWithPatches{Name: "web", Base: obj, Patches: []PatchOp{op}}).Apply()
			if err == nil || !strings.Contains(err.Error(), r.want) {
				t.Fatalf("Apply(%q) = %v, want an error holding %q", r.key, err, r.want)
			}
			if got, want := containersOf(t, obj), containersOf(t, nestedDeployment()); !reflect.DeepEqual(got, want) {
				t.Errorf("a refused patch changed the object:\n got %v\nwant %v", got, want)
			}
		})
	}
}

const nestedBase = "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  template:\n    spec:\n      containers:\n        - name: main\n          image: nginx\n          ports:\n            - containerPort: 80\n"

// The input of the ticket, through each loader route. Untargeted lines are matched to a resource
// by their path, which needs the first selector to be split off the path.
func TestLoaders_ListSelectorInsideListItem(t *testing.T) {
	for name, patchSrc := range map[string]string{
		"flat_yaml_index": "spec.template.spec.containers[0].ports[0].containerPort: 8080\n",
		"flat_yaml_name":  "spec.template.spec.containers[name=main].ports[0].containerPort: 8080\n",
		"targeted_yaml":   "- target: deployment.web\n  patch:\n    spec.template.spec.containers[0].ports[0].containerPort: 8080\n",
		"toml":            "[deployment.web.containers.0]\nports[0].containerPort: 8080\n",
	} {
		t.Run(name, func(t *testing.T) {
			resources, err := LoadResourcesFromMultiYAML(strings.NewReader(nestedBase))
			if err != nil {
				t.Fatalf("load the base: %v", err)
			}
			patches, err := LoadPatchFile(strings.NewReader(patchSrc))
			if err != nil {
				t.Fatalf("load the patch: %v", err)
			}
			set, err := NewPatchableAppSet(resources, patches)
			if err != nil {
				t.Fatalf("build the set: %v", err)
			}
			resolved, err := set.Resolve()
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if len(resolved) != 1 {
				t.Fatalf("resolved %d resources, want 1", len(resolved))
			}
			if err := resolved[0].Apply(); err != nil {
				t.Fatalf("apply: %v", err)
			}
			want := []any{map[string]any{"name": "main", "image": "nginx", "ports": []any{map[string]any{"containerPort": int64(8080)}}}}
			if got := containersOf(t, resolved[0].Base); !reflect.DeepEqual(got, want) {
				t.Errorf("containers:\n got %v\nwant %v", got, want)
			}
		})
	}
}

// The write-back path applies the same patch to a document and keeps what the patch did not touch.
func TestApplyPatchesToDocument_ListSelectorInsideListItem(t *testing.T) {
	src := strings.Replace(nestedBase, "image: nginx", "image: nginx # keep this comment", 1)
	doc := loadOneDocument(t, src)
	op, err := ParsePatchLine("spec.template.spec.containers[0].ports[0].containerPort", 8080)
	if err != nil {
		t.Fatalf("ParsePatchLine: %v", err)
	}
	if err := doc.ApplyPatchesToDocument([]PatchOp{op}); err != nil {
		t.Fatalf("ApplyPatchesToDocument: %v", err)
	}
	out := writtenBack(t, doc.Node)
	for _, want := range []string{"containerPort: 8080", "image: nginx", "# keep this comment"} {
		if !strings.Contains(out, want) {
			t.Errorf("write-back lacks %q:\n%s", want, out)
		}
	}
}
