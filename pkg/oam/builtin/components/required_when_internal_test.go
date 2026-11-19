package components

import "testing"

// TestRequiredWhen_EveryNestedOnlyVolumeSourceHasItsRule: a volume source type the
// handler takes only in its nested form (nestedOnlyVolumeSources) is published as
// required when the volume's type names it. A type added to that list without a
// property of its own in schemaVolumes fails here, rather than going unpublished.
func TestRequiredWhen_EveryNestedOnlyVolumeSourceHasItsRule(t *testing.T) {
	props := schemaVolumes().Items.Properties
	for _, typ := range nestedOnlyVolumeSources {
		field, ok := props[typ]
		if !ok {
			t.Errorf("volume source %q is nested-only and has no property in schemaVolumes", typ)
			continue
		}
		if rw := field.RequiredWhen; rw == nil || rw.Key != "type" || rw.Equals != typ {
			t.Errorf("volume source %q: requiredWhen = %+v, want {type, %q}", typ, rw, typ)
		}
	}
	for key, field := range props {
		if field.RequiredWhen == nil || key == "volumeMode" {
			continue
		}
		found := false
		for _, typ := range nestedOnlyVolumeSources {
			found = found || typ == key
		}
		if !found {
			t.Errorf("schemaVolumes property %q carries a requiredWhen but is not in nestedOnlyVolumeSources", key)
		}
	}
}
