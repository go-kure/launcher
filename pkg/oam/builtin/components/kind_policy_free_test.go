package components_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	nodev1 "k8s.io/api/node/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The kind components of go-kure/launcher#790 to which no dimension of the
// environment policy applies, built on one shared helper (policyFreeKind): the
// cluster-scoped classes and the CSIDriver.

// policyFreeKinds lists them. typ is the type the properties decode into: the
// object itself for a kind with no spec type (wholeObject), its spec type
// otherwise. minimal is the least a component may author, full a value of
// every top-level field.
var policyFreeKinds = []struct {
	component   string
	handler     oam.ComponentHandler
	gvk         schema.GroupVersionKind
	typ         reflect.Type
	wholeObject bool
	minimal     map[string]any
	full        map[string]any
}{
	{
		component: "storageclass", handler: &components.StorageClassHandler{},
		gvk: storagev1.SchemeGroupVersion.WithKind("StorageClass"),
		typ: reflect.TypeFor[storagev1.StorageClass](), wholeObject: true,
		minimal: map[string]any{"provisioner": "ebs.csi.aws.com"},
		full: map[string]any{
			"provisioner":          "ebs.csi.aws.com",
			"parameters":           map[string]any{"type": "gp3", "encrypted": "true"},
			"reclaimPolicy":        "Retain",
			"mountOptions":         []any{"noatime", "discard"},
			"allowVolumeExpansion": false,
			"volumeBindingMode":    "WaitForFirstConsumer",
			"allowedTopologies": []any{map[string]any{"matchLabelExpressions": []any{
				map[string]any{"key": "topology.kubernetes.io/zone", "values": []any{"eu-west-1a", "eu-west-1b"}},
			}}},
		},
	},
	{
		component: "volumeattributesclass", handler: &components.VolumeAttributesClassHandler{},
		gvk: storagev1.SchemeGroupVersion.WithKind("VolumeAttributesClass"),
		typ: reflect.TypeFor[storagev1.VolumeAttributesClass](), wholeObject: true,
		minimal: map[string]any{"driverName": "ebs.csi.aws.com", "parameters": map[string]any{"iops": "3000"}},
		full: map[string]any{
			"driverName": "ebs.csi.aws.com",
			"parameters": map[string]any{"iops": "6000", "throughput": "250"},
		},
	},
	{
		component: "priorityclass", handler: &components.PriorityClassHandler{},
		gvk: schedulingv1.SchemeGroupVersion.WithKind("PriorityClass"),
		typ: reflect.TypeFor[schedulingv1.PriorityClass](), wholeObject: true,
		full: map[string]any{
			"value":            1000000,
			"globalDefault":    true,
			"description":      "Workloads that must not be preempted.",
			"preemptionPolicy": "Never",
		},
	},
	{
		component: "runtimeclass", handler: &components.RuntimeClassHandler{},
		gvk: nodev1.SchemeGroupVersion.WithKind("RuntimeClass"),
		typ: reflect.TypeFor[nodev1.RuntimeClass](), wholeObject: true,
		minimal: map[string]any{"handler": "runc"},
		full: map[string]any{
			"handler":  "kata",
			"overhead": map[string]any{"podFixed": map[string]any{"cpu": "250m", "memory": 134217728}},
			"scheduling": map[string]any{
				"nodeSelector": map[string]any{"runtime": "kata"},
				"tolerations": []any{
					map[string]any{"key": "runtime", "operator": "Equal", "value": "kata", "effect": "NoSchedule"},
				},
			},
		},
	},
	{
		component: "ingressclass", handler: &components.IngressClassHandler{},
		gvk: networkingv1.SchemeGroupVersion.WithKind("IngressClass"),
		typ: reflect.TypeFor[networkingv1.IngressClassSpec](),
		full: map[string]any{
			"controller": "k8s.io/ingress-nginx",
			"parameters": map[string]any{
				"apiGroup": "k8s.example.com", "kind": "IngressParameters", "name": "external",
				"scope": "Namespace", "namespace": "ingress",
			},
		},
	},
	{
		component: "csidriver", handler: &components.CSIDriverHandler{},
		gvk: storagev1.SchemeGroupVersion.WithKind("CSIDriver"),
		typ: reflect.TypeFor[storagev1.CSIDriverSpec](),
		full: map[string]any{
			"attachRequired":                     false,
			"podInfoOnMount":                     true,
			"volumeLifecycleModes":               []any{"Persistent", "Ephemeral"},
			"storageCapacity":                    true,
			"fsGroupPolicy":                      "File",
			"tokenRequests":                      []any{map[string]any{"audience": "vault", "expirationSeconds": 3600}, map[string]any{"audience": ""}},
			"requiresRepublish":                  true,
			"seLinuxMount":                       false,
			"nodeAllocatableUpdatePeriodSeconds": 60,
			"serviceAccountTokenInSecrets":       true,
			"preventPodSchedulingIfMissing":      false,
		},
	},
}

// policyFreeJSON is obj as the JSON tree it encodes to, numbers kept exact.
func policyFreeJSON(t *testing.T, obj any) map[string]any {
	t.Helper()
	data, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal %T: %v", obj, err)
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	var tree map[string]any
	if err := dec.Decode(&tree); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
	return tree
}

// TestPolicyFreeKinds_CanHandle: each handler takes its own type and none of
// the others.
func TestPolicyFreeKinds_CanHandle(t *testing.T) {
	for _, kind := range policyFreeKinds {
		for _, other := range policyFreeKinds {
			if got, want := kind.handler.CanHandle(other.component), kind.component == other.component; got != want {
				t.Errorf("%T: CanHandle(%q) = %v, want %v", kind.handler, other.component, got, want)
			}
		}
	}
}

// TestPolicyFreeKinds_FullCoversEveryField: the full fixture of each kind sets
// every authorable top-level field of the type it decodes into, so the tests
// that build from it see each field. A dependency bump that adds a field fails
// here until the fixture has it.
func TestPolicyFreeKinds_FullCoversEveryField(t *testing.T) {
	for _, kind := range policyFreeKinds {
		t.Run(kind.component, func(t *testing.T) {
			fields := specJSONFields(t, kind.typ)
			if kind.wholeObject {
				for _, identity := range []string{"kind", "apiVersion", "metadata"} {
					if _, ok := fields[identity]; !ok {
						t.Errorf("%s has no %q json field; the kind is not an object type", kind.typ, identity)
					}
					delete(fields, identity)
				}
				// A status is not the author's to write either, and
				// refuseObjectIdentityKeys does not know it.
				if _, ok := fields["status"]; ok {
					t.Errorf("%s has a status field, which a whole-object kind would let an author write", kind.typ)
				}
			}
			for name := range fields {
				if _, ok := kind.full[name]; !ok {
					t.Errorf("the full fixture sets no %q, a field of %s", name, kind.typ)
				}
			}
			for name := range kind.full {
				if _, ok := fields[name]; !ok {
					t.Errorf("the full fixture sets %q, which is no field of %s", name, kind.typ)
				}
			}
		})
	}
}

// TestPolicyFreeKinds_EmitIdentityAndTheAuthoredFields: the object is the
// kind's, cluster-scoped and named after the component, and beside that
// identity it holds exactly what was authored: nothing with the least a
// component may author, every field with the full fixture. A null field is an
// unauthored one. The comparison is on the encoded object, so a field the
// build dropped or added shows, whichever it is.
func TestPolicyFreeKinds_EmitIdentityAndTheAuthoredFields(t *testing.T) {
	for _, kind := range policyFreeKinds {
		withNulls := map[string]any{}
		for name := range kind.full {
			withNulls[name] = nil
		}
		for name, value := range kind.minimal {
			withNulls[name] = value
		}
		for name, props := range map[string]map[string]any{
			"minimal": kind.minimal, "minimal, the rest null": withNulls, "full": kind.full,
		} {
			t.Run(kind.component+"/"+name, func(t *testing.T) {
				// What was authored is read before the handler sees the
				// properties, so a handler that changed its input could not
				// change what its object is compared with.
				authored, data := authoredProperties(t, props)

				obj := generateCoreKind(t, kind.handler, kind.component, "fast", props)
				if got := obj.GetObjectKind().GroupVersionKind(); got != kind.gvk {
					t.Errorf("GVK = %s, want %s", got, kind.gvk)
				}
				if obj.GetNamespace() != "" {
					t.Errorf("namespace = %q, want none on a cluster-scoped object", obj.GetNamespace())
				}
				if _, after := authoredProperties(t, props); !bytes.Equal(after, data) {
					t.Errorf("the handler changed its input: %s, was %s", after, data)
				}

				// What the object must encode to: its identity and the authored
				// fields, as the upstream type encodes those fields.
				decoded := reflect.New(kind.typ).Interface()
				if err := json.Unmarshal(data, decoded); err != nil {
					t.Fatalf("decode the authored properties into %s: %v", kind.typ, err)
				}
				want := map[string]any{}
				if kind.wholeObject {
					want = policyFreeJSON(t, decoded)
				} else {
					want["spec"] = policyFreeJSON(t, decoded)
				}
				apiVersion, kindName := kind.gvk.ToAPIVersionAndKind()
				want["apiVersion"], want["kind"] = apiVersion, kindName
				want["metadata"] = map[string]any{"name": "fast"}

				got := policyFreeJSON(t, obj)
				// Older apimachinery encodes an unset creation time as null.
				if meta, ok := got["metadata"].(map[string]any); ok && meta["creationTimestamp"] == nil {
					delete(meta, "creationTimestamp")
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("object = %v\nwant     %v", got, want)
				}
				// Every authored field is in the encoded object under its own name.
				holder := got
				if !kind.wholeObject {
					holder, _ = got["spec"].(map[string]any)
				}
				for name := range authored {
					if _, ok := holder[name]; !ok {
						t.Errorf("authored %q is not in the object", name)
					}
				}
			})
		}
	}
}

// authoredProperties returns the names of the properties that are not null,
// and those properties as JSON. encoding/json writes map keys sorted, so two
// encodings of the same content are equal.
func authoredProperties(t *testing.T, props map[string]any) (map[string]struct{}, []byte) {
	t.Helper()
	authored := map[string]any{}
	names := map[string]struct{}{}
	for name, value := range props {
		if value != nil {
			authored[name] = value
			names[name] = struct{}{}
		}
	}
	data, err := json.Marshal(authored)
	if err != nil {
		t.Fatalf("marshal the authored properties: %v", err)
	}
	return names, data
}

// TestPolicyFreeKinds_GenerateCopies: each Generate returns an object of its
// own, sharing no map, slice or pointer with the next, so what the transform
// writes on one (the component label, for one) does not reach another build
// of the same config. reaches names, per case, references the authored fields
// hold: the walk must find each when an object is compared with itself, or it
// would pass without having looked there.
func TestPolicyFreeKinds_GenerateCopies(t *testing.T) {
	reaches := map[string][]string{
		"storageclass":          {".Parameters", ".ReclaimPolicy", ".MountOptions", ".AllowedTopologies"},
		"volumeattributesclass": {".Parameters"},
		"priorityclass":         {".PreemptionPolicy"},
		"runtimeclass":          {".Overhead", ".Overhead.PodFixed", ".Scheduling.NodeSelector", ".Scheduling.Tolerations"},
		"ingressclass":          {".Spec.Parameters", ".Spec.Parameters.APIGroup"},
		"csidriver":             {".Spec.AttachRequired", ".Spec.VolumeLifecycleModes", ".Spec.TokenRequests"},
	}
	type copyCase struct {
		name      string
		component string
		handler   oam.ComponentHandler
		props     map[string]any
		reaches   []string
	}
	var cases []copyCase
	for _, kind := range policyFreeKinds {
		if len(reaches[kind.component]) == 0 {
			t.Fatalf("%s names no reference the walk must reach", kind.component)
		}
		cases = append(cases, copyCase{kind.component, kind.component, kind.handler, kind.full, reaches[kind.component]})
	}
	// A quantity too large for its integer form keeps its number behind an
	// unexported pointer, which only the type's own DeepCopy copies.
	cases = append(cases, copyCase{
		name: "runtimeclass/decimal-backed quantity", component: "runtimeclass", handler: &components.RuntimeClassHandler{},
		props: map[string]any{
			"handler":  "kata",
			"overhead": map[string]any{"podFixed": map[string]any{"memory": "100000000000Gi"}},
		},
		reaches: []string{".Overhead.PodFixed[memory].d.Dec"},
	})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := tc.handler.ToApplicationConfig(&oam.Component{Name: "fast", Type: tc.component, Properties: tc.props}, coreKindNamespace)
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			var objs [2]reflect.Value
			for i := range objs {
				generated, err := cfg.Generate(stack.NewApplication("fast", coreKindNamespace, cfg))
				if err != nil || len(generated) != 1 {
					t.Fatalf("Generate: %d objects, err %v", len(generated), err)
				}
				objs[i] = reflect.ValueOf(*generated[0])
			}
			if !reflect.DeepEqual(objs[0].Interface(), objs[1].Interface()) {
				t.Fatalf("two builds differ: %+v and %+v", objs[0].Interface(), objs[1].Interface())
			}
			shared := sharedReferences(objs[0], objs[1], "")
			if len(shared) != 0 {
				t.Errorf("two builds share %v", shared)
			}
			// Vacuity guard: compared with itself, an object shares every
			// reference it holds, so the walk must report the named ones.
			self := sharedReferences(objs[0], objs[0], "")
			for _, path := range tc.reaches {
				if !slices.Contains(self, path) {
					t.Errorf("the walk did not reach %s; it found %v", path, self)
				}
			}
		})
	}
}

// sharedReferences returns the paths at which a and b, two values of one
// type, hold the same non-nil pointer, map or slice backing array. It reads
// unexported fields too: a copy that left one shared is not a copy.
func sharedReferences(a, b reflect.Value, path string) []string {
	var shared []string
	switch a.Kind() {
	case reflect.Pointer, reflect.Interface:
		if a.IsNil() || b.IsNil() {
			return nil
		}
		if a.Kind() == reflect.Pointer && a.Pointer() == b.Pointer() {
			shared = append(shared, path)
		}
		return append(shared, sharedReferences(a.Elem(), b.Elem(), path)...)
	case reflect.Struct:
		for i := range a.NumField() {
			shared = append(shared, sharedReferences(a.Field(i), b.Field(i), path+"."+a.Type().Field(i).Name)...)
		}
	case reflect.Map:
		if a.Len() == 0 || b.Len() == 0 {
			return nil
		}
		if a.Pointer() == b.Pointer() {
			shared = append(shared, path)
		}
		for _, key := range a.MapKeys() {
			if other := b.MapIndex(key); other.IsValid() {
				shared = append(shared, sharedReferences(a.MapIndex(key), other, fmt.Sprintf("%s[%v]", path, key))...)
			}
		}
	case reflect.Slice:
		if a.Len() == 0 || b.Len() == 0 {
			return nil
		}
		if a.Pointer() == b.Pointer() {
			shared = append(shared, path)
		}
		for i := range min(a.Len(), b.Len()) {
			shared = append(shared, sharedReferences(a.Index(i), b.Index(i), fmt.Sprintf("%s[%d]", path, i))...)
		}
	case reflect.Array:
		for i := range a.Len() {
			shared = append(shared, sharedReferences(a.Index(i), b.Index(i), fmt.Sprintf("%s[%d]", path, i))...)
		}
	default:
		// A scalar is copied by value and holds no reference. An API type
		// declares no channel, function or unsafe pointer.
	}
	return shared
}

// TestPolicyFreeKinds_ObjectIdentityIsNotAuthorable: no kind lets an author
// write the object's kind, apiVersion or metadata, under any spelling and
// whatever the value. A whole-object kind refuses each by name; a kind that
// projects a spec type refuses it as a key the spec does not have.
func TestPolicyFreeKinds_ObjectIdentityIsNotAuthorable(t *testing.T) {
	for _, kind := range policyFreeKinds {
		want := ": not authorable: launcher sets the object's kind, apiVersion and metadata"
		if !kind.wholeObject {
			want = "properties do not decode into a "
		}
		for _, tc := range []struct {
			key   string
			value any
		}{
			{"kind", "Pod"}, {"apiVersion", "v1"}, {"metadata", map[string]any{"labels": map[string]any{"a": "b"}}},
			{"metadata", nil}, {"Kind", "Pod"}, {"APIVersion", "v1"}, {"Metadata", map[string]any{"name": "other"}},
		} {
			t.Run(fmt.Sprintf("%s/%s=%v", kind.component, tc.key, tc.value), func(t *testing.T) {
				props := map[string]any{tc.key: tc.value}
				for name, value := range kind.minimal {
					props[name] = value
				}
				err := coreKindErr(kind.handler, kind.component, "fast", props)
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %v, want one mentioning %q", err, want)
				}
				if kind.wholeObject && !strings.HasPrefix(err.Error(), tc.key+":") {
					t.Errorf("err = %v, want it to name the key %q as written", err, tc.key)
				}
			})
		}
	}
}

// TestPolicyFreeKinds_Refusals: the properties are the fields of the type and
// nothing else, at any depth, and a top-level field the API requires must be
// authored.
func TestPolicyFreeKinds_Refusals(t *testing.T) {
	const notA = "properties do not decode into a "
	cases := map[string][]struct {
		name  string
		props map[string]any
		want  string
	}{
		"storageclass": {
			{"no properties", nil, "provisioner: required"},
			{"null provisioner", map[string]any{"provisioner": nil}, "provisioner: required"},
			{"empty provisioner", map[string]any{"provisioner": ""}, "provisioner: required"},
			{"unknown key", map[string]any{"provisioner": "p", "spec": map[string]any{}}, notA + "storage.k8s.io/v1 StorageClass"},
			{"parameter not a string", map[string]any{"provisioner": "p", "parameters": map[string]any{"iops": 3000}}, notA},
			{"topology sub-key", map[string]any{"provisioner": "p", "allowedTopologies": []any{map[string]any{"matchLabels": map[string]any{}}}}, notA},
			{"expansion a string", map[string]any{"provisioner": "p", "allowVolumeExpansion": "yes"}, notA},
			{"null mount option", map[string]any{"provisioner": "p", "mountOptions": []any{"ro", nil}}, "mountOptions[1]"},
			{"two spellings", map[string]any{"provisioner": "p", "Provisioner": "q"}, "sets the same field as"},
		},
		"volumeattributesclass": {
			{"no properties", nil, "driverName: required"},
			{"empty driverName", map[string]any{"driverName": "", "parameters": map[string]any{"iops": "1"}}, "driverName: required"},
			{"no parameters", map[string]any{"driverName": "d"}, "parameters: required"},
			{"null parameters", map[string]any{"driverName": "d", "parameters": nil}, "parameters: required"},
			{"empty parameters", map[string]any{"driverName": "d", "parameters": map[string]any{}}, "parameters: required"},
			{"unknown key", map[string]any{"driverName": "d", "driver": "d"}, notA + "storage.k8s.io/v1 VolumeAttributesClass"},
			{"parameter not a string", map[string]any{"driverName": "d", "parameters": map[string]any{"iops": 3000}}, notA},
			{"two spellings", map[string]any{"driverName": "d", "drivername": "e"}, "sets the same field as"},
		},
		"priorityclass": {
			{"unknown key", map[string]any{"value": 1, "priority": 1}, notA + "scheduling.k8s.io/v1 PriorityClass"},
			{"value a string", map[string]any{"value": "high"}, notA},
			{"value not an integer", map[string]any{"value": 1.5}, notA},
			{"value over int32", map[string]any{"value": 4294967296}, notA},
			{"globalDefault a string", map[string]any{"value": 1, "globalDefault": "true"}, notA},
			{"two spellings", map[string]any{"value": 1, "Value": 2}, "sets the same field as"},
		},
		"runtimeclass": {
			{"no properties", nil, "handler: required"},
			{"empty handler", map[string]any{"handler": ""}, "handler: required"},
			{"unknown key", map[string]any{"handler": "runc", "runtimeHandler": "runc"}, notA + "node.k8s.io/v1 RuntimeClass"},
			{"overhead sub-key", map[string]any{"handler": "runc", "overhead": map[string]any{"fixed": map[string]any{}}}, notA},
			{"bad quantity", map[string]any{"handler": "runc", "overhead": map[string]any{"podFixed": map[string]any{"cpu": "lots"}}}, notA},
			{"null toleration", map[string]any{"handler": "runc", "scheduling": map[string]any{"tolerations": []any{nil}}}, "scheduling.tolerations[0]"},
			{"two spellings", map[string]any{"handler": "runc", "Handler": "kata"}, "sets the same field as"},
		},
		"ingressclass": {
			{"unknown key", map[string]any{"controllerName": "k8s.io/ingress-nginx"}, notA + "networking.k8s.io/v1 IngressClassSpec"},
			{"the object's spec", map[string]any{"spec": map[string]any{"controller": "c"}}, notA},
			{"parameters sub-key", map[string]any{"parameters": map[string]any{"kind": "K", "name": "n", "group": "g"}}, notA},
			{"parameters a string", map[string]any{"parameters": "external"}, notA},
			{"two spellings", map[string]any{"controller": "a", "Controller": "b"}, "sets the same field as"},
		},
		"csidriver": {
			{"unknown key", map[string]any{"driverName": "ebs.csi.aws.com"}, notA + "storage.k8s.io/v1 CSIDriverSpec"},
			{"attachRequired a string", map[string]any{"attachRequired": "false"}, notA},
			{"token request sub-key", map[string]any{"tokenRequests": []any{map[string]any{"audiences": []any{"a"}}}}, notA},
			{"modes a string", map[string]any{"volumeLifecycleModes": "Persistent"}, notA},
			{"null mode", map[string]any{"volumeLifecycleModes": []any{"Persistent", nil}}, "volumeLifecycleModes[1]"},
			{"two spellings", map[string]any{"fsGroupPolicy": "File", "FSGroupPolicy": "None"}, "sets the same field as"},
		},
	}
	for _, kind := range policyFreeKinds {
		if len(cases[kind.component]) == 0 {
			t.Errorf("%s has no refusal cases", kind.component)
		}
		for _, tc := range cases[kind.component] {
			t.Run(kind.component+"/"+tc.name, func(t *testing.T) {
				err := coreKindErr(kind.handler, kind.component, "fast", tc.props)
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
				}
			})
		}
	}
}

// TestPolicyFreeKinds_AuthoredValuesArriveTyped reads a few authored values
// back from the typed object: an authored zero and false are kept, a quantity
// written as a number takes its canonical form, and lists keep their order.
func TestPolicyFreeKinds_AuthoredValuesArriveTyped(t *testing.T) {
	full := map[string]map[string]any{}
	handlers := map[string]oam.ComponentHandler{}
	for _, kind := range policyFreeKinds {
		full[kind.component], handlers[kind.component] = kind.full, kind.handler
	}
	build := func(component string, props map[string]any) any {
		return generateCoreKind(t, handlers[component], component, "fast", props)
	}

	sc := build("storageclass", full["storageclass"]).(*storagev1.StorageClass)
	if sc.AllowVolumeExpansion == nil || *sc.AllowVolumeExpansion {
		t.Errorf("allowVolumeExpansion = %v, want the authored false", sc.AllowVolumeExpansion)
	}
	if !slices.Equal(sc.MountOptions, []string{"noatime", "discard"}) {
		t.Errorf("mountOptions = %v, want them in authored order", sc.MountOptions)
	}
	if sc.ReclaimPolicy == nil || *sc.ReclaimPolicy != corev1.PersistentVolumeReclaimRetain {
		t.Errorf("reclaimPolicy = %v, want Retain", sc.ReclaimPolicy)
	}

	pc := build("priorityclass", map[string]any{"value": 0, "globalDefault": false}).(*schedulingv1.PriorityClass)
	if pc.Value != 0 || pc.GlobalDefault {
		t.Errorf("value = %d, globalDefault = %v; want the authored 0 and false", pc.Value, pc.GlobalDefault)
	}
	if negative := build("priorityclass", map[string]any{"value": -10}).(*schedulingv1.PriorityClass); negative.Value != -10 {
		t.Errorf("value = %d, want the authored -10", negative.Value)
	}
	// The API requires no value. The type always encodes one, so a class that
	// authors none carries value: 0, which the API reads an absent one as.
	unvalued := policyFreeJSON(t, build("priorityclass", map[string]any{"description": "x"}))
	if got, ok := unvalued["value"]; !ok || fmt.Sprint(got) != "0" {
		t.Errorf("value = %v (present: %v), want 0 on a class that authors none", got, ok)
	}

	rc := build("runtimeclass", full["runtimeclass"]).(*nodev1.RuntimeClass)
	if rc.Overhead == nil || !rc.Overhead.PodFixed.Memory().Equal(resource.MustParse("128Mi")) || rc.Overhead.PodFixed.Memory().String() != "134217728" {
		t.Errorf("overhead = %+v, want podFixed.memory 134217728", rc.Overhead)
	}

	ic := build("ingressclass", full["ingressclass"]).(*networkingv1.IngressClass)
	if p := ic.Spec.Parameters; p == nil || p.Kind != "IngressParameters" || p.Scope == nil || *p.Scope != "Namespace" || p.Namespace == nil || *p.Namespace != "ingress" {
		t.Errorf("parameters = %+v, want the authored reference", p)
	}

	driver := build("csidriver", full["csidriver"]).(*storagev1.CSIDriver)
	if driver.Spec.AttachRequired == nil || *driver.Spec.AttachRequired {
		t.Errorf("attachRequired = %v, want the authored false", driver.Spec.AttachRequired)
	}
	wantTokens := []storagev1.TokenRequest{{Audience: "vault", ExpirationSeconds: new(int64(3600))}, {Audience: ""}}
	if !reflect.DeepEqual(driver.Spec.TokenRequests, wantTokens) {
		t.Errorf("tokenRequests = %+v, want %+v", driver.Spec.TokenRequests, wantTokens)
	}
}

// TestPolicyFreeKinds_ThroughTheTransform: under the strictest policy the
// tests have, under the transform's default one and under none passed, each
// kind builds its one cluster-scoped object, and the transform sets the
// component label on it and nothing else.
func TestPolicyFreeKinds_ThroughTheTransform(t *testing.T) {
	for _, kind := range policyFreeKinds {
		for name, policy := range map[string]oam.Policy{"strict policy": ptStrictPolicy(), "no policy passed": nil} {
			t.Run(kind.component+"/"+name, func(t *testing.T) {
				objs, err := pvTransform(kind.component, kind.handler, kind.full, policy)
				if err != nil {
					t.Fatalf("transform: %v", err)
				}
				if len(objs) != 1 {
					t.Fatalf("generated %d objects, want one", len(objs))
				}
				obj := objs[0]
				if got := obj.GetObjectKind().GroupVersionKind(); got != kind.gvk {
					t.Errorf("GVK = %s, want %s", got, kind.gvk)
				}
				if obj.GetName() != "web" || obj.GetNamespace() != "" {
					t.Errorf("identity = %q/%q, want the cluster-scoped web", obj.GetNamespace(), obj.GetName())
				}
				wantLabels := map[string]string{oam.ComponentLabelKeyForDomain(""): "web"}
				if !reflect.DeepEqual(obj.GetLabels(), wantLabels) || len(obj.GetAnnotations()) != 0 {
					t.Errorf("labels = %v, annotations = %v; want labels %v and no annotation", obj.GetLabels(), obj.GetAnnotations(), wantLabels)
				}
			})
		}
	}
}
