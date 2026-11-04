package components

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestTypedVolumeMounts_MatchAuthoredMounts holds validateTypedVolumeMounts to
// the rules parseMountExtras and checkMountPropagationPrivileged apply to an
// authored app.yaml mount (go-kure/launcher#1212): one table of mounts, each
// run through both, which must both accept it or both refuse it for the same
// reason. The table holds every refusal class of parseMountExtras, each rule on
// its own and the pairs whose first diagnostic depends on the order of the
// rules.
func TestTypedVolumeMounts_MatchAuthoredMounts(t *testing.T) {
	cases := []struct {
		name       string
		mount      map[string]any
		readOnly   bool
		privileged bool
		// reason is the part of the refusal after the field, "" for a mount both accept.
		reason string
	}{
		{name: "no option"},
		{name: "relative subPath", mount: map[string]any{"subPath": "a/b"}},
		{name: "subPath with .. inside a name", mount: map[string]any{"subPath": "a..b"}},
		{name: "empty subPath", mount: map[string]any{"subPath": ""}},
		{name: "subPathExpr with a variable", mount: map[string]any{"subPathExpr": "$(POD_NAME)/x"}},
		{name: "mountPropagation None", mount: map[string]any{"mountPropagation": "None"}},
		{name: "mountPropagation HostToContainer", mount: map[string]any{"mountPropagation": "HostToContainer"}},
		{name: "Bidirectional on a privileged container", mount: map[string]any{"mountPropagation": "Bidirectional"}, privileged: true},
		{name: "recursiveReadOnly Disabled", mount: map[string]any{"recursiveReadOnly": "Disabled"}},
		{name: "recursiveReadOnly Enabled with readOnly", mount: map[string]any{"recursiveReadOnly": "Enabled"}, readOnly: true},
		{name: "recursiveReadOnly IfPossible with readOnly and propagation None", mount: map[string]any{"recursiveReadOnly": "IfPossible", "mountPropagation": "None"}, readOnly: true},

		{name: "absolute subPath", mount: map[string]any{"subPath": "/abs"}, reason: `must be a relative path, got "/abs"`},
		{name: "subPath with a .. element", mount: map[string]any{"subPath": "a/../b"}, reason: `must not contain a '..' element, got "a/../b"`},
		{name: "subPath that is ..", mount: map[string]any{"subPath": ".."}, reason: `must not contain a '..' element, got ".."`},
		{name: "absolute subPathExpr", mount: map[string]any{"subPathExpr": "/x"}, reason: `must be a relative path, got "/x"`},
		{name: "subPathExpr with a .. element", mount: map[string]any{"subPathExpr": "x/.."}, reason: `must not contain a '..' element, got "x/.."`},
		{name: "subPath and subPathExpr", mount: map[string]any{"subPath": "a", "subPathExpr": "b"}, reason: "subPathExpr and subPath are mutually exclusive"},
		{name: "exclusion before the path rule", mount: map[string]any{"subPath": "/a", "subPathExpr": "/b"}, reason: "subPathExpr and subPath are mutually exclusive"},
		{name: "empty subPath beside a bad subPathExpr", mount: map[string]any{"subPath": "", "subPathExpr": "/b"}, reason: `must be a relative path, got "/b"`},
		{name: "unknown mountPropagation", mount: map[string]any{"mountPropagation": "Foo"}, reason: `must be one of None, HostToContainer, Bidirectional, got "Foo"`},
		{name: "empty mountPropagation", mount: map[string]any{"mountPropagation": ""}, reason: `must be one of None, HostToContainer, Bidirectional, got ""`},
		{name: "Bidirectional on a container that is not privileged", mount: map[string]any{"mountPropagation": "Bidirectional"}, reason: "Bidirectional is available only to a container with securityContext.privileged: true"},
		{name: "unknown recursiveReadOnly", mount: map[string]any{"recursiveReadOnly": "Bogus"}, readOnly: true, reason: `must be one of Disabled, IfPossible, Enabled, got "Bogus"`},
		{name: "recursiveReadOnly Enabled without readOnly", mount: map[string]any{"recursiveReadOnly": "Enabled"}, reason: "Enabled needs readOnly: true"},
		{name: "recursiveReadOnly IfPossible without readOnly", mount: map[string]any{"recursiveReadOnly": "IfPossible"}, reason: "IfPossible needs readOnly: true"},
		{name: "recursiveReadOnly with HostToContainer", mount: map[string]any{"recursiveReadOnly": "Enabled", "mountPropagation": "HostToContainer"}, readOnly: true, reason: `Enabled needs mountPropagation None or none at all, got "HostToContainer"`},
		{name: "readOnly rule before the propagation rule", mount: map[string]any{"recursiveReadOnly": "Enabled", "mountPropagation": "HostToContainer"}, reason: "Enabled needs readOnly: true"},
		{name: "propagation enum before the recursiveReadOnly rules", mount: map[string]any{"recursiveReadOnly": "Enabled", "mountPropagation": "Foo"}, readOnly: true, reason: `must be one of None, HostToContainer, Bidirectional, got "Foo"`},
	}
	const label = `containers[0] "app": volumeMounts[0]`
	var privileged = true
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sc *corev1.SecurityContext
			if tc.privileged {
				sc = &corev1.SecurityContext{Privileged: &privileged}
			}

			// The authored route: the helper the app.yaml containers use, then
			// the container-level check their callers add.
			vm := corev1.VolumeMount{Name: "data", MountPath: "/data", ReadOnly: tc.readOnly}
			authored := parseMountExtras(tc.mount, label, &vm)
			if authored == nil {
				authored = checkMountPropagationPrivileged(`containers[0] "app"`, []corev1.VolumeMount{vm}, sc)
			}

			// The typed route: the same mount as a pod spec holds it.
			typedMount := corev1.VolumeMount{Name: "data", MountPath: "/data", ReadOnly: tc.readOnly}
			if v, ok := tc.mount["subPath"].(string); ok {
				typedMount.SubPath = v
			}
			if v, ok := tc.mount["subPathExpr"].(string); ok {
				typedMount.SubPathExpr = v
			}
			if v, ok := tc.mount["mountPropagation"].(string); ok {
				mode := corev1.MountPropagationMode(v)
				typedMount.MountPropagation = &mode
			}
			if v, ok := tc.mount["recursiveReadOnly"].(string); ok {
				mode := corev1.RecursiveReadOnlyMode(v)
				typedMount.RecursiveReadOnly = &mode
			}
			typed := validateTypedVolumeMounts("", &corev1.PodSpec{Containers: []corev1.Container{{Name: "app", SecurityContext: sc, VolumeMounts: []corev1.VolumeMount{typedMount}}}})

			if tc.reason == "" {
				if authored != nil || typed != nil {
					t.Fatalf("authored = %v, typed = %v; want both to accept the mount", authored, typed)
				}
				return
			}
			for route, err := range map[string]error{"authored": authored, "typed": typed} {
				if err == nil {
					t.Errorf("%s route accepted the mount, want it refused with %q", route, tc.reason)
				} else if !strings.Contains(err.Error(), tc.reason) {
					t.Errorf("%s route: err = %v, want one holding %q", route, err, tc.reason)
				}
			}
		})
	}
}

// TestTypedVolumeMounts_NamesTheMount: the error names the container and the
// mount's position, init containers before regular ones, and a later mount's
// position when an earlier one is fine.
func TestTypedVolumeMounts_NamesTheMount(t *testing.T) {
	ok := corev1.VolumeMount{Name: "a", MountPath: "/a"}
	bad := corev1.VolumeMount{Name: "b", MountPath: "/b", SubPath: "/abs"}
	ps := &corev1.PodSpec{
		InitContainers: []corev1.Container{{Name: "init", VolumeMounts: []corev1.VolumeMount{ok, bad}}},
		Containers:     []corev1.Container{{Name: "app", VolumeMounts: []corev1.VolumeMount{bad}}},
	}
	err := validateTypedVolumeMounts("template.spec.", ps)
	if err == nil || !strings.Contains(err.Error(), `template.spec.initContainers[0] "init": volumeMounts[1].subPath: must be a relative path, got "/abs"`) {
		t.Errorf("err = %v, want the init container's second mount named", err)
	}
	ps.InitContainers = nil
	err = validateTypedVolumeMounts("template.spec.", ps)
	if err == nil || !strings.Contains(err.Error(), `template.spec.containers[0] "app": volumeMounts[0].subPath:`) {
		t.Errorf("err = %v, want the container's first mount named", err)
	}
}

// TestTypedVolumeMounts_NoMountsNoPodSpec: a pod spec with no mount passes, and
// so does an object that runs no pod.
func TestTypedVolumeMounts_NoMountsNoPodSpec(t *testing.T) {
	if err := validateTypedVolumeMounts("", &corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}}); err != nil {
		t.Errorf("a pod spec with no mount: %v", err)
	}
	if err := validateManifestMounts([]client.Object{&corev1.ConfigMap{}}); err != nil {
		t.Errorf("a ConfigMap: %v", err)
	}
}
