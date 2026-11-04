package components_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests cover go-kure/launcher#1177: the nested form of a `volumes`
// entry on the seven controller kinds, and the volume-source policy gates
// those kinds now share with the operator pod templates.

func nested(typ, name string, source props) props {
	return props{"name": name, "type": typ, "mountPath": "/mnt/" + name, typ: source}
}

func volumeByName(t *testing.T, ps corev1.PodSpec, name string) corev1.Volume {
	t.Helper()
	for _, v := range ps.Volumes {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("pod has no volume %q: %+v", name, ps.Volumes)
	return corev1.Volume{}
}

// TestVolumeSource_Render: every allow-listed source reaches the pod and its
// mount, on every workload kind.
func TestVolumeSource_Render(t *testing.T) {
	entries := []any{
		nested("projected", "tok", props{"sources": []any{
			props{"serviceAccountToken": props{"path": "token", "expirationSeconds": 3600, "audience": "api"}},
			props{"configMap": props{"name": "cm", "items": []any{props{"key": "a", "path": "a.txt"}}}},
		}}),
		nested("downwardAPI", "meta", props{"items": []any{
			props{"path": "labels", "fieldRef": props{"fieldPath": "metadata.labels"}},
		}}),
		nested("csi", "store", props{"driver": "secrets-store.csi.k8s.io", "readOnly": true, "volumeAttributes": props{"secretProviderClass": "spc"}}),
		nested("ephemeral", "scratch", props{"volumeClaimTemplate": props{"spec": props{
			"accessModes": []any{"ReadWriteOnce"},
			"resources":   props{"requests": props{"storage": "1Gi"}},
		}}}),
		nested("image", "assets", props{"reference": "ghcr.io/org/assets:v1", "pullPolicy": "IfNotPresent"}),
		nested("configMap", "cfg", props{"name": "app-cm", "defaultMode": 0o444, "optional": true}),
		nested("secret", "sec", props{"secretName": "app-secret", "items": []any{props{"key": "k", "path": "k"}}}),
		nested("emptyDir", "mem", props{"medium": "Memory", "sizeLimit": "64Mi"}),
	}
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			ps := podTemplateSpec(t, generateKind(t, k.handler, k.name, withProps(k.props, props{"volumes": entries})))
			if got := volumeByName(t, ps, "tok").Projected; got == nil || len(got.Sources) != 2 || got.Sources[0].ServiceAccountToken.Audience != "api" {
				t.Errorf("projected = %+v", got)
			}
			if got := volumeByName(t, ps, "meta").DownwardAPI; got == nil || got.Items[0].FieldRef.FieldPath != "metadata.labels" {
				t.Errorf("downwardAPI = %+v", got)
			}
			if got := volumeByName(t, ps, "store").CSI; got == nil || got.Driver != "secrets-store.csi.k8s.io" || got.ReadOnly == nil || !*got.ReadOnly {
				t.Errorf("csi = %+v", got)
			}
			if got := volumeByName(t, ps, "scratch").Ephemeral; got == nil || got.VolumeClaimTemplate == nil {
				t.Errorf("ephemeral = %+v", got)
			}
			if got := volumeByName(t, ps, "assets").Image; got == nil || got.Reference != "ghcr.io/org/assets:v1" || got.PullPolicy != corev1.PullIfNotPresent {
				t.Errorf("image = %+v", got)
			}
			if got := volumeByName(t, ps, "cfg").ConfigMap; got == nil || got.Name != "app-cm" || *got.DefaultMode != 0o444 || *got.Optional != true {
				t.Errorf("configMap = %+v", got)
			}
			if got := volumeByName(t, ps, "sec").Secret; got == nil || got.SecretName != "app-secret" || len(got.Items) != 1 {
				t.Errorf("secret = %+v", got)
			}
			if got := volumeByName(t, ps, "mem").EmptyDir; got == nil || got.Medium != corev1.StorageMediumMemory || got.SizeLimit.Cmp(resource.MustParse("64Mi")) != 0 {
				t.Errorf("emptyDir = %+v", got)
			}
			mounts := ps.Containers[0].VolumeMounts
			for _, name := range []string{"tok", "meta", "store", "scratch", "assets", "cfg", "sec", "mem"} {
				if !slices.ContainsFunc(mounts, func(m corev1.VolumeMount) bool { return m.Name == name && m.MountPath == "/mnt/"+name }) {
					t.Errorf("no mount for %q in %+v", name, mounts)
				}
			}
		})
	}
}

// TestVolumeSource_MountOptions: the mount keys of go-kure/launcher#1198 apply
// to a nested source as to a flat one.
func TestVolumeSource_MountOptions(t *testing.T) {
	e := nested("csi", "store", props{"driver": "d"})
	e["readOnly"] = true
	e["subPath"] = "sub"
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			ps := podTemplateSpec(t, generateKind(t, k.handler, k.name, withProps(k.props, props{"volumes": []any{e}})))
			var got corev1.VolumeMount
			for _, m := range ps.Containers[0].VolumeMounts {
				if m.Name == "store" {
					got = m
				}
			}
			if !got.ReadOnly || got.SubPath != "sub" {
				t.Errorf("mount = %+v, want readOnly and subPath sub", got)
			}
		})
	}
}

// TestVolumeSource_Refusals: each rule refuses on every workload kind, and the
// error names the volume.
func TestVolumeSource_Refusals(t *testing.T) {
	cases := []struct {
		name  string
		entry props
		want  []string
	}{
		{"unknown nested field", nested("csi", "v", props{"driver": "d", "bogus": 1}), []string{`volume "v"`, "bogus"}},
		{"wrongly typed nested field", nested("csi", "v", props{"driver": 5}), []string{`volume "v"`}},
		{"nested source is not an object", props{"name": "v", "type": "projected", "mountPath": "/m", "projected": "x"}, []string{`volume "v"`, "must be an object"}},
		{"nested source missing", props{"name": "v", "type": "projected", "mountPath": "/m"}, []string{`volume "v"`, "projected object"}},
		{"nested source null", props{"name": "v", "type": "projected", "mountPath": "/m", "projected": nil}, []string{`volume "v"`, "projected object"}},
		{"unknown key beside the nested object", func() props { e := nested("csi", "v", props{"driver": "d"}); e["extra"] = 1; return e }(), []string{`volume "v"`, "extra"}},
		{"flat plus nested configMap", func() props {
			e := nested("configMap", "v", props{"name": "x"})
			e["configMapName"] = "y"
			return e
		}(), []string{`volume "v"`, "mutually exclusive"}},
		{"flat plus nested secret", func() props {
			e := nested("secret", "v", props{"secretName": "x"})
			e["secretName"] = "y"
			return e
		}(), []string{`volume "v"`, "mutually exclusive"}},
		{"flat plus nested emptyDir", func() props {
			e := nested("emptyDir", "v", props{"medium": "Memory"})
			e["sizeLimit"] = "1Gi"
			return e
		}(), []string{`volume "v"`, "mutually exclusive"}},
		{"devicePath on a nested source", props{"name": "v", "type": "csi", "devicePath": "/dev/x", "csi": props{"driver": "d"}}, []string{`volume "v"`, "devicePath is only valid on a pvc volume"}},
		{"Block ephemeral claim", nested("ephemeral", "v", props{"volumeClaimTemplate": props{"spec": props{
			"accessModes": []any{"ReadWriteOnce"}, "volumeMode": "Block",
			"resources": props{"requests": props{"storage": "1Gi"}},
		}}}), []string{`volume "v"`, "volumeMode: Block is not supported"}},
		{"negative emptyDir sizeLimit", nested("emptyDir", "v", props{"sizeLimit": "-1Gi"}), []string{`volume "v"`, "must not be negative"}},
		{"invalid image reference", nested("image", "v", props{"reference": "NOT A REF!"}), []string{`volume "v"`, "image.reference"}},
		{"defaulted zero downwardAPI apiVersion", nested("downwardAPI", "v", props{"items": []any{props{"path": "p", "fieldRef": props{"fieldPath": "metadata.name", "apiVersion": ""}}}}), []string{`volume "v"`, "cannot be carried"}},
		{"hostPath nested", props{"name": "v", "type": "hostPath", "mountPath": "/m", "hostPath": props{"path": "/x"}, "path": "/x"}, []string{`volume "v"`}},
		{"nfs", props{"name": "v", "type": "nfs", "mountPath": "/m", "nfs": props{"server": "s", "path": "/p"}}, []string{`volume "v"`, `"nfs"`, "Pod Security restricted"}},
		{"gitRepo", props{"name": "v", "type": "gitRepo", "mountPath": "/m"}, []string{`volume "v"`, "Pod Security restricted"}},
		{"persistentVolumeClaim spelled upstream", props{"name": "v", "type": "persistentVolumeClaim", "mountPath": "/m"}, []string{`volume "v"`, "type: pvc"}},
		{"unknown type", props{"name": "v", "type": "bogus", "mountPath": "/m"}, []string{`volume "v"`, "unrecognized type"}},
	}
	for _, c := range cases {
		for _, k := range workloadKinds {
			t.Run(c.name+"/"+k.name, func(t *testing.T) {
				refuses(t, k.name, withProps(k.props, props{"volumes": []any{c.entry}}), c.want)
			})
		}
	}
}

// TestVolumeSource_FlatFormUnchanged: a flat entry still reads as before, a
// claimName-less pvc is still refused where no claim is generated.
func TestVolumeSource_FlatFormUnchanged(t *testing.T) {
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			e := props{"name": "d", "type": "configMap", "mountPath": "/d", "configMapName": "cm"}
			ps := podTemplateSpec(t, generateKind(t, k.handler, k.name, withProps(k.props, props{"volumes": []any{e}})))
			if got := volumeByName(t, ps, "d").ConfigMap; got == nil || got.Name != "cm" {
				t.Errorf("configMap = %+v", got)
			}
		})
	}
	for _, kind := range []string{"daemonset", "job", "cronjob"} {
		for _, k := range workloadKinds {
			if k.name != kind {
				continue
			}
			t.Run("pvc without claimName/"+kind, func(t *testing.T) {
				refuses(t, kind, withProps(k.props, props{"volumes": []any{props{"name": "p", "type": "pvc", "mountPath": "/p"}}}), []string{`volume "p"`})
			})
		}
	}
}

// TestVolumeSource_NullAlternateFlatKey: the alternate form of an entry reads
// as absent when it is null, as every other null key does, both the flat key
// beside a nested object and the nested object beside flat keys; only a
// non-null one is refused as exclusive. A null devicePath beside a nested
// source reads as absent too (a set one is refused before the source is read).
func TestVolumeSource_NullAlternateFlatKey(t *testing.T) {
	cases := []props{
		{"name": "v", "type": "configMap", "mountPath": "/m", "configMapName": "cm", "configMap": nil},
		{"name": "v", "type": "secret", "mountPath": "/m", "secretName": "s", "secret": nil},
		{"name": "v", "type": "emptyDir", "mountPath": "/m", "sizeLimit": "1Gi", "emptyDir": nil},
		{"name": "v", "type": "csi", "mountPath": "/m", "devicePath": nil, "csi": props{"driver": "example.com"}},
		{"name": "v", "type": "configMap", "mountPath": "/m", "configMapName": nil, "configMap": props{"name": "cm"}},
		{"name": "v", "type": "secret", "mountPath": "/m", "secretName": nil, "secret": props{"secretName": "s"}},
		{"name": "v", "type": "emptyDir", "mountPath": "/m", "sizeLimit": nil, "emptyDir": props{"medium": "Memory"}},
	}
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			for _, e := range cases {
				ps := podTemplateSpec(t, generateKind(t, k.handler, k.name, withProps(k.props, props{"volumes": []any{e}})))
				if len(ps.Volumes) != 1 {
					t.Errorf("%v: volumes = %+v, want one", e["type"], ps.Volumes)
				}
			}
		})
	}
}

// volumeSourceKeysOf reads the json names of the fields of corev1.VolumeSource.
func volumeSourceKeysOf() []string {
	var keys []string
	typ := reflect.TypeOf(corev1.VolumeSource{})
	for i := 0; i < typ.NumField(); i++ {
		if tag, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ","); tag != "" {
			keys = append(keys, tag)
		}
	}
	return keys
}

// TestVolumeSourceKeys_MatchUpstream: the list of refused source names stays
// the upstream type's, so a source added by a k8s.io/api bump is noticed. The
// allow-list is checked against it, and every other source is refused with a
// reason, not as unrecognized.
func TestVolumeSourceKeys_MatchUpstream(t *testing.T) {
	allowed := []string{"configMap", "csi", "downwardAPI", "emptyDir", "ephemeral", "image", "projected", "secret", "hostPath", "persistentVolumeClaim"}
	keys := volumeSourceKeysOf()
	if len(keys) != 30 {
		t.Fatalf("corev1.VolumeSource has %d sources, want 30; classify the new one and update components.volumeSourceKeys", len(keys))
	}
	for _, key := range keys {
		if slices.Contains(allowed, key) {
			continue
		}
		t.Run(key, func(t *testing.T) {
			refuses(t, "deployment", withProps(workloadKinds[5].props, props{"volumes": []any{props{"name": "v", "type": key, "mountPath": "/m"}}}), []string{`volume "v"`, "Pod Security restricted"})
		})
	}
}

func volumePolicyConfig(t *testing.T, kind string, entry props) oam.Enforceable {
	t.Helper()
	for _, k := range workloadKinds {
		if k.name != kind {
			continue
		}
		cfg, err := k.handler.ToApplicationConfig(&oam.Component{Name: "app", Type: kind, Properties: withProps(k.props, props{"volumes": []any{entry}})}, "default")
		if err != nil {
			t.Fatalf("ToApplicationConfig: %v", err)
		}
		return cfg.(oam.Enforceable)
	}
	t.Fatalf("no kind %q", kind)
	return nil
}

// TestVolumeSource_PolicyGates: the gates enforcePodTemplatePolicy applies to
// a volume source hold on the controller kinds too (go-kure/launcher#1177).
func TestVolumeSource_PolicyGates(t *testing.T) {
	ephemeral := func(storage string) props {
		return nested("ephemeral", "scratch", props{"volumeClaimTemplate": props{"spec": props{
			"accessModes": []any{"ReadWriteOnce"},
			"resources":   props{"requests": props{"storage": storage}},
		}}})
	}
	image := nested("image", "assets", props{"reference": "evil.example.com/assets:v1"})
	cases := []struct {
		name   string
		entry  props
		policy *stubPolicy
		want   string
	}{
		{"ephemeral claim over the storage cap", ephemeral("50Gi"), &stubPolicy{maxStorageSize: "10Gi"}, "scratch"},
		{"image volume outside the registry allowlist", image, &stubPolicy{allowedRegistries: []string{"ghcr.io"}}, "assets"},
	}
	for _, kind := range []string{"deployment", "statefulset", "daemonset", "job", "cronjob"} {
		for _, c := range cases {
			t.Run(c.name+"/"+kind, func(t *testing.T) {
				err := volumePolicyConfig(t, kind, c.entry).ApplyPolicy(c.policy)
				if err == nil || !strings.Contains(err.Error(), c.want) {
					t.Fatalf("ApplyPolicy = %v, want an error naming %q", err, c.want)
				}
			})
		}
	}
	t.Run("within the policy", func(t *testing.T) {
		if err := volumePolicyConfig(t, "deployment", ephemeral("5Gi")).ApplyPolicy(&stubPolicy{maxStorageSize: "10Gi"}); err != nil {
			t.Errorf("ephemeral within the cap: %v", err)
		}
		ok := nested("image", "assets", props{"reference": "ghcr.io/org/assets:v1"})
		if err := volumePolicyConfig(t, "job", ok).ApplyPolicy(&stubPolicy{allowedRegistries: []string{"ghcr.io"}}); err != nil {
			t.Errorf("image within the allowlist: %v", err)
		}
	})
	t.Run("csi and projected have no gate", func(t *testing.T) {
		for _, e := range []props{nested("csi", "c", props{"driver": "d"}), nested("projected", "p", props{"sources": []any{props{"configMap": props{"name": "x"}}}})} {
			if err := volumePolicyConfig(t, "deployment", e).ApplyPolicy(&stubPolicy{maxStorageSize: "1Ki", allowedRegistries: []string{"ghcr.io"}}); err != nil {
				t.Errorf("%v: %v", e["type"], err)
			}
		}
	})
}
