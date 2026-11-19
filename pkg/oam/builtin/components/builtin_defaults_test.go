package components_test

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The handler-built workload kinds with a reshaped parser report the values
// their own parser fills when the author wrote none (go-kure/launcher#1580):
// the role member `role-deployment` (the kind-named `deployment`, `daemonset`,
// `statefulset`, `job` and `cronjob` are raw since go-kure/launcher#1629 and
// fill nothing). These tests read the report through oam.BuiltinDefaultReporter, as
// the transform does.

type configBuilder interface {
	ToApplicationConfig(*oam.Component, string) (stack.ApplicationConfig, error)
}

var builtinKinds = map[string]struct {
	handler configBuilder
	base    map[string]any
}{
	roleMemberType: {&components.RoleDeploymentHandler{}, map[string]any{"image": "ghcr.io/org/app:v1"}},
}

func newKindConfig(t *testing.T, kind string, props map[string]any, traits ...oam.Trait) stack.ApplicationConfig {
	t.Helper()
	k := builtinKinds[kind]
	all := maps.Clone(k.base)
	maps.Copy(all, props)
	cfg, err := k.handler.ToApplicationConfig(&oam.Component{Name: "app", Type: kind, Properties: all, Traits: traits}, "default")
	if err != nil {
		t.Fatalf("%s ToApplicationConfig: %v", kind, err)
	}
	return cfg
}

// reportedBuiltin returns the parse-time built-in fills of cfg as path -> ref,
// sorted by path when listed. The fills made at generation (resources and the
// token automount, which builtin_defaults_generate_test.go covers) are left out.
// A config that does not implement the reporter returns nothing.
func reportedBuiltin(cfg stack.ApplicationConfig) map[string]string {
	out := map[string]string{}
	if r, ok := cfg.(oam.BuiltinDefaultReporter); ok {
		for _, d := range r.BuiltinDefaults() {
			if d.Path == "automountServiceAccountToken" || strings.Contains(d.Path, "resources.") {
				continue
			}
			out[d.Path] = d.Ref
		}
	}
	return out
}

func sortedPaths(m map[string]string) []string {
	return slices.Sorted(maps.Keys(m))
}

func generatedJSON(t *testing.T, cfg stack.ApplicationConfig) string {
	t.Helper()
	objects, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var all []any
	for _, o := range objects {
		all = append(all, *o)
	}
	b, err := json.Marshal(all)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

var (
	oneVolume = []any{map[string]any{"name": "data", "type": "pvc", "claimName": "data-claim", "mountPath": "/data"}}
	ctrPorts  = []any{map[string]any{"containerPort": 8080}, map[string]any{"containerPort": 9090, "protocol": "UDP"}}
	init1     = func(ports []any) []any {
		// Only a restartable init container (a native sidecar) reads ports.
		return []any{map[string]any{"name": "setup", "image": "ghcr.io/org/init:v1", "restartPolicy": "Always", "ports": ports}}
	}
	side1 = func(ports []any) []any {
		return []any{map[string]any{"name": "proxy", "image": "ghcr.io/org/proxy:v1", "ports": ports}}
	}
)

// builtinRow is one fill: the kinds that carry it, the properties that leave it to
// the library, and the properties that author the value the library would have used.
type builtinRow struct {
	name  string
	kinds []string
	props map[string]any
	want  []string
	// authored overlays props with the filled value written out. The objects must be
	// identical to the unwritten run, and the paths in gone must no longer be reported.
	authored map[string]any
	gone     []string
}

func builtinRows() []builtinRow {
	allPods := []string{roleMemberType}
	return []builtinRow{
		{name: "replicas", kinds: []string{roleMemberType}, want: []string{"replicas"},
			authored: map[string]any{"replicas": 1}, gone: []string{"replicas"}},
		{name: "replicas null reads as omitted", kinds: []string{roleMemberType},
			props: map[string]any{"replicas": nil}, want: []string{"replicas"}},
		{name: "main container port protocol", kinds: allPods, props: map[string]any{"ports": ctrPorts},
			authored: map[string]any{"ports": []any{map[string]any{"containerPort": 8080, "protocol": "TCP"}, map[string]any{"containerPort": 9090, "protocol": "UDP"}}},
			gone:     []string{"ports[0].protocol"}},
		{name: "init container port protocol", kinds: allPods, props: map[string]any{"initContainers": init1(ctrPorts)},
			authored: map[string]any{"initContainers": init1([]any{map[string]any{"containerPort": 8080, "protocol": "TCP"}, map[string]any{"containerPort": 9090, "protocol": "UDP"}})},
			gone:     []string{"initContainers[0].ports[0].protocol"}},
		{name: "sidecar port protocol", kinds: []string{roleMemberType}, props: map[string]any{"sidecars": side1(ctrPorts)},
			authored: map[string]any{"sidecars": side1([]any{map[string]any{"containerPort": 8080, "protocol": "TCP"}, map[string]any{"containerPort": 9090, "protocol": "UDP"}})},
			gone:     []string{"sidecars[0].ports[0].protocol"}},
		{name: "pvc volume accessModes", kinds: allPods, props: map[string]any{"volumes": oneVolume},
			authored: map[string]any{"volumes": []any{map[string]any{"name": "data", "type": "pvc", "claimName": "data-claim", "mountPath": "/data", "accessModes": []any{"ReadWriteOnce"}}}},
			gone:     []string{"volumes[0].accessModes"}},
	}
}

// expectedPaths composes the paths a row expects for kind: the row's own list, or,
// for the port rows, the entries the fixture leaves to the library. Wanted paths
// that depend on the kind's other fills are added here.
func expectedPaths(kind string, r builtinRow) []string {
	want := slices.Clone(r.want)
	switch r.name {
	case "main container port protocol":
		want = append(want, "ports[0].protocol")
	case "init container port protocol":
		want = append(want, "initContainers[0].ports[0].protocol")
	case "sidecar port protocol":
		want = append(want, "sidecars[0].ports[0].protocol")
	case "pvc volume accessModes":
		want = append(want, "volumes[0].accessModes")
		if kind == roleMemberType {
			want = append(want, "strategy.type")
		}
	}
	// What a bare kind always carries.
	switch kind {
	case roleMemberType:
		want = append(want, "replicas")
	}
	for k, v := range r.props {
		if v == nil {
			continue
		}
		want = slices.DeleteFunc(want, func(p string) bool { return p == k })
	}
	slices.Sort(want)
	return slices.Compact(want)
}

// TestBuiltinDefaults_ParseTimeFills: with no policy, each fill the parser made is
// reported at the path the author would have written, and a value the author wrote
// (null counted as omitted) is not.
func TestBuiltinDefaults_ParseTimeFills(t *testing.T) {
	for _, row := range builtinRows() {
		for _, kind := range row.kinds {
			t.Run(kind+"/"+row.name, func(t *testing.T) {
				got := sortedPaths(reportedBuiltin(newKindConfig(t, kind, row.props)))
				want := expectedPaths(kind, row)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("reported paths = %v, want %v", got, want)
				}
			})
		}
	}
}

// TestBuiltinDefaults_ReportedFillsAreReal (check B of the plan): authoring the
// value the library would have used gives the same objects and no report at that
// path.
func TestBuiltinDefaults_ReportedFillsAreReal(t *testing.T) {
	for _, row := range builtinRows() {
		if row.authored == nil {
			continue
		}
		for _, kind := range row.kinds {
			t.Run(kind+"/"+row.name, func(t *testing.T) {
				before := newKindConfig(t, kind, row.props)
				props := maps.Clone(row.props)
				if props == nil {
					props = map[string]any{}
				}
				maps.Copy(props, row.authored)
				after := newKindConfig(t, kind, props)
				if a, b := generatedJSON(t, before), generatedJSON(t, after); a != b {
					t.Errorf("authoring the filled value changed the objects:\n unwritten: %s\n written:   %s", a, b)
				}
				got := reportedBuiltin(after)
				for _, p := range row.gone {
					if _, still := got[p]; still {
						t.Errorf("%s still reported after the author wrote it", p)
					}
				}
			})
		}
	}
}

// TestBuiltinDefaults_RefNamesTheKindAndTheFill: the ref is the kind and the path,
// item indexes kept.
func TestBuiltinDefaults_RefNamesTheKindAndTheFill(t *testing.T) {
	cfg := newKindConfig(t, roleMemberType, map[string]any{"ports": ctrPorts})
	got := reportedBuiltin(cfg)
	if got["replicas"] != "deployment.replicas" || got["ports[0].protocol"] != "deployment.ports[0].protocol" {
		t.Errorf("refs = %v", got)
	}
}

// TestBuiltinDefaults_PolicyWinsTheReplicasPath: where the policy default fills
// `replicas`, the value that stands is the policy's, so the path is reported once,
// as policy and never as builtin. A default equal to the built-in value is still
// the policy's.
func TestBuiltinDefaults_PolicyWinsTheReplicasPath(t *testing.T) {
	for _, kind := range []string{roleMemberType} {
		for _, dflt := range []int32{1, 3} {
			t.Run(kind, func(t *testing.T) {
				cfg := newKindConfig(t, kind, nil)
				if _, ok := reportedBuiltin(cfg)["replicas"]; !ok {
					t.Fatal("before the policy, replicas is not reported as built in")
				}
				if err := cfg.(oam.Enforceable).ApplyPolicy(&stubPolicy{defaultReplicas: &dflt}); err != nil {
					t.Fatal(err)
				}
				if _, ok := reportedBuiltin(cfg)["replicas"]; ok {
					t.Errorf("replicas reported as built in where the policy default %d stands", dflt)
				}
				policy := cfg.(oam.PolicyDefaultReporter).PolicyDefaults()
				if !slices.ContainsFunc(policy, func(d oam.PolicyDefault) bool { return d.Path == "replicas" }) {
					t.Errorf("the policy default is not reported at replicas: %v", policy)
				}
			})
		}
	}
}

// TestBuiltinDefaults_ScalerOwnsTheReplicaCount: under a scaler the kind is built
// without spec.replicas, so no fill happens and none is reported.
func TestBuiltinDefaults_ScalerOwnsTheReplicaCount(t *testing.T) {
	scaler := oam.Trait{Type: "scaler", Properties: map[string]any{"minReplicas": 1, "maxReplicas": 2}}
	for _, kind := range []string{roleMemberType} {
		t.Run(kind, func(t *testing.T) {
			cfg := newKindConfig(t, kind, nil, scaler)
			if _, ok := reportedBuiltin(cfg)["replicas"]; ok {
				t.Error("replicas reported as filled under a scaler")
			}
			_ = cfg.(oam.Enforceable).ApplyPolicy(&stubPolicy{defaultReplicas: int32ptr(3)})
			if _, ok := reportedBuiltin(cfg)["replicas"]; ok {
				t.Error("replicas reported as filled under a scaler, after the policy")
			}
		})
	}
}

// TestBuiltinDefaults_PureAndIdempotent: reading the report changes nothing, and
// the policy running twice leaves the same report.
func TestBuiltinDefaults_PureAndIdempotent(t *testing.T) {
	props := map[string]any{"ports": ctrPorts, "volumes": oneVolume}
	for kind := range builtinKinds {
		t.Run(kind, func(t *testing.T) {
			cfg := newKindConfig(t, kind, props)
			first := reportedBuiltin(cfg)
			if again := reportedBuiltin(cfg); !reflect.DeepEqual(first, again) {
				t.Errorf("two reads differ: %v then %v", first, again)
			}
			policy := &stubPolicy{defaultReplicas: int32ptr(2)}
			for range 2 {
				if err := cfg.(oam.Enforceable).ApplyPolicy(policy); err != nil {
					t.Fatal(err)
				}
			}
			a, b := reportedBuiltin(cfg), reportedBuiltin(cfg)
			if !reflect.DeepEqual(a, b) {
				t.Errorf("reads after the policy differ: %v then %v", a, b)
			}
			if err := cfg.(oam.Enforceable).ApplyPolicy(&oam.NoopPolicy{}); err != nil {
				t.Fatal(err)
			}
			if c := reportedBuiltin(cfg); !reflect.DeepEqual(a, c) {
				t.Errorf("a further no-op policy changed the report: %v then %v", a, c)
			}
		})
	}
}

// TestBuiltinDefaults_OnePathOneSource: no path is in both reports.
func TestBuiltinDefaults_OnePathOneSource(t *testing.T) {
	for kind := range builtinKinds {
		t.Run(kind, func(t *testing.T) {
			cfg := newKindConfig(t, kind, map[string]any{"ports": ctrPorts})
			if err := cfg.(oam.Enforceable).ApplyPolicy(&stubPolicy{defaultReplicas: int32ptr(2), defaultCPURequest: "50m"}); err != nil {
				t.Fatal(err)
			}
			builtin := reportedBuiltin(cfg)
			for _, d := range cfg.(oam.PolicyDefaultReporter).PolicyDefaults() {
				if _, both := builtin[d.Path]; both {
					t.Errorf("%s is reported as policy and as builtin", d.Path)
				}
			}
		})
	}
}

// TestBuiltinDefaults_RecreateStrategyForANonRWXClaim: a role-deployment with a claim
// that is not ReadWriteMany is built with the Recreate strategy unless the author
// wrote one. The fill is reported at strategy.type from the same decision Generate
// takes, over a matrix of authored and absent.
func TestBuiltinDefaults_RecreateStrategyForANonRWXClaim(t *testing.T) {
	rwx := []any{map[string]any{"name": "data", "type": "pvc", "claimName": "data-claim", "mountPath": "/data", "accessModes": []any{"ReadWriteMany"}}}
	cases := []struct {
		name     string
		props    map[string]any
		wantFill bool
	}{
		{"rwo claim, strategy absent", map[string]any{"volumes": oneVolume}, true},
		{"rwo claim, strategy authored", map[string]any{"volumes": oneVolume, "strategy": map[string]any{"type": "Recreate"}}, false},
		{"rwx claim, strategy absent", map[string]any{"volumes": rwx}, false},
		{"no claim", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newKindConfig(t, roleMemberType, tc.props)
			_, reported := reportedBuiltin(cfg)["strategy.type"]
			if reported != tc.wantFill {
				t.Errorf("strategy.type reported = %v, want %v", reported, tc.wantFill)
			}
			// The fill is real: with the value written out the objects do not move.
			if tc.wantFill {
				written := maps.Clone(tc.props)
				written["strategy"] = map[string]any{"type": "Recreate"}
				if a, b := generatedJSON(t, cfg), generatedJSON(t, newKindConfig(t, roleMemberType, written)); a != b {
					t.Errorf("authoring strategy.type Recreate changed the objects:\n%s\n%s", a, b)
				}
			}
		})
	}
}

// TestBuiltinDefaults_RefsAreKindAndFullPath: a built-in fill's Ref is the kind and
// the path with its indexes, for a parse-time fill and a generation-time fill of a
// container list entry alike.
func TestBuiltinDefaults_RefsAreKindAndFullPath(t *testing.T) {
	cfg := newKindConfig(t, roleMemberType, map[string]any{"initContainers": init1(ctrPorts)})
	r, ok := cfg.(oam.BuiltinDefaultReporter)
	if !ok {
		t.Fatalf("%T does not report built-in defaults", cfg)
	}
	got := map[string]string{}
	for _, d := range r.BuiltinDefaults() {
		got[d.Path] = d.Ref
	}
	for path, ref := range map[string]string{
		"initContainers[0].ports[0].protocol":         "deployment.initContainers[0].ports[0].protocol",
		"initContainers[0].resources.requests.cpu":    "deployment.initContainers[0].resources.requests.cpu",
		"initContainers[0].resources.limits.memory":   "deployment.initContainers[0].resources.limits.memory",
		"initContainers[0].resources.requests.memory": "deployment.initContainers[0].resources.requests.memory",
	} {
		if got[path] != ref {
			t.Errorf("Ref at %q = %q, want %q (reported: %v)", path, got[path], ref, got)
		}
	}
}
