package components_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The serviceaccount, persistentvolumeclaim and configmap kind components
// (go-kure/launcher#702) each emit exactly one object, named after the
// component and carrying the component's `app` label.

type policyApplier interface {
	ApplyPolicy(oam.Policy) error
}

func kindConfig(t *testing.T, h oam.ComponentHandler, typ, name string, props map[string]any) (stack.ApplicationConfig, error) {
	t.Helper()
	return h.ToApplicationConfig(&oam.Component{Name: name, Type: typ, Properties: props}, "default")
}

func generateOne(t *testing.T, cfg stack.ApplicationConfig, name string) client.Object {
	t.Helper()
	objs, err := cfg.Generate(stack.NewApplication(name, "default", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected exactly one object, got %d", len(objs))
	}
	obj := *objs[0]
	if obj.GetName() != name || obj.GetNamespace() != "default" {
		t.Errorf("metadata = %s/%s, want default/%s", obj.GetNamespace(), obj.GetName(), name)
	}
	if got := obj.GetLabels()["app"]; got != name {
		t.Errorf("app label = %q, want %q", got, name)
	}
	if len(obj.GetAnnotations()) != 0 {
		t.Errorf("annotations = %v, want none", obj.GetAnnotations())
	}
	return obj
}

func TestKindObjectHandlers_CanHandle(t *testing.T) {
	for typ, h := range map[string]oam.ComponentHandler{
		"serviceaccount":        &components.ServiceAccountHandler{},
		"persistentvolumeclaim": &components.PersistentVolumeClaimHandler{},
		"configmap":             &components.ConfigMapHandler{},
	} {
		if !h.CanHandle(typ) {
			t.Errorf("%T: CanHandle(%q) = false", h, typ)
		}
		if h.CanHandle("pvc") {
			t.Errorf("%T: CanHandle(\"pvc\") = true; the pvc trait's type is not a component type", h)
		}
	}
}

// An unauthored automountServiceAccountToken stays unset: the kind is a
// projection, and the false a workload's generated account carries is an
// opinion of that workload, not of the kind.
func TestServiceAccountHandler_Generate(t *testing.T) {
	h := &components.ServiceAccountHandler{}

	cfg, err := kindConfig(t, h, "serviceaccount", "api", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	sa := generateOne(t, cfg, "api").(*corev1.ServiceAccount)
	if sa.AutomountServiceAccountToken != nil {
		t.Errorf("automountServiceAccountToken = %v, want unset", *sa.AutomountServiceAccountToken)
	}
	if len(sa.ImagePullSecrets) != 0 {
		t.Errorf("imagePullSecrets = %v, want none", sa.ImagePullSecrets)
	}

	cfg, err = kindConfig(t, h, "serviceaccount", "api", map[string]any{
		"automountServiceAccountToken": false,
		"imagePullSecrets":             []any{map[string]any{"name": "regcred"}, map[string]any{"name": "mirror"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sa = generateOne(t, cfg, "api").(*corev1.ServiceAccount)
	if sa.AutomountServiceAccountToken == nil || *sa.AutomountServiceAccountToken {
		t.Errorf("automountServiceAccountToken = %v, want false", sa.AutomountServiceAccountToken)
	}
	if len(sa.ImagePullSecrets) != 2 || sa.ImagePullSecrets[0].Name != "regcred" || sa.ImagePullSecrets[1].Name != "mirror" {
		t.Errorf("imagePullSecrets = %v, want [regcred mirror] in authored order", sa.ImagePullSecrets)
	}
}

func TestServiceAccountHandler_Refusals(t *testing.T) {
	h := &components.ServiceAccountHandler{}
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"automount not a bool":     {map[string]any{"automountServiceAccountToken": "no"}, "automountServiceAccountToken: must be a boolean"},
		"pull secret without name": {map[string]any{"imagePullSecrets": []any{map[string]any{}}}, "imagePullSecrets[0]"},
		"pull secret unknown key":  {map[string]any{"imagePullSecrets": []any{map[string]any{"name": "a", "namespace": "b"}}}, "namespace"},
		"pull secret bad name":     {map[string]any{"imagePullSecrets": []any{map[string]any{"name": "Reg_Cred"}}}, "imagePullSecrets[0]"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := kindConfig(t, h, "serviceaccount", "api", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

func TestPersistentVolumeClaimHandler_Generate(t *testing.T) {
	h := &components.PersistentVolumeClaimHandler{}

	cfg, err := kindConfig(t, h, "persistentvolumeclaim", "data", map[string]any{"size": "5Gi"})
	if err != nil {
		t.Fatal(err)
	}
	pvc := generateOne(t, cfg, "data").(*corev1.PersistentVolumeClaim)
	if got := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; got.String() != "5Gi" {
		t.Errorf("requests.storage = %s, want 5Gi", got.String())
	}
	if len(pvc.Spec.AccessModes) != 1 || pvc.Spec.AccessModes[0] != corev1.ReadWriteOnce {
		t.Errorf("accessModes = %v, want [ReadWriteOnce] by default", pvc.Spec.AccessModes)
	}
	if pvc.Spec.StorageClassName != nil {
		t.Errorf("storageClassName = %q, want unset (the cluster default class)", *pvc.Spec.StorageClassName)
	}
	if pvc.Spec.VolumeMode != nil {
		t.Errorf("volumeMode = %v, want unset", *pvc.Spec.VolumeMode)
	}

	cfg, err = kindConfig(t, h, "persistentvolumeclaim", "data", map[string]any{
		"size": "1Gi", "storageClassName": "fast", "accessModes": []any{"ReadWriteMany"}, "volumeMode": "Block",
	})
	if err != nil {
		t.Fatal(err)
	}
	pvc = generateOne(t, cfg, "data").(*corev1.PersistentVolumeClaim)
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "fast" {
		t.Errorf("storageClassName = %v, want fast", pvc.Spec.StorageClassName)
	}
	if len(pvc.Spec.AccessModes) != 1 || pvc.Spec.AccessModes[0] != corev1.ReadWriteMany {
		t.Errorf("accessModes = %v, want [ReadWriteMany]", pvc.Spec.AccessModes)
	}
	if pvc.Spec.VolumeMode == nil || *pvc.Spec.VolumeMode != corev1.PersistentVolumeBlock {
		t.Errorf("volumeMode = %v, want Block", pvc.Spec.VolumeMode)
	}
}

// An authored empty storageClassName requests no class, which the API
// distinguishes from an unset one (the cluster default class).
func TestPersistentVolumeClaimHandler_ExplicitEmptyStorageClass(t *testing.T) {
	cfg, err := kindConfig(t, &components.PersistentVolumeClaimHandler{}, "persistentvolumeclaim", "data",
		map[string]any{"size": "1Gi", "storageClassName": ""})
	if err != nil {
		t.Fatal(err)
	}
	pvc := generateOne(t, cfg, "data").(*corev1.PersistentVolumeClaim)
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "" {
		t.Errorf("storageClassName = %v, want a pointer to \"\"", pvc.Spec.StorageClassName)
	}
}

// Size precedence is authored > policy default; with neither, the claim is
// refused. The policy maximum applies to either.
func TestPersistentVolumeClaimHandler_Policy(t *testing.T) {
	h := &components.PersistentVolumeClaimHandler{}

	cfg, err := kindConfig(t, h, "persistentvolumeclaim", "data", map[string]any{})
	if err != nil {
		t.Fatalf("an unauthored size is left for the policy default: %v", err)
	}
	if err := cfg.(policyApplier).ApplyPolicy(&stubPolicy{defaultStorageSize: "2Gi"}); err != nil {
		t.Fatal(err)
	}
	pvc := generateOne(t, cfg, "data").(*corev1.PersistentVolumeClaim)
	if got := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; got.String() != "2Gi" {
		t.Errorf("requests.storage = %s, want the policy default 2Gi", got.String())
	}

	cfg, _ = kindConfig(t, h, "persistentvolumeclaim", "data", map[string]any{"size": "3Gi"})
	if err := cfg.(policyApplier).ApplyPolicy(&stubPolicy{defaultStorageSize: "2Gi"}); err != nil {
		t.Fatal(err)
	}
	pvc = generateOne(t, cfg, "data").(*corev1.PersistentVolumeClaim)
	if got := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; got.String() != "3Gi" {
		t.Errorf("requests.storage = %s, want the authored 3Gi over the policy default", got.String())
	}

	cfg, _ = kindConfig(t, h, "persistentvolumeclaim", "data", map[string]any{})
	if err := cfg.(policyApplier).ApplyPolicy(nil); err == nil || !strings.Contains(err.Error(), "size: required") {
		t.Errorf("no size and no policy default: err = %v, want a size refusal", err)
	}
	if _, err := cfg.Generate(stack.NewApplication("data", "default", cfg)); err == nil {
		t.Error("Generate without a size succeeded, want a refusal")
	}

	cfg, _ = kindConfig(t, h, "persistentvolumeclaim", "data", map[string]any{"size": "20Gi"})
	if err := cfg.(policyApplier).ApplyPolicy(&stubPolicy{maxStorageSize: "10Gi"}); err == nil {
		t.Error("20Gi under a 10Gi maximum: ApplyPolicy succeeded, want a refusal")
	}
}

func TestPersistentVolumeClaimHandler_Refusals(t *testing.T) {
	h := &components.PersistentVolumeClaimHandler{}
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"zero size":           {map[string]any{"size": "0"}, "size: must be positive"},
		"negative size":       {map[string]any{"size": "-1Gi"}, "size: must be positive"},
		"bad size":            {map[string]any{"size": "lots"}, "size: invalid quantity"},
		"bad class":           {map[string]any{"size": "1Gi", "storageClassName": "Fast_SSD"}, "storageClassName"},
		"empty access modes":  {map[string]any{"size": "1Gi", "accessModes": []any{}}, "at least one access mode"},
		"unknown access mode": {map[string]any{"size": "1Gi", "accessModes": []any{"ReadWriteSome"}}, "ReadWriteSome"},
		"RWOP combined":       {map[string]any{"size": "1Gi", "accessModes": []any{"ReadWriteOncePod", "ReadOnlyMany"}}, "cannot be combined"},
		"bad volume mode":     {map[string]any{"size": "1Gi", "volumeMode": "Raw"}, "volumeMode"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := kindConfig(t, h, "persistentvolumeclaim", "data", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

func TestConfigMapHandler_Generate(t *testing.T) {
	h := &components.ConfigMapHandler{}

	cfg, err := kindConfig(t, h, "configmap", "settings", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	cm := generateOne(t, cfg, "settings").(*corev1.ConfigMap)
	if len(cm.Data) != 0 || len(cm.BinaryData) != 0 || cm.Immutable != nil {
		t.Errorf("empty configmap = data %v, binaryData %v, immutable %v; want all unset", cm.Data, cm.BinaryData, cm.Immutable)
	}

	cfg, err = kindConfig(t, h, "configmap", "settings", map[string]any{
		"data":       map[string]any{"LOG_LEVEL": "debug", "app.properties": "a=1\nb=2\n"},
		"binaryData": map[string]any{"logo.png": "iVBORw0K"},
		"immutable":  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	cm = generateOne(t, cfg, "settings").(*corev1.ConfigMap)
	if cm.Data["LOG_LEVEL"] != "debug" || cm.Data["app.properties"] != "a=1\nb=2\n" {
		t.Errorf("data = %v", cm.Data)
	}
	if got := cm.BinaryData["logo.png"]; string(got) != "\x89PNG\r\n" {
		t.Errorf("binaryData[logo.png] = %q, want the decoded bytes", got)
	}
	if cm.Immutable == nil || !*cm.Immutable {
		t.Errorf("immutable = %v, want true", cm.Immutable)
	}
}

func TestConfigMapHandler_Refusals(t *testing.T) {
	h := &components.ConfigMapHandler{}
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"number value":        {map[string]any{"data": map[string]any{"replicas": 3}}, "data.replicas: must be a string"},
		"bool value":          {map[string]any{"data": map[string]any{"debug": true}}, "data.debug: must be a string"},
		"bad key":             {map[string]any{"data": map[string]any{"a/b": "x"}}, `data: invalid key "a/b"`},
		"bad binary key":      {map[string]any{"binaryData": map[string]any{"a b": "eA=="}}, `binaryData: invalid key "a b"`},
		"key in both":         {map[string]any{"data": map[string]any{"k": "x"}, "binaryData": map[string]any{"k": "eA=="}}, "binaryData.k: key also appears in data"},
		"not base64":          {map[string]any{"binaryData": map[string]any{"k": "not base64!"}}, "binaryData.k: invalid base64"},
		"binary not a string": {map[string]any{"binaryData": map[string]any{"k": 1}}, "binaryData.k: must be a base64-encoded string"},
		"immutable not bool":  {map[string]any{"immutable": "yes"}, "immutable: must be a boolean"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := kindConfig(t, h, "configmap", "settings", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// The summed size of the data values and the decoded binaryData values may
// reach corev1.MaxSecretSize and no further, as ValidateConfigMap allows.
func TestConfigMapHandler_TotalSizeLimit(t *testing.T) {
	h := &components.ConfigMapHandler{}
	half := corev1.MaxSecretSize / 2
	for name, tc := range map[string]struct {
		data, binary int // bytes in one data value and one decoded binaryData value
		refused      bool
	}{
		"data at the limit":          {data: corev1.MaxSecretSize},
		"data one byte over":         {data: corev1.MaxSecretSize + 1, refused: true},
		"split at the limit":         {data: half, binary: corev1.MaxSecretSize - half},
		"split one byte over":        {data: half, binary: corev1.MaxSecretSize - half + 1, refused: true},
		"binary alone one byte over": {binary: corev1.MaxSecretSize + 1, refused: true},
	} {
		t.Run(name, func(t *testing.T) {
			props := map[string]any{}
			if tc.data > 0 {
				props["data"] = map[string]any{"text": strings.Repeat("x", tc.data)}
			}
			if tc.binary > 0 {
				props["binaryData"] = map[string]any{"blob": base64.StdEncoding.EncodeToString(make([]byte, tc.binary))}
			}
			_, err := kindConfig(t, h, "configmap", "settings", props)
			if tc.refused {
				if err == nil || !strings.Contains(err.Error(), "over the 1048576-byte limit") {
					t.Fatalf("err = %v, want the size-limit refusal", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want the ConfigMap accepted at the limit", err)
			}
		})
	}
}

// With several bad entries, the one reported is the first in sorted key
// order, whatever order the map iterates in. Five bad keys and fifty runs make
// an unsorted loop report "a" every time with a chance of about 5^-50.
func TestConfigMapHandler_RefusalsInSortedKeyOrder(t *testing.T) {
	h := &components.ConfigMapHandler{}
	// five returns keys a..e (in scrambled literal order), each with suffix
	// appended and mapped to v.
	five := func(suffix string, v any) map[string]any {
		m := map[string]any{}
		for _, k := range []string{"e", "c", "a", "d", "b"} {
			m[k+suffix] = v
		}
		return m
	}
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"invalid data keys":       {map[string]any{"data": five("/x", "x")}, `data: invalid key "a/x"`},
		"non-string data values":  {map[string]any{"data": five("", 1)}, "data.a: must be a string"},
		"invalid binaryData keys": {map[string]any{"binaryData": five("/x", "eA==")}, `binaryData: invalid key "a/x"`},
		"bad base64 values":       {map[string]any{"binaryData": five("", "not base64!")}, "binaryData.a: invalid base64"},
	} {
		t.Run(name, func(t *testing.T) {
			for range 50 {
				_, err := kindConfig(t, h, "configmap", "settings", tc.props)
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
				}
			}
		})
	}
}

// CheckConfigMapSize is the check the configmap trait shares: it sums the data
// values and the decoded binaryData values against corev1.MaxSecretSize.
func TestCheckConfigMapSize(t *testing.T) {
	half := corev1.MaxSecretSize / 2
	for name, tc := range map[string]struct {
		data    map[string]string
		binary  map[string][]byte
		refused bool
	}{
		"empty": {},
		"at the limit across both": {
			data:   map[string]string{"t": strings.Repeat("x", half)},
			binary: map[string][]byte{"b": make([]byte, corev1.MaxSecretSize-half)},
		},
		"one byte over across both": {
			data:    map[string]string{"t": strings.Repeat("x", half)},
			binary:  map[string][]byte{"b": make([]byte, corev1.MaxSecretSize-half+1)},
			refused: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := components.CheckConfigMapSize(tc.data, tc.binary)
			if tc.refused {
				if err == nil || !strings.Contains(err.Error(), "hold 1048577 bytes, over the 1048576-byte limit") {
					t.Fatalf("err = %v, want the size-limit refusal", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
		})
	}
}
