package components_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// rawSchedulingByKind names, per kind-named workload, the raw corev1 scheduling
// properties it publishes (scheduling.go). `deployment` has had all three since
// go-kure/launcher#412 and `daemonset` its `tolerations` from before that;
// go-kure/launcher#790 fills in the rest kind by kind. The role kinds
// (webservice, worker) are not here: they publish the affinity shorthand and
// their own topologySpread opinion instead.
//
// The parsers themselves are covered on `deployment` (scheduling_test.go). The
// tests below cover what differs per kind: that the key is published, parsed,
// rendered onto the pod template and copied, and that a key a kind does not
// publish is refused rather than dropped.
var rawSchedulingByKind = map[string][]string{
	"deployment":  {"affinity", "tolerations", "topologySpreadConstraints"},
	"statefulset": {"tolerations", "topologySpreadConstraints"},
	"daemonset":   {"affinity", "tolerations", "topologySpreadConstraints"},
	"job":         {"affinity", "tolerations", "topologySpreadConstraints"},
	"cronjob":     {"affinity", "tolerations", "topologySpreadConstraints"},
}

var allRawSchedulingKeys = []string{"affinity", "tolerations", "topologySpreadConstraints"}

// rawSchedulingValue returns a fresh authored value for one raw scheduling key.
func rawSchedulingValue(key string) any {
	switch key {
	case "affinity":
		return map[string]any{"nodeAffinity": map[string]any{
			"requiredDuringSchedulingIgnoredDuringExecution": map[string]any{
				"nodeSelectorTerms": []any{map[string]any{
					"matchExpressions": []any{map[string]any{
						"key": "kubernetes.io/arch", "operator": "In", "values": []any{"arm64"},
					}},
				}},
			},
		}}
	case "tolerations":
		return []any{map[string]any{
			"key": "dedicated", "operator": "Equal", "value": "batch",
			"effect": "NoExecute", "tolerationSeconds": 30,
		}}
	case "topologySpreadConstraints":
		return []any{map[string]any{
			"maxSkew": 1, "topologyKey": "topology.kubernetes.io/zone", "whenUnsatisfiable": "DoNotSchedule",
			"labelSelector": map[string]any{"matchLabels": map[string]any{"app": "app"}},
		}}
	}
	panic("no value for " + key)
}

// checkRawSchedulingValue asserts ps carries exactly what rawSchedulingValue
// authors for key.
func checkRawSchedulingValue(t *testing.T, key string, ps corev1.PodSpec) {
	t.Helper()
	switch key {
	case "affinity":
		want := &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key: "kubernetes.io/arch", Operator: corev1.NodeSelectorOpIn, Values: []string{"arm64"},
					}},
				}},
			},
		}}
		if !reflect.DeepEqual(ps.Affinity, want) {
			t.Errorf("Affinity = %+v, want %+v", ps.Affinity, want)
		}
	case "tolerations":
		seconds := int64(30)
		want := []corev1.Toleration{{
			Key: "dedicated", Operator: corev1.TolerationOpEqual, Value: "batch",
			Effect: corev1.TaintEffectNoExecute, TolerationSeconds: &seconds,
		}}
		if !reflect.DeepEqual(ps.Tolerations, want) {
			t.Errorf("Tolerations = %+v, want %+v", ps.Tolerations, want)
		}
	case "topologySpreadConstraints":
		want := []corev1.TopologySpreadConstraint{{
			MaxSkew: 1, TopologyKey: "topology.kubernetes.io/zone", WhenUnsatisfiable: corev1.DoNotSchedule,
			LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "app"}},
		}}
		if !reflect.DeepEqual(ps.TopologySpreadConstraints, want) {
			t.Errorf("TopologySpreadConstraints = %+v, want %+v", ps.TopologySpreadConstraints, want)
		}
	}
}

// rawSchedulingKind is one workloadKinds entry with the raw scheduling keys it
// publishes.
type rawSchedulingKind struct {
	name    string
	handler oam.ComponentHandler
	props   map[string]any
	keys    []string
}

// rawSchedulingKinds returns the workloadKinds entries rawSchedulingByKind
// covers, and fails if the two tables disagree on which kinds exist.
func rawSchedulingKinds(t *testing.T) []rawSchedulingKind {
	t.Helper()
	var out []rawSchedulingKind
	for _, k := range workloadKinds {
		keys, ok := rawSchedulingByKind[k.name]
		if !ok {
			continue
		}
		out = append(out, rawSchedulingKind{k.name, k.handler, k.props, keys})
	}
	if len(out) != len(rawSchedulingByKind) {
		t.Fatalf("rawSchedulingByKind names %d kinds, workloadKinds has %d of them", len(rawSchedulingByKind), len(out))
	}
	return out
}

// TestRawSchedulingKinds_RoundTrip: every key a kind publishes reaches its pod
// template as authored.
func TestRawSchedulingKinds_RoundTrip(t *testing.T) {
	for _, k := range rawSchedulingKinds(t) {
		if len(k.keys) == 0 {
			continue
		}
		t.Run(k.name, func(t *testing.T) {
			extra := map[string]any{}
			for _, key := range k.keys {
				extra[key] = rawSchedulingValue(key)
			}
			ps := podTemplateSpec(t, generateKind(t, k.handler, k.name, withProps(k.props, extra)))
			for _, key := range k.keys {
				checkRawSchedulingValue(t, key, ps)
			}
		})
	}
}

// TestRawSchedulingKinds_UnauthoredAndNullEmitNothing: nothing is inferred. An
// absent key and an explicit null both leave the pod-template field unset.
func TestRawSchedulingKinds_UnauthoredAndNullEmitNothing(t *testing.T) {
	for _, k := range rawSchedulingKinds(t) {
		nulls := map[string]any{}
		for _, key := range k.keys {
			nulls[key] = nil
		}
		for name, props := range map[string]map[string]any{"unauthored": k.props, "null": withProps(k.props, nulls)} {
			t.Run(k.name+"/"+name, func(t *testing.T) {
				ps := podTemplateSpec(t, generateKind(t, k.handler, k.name, props))
				if ps.Tolerations != nil {
					t.Errorf("Tolerations = %+v, want nil", ps.Tolerations)
				}
				if ps.TopologySpreadConstraints != nil {
					t.Errorf("TopologySpreadConstraints = %+v, want nil", ps.TopologySpreadConstraints)
				}
				// A kind with the affinity shorthand builds its own affinity; only a
				// kind publishing the raw shape is held to nil here.
				if slices.Contains(k.keys, "affinity") && ps.Affinity != nil {
					t.Errorf("Affinity = %+v, want nil", ps.Affinity)
				}
			})
		}
	}
}

// TestRawSchedulingKinds_RejectionsReachTheAuthor: a value the shared parser
// refuses is refused on every kind publishing the key, naming it — the parser
// is wired, not merely its schema.
func TestRawSchedulingKinds_RejectionsReachTheAuthor(t *testing.T) {
	bad := map[string]struct {
		value any
		want  []string
	}{
		"affinity": {map[string]any{}, []string{"affinity"}},
		// parseTolerations labels an entry `toleration[i]`, in the singular.
		"tolerations": {[]any{map[string]any{"key": "dedicated", "operator": "Sometimes"}}, []string{"toleration[0].operator", "Sometimes"}},
		"topologySpreadConstraints": {
			[]any{map[string]any{"topologyKey": "topology.kubernetes.io/zone", "whenUnsatisfiable": "DoNotSchedule"}},
			[]string{"topologySpreadConstraints[0]", "maxSkew"},
		},
	}
	for _, k := range rawSchedulingKinds(t) {
		for _, key := range k.keys {
			t.Run(k.name+"/"+key, func(t *testing.T) {
				_, err := k.handler.ToApplicationConfig(&oam.Component{
					Name: "app", Type: k.name, Properties: withProps(k.props, map[string]any{key: bad[key].value}),
				}, "default")
				if err == nil {
					t.Fatal("expected an error, got none")
				}
				for _, w := range bad[key].want {
					if !strings.Contains(err.Error(), w) {
						t.Errorf("error %q does not contain %q", err.Error(), w)
					}
				}
			})
		}
	}
}

// TestRawSchedulingKinds_AuthoredCheck: the authored-property check `kurel
// build` runs first accepts each key a kind publishes, and refuses the raw
// shape of a key the kind does not publish instead of letting it be dropped.
func TestRawSchedulingKinds_AuthoredCheck(t *testing.T) {
	handlers := map[string]oam.ComponentHandler{}
	kinds := rawSchedulingKinds(t)
	for _, k := range kinds {
		handlers[k.name] = k.handler
	}
	transformer := oam.NewTransformer(handlers, nil)
	for _, k := range kinds {
		for _, key := range allRawSchedulingKeys {
			published := slices.Contains(k.keys, key)
			t.Run(k.name+"/"+key, func(t *testing.T) {
				app := &oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{{
					Name: "app", Type: k.name, Properties: withProps(k.props, map[string]any{key: rawSchedulingValue(key)}),
				}}}}
				err := transformer.ValidateAuthoredProperties(app)
				switch {
				case published && err != nil:
					t.Errorf("%s publishes %s, but the authored check refuses it: %v", k.name, key, err)
				case !published && err == nil:
					t.Errorf("%s does not publish raw %s, but the authored check accepts it", k.name, key)
				}
			})
		}
	}
}

// TestRawSchedulingKinds_RenderingTwiceIsUnaffectedByEditingTheFirstRender: the
// reuse contract of render_reuse_aliasing_test.go, on every kind and key —
// editing what the first render emitted must not reach the second.
func TestRawSchedulingKinds_RenderingTwiceIsUnaffectedByEditingTheFirstRender(t *testing.T) {
	for _, k := range rawSchedulingKinds(t) {
		if len(k.keys) == 0 {
			continue
		}
		t.Run(k.name, func(t *testing.T) {
			extra := map[string]any{}
			for _, key := range k.keys {
				extra[key] = rawSchedulingValue(key)
			}
			second := renderTwice(t, k.handler, k.name, withProps(k.props, extra), func(objects []*client.Object) {
				ps := podTemplateSpec(t, objects)
				for _, key := range k.keys {
					switch key {
					case "affinity":
						ps.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.
							NodeSelectorTerms[0].MatchExpressions[0].Values[0] = "edited"
					case "tolerations":
						*ps.Tolerations[0].TolerationSeconds = 999
						ps.Tolerations[0].Key = "edited"
					case "topologySpreadConstraints":
						ps.TopologySpreadConstraints[0].LabelSelector.MatchLabels["app"] = "edited"
						ps.TopologySpreadConstraints[0].MaxSkew = 9
					}
				}
			})
			ps := podTemplateSpec(t, second)
			for _, key := range k.keys {
				checkRawSchedulingValue(t, key, ps)
			}
		})
	}
}
