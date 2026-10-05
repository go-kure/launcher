package oam

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	poolerKind   = schema.GroupKind{Group: "postgresql.cnpg.io", Kind: "Pooler"}
	databaseKind = schema.GroupKind{Group: "postgresql.cnpg.io", Kind: "Database"}
)

// loweringHarness is a LoweringContext as the engine hands one to a component
// rule inside a transform: the transform's allocator, carrying a hook that
// records what it is asked and answers from answers, by default name.
type loweringHarness struct {
	namer   *NameAllocator
	asked   []NameRequest
	answers map[string]string
}

func newLoweringHarness(answers map[string]string) *loweringHarness {
	h := &loweringHarness{namer: NewNameAllocator(), answers: answers}
	h.namer.hook = func(req NameRequest) (string, bool) {
		h.asked = append(h.asked, req)
		name, ok := h.answers[req.Default]
		return name, ok
	}
	return h
}

func (h *loweringHarness) lctx(component string) LoweringContext {
	return LoweringContext{
		Namer:     h.namer,
		Document:  &Application{Metadata: Metadata{Name: "shop"}},
		Component: &Component{Name: component, Type: "postgresql"},
	}
}

func TestLoweringResolveName_Order(t *testing.T) {
	authored := NameSpec{Role: NameRolePooler, Kind: poolerKind, Property: "poolerName", Authored: "mine"}
	plain := NameSpec{Role: NameRolePooler, Kind: poolerKind}

	for _, tt := range []struct {
		name      string
		answers   map[string]string
		spec      NameSpec
		want      string
		wantAsked []NameRequest
	}{
		{name: "the author's name wins and the hook is not asked", answers: map[string]string{"db-pooler": "theirs"}, spec: authored, want: "mine"},
		{
			name: "the hook's name is used when the author wrote none", answers: map[string]string{"db-pooler": "theirs"}, spec: plain, want: "theirs",
			wantAsked: []NameRequest{{Application: "shop", Component: "db", Role: NameRolePooler, Kind: "Pooler.postgresql.cnpg.io", Default: "db-pooler"}},
		},
		{
			name: "the hook declining keeps the default", spec: plain, want: "db-pooler",
			wantAsked: []NameRequest{{Application: "shop", Component: "db", Role: NameRolePooler, Kind: "Pooler.postgresql.cnpg.io", Default: "db-pooler"}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newLoweringHarness(tt.answers)
			got, err := h.lctx("db").ResolveName("db", "pooler", tt.spec)
			if err != nil {
				t.Fatalf("ResolveName: %v", err)
			}
			if got != tt.want {
				t.Errorf("name = %q, want %q", got, tt.want)
			}
			if !slices.Equal(h.asked, tt.wantAsked) {
				t.Errorf("the hook was asked %+v, want %+v", h.asked, tt.wantAsked)
			}
		})
	}

	t.Run("no hook keeps the default", func(t *testing.T) {
		lctx := LoweringContext{Namer: NewNameAllocator(), Component: &Component{Name: "db"}}
		got, err := lctx.ResolveName("db", "pooler", plain)
		if err != nil || got != "db-pooler" {
			t.Fatalf("ResolveName = %q, %v; want the default", got, err)
		}
	})
}

// NameRequest.Application is the name of the document the rule is lowering, as
// it stands when the name is made: the document in the context, else the origin
// document's, and the one a caller outside lowering names (ComponentEndpointsNamed)
// before both.
func TestLoweringResolveName_Application(t *testing.T) {
	spec := NameSpec{Role: NameRolePooler, Kind: poolerKind}
	doc := &Application{Metadata: Metadata{Name: "lowered"}}
	for _, tt := range []struct {
		name string
		lctx LoweringContext
		want string
	}{
		{"the document being lowered", LoweringContext{Document: doc, Origin: Origin{Document: "authored"}}, "lowered"},
		{"the origin's, with no document", LoweringContext{Origin: Origin{Document: "authored"}}, "authored"},
		{"the caller's, before both", LoweringContext{Document: doc, Origin: Origin{Document: "authored"}, application: "given"}, "given"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newLoweringHarness(nil)
			tt.lctx.Namer = h.namer
			if _, err := tt.lctx.ResolveName("db", "pooler", spec); err != nil {
				t.Fatalf("ResolveName: %v", err)
			}
			if len(h.asked) != 1 || h.asked[0].Application != tt.want || h.asked[0].Component != "" {
				t.Errorf("the hook was asked %+v, want Application %q and no component", h.asked, tt.want)
			}
		})
	}
}

func TestLoweringResolveName_Refusals(t *testing.T) {
	pooler := NameSpec{Role: NameRolePooler, Kind: poolerKind}
	for _, tt := range []struct {
		name    string
		lctx    func(h *loweringHarness) LoweringContext
		answers map[string]string
		suffix  string
		spec    NameSpec
		want    string
	}{
		{
			name: "no Namer", lctx: func(*loweringHarness) LoweringContext { return LoweringContext{} }, suffix: "pooler", spec: pooler,
			want: "ResolveName needs a LoweringContext with a Namer",
		},
		{
			name: "a role that names no object", suffix: "pooler", spec: NameSpec{Role: NameRoleBundle},
			want: `role "bundle" names no object; a lowering rule resolves object names only`,
		},
		{
			name: "an unknown role", suffix: "pooler", spec: NameSpec{Role: "service", Kind: poolerKind},
			want: `"service" is not a name role`,
		},
		{
			name: "an object role with no kind", suffix: "pooler", spec: NameSpec{Role: NameRolePooler},
			want: `role "pooler" names an object, and its NameSpec has no Kind`,
		},
		{
			name: "a default that is no name", suffix: "Not_A_Name", spec: pooler,
			want: "Not_A_Name",
		},
		{
			name: "an authored pooler name that is no DNS-1035 label", suffix: "pooler",
			spec: NameSpec{Role: NameRolePooler, Kind: poolerKind, Property: "poolerName", Authored: "a.b"},
			want: `poolerName "a.b" cannot be the name for role "pooler": not a valid DNS-1035 label: `,
		},
		{
			name: "an empty authored name", suffix: "pooler",
			spec: NameSpec{Role: NameRolePooler, Kind: poolerKind, Property: "poolerName"},
			want: `poolerName "" cannot be the name for role "pooler": it is empty; write a valid name, or leave the property out for the default "db-pooler"`,
		},
		{
			name: "a hook answer that is no DNS-1035 label", suffix: "pooler", spec: pooler, answers: map[string]string{"db-pooler": "1st"},
			want: `the Naming hook returned "1st" for role "pooler" in place of "db-pooler": not a valid DNS-1035 label: `,
		},
		{
			name: "a hook answer that is no subdomain, for a database", suffix: "orders",
			spec: NameSpec{Role: NameRoleDatabase, Kind: databaseKind}, answers: map[string]string{"db-orders": "Orders"},
			want: `the Naming hook returned "Orders" for role "database" in place of "db-orders": not a valid DNS-1123 subdomain: `,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newLoweringHarness(tt.answers)
			lctx := h.lctx("db")
			if tt.lctx != nil {
				lctx = tt.lctx(h)
			}
			_, err := lctx.ResolveName("db", tt.suffix, tt.spec)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want one containing %q", err, tt.want)
			}
		})
	}

	// The two roles differ in what they accept: a dotted name is a Database's
	// and not a Pooler's.
	t.Run("a dotted name is a database's", func(t *testing.T) {
		h := newLoweringHarness(map[string]string{"db-orders": "orders.v2"})
		got, err := h.lctx("db").ResolveName("db", "orders", NameSpec{Role: NameRoleDatabase, Kind: databaseKind})
		if err != nil || got != "orders.v2" {
			t.Fatalf("ResolveName = %q, %v; want the hook's dotted name", got, err)
		}
	})
}

// Where base and suffix build no valid default, an authored name is still the
// name and the hook, which has no default to be asked about, is not asked. Two
// such names of one component stay two owners: each is told from the other by
// its own base and suffix.
func TestLoweringResolveName_AuthoredWhereTheDefaultCannotBeBuilt(t *testing.T) {
	spec := func(i int, name string) NameSpec {
		return NameSpec{Role: NameRoleDatabase, Kind: databaseKind, Property: fmt.Sprintf("databases[%d].objectName", i), Authored: name}
	}
	h := newLoweringHarness(map[string]string{"db-app_data": "theirs"})
	got, err := h.lctx("db").ResolveName("db", "app_data", spec(0, "app-data"))
	if err != nil || got != "app-data" {
		t.Fatalf("ResolveName = %q, %v; want the authored name", got, err)
	}
	if len(h.asked) != 0 {
		t.Errorf("the hook was asked %+v, want nothing", h.asked)
	}

	_, err = h.lctx("db").ResolveName("db", "app_log", spec(1, "app-data"))
	want := `name collision: Database.postgresql.cnpg.io "app-data" is named by component "db" (role "database", set by databases[0].objectName) and by component "db" (role "database", set by databases[1].objectName); give one of them another name`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}

	_, err = h.lctx("db").ResolveName("db", "app_data", spec(0, "App_Data"))
	want = `databases[0].objectName "App_Data" cannot be the name for role "database": not a valid DNS-1123 subdomain: `
	if err == nil || !strings.Contains(err.Error(), want) || !strings.HasSuffix(err.Error(), "; write a valid name") {
		t.Fatalf("err = %v, want one containing %q and naming no default", err, want)
	}
}

// resolvingComponentRule resolves a pooler name for the component it lowers.
type resolvingComponentRule struct{}

func (resolvingComponentRule) ComponentType() string { return "probe" }

func (resolvingComponentRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	if _, err := lctx.ResolveName(comp.Name, "pooler", NameSpec{Role: NameRolePooler, Kind: poolerKind}); err != nil {
		return LoweringResult{}, err
	}
	return LoweringResult{Components: []Component{{Name: comp.Name + "-web", Type: "webservice", Properties: map[string]any{"image": "nginx"}}}}, nil
}

// resolvingDocRule is testDocRule, resolving a database name of its own before
// it renames the document.
type resolvingDocRule struct{ testDocRule }

func (r resolvingDocRule) LowerDocument(doc *Application, lctx LoweringContext) (LoweringResult, error) {
	if _, err := lctx.ResolveName(doc.Metadata.Name, "db", NameSpec{Role: NameRoleDatabase, Kind: databaseKind}); err != nil {
		return LoweringResult{}, err
	}
	return r.testDocRule.LowerDocument(doc, lctx)
}

// Inside a transform every rule is asked through the transform's hook, with the
// document's name as it stands when the name is made: a document rule with the
// name of the document it was given, a component rule, which runs once the
// document is renamed, with the lowered one.
func TestLoweringResolveName_ApplicationAcrossADocumentRename(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterDocumentLowering(resolvingDocRule{testDocRule{kind: "Renamer"}})
	tr.RegisterComponentLowering(resolvingComponentRule{})
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Renamer",
		Metadata:   Metadata{Name: "authored"},
		Spec:       ApplicationSpec{Components: []Component{{Name: "probe-me", Type: "probe", Properties: map[string]any{}}}},
	}

	h := newLoweringHarness(nil)
	if _, err := tr.lower(app, TransformContext{nameClaims: h.namer}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	want := []NameRequest{
		{Application: "authored", Role: NameRoleDatabase, Kind: "Database.postgresql.cnpg.io", Default: "authored-db"},
		{Application: "authored-lowered", Component: "probe-me", Role: NameRolePooler, Kind: "Pooler.postgresql.cnpg.io", Default: "probe-me-pooler"},
	}
	if !slices.Equal(h.asked, want) {
		t.Errorf("the hook was asked\n  %+v\nwant\n  %+v", h.asked, want)
	}
}

// hpaNamingRule lowers a "probe" component to an "a" component carrying a
// "named" trait, after resolving for itself the HorizontalPodAutoscaler name
// that trait resolves when it is applied.
type hpaNamingRule struct{}

func (hpaNamingRule) ComponentType() string { return "probe" }

func (hpaNamingRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	if _, err := lctx.ResolveName(comp.Name, "hpa", NameSpec{Role: NameRolePooler, Kind: hpaKind}); err != nil {
		return LoweringResult{}, err
	}
	return LoweringResult{Components: []Component{{
		Name: comp.Name, Type: "a", Properties: map[string]any{},
		Traits: []Trait{named(comp.Name+"-hpa", comp.Name+"-sub")},
	}}}, nil
}

// One transform holds a name a lowering rule resolved against the names it
// resolves after lowering: the two naming one object of the document's
// namespace are refused with both named.
func TestTransform_LoweredNameHeldAgainstALaterOne(t *testing.T) {
	tr := NewTransformer(map[string]ComponentHandler{"a": stubHandler("a", 0)}, map[string]TraitHandler{"named": namedTrait{}})
	tr.RegisterComponentLowering(hpaNamingRule{})
	app := func() *Application {
		return &Application{
			APIVersion: SupportedAPIVersion,
			Kind:       terminalDocumentKind,
			Metadata:   Metadata{Name: "shop"},
			Spec:       ApplicationSpec{Components: []Component{{Name: "web", Type: "probe", Properties: map[string]any{}}}},
		}
	}

	_, _, err := tr.TransformWithPolicy(app(), TransformContext{})
	const want = `name collision: HorizontalPodAutoscaler.autoscaling "default/web-hpa" is named by ` +
		`component "web" (role "pooler", its default) and by component "web" traits[0] "named"`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v\nwant one containing %q", err, want)
	}

	// The trait names its object in namespace default whatever the document's is
	// (hpaSpec), so in another namespace the two are two objects.
	if _, _, err := tr.TransformWithPolicy(app(), TransformContext{Namespace: "prod"}); err != nil {
		t.Fatalf("the rule's object in prod and the trait's in default: %v", err)
	}
}

// resolvingRawRule is testRawRule, resolving a pooler name and writing it into
// the document it emits. authored, when set, is the name the document's author
// wrote for it.
type resolvingRawRule struct {
	testRawRule
	authored string
}

func (r resolvingRawRule) LowerDocument(doc any, lctx LoweringContext) (LoweringResult, error) {
	spec := NameSpec{Role: NameRolePooler, Kind: poolerKind}
	if r.authored != "" {
		spec.Property, spec.Authored = "poolerName", r.authored
	}
	name, err := lctx.ResolveName(doc.(*testRawDoc).Metadata.Name, "pooler", spec)
	if err != nil {
		return LoweringResult{}, err
	}
	res, err := r.testRawRule.LowerDocument(doc, lctx)
	if err == nil {
		res.Documents[0].Spec.Components[0].Properties["poolerName"] = name
	}
	return res, err
}

// LowerRaws keeps the defaults: its rules are not asked through the context's
// Naming hook.
func TestLowerRaws_ResolveNameKeepsTheDefault(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterRawDocumentLowering(resolvingRawRule{testRawRule: testRawRule{kind: "WebApplication"}})
	asked := 0
	out, err := tr.LowerRaws([]json.RawMessage{rawWebApplication("shop")}, TransformContext{Naming: func(NameRequest) (string, bool) {
		asked++
		return "theirs", true
	}})
	if err != nil || len(out) != 1 {
		t.Fatalf("LowerRaws = %d documents, %v; want one", len(out), err)
	}
	if asked != 0 {
		t.Errorf("the Naming hook was asked %d times under LowerRaws, want none", asked)
	}
	if !strings.Contains(string(out[0]), "poolerName: shop-pooler") {
		t.Errorf("the raw rule's name is not the default shop-pooler:\n%s", out[0])
	}
}

// LowerRaws lowers several documents with one allocator: one name resolved for
// two of them is one object only where they share a namespace.
func TestLowerRaws_ResolveNameAcrossDocuments(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterRawDocumentLowering(resolvingRawRule{testRawRule: testRawRule{kind: "WebApplication"}, authored: "shared"})

	apart := []json.RawMessage{rawWebApplicationNS("a", "one"), rawWebApplicationNS("b", "two")}
	if _, err := tr.LowerRaws(apart, TransformContext{}); err != nil {
		t.Fatalf("one name in two namespaces: %v", err)
	}

	together := []json.RawMessage{rawWebApplicationNS("a", "one"), rawWebApplicationNS("b", "one")}
	_, err := tr.LowerRaws(together, TransformContext{})
	const want = `name collision: Pooler.postgresql.cnpg.io "shared" is named by ` +
		`document "a" (role "pooler", set by poolerName) and by ` +
		`document "b" (role "pooler", set by poolerName); give one of them another name`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v\nwant one containing %s", err, want)
	}
}

// resolvingTraitRule lowers its trait to a terminal one, after resolving the
// pooler name of the component the trait is on.
type resolvingTraitRule struct{ typ string }

func (r resolvingTraitRule) TraitType() string { return r.typ }

func (r resolvingTraitRule) LowerTrait(_ *Trait, lctx LoweringContext) (LoweringResult, error) {
	if _, err := lctx.ResolveName(lctx.Component.Name, "pooler", NameSpec{Role: NameRolePooler, Kind: poolerKind}); err != nil {
		return LoweringResult{}, err
	}
	return LoweringResult{Traits: []Trait{{Type: "expose", Properties: map[string]any{}}}}, nil
}

// Two rules that each resolve one name for one component would each generate
// the object: the second is refused, with both named.
func TestLoweringResolveName_TwoRulesOneName(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterTraitLowering(resolvingTraitRule{typ: "pooled"})
	tr.RegisterTraitLowering(resolvingTraitRule{typ: "pooled-too"})
	app := makeApp("shop", Component{
		Name: "db",
		Type: "webservice",
		Traits: []Trait{
			{Type: "pooled", Properties: map[string]any{}},
			{Type: "pooled-too", Properties: map[string]any{}},
		},
	})
	app.APIVersion = SupportedAPIVersion
	app.Kind = terminalDocumentKind

	_, err := tr.lower(app, TransformContext{})
	const want = `name collision: Pooler.postgresql.cnpg.io "db-pooler" is named by ` +
		`component "db" traits[0] "pooled" (role "pooler", its default) and by ` +
		`component "db" traits[1] "pooled-too" (role "pooler", its default); give one of them another name`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v\nwant one containing %s", err, want)
	}
}

// A name a rule resolves is not reserved as a component name: the rule reserves
// the one it emits a component under, and may emit two under one name.
func TestLoweringResolveName_ReservesNoComponentName(t *testing.T) {
	h := newLoweringHarness(nil)
	lctx := h.lctx("db")
	name, err := lctx.ResolveName("db", "pooler", NameSpec{Role: NameRolePooler, Kind: poolerKind})
	if err != nil {
		t.Fatalf("ResolveName: %v", err)
	}
	if err := h.namer.Reserve(name, lctx.Origin); err != nil {
		t.Fatalf("reserving the resolved name as a component name: %v", err)
	}
}

// Two names resolved for one kind are refused when they are one name, with
// both named. So is one name resolved a second time for the same default: each
// resolution names an object the rule generates.
func TestLoweringResolveName_TwoOfOneKind(t *testing.T) {
	database := func(property, authored string) NameSpec {
		return NameSpec{Role: NameRoleDatabase, Kind: databaseKind, Property: property, Authored: authored}
	}

	h := newLoweringHarness(nil)
	lctx := h.lctx("db")
	if _, err := lctx.ResolveName("db", "orders", database("", "")); err != nil {
		t.Fatalf("ResolveName: %v", err)
	}
	_, err := lctx.ResolveName("db", "billing", database("databases[1].objectName", "db-orders"))
	const want = `name collision: Database.postgresql.cnpg.io "db-orders" is named by ` +
		`component "db" (role "database", its default) and by ` +
		`component "db" (role "database", set by databases[1].objectName); give one of them another name`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}

	_, err = lctx.ResolveName("db", "orders", database("", ""))
	const wantTwice = `name collision: Database.postgresql.cnpg.io "db-orders" is named twice by ` +
		`component "db" (role "database", its default); give one of them another name`
	if err == nil || err.Error() != wantTwice {
		t.Fatalf("err = %v\nwant %s", err, wantTwice)
	}

	// A Pooler and a Database of one name are two objects.
	h = newLoweringHarness(nil)
	lctx = h.lctx("db")
	if _, err := lctx.ResolveName("db", "pooler", NameSpec{Role: NameRolePooler, Kind: poolerKind}); err != nil {
		t.Fatalf("ResolveName: %v", err)
	}
	if _, err := lctx.ResolveName("db", "pooler", database("", "")); err != nil {
		t.Fatalf("a Database named like the Pooler: %v", err)
	}
}

// The names lowering rules resolved are claimed with the document's namespace
// before any other, so one resolved after lowering that names the same object
// is refused with both named, and one in another namespace or of another kind
// is not.
func TestClaimLowered_HeldAgainstNamesResolvedAfterLowering(t *testing.T) {
	setup := func(t *testing.T) *nameResolver {
		t.Helper()
		h := newLoweringHarness(nil)
		if _, err := h.lctx("db").ResolveName("db", "pooler", NameSpec{Role: NameRolePooler, Kind: poolerKind}); err != nil {
			t.Fatalf("ResolveName: %v", err)
		}
		if err := h.namer.claimLowered("prod"); err != nil {
			t.Fatalf("claimLowered: %v", err)
		}
		return &nameResolver{application: "shop", claims: h.namer}
	}
	// A name resolved after lowering, for an object of kind in namespace. No role
	// resolved there names a Pooler yet, so the role is another object role's.
	later := func(kind schema.GroupKind, namespace string) (nameOwner, NameSpec) {
		spec := NameSpec{Role: NameRoleNetworkPolicy, Kind: kind, Namespace: namespace, Default: "db-allow", Property: "name", Authored: "db-pooler"}
		return nameOwner{component: "web", role: spec.Role, trait: "networkpolicy", authored: true, def: spec.Default}, spec
	}

	t.Run("the same kind, namespace and name", func(t *testing.T) {
		owner, spec := later(poolerKind, "prod")
		_, err := setup(t).resolve(owner, spec)
		const want = `name collision: Pooler.postgresql.cnpg.io "prod/db-pooler" is named by ` +
			`component "db" (role "pooler", its default) and by ` +
			`component "web" traits[0] "networkpolicy" (role "networkpolicy", set by name); give one of them another name`
		if err == nil || err.Error() != want {
			t.Fatalf("err = %v\nwant %s", err, want)
		}
	})
	t.Run("another namespace", func(t *testing.T) {
		owner, spec := later(poolerKind, "staging")
		if _, err := setup(t).resolve(owner, spec); err != nil {
			t.Fatalf("resolve: %v", err)
		}
	})
	t.Run("another kind", func(t *testing.T) {
		owner, spec := later(databaseKind, "prod")
		if _, err := setup(t).resolve(owner, spec); err != nil {
			t.Fatalf("resolve: %v", err)
		}
	})
}
