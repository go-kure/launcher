package components_test

import (
	"encoding/json"
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// These tests cover the container fields go-kure/launcher#790 adds to every
// container of a hand-parsed workload kind: imagePullPolicy,
// terminationMessagePath, terminationMessagePolicy, stdin, stdinOnce, tty and
// resizePolicy, on the main container, on an init container and on a sidecar.

// allContainerFields authors every field with a value that differs from the
// API default, so a field that did not reach the container cannot pass.
// restart is the resize restart policy of the cpu entry.
func allContainerFields(restart string) map[string]any {
	return map[string]any{
		"imagePullPolicy":          "Always",
		"terminationMessagePath":   "/tmp/termination",
		"terminationMessagePolicy": "FallbackToLogsOnError",
		"stdin":                    true,
		"stdinOnce":                true,
		"tty":                      true,
		"resizePolicy": []any{
			map[string]any{"resourceName": "cpu", "restartPolicy": restart},
			map[string]any{"resourceName": "memory", "restartPolicy": "NotRequired"},
		},
	}
}

func checkAllContainerFields(t *testing.T, what string, c corev1.Container, restart corev1.ResourceResizeRestartPolicy) {
	t.Helper()
	if c.ImagePullPolicy != corev1.PullAlways {
		t.Errorf("%s ImagePullPolicy = %q, want Always", what, c.ImagePullPolicy)
	}
	if c.TerminationMessagePath != "/tmp/termination" {
		t.Errorf("%s TerminationMessagePath = %q, want /tmp/termination", what, c.TerminationMessagePath)
	}
	if c.TerminationMessagePolicy != corev1.TerminationMessageFallbackToLogsOnError {
		t.Errorf("%s TerminationMessagePolicy = %q, want FallbackToLogsOnError", what, c.TerminationMessagePolicy)
	}
	if !c.Stdin || !c.StdinOnce || !c.TTY {
		t.Errorf("%s stdin/stdinOnce/tty = %t/%t/%t, want all true", what, c.Stdin, c.StdinOnce, c.TTY)
	}
	want := []corev1.ContainerResizePolicy{
		{ResourceName: corev1.ResourceCPU, RestartPolicy: restart},
		{ResourceName: corev1.ResourceMemory, RestartPolicy: corev1.NotRequired},
	}
	if !reflect.DeepEqual(c.ResizePolicy, want) {
		t.Errorf("%s ResizePolicy = %+v, want %+v", what, c.ResizePolicy, want)
	}
}

// TestWorkloadKinds_ContainerFieldsRender: every field reaches the main
// container, an init container and — where the kind has sidecars — a sidecar,
// on every hand-parsed workload kind. webservice and worker go through their
// lowering rules, so this also covers the keys being forwarded to the
// deployment member.
func TestWorkloadKinds_ContainerFieldsRender(t *testing.T) {
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			initEntry := allContainerFields("NotRequired")
			initEntry["name"] = "init"
			initEntry["image"] = "ghcr.io/org/init:v1"
			props := withProps(k.props, allContainerFields("RestartContainer"))
			props["initContainers"] = []any{initEntry}
			if sidecarKinds[k.name] {
				sidecar := allContainerFields("RestartContainer")
				sidecar["name"] = "proxy"
				sidecar["image"] = "ghcr.io/org/proxy:v1"
				props["sidecars"] = []any{sidecar}
			}
			ps := podTemplateSpec(t, generateKind(t, k.handler, k.name, props))
			checkAllContainerFields(t, "main", ps.Containers[0], corev1.RestartContainer)
			checkAllContainerFields(t, "init", containerNamed(t, ps.InitContainers, "init"), corev1.NotRequired)
			if sidecarKinds[k.name] {
				checkAllContainerFields(t, "sidecar", containerNamed(t, ps.Containers, "proxy"), corev1.RestartContainer)
			}
		})
	}
}

// TestWorkloadKinds_ContainerFieldsUnauthoredStayZero: nothing is defaulted. A
// component authoring none of the fields renders none, on any container, which
// leaves the API server's own defaults in force.
func TestWorkloadKinds_ContainerFieldsUnauthoredStayZero(t *testing.T) {
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			props := withProps(k.props, map[string]any{
				"initContainers": []any{map[string]any{"name": "init", "image": "ghcr.io/org/init:v1"}},
			})
			if sidecarKinds[k.name] {
				props["sidecars"] = []any{map[string]any{"name": "proxy", "image": "ghcr.io/org/proxy:v1"}}
			}
			ps := podTemplateSpec(t, generateKind(t, k.handler, k.name, props))
			for _, c := range append(append([]corev1.Container{}, ps.InitContainers...), ps.Containers...) {
				if c.ImagePullPolicy != "" || c.TerminationMessagePath != "" || c.TerminationMessagePolicy != "" ||
					c.Stdin || c.StdinOnce || c.TTY || c.ResizePolicy != nil {
					t.Errorf("container %q carries an unauthored field: %+v", c.Name, c)
				}
			}
		})
	}
}

// TestContainerFields_NullAndEmptyAreAbsent: an explicit null is absence, per
// the package-wide null contract; so are an empty terminationMessagePath
// (parseStringField) and an empty resizePolicy list. An empty string on either
// enum is a refusal (containerFieldErrorCases), not absence.
func TestContainerFields_NullAndEmptyAreAbsent(t *testing.T) {
	for name, fields := range map[string]map[string]any{
		"null": {
			"imagePullPolicy": nil, "terminationMessagePath": nil, "terminationMessagePolicy": nil,
			"stdin": nil, "stdinOnce": nil, "tty": nil, "resizePolicy": nil,
		},
		"empty": {
			"terminationMessagePath": "",
			"resizePolicy":           []any{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			entry := func(n string) map[string]any {
				m := maps.Clone(fields)
				m["name"] = n
				m["image"] = "ghcr.io/org/" + n + ":v1"
				return m
			}
			props := withProps(fields, map[string]any{
				"image":          "ghcr.io/org/app:v1",
				"initContainers": []any{entry("init")},
				"sidecars":       []any{entry("proxy")},
			})
			ps := podTemplateSpec(t, generateKind(t, &components.DeploymentHandler{}, "deployment", props))
			for _, c := range append(append([]corev1.Container{}, ps.InitContainers...), ps.Containers...) {
				if c.ImagePullPolicy != "" || c.TerminationMessagePath != "" || c.TerminationMessagePolicy != "" ||
					c.Stdin || c.StdinOnce || c.TTY || c.ResizePolicy != nil {
					t.Errorf("container %q carries a field: %+v", c.Name, c)
				}
			}
		})
	}
}

// TestContainerFields_FalseBooleansRenderAsAbsent: an authored false is the
// zero value of a field the API omits when empty, so it renders as absent
// rather than as an explicit false. The serialized container is what is
// read, so an encoding that wrote the key would fail; the authored true shows
// that the three keys are found when they are written.
func TestContainerFields_FalseBooleansRenderAsAbsent(t *testing.T) {
	keys := []string{"stdin", "stdinOnce", "tty"}
	for _, authored := range []bool{false, true} {
		props := map[string]any{"image": "ghcr.io/org/app:v1"}
		for _, k := range keys {
			props[k] = authored
		}
		ps := podTemplateSpec(t, generateKind(t, &components.DeploymentHandler{}, "deployment", props))
		raw, err := json.Marshal(ps.Containers[0])
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		var rendered map[string]any
		if err := json.Unmarshal(raw, &rendered); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		for _, k := range keys {
			got, present := rendered[k]
			switch {
			case authored && got != true:
				t.Errorf("authored true: rendered %s = %v (present %t), want true; container: %s", k, got, present, raw)
			case !authored && present:
				t.Errorf("authored false: rendered container carries %s = %v, want the key absent; container: %s", k, got, raw)
			}
		}
	}
}

// containerFieldErrorCases are the refusals parseContainerFields makes on any
// container. want is matched against the error text.
var containerFieldErrorCases = []struct {
	name   string
	fields map[string]any
	want   []string
}{
	{"pull policy not in the set", map[string]any{"imagePullPolicy": "Sometimes"},
		[]string{"imagePullPolicy", `invalid value "Sometimes"`, "Always, IfNotPresent, Never"}},
	{"pull policy wrong case", map[string]any{"imagePullPolicy": "always"},
		[]string{"imagePullPolicy", `invalid value "always"`}},
	{"pull policy empty", map[string]any{"imagePullPolicy": ""},
		[]string{`imagePullPolicy: invalid value ""`, "Always, IfNotPresent, Never"}},
	{"pull policy wrong type", map[string]any{"imagePullPolicy": true},
		[]string{"imagePullPolicy: must be a string, got bool"}},
	{"termination message path wrong type", map[string]any{"terminationMessagePath": 3},
		[]string{"terminationMessagePath: must be a string, got int"}},
	{"termination message policy not in the set", map[string]any{"terminationMessagePolicy": "Logs"},
		[]string{"terminationMessagePolicy", `invalid value "Logs"`, "File, FallbackToLogsOnError"}},
	{"termination message policy empty", map[string]any{"terminationMessagePolicy": ""},
		[]string{`terminationMessagePolicy: invalid value ""`, "File, FallbackToLogsOnError"}},
	{"stdin wrong type", map[string]any{"stdin": "true"},
		[]string{"stdin: must be a boolean, got string"}},
	{"stdinOnce wrong type", map[string]any{"stdinOnce": 1},
		[]string{"stdinOnce: must be a boolean, got int"}},
	{"tty wrong type", map[string]any{"tty": "yes"},
		[]string{"tty: must be a boolean, got string"}},
	{"resize policy not a list", map[string]any{"resizePolicy": map[string]any{}},
		[]string{"resizePolicy: must be an array"}},
	{"resize policy entry not an object", map[string]any{"resizePolicy": []any{"cpu"}},
		[]string{"resizePolicy[0]: must be an object"}},
	{"resize policy entry null", map[string]any{"resizePolicy": []any{nil}},
		[]string{"resizePolicy[0]: must be an object"}},
	{"resize policy unknown key", map[string]any{"resizePolicy": []any{map[string]any{"resourceName": "cpu", "restartPolicy": "NotRequired", "resource": "cpu"}}},
		[]string{"resizePolicy[0]", `"resource"`}},
	{"resize policy without a resource", map[string]any{"resizePolicy": []any{map[string]any{"restartPolicy": "NotRequired"}}},
		[]string{"resizePolicy[0]: resourceName is required"}},
	{"resize policy without a restart policy", map[string]any{"resizePolicy": []any{map[string]any{"resourceName": "cpu"}}},
		[]string{"resizePolicy[0]: restartPolicy is required"}},
	{"resize policy on an unsupported resource", map[string]any{"resizePolicy": []any{map[string]any{"resourceName": "ephemeral-storage", "restartPolicy": "NotRequired"}}},
		[]string{"resizePolicy[0].resourceName", `invalid value "ephemeral-storage"`, "cpu, memory"}},
	{"resize policy with an unknown restart policy", map[string]any{"resizePolicy": []any{map[string]any{"resourceName": "cpu", "restartPolicy": "Always"}}},
		[]string{"resizePolicy[0].restartPolicy", `invalid value "Always"`, "NotRequired, RestartContainer"}},
	{"resize policy names a resource twice", map[string]any{"resizePolicy": []any{
		map[string]any{"resourceName": "cpu", "restartPolicy": "NotRequired"},
		map[string]any{"resourceName": "cpu", "restartPolicy": "RestartContainer"},
	}}, []string{"resizePolicy[1].resourceName", `"cpu"`, "earlier entry"}},
}

// TestWorkloadKinds_ContainerFieldErrors: each refusal is made for the main
// container of every kind, and names the field.
func TestWorkloadKinds_ContainerFieldErrors(t *testing.T) {
	for _, k := range workloadKinds {
		for _, tc := range containerFieldErrorCases {
			t.Run(k.name+"/"+tc.name, func(t *testing.T) {
				_, err := k.handler.ToApplicationConfig(&oam.Component{
					Name: "app", Type: k.name, Properties: withProps(k.props, tc.fields),
				}, "default")
				if err == nil {
					t.Fatal("expected an error, got none")
				}
				for _, w := range tc.want {
					if !strings.Contains(err.Error(), w) {
						t.Errorf("error %q does not contain %q", err.Error(), w)
					}
				}
			})
		}
	}
}

// TestExtraContainer_ContainerFieldErrors: the same refusals on an init
// container and on a sidecar, each prefixed with the list position and the
// container name.
func TestExtraContainer_ContainerFieldErrors(t *testing.T) {
	for _, list := range []string{"initContainers", "sidecars"} {
		for _, tc := range containerFieldErrorCases {
			t.Run(list+"/"+tc.name, func(t *testing.T) {
				entry := maps.Clone(tc.fields)
				entry["name"] = "c"
				entry["image"] = "ghcr.io/org/c:v1"
				_, err := (&components.DeploymentHandler{}).ToApplicationConfig(&oam.Component{
					Name: "app", Type: "deployment",
					Properties: map[string]any{"image": "ghcr.io/org/app:v1", list: []any{entry}},
				}, "default")
				if err == nil {
					t.Fatal("expected an error, got none")
				}
				for _, w := range append([]string{list + `[0] "c": `}, tc.want...) {
					if !strings.Contains(err.Error(), w) {
						t.Errorf("error %q does not contain %q", err.Error(), w)
					}
				}
			})
		}
	}
}

// TestContainerFields_AuthoredCheckAgreesWithParser: the authored-property
// check `kurel build` runs first and the handler's parser give the same answer
// on the three string fields, for the main container, an init container and a
// sidecar. An empty string is where they could part: the published enums do not
// list it, and parseStringField would read it as absence.
func TestContainerFields_AuthoredCheckAgreesWithParser(t *testing.T) {
	h := &components.DeploymentHandler{}
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{"deployment": h}, nil)
	cases := []struct {
		name   string
		fields map[string]any
		ok     bool
	}{
		{"pull policy in the set", map[string]any{"imagePullPolicy": "Always"}, true},
		{"pull policy empty", map[string]any{"imagePullPolicy": ""}, false},
		{"pull policy wrong case", map[string]any{"imagePullPolicy": "always"}, false},
		{"termination message policy in the set", map[string]any{"terminationMessagePolicy": "File"}, true},
		{"termination message policy empty", map[string]any{"terminationMessagePolicy": ""}, false},
		{"termination message path empty", map[string]any{"terminationMessagePath": ""}, true},
	}
	positions := map[string]func(fields map[string]any) map[string]any{
		"main": func(fields map[string]any) map[string]any {
			return withProps(fields, map[string]any{"image": "ghcr.io/org/app:v1"})
		},
	}
	for _, list := range []string{"initContainers", "sidecars"} {
		positions[list] = func(fields map[string]any) map[string]any {
			entry := withProps(fields, map[string]any{"name": "c", "image": "ghcr.io/org/c:v1"})
			return map[string]any{"image": "ghcr.io/org/app:v1", list: []any{entry}}
		}
	}
	for position, props := range positions {
		for _, tc := range cases {
			t.Run(position+"/"+tc.name, func(t *testing.T) {
				component := oam.Component{Name: "app", Type: "deployment", Properties: props(tc.fields)}
				authoredErr := tr.ValidateAuthoredProperties(&oam.Application{
					Spec: oam.ApplicationSpec{Components: []oam.Component{component}},
				})
				_, parseErr := h.ToApplicationConfig(&component, "default")
				if (authoredErr == nil) != tc.ok {
					t.Errorf("authored check: err = %v, want accepted = %t", authoredErr, tc.ok)
				}
				if (parseErr == nil) != tc.ok {
					t.Errorf("parser: err = %v, want accepted = %t", parseErr, tc.ok)
				}
			})
		}
	}
}

// TestInitContainer_ResizeRestartContainerRefused: upstream refuses
// RestartContainer on an init container that is not restartable
// (validateInitContainers), and this package models no restartable one. The
// same value is accepted on the main container and on a sidecar, and
// NotRequired on the init container.
func TestInitContainer_ResizeRestartContainerRefused(t *testing.T) {
	resize := func(restart string) []any {
		return []any{map[string]any{"resourceName": "memory", "restartPolicy": restart}}
	}
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			props := withProps(k.props, map[string]any{"initContainers": []any{
				map[string]any{"name": "init", "image": "ghcr.io/org/init:v1", "resizePolicy": resize("RestartContainer")},
			}})
			_, err := k.handler.ToApplicationConfig(&oam.Component{Name: "app", Type: k.name, Properties: props}, "default")
			if err == nil {
				t.Fatal("expected an error, got none")
			}
			for _, w := range []string{`initContainers[0] "init"`, "resizePolicy[0].restartPolicy", "RestartContainer", "init container"} {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err.Error(), w)
				}
			}
		})
	}
	props := map[string]any{
		"image":          "ghcr.io/org/app:v1",
		"resizePolicy":   resize("RestartContainer"),
		"initContainers": []any{map[string]any{"name": "init", "image": "ghcr.io/org/init:v1", "resizePolicy": resize("NotRequired")}},
		"sidecars":       []any{map[string]any{"name": "proxy", "image": "ghcr.io/org/proxy:v1", "resizePolicy": resize("RestartContainer")}},
	}
	generateKind(t, &components.DeploymentHandler{}, "deployment", props)
}

// TestJobPods_ResizeRestartContainerNeedsARestartingPod: upstream requires
// NotRequired when the pod's restartPolicy is Never (validateResizePolicy).
// Only job and cronjob can author that pod policy; under their default,
// OnFailure, RestartContainer is accepted.
func TestJobPods_ResizeRestartContainerNeedsARestartingPod(t *testing.T) {
	for _, k := range workloadKinds {
		if k.name != "job" && k.name != "cronjob" {
			continue
		}
		resize := func(restart string) []any {
			return []any{map[string]any{"resourceName": "cpu", "restartPolicy": restart}}
		}
		// The rule is checked on the assembled pod spec, so the parse succeeds
		// and Generate is what reports it.
		generate := func(t *testing.T, props map[string]any) error {
			t.Helper()
			cfg, err := k.handler.ToApplicationConfig(&oam.Component{Name: "app", Type: k.name, Properties: props}, "default")
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			_, err = cfg.Generate(stack.NewApplication("app", "default", cfg))
			return err
		}
		t.Run(k.name+"/Never refuses RestartContainer", func(t *testing.T) {
			err := generate(t, withProps(k.props, map[string]any{"restartPolicy": "Never", "resizePolicy": resize("RestartContainer")}))
			if err == nil {
				t.Fatal("expected an error, got none")
			}
			for _, w := range []string{`containers "app"`, "resizePolicy[0].restartPolicy", "RestartContainer", "restartPolicy is Never", "NotRequired"} {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err.Error(), w)
				}
			}
		})
		t.Run(k.name+"/Never accepts NotRequired", func(t *testing.T) {
			if err := generate(t, withProps(k.props, map[string]any{"restartPolicy": "Never", "resizePolicy": resize("NotRequired")})); err != nil {
				t.Fatalf("NotRequired under restartPolicy Never must build, got: %v", err)
			}
		})
		t.Run(k.name+"/OnFailure accepts RestartContainer", func(t *testing.T) {
			for _, props := range []map[string]any{
				{"resizePolicy": resize("RestartContainer")},
				{"restartPolicy": "OnFailure", "resizePolicy": resize("RestartContainer")},
			} {
				if err := generate(t, withProps(k.props, props)); err != nil {
					t.Fatalf("RestartContainer under restartPolicy OnFailure must build, got: %v", err)
				}
			}
		})
	}
}

// TestContainerFields_RenderingTwiceIsUnaffectedByEditingTheFirstRender: the
// reuse contract in render_reuse_aliasing_test.go holds for the resize policy —
// editing the first render's list must not reach the second. buildPodSpec's
// per-container deep copy is enough to make this pass, so the copy the field
// writer makes itself is pinned by TestContainerFieldsApplyCopiesResizePolicy.
func TestContainerFields_RenderingTwiceIsUnaffectedByEditingTheFirstRender(t *testing.T) {
	entry := func(name string) map[string]any {
		m := allContainerFields("NotRequired")
		m["name"] = name
		m["image"] = "ghcr.io/org/" + name + ":v1"
		return m
	}
	props := withProps(allContainerFields("NotRequired"), map[string]any{
		"image":          "ghcr.io/org/app:v1",
		"initContainers": []any{entry("init")},
		"sidecars":       []any{entry("proxy")},
	})
	second := renderTwice(t, &components.DeploymentHandler{}, "deployment", props, func(objects []*client.Object) {
		ps := podTemplateSpec(t, objects)
		ps.Containers[0].ResizePolicy[0].RestartPolicy = corev1.RestartContainer
		ps.Containers[1].ResizePolicy[0].RestartPolicy = corev1.RestartContainer
		ps.InitContainers[0].ResizePolicy[0].RestartPolicy = corev1.RestartContainer
	})
	ps := podTemplateSpec(t, second)
	checkAllContainerFields(t, "main", ps.Containers[0], corev1.NotRequired)
	checkAllContainerFields(t, "sidecar", containerNamed(t, ps.Containers, "proxy"), corev1.NotRequired)
	checkAllContainerFields(t, "init", containerNamed(t, ps.InitContainers, "init"), corev1.NotRequired)
}

// TestContainerFields_AuthoredCheckAcceptsTheFullSurface: every field, on the
// main container and on both entry kinds, passes the authored-property check
// `kurel build` runs before any handler sees the document.
func TestContainerFields_AuthoredCheckAcceptsTheFullSurface(t *testing.T) {
	entry := func(name string) map[string]any {
		m := allContainerFields("NotRequired")
		m["name"] = name
		m["image"] = "ghcr.io/org/" + name + ":v1"
		return m
	}
	app := authoredApp(withProps(allContainerFields("RestartContainer"), map[string]any{
		"image":          "ghcr.io/org/app:v1",
		"initContainers": []any{entry("init")},
		"sidecars":       []any{entry("proxy")},
	}))
	if err := authoredTransformer().ValidateAuthoredProperties(app); err != nil {
		t.Fatalf("a document using only the declared container fields must be accepted, got: %v", err)
	}
}

// TestContainerFields_AuthoredCheckRejectsUnreadContainerKeys: the container's
// own restartPolicy and restartPolicyRules are read on an init container only
// (parseInitContainerRestart), so the authored-property check refuses them on
// the main container and on a sidecar of a webservice (authoredApp). On job
// and cronjob a top-level restartPolicy is the pod's, a different key.
func TestContainerFields_AuthoredCheckRejectsUnreadContainerKeys(t *testing.T) {
	for _, key := range []string{"restartPolicy", "restartPolicyRules"} {
		for _, list := range []string{"", "sidecars"} {
			t.Run(key+"/"+list, func(t *testing.T) {
				props := map[string]any{"image": "ghcr.io/org/app:v1"}
				if list == "" {
					props[key] = "Always"
				} else {
					props[list] = []any{map[string]any{"name": "c", "image": "ghcr.io/org/c:v1", key: "Always"}}
				}
				err := authoredTransformer().ValidateAuthoredProperties(authoredApp(props))
				if err == nil {
					t.Fatalf("undeclared key %q must be rejected", key)
				}
				if !strings.Contains(err.Error(), `"`+key+`"`) {
					t.Errorf("error = %v, want it to name %q", err, key)
				}
			})
		}
	}
}
