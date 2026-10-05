package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// An image volume mounts an OCI image into a pod, and the kubelet pulls it as
// it pulls a container's image. These tests hold its reference to the allowed
// registries on every path that produces a pod spec (go-kure/launcher#790).
// The four paths of the pod kind and of the three pod template kinds are held
// in TestPodPolicy_RefusedOnEveryPath and
// TestPodTemplateKindsPolicy_RefusedOnEveryPath, cnpg-pooler's template in
// TestCnpgPoolerConfig_ApplyPolicy.

// ivVolume is the YAML of a pod spec's volumes list holding one image volume,
// named ext, with the given reference.
func ivVolume(reference string) string {
	return "volumes:\n  - name: ext\n    image:\n      reference: " + reference + "\n"
}

// ivWorkloads are the nine kinds whose pod spec the shared object check reads
// (renderedPodSpec), each with the path of that pod spec in the object.
var ivWorkloads = []struct{ kind, apiVersion, path, podSpec string }{
	{"Pod", "v1", "spec", htPlainPod},
	{"PodTemplate", "v1", "template.spec", htPlainPod},
	{"ReplicationController", "v1", "spec.template.spec", htPlainPod},
	{"Deployment", "apps/v1", "spec.template.spec", htPlainPod},
	{"StatefulSet", "apps/v1", "spec.template.spec", htPlainPod},
	{"DaemonSet", "apps/v1", "spec.template.spec", htPlainPod},
	{"ReplicaSet", "apps/v1", "spec.template.spec", htPlainPod},
	{"Job", "batch/v1", "spec.template.spec", "restartPolicy: Never\n" + htPlainPod},
	{"CronJob", "batch/v1", "spec.jobTemplate.spec.template.spec", "restartPolicy: Never\n" + htPlainPod},
}

// TestImageVolume_RefusedOnEveryWorkloadKind: an object written elsewhere, of
// each kind whose pod spec the shared check reads, is refused when an image
// volume of its pod names a registry the policy does not list, with the
// registry class and the volume named; the same object builds when the volume
// names a listed registry.
func TestImageVolume_RefusedOnEveryWorkloadKind(t *testing.T) {
	for _, w := range ivWorkloads {
		t.Run(w.kind, func(t *testing.T) {
			_, err := mfTransform("manifests", mfInline(ptWorkload(w.kind, w.apiVersion, w.path, w.podSpec+ivVolume("other.example/team/ext:1.0.0"))), rcStrict())
			rcWantClass(t, err, oam.RefusalRegistry)
			want := w.path + `: volume "ext" image.reference: image "other.example/team/ext:1.0.0" is not from an allowed registry [registry.example]`
			if err != nil && !strings.Contains(err.Error(), want) {
				t.Errorf("err = %v, want one holding %q", err, want)
			}

			objs, err := mfTransform("manifests", mfInline(ptWorkload(w.kind, w.apiVersion, w.path, w.podSpec+ivVolume("registry.example/team/ext:1.0.0"))), rcStrict())
			if err != nil || len(objs) != 1 {
				t.Errorf("an image volume from a listed registry: objects %v, err %v; want the one %s", objs, err, w.kind)
			}
		})
	}
}

// TestImageVolume_ClassOnEveryObjectPath: the refusal is the component's
// violation with the registry class on each of the five ways a build meets an
// object written elsewhere, at the transform and at generation.
func TestImageVolume_ClassOnEveryObjectPath(t *testing.T) {
	doc := ptDeployment(htPlainPod + ivVolume("other.example/team/ext:1.0.0"))
	for _, path := range rcObjectPaths {
		t.Run(path.name, func(t *testing.T) {
			err := path.build(t, doc, rcStrict)
			rcWantClass(t, err, oam.RefusalRegistry)
			if want := `volume "ext" image.reference: image "other.example/team/ext:1.0.0" is not from an allowed registry`; err != nil && !strings.Contains(err.Error(), want) {
				t.Errorf("err = %v, want one holding %q", err, want)
			}
		})
	}
}

// TestImageVolume_ReferenceIsReadAsAContainerImage: the registry of an image
// volume's reference is read as a container image's is. A reference with no
// registry host is Docker Hub's, a digest is not part of the host, and the
// host is compared with the list as written. No list allows every registry.
func TestImageVolume_ReferenceIsReadAsAContainerImage(t *testing.T) {
	const digest = "@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	cases := []struct {
		name      string
		reference string
		allowed   []string
		refused   bool
	}{
		{"no host, Docker Hub not listed", "ext:1.0.0", []string{"registry.example"}, true},
		{"no host, Docker Hub listed", "ext:1.0.0", []string{"docker.io"}, false},
		{"a repository path with no host", "team/ext:1.0.0", []string{"docker.io"}, false},
		{"no host, by digest", "ext" + digest, []string{"registry.example"}, true},
		{"listed host, by digest", "registry.example/team/ext" + digest, []string{"registry.example"}, false},
		{"other host, by digest", "other.example/team/ext" + digest, []string{"registry.example"}, true},
		{"listed host, tag and digest", "registry.example/team/ext:1.0.0" + digest, []string{"registry.example"}, false},
		{"host with a port, listed with it", "registry.example:5000/team/ext:1.0.0", []string{"registry.example:5000"}, false},
		{"host with a port, listed without it", "registry.example:5000/team/ext:1.0.0", []string{"registry.example"}, true},
		{"other host, no list", "other.example/team/ext:1.0.0", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The pod's own container is from a listed registry in every case, so a
			// refusal is the volume's.
			spec := "containers:\n  - name: app\n    image: " + ivListed(tc.allowed) + "/team/app:1.2.3\n" + ivVolume(`"`+tc.reference+`"`)
			_, err := pvTransform("pod", &components.PodHandler{}, ptObject(t, spec), &stubPolicy{allowedRegistries: tc.allowed})
			if !tc.refused {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			rcWantClass(t, err, oam.RefusalRegistry)
			if want := `volume "ext" image.reference: image "` + tc.reference + `" is not from an allowed registry`; err != nil && !strings.Contains(err.Error(), want) {
				t.Errorf("err = %v, want one holding %q", err, want)
			}
		})
	}
}

// ivListed is a registry the list allows: its first entry, or any host when
// there is no list.
func ivListed(allowed []string) string {
	if len(allowed) == 0 {
		return "registry.example"
	}
	return allowed[0]
}
