package components

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack/helm"
	"github.com/go-kure/kure/pkg/stack/layout"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// TestHookGroupNaming_ChildNames: with no prefix the directory keeps the whole
// default name and the Kustomization name is shortened to 63 characters; a
// prefix names both as written. A prefix that makes a name too long is refused
// for the longest child name, whichever group that is, so the prefix length the
// message asks for fits every child.
func TestHookGroupNaming_ChildNames(t *testing.T) {
	suffixes := []string{"-00-pre-install", "-01-main", "-02-post-install", "-03-test"}

	t.Run("the default", func(t *testing.T) {
		long := strings.Repeat("c", 60)
		got, err := hookGroupNaming{component: long, application: "shop"}.childNames(long, suffixes)
		if err != nil {
			t.Fatalf("childNames: %v", err)
		}
		for i, suffix := range suffixes {
			if want := "shop-" + long + suffix; got[i].dir != want {
				t.Errorf("child %d: directory %q, want %q", i, got[i].dir, want)
			}
			want := oam.ShortenNameWithSuffix("shop-"+long, suffix, 63)
			if got[i].kustomization != want || len(want) > 63 || !strings.HasSuffix(want, suffix) {
				t.Errorf("child %d: Kustomization name %q, want %q: at most 63 characters and ending in %q", i, got[i].kustomization, want, suffix)
			}
		}
		short, err := hookGroupNaming{component: "db", application: "shop"}.childNames("db", suffixes)
		if err != nil {
			t.Fatalf("childNames: %v", err)
		}
		if want := (hookGroupChildNames{dir: "shop-db-01-main", kustomization: "shop-db-01-main"}); short[1] != want {
			t.Errorf("a name that fits: %+v, want %+v", short[1], want)
		}
		direct, err := hookGroupNaming{component: "db"}.childNames("db", suffixes)
		if err != nil {
			t.Fatalf("childNames: %v", err)
		}
		if want := (hookGroupChildNames{dir: "db-01-main", kustomization: "db-01-main"}); direct[1] != want {
			t.Errorf("no application: %+v, want %+v", direct[1], want)
		}
	})

	t.Run("a prefix", func(t *testing.T) {
		// 47 characters: the longest child name is exactly 63.
		fits := strings.Repeat("p", 47)
		got, err := hookGroupNaming{component: "db", application: "shop", prefix: fits}.childNames("db", suffixes)
		if err != nil {
			t.Fatalf("childNames: %v", err)
		}
		for i, suffix := range suffixes {
			if want := (hookGroupChildNames{dir: fits + suffix, kustomization: fits + suffix}); got[i] != want {
				t.Errorf("child %d: %+v, want %+v", i, got[i], want)
			}
		}

		// 49 characters: the first child's name is 64 already, and the third's,
		// the longest, is 65.
		over := strings.Repeat("p", 49)
		_, err = hookGroupNaming{component: "db", application: "shop", prefix: over}.childNames("db", suffixes)
		want := `helmtemplate: component "db": hook-group name "` + over + `-02-post-install" (role "hook-group") is 65 characters, and a Flux Kustomization name has at most 63; ` +
			`its prefix "` + over + `" was set by hookGroupNamePrefix or returned by the Naming hook and is never shortened: use a prefix of at most 47 characters, or none for the default, which is shortened`
		if err == nil || err.Error() != want {
			t.Errorf("err = %v\nwant %s", err, want)
		}
		// The same refusal, recognisable (go-kure/launcher#787).
		wantRefusal := oam.HookGroupNameError{
			ComponentType: "helmtemplate", Component: "db", Role: oam.NameRoleHookGroup,
			Name: over + "-02-post-install", Length: 65, Limit: 63, Prefix: over,
		}
		var refusal *oam.HookGroupNameError
		if !errors.As(err, &refusal) || *refusal != wantRefusal {
			t.Errorf("errors.As found %+v, want %+v", refusal, wantRefusal)
		}
		if !errors.Is(err, oam.ErrHookGroupNameTooLong) {
			t.Error("errors.Is(err, oam.ErrHookGroupNameTooLong) = false")
		}

		_, err = hookGroupNaming{component: "db", prefix: "Bad_Prefix"}.childNames("db", suffixes)
		if want := `helmtemplate: component "db": hook-group name "Bad_Prefix-00-pre-install" (role "hook-group") is not a valid DNS-1123 subdomain: `; err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("err = %v\nwant one beginning %q", err, want)
		}
		// Another refusal of a hook-group name is not this one.
		if errors.Is(err, oam.ErrHookGroupNameTooLong) {
			t.Error("the refusal of a name that is no subdomain answers to oam.ErrHookGroupNameTooLong")
		}
	})
}

// TestChartRender_ChildSuffixesAreWhatPartitionNames: the names the check reads
// (childSuffixes, from the render) are the names partition gives its children
// (created from the copy Generate handed out), one for one and in order: before
// any Generate, after one and after a second. A test hook's group, which the
// render drops, is in neither list.
func TestChartRender_ChildSuffixesAreWhatPartitionNames(t *testing.T) {
	const withTestHook = helmTemplateThreeGroupChart + `---
apiVersion: v1
kind: ConfigMap
metadata:
  name: probe
  annotations:
    helm.sh/hook: test
`
	want := []string{"-00-pre-install", "-01-main", "-02-post-install"}
	for name, generates := range map[string]int{"no Generate": 0, "one Generate": 1, "two Generates": 2} {
		t.Run(name, func(t *testing.T) {
			cfg := helmTemplateFixture(t, stubRender(withTestHook))
			if err := cfg.ApplyPolicy(&oam.NoopPolicy{}); err != nil {
				t.Fatalf("ApplyPolicy: %v", err)
			}
			for range generates {
				if _, err := cfg.Generate(nil); err != nil {
					t.Fatalf("Generate: %v", err)
				}
			}
			suffixes := cfg.childSuffixes()
			if strings.Join(suffixes, " ") != strings.Join(want, " ") {
				t.Fatalf("childSuffixes = %q, want %q", suffixes, want)
			}
			if generates > 0 {
				if len(cfg.emitted) != len(cfg.hookGroups) {
					t.Fatalf("Generate handed out %d group(s) of the render's %d", len(cfg.emitted), len(cfg.hookGroups))
				}
				for i, g := range cfg.emitted {
					if got := hookGroupSuffix(i, g); got != suffixes[i] || len(g.Resources) != len(cfg.hookGroups[i].Resources) {
						t.Errorf("group %d handed out: suffix %q with %d object(s), want %q with %d", i, got, len(g.Resources), suffixes[i], len(cfg.hookGroups[i].Resources))
					}
				}
			}
			ml := &layout.ManifestLayout{Name: "myapp", Namespace: "apps"}
			if err := cfg.AugmentLayout(ml); err != nil {
				t.Fatalf("AugmentLayout: %v", err)
			}
			if len(ml.Children) != len(suffixes) {
				t.Fatalf("%d child layout(s), want %d", len(ml.Children), len(suffixes))
			}
			for i, child := range ml.Children {
				if !strings.HasSuffix(child.Name, suffixes[i]) || !strings.HasSuffix(child.KustomizationName, suffixes[i]) || len(child.Resources) != 1 {
					t.Errorf("child %d: name %q, Kustomization name %q, %d object(s); want both ending in %q and one object", i, child.Name, child.KustomizationName, len(child.Resources), suffixes[i])
				}
			}
			if got, want := resourceNames(layoutResources(ml)), "pre main post"; strings.Join(got, " ") != want {
				t.Errorf("the children hold %q, want %q: the test hook is in no group", got, want)
			}
		})
	}
}

// layoutResources returns the objects of ml's children, in order.
func layoutResources(ml *layout.ManifestLayout) []client.Object {
	var objs []client.Object
	for _, child := range ml.Children {
		objs = append(objs, child.Resources...)
	}
	return objs
}

// TestHelmTemplateConfig_CheckHookGroupNames: the config says whether a
// hook-group name built from its prefix is refused as soon as its chart is
// rendered, and not before: it renders nothing itself. AugmentLayout returns the
// same refusal, which is what a config no transform checked meets: one built
// directly, or one whose prefix was set after the check.
func TestHelmTemplateConfig_CheckHookGroupNames(t *testing.T) {
	// 48 characters: with "-02-post-install" the longest name is 64.
	over := strings.Repeat("p", 48)
	wantRefusal := oam.HookGroupNameError{
		ComponentType: "helmtemplate", Component: "myapp", Role: oam.NameRoleHookGroup,
		Name: over + "-02-post-install", Length: 64, Limit: 63, Prefix: over,
	}
	isRefusal := func(t *testing.T, step string, err error) {
		t.Helper()
		var refusal *oam.HookGroupNameError
		if !errors.As(err, &refusal) || *refusal != wantRefusal {
			t.Errorf("%s: err = %v\nwant the refusal %+v", step, err, wantRefusal)
		}
	}

	t.Run("once rendered", func(t *testing.T) {
		renders := 0
		cfg := helmTemplateFixture(t, func(string, string, map[string]any, ...helm.RenderOption) ([]byte, error) {
			renders++
			return []byte(helmTemplateThreeGroupChart), nil
		})
		cfg.HookGroupNamePrefix = over
		if err := cfg.CheckHookGroupNames(); err != nil || renders != 0 {
			t.Fatalf("before any render: err = %v after %d render(s), want nil and none", err, renders)
		}
		// A nil policy checks nothing and renders nothing, so nothing is known yet.
		if err := cfg.ApplyPolicy(nil); err != nil {
			t.Fatalf("ApplyPolicy(nil): %v", err)
		}
		if err := cfg.CheckHookGroupNames(); err != nil || renders != 0 {
			t.Fatalf("after a nil policy: err = %v after %d render(s), want nil and none", err, renders)
		}
		// The transform's policy is never nil, and the config renders for it.
		if err := cfg.ApplyPolicy(&oam.NoopPolicy{}); err != nil {
			t.Fatalf("ApplyPolicy: %v", err)
		}
		isRefusal(t, "after the policy rendered the chart", cfg.CheckHookGroupNames())
		ml := &layout.ManifestLayout{Name: "myapp", Namespace: "apps"}
		isRefusal(t, "AugmentLayout", cfg.AugmentLayout(ml))
		if len(ml.Children) != 0 {
			t.Errorf("the refused layout was given %d child(ren)", len(ml.Children))
		}
		if renders != 1 {
			t.Errorf("the chart was rendered %d times, want once", renders)
		}
	})

	t.Run("a prefix set after the check is refused by AugmentLayout", func(t *testing.T) {
		cfg := helmTemplateFixture(t, stubRender(helmTemplateThreeGroupChart))
		if err := cfg.ApplyPolicy(&oam.NoopPolicy{}); err != nil {
			t.Fatalf("ApplyPolicy: %v", err)
		}
		if err := cfg.CheckHookGroupNames(); err != nil {
			t.Fatalf("the default prefix: %v", err)
		}
		cfg.SetHookGroupNamePrefix(over)
		isRefusal(t, "AugmentLayout", cfg.AugmentLayout(&layout.ManifestLayout{Name: "myapp", Namespace: "apps"}))
	})

	t.Run("a config built directly is refused by AugmentLayout", func(t *testing.T) {
		cfg := &HelmTemplateConfig{
			Name: "myapp", SourceURL: "https://charts.example.com", Chart: "myapp",
			HookGroupNamePrefix: over, renderChart: stubRender(helmTemplateThreeGroupChart),
		}
		isRefusal(t, "AugmentLayout", cfg.AugmentLayout(&layout.ManifestLayout{Name: "myapp", Namespace: "apps"}))
	})

	t.Run("nothing to refuse", func(t *testing.T) {
		// A chart with one group has no hook-group layout, so the prefix names
		// nothing; and the default prefix is shortened, never refused.
		const oneGroup = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: main\n"
		for name, cfg := range map[string]*HelmTemplateConfig{
			"one group":          helmTemplateFixture(t, stubRender(oneGroup)),
			"the default prefix": helmTemplateFixture(t, stubRender(helmTemplateThreeGroupChart)),
			"a prefix that fits": helmTemplateFixture(t, stubRender(helmTemplateThreeGroupChart)),
		} {
			switch name {
			case "one group":
				cfg.HookGroupNamePrefix = over
			case "the default prefix":
				cfg.Application = strings.Repeat("a", 70)
			case "a prefix that fits":
				cfg.HookGroupNamePrefix = over[:47]
			}
			if err := cfg.ApplyPolicy(&oam.NoopPolicy{}); err != nil {
				t.Fatalf("%s: ApplyPolicy: %v", name, err)
			}
			if err := cfg.CheckHookGroupNames(); err != nil {
				t.Errorf("%s: CheckHookGroupNames: %v", name, err)
			}
			if err := cfg.AugmentLayout(&layout.ManifestLayout{Name: "myapp", Namespace: "apps"}); err != nil {
				t.Errorf("%s: AugmentLayout: %v", name, err)
			}
		}
	})
}
