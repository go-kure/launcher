package oam

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
)

// listOf is an unnamed `v1` List of items, as a Go caller's own config may hand
// one out.
func listOf(items ...any) *client.Object {
	return collisionObject(&unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "List", "items": items,
	}})
}

// configMapItem is a ConfigMap as a list member.
func configMapItem(namespace, name string) map[string]any {
	return map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"namespace": namespace, "name": name}}
}

// kindlessItem is a list member that states no apiVersion and no kind.
func kindlessItem(namespace, name string) map[string]any {
	return map[string]any{"metadata": map[string]any{"namespace": namespace, "name": name}}
}

// kindlessEnvelope is an object that states no apiVersion and no kind and holds
// items: Flux would read it by its items, and nothing can apply the object itself.
func kindlessEnvelope(namespace, name string, items ...any) *client.Object {
	return collisionObject(&unstructured.Unstructured{Object: kindlessEnvelopeItem(namespace, name, items...)})
}

// kindlessEnvelopeItem is such an envelope as a list member. Inside a List it is
// never keyed: Kustomize's inlining hands it on, having no kind that ends in List,
// and Flux then reads it by its items, checking no kind. That is Flux's one level:
// inside an envelope only Flux expands it is a member like any other, and keyed.
func kindlessEnvelopeItem(namespace, name string, items ...any) map[string]any {
	e := kindlessItem(namespace, name)
	e["items"] = items
	return e
}

// fluxOnlyEnvelope is an envelope Kustomize's build leaves alone, its kind not
// ending in List, and Flux replaces with its items.
func fluxOnlyEnvelope(items ...any) *client.Object {
	return collisionObject(&unstructured.Unstructured{Object: envelope("example.com/v1", "Widget", items...)})
}

// envelopeShape is one way an object can hold others in an items array, built
// over members a site can tell apart by name.
type envelopeShape struct {
	name  string
	build func(member func(name string) map[string]any) *unstructured.Unstructured
	// names are the members the envelope holds, at any depth; applied are those
	// Flux applies (appliedObjects).
	names, applied []string
}

func envelope(apiVersion, kind string, items ...any) map[string]any {
	return map[string]any{"apiVersion": apiVersion, "kind": kind, "metadata": map[string]any{"name": "envelope"}, "items": items}
}

var envelopeShapes = []envelopeShape{
	{
		name: "a v1 List",
		build: func(m func(string) map[string]any) *unstructured.Unstructured {
			return &unstructured.Unstructured{Object: envelope("v1", "List", m("a"), m("b"))}
		},
		names: []string{"a", "b"}, applied: []string{"a", "b"},
	},
	{
		name: "a typed list kind",
		build: func(m func(string) map[string]any) *unstructured.Unstructured {
			return &unstructured.Unstructured{Object: envelope("v1", "PersistentVolumeClaimList", m("a"))}
		},
		names: []string{"a"}, applied: []string{"a"},
	},
	{
		name: "a list kind no scheme registers",
		build: func(m func(string) map[string]any) *unstructured.Unstructured {
			return &unstructured.Unstructured{Object: envelope("example.com/v1", "WidgetList", m("a"))}
		},
		names: []string{"a"}, applied: []string{"a"},
	},
	{
		name: "an envelope only Flux expands",
		build: func(m func(string) map[string]any) *unstructured.Unstructured {
			return &unstructured.Unstructured{Object: envelope("example.com/v1", "Widget", m("a"))}
		},
		names: []string{"a"}, applied: []string{"a"},
	},
	{
		name: "a List inside a List",
		build: func(m func(string) map[string]any) *unstructured.Unstructured {
			return &unstructured.Unstructured{Object: envelope("v1", "List", envelope("v1", "List", m("a")), m("b"))}
		},
		names: []string{"a", "b"}, applied: []string{"a", "b"},
	},
	{
		// Only "b" is applied. Kustomize's build leaves the envelope alone, since
		// its kind does not end in List, so nothing expands the List inside it;
		// Flux then expands the envelope one level and applies that inner List as
		// the object it is, with "a" still inside it.
		name: "a List inside an envelope only Flux expands",
		build: func(m func(string) map[string]any) *unstructured.Unstructured {
			return &unstructured.Unstructured{Object: envelope("example.com/v1", "Widget", envelope("v1", "List", m("a")), m("b"))}
		},
		names: []string{"a", "b"}, applied: []string{"b"},
	},
	{
		// "a" is applied. The List is inlined; its member has no kind, so
		// Kustomize's inlining hands it on, and Flux reads it by its items without
		// looking for a kind. No site keys or reads the kindless member itself.
		name: "an envelope with no kind inside a List",
		build: func(m func(string) map[string]any) *unstructured.Unstructured {
			return &unstructured.Unstructured{Object: envelope("v1", "List", kindlessEnvelopeItem("shop", "inner", m("a")))}
		},
		names: []string{"a"}, applied: []string{"a"},
	},
	{
		// Nothing is applied from inside it. An envelope is an object whose items
		// is an array, for Kustomize and for Flux alike; with items a map, this is
		// one object that happens to have a field of that name.
		name: "an items field that is no array",
		build: func(m func(string) map[string]any) *unstructured.Unstructured {
			e := envelope("example.com/v1", "Widget")
			e["items"] = map[string]any{"a": m("a")}
			return &unstructured.Unstructured{Object: e}
		},
		names: []string{"a"},
	},
}

// TestListEnvelope_EverySiteReadsWhatFluxApplies holds every reader of generated
// objects to one rule for what an envelope stands for (appliedObjects): the two
// collision checks, a sibling group's own comparison, the forced-volume scan, the
// component label and the reserved metadata keys. A site that read an envelope by
// a rule of its own would see another set of members for one of the shapes. The
// one difference between them is tested with each comparison: a generated object
// with no kind that holds items is refused there (keyedObjects), where the three
// scans read it by its items. As a member of a List the six read it alike.
func TestListEnvelope_EverySiteReadsWhatFluxApplies(t *testing.T) {
	plain := func(name string) map[string]any { return unstructuredClaim(name, false) }
	claim := func(name string) client.Object { return &unstructured.Unstructured{Object: plain(name)} }
	sites := []struct {
		name string
		// sees reports whether the site reads the member name of shape.
		sees func(t *testing.T, shape envelopeShape, name string) bool
	}{
		{"CheckInDocumentCollisions", func(_ *testing.T, shape envelopeShape, name string) bool {
			err := CheckInDocumentCollisions([]GeneratedApplication{
				generatedApp("one", "one", collisionObject(shape.build(plain))),
				generatedApp("two", "two", collisionObject(claim(name))),
			})
			return err != nil && strings.Contains(err.Error(), `PersistentVolumeClaim "shop/`+name+`"`)
		}},
		{"CheckCrossDocumentCollisions", func(_ *testing.T, shape envelopeShape, name string) bool {
			err := CheckCrossDocumentCollisions([]GeneratedDocument{
				generatedDoc("shop", "one", collisionObject(shape.build(plain))),
				generatedDoc("shop", "two", collisionObject(claim(name))),
			})
			return err != nil && strings.Contains(err.Error(), `PersistentVolumeClaim "shop/`+name+`"`)
		}},
		{"a sibling group", func(_ *testing.T, shape envelopeShape, name string) bool {
			_, err := siblingGroupOf([]client.Object{shape.build(plain)}, []client.Object{claim(name)}).Generate(nil)
			return err != nil && strings.Contains(err.Error(), `PersistentVolumeClaim "shop/`+name+`"`)
		}},
		{"WarnForcedVolumes", func(_ *testing.T, shape envelopeShape, name string) bool {
			var warnings []string
			tr := NewTransformer(nil, nil)
			tr.SetWarningHandler(func(w string) { warnings = append(warnings, w) })
			forced := shape.build(func(n string) map[string]any { return unstructuredClaim(n, n == name) })
			tr.WarnForcedVolumes([]GeneratedApplication{generatedApp("raw", "", collisionObject(forced))})
			return slices.ContainsFunc(warnings, func(w string) bool {
				return strings.Contains(w, "PersistentVolumeClaim shop/"+name+" ")
			})
		}},
		{"the component label", func(t *testing.T, shape envelopeShape, name string) bool {
			members := map[string]map[string]any{}
			labelled := shape.build(func(n string) map[string]any {
				members[n] = plain(n)
				return members[n]
			})
			if err := stampComponentLabel(labelled, ownershipKey, "web"); err != nil {
				t.Fatalf("stampComponentLabel: %v", err)
			}
			got, _, _ := unstructured.NestedString(members[name], "metadata", "labels", ownershipKey)
			return got == "web"
		}},
		{"the reserved metadata keys", func(t *testing.T, shape envelopeShape, name string) bool {
			reserving := shape.build(func(n string) map[string]any {
				m := plain(n)
				if n == name {
					m["metadata"].(map[string]any)["labels"] = map[string]any{"example.org/tenant": "a"}
				}
				return m
			})
			wrapped := wrapOwnedConfigReserving(&ownershipObjectsConfig{objects: []client.Object{reserving}}, "web", ownershipKey, mustReserve(t, reservedForTest...))
			_, err := stack.NewApplication("web", "shop", wrapped).Generate()
			if err != nil && !errors.Is(err, ErrReservedMetadataKey) {
				t.Fatalf("Generate: %v", err)
			}
			return err != nil
		}},
	}
	for _, shape := range envelopeShapes {
		t.Run(shape.name, func(t *testing.T) {
			var applied []string
			for _, obj := range appliedObjects(shape.build(plain)) {
				if obj.GetObjectKind().GroupVersionKind().Kind == "PersistentVolumeClaim" {
					applied = append(applied, obj.GetName())
				}
			}
			if !slices.Equal(applied, shape.applied) {
				t.Fatalf("appliedObjects holds the members %v, want %v", applied, shape.applied)
			}
			for _, site := range sites {
				for _, name := range shape.names {
					if got, want := site.sees(t, shape, name), slices.Contains(shape.applied, name); got != want {
						t.Errorf("%s reads member %q = %v, want %v", site.name, name, got, want)
					}
				}
			}
		})
	}
}

// siblingGroupOf is a sibling group "web" of two members, of the types "a" and
// "b", that generate the given objects.
func siblingGroupOf(a, b []client.Object) *siblingGroupConfig {
	return &siblingGroupConfig{
		members: []*stack.Application{
			stack.NewApplication("web", "shop", &ownershipObjectsConfig{objects: a}),
			stack.NewApplication("web", "shop", &ownershipObjectsConfig{objects: b}),
		},
		types: []string{"a", "b"},
	}
}

// TestSiblingGroup_ListMembersAreCompared: two members of a sibling group are
// compared by the objects Flux applies, so an item of a List one member hands
// out collides with the same object from the other, and two members that each
// hand out a List of their own do not collide on the envelope. The group never
// reaches CheckInDocumentCollisions as more than one application, so nothing else
// compares member with member.
func TestSiblingGroup_ListMembersAreCompared(t *testing.T) {
	object := func(p *client.Object) client.Object { return *p }
	cases := []struct {
		name string
		a, b []client.Object
		// want is the whole error message; "" means accepted.
		want string
	}{
		{
			name: "an item of a List and the same object from the other member",
			a:    []client.Object{object(listOf(configMapItem("shop", "shared")))},
			b:    []client.Object{object(configMap("shop", "shared"))},
			want: `sibling group "web": members "a" and "b" both generate ConfigMap "shop/shared"; exactly one member may`,
		},
		{
			name: "the same item in the Lists of both members",
			a:    []client.Object{object(listOf(configMapItem("shop", "own"), configMapItem("shop", "shared")))},
			b:    []client.Object{object(listOf(configMapItem("shop", "shared")))},
			want: `sibling group "web": members "a" and "b" both generate ConfigMap "shop/shared"; exactly one member may`,
		},
		{
			name: "a List item with no kind",
			a:    []client.Object{object(listOf(kindlessItem("shop", "settings")))},
			b:    []client.Object{object(configMap("shop", "other"))},
			want: `sibling group "web": member "a" generates object "shop/settings" with no kind; set its apiVersion and kind so the group can compare its members' objects`,
		},
		{
			name: "an envelope with no kind, not read by its items",
			a:    []client.Object{object(kindlessEnvelope("shop", "settings", configMapItem("shop", "one")))},
			b:    []client.Object{object(configMap("shop", "other"))},
			want: `sibling group "web": member "a" generates object "shop/settings" with no kind; set its apiVersion and kind so the group can compare its members' objects`,
		},
		{
			name: "an item with no kind that holds items is read by them, and they are compared",
			a:    []client.Object{object(listOf(kindlessEnvelopeItem("shop", "inner", configMapItem("shop", "shared"))))},
			b:    []client.Object{object(configMap("shop", "shared"))},
			want: `sibling group "web": members "a" and "b" both generate ConfigMap "shop/shared"; exactly one member may`,
		},
		{
			name: "an item with no kind that holds items of its own is accepted",
			a:    []client.Object{object(listOf(kindlessEnvelopeItem("shop", "inner", configMapItem("shop", "one"))))},
			b:    []client.Object{object(configMap("shop", "other"))},
		},
		{
			name: "an item with no kind inside an envelope only Flux expands is refused, items or not",
			a:    []client.Object{object(fluxOnlyEnvelope(kindlessEnvelopeItem("shop", "inner", configMapItem("shop", "one"))))},
			b:    []client.Object{object(configMap("shop", "other"))},
			want: `sibling group "web": member "a" generates object "shop/inner" with no kind; set its apiVersion and kind so the group can compare its members' objects`,
		},
		{
			name: "a List of its own from each member",
			a:    []client.Object{object(listOf(configMapItem("shop", "one")))},
			b:    []client.Object{object(listOf(configMapItem("shop", "two")))},
		},
		{
			name: "an empty List from each member",
			a:    []client.Object{object(listOf())},
			b:    []client.Object{object(listOf())},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			objs, err := siblingGroupOf(tc.a, tc.b).Generate(nil)
			if tc.want != "" {
				if err == nil || err.Error() != tc.want {
					t.Fatalf("error = %v\nwant %s", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Each envelope is returned as generated, where it stood: the group
			// compares what a List holds and hands out the List.
			var got []client.Object
			for _, p := range objs {
				if p != nil {
					got = append(got, *p)
				}
			}
			if want := []client.Object{tc.a[0], tc.b[0]}; !slices.Equal(got, want) {
				t.Errorf("group objects = %v, want the two Lists as generated", got)
			}
		})
	}
}
