package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// These tests cover go-kure/launcher#1227: the alertmanager, prometheus and
// thanosruler components hold the volumeMounts they pass to the pods to the
// single-mount rules every other typed pod reader applies (validateTypedMount,
// go-kure/launcher#1212). (a) The volumeMounts of a listed container or init
// container, patches of the operator's own included; (b) the top-level
// volumeMounts, which the operator appends to its own mounts as written.

// mmKind is a monitoring workload kind: main is the container the top-level
// volumeMounts reach, patch a container and patchInit an init container the
// operator generates ("" where it generates none).
type mmKind struct {
	typ       string
	handler   oam.ComponentHandler
	main      string
	patch     string
	patchInit string
}

var mmKinds = []mmKind{
	{"alertmanager", &components.AlertmanagerHandler{}, "alertmanager", "config-reloader", "init-config-reloader"},
	{"prometheus", &components.PrometheusHandler{}, "prometheus", "config-reloader", "init-config-reloader"},
	{"thanosruler", &components.ThanosRulerHandler{}, "thanos-ruler", "config-reloader", ""},
}

// mmBuild builds a component of kind k holding props, under no policy.
func mmBuild(k mmKind, props map[string]any) error {
	_, err := policyFreeTransform(k.typ, k.handler, nil, oam.Component{Name: "web", Properties: props})
	return err
}

// mmBuildPrivileged is mmBuild under a policy that admits a privileged
// container, for the cases that assert the mount rules admit one: with no
// policy passed, the build refuses securityContext.privileged before any mount
// rule is asked.
func mmBuildPrivileged(k mmKind, props map[string]any) error {
	_, err := pvTransform(k.typ, k.handler, props, &stubPolicy{allowPrivileged: true})
	return err
}

// mmMount is a mount of the volume scratch at /scratch carrying the keys of
// extra.
func mmMount(extra map[string]any) map[string]any {
	m := map[string]any{"name": "scratch", "mountPath": "/scratch"}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// mmProps is a spec with the volume scratch and the given further keys.
func mmProps(extra map[string]any) map[string]any {
	p := map[string]any{"volumes": []any{map[string]any{"name": "scratch", "emptyDir": map[string]any{}}}}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// mmListed is a container the operator does not generate, named side, with the
// given mounts and further keys.
func mmListed(mounts []any, extra map[string]any) map[string]any {
	c := map[string]any{"name": "side", "image": "registry.example/team/side:1.0.0", "volumeMounts": mounts}
	for k, v := range extra {
		c[k] = v
	}
	return c
}

// mmRefused are the mounts the rules refuse. tail follows the path of the
// mount, `volumeMounts[0]`, so it carries its own separator.
var mmRefused = []struct {
	name  string
	mount map[string]any
	tail  string
}{
	{"absolute subPath", map[string]any{"subPath": "/abs"}, `.subPath: must be a relative path, got "/abs"`},
	{"subPath with a .. element", map[string]any{"subPath": "../x"}, `.subPath: must not contain a '..' element, got "../x"`},
	{"subPath beside subPathExpr", map[string]any{"subPath": "a", "subPathExpr": "b"}, `: subPathExpr and subPath are mutually exclusive`},
	{"absolute subPathExpr", map[string]any{"subPathExpr": "/abs"}, `.subPathExpr: must be a relative path, got "/abs"`},
	{"unknown mountPropagation", map[string]any{"mountPropagation": "Foo"}, `.mountPropagation: must be one of None, HostToContainer, Bidirectional, got "Foo"`},
	{"unknown recursiveReadOnly", map[string]any{"recursiveReadOnly": "Foo"}, `.recursiveReadOnly: must be one of Disabled, IfPossible, Enabled, got "Foo"`},
	{"recursiveReadOnly Enabled without readOnly", map[string]any{"recursiveReadOnly": "Enabled"}, `.recursiveReadOnly: Enabled needs readOnly: true`},
	{"recursiveReadOnly with HostToContainer", map[string]any{"readOnly": true, "recursiveReadOnly": "IfPossible", "mountPropagation": "HostToContainer"},
		`.recursiveReadOnly: IfPossible needs mountPropagation None or none at all, got "HostToContainer"`},
	{"Bidirectional on a container that is not privileged", map[string]any{"mountPropagation": "Bidirectional"},
		`.mountPropagation: Bidirectional is available only to a container with securityContext.privileged: true`},
}

// mmAccepted are mounts the rules admit.
var mmAccepted = []struct {
	name  string
	mount map[string]any
}{
	{"relative subPath", map[string]any{"subPath": "sub"}},
	{"subPathExpr", map[string]any{"subPathExpr": "$(POD_NAME)/x"}},
	{"mountPropagation HostToContainer", map[string]any{"mountPropagation": "HostToContainer"}},
	{"recursiveReadOnly Enabled with readOnly", map[string]any{"readOnly": true, "recursiveReadOnly": "Enabled"}},
	{"recursiveReadOnly Disabled", map[string]any{"recursiveReadOnly": "Disabled"}},
}

// TestMonitoringMounts_ListedContainersAndInitContainers: the mounts of a
// container or init container the operator does not generate are refused by
// each rule, naming the container and the mount, and admitted when they follow
// them (a).
func TestMonitoringMounts_ListedContainersAndInitContainers(t *testing.T) {
	for _, k := range mmKinds {
		for _, list := range []string{"containers", "initContainers"} {
			for _, tc := range mmRefused {
				t.Run(k.typ+"/"+list+"/"+tc.name, func(t *testing.T) {
					err := mmBuild(k, mmProps(map[string]any{list: []any{mmListed([]any{mmMount(tc.mount)}, nil)}}))
					trWant(t, err, list+`[0] "side": volumeMounts[0]`+tc.tail)
				})
			}
			for _, tc := range mmAccepted {
				t.Run(k.typ+"/"+list+"/accepted: "+tc.name, func(t *testing.T) {
					if err := mmBuild(k, mmProps(map[string]any{list: []any{mmListed([]any{mmMount(tc.mount)}, nil)}})); err != nil {
						t.Errorf("err = %v, want the component built", err)
					}
				})
			}
		}
	}
}

// TestMonitoringMounts_SecondMountAndSecondContainerAreNamed: the refusal names
// the mount and the container it is in, not the first.
func TestMonitoringMounts_SecondMountAndSecondContainerAreNamed(t *testing.T) {
	for _, k := range mmKinds {
		t.Run(k.typ, func(t *testing.T) {
			other := map[string]any{"name": "other", "image": "registry.example/team/other:1.0.0"}
			second := mmListed([]any{mmMount(nil), mmMount(map[string]any{"mountPath": "/two", "subPath": "../x"})}, nil)
			err := mmBuild(k, mmProps(map[string]any{"containers": []any{other, second}}))
			trWant(t, err, `containers[1] "side": volumeMounts[1].subPath: must not contain a '..' element, got "../x"`)
		})
	}
}

// TestMonitoringMounts_PatchOfAGeneratedContainer: an entry named for a
// container the operator generates is merged into it, and the merge can supply
// readOnly from the operator's own mount at the path, so a patch is not
// refused for recursiveReadOnly without readOnly; every other rule holds. The
// same mount on a container of another name is refused.
func TestMonitoringMounts_PatchOfAGeneratedContainer(t *testing.T) {
	rro := mmMount(map[string]any{"recursiveReadOnly": "Enabled"})
	for _, k := range mmKinds {
		patches := []struct{ list, name string }{{"containers", k.patch}}
		if k.patchInit != "" {
			patches = append(patches, struct{ list, name string }{"initContainers", k.patchInit})
		}
		for _, p := range patches {
			patch := func(mounts []any, extra map[string]any) map[string]any {
				c := map[string]any{"name": p.name, "volumeMounts": mounts}
				for key, v := range extra {
					c[key] = v
				}
				return c
			}
			t.Run(k.typ+"/"+p.list+"/readOnly may come from the operator's mount", func(t *testing.T) {
				if err := mmBuild(k, mmProps(map[string]any{p.list: []any{patch([]any{rro}, nil)}})); err != nil {
					t.Errorf("err = %v, want the patch admitted", err)
				}
			})
			t.Run(k.typ+"/"+p.list+"/the same mount on a container of another name", func(t *testing.T) {
				err := mmBuild(k, mmProps(map[string]any{p.list: []any{mmListed([]any{rro}, nil)}}))
				trWant(t, err, p.list+`[0] "side": volumeMounts[0].recursiveReadOnly: Enabled needs readOnly: true`)
			})
			t.Run(k.typ+"/"+p.list+"/propagation still holds", func(t *testing.T) {
				mount := mmMount(map[string]any{"recursiveReadOnly": "Enabled", "mountPropagation": "HostToContainer"})
				err := mmBuild(k, mmProps(map[string]any{p.list: []any{patch([]any{mount}, nil)}}))
				trWant(t, err, p.list+`[0] "`+p.name+`": volumeMounts[0].recursiveReadOnly: Enabled needs mountPropagation None or none at all, got "HostToContainer"`)
			})
			t.Run(k.typ+"/"+p.list+"/subPath still holds", func(t *testing.T) {
				err := mmBuild(k, mmProps(map[string]any{p.list: []any{patch([]any{mmMount(map[string]any{"subPath": "/abs"})}, nil)}}))
				trWant(t, err, p.list+`[0] "`+p.name+`": volumeMounts[0].subPath: must be a relative path, got "/abs"`)
			})
			t.Run(k.typ+"/"+p.list+"/Bidirectional needs the patch to be privileged", func(t *testing.T) {
				bidi := mmMount(map[string]any{"mountPropagation": "Bidirectional"})
				err := mmBuild(k, mmProps(map[string]any{p.list: []any{patch([]any{bidi}, nil)}}))
				trWant(t, err, p.list+`[0] "`+p.name+`": volumeMounts[0].mountPropagation: Bidirectional is available only to a container with securityContext.privileged: true`)

				privileged := patch([]any{bidi}, map[string]any{"securityContext": map[string]any{"privileged": true}})
				if err := mmBuildPrivileged(k, mmProps(map[string]any{p.list: []any{privileged}})); err != nil {
					t.Errorf("a privileged patch: err = %v, want it admitted", err)
				}
			})
		}
	}
}

// TestMonitoringMounts_TopLevelVolumeMounts: the top-level volumeMounts are
// held to the rules the operator copies them under, each refused naming the
// entry and admitted when it follows them (b).
func TestMonitoringMounts_TopLevelVolumeMounts(t *testing.T) {
	for _, k := range mmKinds {
		for _, tc := range mmRefused {
			t.Run(k.typ+"/"+tc.name, func(t *testing.T) {
				err := mmBuild(k, mmProps(map[string]any{"volumeMounts": []any{mmMount(tc.mount)}}))
				trWant(t, err, "volumeMounts[0]"+tc.tail)
			})
		}
		for _, tc := range mmAccepted {
			t.Run(k.typ+"/accepted: "+tc.name, func(t *testing.T) {
				if err := mmBuild(k, mmProps(map[string]any{"volumeMounts": []any{mmMount(tc.mount)}})); err != nil {
					t.Errorf("err = %v, want the component built", err)
				}
			})
		}
	}
}

// TestMonitoringMounts_TopLevelSeesThePatchOfTheMainContainer: the operator
// merges a patch of the main container into it, a mount at a path written as
// the top-level entry's into that entry (README: a patch mount keeps the earlier
// mount's fields where it leaves them unset). The top-level entry is judged as
// merged: readOnly from the patch admits recursiveReadOnly, and a privileged
// patch admits Bidirectional.
func TestMonitoringMounts_TopLevelSeesThePatchOfTheMainContainer(t *testing.T) {
	rro := mmMount(map[string]any{"recursiveReadOnly": "Enabled"})
	bidi := mmMount(map[string]any{"mountPropagation": "Bidirectional"})
	for _, k := range mmKinds {
		patch := func(mounts []any, extra map[string]any) map[string]any {
			c := map[string]any{"name": k.main, "volumeMounts": mounts}
			for key, v := range extra {
				c[key] = v
			}
			return c
		}
		t.Run(k.typ+"/readOnly comes from the patch's mount at the path", func(t *testing.T) {
			props := mmProps(map[string]any{
				"volumeMounts": []any{rro},
				"containers":   []any{patch([]any{map[string]any{"name": "scratch", "mountPath": "/scratch", "readOnly": true}}, nil)},
			})
			if err := mmBuild(k, props); err != nil {
				t.Errorf("err = %v, want the entry admitted as merged", err)
			}
		})
		t.Run(k.typ+"/a patch mount at another path does not supply it", func(t *testing.T) {
			props := mmProps(map[string]any{
				"volumeMounts": []any{rro},
				"containers":   []any{patch([]any{map[string]any{"name": "scratch", "mountPath": "/elsewhere", "readOnly": true}}, nil)},
			})
			trWant(t, mmBuild(k, props), `volumeMounts[0].recursiveReadOnly: Enabled needs readOnly: true`)
		})
		t.Run(k.typ+"/propagation comes from the patch's mount at the path", func(t *testing.T) {
			props := mmProps(map[string]any{
				"volumeMounts": []any{mmMount(map[string]any{"readOnly": true, "recursiveReadOnly": "Enabled"})},
				"containers":   []any{patch([]any{map[string]any{"name": "scratch", "mountPath": "/scratch", "mountPropagation": "HostToContainer"}}, nil)},
			})
			trWant(t, mmBuild(k, props), `volumeMounts[0].recursiveReadOnly: Enabled needs mountPropagation None or none at all, got "HostToContainer"`)
		})
		// The next three cases pin the overlay lines of mergedMount
		// (operator_pods.go:262-264 subPath, :265-267 subPathExpr, :271-273
		// recursiveReadOnly). Each patch mount is valid on its own, so the
		// refusal can only come from the merged entry; the entry alone is
		// admitted.
		t.Run(k.typ+"/subPath comes from the patch's mount at the path", func(t *testing.T) {
			entry := mmMount(map[string]any{"subPathExpr": "$(POD_NAME)"})
			props := mmProps(map[string]any{
				"volumeMounts": []any{entry},
				"containers":   []any{patch([]any{map[string]any{"name": "scratch", "mountPath": "/scratch", "subPath": "logs"}}, nil)},
			})
			trWant(t, mmBuild(k, props), `volumeMounts[0]: subPathExpr and subPath are mutually exclusive`)
			if err := mmBuild(k, mmProps(map[string]any{"volumeMounts": []any{entry}})); err != nil {
				t.Errorf("the entry alone: err = %v, want it admitted", err)
			}
		})
		t.Run(k.typ+"/subPathExpr comes from the patch's mount at the path", func(t *testing.T) {
			entry := mmMount(map[string]any{"subPath": "logs"})
			props := mmProps(map[string]any{
				"volumeMounts": []any{entry},
				"containers":   []any{patch([]any{map[string]any{"name": "scratch", "mountPath": "/scratch", "subPathExpr": "$(POD_NAME)"}}, nil)},
			})
			trWant(t, mmBuild(k, props), `volumeMounts[0]: subPathExpr and subPath are mutually exclusive`)
			if err := mmBuild(k, mmProps(map[string]any{"volumeMounts": []any{entry}})); err != nil {
				t.Errorf("the entry alone: err = %v, want it admitted", err)
			}
		})
		t.Run(k.typ+"/recursiveReadOnly comes from the patch's mount at the path", func(t *testing.T) {
			entry := mmMount(nil)
			props := mmProps(map[string]any{
				"volumeMounts": []any{entry},
				"containers":   []any{patch([]any{map[string]any{"name": "scratch", "mountPath": "/scratch", "recursiveReadOnly": "Enabled"}}, nil)},
			})
			trWant(t, mmBuild(k, props), `volumeMounts[0].recursiveReadOnly: Enabled needs readOnly: true`)
			if err := mmBuild(k, mmProps(map[string]any{"volumeMounts": []any{entry}})); err != nil {
				t.Errorf("the entry alone: err = %v, want it admitted", err)
			}
		})
		t.Run(k.typ+"/Bidirectional needs a privileged patch of the main container", func(t *testing.T) {
			trWant(t, mmBuild(k, mmProps(map[string]any{"volumeMounts": []any{bidi}})),
				`volumeMounts[0].mountPropagation: Bidirectional is available only to a container with securityContext.privileged: true`)

			props := mmProps(map[string]any{
				"volumeMounts": []any{bidi},
				"containers":   []any{patch(nil, map[string]any{"securityContext": map[string]any{"privileged": true}})},
			})
			if err := mmBuildPrivileged(k, props); err != nil {
				t.Errorf("err = %v, want it admitted with the main container privileged", err)
			}

			other := map[string]any{"name": k.patch, "securityContext": map[string]any{"privileged": true}}
			trWant(t, mmBuild(k, mmProps(map[string]any{"volumeMounts": []any{bidi}, "containers": []any{other}})),
				`volumeMounts[0].mountPropagation: Bidirectional is available only to a container with securityContext.privileged: true`)
		})
	}
}

// TestMonitoringMounts_PathClashesStillRefused: the checks that were there stay
// as they were: two entries at one path are refused naming both, after the
// mount's own rules.
func TestMonitoringMounts_PathClashesStillRefused(t *testing.T) {
	for _, k := range mmKinds {
		t.Run(k.typ, func(t *testing.T) {
			err := mmBuild(k, mmProps(map[string]any{"volumeMounts": []any{mmMount(nil), mmMount(nil)}}))
			trWant(t, err, `volumeMounts[1] "/scratch": the mount path is that of volumeMounts[0] "/scratch"`)

			err = mmBuild(k, mmProps(map[string]any{"volumeMounts": []any{mmMount(nil), mmMount(map[string]any{"subPath": "/abs"})}}))
			if err == nil || !strings.Contains(err.Error(), `volumeMounts[1].subPath: must be a relative path, got "/abs"`) {
				t.Errorf("err = %v, want the subPath refused before the path clash", err)
			}
		})
	}
}
