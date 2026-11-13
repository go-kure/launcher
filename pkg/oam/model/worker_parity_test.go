package model_test

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// Parity of the builtin `worker` with the same component written as a configuration model
// (go-kure/launcher#1261, step F). The model below covers the reduced scope of the worker: image,
// replicas, env, resources, command, args and the topology-spread switch. Its gaps are listed in
// the pull request: the affinity shorthand, volumes with role claims, initContainers, sidecars, pod
// metadata, object-name keys, and traits authored on the component (a model rule does not forward
// them).
//
// "Identical" is read in one strict way. A model part is named `<component>-<part>`, and the Go rule
// names the member after the component. So the comparison maps the part suffix back: it replaces a
// string that is exactly `<component>-<part>` with `<component>`, and nothing else. Before it maps, it
// checks that the model output carries `<component>-<part>` exactly where the Go output carries
// `<component>`, and it counts those places. Every other leaf must be equal as it is.
//
// The model writes the ServiceAccount name as `${self.name}-sa`: a kind part such as
// `serviceaccount@v1` declares no output, so `parts.sa.<key>` cannot read it.

const workerModel = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Model
metadata:
  name: worker-cfg.acme.example
  version: v1
spec:
  celEnv: "1"
  inputs:
    image: {type: string, required: true}
    replicas: {type: integer, default: 1}
    topologySpread: {type: boolean, default: true}
    env:
      type: array
      items:
        type: object
        properties:
          name: {type: string, required: true}
          value: {type: string}
    command: {type: array, items: {type: string}}
    args: {type: array, items: {type: string}}
    resources:
      type: object
      properties:
        requests:
          type: object
          properties:
            cpu: {type: string}
            memory: {type: string}
        limits:
          type: object
          properties:
            cpu: {type: string}
            memory: {type: string}
  parts:
    - id: sa
      type: serviceaccount@v1
      properties:
        automountServiceAccountToken: false
    - id: spread
      type: deployment.apps@v1
      when: "${inputs.topologySpread}"
      properties:
        image: "${inputs.image}"
        replicas: "${inputs.replicas}"
        env: "${inputs.env}"
        command: "${inputs.command}"
        args: "${inputs.args}"
        resources: "${inputs.resources}"
        serviceAccountName: "${self.name}-sa"
      traits:
        - type: topology-spread.launcher.gokure.dev@v1
    - id: plain
      type: deployment.apps@v1
      when: "${!inputs.topologySpread}"
      properties:
        image: "${inputs.image}"
        replicas: "${inputs.replicas}"
        env: "${inputs.env}"
        command: "${inputs.command}"
        args: "${inputs.args}"
        resources: "${inputs.resources}"
        serviceAccountName: "${self.name}-sa"
`

const parityComponent = "orders"

var parityParts = []string{"sa", "spread", "plain"}

type objMap = map[[2]string]map[string]any

// parityRender transforms one component of the given type and properties.
func parityRender(t *testing.T, tr *oam.Transformer, typ string, props map[string]any) objMap {
	t.Helper()
	return objectsOK(t, tr, []oam.Component{{Name: parityComponent, Type: typ, Properties: props}}, nil)
}

// parityCompare compares the Go objects with the model objects. It returns the problems it found and
// the number of leaves where the model carried `<component>-<part>` and the Go output carried
// `<component>`.
func parityCompare(goObjs, modelObjs objMap) (problems []string, mapped int) {
	if len(goObjs) != len(modelObjs) {
		problems = append(problems, fmt.Sprintf("Go has %d objects, the model has %d", len(goObjs), len(modelObjs)))
	}
	// The mapping hides the part suffix, so a reference to the wrong part would count like a right
	// one. Read each Deployment's ServiceAccount reference before any mapping: it must name an
	// emitted ServiceAccount of the model.
	for mk, o := range modelObjs {
		if mk[0] != "Deployment" {
			continue
		}
		ref, _ := lookupString(o, "spec", "template", "spec", "serviceAccountName")
		if _, ok := modelObjs[[2]string{"ServiceAccount", ref}]; !ok {
			problems = append(problems, fmt.Sprintf("%s %s: serviceAccountName %q names no ServiceAccount the model emits", mk[0], mk[1], ref))
		}
	}
	used := map[[2]string]bool{}
	var keys [][2]string
	for k := range goObjs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+"/"+keys[i][1] < keys[j][0]+"/"+keys[j][1] })
	for _, gk := range keys {
		var match [][2]string
		for _, part := range parityParts {
			mk := [2]string{gk[0], gk[1] + "-" + part}
			if _, ok := modelObjs[mk]; ok {
				match = append(match, mk)
			}
		}
		if len(match) != 1 {
			problems = append(problems, fmt.Sprintf("%s %s: the model has %d objects named %s-<part>, want 1", gk[0], gk[1], len(match), gk[1]))
			continue
		}
		used[match[0]] = true
		parityWalk(gk[0]+" "+gk[1], match[0][1], goObjs[gk], modelObjs[match[0]], &problems, &mapped)
	}
	for mk := range modelObjs {
		if !used[mk] {
			problems = append(problems, fmt.Sprintf("%s %s: the model object has no Go counterpart", mk[0], mk[1]))
		}
	}
	return problems, mapped
}

// lookupString reads a string at a path of object keys.
func lookupString(o map[string]any, path ...string) (string, bool) {
	var cur any = o
	for _, k := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		cur = m[k]
	}
	s, ok := cur.(string)
	return s, ok
}

func parityWalk(path, own string, g, m any, problems *[]string, mapped *int) {
	switch gv := g.(type) {
	case map[string]any:
		mv, ok := m.(map[string]any)
		if !ok {
			*problems = append(*problems, fmt.Sprintf("%s: Go has an object, the model has %T", path, m))
			return
		}
		for k, v := range gv {
			mm, has := mv[k]
			if !has {
				*problems = append(*problems, fmt.Sprintf("%s.%s: the model has no such key", path, k))
				continue
			}
			parityWalk(path+"."+k, own, v, mm, problems, mapped)
		}
		for k := range mv {
			if _, has := gv[k]; !has {
				*problems = append(*problems, fmt.Sprintf("%s.%s: Go has no such key", path, k))
			}
		}
	case []any:
		mv, ok := m.([]any)
		if !ok || len(mv) != len(gv) {
			*problems = append(*problems, fmt.Sprintf("%s: Go has a list of %d, the model has %#v", path, len(gv), m))
			return
		}
		for i := range gv {
			parityWalk(fmt.Sprintf("%s[%d]", path, i), own, gv[i], mv[i], problems, mapped)
		}
	case string:
		ms, ok := m.(string)
		if !ok {
			*problems = append(*problems, fmt.Sprintf("%s: Go has %q, the model has %#v", path, gv, m))
			return
		}
		if ms == gv {
			return
		}
		if gv == parityComponent {
			for _, part := range parityParts {
				if ms != parityComponent+"-"+part {
					continue
				}
				// A part name inside an object is the object's own, except the ServiceAccount
				// reference, which names another object and is checked against the emitted
				// ServiceAccounts before the walk. This catches a selector, a label or a container
				// name that names the wrong part, which the mapping alone would hide.
				if ms != own && !strings.HasSuffix(path, ".serviceAccountName") {
					*problems = append(*problems, fmt.Sprintf("%s: the model has %q, which is not the part of the object (%q)", path, ms, own))
					return
				}
				*mapped++
				return
			}
		}
		*problems = append(*problems, fmt.Sprintf("%s: Go has %q, the model has %q", path, gv, ms))
	default:
		if !reflect.DeepEqual(g, m) {
			*problems = append(*problems, fmt.Sprintf("%s: Go has %#v, the model has %#v", path, g, m))
		}
	}
}

// parityCase renders the Go worker and the model for one property set and compares them.
func parityCase(t *testing.T, props map[string]any) (problems []string, mapped int) {
	t.Helper()
	goObjs := parityRender(t, expandTransformer(t), "worker", props)
	tr, _ := registered(t, workerModel)
	modelObjs := parityRender(t, tr, "worker-cfg.acme.example@v1", props)
	return parityCompare(goObjs, modelObjs)
}

func requireParity(t *testing.T, props map[string]any, wantMapped int) {
	t.Helper()
	problems, mapped := parityCase(t, props)
	if len(problems) > 0 {
		t.Fatalf("the model differs from the Go worker:\n  %s", strings.Join(problems, "\n  "))
	}
	if mapped != wantMapped {
		t.Errorf("the model carries %s-<part> at %d places where Go carries %s, want %d", parityComponent, mapped, parityComponent, wantMapped)
	}
}

// The default worker: a Deployment with the two topology constraints and a ServiceAccount, both
// named after the component by Go.
func TestWorkerParity_Default(t *testing.T) {
	requireParity(t, map[string]any{"image": "nginx:1.27", "replicas": 3}, 10)
}

// The full property set goes through both parts of the model, the one with the trait and the one
// without, because each part has its own expressions for every property.
func TestWorkerParity_Properties(t *testing.T) {
	for _, tc := range []struct {
		name   string
		spread bool
		mapped int
	}{{"spread", true, 10}, {"plain", false, 8}} {
		t.Run(tc.name, func(t *testing.T) {
			requireParity(t, map[string]any{
				"image":          "nginx:1.27",
				"replicas":       3,
				"topologySpread": tc.spread,
				"env":            []any{map[string]any{"name": "MODE", "value": "fast"}},
				"command":        []any{"/bin/sh"},
				"args":           []any{"-c", "echo hello"},
				"resources": map[string]any{
					"requests": map[string]any{"cpu": "200m", "memory": "256Mi"},
					"limits":   map[string]any{"memory": "512Mi"},
				},
			}, tc.mapped)
		})
	}
}

func TestWorkerParity_TopologySpreadOff(t *testing.T) {
	requireParity(t, map[string]any{"image": "nginx:1.27", "replicas": 3, "topologySpread": false}, 8)
}

// The affinity shorthand (`enablePodAntiAffinity`) is valid on the Go worker and undeclared in the
// model, so the authored-property check refuses it on the model and names the property. This is a
// recorded gap and not a parity: the model refuses where Go builds.
func TestWorkerParity_UnmodelledPropertyRefused(t *testing.T) {
	props := map[string]any{"image": "nginx:1.27", "affinity": map[string]any{"enablePodAntiAffinity": true}}
	check := func(typ string, tr *oam.Transformer) error {
		app := &oam.Application{
			APIVersion: oam.SupportedAPIVersion, Kind: "Application",
			Metadata: oam.Metadata{Name: "shop", Namespace: "demo"},
			Spec:     oam.ApplicationSpec{Components: []oam.Component{{Name: parityComponent, Type: typ, Properties: props}}},
		}
		_, err := tr.ValidateAuthoredPropertiesForTarget(app, nil, nil)
		return err
	}
	if err := check("worker", expandTransformer(t)); err != nil {
		t.Fatalf("test bug: the Go worker refused the affinity shorthand: %v", err)
	}
	tr, _ := registered(t, workerModel)
	err := check("worker-cfg.acme.example@v1", tr)
	if err == nil || !strings.Contains(err.Error(), "affinity") {
		t.Fatalf("the model accepted a property it does not declare, or named the wrong one: %v", err)
	}
	if strings.Contains(err.Error(), "enablePodAntiAffinity") {
		t.Errorf("the model refused a nested key and not the top-level property: %v", err)
	}
}

// Controls: the comparer must pass for two equal outputs and fail for a difference.
func TestWorkerParity_ComparerControls(t *testing.T) {
	goObjs := parityRender(t, expandTransformer(t), "worker", map[string]any{"image": "nginx:1.27", "replicas": 3})
	// Go against itself: nothing to map, so the comparer's own object pairing needs the suffix, and
	// the pairing finds no `<component>-<part>` object. It must report that, not pass.
	if problems, _ := parityCompare(goObjs, goObjs); len(problems) == 0 {
		t.Error("the comparer passed Go against Go, which has no part names")
	}
	// The model against itself with a changed leaf must fail.
	tr, _ := registered(t, workerModel)
	props := map[string]any{"image": "nginx:1.27", "replicas": 3}
	good := parityRender(t, tr, "worker-cfg.acme.example@v1", props)
	if problems, mapped := parityCompare(goObjs, good); len(problems) > 0 || mapped == 0 {
		t.Fatalf("control: the unchanged model must pass with mapped leaves: %v (mapped %d)", problems, mapped)
	}
	bad := parityRender(t, tr, "worker-cfg.acme.example@v1", map[string]any{"image": "nginx:1.28", "replicas": 3})
	if problems, _ := parityCompare(goObjs, bad); len(problems) == 0 {
		t.Error("the comparer passed a model with a different image")
	}
	// A reference to the wrong part must fail, though it maps to the same string as a right one.
	wrongRef := parityRender(t, tr, "worker-cfg.acme.example@v1", props)
	pod := wrongRef[[2]string{"Deployment", parityComponent + "-spread"}]["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	pod["serviceAccountName"] = parityComponent + "-plain"
	if problems, _ := parityCompare(goObjs, wrongRef); len(problems) == 0 {
		t.Error("the comparer passed a Deployment that references a ServiceAccount the model does not emit")
	}
	// A selector that names another part must fail, though it maps to the same string.
	wrongSel := parityRender(t, tr, "worker-cfg.acme.example@v1", props)
	sel := wrongSel[[2]string{"Deployment", parityComponent + "-spread"}]["spec"].(map[string]any)["selector"].(map[string]any)["matchLabels"].(map[string]any)
	sel["app"] = parityComponent + "-sa"
	if problems, _ := parityCompare(goObjs, wrongSel); len(problems) == 0 {
		t.Error("the comparer passed a selector that names another part")
	}
	// A mapped leaf in a place where Go carries something else must fail.
	skewed := parityRender(t, tr, "worker-cfg.acme.example@v1", props)
	dep := skewed[[2]string{"Deployment", parityComponent + "-spread"}]
	dep["metadata"].(map[string]any)["namespace"] = parityComponent + "-spread"
	if problems, _ := parityCompare(goObjs, skewed); len(problems) == 0 {
		t.Error("the comparer passed a part name where Go carries the namespace")
	}
}
