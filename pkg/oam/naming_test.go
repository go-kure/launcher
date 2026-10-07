package oam

import (
	"os"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	hpaKind  = schema.GroupKind{Group: "autoscaling", Kind: "HorizontalPodAutoscaler"}
	roleKind = schema.GroupKind{Group: "rbac.authorization.k8s.io", Kind: "Role"}
)

// TestNameRoles_ReadmeTable holds the README's role table to NameRoles: one row
// per role, in the same order, and none for anything else. A role added to the
// code without its row, or documented without existing, fails here.
func TestNameRoles_ReadmeTable(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("reading the README: %v", err)
	}
	const heading = "### Name roles and the `Naming` hook\n"
	_, section, found := strings.Cut(string(readme), heading)
	if !found {
		t.Fatalf("the README has no %q section", strings.TrimSpace(heading))
	}
	var rows []NameRole
	inTable := false
	for line := range strings.SplitSeq(section, "\n") {
		if !strings.HasPrefix(line, "|") {
			if inTable {
				break
			}
			continue
		}
		inTable = true
		// The header and the rule under it carry no backticked first cell.
		if cell := strings.TrimSpace(strings.Split(line, "|")[1]); strings.HasPrefix(cell, "`") {
			rows = append(rows, NameRole(strings.Trim(cell, "`")))
		}
	}
	if !slices.Equal(rows, NameRoles()) {
		t.Errorf("the README's role table lists %v, NameRoles() is %v", rows, NameRoles())
	}
}

// namingHarness is one transform's resolver with a hook that records what it
// is asked and answers from answers, by default name.
type namingHarness struct {
	resolver *nameResolver
	asked    []NameRequest
	answers  map[string]string
}

func newNamingHarness(answers map[string]string) *namingHarness {
	h := &namingHarness{answers: answers}
	h.resolver = &nameResolver{application: "shop", claims: NewNameAllocator(), hook: func(req NameRequest) (string, bool) {
		h.asked = append(h.asked, req)
		name, ok := h.answers[req.Default]
		return name, ok
	}}
	return h
}

// trait returns a trait as the engine hands one to a handler: the slot-th
// trait of component.
func (h *namingHarness) trait(component, traitType string, slot int) *Trait {
	return &Trait{Type: traitType, naming: &traitNaming{resolver: h.resolver, component: component, slot: slot}}
}

func hpaSpec(def string) NameSpec {
	return NameSpec{Role: NameRoleHPA, Kind: hpaKind, Namespace: "default", Default: def}
}

func TestNameRoles_ClosedSetInFixedOrder(t *testing.T) {
	want := []NameRole{"bundle", "group", "sub-application", "netpol-synth", "hpa", "pdb", "tls-secret", "external-secret", "rbac", "networkpolicy", "ingress", "httproute", "volsync-replicationsource", "pooler", "database", "object", "helm-source", "values-configmap", "values-secret", "helm-release", "oci-kustomization", "oci-source", "workload-deployment", "workload-service", "workload-serviceaccount", "workload-volume-claim", "postgresql-cluster", "postgresql-objectstore", "hook-group", "layout"}
	if got := NameRoles(); !slices.Equal(got, want) {
		t.Fatalf("NameRoles() = %v, want %v", got, want)
	}
	roles := NameRoles()
	roles[0] = "changed"
	if NameRoles()[0] != NameRoleBundle {
		t.Fatal("NameRoles returned its own table: a caller changed it")
	}
}

func TestResolveName_Order(t *testing.T) {
	authored := hpaSpec("web-hpa")
	authored.Property, authored.Authored = "hpaName", "mine"

	tests := []struct {
		name      string
		answers   map[string]string
		spec      NameSpec
		want      string
		wantAsked int
	}{
		{name: "the author's name wins and the hook is not asked", answers: map[string]string{"web-hpa": "theirs"}, spec: authored, want: "mine", wantAsked: 0},
		{name: "the hook's name is used when the author wrote none", answers: map[string]string{"web-hpa": "theirs"}, spec: hpaSpec("web-hpa"), want: "theirs", wantAsked: 1},
		{name: "the hook declining keeps the default", spec: hpaSpec("web-hpa"), want: "web-hpa", wantAsked: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newNamingHarness(tt.answers)
			got, err := h.trait("web", "scaler", 0).ResolveName(tt.spec)
			if err != nil {
				t.Fatalf("ResolveName: %v", err)
			}
			if got != tt.want {
				t.Errorf("name = %q, want %q", got, tt.want)
			}
			if len(h.asked) != tt.wantAsked {
				t.Errorf("the hook was asked %d times, want %d", len(h.asked), tt.wantAsked)
			}
		})
	}

	t.Run("no hook keeps the default", func(t *testing.T) {
		tr := &Trait{Type: "scaler", naming: &traitNaming{resolver: &nameResolver{application: "shop", claims: NewNameAllocator()}, component: "web"}}
		got, err := tr.ResolveName(hpaSpec("web-hpa"))
		if err != nil || got != "web-hpa" {
			t.Fatalf("ResolveName = %q, %v; want the default", got, err)
		}
	})
}

// The hook is asked once per resolved name, with no memory between calls: the
// same trait resolving the same name again asks again.
func TestResolveName_HookAskedOncePerCall(t *testing.T) {
	h := newNamingHarness(nil)
	tr := h.trait("web", "scaler", 0)
	for range 2 {
		if _, err := tr.ResolveName(hpaSpec("web-hpa")); err != nil {
			t.Fatalf("ResolveName: %v", err)
		}
	}
	pdb := NameSpec{Role: NameRolePDB, Kind: schema.GroupKind{Group: "policy", Kind: "PodDisruptionBudget"}, Namespace: "default", Default: "web-pdb"}
	if _, err := tr.ResolveName(pdb); err != nil {
		t.Fatalf("ResolveName: %v", err)
	}
	if len(h.asked) != 3 {
		t.Fatalf("the hook was asked %d times for 3 calls, want 3", len(h.asked))
	}
}

func TestResolveName_HookRequest(t *testing.T) {
	h := newNamingHarness(nil)
	if _, err := h.trait("web", "scaler", 0).ResolveName(hpaSpec("web-hpa")); err != nil {
		t.Fatalf("ResolveName: %v", err)
	}
	sub := NameSpec{Role: NameRoleSubApplication, Default: "web-scaler"}
	if _, err := h.trait("web", "scaler", 0).ResolveName(sub); err != nil {
		t.Fatalf("ResolveName: %v", err)
	}
	want := []NameRequest{
		{Application: "shop", Component: "web", Role: NameRoleHPA, Kind: "HorizontalPodAutoscaler.autoscaling", Default: "web-hpa"},
		{Application: "shop", Component: "web", Role: NameRoleSubApplication, Kind: "", Default: "web-scaler"},
	}
	if !slices.Equal(h.asked, want) {
		t.Fatalf("the hook was asked\n  %+v\nwant\n  %+v", h.asked, want)
	}
}

// A document lowering rule may rename the document. The hook is asked about the
// document the names are resolved for: the renamed one, which the defaults are
// built from (go-kure/launcher#787).
func TestNaming_ApplicationIsTheLoweredDocument(t *testing.T) {
	tr := NewTransformer(map[string]ComponentHandler{"a": stubHandler("a", 0)},
		map[string]TraitHandler{"named": namedTrait{}})
	tr.RegisterDocumentLowering(testDocRule{kind: "Renamer"})

	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Renamer",
		Metadata:   Metadata{Name: "authored"},
		Spec: ApplicationSpec{Components: []Component{{
			Name: "web", Type: "a", Properties: map[string]any{},
			Traits: []Trait{named("web-hpa", "web-sub")},
		}}},
	}
	var asked []NameRequest
	_, _, err := tr.TransformWithPolicy(app, TransformContext{Naming: func(req NameRequest) (string, bool) {
		asked = append(asked, req)
		return "", false
	}})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}

	var roles []NameRole
	for _, req := range asked {
		roles = append(roles, req.Role)
		if req.Application != "authored-lowered" {
			t.Errorf("role %q: Application = %q, want the lowered document's name %q", req.Role, req.Application, "authored-lowered")
		}
		if req.Role == NameRoleBundle && req.Default != "authored-lowered" {
			t.Errorf("the bundle's default is %q, want the lowered document's name %q", req.Default, "authored-lowered")
		}
	}
	for _, role := range []NameRole{NameRoleBundle, NameRoleHPA, NameRoleSubApplication} {
		if !slices.Contains(roles, role) {
			t.Errorf("the hook was not asked for role %q; it was asked for %v", role, roles)
		}
	}
}

// An override is used as written or refused, never shortened; the error names
// the role and where the name came from.
func TestResolveName_RefusesAnInvalidOverride(t *testing.T) {
	tooLong := strings.Repeat("a", 254)
	fits := strings.Repeat("a", 253)

	for _, tt := range []struct{ name, value, want string }{
		{"empty", "", "it is empty"},
		{"uppercase", "Web", "not a valid DNS-1123 subdomain"},
		{"a path", "a/b", "not a valid DNS-1123 subdomain"},
		{"254 characters", tooLong, "must be no more than 253 characters"},
	} {
		t.Run("authored "+tt.name, func(t *testing.T) {
			h := newNamingHarness(nil)
			spec := hpaSpec("web-hpa")
			spec.Property, spec.Authored = "hpaName", tt.value
			_, err := h.trait("web", "scaler", 0).ResolveName(spec)
			for _, want := range []string{"hpaName", `role "hpa"`, tt.want} {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %v, want one containing %q", err, want)
				}
			}
			if len(h.asked) != 0 {
				t.Errorf("the hook was asked about a name the author wrote")
			}
		})
		t.Run("hook "+tt.name, func(t *testing.T) {
			h := newNamingHarness(map[string]string{"web-hpa": tt.value})
			_, err := h.trait("web", "scaler", 0).ResolveName(hpaSpec("web-hpa"))
			for _, want := range []string{"the Naming hook returned", `role "hpa"`, `in place of "web-hpa"`, tt.want} {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %v, want one containing %q", err, want)
				}
			}
		})
	}

	t.Run("253 characters are used as written", func(t *testing.T) {
		h := newNamingHarness(map[string]string{"web-hpa": fits})
		got, err := h.trait("web", "scaler", 0).ResolveName(hpaSpec("web-hpa"))
		if err != nil || got != fits {
			t.Fatalf("ResolveName = %q, %v; want the 253-character name unchanged", got, err)
		}
	})
}

// Every role takes the same rule for an override, a role that names no object
// included.
func TestResolveName_NonObjectRolesTakeTheSubdomainRule(t *testing.T) {
	for _, role := range []NameRole{NameRoleBundle, NameRoleGroup, NameRoleSubApplication} {
		for _, bad := range []string{"", "Shop", "a/b", "..", "under_score", strings.Repeat("a", 254)} {
			h := newNamingHarness(map[string]string{"shop": bad})
			_, err := h.resolver.resolve(nameOwner{role: role, def: "shop"}, NameSpec{Role: role, Default: "shop"})
			if err == nil || !strings.Contains(err.Error(), "the Naming hook returned") {
				t.Errorf("role %q: hook answer %q: err = %v, want a refusal", role, bad, err)
			}
		}
	}
}

// The default is launcher's own name: it is used as it is, not checked again.
func TestResolveName_DefaultIsUsedAsItIs(t *testing.T) {
	h := newNamingHarness(nil)
	odd := strings.Repeat("a", 300)
	got, err := h.trait("web", "scaler", 0).ResolveName(NameSpec{Role: NameRoleSubApplication, Default: odd})
	if err != nil || got != odd {
		t.Fatalf("ResolveName = %d characters, %v; want the default unchanged", len(got), err)
	}
}

func TestResolveName_Claims(t *testing.T) {
	t.Run("two traits resolving one object name is refused, naming both", func(t *testing.T) {
		h := newNamingHarness(nil)
		if _, err := h.trait("web", "scaler", 0).ResolveName(hpaSpec("web-hpa")); err != nil {
			t.Fatalf("first: %v", err)
		}
		_, err := h.trait("web", "scaler", 1).ResolveName(hpaSpec("web-hpa"))
		want := `name collision: HorizontalPodAutoscaler.autoscaling "default/web-hpa" is named by component "web" traits[0] "scaler" (role "hpa", its default) and by component "web" traits[1] "scaler" (role "hpa", its default); give one of them another name`
		if err == nil || err.Error() != want {
			t.Fatalf("err = %v\nwant  %s", err, want)
		}
	})

	t.Run("a hook name equal to another default is refused, naming the hook", func(t *testing.T) {
		h := newNamingHarness(map[string]string{"api-hpa": "web-hpa"})
		if _, err := h.trait("web", "scaler", 0).ResolveName(hpaSpec("web-hpa")); err != nil {
			t.Fatalf("first: %v", err)
		}
		_, err := h.trait("api", "scaler", 0).ResolveName(hpaSpec("api-hpa"))
		for _, want := range []string{`component "web" traits[0] "scaler" (role "hpa", its default)`, `component "api" traits[0] "scaler" (role "hpa", returned by the Naming hook in place of "api-hpa")`} {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want one containing %q", err, want)
			}
		}
	})

	t.Run("an authored name equal to another default is refused, naming the property", func(t *testing.T) {
		h := newNamingHarness(nil)
		if _, err := h.trait("web", "scaler", 0).ResolveName(hpaSpec("web-hpa")); err != nil {
			t.Fatalf("first: %v", err)
		}
		spec := hpaSpec("api-hpa")
		spec.Property, spec.Authored = "hpaName", "web-hpa"
		_, err := h.trait("api", "scaler", 0).ResolveName(spec)
		want := `component "api" traits[0] "scaler" (role "hpa", set by hpaName)`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want one containing %q", err, want)
		}
	})

	t.Run("the same trait resolving its name again claims nothing new", func(t *testing.T) {
		h := newNamingHarness(nil)
		for range 2 {
			if _, err := h.trait("web", "scaler", 0).ResolveName(hpaSpec("web-hpa")); err != nil {
				t.Fatalf("ResolveName: %v", err)
			}
		}
	})

	t.Run("one name for another kind, or in another namespace, is another object", func(t *testing.T) {
		h := newNamingHarness(nil)
		specs := []NameSpec{
			hpaSpec("web"),
			{Role: NameRoleRBAC, Kind: roleKind, Namespace: "default", Default: "web"},
			{Role: NameRoleRBAC, Kind: roleKind, Namespace: "other", Default: "web"},
		}
		for i, spec := range specs {
			if _, err := h.trait("web", "t", i).ResolveName(spec); err != nil {
				t.Fatalf("spec %d: %v", i, err)
			}
		}
	})

	t.Run("a group's bundle and the application's share one space", func(t *testing.T) {
		h := newNamingHarness(map[string]string{"shop-apps": "shop"})
		if _, err := h.resolver.resolve(nameOwner{role: NameRoleBundle, def: "shop"}, NameSpec{Role: NameRoleBundle, Default: "shop"}); err != nil {
			t.Fatalf("bundle: %v", err)
		}
		_, err := h.resolver.resolve(nameOwner{role: NameRoleGroup, def: "shop-apps"}, NameSpec{Role: NameRoleGroup, Default: "shop-apps"})
		want := `name collision: bundle "shop" is named by the application (role "bundle", its default) and by the application (role "group", returned by the Naming hook in place of "shop-apps"); give one of them another name`
		if err == nil || err.Error() != want {
			t.Fatalf("err = %v\nwant  %s", err, want)
		}
	})

	// The resolver claims no sub-application name: a policy may rename one after
	// it is resolved, so the bundle refuses two of one name once its traits have
	// run (TestBundleApplicationNames).
	t.Run("two sub-applications of one name are not claimed", func(t *testing.T) {
		h := newNamingHarness(nil)
		for i, traitType := range []string{"configmap", "pvc"} {
			if _, err := h.trait("web", traitType, i).ResolveName(NameSpec{Role: NameRoleSubApplication, Default: "dup"}); err != nil {
				t.Fatalf("%s: %v", traitType, err)
			}
		}
	})

	t.Run("a forwarded trait and a rule's own trait in one slot are two owners", func(t *testing.T) {
		h := newNamingHarness(nil)
		forwarded := h.trait("web", "scaler", 0)
		forwarded.naming.authored = true
		if _, err := forwarded.ResolveName(hpaSpec("web-hpa")); err != nil {
			t.Fatalf("first: %v", err)
		}
		if _, err := h.trait("web", "scaler", 0).ResolveName(hpaSpec("web-hpa")); err == nil {
			t.Fatal("two traits resolved one HorizontalPodAutoscaler name and neither was refused")
		}
	})
}

// A NameSpec the resolver cannot key is a handler's mistake, refused before any
// name is used.
func TestResolveName_RefusesAMalformedSpec(t *testing.T) {
	for _, tt := range []struct {
		name string
		spec NameSpec
		want string
	}{
		{"an unknown role", NameSpec{Role: "service", Default: "web"}, `"service" is not a name role (the roles: bundle, group, sub-application, netpol-synth, hpa, pdb, tls-secret, external-secret, rbac, networkpolicy, ingress, httproute, volsync-replicationsource, pooler, database, object, helm-source, values-configmap, values-secret, helm-release, oci-kustomization, oci-source, workload-deployment, workload-service, workload-serviceaccount, workload-volume-claim, postgresql-cluster, postgresql-objectstore, hook-group, layout)`},
		{"an object role with no kind", NameSpec{Role: NameRoleHPA, Default: "web-hpa"}, `role "hpa" names an object, and its NameSpec has no Kind`},
		{"a non-object role with a kind", NameSpec{Role: NameRoleSubApplication, Kind: hpaKind, Default: "web"}, `role "sub-application" names no object`},
		{"no default", NameSpec{Role: NameRoleSubApplication}, `role "sub-application" has no default name`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newNamingHarness(nil)
			_, err := h.trait("web", "scaler", 0).ResolveName(tt.spec)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want one containing %q", err, tt.want)
			}
			if len(h.asked) != 0 {
				t.Error("the hook was asked about a malformed spec")
			}
		})
	}
}

// A trait built outside a transform has no hook and no claim space: its
// ResolveName validates an authored name and returns it or the default.
func TestResolveName_OutsideATransform(t *testing.T) {
	tr := &Trait{Type: "scaler"}
	if got, err := tr.ResolveName(hpaSpec("web-hpa")); err != nil || got != "web-hpa" {
		t.Fatalf("default: ResolveName = %q, %v", got, err)
	}
	spec := hpaSpec("web-hpa")
	spec.Property, spec.Authored = "hpaName", "mine"
	if got, err := tr.ResolveName(spec); err != nil || got != "mine" {
		t.Fatalf("authored: ResolveName = %q, %v", got, err)
	}
	spec.Authored = "Mine"
	if _, err := tr.ResolveName(spec); err == nil || !strings.Contains(err.Error(), "hpaName") {
		t.Fatalf("an invalid authored name was accepted outside a transform: %v", err)
	}
	// Nothing is claimed: a second trait resolving the same name is not refused.
	if _, err := (&Trait{Type: "scaler"}).ResolveName(hpaSpec("web-hpa")); err != nil {
		t.Fatalf("a second trait outside a transform was refused: %v", err)
	}
}
