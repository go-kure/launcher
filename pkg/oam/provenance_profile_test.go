package oam

import (
	"maps"
	"reflect"
	"testing"
)

// provVADHandler is a trait handler whose ValidateAndApplyDefaults rewrites the
// rendering it is handed in place, as the built-in handlers do: an empty "kind"
// becomes a default, and an absent "retries" is added.
type provVADHandler struct{ stubTraitHandler }

func (h *provVADHandler) CapabilityRequired() bool { return false }
func (h *provVADHandler) ValidateAndApplyDefaults(r map[string]any) (map[string]any, error) {
	if k, ok := r["kind"].(string); ok && k == "" {
		r["kind"] = "ClusterIssuer"
	}
	if _, ok := r["retries"]; !ok {
		r["retries"] = 3
	}
	return r, nil
}

// provVADRule is the trait-position lowering rule counterpart.
type provVADRule struct{ typ string }

func (r provVADRule) TraitType() string { return r.typ }
func (r provVADRule) LowerTrait(*Trait, LoweringContext) (LoweringResult, error) {
	return LoweringResult{}, nil
}
func (r provVADRule) ValidateAndApplyDefaults(rendering map[string]any) (map[string]any, error) {
	if _, ok := rendering["retries"]; !ok {
		rendering["retries"] = 3
	}
	return rendering, nil
}

func provCapDef(name string) *CapabilityDefinition {
	return &CapabilityDefinition{
		APIVersion: "launcher.gokure.dev/v1alpha1",
		Kind:       "CapabilityDefinition",
		Metadata:   Metadata{Name: name},
		Spec: CapabilityDefSpec{Rendering: CapabilityRenderingSchema{Properties: map[string]PropertySchema{
			"timeout": {Type: "integer", Required: true},
			"mode":    {Type: "string", Default: "auto"},
			"kind":    {Type: "string"},
			"retries": {Type: "integer"},
		}}},
	}
}

func provProfile() *ClusterProfile {
	return &ClusterProfile{Spec: ClusterProfileSpec{Capabilities: map[string]CapabilityBinding{
		// timeout from the profile, mode from the definition, kind rewritten from the
		// profile's empty string by the handler, retries added by the handler.
		"custom": {Rendering: map[string]any{"timeout": float64(30), "kind": ""}},
		// the rule branch: retries added by the rule, mode by the definition.
		"custom-rule": {Rendering: map[string]any{"timeout": float64(5)}},
		// no handler, no rule: nothing touches it.
		"plain": {Rendering: map[string]any{"a": "x", "b": map[string]any{"c": 1}}},
	}}}
}

func provProfileTransformer() *Transformer {
	tr := NewTransformer(nil, nil)
	tr.RegisterTrait("custom", &provVADHandler{stubTraitHandler{typ: "custom"}})
	tr.RegisterTraitLowering(provVADRule{typ: "custom-rule"})
	tr.SetCapabilityDefs(map[string]*CapabilityDefinition{
		"custom":      provCapDef("custom"),
		"custom-rule": provCapDef("custom-rule"),
	})
	return tr
}

// TestProvenance_ProfileEvaluation: the opt-in evaluation stamps each leaf with the
// step that supplied it, a handler rewrite of a profile-given leaf counts as the
// handler's, and the plain call stamps nothing and returns the same renderings
// (test 10).
func TestProvenance_ProfileEvaluation(t *testing.T) {
	stamped, err := provProfileTransformer().EvaluateProfileWithProvenance(provProfile())
	if err != nil {
		t.Fatalf("EvaluateProfileWithProvenance: %v", err)
	}
	plain, err := provProfileTransformer().EvaluateProfile(provProfile())
	if err != nil {
		t.Fatalf("EvaluateProfile: %v", err)
	}
	want := map[string]map[string]string{
		"custom": {
			"timeout": detailProfile,
			"mode":    detailSchemaDefault,
			"kind":    detailHandlerDefault,
			"retries": detailHandlerDefault,
		},
		"custom-rule": {
			"timeout": detailProfile,
			"mode":    detailSchemaDefault,
			"retries": detailHandlerDefault,
		},
		"plain": {"a": detailProfile, "b.c": detailProfile},
	}
	for key, w := range want {
		if got := stamped.Spec.Capabilities[key].detail; !reflect.DeepEqual(got, w) {
			t.Errorf("%s: detail %v, want %v", key, got, w)
		}
		if d := plain.Spec.Capabilities[key].detail; d != nil {
			t.Errorf("%s: the plain call stamped %v", key, d)
		}
		if !reflect.DeepEqual(stamped.Spec.Capabilities[key].Rendering, plain.Spec.Capabilities[key].Rendering) {
			t.Errorf("%s: renderings differ: %v vs %v", key, stamped.Spec.Capabilities[key].Rendering, plain.Spec.Capabilities[key].Rendering)
		}
	}
}

// provTypedVADHandler changes typed collections in place, as a handler may.
type provTypedVADHandler struct{ stubTraitHandler }

func (h *provTypedVADHandler) CapabilityRequired() bool { return false }
func (h *provTypedVADHandler) ValidateAndApplyDefaults(r map[string]any) (map[string]any, error) {
	r["labels"].(map[string]string)["b"] = "y"
	r["tags"].([]string)[0] = "z"
	// A null inside a typed collection, replaced by a default.
	r["nested"].(map[string][]string)["tags"] = []string{"default"}
	return r, nil
}

// TestProvenance_ProfileTypedCollectionMutation: a handler that changes a typed map
// or slice leaf in place still has the leaf attributed to it, because the snapshots
// taken before the step are copies, not references (review finding on the snapshot).
func TestProvenance_ProfileTypedCollectionMutation(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterTrait("typed", &provTypedVADHandler{stubTraitHandler{typ: "typed"}})
	profile := &ClusterProfile{Spec: ClusterProfileSpec{Capabilities: map[string]CapabilityBinding{
		"typed": {Rendering: map[string]any{
			"labels": map[string]string{"a": "x"},
			"tags":   []string{"a"},
			"nested": map[string][]string{"tags": nil},
			"plain":  "p",
		}},
	}}}
	got, err := tr.EvaluateProfileWithProvenance(profile)
	if err != nil {
		t.Fatalf("EvaluateProfileWithProvenance: %v", err)
	}
	want := map[string]string{
		"labels": detailHandlerDefault, "tags": detailHandlerDefault,
		"nested": detailHandlerDefault, "plain": detailProfile,
	}
	if d := got.Spec.Capabilities["typed"].detail; !reflect.DeepEqual(d, want) {
		t.Errorf("detail %v, want %v", d, want)
	}
}

// TestProvenance_PlainEvaluationClearsStamps: a profile the opt-in call stamped comes
// back unstamped from the plain call, for a binding no step touches too (review
// finding on a retained binding.detail).
func TestProvenance_PlainEvaluationClearsStamps(t *testing.T) {
	tr := provProfileTransformer()
	stamped, err := tr.EvaluateProfileWithProvenance(provProfile())
	if err != nil {
		t.Fatalf("EvaluateProfileWithProvenance: %v", err)
	}
	if stamped.Spec.Capabilities["plain"].detail == nil {
		t.Fatal("the opt-in call stamped nothing on the untouched binding; the test would prove nothing")
	}
	again, err := tr.EvaluateProfile(stamped)
	if err != nil {
		t.Fatalf("EvaluateProfile: %v", err)
	}
	for key, b := range again.Spec.Capabilities {
		if b.detail != nil {
			t.Errorf("%s: the plain call kept the stamp %v", key, b.detail)
		}
	}
}

// TestProvenance_StampForNormalizedDescendants: a leaf the component schema check
// expanded from a typed collection the profile stamped as one leaf takes the stamp
// of its nearest stamped ancestor; an unstamped path stays empty (review finding on
// normalized descendants).
func TestProvenance_StampForNormalizedDescendants(t *testing.T) {
	detail := map[string]string{"class": detailProfile, "class.deep": detailHandlerDefault, "other": detailSchemaDefault}
	for path, want := range map[string]string{
		"class":        detailProfile,
		"class.tier":   detailProfile,
		"class[0]":     detailProfile,
		"class.deep.x": detailHandlerDefault,
		"classy":       "",
		"missing.x":    "",
	} {
		if got := stampFor(detail, path); got != want {
			t.Errorf("stampFor(%q) = %q, want %q", path, got, want)
		}
	}
	if got := stampFor(nil, "class.tier"); got != "" {
		t.Errorf("stampFor on no stamp = %q, want empty", got)
	}
}

// TestProvenance_TypedFillKeepsStampAfterNormalization: end to end, the component
// schema turns a profile's map[string]string at class into class.tier, and the
// report keeps the profile's stamp on it.
func TestProvenance_TypedFillKeepsStampAfterNormalization(t *testing.T) {
	h := &objectDefaultsHandler{keyedDefaultsHandler{key: "store", keys: []string{"class"}}}
	tr := NewTransformer(map[string]ComponentHandler{"store": h}, nil)
	profile := &ClusterProfile{Spec: ClusterProfileSpec{Capabilities: map[string]CapabilityBinding{
		"store": {Rendering: map[string]any{"class": map[string]string{"tier": "fast"}}},
	}}}
	evaluated, err := tr.EvaluateProfileWithProvenance(profile)
	if err != nil {
		t.Fatalf("EvaluateProfileWithProvenance: %v", err)
	}
	app := storeApp(Component{Name: "data", Type: "store", Properties: map[string]any{}})
	_, _, prov, err := tr.TransformWithProvenance(app, TransformContext{Capabilities: evaluated.Spec.Capabilities})
	if err != nil {
		t.Fatalf("TransformWithProvenance: %v", err)
	}
	want := map[string]Source{"class.tier": {Kind: SourceCapability, Ref: "store", Detail: detailProfile}}
	if got := entriesFor(prov, "data", "", ""); !reflect.DeepEqual(got, want) {
		t.Errorf("entries\n got %v\nwant %v", got, want)
	}
}

// provMixedHandler fills the object-valued "class" key from the "store" capability
// at the top level and a nested "volumes[0].storageClass" from the "pvc" one.
type provMixedHandler struct{ provComponentHandler }

func (provMixedHandler) CapabilityDefaults() (string, []string) { return "store", []string{"class"} }
func (provMixedHandler) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{"size": {Type: PropertyTypeString}, "class": {}, "volumes": {}}
}
func (provMixedHandler) FillCapabilityDefaults(props map[string]any, lctx LoweringContext) (map[string]any, error) {
	b, ok := lctx.Capability("pvc")
	if !ok {
		return props, nil
	}
	out := maps.Clone(props)
	out["volumes"] = []any{map[string]any{"storageClass": b.Rendering["storageClassName"]}}
	return out, nil
}

type provMixedStoreTrait struct{ stubTraitHandler }

func (h *provMixedStoreTrait) CapabilityRequired() bool { return false }
func (h *provMixedStoreTrait) ValidateAndApplyDefaults(r map[string]any) (map[string]any, error) {
	if c, ok := r["class"].(map[string]any); ok {
		if _, has := c["kind"]; !has {
			c["kind"] = "Default"
		}
	}
	return r, nil
}

func provMixedTransformer(c *provCapture) *Transformer {
	return NewTransformer(
		map[string]ComponentHandler{"mixed": provMixedHandler{provComponentHandler{typ: "mixed", c: c}}},
		map[string]TraitHandler{"store": &provMixedStoreTrait{stubTraitHandler{typ: "store"}}})
}

// TestProvenance_ProfileDetailInComponents: a top-level fill from an evaluated
// profile carries the stamp of each leaf, an object-valued fill whose leaves have
// different origins carries each one's own, and a nested-fill leaf has no Detail
// even from the same evaluated profile (test 13b). A profile evaluated with the plain
// call gives no Detail anywhere.
func TestProvenance_ProfileDetailInComponents(t *testing.T) {
	profile := func() *ClusterProfile {
		return &ClusterProfile{Spec: ClusterProfileSpec{Capabilities: map[string]CapabilityBinding{
			"store": {Rendering: map[string]any{"class": map[string]any{"name": "platform"}}},
			"pvc":   {Rendering: map[string]any{"storageClassName": "ssd"}},
		}}}
	}
	build := func() (*Transformer, *Application, *provCapture) {
		c := newProvCapture()
		tr := provMixedTransformer(c)
		return tr, provApp(Component{Name: "m", Type: "mixed", Properties: map[string]any{"size": "1Gi"}}), c
	}
	run := func(opt bool) map[string]Source {
		tr, app, c := build()
		var evaluated *ClusterProfile
		var err error
		if opt {
			evaluated, err = tr.EvaluateProfileWithProvenance(profile())
		} else {
			evaluated, err = tr.EvaluateProfile(profile())
		}
		if err != nil {
			t.Fatalf("evaluate (opt-in %v): %v", opt, err)
		}
		_, _, prov, err := tr.TransformWithProvenance(app, TransformContext{Capabilities: evaluated.Spec.Capabilities})
		if err != nil {
			t.Fatalf("TransformWithProvenance (opt-in %v): %v", opt, err)
		}
		// The nested and top-level fills are part of the fixture, so the report is
		// held to the properties the handler received.
		checkComplete(t, prov, c, authoredExpect(app))
		return entriesFor(prov, "m", "", "")
	}

	got := run(true)
	want := map[string]Source{
		"size":                    {Kind: SourceAuthored},
		"class.name":              {Kind: SourceCapability, Ref: "store", Detail: detailProfile},
		"class.kind":              {Kind: SourceCapability, Ref: "store", Detail: detailHandlerDefault},
		"volumes[0].storageClass": {Kind: SourceCapability, Ref: "pvc"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("opt-in evaluation: entries\n got %v\nwant %v", got, want)
	}

	plain := run(false)
	for p, s := range plain {
		if s.Detail != "" {
			t.Errorf("plain evaluation: %s carries Detail %q", p, s.Detail)
		}
	}
	// The same paths and kinds, whichever call evaluated the profile.
	for p, s := range got {
		s.Detail = ""
		if plain[p] != s {
			t.Errorf("plain evaluation: %s = %+v, want %+v", p, plain[p], s)
		}
	}
	if len(plain) != len(got) {
		t.Errorf("plain evaluation reports %d entries, opt-in %d", len(plain), len(got))
	}
}
