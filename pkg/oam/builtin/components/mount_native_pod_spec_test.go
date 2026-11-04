package components_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// mnKinds are the kind components that decode an upstream pod spec: the pod
// itself and those whose pod template holds it. prefix is where the
// pod spec sits in the component's properties, in front of a container's path.
var mnKinds = []struct {
	typ     string
	handler oam.ComponentHandler
	props   func(podSpec string) string
	prefix  string
}{
	{"pod", &components.PodHandler{}, func(s string) string { return s }, ""},
	{"podtemplate", &components.PodTemplateHandler{}, podTemplatePlain, "template.spec."},
	{"replicaset", &components.ReplicaSetHandler{}, rsPlain, "template.spec."},
	{"replicationcontroller", &components.ReplicationControllerHandler{}, rcPlain, "template.spec."},
}

// mnMount is the YAML of a container's volumeMounts list holding one mount of
// the volume named data, with extra lines (written at the mount's own depth).
func mnMount(indent string, extra ...string) string {
	var b strings.Builder
	b.WriteString(indent + "volumeMounts:\n")
	b.WriteString(indent + "  - name: data\n")
	b.WriteString(indent + "    mountPath: /data\n")
	for _, l := range extra {
		b.WriteString(indent + "    " + l + "\n")
	}
	return b.String()
}

const mnDataVolume = "volumes:\n  - name: data\n    emptyDir: {}\n"

// mnPlacements say where the mount sits in the pod spec: on the regular
// container or on an init container. label is the container's name in a
// refusal, after the pod spec's path.
var mnPlacements = []struct {
	name, label string
	spec        func(mount string) string
}{
	{"container", `containers[0] "app": `, func(mount string) string { return htPlainPod + mount + mnDataVolume }},
	{"init container", `initContainers[0] "init": `, func(mount string) string {
		return htPlainPod + "initContainers:\n  - name: init\n    image: registry.example/team/init:1.0.0\n" + mount + mnDataVolume
	}},
}

// mnRefused are the single-mount rules of upstream ValidateVolumeMounts, as
// the app.yaml containers apply them (parseMountExtras). extra is the mount's
// lines, and tail what the refusal says after the container.
var mnRefused = []struct {
	name  string
	extra []string
	tail  string
}{
	{"absolute subPath", []string{"subPath: /abs"}, `volumeMounts[0].subPath: must be a relative path, got "/abs"`},
	{"subPath with a .. element", []string{"subPath: a/../b"}, `volumeMounts[0].subPath: must not contain a '..' element, got "a/../b"`},
	{"subPath that is ..", []string{"subPath: .."}, `volumeMounts[0].subPath: must not contain a '..' element, got ".."`},
	{"absolute subPathExpr", []string{"subPathExpr: /$(POD_NAME)"}, `volumeMounts[0].subPathExpr: must be a relative path, got "/$(POD_NAME)"`},
	{"subPathExpr with a .. element", []string{"subPathExpr: ../x"}, `volumeMounts[0].subPathExpr: must not contain a '..' element, got "../x"`},
	{"subPath and subPathExpr", []string{"subPath: a", "subPathExpr: b"}, `volumeMounts[0]: subPathExpr and subPath are mutually exclusive`},
	{"unknown mountPropagation", []string{"mountPropagation: Foo"}, `volumeMounts[0].mountPropagation: must be one of None, HostToContainer, Bidirectional, got "Foo"`},
	{"empty mountPropagation", []string{`mountPropagation: ""`}, `volumeMounts[0].mountPropagation: must be one of None, HostToContainer, Bidirectional, got ""`},
	{"unknown recursiveReadOnly", []string{"readOnly: true", "recursiveReadOnly: Bogus"}, `volumeMounts[0].recursiveReadOnly: must be one of Disabled, IfPossible, Enabled, got "Bogus"`},
	{"recursiveReadOnly Enabled without readOnly", []string{"recursiveReadOnly: Enabled"}, `volumeMounts[0].recursiveReadOnly: Enabled needs readOnly: true`},
	{"recursiveReadOnly IfPossible without readOnly", []string{"recursiveReadOnly: IfPossible"}, `volumeMounts[0].recursiveReadOnly: IfPossible needs readOnly: true`},
	{"recursiveReadOnly with HostToContainer", []string{"readOnly: true", "recursiveReadOnly: IfPossible", "mountPropagation: HostToContainer"}, `volumeMounts[0].recursiveReadOnly: IfPossible needs mountPropagation None or none at all, got "HostToContainer"`},
	{"Bidirectional on a container that is not privileged", []string{"mountPropagation: Bidirectional"}, `volumeMounts[0].mountPropagation: Bidirectional is available only to a container with securityContext.privileged: true`},
}

// mnAccepted are mounts the rules allow; each must build unchanged.
var mnAccepted = []struct {
	name  string
	extra []string
}{
	{"no option at all", nil},
	{"relative subPath", []string{"subPath: sub/dir"}},
	{"a ..-looking name that is no element", []string{"subPath: a..b"}},
	{"empty subPath", []string{`subPath: ""`}},
	{"subPathExpr with a variable", []string{"subPathExpr: $(POD_NAME)/x"}},
	{"mountPropagation None", []string{"mountPropagation: None"}},
	{"mountPropagation HostToContainer", []string{"mountPropagation: HostToContainer"}},
	{"recursiveReadOnly Disabled", []string{"recursiveReadOnly: Disabled"}},
	{"recursiveReadOnly Enabled with readOnly", []string{"readOnly: true", "recursiveReadOnly: Enabled"}},
	{"recursiveReadOnly IfPossible with readOnly and propagation None", []string{"readOnly: true", "recursiveReadOnly: IfPossible", "mountPropagation: None"}},
}

// TestNativePodSpecMounts_RefusedOnTheKinds: a mount the single-mount rules
// refuse is refused when the pod, podtemplate, replicaset or
// replicationcontroller component is read, on a container and on an init
// container, with no policy applied. The error names the container and the
// mount's position and field.
func TestNativePodSpecMounts_RefusedOnTheKinds(t *testing.T) {
	for _, k := range mnKinds {
		for _, pl := range mnPlacements {
			for _, tc := range mnRefused {
				t.Run(k.typ+"/"+pl.name+"/"+tc.name, func(t *testing.T) {
					_, err := pvTransform(k.typ, k.handler, ptObject(t, k.props(pl.spec(mnMount("    ", tc.extra...)))), nil)
					if err == nil {
						t.Fatal("built, want the mount refused")
					}
					want := k.prefix + pl.label + tc.tail
					if !strings.Contains(err.Error(), want) {
						t.Errorf("err = %v, want one holding %q", err, want)
					}
				})
			}
		}
	}
}

// TestNativePodSpecMounts_AcceptedOnTheKinds: a mount the rules allow builds
// on each kind, so no input that builds today is refused for a reason the API
// server accepts.
func TestNativePodSpecMounts_AcceptedOnTheKinds(t *testing.T) {
	for _, k := range mnKinds {
		for _, pl := range mnPlacements {
			for _, tc := range mnAccepted {
				t.Run(k.typ+"/"+pl.name+"/"+tc.name, func(t *testing.T) {
					if objs, err := pvTransform(k.typ, k.handler, ptObject(t, k.props(pl.spec(mnMount("    ", tc.extra...)))), nil); err != nil || len(objs) != 1 {
						t.Errorf("objects %v, err %v; want the one object", objs, err)
					}
				})
			}
		}
	}
}

// TestNativePodSpecMounts_RefusedUnderAPolicy: the rule is not the policy's:
// the same refusal comes with a policy applied, and it is no policy class.
func TestNativePodSpecMounts_RefusedUnderAPolicy(t *testing.T) {
	for _, k := range mnKinds {
		t.Run(k.typ, func(t *testing.T) {
			spec := mnPlacements[0].spec(mnMount("    ", "subPath: /abs"))
			_, err := pvTransform(k.typ, k.handler, ptObject(t, k.props(spec)), rcStrict())
			trWant(t, err, k.prefix+mnPlacements[0].label+`volumeMounts[0].subPath: must be a relative path, got "/abs"`)
		})
	}
}

// TestNativePodSpecMounts_ManifestsOnEveryWorkloadKind: an inline manifests
// source holding an object of any kind whose pod spec the shared check reads is
// refused at the same rules, with no policy applied, naming the object and the
// path of its pod spec; a source whose mount is allowed builds.
func TestNativePodSpecMounts_ManifestsOnEveryWorkloadKind(t *testing.T) {
	for _, w := range ivWorkloads {
		for _, pl := range mnPlacements {
			t.Run(w.kind+"/"+pl.name, func(t *testing.T) {
				bad := w.podSpec + strings.TrimPrefix(pl.spec(mnMount("    ", "subPath: /abs")), htPlainPod)
				_, err := mfTransform("manifests", mfInline(ptWorkload(w.kind, w.apiVersion, w.path, bad)), nil)
				if err == nil {
					t.Fatal("built, want the mount refused")
				}
				for _, want := range []string{"manifest source: object " + w.kind, w.path + "." + pl.label + `volumeMounts[0].subPath: must be a relative path, got "/abs"`} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("err = %v, want one holding %q", err, want)
					}
				}

				good := w.podSpec + strings.TrimPrefix(pl.spec(mnMount("    ", "subPath: sub")), htPlainPod)
				if objs, err := mfTransform("manifests", mfInline(ptWorkload(w.kind, w.apiVersion, w.path, good)), nil); err != nil || len(objs) != 1 {
					t.Errorf("an allowed mount: objects %v, err %v; want the one %s", objs, err, w.kind)
				}
			})
		}
	}
}

// TestNativePodSpecMounts_ManifestsRefusedUnderAPolicy: the same refusal with a
// policy applied, and it is no policy class.
func TestNativePodSpecMounts_ManifestsRefusedUnderAPolicy(t *testing.T) {
	doc := ptDeployment(mnPlacements[0].spec(mnMount("    ", "subPath: ../x")))
	_, err := mfTransform("manifests", mfInline(doc), rcStrict())
	trWant(t, err, `spec.template.spec.containers[0] "app": volumeMounts[0].subPath: must not contain a '..' element, got "../x"`)
}

// TestNativePodSpecMounts_ManifestsURLSource: an object fetched from a url
// source is held to the rules too, when the source is read at generation.
func TestNativePodSpecMounts_ManifestsURLSource(t *testing.T) {
	serve := func(doc string) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(doc)) }))
		t.Cleanup(srv.Close)
		return srv.URL
	}
	_, err := mfTransform("manifests", map[string]any{"url": serve(ptDeployment(mnPlacements[0].spec(mnMount("    ", "subPath: /abs"))))}, nil)
	if err == nil {
		t.Fatal("built, want the mount refused")
	}
	for _, want := range []string{"manifest source: object Deployment", `spec.template.spec.containers[0] "app": volumeMounts[0].subPath: must be a relative path, got "/abs"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want one holding %q", err, want)
		}
	}
	if objs, err := mfTransform("manifests", map[string]any{"url": serve(ptDeployment(mnPlacements[0].spec(mnMount("    ", "subPath: sub"))))}, nil); err != nil || len(objs) != 1 {
		t.Errorf("an allowed mount: objects %v, err %v; want the one Deployment", objs, err)
	}
}

// TestNativePodSpecMounts_ObjectsWithoutAPodSpecPass: a source holding only
// objects that run no pod is unaffected.
func TestNativePodSpecMounts_ObjectsWithoutAPodSpecPass(t *testing.T) {
	doc := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\ndata:\n  subPath: /abs\n"
	if objs, err := mfTransform("manifests", mfInline(doc), nil); err != nil || len(objs) != 1 {
		t.Errorf("objects %v, err %v; want the one ConfigMap", objs, err)
	}
}
