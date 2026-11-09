package patch

import (
	"reflect"
	"strings"
	"testing"
)

// collidingKeys is a patch value whose two keys both read "16" after conversion.
func collidingKeys() map[interface{}]interface{} {
	return map[interface{}]interface{}{0x10: "hexadecimal", "16": "string"}
}

// collisionTarget builds the resource every row patches. A row compares it with a second
// copy from the same function, so a change shows even where the patch returned an error.
func collisionTarget() map[string]any {
	return map[string]any{"spec": map[string]any{
		"keys": map[string]any{},
		"list": []any{
			map[string]any{"name": "a"},
			map[string]any{"name": "b"},
		},
	}}
}

const collisionText = `both read "16"`

func TestApplyPatchOp_RefusesCollidingKeysInPatchValue(t *testing.T) {
	rows := []struct {
		name string
		op   PatchOp
	}{
		{"replace", PatchOp{Op: "replace", Path: "spec.keys", Value: collidingKeys()}},
		{"replace_in_a_map", PatchOp{Op: "replace", Path: "spec.keys", Value: map[string]any{"inner": collidingKeys()}}},
		{"replace_in_a_slice", PatchOp{Op: "replace", Path: "spec.keys", Value: []any{collidingKeys()}}},
		{"append", PatchOp{Op: "append", Path: "spec.list", Value: collidingKeys()}},
		{"insert_before", PatchOp{Op: "insertBefore", Path: "spec.list", Selector: "name=b", Value: collidingKeys()}},
		{"insert_after", PatchOp{Op: "insertAfter", Path: "spec.list", Selector: "name=a", Value: collidingKeys()}},
		{"array_item", PatchOp{Op: "replace", Path: "spec.list", Selector: "name=a", Value: collidingKeys()}},
		{"array_nested_field", PatchOp{Op: "replace", Path: "spec.list", Selector: "name=a", Value: map[string]any{"keys": collidingKeys()}}},
	}
	for _, tt := range rows {
		t.Run(tt.name, func(t *testing.T) {
			obj := collisionTarget()
			err := applyPatchOp(obj, tt.op)
			if err == nil {
				t.Fatalf("applyPatchOp accepted a value with keys 0x10 and \"16\"; resource: %v", obj)
			}
			if !strings.Contains(err.Error(), collisionText) {
				t.Errorf("error does not name the collision: %v", err)
			}
			if !reflect.DeepEqual(obj, collisionTarget()) {
				t.Errorf("a refused patch changed the resource: %v", obj)
			}
		})
	}
}

// A second list selector reaches the sites through the recursion in applyArrayReplace.
func TestApplyPatchOp_RefusesCollidingKeysBehindASecondSelector(t *testing.T) {
	target := func() map[string]any {
		return map[string]any{"spec": map[string]any{"outer": []any{
			map[string]any{"name": "o", "inner": []any{map[string]any{"name": "i"}}},
		}}}
	}
	for _, key := range []string{
		"spec.outer[name=o].inner[name=i]",
		"spec.outer[name=o].inner[-]",
	} {
		t.Run(key, func(t *testing.T) {
			op, err := ParsePatchLine(key, collidingKeys())
			if err != nil {
				t.Fatalf("ParsePatchLine: %v", err)
			}
			obj := target()
			err = applyPatchOp(obj, op)
			if err == nil {
				t.Fatalf("applyPatchOp accepted a value with keys 0x10 and \"16\"; resource: %v", obj)
			}
			if !strings.Contains(err.Error(), collisionText) {
				t.Errorf("error does not name the collision: %v", err)
			}
			if !reflect.DeepEqual(obj, target()) {
				t.Errorf("a refused patch changed the resource: %v", obj)
			}
		})
	}
}

const collidingPatchFile = "- target: keys\n  patch:\n    spec.keys:\n      0x10: hexadecimal\n      \"16\": string\n"

const collidingTargetedPatchFile = "- target: keys\n  patch:\n    spec.list[name=a]:\n      keys:\n        0x10: hexadecimal\n        \"16\": string\n"

func TestLoadedPatch_RefusesCollidingKeys(t *testing.T) {
	for name, src := range map[string]string{
		"flat":     collidingPatchFile,
		"targeted": collidingTargetedPatchFile,
	} {
		t.Run(name, func(t *testing.T) {
			patches, err := LoadPatchFile(strings.NewReader(src))
			if err != nil || len(patches) != 1 {
				t.Fatalf("LoadPatchFile: n=%d err=%v", len(patches), err)
			}
			obj := collisionTarget()
			err = applyPatchOp(obj, patches[0].Patch)
			if err == nil {
				t.Fatalf("applyPatchOp accepted a loaded value with keys 0x10 and \"16\"; resource: %v", obj)
			}
			if !strings.Contains(err.Error(), collisionText) {
				t.Errorf("error does not name the collision: %v", err)
			}
			if !reflect.DeepEqual(obj, collisionTarget()) {
				t.Errorf("a refused patch changed the resource: %v", obj)
			}
		})
	}
}

func TestApplyPatchesToDocument_RefusesCollidingKeys(t *testing.T) {
	const src = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: keys\nspec:\n  keys: {}\n"
	doc := loadOneDocument(t, src)
	before := writtenBack(t, doc.Node)
	err := doc.ApplyPatchesToDocument([]PatchOp{{Op: "replace", Path: "spec.keys", Value: collidingKeys()}})
	if err == nil {
		t.Fatalf("ApplyPatchesToDocument accepted a value with keys 0x10 and \"16\"; document:\n%s", writtenBack(t, doc.Node))
	}
	if !strings.Contains(err.Error(), collisionText) {
		t.Errorf("error does not name the collision: %v", err)
	}
	if got := writtenBack(t, doc.Node); got != before {
		t.Errorf("a refused patch changed the document:\n%s", got)
	}
}

func TestConvertValueForUnstructured_ReportsACollision(t *testing.T) {
	got, err := convertValueForUnstructured(collidingKeys())
	if err == nil {
		t.Fatalf("convertValueForUnstructured accepted keys 0x10 and \"16\": %v", got)
	}
	if !strings.Contains(err.Error(), collisionText) {
		t.Errorf("error does not name the collision: %v", err)
	}
	if got != nil {
		t.Errorf("a refused value came back as %v, want nil", got)
	}
}

// Two non-string keys that read differently both apply.
func TestApplyPatchOp_AppliesNonCollidingNonStringKeys(t *testing.T) {
	obj := collisionTarget()
	op := PatchOp{Op: "replace", Path: "spec.keys", Value: map[interface{}]interface{}{1: "one", 2: "two"}}
	if err := applyPatchOp(obj, op); err != nil {
		t.Fatalf("applyPatchOp: %v", err)
	}
	requireString(t, obj, "one", "spec", "keys", "1")
	requireString(t, obj, "two", "spec", "keys", "2")
}
