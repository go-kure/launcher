package components_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func TestPersistentVolumeHandler_CanHandle(t *testing.T) {
	h := &components.PersistentVolumeHandler{}
	if !h.CanHandle("persistentvolume") {
		t.Error("CanHandle(persistentvolume) = false")
	}
	if h.CanHandle("persistentvolumeclaim") {
		t.Error("CanHandle(persistentvolumeclaim) = true")
	}
}

// TestPersistentVolumeHandler_EmitsIdentityOnly: with no properties the
// PersistentVolume carries its name and nothing else, which the API server
// then refuses for want of a source. It is cluster-scoped, so it has no
// namespace, whatever namespace the application is built for.
func TestPersistentVolumeHandler_EmitsIdentityOnly(t *testing.T) {
	for name, props := range map[string]map[string]any{
		"no properties":    nil,
		"empty properties": {},
		"null fields":      {"capacity": nil, "nfs": nil, "accessModes": nil},
	} {
		t.Run(name, func(t *testing.T) {
			pv := generateCoreKind(t, &components.PersistentVolumeHandler{}, "persistentvolume", "data", props).(*corev1.PersistentVolume)
			if pv.APIVersion != "v1" || pv.Kind != "PersistentVolume" {
				t.Errorf("GVK = %s %s, want v1 PersistentVolume", pv.APIVersion, pv.Kind)
			}
			if pv.Namespace != "" {
				t.Errorf("namespace = %q, want none on a cluster-scoped object", pv.Namespace)
			}
			if !reflect.DeepEqual(pv.Spec, corev1.PersistentVolumeSpec{}) {
				t.Errorf("spec = %+v, want empty", pv.Spec)
			}
		})
	}
}

// TestPersistentVolumeHandler_EmitsAuthoredSpec: a field of the spec and a
// promoted field of its embedded volume source are authored side by side, and
// both arrive as written, a quantity written as a number included.
func TestPersistentVolumeHandler_EmitsAuthoredSpec(t *testing.T) {
	pv := generateCoreKindUnder(t, &components.PersistentVolumeHandler{}, "persistentvolume", "data", map[string]any{
		"capacity":                      map[string]any{"storage": 1073741824},
		"accessModes":                   []any{"ReadWriteMany", "ReadOnlyMany"},
		"persistentVolumeReclaimPolicy": "Retain",
		"storageClassName":              "shared",
		"mountOptions":                  []any{"nfsvers=4.1", "ro"},
		"volumeMode":                    "Filesystem",
		"claimRef":                      map[string]any{"namespace": "shop", "name": "data"},
		"nfs":                           map[string]any{"server": "nfs.example.com", "path": "/exports/data", "readOnly": true},
	}, &stubPolicy{maxStorageSize: "1Gi"}, nil).(*corev1.PersistentVolume)

	if q := pv.Spec.Capacity[corev1.ResourceStorage]; q.String() != "1073741824" {
		t.Errorf("capacity.storage = %s, want 1073741824", q.String())
	}
	wantModes := []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany, corev1.ReadOnlyMany}
	if !slices.Equal(pv.Spec.AccessModes, wantModes) {
		t.Errorf("accessModes = %v, want %v in authored order", pv.Spec.AccessModes, wantModes)
	}
	if pv.Spec.PersistentVolumeReclaimPolicy != corev1.PersistentVolumeReclaimRetain || pv.Spec.StorageClassName != "shared" {
		t.Errorf("reclaim policy = %q, storage class = %q; want Retain and shared", pv.Spec.PersistentVolumeReclaimPolicy, pv.Spec.StorageClassName)
	}
	if !slices.Equal(pv.Spec.MountOptions, []string{"nfsvers=4.1", "ro"}) {
		t.Errorf("mountOptions = %v, want them in authored order", pv.Spec.MountOptions)
	}
	if pv.Spec.VolumeMode == nil || *pv.Spec.VolumeMode != corev1.PersistentVolumeFilesystem {
		t.Errorf("volumeMode = %v, want Filesystem", pv.Spec.VolumeMode)
	}
	if ref := pv.Spec.ClaimRef; ref == nil || ref.Namespace != "shop" || ref.Name != "data" {
		t.Errorf("claimRef = %+v, want shop/data", ref)
	}
	wantNFS := &corev1.NFSVolumeSource{Server: "nfs.example.com", Path: "/exports/data", ReadOnly: true}
	if !reflect.DeepEqual(pv.Spec.NFS, wantNFS) {
		t.Errorf("nfs = %+v, want %+v", pv.Spec.NFS, wantNFS)
	}
}

// TestPersistentVolumeHandler_Refusals: the properties are the
// PersistentVolumeSpec fields and nothing else, at any depth.
func TestPersistentVolumeHandler_Refusals(t *testing.T) {
	const notASpec = "properties do not decode into a v1 PersistentVolumeSpec"
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"unknown key":           {map[string]any{"size": "1Gi"}, notASpec},
		"metadata key":          {map[string]any{"labels": map[string]any{"team": "a"}}, notASpec},
		"the embedded type":     {map[string]any{"persistentVolumeSource": map[string]any{"nfs": map[string]any{}}}, notASpec},
		"source sub-key":        {map[string]any{"nfs": map[string]any{"host": "nfs.example.com"}}, notASpec},
		"bad quantity":          {map[string]any{"capacity": map[string]any{"storage": "lots"}}, notASpec},
		"access modes a string": {map[string]any{"accessModes": "ReadWriteOnce"}, notASpec},
		"null access mode":      {map[string]any{"accessModes": []any{"ReadWriteOnce", nil}}, "accessModes[1]"},
		"two spellings":         {map[string]any{"hostPath": map[string]any{"path": "/a"}, "HostPath": map[string]any{"path": "/b"}}, "sets the same field as"},
	} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(&components.PersistentVolumeHandler{}, "persistentvolume", "data", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// The volume specs the policy tests build, as the YAML of the spec's fields.
const (
	pvNFS      = "capacity:\n  storage: 10Gi\naccessModes: [ReadWriteMany]\nnfs:\n  server: nfs.example.com\n  path: /exports/data\n"
	pvOverMax  = "capacity:\n  storage: 20Gi\naccessModes: [ReadWriteMany]\nnfs:\n  server: nfs.example.com\n  path: /exports/data\n"
	pvHostPath = "capacity:\n  storage: 1Gi\naccessModes: [ReadWriteOnce]\nhostPath:\n  path: /var/lib/data\n"
	pvLocal    = "capacity:\n  storage: 1Gi\naccessModes: [ReadWriteOnce]\nlocal:\n  path: /mnt/disks/ssd1\n" +
		"nodeAffinity:\n  required:\n    nodeSelectorTerms:\n      - matchExpressions:\n          - key: kubernetes.io/hostname\n" +
		"            operator: In\n            values: [node-1]\n"
	pvCSI  = "capacity:\n  storage: 1Gi\naccessModes: [ReadWriteOnce]\ncsi:\n  driver: csi.example.com\n  volumeHandle: vol-1\n"
	pvFlex = "capacity:\n  storage: 1Gi\naccessModes: [ReadWriteOnce]\nflexVolume:\n  driver: example.com/flex\n  options:\n    path: /var/lib/data\n"
)

// pvDoc is the YAML of a PersistentVolume named thing with the given spec.
func pvDoc(apiVersion, spec string) string {
	return "apiVersion: " + apiVersion + "\nkind: PersistentVolume\nmetadata:\n  name: thing\nspec:\n" + htIndent(spec, "  ")
}

// pvTransform runs a one-component Application through the transform under
// policy in namespace demo, and generates every application of the result.
// The component is named web.
func pvTransform(componentType string, h oam.ComponentHandler, props map[string]any, policy oam.Policy) ([]client.Object, error) {
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{componentType: h}, nil)
	cluster, err := tr.Transform(mfApp(componentType, props), oam.TransformContext{Namespace: "demo", Policy: policy})
	if err != nil {
		return nil, err
	}
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		return nil, err
	}
	var out []client.Object
	for _, a := range apps {
		for _, o := range a.Objects {
			out = append(out, *o)
		}
	}
	return out, nil
}

// pvPolicyPaths are the four ways a build produces a PersistentVolume: the
// persistentvolume kind, and the three components that emit an object written
// elsewhere. Each builds the volume with the given spec under the given policy;
// a nil policy is none passed. where is how a refusal names the volume and the
// path of its spec: the kind's properties are the spec's fields, the other
// three hold a whole object.
var pvPolicyPaths = []struct {
	name  string
	where string
	build func(t *testing.T, spec string, policy *stubPolicy) ([]client.Object, error)
}{
	{
		name: "kind", where: "",
		build: func(t *testing.T, spec string, policy *stubPolicy) ([]client.Object, error) {
			return pvTransform("persistentvolume", &components.PersistentVolumeHandler{}, ptObject(t, spec), pvPolicy(policy))
		},
	},
	{
		name: "template delivery", where: `helmtemplate: rendered PersistentVolume "thing": spec.`,
		build: func(t *testing.T, spec string, policy *stubPolicy) ([]client.Object, error) {
			srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", map[string]string{"pv.yaml": pvDoc("v1", spec)})
			if policy != nil {
				withHost := *policy
				withHost.allowedRegistries = append(slices.Clone(policy.allowedRegistries), htServerHost(t, srvURL))
				policy = &withHost
			}
			return htTransform(srvURL, pvPolicy(policy))
		},
	},
	{
		name: "passthrough", where: `passthrough: object PersistentVolume "thing": spec.`,
		build: func(t *testing.T, spec string, policy *stubPolicy) ([]client.Object, error) {
			props := map[string]any{"object": ptObject(t, pvDoc("v1", spec)), "clusterScoped": true}
			return pvTransform("passthrough", &components.PassthroughHandler{}, props, pvPolicy(policy))
		},
	},
	{
		name: "manifests", where: `manifest source: object PersistentVolume "thing": spec.`,
		build: func(_ *testing.T, spec string, policy *stubPolicy) ([]client.Object, error) {
			return mfTransform("manifests", mfInline(pvDoc("v1", spec)), pvPolicy(policy))
		},
	},
}

// pvPolicy is p as the transform takes it: no policy when p is nil.
func pvPolicy(p *stubPolicy) oam.Policy {
	if p == nil {
		return nil
	}
	return p
}

// TestPersistentVolumePolicy_RefusedOnEveryPath: a hostPath or local volume
// under a policy that does not allow hostPath volumes, and a volume over the
// storage maximum, are refused whichever of the four paths produces the
// object, with a violation naming the component and the field. With no policy
// passed the transform applies its default, which allows no hostPath volume.
func TestPersistentVolumePolicy_RefusedOnEveryPath(t *testing.T) {
	cases := []struct {
		name   string
		spec   string
		policy *stubPolicy
		want   string
	}{
		{"hostPath volume", pvHostPath, ptStrictPolicy(), "hostPath: hostPath volumes are not allowed by environment policy"},
		{"hostPath volume with no policy passed", pvHostPath, nil, "hostPath: hostPath volumes are not allowed by environment policy"},
		{"local volume", pvLocal, ptStrictPolicy(), "local: local volumes expose a path on the node, as hostPath volumes do, and are not allowed by environment policy"},
		{"local volume with no policy passed", pvLocal, nil, "local: local volumes expose a path on the node"},
		{"capacity over the storage maximum", pvOverMax, ptStrictPolicy(), `capacity.storage "20Gi" exceeds enforced maximum "10Gi"`},
	}
	for _, path := range pvPolicyPaths {
		for _, tc := range cases {
			t.Run(path.name+"/"+tc.name, func(t *testing.T) {
				_, err := path.build(t, tc.spec, tc.policy)
				htWantViolation(t, err, path.where+tc.want)
				if path.where == "" && err != nil && strings.Contains(err.Error(), "spec.") {
					t.Errorf("error %q names a spec. path; the kind's properties are the spec's fields", err)
				}
			})
		}
	}
}

// TestPersistentVolumePolicy_AllowedOnEveryPath: a volume the policy does not
// refuse builds on each of the four paths: a remote source at the storage
// maximum, a hostPath and a local volume where the policy allows hostPath
// volumes, and a volume of any size with no storage maximum. A csi or
// flexVolume source is not checked, whatever its driver exposes, so it builds
// under the strict policy too.
func TestPersistentVolumePolicy_AllowedOnEveryPath(t *testing.T) {
	hostPathAllowed := ptStrictPolicy()
	hostPathAllowed.allowHostPathVols = true
	cases := []struct {
		name   string
		spec   string
		policy *stubPolicy
	}{
		{"nfs volume at the storage maximum", pvNFS, ptStrictPolicy()},
		{"hostPath volume where the policy allows it", pvHostPath, hostPathAllowed},
		{"local volume where the policy allows hostPath volumes", pvLocal, hostPathAllowed},
		{"any size with no policy passed", pvOverMax, nil},
		{"csi volume, not checked", pvCSI, ptStrictPolicy()},
		{"flexVolume volume, not checked", pvFlex, ptStrictPolicy()},
	}
	for _, path := range pvPolicyPaths {
		for _, tc := range cases {
			t.Run(path.name+"/"+tc.name, func(t *testing.T) {
				objs, err := path.build(t, tc.spec, tc.policy)
				if err != nil {
					t.Fatalf("transform: %v", err)
				}
				if len(objs) != 1 || objs[0].GetObjectKind().GroupVersionKind().Kind != "PersistentVolume" {
					t.Fatalf("generated %v, want the one PersistentVolume", objs)
				}
				if ns := objs[0].GetNamespace(); ns != "" {
					t.Errorf("namespace = %q, want none on a cluster-scoped object", ns)
				}
			})
		}
	}
}

// TestPersistentVolumePolicy_UnreadableVersionIsRefused: a PersistentVolume in
// an API version the build does not know reaches the check untyped, so its
// source and capacity cannot be read. It is refused rather than passed, on
// each path that takes an object written elsewhere, even where the spec it
// holds would be allowed.
func TestPersistentVolumePolicy_UnreadableVersionIsRefused(t *testing.T) {
	doc := pvDoc("v1beta1", pvNFS)
	want := []string{`apiVersion "v1beta1"`, "cannot be checked against environment policy"}

	t.Run("template delivery", func(t *testing.T) {
		srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", map[string]string{"pv.yaml": doc})
		_, err := htTransform(srvURL, mfPolicyFor(t, srvURL))
		htWantViolation(t, err, append(want, "helmtemplate: rendered PersistentVolume")...)
	})
	t.Run("passthrough", func(t *testing.T) {
		_, err := ptTransform(ptObject(t, doc), ptStrictPolicy())
		htWantViolation(t, err, append(want, "passthrough: object PersistentVolume")...)
	})
	t.Run("manifests", func(t *testing.T) {
		_, err := mfTransform("manifests", mfInline(doc), ptStrictPolicy())
		htWantViolation(t, err, append(want, "manifest source: object PersistentVolume")...)
	})
	// The parser unpacks a list of an unregistered kind into untyped objects,
	// so a v1 PersistentVolume inside one cannot be read either.
	t.Run("manifests, inside a list of an unregistered kind", func(t *testing.T) {
		list := "apiVersion: example.io/v1\nkind: ThingList\nitems:\n" +
			"  - apiVersion: v1\n    kind: PersistentVolume\n    metadata:\n      name: thing\n    spec:\n" + htIndent(pvNFS, "      ")
		_, err := mfTransform("manifests", mfInline(list), ptStrictPolicy())
		htWantViolation(t, err, `manifest source: object PersistentVolume "thing"`, `apiVersion "v1"`, "cannot be checked against environment policy")
	})
}

// TestPersistentVolumeConfig_ApplyPolicy_NilIsANoOp: a nil policy checks
// nothing, as on every other config.
func TestPersistentVolumeConfig_ApplyPolicy_NilIsANoOp(t *testing.T) {
	cfg := &components.PersistentVolumeConfig{Name: "data", Spec: corev1.PersistentVolumeSpec{
		PersistentVolumeSource: corev1.PersistentVolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/data"}},
	}}
	if err := cfg.ApplyPolicy(nil); err != nil {
		t.Errorf("ApplyPolicy(nil) = %v, want nil", err)
	}
}
