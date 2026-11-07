package oam

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	outputClusterKind = schema.GroupKind{Group: "postgresql.cnpg.io", Kind: "Cluster"}
	outputSecretKind  = schema.GroupKind{Group: "", Kind: "Secret"}
)

// stubOutputRule is a ComponentLoweringRule that also implements OutputProvider.
// Its decls run on the LoweringContext the query hands it.
type stubOutputRule struct {
	typeName string
	decls    func(c *Component, lctx LoweringContext) ([]OutputDecl, error)
}

func (r stubOutputRule) ComponentType() string { return r.typeName }
func (r stubOutputRule) LowerComponent(c *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{*c}}, nil
}
func (r stubOutputRule) Outputs(c *Component, lctx LoweringContext) ([]OutputDecl, error) {
	return r.decls(c, lctx)
}

// clusterDecls declares the cluster, resolved under its name role, and a Secret
// derived from it.
func clusterDecls(c *Component, lctx LoweringContext) ([]OutputDecl, error) {
	name, err := lctx.ResolveName(c.Name, "cluster", NameSpec{Role: NameRolePostgresqlCluster, Kind: outputClusterKind})
	if err != nil {
		return nil, err
	}
	return []OutputDecl{
		{Key: "cluster", Role: NameRolePostgresqlCluster, Kind: outputClusterKind, Default: c.Name + "-cluster", Name: name},
		{Key: "app_secret", Kind: outputSecretKind, From: "cluster", Derive: func(s string) string { return s + "-app" }},
	}, nil
}

func TestComponentOutputs_RuleDispatch(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(stubOutputRule{typeName: "db", decls: clusterDecls})
	tr.RegisterComponentLowering(stubPlainRule{typeName: "plain"})

	got, err := tr.ComponentOutputs(&Component{Name: "pg", Type: "db"})
	if err != nil {
		t.Fatalf("ComponentOutputs: %v", err)
	}
	want := []Output{
		{Key: "cluster", Kind: outputClusterKind, Name: "pg-cluster"},
		{Key: "app_secret", Kind: outputSecretKind, Name: "pg-cluster-app"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("ComponentOutputs = %+v, want %+v", got, want)
	}
	for _, c := range []*Component{nil, {Name: "x", Type: "plain"}, {Name: "x", Type: "unknown"}} {
		if outs, err := tr.ComponentOutputs(c); outs != nil || err != nil {
			t.Errorf("ComponentOutputs(%+v) = %v, %v; want nil, nil", c, outs, err)
		}
	}
}

// The query claims nothing: asking twice, and asking for a name the transform
// would refuse as a second resolution, gives the same answer both times.
func TestComponentOutputs_ClaimsNothing(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(stubOutputRule{typeName: "db", decls: clusterDecls})
	comp := &Component{Name: "pg", Type: "db"}
	for i := 0; i < 2; i++ {
		if _, err := tr.ComponentOutputs(comp); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
}

func TestComponentOutputsNamed_Hook(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(stubOutputRule{typeName: "db", decls: clusterDecls})
	comp := &Component{Name: "pg", Type: "db"}

	var asked []NameRequest
	hook := func(req NameRequest) (string, bool) {
		asked = append(asked, req)
		return "chosen", true
	}
	got, err := tr.ComponentOutputsNamed("shop", comp, hook)
	if err != nil || len(got) != 2 {
		t.Fatalf("ComponentOutputsNamed = %v, %v", got, err)
	}
	if got[0].Name != "chosen" || got[1].Name != "chosen-app" {
		t.Errorf("names = %q, %q; want the hook's name and the one derived from it", got[0].Name, got[1].Name)
	}
	want := NameRequest{Application: "shop", Component: "pg", Role: NameRolePostgresqlCluster, Kind: "Cluster.postgresql.cnpg.io", Default: "pg-cluster"}
	if len(asked) != 1 || asked[0] != want {
		t.Errorf("the hook was asked %+v, want once with %+v", asked, want)
	}

	// A nil hook is ComponentOutputs.
	got, err = tr.ComponentOutputsNamed("shop", comp, nil)
	if err != nil || got[0].Name != "pg-cluster" {
		t.Errorf("no hook: %v, %v; want the default", got, err)
	}

	// An answer the transform refuses is refused here, in the same words.
	_, err = tr.ComponentOutputsNamed("shop", comp, func(NameRequest) (string, bool) { return "No_Name", true })
	if err == nil || !strings.Contains(err.Error(), `the Naming hook returned "No_Name"`) {
		t.Errorf("err = %v, want the transform's refusal", err)
	}
}

func TestComponentOutputs_RefusesBadDeclarations(t *testing.T) {
	derive := func(s string) string { return s }
	cases := []struct {
		name  string
		decls []OutputDecl
		want  string
	}{
		{"hyphenated key", []OutputDecl{{Key: "app-secret", Kind: outputSecretKind, Name: "x"}}, `key "app-secret"`},
		{"leading digit", []OutputDecl{{Key: "0data", Kind: outputSecretKind, Name: "x"}}, `key "0data"`},
		{"empty key", []OutputDecl{{Kind: outputSecretKind, Name: "x"}}, `key ""`},
		{"duplicate key", []OutputDecl{
			{Key: "a", Kind: outputSecretKind, Name: "x"},
			{Key: "a", Kind: outputSecretKind, Name: "y"},
		}, `declared twice`},
		{"allocated output without a name", []OutputDecl{{Key: "a", Kind: outputSecretKind}}, `no name`},
		{"derived from a missing key", []OutputDecl{{Key: "a", Kind: outputSecretKind, From: "nope", Derive: derive}}, `derives from "nope"`},
		{"derived from a later key", []OutputDecl{
			{Key: "a", Kind: outputSecretKind, From: "b", Derive: derive},
			{Key: "b", Kind: outputSecretKind, Name: "x"},
		}, `derives from "b"`},
		{"derived without a function", []OutputDecl{
			{Key: "b", Kind: outputSecretKind, Name: "x"},
			{Key: "a", Kind: outputSecretKind, From: "b"},
		}, `no derivation`},
		{"derived that also names itself", []OutputDecl{
			{Key: "b", Kind: outputSecretKind, Name: "x"},
			{Key: "a", Kind: outputSecretKind, From: "b", Derive: derive, Name: "y"},
		}, `both`},
		{"no kind", []OutputDecl{{Key: "a", Name: "x"}}, `no kind`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewTransformer(nil, nil)
			tr.RegisterComponentLowering(stubOutputRule{typeName: "db", decls: func(*Component, LoweringContext) ([]OutputDecl, error) {
				return tc.decls, nil
			}})
			_, err := tr.ComponentOutputs(&Component{Name: "pg", Type: "db"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

// An allocated declaration is held against the name the allocator gives, which
// needs the role the name is resolved under and the default it falls back to. A
// declaration the allocator would refuse (an unknown role, a role that names no
// object, no default) is refused where it is answered, by the query as by the
// build, with the output's key and what is wrong.
func TestComponentOutputs_RefusesAnAllocatedDeclarationTheAllocatorWouldRefuse(t *testing.T) {
	cases := []struct {
		name string
		decl OutputDecl
		want []string
	}{
		{"unknown role", OutputDecl{Key: "a", Role: "nonsense", Kind: outputSecretKind, Default: "pg", Name: "pg"}, []string{`output "a"`, `"nonsense"`, "name role"}},
		{"empty role", OutputDecl{Key: "a", Kind: outputSecretKind, Default: "pg", Name: "pg"}, []string{`output "a"`, "name role"}},
		{"role that names no object", OutputDecl{Key: "a", Role: NameRoleBundle, Kind: outputSecretKind, Default: "pg", Name: "pg"}, []string{`output "a"`, `"` + string(NameRoleBundle) + `"`, "no object"}},
		{"empty default", OutputDecl{Key: "a", Role: NameRoleObject, Kind: outputSecretKind, Name: "pg"}, []string{`output "a"`, "default"}},
		{"blank default", OutputDecl{Key: "a", Role: NameRoleObject, Kind: outputSecretKind, Default: "  ", Name: "pg"}, []string{`output "a"`, "default"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewTransformer(nil, nil)
			tr.RegisterComponentLowering(stubOutputRule{typeName: "db", decls: func(*Component, LoweringContext) ([]OutputDecl, error) {
				return []OutputDecl{tc.decl}, nil
			}})
			_, err := tr.ComponentOutputs(&Component{Name: "pg", Type: "db"})
			if err == nil {
				t.Fatalf("ComponentOutputs admitted %+v", tc.decl)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("err = %v, want it to contain %q", err, w)
				}
			}
		})
	}

	// A valid allocated declaration is still admitted.
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(stubOutputRule{typeName: "db", decls: clusterDecls})
	if _, err := tr.ComponentOutputs(&Component{Name: "pg", Type: "db"}); err != nil {
		t.Errorf("a valid declaration: %v", err)
	}
}

// A key is one CEL identifier, so every shape a fixed key or a volume claim key
// takes passes the check and a name with a hyphen does not.
func TestValidOutputKey(t *testing.T) {
	for _, k := range []string{"cluster", "app_secret", "claim_app_data", "claim_0data", "_x", "A1"} {
		if !validOutputKey(k) {
			t.Errorf("validOutputKey(%q) = false, want true", k)
		}
	}
	for _, k := range []string{"", "app-secret", "0data", "a.b", "a b", "é"} {
		if validOutputKey(k) {
			t.Errorf("validOutputKey(%q) = true, want false", k)
		}
	}
}
