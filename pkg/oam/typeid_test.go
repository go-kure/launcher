package oam

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateTypeIdentity_Accepts(t *testing.T) {
	for _, s := range []string{
		"cnpg-cluster.launcher.gokure.dev@v1",
		"webapp.acme.example@v1.1",
		"webapp.acme.example@v1.1.3",
		"deployment.apps@v1",
		"service@v1",
		"gateway.networking.k8s.io@v1beta1",
		"cert.cert-manager.io@v2alpha3",
		"app.1st.example@v0",
	} {
		id, err := ValidateTypeIdentity(s)
		if err != nil {
			t.Errorf("ValidateTypeIdentity(%q): %v", s, err)
			continue
		}
		if id.String() != s {
			t.Errorf("ValidateTypeIdentity(%q).String() = %q", s, id.String())
		}
	}
}

func TestValidateTypeIdentity_Refuses(t *testing.T) {
	for name, s := range map[string]string{
		"no version separator":           "webapp.acme.example",
		"empty version":                  "webapp.acme.example@",
		"empty name":                     "@v1",
		"empty":                          "",
		"uppercase name":                 "WebApp.acme.example@v1",
		"uppercase domain":               "webapp.Acme.example@v1",
		"uppercase version":              "webapp.acme.example@V1",
		"first label starts with digit":  "1app.acme.example@v1",
		"first label starts with dash":   "-app.acme.example@v1",
		"label ends with dash":           "app-.acme.example@v1",
		"empty label":                    "app..example@v1",
		"trailing dot":                   "app.example.@v1",
		"underscore":                     "my_app.acme.example@v1",
		"slash":                          "apps/deployment@v1",
		"two separators":                 "app.acme.example@v1@v2",
		"version without v":              "app.acme.example@1.2.3",
		"version not numeric":            "app.acme.example@vx",
		"four-part version":              "app.acme.example@v1.2.3.4",
		"leading zero":                   "app.acme.example@v01",
		"prerelease":                     "app.acme.example@v1.0.0-rc1",
		"api version without number":     "app.acme.example@v1beta",
		"api version on a launcher type": "cnpg-cluster.launcher.gokure.dev@v1beta1",
		"name too long":                  strings.Repeat("a", 254) + "@v1",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ValidateTypeIdentity(s)
			if !errors.Is(err, ErrTypeIdentityInvalid) {
				t.Fatalf("ValidateTypeIdentity(%q) error = %v, want ErrTypeIdentityInvalid", s, err)
			}
		})
	}
}

func mustIDs(t *testing.T, ss ...string) []TypeID {
	t.Helper()
	out := make([]TypeID, 0, len(ss))
	for _, s := range ss {
		id, err := ValidateTypeIdentity(s)
		if err != nil {
			t.Fatalf("fixture %q: %v", s, err)
		}
		out = append(out, id)
	}
	return out
}

func TestResolveTypeReference_Resolves(t *testing.T) {
	reg := mustIDs(t,
		"cnpg-cluster.launcher.gokure.dev@v1",
		"deployment.apps@v1",
		"service@v1",
		"webapp.acme.example@v1.0.0",
		"webapp.acme.example@v1.1.0",
		"webapp.acme.example@v2.0.0",
		"route.gateway.networking.k8s.io@v1beta1",
	)
	for name, tc := range map[string]struct{ ref, want string }{
		"short name, one version":                {"cnpg-cluster", "cnpg-cluster.launcher.gokure.dev@v1"},
		"short name is the kind":                 {"deployment", "deployment.apps@v1"},
		"core kind is its own name":              {"service", "service@v1"},
		"qualified, one version":                 {"deployment.apps", "deployment.apps@v1"},
		"qualified with version":                 {"cnpg-cluster.launcher.gokure.dev@v1", "cnpg-cluster.launcher.gokure.dev@v1"},
		"short name with a short version":        {"webapp@v1.1", "webapp.acme.example@v1.1.0"},
		"short version is exact":                 {"webapp@v2", "webapp.acme.example@v2.0.0"},
		"short version matches major.minor.0":    {"webapp@v2.0", "webapp.acme.example@v2.0.0"},
		"full version":                           {"webapp@v1.0.0", "webapp.acme.example@v1.0.0"},
		"short version of a one-version type":    {"cnpg-cluster@v1.0.0", "cnpg-cluster.launcher.gokure.dev@v1"},
		"api version, short name":                {"route@v1beta1", "route.gateway.networking.k8s.io@v1beta1"},
		"qualified name with a dotted domain":    {"route.gateway.networking.k8s.io", "route.gateway.networking.k8s.io@v1beta1"},
		"result carries the registered spelling": {"deployment@v1.0.0", "deployment.apps@v1"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ResolveTypeReference(tc.ref, reg)
			if err != nil {
				t.Fatalf("ResolveTypeReference(%q): %v", tc.ref, err)
			}
			if got.String() != tc.want {
				t.Fatalf("ResolveTypeReference(%q) = %s, want %s", tc.ref, got, tc.want)
			}
		})
	}
}

func TestResolveTypeReference_Refuses(t *testing.T) {
	reg := mustIDs(t,
		"cnpg-cluster.launcher.gokure.dev@v1",
		"webapp.acme.example@v1.0.0",
		"webapp.acme.example@v2.0.0",
		"thing.one.example@v1",
		"thing.two.example@v1",
	)
	for name, tc := range map[string]struct {
		ref  string
		want error
		in   string
	}{
		"omitted version, two registered":     {"webapp", ErrTypeReferenceAmbiguous, "v1.0.0, v2.0.0"},
		"qualified, omitted version, two":     {"webapp.acme.example", ErrTypeReferenceAmbiguous, "v1.0.0, v2.0.0"},
		"short name claimed by two types":     {"thing", ErrTypeReferenceAmbiguous, "thing.one.example, thing.two.example"},
		"short name claimed by two, version":  {"thing@v1", ErrTypeReferenceAmbiguous, "thing.one.example"},
		"unknown short name":                  {"nothing", ErrTypeReferenceUnknown, "nothing"},
		"unknown qualified name":              {"nothing.acme.example@v1", ErrTypeReferenceUnknown, "nothing"},
		"a short name is never a domain part": {"launcher", ErrTypeReferenceUnknown, "launcher"},
		"unregistered version":                {"webapp@v3", ErrTypeReferenceUnknown, "v1.0.0, v2.0.0"},
		"a short version is not a prefix":     {"webapp@v1.1", ErrTypeReferenceUnknown, "v1.0.0, v2.0.0"},
		"uppercase":                           {"WebApp@v1", ErrTypeIdentityInvalid, ""},
		"empty reference":                     {"", ErrTypeIdentityInvalid, ""},
		"empty version after @":               {"webapp@", ErrTypeIdentityInvalid, ""},
		"malformed version":                   {"webapp@1", ErrTypeIdentityInvalid, ""},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ResolveTypeReference(tc.ref, reg)
			if !errors.Is(err, tc.want) {
				t.Fatalf("ResolveTypeReference(%q) = %v, %v; want %v", tc.ref, got, err, tc.want)
			}
			if got != (TypeID{}) {
				t.Errorf("a refusal returned %v, want the zero TypeID", got)
			}
			if !strings.Contains(err.Error(), tc.in) {
				t.Errorf("error %q does not name %q", err, tc.in)
			}
		})
	}
}

func TestTypeRegistry_RegisterRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		first, second string
	}{
		"same name and version":                      {"webapp.acme.example@v1", "webapp.acme.example@v1"},
		"v2 and v2.0.0 are one version":              {"webapp.acme.example@v2", "webapp.acme.example@v2.0.0"},
		"short name equals another qualified name":   {"service@v1", "service.example.dev@v1"},
		"qualified name equals another short name":   {"service.example.dev@v1", "service@v1"},
		"a bare core kind, two versions of one name": {"service@v1", "service@v1.0"},
	} {
		t.Run(name, func(t *testing.T) {
			r := NewTypeRegistry()
			id1, err := ValidateTypeIdentity(tc.first)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Register(id1); err != nil {
				t.Fatalf("first registration: %v", err)
			}
			id2, err := ValidateTypeIdentity(tc.second)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Register(id2); !errors.Is(err, ErrTypeRegistration) {
				t.Fatalf("second registration error = %v, want ErrTypeRegistration", err)
			}
			if got := len(r.IDs()); got != 1 {
				t.Errorf("a refused registration left %d identities, want 1", got)
			}
		})
	}
}

func TestTypeRegistry_RegisterRefusesAnInvalidIdentity(t *testing.T) {
	for _, id := range []TypeID{{}, {Name: "webapp.acme.example"}, {Name: "Webapp.acme.example", Version: "v1"}, {Name: "webapp.acme.example", Version: "1"}} {
		if err := NewTypeRegistry().Register(id); !errors.Is(err, ErrTypeRegistration) || !errors.Is(err, ErrTypeIdentityInvalid) {
			t.Errorf("Register(%+v) = %v, want ErrTypeRegistration wrapping ErrTypeIdentityInvalid", id, err)
		}
	}
}

func TestResolveTypeReference_RefusesAConflictedSet(t *testing.T) {
	reg := mustIDs(t, "service@v1", "service.example.dev@v1")
	if _, err := ResolveTypeReference("service", reg); !errors.Is(err, ErrTypeRegistration) {
		t.Fatalf("a registered set with a short-name collision resolved: %v", err)
	}
}

func TestTypeRegistry_EveryVersionStaysRegistered(t *testing.T) {
	r := NewTypeRegistry()
	for _, id := range mustIDs(t, "webapp.acme.example@v2", "webapp.acme.example@v1", "webapp.acme.example@v1.10", "webapp.acme.example@v1.2") {
		if err := r.Register(id); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, id := range r.IDs() {
		got = append(got, id.Version)
	}
	if want := "v1 v1.2 v1.10 v2"; strings.Join(got, " ") != want {
		t.Fatalf("versions = %v, want %q (ordered numerically)", got, want)
	}
	for _, v := range []string{"v1", "v1.2", "v1.10", "v2"} {
		if _, err := r.Resolve("webapp@" + v); err != nil {
			t.Errorf("webapp@%s: %v", v, err)
		}
	}
}

func TestTypeID_ShortName(t *testing.T) {
	for id, want := range map[TypeID]string{
		{"cnpg-cluster.launcher.gokure.dev", "v1"}: "cnpg-cluster",
		{"deployment.apps", "v1"}:                  "deployment",
		{"service", "v1"}:                          "service",
	} {
		if got := id.ShortName(); got != want {
			t.Errorf("%s.ShortName() = %q, want %q", id, got, want)
		}
	}
}
