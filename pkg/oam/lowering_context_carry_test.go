package oam

import (
	"reflect"
	"testing"
)

// TestLoweringContext_CarriesEveryFieldARuleReads: loweringContext builds the
// LoweringContext every lowering call starts from. Every field of TransformContext
// is classified here, so a field added later fails this test until it is decided
// whether a rule reads it; a field a rule reads is carried, and carried as the same
// object, not a copy.
func TestLoweringContext_CarriesEveryFieldARuleReads(t *testing.T) {
	carried := map[string]bool{
		"Capabilities":         true,
		"consumedCapabilities": true,
		"imageLevel":           true,
		"sourceLevel":          true,
	}
	notRead := map[string]string{
		"ClusterID":                "generation input; LoweringContext has no accessor for it",
		"TenantID":                 "generation input; LoweringContext has no accessor for it",
		"Environment":              "generation input; LoweringContext has no accessor for it",
		"AppVersion":               "generation input; LoweringContext has no accessor for it",
		"TeamID":                   "generation input; LoweringContext has no accessor for it",
		"Namespace":                "generation input; LoweringContext has no accessor for it",
		"FluxNamespace":            "generation input; LoweringContext has no accessor for it",
		"GroupsWaitForFluxObjects": "generation input; LoweringContext has no accessor for it",
		"Policy":                   "a rule reads the two levels derived from it (imageLevel, sourceLevel), not the policy",
		"TargetVersion":            "generation input; LoweringContext has no accessor for it",
		"FeatureGates":             "generation input; LoweringContext has no accessor for it",
		"EgressPeers":              "generation input; LoweringContext has no accessor for it",
		"IngressPeers":             "generation input; LoweringContext has no accessor for it",
		"ComponentLabelKey":        "generation input; LoweringContext has no accessor for it",
		"Domain":                   "generation input; LoweringContext has no accessor for it",
		"ReservedMetadataKeys":     "generation input; LoweringContext has no accessor for it",
		"Naming":                   "travels on the run's NameAllocator (hook), which each site sets as Namer",
		"EmitHook":                 "travels on the run's NameAllocator (emit), which each site sets as Namer",
		"names":                    "engine state of the generation step",
		"nameClaims":               "the run's NameAllocator, set at each site as Namer",
		"provenance":               "engine state of TransformWithProvenance",
		"subAppDecorations":        "engine state of the generation step",
		"traitSubApps":             "engine state of the generation step",
		"decoratorFills":           "engine state of the generation step",
		"generatedSourceReaders":   "engine state of the generation step",
		"subAppOrigins":            "engine state of the generation step",
	}

	rt := reflect.TypeOf(TransformContext{})
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		if carried[name] {
			continue
		}
		if _, ok := notRead[name]; !ok {
			t.Errorf("TransformContext.%s is not classified: decide whether a rule reads it, then carry it in loweringContext or add it to notRead with the reason", name)
		}
	}
	for name := range carried {
		if _, ok := rt.FieldByName(name); !ok {
			t.Errorf("carried field %q no longer exists in TransformContext", name)
		}
	}
	for name := range notRead {
		if _, ok := rt.FieldByName(name); !ok {
			t.Errorf("notRead field %q no longer exists in TransformContext", name)
		}
	}

	// Each carried field arrives with its own value. Reflection cannot set an
	// unexported field, so the values are set directly.
	consumed := map[string]struct{}{}
	ctx := TransformContext{
		Capabilities:         map[string]CapabilityBinding{"cap": {Rendering: map[string]any{"k": "v"}}},
		consumedCapabilities: consumed,
		imageLevel:           ImageLevelDigest,
		sourceLevel:          ImageLevelVersion,
	}
	lctx := ctx.loweringContext()

	binding, ok := lctx.Capability("cap")
	if !ok || binding.Rendering["k"] != "v" {
		t.Errorf("Capabilities not carried: Capability(cap) = %v, %v", binding, ok)
	}
	// The consumed set is the same map: a copy would lose every read a rule makes.
	if _, ok := consumed["cap"]; !ok {
		t.Error("consumedCapabilities not carried as the same map: a read through the context did not reach it")
	}
	if got := lctx.ImageLevel(); got != ImageLevelDigest {
		t.Errorf("imageLevel not carried: got %q, want %q", got, ImageLevelDigest)
	}
	if got := lctx.SourceLevel(); got != ImageLevelVersion {
		t.Errorf("sourceLevel not carried: got %q, want %q", got, ImageLevelVersion)
	}

	// Nothing of the element being lowered comes from the transform context.
	if lctx.Document != nil || lctx.Component != nil || lctx.Namer != nil || !reflect.ValueOf(lctx.Origin).IsZero() {
		t.Errorf("loweringContext set a field of the element being lowered: %+v", lctx)
	}
}
