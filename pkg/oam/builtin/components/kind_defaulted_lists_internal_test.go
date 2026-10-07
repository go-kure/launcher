package components

import (
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// omitemptyList says the field is a list, not a []byte (which encodes as a
// string), that the type omits when empty. Only omitempty does: omitzero
// omits a nil slice, and a decoded [] is an empty non-nil one, which it keeps.
func (f kindField) omitemptyList() bool {
	if f.field.Type.Kind() != reflect.Slice || f.field.Type.Elem().Kind() == reflect.Uint8 {
		return false
	}
	return slices.Contains(f.jsonOptions(), "omitempty")
}

// listDefaultLiteral is def, a list default as a marker ({a,b}) or a CRD
// ([a,b]) gives it, as the JSON literal a defaulted-zero list holds. It is ""
// when the default is the empty list, which an omitted [] keeps.
func listDefaultLiteral(def string) string {
	s := strings.TrimSpace(def)
	if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
		s = "[" + s[1:len(s)-1] + "]"
	}
	if strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")) == "" {
		return ""
	}
	return s
}

// listDocDefault finds a field comment that speaks of a default. It is meant to
// over-match: every list it finds is answered in defaultedListAnswers, so a
// default the comment states is never passed over unread.
var listDocDefault = regexp.MustCompile(`(?i)default`)

// listDefault answers one list field found defaulted: refused, or not refused
// with the reason. A row is keyed by the field's declaring Go type and json
// name, so a field shared by several kinds is answered once, in a form a list
// parsed from another source can be compared with.
type listDefault struct {
	refused bool
	// reason says why an authored [] keeps its meaning. Empty when refused.
	reason string
}

// defaultedListAnswers answers every list field TestKindComponents_DefaultedEmptyLists
// finds: omitted when empty, and either given a default by the kind's API
// source (a CRD schema or a default marker) or described with the word
// "default" by its field comment (SwaggerDoc). The second covers the
// Kubernetes types, whose defaults are applied in the API server's own code,
// which is not in the module graph: a default a comment does not mention is
// not found.
var defaultedListAnswers = map[string]listDefault{
	"v2.HorizontalPodAutoscalerSpec.metrics":          {refused: true},
	"v2.HPAScalingRules.policies":                     {refused: true},
	"v1.NetworkPolicySpec.policyTypes":                {refused: true},
	"v1.ImageRepositorySpec.exclusionList":            {reason: "image-reflector-controller v1.2.5 filters tags by ImageRepository.GetExclusionList (internal/controller/imagerepository_controller.go:434), which returns the CRD's default list when the list is empty (api v1/imagerepository_types.go:177-183), so an omitted [] keeps its meaning"},
	"v1.CSIDriverSpec.volumeLifecycleModes":           {reason: "the comment says an empty list means Persistent, the default the API server sets, so an omitted [] keeps its meaning"},
	"v2.CiliumEgressGatewayPolicySpec.egressGateways": {reason: "the CRD's default is the empty list, which an omitted [] keeps"},
	"v1.PodAffinityTerm.matchLabelKeys":               {reason: "the comment says the default is empty, which an omitted [] keeps"},
	"v1.PodAffinityTerm.mismatchLabelKeys":            {reason: "the comment says the default is empty, which an omitted [] keeps"},
	"v1.TopologySpreadConstraint.matchLabelKeys":      {reason: "the comment's default is the feature gate's; it says a null or empty list means the same"},
	"v1.PodSpec.evictionResponders":                   {reason: "the comment's default is a responder of the Eviction API, not a default of the list"},
	"v1.Container.ports":                              {reason: "the comment's default is the address a container listens on, not a default of the list"},
	"v1.ISCSIVolumeSource.portals":                    {reason: "the comment's default is the port of a portal, not a default of the list"},
	"v1.ISCSIPersistentVolumeSource.portals":          {reason: "the comment's default is the port of a portal, not a default of the list"},
	"v1.IngressSpec.rules":                            {reason: "the comment's default is the default backend, which takes the traffic no rule matches; the API server sets no rules"},
	"v1.IngressTLS.hosts":                             {reason: "the comment's default is the ingress controller's wildcard host; the API server sets no hosts"},
	"v1.NetworkPolicySpec.ingress":                    {reason: "the comment's default is the isolation of the selected pods; the API server reads an empty list and none alike and sets none"},
	"v1.NetworkPolicySpec.egress":                     {reason: "the comment's default is the isolation of the selected pods; the API server reads an empty list and none alike and sets none"},
}

// listDefaultedTables holds the defaulted-zero lists of the kinds that refuse
// an authored empty list, by component.
var listDefaultedTables = map[string]defaultedZeroFields{
	"horizontalpodautoscaler": hpaDefaultedZeros,
	"networkpolicy":           networkPolicyDefaultedZeros,
}

// TestKindComponents_DefaultedEmptyLists finds, for every kind in apiSetKinds,
// the lists its type omits when empty that the API may default (see
// defaultedListAnswers), and holds each to its answer: a refused one is in the
// defaulted-zero list of every kind that reaches it, with its source default
// where the source gives one; a list in a kind's defaulted-zero list is a
// refused one; and a list found without an answer, or an answer nothing finds,
// fails. A dependency bump that defaults a new list fails here, naming it.
func TestKindComponents_DefaultedEmptyLists(t *testing.T) {
	type found struct {
		def   string // the source's default; empty when found by the comment
		paths []string
	}
	finds := map[string]*found{}
	lists := map[string]bool{}
	for _, kind := range apiSetKinds {
		src := kind.source(t)
		walkKindFields(kind.typ, src.required, func(f kindField) {
			top, _, _ := strings.Cut(f.path, ".")
			if slices.Contains(kind.skip, top) || !f.omitemptyList() {
				return
			}
			lists[kind.component+": "+f.path] = true
			name, _, _ := strings.Cut(f.field.Tag.Get("json"), ",")
			key := f.owner.String() + "." + name
			def, sourced := src.def(f)
			if !sourced && !listDocDefault.MatchString(fieldSwaggerDoc(f.owner, name)) {
				return
			}
			if finds[key] == nil {
				finds[key] = &found{}
			}
			if sourced {
				finds[key].def = def
			}
			finds[key].paths = append(finds[key].paths, kind.component+": "+f.path)
		})
	}
	// Vacuity guard: both ways of finding a default reach a field.
	for _, key := range []string{"v1.ImageRepositorySpec.exclusionList", "v2.HorizontalPodAutoscalerSpec.metrics"} {
		if finds[key] == nil {
			t.Fatalf("the walk did not find %s; it found %v", key, slices.Sorted(maps.Keys(finds)))
		}
	}
	refusedPaths := map[string]bool{}
	for _, key := range slices.Sorted(maps.Keys(finds)) {
		f := finds[key]
		answer, ok := defaultedListAnswers[key]
		switch {
		case !ok:
			t.Errorf("%s (%s) is omitted when empty and may be defaulted (source default %q): answer it in defaultedListAnswers", key, strings.Join(f.paths, ", "), f.def)
			continue
		case !answer.refused && answer.reason == "":
			t.Errorf("%s is not refused and gives no reason", key)
		case answer.refused && answer.reason != "":
			t.Errorf("%s is refused and gives a reason not to refuse it", key)
		}
		if !answer.refused {
			continue
		}
		for _, at := range f.paths {
			refusedPaths[at] = true
			component, path, _ := strings.Cut(at, ": ")
			table, ok := listDefaultedTables[component]
			def, listed := table.fields[path]
			switch {
			case !ok || !listed:
				t.Errorf("%s: %s is refused, and the kind's defaulted-zero list does not hold %s", key, component, path)
			case f.def != "" && def != listDefaultLiteral(f.def):
				t.Errorf("%s: the defaulted-zero list of %s gives the default %s; the source says %s", key, component, def, listDefaultLiteral(f.def))
			}
		}
	}
	for _, key := range slices.Sorted(maps.Keys(defaultedListAnswers)) {
		if finds[key] == nil {
			t.Errorf("defaultedListAnswers answers %s, which the walk does not find", key)
		}
	}
	for _, component := range slices.Sorted(maps.Keys(listDefaultedTables)) {
		for _, path := range slices.Sorted(maps.Keys(listDefaultedTables[component].fields)) {
			at := component + ": " + path
			if lists[at] && !refusedPaths[at] {
				t.Errorf("the defaulted-zero list of %s holds the list %s, which defaultedListAnswers does not refuse", component, path)
			}
		}
	}
}

// fieldSwaggerDoc returns the field comment of the json field name on owner, or
// "" when owner publishes none.
func fieldSwaggerDoc(owner reflect.Type, name string) string {
	m := reflect.New(owner).Elem().MethodByName("SwaggerDoc")
	if !m.IsValid() {
		return ""
	}
	doc, _ := m.Call(nil)[0].Interface().(map[string]string)
	return doc[name]
}

// TestKindComponents_RefuseDefaultedEmptyList: an authored [] on a refused list
// is refused on the kind's handler, by the path authored, and a non-empty list
// or none builds.
func TestKindComponents_RefuseDefaultedEmptyList(t *testing.T) {
	hpa := func(extra map[string]any) map[string]any {
		props := map[string]any{"scaleTargetRef": map[string]any{"kind": "Deployment", "name": "app", "apiVersion": "apps/v1"}, "maxReplicas": 3}
		maps.Copy(props, extra)
		return props
	}
	policy := func(extra map[string]any) map[string]any {
		props := map[string]any{"podSelector": map[string]any{}}
		maps.Copy(props, extra)
		return props
	}
	buildHPA := func(props map[string]any) error {
		_, err := (&HorizontalPodAutoscalerHandler{}).ToApplicationConfig(&oam.Component{Name: "hpa", Properties: props}, "ns")
		return err
	}
	buildNetworkPolicy := func(props map[string]any) error {
		_, err := (&NetworkPolicyHandler{}).ToApplicationConfig(&oam.Component{Name: "policy", Properties: props}, "ns")
		return err
	}
	for name, c := range map[string]struct {
		build func(map[string]any) error
		props map[string]any
		want  string
	}{
		"hpa metrics": {buildHPA, hpa(map[string]any{"metrics": []any{}}),
			`metrics: [] cannot be carried by the Kubernetes API types (the field is omitted when empty, so the API server would apply its default [{"type":"Resource","resource":{"name":"cpu","target":{"type":"Utilization","averageUtilization":80}}}])`},
		"hpa scale-down policies": {buildHPA, hpa(map[string]any{"behavior": map[string]any{"scaleDown": map[string]any{"policies": []any{}}}}),
			`behavior.scaleDown.policies: [] cannot be carried by the Kubernetes API types (the field is omitted when empty, so the API server would apply its default [{"type":"Percent","value":100,"periodSeconds":15}])`},
		"hpa metrics set": {buildHPA, hpa(map[string]any{"metrics": []any{map[string]any{"type": "Resource", "resource": map[string]any{"name": "memory", "target": map[string]any{"type": "Utilization", "averageUtilization": 70}}}}}), ""},
		"hpa no metrics":  {buildHPA, hpa(nil), ""},
		"networkpolicy policyTypes": {buildNetworkPolicy, policy(map[string]any{"policyTypes": []any{}}),
			`policyTypes: [] cannot be carried by the Kubernetes API types (the field is omitted when empty, so the API server would apply its default ["Ingress"], with "Egress" when an egress rule is authored)`},
		"networkpolicy policyTypes set": {buildNetworkPolicy, policy(map[string]any{"policyTypes": []any{"Egress"}}), ""},
		"networkpolicy no policyTypes":  {buildNetworkPolicy, policy(nil), ""},
	} {
		t.Run(name, func(t *testing.T) {
			err := c.build(c.props)
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("unexpected error %v", err)
			case c.want != "" && (err == nil || err.Error() != c.want):
				t.Fatalf("got %v, want %s", err, c.want)
			}
		})
	}
}
