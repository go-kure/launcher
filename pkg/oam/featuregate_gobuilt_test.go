package oam

import (
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// The gate check reads a value as the writer writes it. A property value built in Go
// from API types holds structs where a decoded document holds maps, and the check's
// map and slice coercers (asObjectValue, asArrayValue) read a struct as a leaf, so a
// gated field set inside it passed a below-release check (go-kure/launcher#1320).

// goBuiltPolicyHandler is a terminal policy handler with two open nodes, as the
// raw-PodSpec kinds have: `template` (a pod template) and `containers` (a list of
// open containers), each naming its gated paths.
type goBuiltPolicyHandler struct{}

func (goBuiltPolicyHandler) CanHandle(policyType string) bool { return policyType == "gobuilt-policy" }
func (goBuiltPolicyHandler) Apply(*ApplicationPolicy, []string, *PolicyResult) error {
	return nil
}
func (goBuiltPolicyHandler) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"template": {
			Type:                 PropertyTypeObject,
			AdditionalProperties: true,
			GatedPaths: map[string]string{
				"spec.hostnameOverride":                "HostnameOverride",
				"spec.containers[].restartPolicyRules": "ContainerRestartRules",
			},
		},
		"containers": {
			Type: PropertyTypeArray,
			Items: &PropertySchema{
				Type:                 PropertyTypeObject,
				AdditionalProperties: true,
				GatedPaths:           map[string]string{"env[].valueFrom.fileKeyRef": "EnvFiles"},
			},
		},
	}
}

func goBuiltPolicyApp(props map[string]any) *Application {
	app := makeApp("gobuilt")
	app.Spec.Policies = []ApplicationPolicy{{Name: "p", Type: "gobuilt-policy", Properties: props}}
	return app
}

func goBuiltTransformer() *Transformer {
	tr := NewTransformer(nil, nil)
	tr.RegisterPolicy("gobuilt-policy", goBuiltPolicyHandler{})
	return tr
}

func strPtr(s string) *string { return &s }

// unencodableSpec is a struct the writer cannot write: a channel has no JSON form.
type unencodableSpec struct {
	C chan int `json:"c"`
}

// TestValidateAuthoredPropertiesForTarget_GoBuiltStructs: a gated field carried in a
// Go-built struct is refused below its release exactly as the same document decoded
// to maps is, and accepted at the release and with no target.
func TestValidateAuthoredPropertiesForTarget_GoBuiltStructs(t *testing.T) {
	fileKeyEnv := []corev1.EnvVar{{Name: "A", ValueFrom: &corev1.EnvVarSource{
		FileKeyRef: &corev1.FileKeySelector{VolumeName: "v", Path: "p", Key: "k"}}}}
	cases := []struct {
		name    string
		props   map[string]any
		refused []string // fragments of the refusal below the release; nil means accepted at every target
	}{
		{name: "a PodSpec in the template map",
			props:   map[string]any{"template": map[string]any{"spec": corev1.PodSpec{HostnameOverride: strPtr("h")}}},
			refused: []string{"HostnameOverride", "properties.template.spec.hostnameOverride"}},
		{name: "a pointer to a PodSpec in the template map",
			props:   map[string]any{"template": map[string]any{"spec": &corev1.PodSpec{HostnameOverride: strPtr("h")}}},
			refused: []string{"HostnameOverride", "properties.template.spec.hostnameOverride"}},
		{name: "a typed slice of EnvVar structs in an open container",
			props:   map[string]any{"containers": []map[string]any{{"name": "c", "env": fileKeyEnv}}},
			refused: []string{"EnvFiles", "properties.containers[0].env[0].valueFrom.fileKeyRef"}},
		{name: "a slice of Container structs two levels down",
			props: map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []corev1.Container{
				{Name: "c", RestartPolicyRules: []corev1.ContainerRestartRule{{Action: corev1.ContainerRestartRuleActionRestart}}}}}}},
			refused: []string{"ContainerRestartRules", "properties.template.spec.containers[0].restartPolicyRules"}},
		{name: "the same document decoded to maps",
			props:   map[string]any{"template": map[string]any{"spec": map[string]any{"hostnameOverride": "h"}}},
			refused: []string{"HostnameOverride", "properties.template.spec.hostnameOverride"}},

		// What passes at every target.
		{name: "a PodSpec that leaves the gated field unset",
			props: map[string]any{"template": map[string]any{"spec": corev1.PodSpec{ServiceAccountName: "sa"}}}},
		{name: "an empty PodSpec", props: map[string]any{"template": map[string]any{"spec": corev1.PodSpec{}}}},
		{name: "a nil PodSpec pointer", props: map[string]any{"template": map[string]any{"spec": (*corev1.PodSpec)(nil)}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := goBuiltTransformer()
			_, err := tr.ValidateAuthoredPropertiesForTarget(goBuiltPolicyApp(c.props), nil, &KubernetesVersion{1, 34})
			if c.refused == nil {
				if err != nil {
					t.Errorf("1.34: want accepted, got %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("1.34: want the gated field refused")
				}
				for _, want := range append([]string{`policy "p" (type "gobuilt-policy")`, "kubernetesVersion is 1.34"}, c.refused...) {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("refusal %q lacks %q", err, want)
					}
				}
			}
			for _, target := range []*KubernetesVersion{{1, 35}, nil} {
				if _, err := tr.ValidateAuthoredPropertiesForTarget(goBuiltPolicyApp(c.props), nil, target); err != nil {
					t.Errorf("target %v: want accepted, got %v", target, err)
				}
			}
		})
	}
}

// TestValidateAuthoredPropertiesForTarget_GoBuiltStructDoesNotEncode: a struct the
// writer cannot write is refused below the release, naming its path, and not read as
// holding nothing; at the release and with no target nothing is read.
func TestValidateAuthoredPropertiesForTarget_GoBuiltStructDoesNotEncode(t *testing.T) {
	props := func() map[string]any {
		return map[string]any{"template": map[string]any{"spec": unencodableSpec{C: make(chan int)}}}
	}
	tr := goBuiltTransformer()
	_, err := tr.ValidateAuthoredPropertiesForTarget(goBuiltPolicyApp(props()), nil, &KubernetesVersion{1, 34})
	if err == nil {
		t.Fatal("1.34: want a struct that does not encode refused")
	}
	for _, want := range []string{"properties.template.spec", "does not encode as JSON"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q lacks %q", err, want)
		}
	}
	for _, target := range []*KubernetesVersion{{1, 35}, nil} {
		if _, err := tr.ValidateAuthoredPropertiesForTarget(goBuiltPolicyApp(props()), nil, target); err != nil {
			t.Errorf("target %v: want accepted, got %v", target, err)
		}
	}
}

// TestValidateAuthoredPropertiesForTarget_GoBuiltDeclaredItems: a struct element
// reached through a declared object Items schema. The authored validation holds a
// declared object to a map or a string-keyed map, so it may refuse the struct before
// the gate pass runs; either way the value does not pass a below-release check.
func TestValidateAuthoredPropertiesForTarget_GoBuiltDeclaredItems(t *testing.T) {
	props := map[string]any{"containers": []corev1.Container{{Name: "c", Env: []corev1.EnvVar{{Name: "A"}}}}}
	_, err := goBuiltTransformer().ValidateAuthoredPropertiesForTarget(goBuiltPolicyApp(props), nil, &KubernetesVersion{1, 34})
	if err == nil {
		t.Fatal("a struct element under a declared object Items schema must not pass at 1.34")
	}
	t.Logf("refused by: %v", err)
}

// TestCheckGatedValue_GoBuiltStruct: the value-level walk reads a struct at its own
// node, not only below a map, with a declared Properties schema and with a gated path.
func TestCheckGatedValue_GoBuiltStruct(t *testing.T) {
	below := KubernetesVersion{1, 34}
	pod := corev1.PodSpec{HostnameOverride: strPtr("h")}

	byProperty := PropertySchema{Type: PropertyTypeObject, Properties: map[string]PropertySchema{
		"hostnameOverride": {Type: PropertyTypeString, Gate: "HostnameOverride"}}}
	if err := checkGatedValue(byProperty, pod, "spec", below); err == nil || !strings.Contains(err.Error(), "spec.hostnameOverride") {
		t.Errorf("declared Properties: want spec.hostnameOverride refused, got %v", err)
	}
	byPath := PropertySchema{Type: PropertyTypeObject, AdditionalProperties: true,
		GatedPaths: map[string]string{"hostnameOverride": "HostnameOverride"}}
	if err := checkGatedValue(byPath, pod, "spec", below); err == nil || !strings.Contains(err.Error(), "spec.hostnameOverride") {
		t.Errorf("GatedPaths: want spec.hostnameOverride refused, got %v", err)
	}
	items := PropertySchema{Type: PropertyTypeArray, Items: &byProperty}
	if err := checkGatedValue(items, []corev1.PodSpec{pod}, "specs", below); err == nil || !strings.Contains(err.Error(), "specs[0].hostnameOverride") {
		t.Errorf("Items: want specs[0].hostnameOverride refused, got %v", err)
	}
	if err := checkGatedValue(byProperty, pod, "spec", KubernetesVersion{1, 35}); err != nil {
		t.Errorf("at the release: want accepted, got %v", err)
	}
	// A struct that does not encode, under a declared Properties schema: refused while
	// a gate below is off, accepted once every gate below is on.
	bad := unencodableSpec{C: make(chan int)}
	if err := checkGatedValue(byProperty, bad, "spec", below); err == nil || !strings.Contains(err.Error(), "spec: ") ||
		!strings.Contains(err.Error(), "does not encode as JSON") {
		t.Errorf("declared Properties, unencodable: want spec refused as not encoding, got %v", err)
	}
	if err := checkGatedValue(byProperty, bad, "spec", KubernetesVersion{1, 35}); err != nil {
		t.Errorf("declared Properties, unencodable, at the release: want accepted, got %v", err)
	}
}

// TestCheckGatedValue_GoBuiltUnencodableUnderPaths: a struct that does not encode,
// met on the way down a GatedPaths or a ValueGatedPaths path, is refused while a gate
// the path reads is off at target and accepted once every such gate is on.
func TestCheckGatedValue_GoBuiltUnencodableUnderPaths(t *testing.T) {
	below, at := KubernetesVersion{1, 34}, KubernetesVersion{1, 35}
	bad := map[string]any{"spec": unencodableSpec{C: make(chan int)}}
	for name, schema := range map[string]PropertySchema{
		"GatedPaths": {Type: PropertyTypeObject, AdditionalProperties: true,
			GatedPaths: map[string]string{"spec.hostnameOverride": "HostnameOverride"}},
		"ValueGatedPaths": {Type: PropertyTypeObject, AdditionalProperties: true,
			ValueGatedPaths: map[string]map[string]string{"spec.hostnameOverride": {"h": "HostnameOverride"}}},
	} {
		if err := checkGatedValue(schema, bad, "t", below); err == nil || !strings.Contains(err.Error(), "does not encode as JSON") {
			t.Errorf("%s at 1.34: want refused as not encoding, got %v", name, err)
		}
		if err := checkGatedValue(schema, bad, "t", at); err != nil {
			t.Errorf("%s at 1.35: want accepted, got %v", name, err)
		}
	}
}

// TestCheckGatedValue_GoBuiltStructEncodesToArrayOrScalar: a struct that encodes to an
// array (a runtime.RawExtension holding a JSON array) or to a scalar is read as that
// array or scalar, on a GatedPaths array step, at the end of a ValueGatedPaths path and
// on a ValueGates property; one that does not encode is refused at the same places
// while a gate is off.
func TestCheckGatedValue_GoBuiltStructEncodesToArrayOrScalar(t *testing.T) {
	below, at := KubernetesVersion{1, 34}, KubernetesVersion{1, 35}
	raw := func(s string) runtime.RawExtension { return runtime.RawExtension{Raw: []byte(s)} }
	bad := unencodableSpec{C: make(chan int)}
	valueGated := PropertySchema{Type: PropertyTypeString, Enum: []any{"Never", "Always"},
		ValueGates: map[string]string{"Never": "ContainerRestartRules"}}
	for name, tc := range map[string]struct {
		schema PropertySchema
		value  func(leaf any) any
		leaf   any
		want   string
	}{
		"array step": {
			PropertySchema{Type: PropertyTypeObject, AdditionalProperties: true,
				GatedPaths: map[string]string{"containers[].restartPolicyRules": "ContainerRestartRules"}},
			func(any) any {
				return map[string]any{"containers": raw(`[{"name":"app","restartPolicyRules":[{}]}]`)}
			},
			nil, "t.containers[0].restartPolicyRules",
		},
		"value path end": {
			PropertySchema{Type: PropertyTypeObject, AdditionalProperties: true,
				ValueGatedPaths: map[string]map[string]string{"restartPolicy": {"Never": "ContainerRestartRules"}}},
			func(any) any { return map[string]any{"restartPolicy": raw(`"Never"`)} },
			nil, `value "Never"`,
		},
		"value gates property": {valueGated, func(any) any { return raw(`"Never"`) }, nil, `value "Never"`},
	} {
		if err := checkGatedValue(tc.schema, tc.value(tc.leaf), "t", below); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s at 1.34: want refused with %q, got %v", name, tc.want, err)
		}
		if err := checkGatedValue(tc.schema, tc.value(tc.leaf), "t", at); err != nil {
			t.Errorf("%s at 1.35: want accepted, got %v", name, err)
		}
	}
	arrayStep := PropertySchema{Type: PropertyTypeObject, AdditionalProperties: true,
		GatedPaths: map[string]string{"containers[].restartPolicyRules": "ContainerRestartRules"}}
	for name, tc := range map[string]struct {
		schema PropertySchema
		value  any
	}{
		"array step":     {arrayStep, map[string]any{"containers": bad}},
		"value gates":    {valueGated, bad},
		"value path end": {PropertySchema{Type: PropertyTypeObject, AdditionalProperties: true, ValueGatedPaths: map[string]map[string]string{"restartPolicy": {"Never": "ContainerRestartRules"}}}, map[string]any{"restartPolicy": bad}},
	} {
		if err := checkGatedValue(tc.schema, tc.value, "t", below); err == nil || !strings.Contains(err.Error(), "does not encode as JSON") {
			t.Errorf("%s unencodable at 1.34: want refused as not encoding, got %v", name, err)
		}
		if err := checkGatedValue(tc.schema, tc.value, "t", at); err != nil {
			t.Errorf("%s unencodable at 1.35: want accepted, got %v", name, err)
		}
	}
}

// TestCheckGatedValue_GoBuiltStructEncodingToNullIsAbsent: a struct that encodes to JSON
// null (the zero runtime.RawExtension) uses no gate, as a decoded null uses none.
func TestCheckGatedValue_GoBuiltStructEncodingToNullIsAbsent(t *testing.T) {
	below := KubernetesVersion{1, 34}
	null := runtime.RawExtension{}
	var nilRaw *runtime.RawExtension
	for name, tc := range map[string]struct {
		schema PropertySchema
		value  any
	}{
		"own gate": {PropertySchema{Type: PropertyTypeObject, Gate: "HostnameOverride"}, null},
		"gated path": {PropertySchema{Type: PropertyTypeObject, AdditionalProperties: true,
			GatedPaths: map[string]string{"hostnameOverride": "HostnameOverride"}},
			map[string]any{"hostnameOverride": null}},
		"value gated path": {PropertySchema{Type: PropertyTypeObject, AdditionalProperties: true,
			ValueGatedPaths: map[string]map[string]string{"restartPolicy": {"Never": "ContainerRestartRules"}}},
			map[string]any{"restartPolicy": null}},
		"pointer to a nil pointer": {PropertySchema{Type: PropertyTypeObject, AdditionalProperties: true,
			GatedPaths: map[string]string{"hostnameOverride": "HostnameOverride"}},
			map[string]any{"hostnameOverride": &nilRaw}},
		"nil pointer": {PropertySchema{Type: PropertyTypeObject, Gate: "HostnameOverride"}, nilRaw},
	} {
		if err := checkGatedValue(tc.schema, tc.value, "spec", below); err != nil {
			t.Errorf("%s: a struct encoding to null must be absent, got %v", name, err)
		}
	}
}

// ptrMarshaled has its JSON form on the pointer type: the writer marshals the elements of
// a typed slice in place, so it calls MarshalJSON, and the gate check must read the same.
type ptrMarshaled struct {
	HostnameOverride string `json:"hostnameOverride"`
}

func (*ptrMarshaled) MarshalJSON() ([]byte, error) {
	return []byte(`{"hostnameOverride":"from-marshaler"}`), nil
}

// ptrMarshaledNull encodes to null through its pointer-type MarshalJSON.
type ptrMarshaledNull struct {
	HostnameOverride string `json:"hostnameOverride"`
}

func (*ptrMarshaledNull) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

// TestCheckGatedValue_GoBuiltSliceKeepsPointerMarshaler: a typed slice of structs is
// converted whole, so an element's MarshalJSON on the pointer type applies as it does
// when the writer marshals the slice.
func TestCheckGatedValue_GoBuiltSliceKeepsPointerMarshaler(t *testing.T) {
	below := KubernetesVersion{1, 34}
	schema := PropertySchema{Type: PropertyTypeObject, AdditionalProperties: true,
		GatedPaths: map[string]string{"entries[].hostnameOverride": "HostnameOverride"}}
	// The struct's field is empty and its marshaler supplies the gated key: refused.
	got := checkGatedValue(schema, map[string]any{"entries": []ptrMarshaled{{}}}, "t", below)
	if got == nil || !strings.Contains(got.Error(), "t.entries[0].hostnameOverride") {
		t.Errorf("a gated key supplied by the pointer marshaler: want refused, got %v", got)
	}
	// The struct sets the gated field and its marshaler writes null: absent.
	if err := checkGatedValue(schema, map[string]any{"entries": []ptrMarshaledNull{{HostnameOverride: "h"}}}, "t", below); err != nil {
		t.Errorf("an element the marshaler writes as null: want accepted, got %v", err)
	}
}

// TestWrittenForm: a struct is read back from its JSON as a map, keeping the number
// text (a 64-bit integer survives), and a value that does not encode is an error.
func TestWrittenForm(t *testing.T) {
	type big struct {
		N uint64 `json:"n"`
	}
	got, err := writtenForm(big{N: 18446744073709551615})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("writtenForm = %T, want map[string]any", got)
	}
	if n, ok := m["n"].(json.Number); !ok || n.String() != "18446744073709551615" {
		t.Errorf("n = %#v, want json.Number 18446744073709551615", m["n"])
	}
	if _, err := writtenForm(unencodableSpec{C: make(chan int)}); err == nil {
		t.Error("a struct holding a channel must be an error")
	}
}
