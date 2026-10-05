package kurel

import (
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"
	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/fluxcd"
	"github.com/go-kure/kure/pkg/stack/layout"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests pin the names of a helmtemplate component's hook-group layouts
// (go-kure/launcher#787) through kurel's own transformer and the base library's
// layout walker and Flux integrator under per-layout placement: the directory of
// each group, the Flux Kustomization generated for it, and the spec.dependsOn
// that chains the groups.

// serveHookChart serves a chart with three hook groups (pre-install, main,
// post-install) and returns the repository URL.
func serveHookChart(t *testing.T) string {
	t.Helper()
	cm := func(name, hook string) string {
		out := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}-" + name + "\n"
		if hook != "" {
			out += "  annotations:\n    helm.sh/hook: " + hook + "\n"
		}
		return out
	}
	chart := buildMinimalChartTar(t, "testchart", "0.1.0", map[string]string{
		"testchart/templates/pre.yaml":  cm("pre", "pre-install"),
		"testchart/templates/main.yaml": cm("main", ""),
		"testchart/templates/post.yaml": cm("post", "post-install"),
	})
	// The chart URL is derived from the request: the handler runs on the server's
	// goroutines.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.yaml":
			fmt.Fprint(w, helmIndexYAML("testchart", "0.1.0", "http://"+r.Host+"/testchart-0.1.0.tgz"))
		case "/testchart-0.1.0.tgz":
			_, _ = w.Write(chart)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// hookComponent is a component named name of componentType (helmtemplate, or
// helm, which extra then switches to delivery: template) on the chart at url.
// extra is appended to its properties, one "        key: value\n" line each.
func hookComponent(name, componentType, url, extra string) string {
	return fmt.Sprintf(`    - name: %s
      type: %s
      properties:
        chart: testchart
        version: "0.1.0"
        source:
          url: %s
%s`, name, componentType, url, extra)
}

// hookApp is a document named application holding components, and tail after
// them (policies).
func hookApp(application, components, tail string) string {
	return fmt.Sprintf(`apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: %s
  namespace: default
spec:
  components:
%s%s`, application, components, tail)
}

// hookGroupTree transforms doc under ctx, walks the result under per-layout
// placement and integrates Flux into the walked tree, as a consumer that writes
// one Kustomization per layout does.
func hookGroupTree(t *testing.T, doc string, ctx oam.TransformContext) (*layout.ManifestLayout, error) {
	t.Helper()
	transformer := newBuiltinTransformer()
	app, err := oam.ParseWithExtraTypes([]byte(doc), nil, transformer.LowerableTypes())
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if err := transformer.ValidateAuthoredProperties(app); err != nil {
		t.Fatalf("validating: %v", err)
	}
	ctx.Domain = kurelDomain
	cluster, err := transformer.Transform(app, ctx)
	if err != nil {
		return nil, err
	}
	// The Flux workflow writes a Kustomization only for a bundle with a source.
	var source func(b *stack.Bundle)
	source = func(b *stack.Bundle) {
		if b == nil {
			return
		}
		if len(b.Applications) > 0 {
			b.SourceRef = &stack.SourceRef{Kind: "OCIRepository", Name: "artifact", URL: "oci://registry.example/artifact", Tag: "v1"}
		}
		for _, child := range b.Children {
			source(child)
		}
	}
	var nodes func(n *stack.Node)
	nodes = func(n *stack.Node) {
		if n == nil {
			return
		}
		source(n.Bundle)
		for _, child := range n.Children {
			nodes(child)
		}
	}
	nodes(cluster.Node)

	rules := layout.DefaultLayoutRules()
	rules.FluxPlacement = layout.FluxIntegratedPerLayout
	root, err := layout.WalkCluster(cluster, rules)
	if err != nil {
		return nil, err
	}
	if err := fluxcd.NewWorkflowEngine().GetLayoutIntegrator().IntegrateWithLayout(root, cluster, rules); err != nil {
		return nil, err
	}
	return root, nil
}

// mustHookGroupTree is hookGroupTree for a document that builds.
func mustHookGroupTree(t *testing.T, doc string, ctx oam.TransformContext) *layout.ManifestLayout {
	t.Helper()
	root, err := hookGroupTree(t, doc, ctx)
	if err != nil {
		t.Fatalf("building the tree: %v", err)
	}
	return root
}

// treeKustomizations returns every Flux Kustomization in the tree under root,
// by name, with the names its spec.dependsOn lists.
func treeKustomizations(root *layout.ManifestLayout) map[string][]string {
	out := map[string][]string{}
	var walk func(ml *layout.ManifestLayout)
	walk = func(ml *layout.ManifestLayout) {
		for _, o := range ml.Resources {
			kz, ok := o.(*kustv1.Kustomization)
			if !ok {
				continue
			}
			var deps []string
			for _, dep := range kz.Spec.DependsOn {
				deps = append(deps, dep.Name)
			}
			out[kz.Name] = deps
		}
		for _, child := range ml.Children {
			walk(child)
		}
	}
	walk(root)
	return out
}

// hookGroupDirs returns the names of the hook-group layouts of component: the
// children of the layout named after it.
func hookGroupDirs(t *testing.T, root *layout.ManifestLayout, component string) []string {
	t.Helper()
	var found *layout.ManifestLayout
	var walk func(ml *layout.ManifestLayout)
	walk = func(ml *layout.ManifestLayout) {
		if ml.Name == component && len(ml.Children) > 0 {
			found = ml
		}
		for _, child := range ml.Children {
			walk(child)
		}
	}
	walk(root)
	if found == nil {
		t.Fatalf("the tree has no layout for component %q with children", component)
	}
	names := make([]string, len(found.Children))
	for i, child := range found.Children {
		names[i] = child.Name
	}
	return names
}

// hookGroupsOnly keeps the Kustomizations whose name ends in a hook-group
// suffix of the test chart.
func hookGroupsOnly(all map[string][]string) map[string][]string {
	out := map[string][]string{}
	for name, deps := range all {
		for _, suffix := range hookChartSuffixes {
			if strings.HasSuffix(name, suffix) {
				out[name] = deps
			}
		}
	}
	return out
}

var hookChartSuffixes = []string{"-00-pre-install", "-01-main", "-02-post-install"}

// The default names, as the README states them: for a flat application the
// component's Kustomization is "<bundle>-<component>" and its hook groups are
// "<application>-<component>-<NN>-<phase>"; in an ordered application the
// component's carries its group's bundle and the hook groups' do not.
func TestHookGroupNames_Defaults(t *testing.T) {
	url := serveHookChart(t)
	const placed = `  policies:
    - name: db-first
      type: placement
      properties:
        component: db
        tier: infra
    - name: web-last
      type: placement
      properties:
        component: web
        tier: apps
`
	const web = `    - name: web
      type: webservice
      properties:
        image: ghcr.io/example/web:v1.0.0
        port: 8080
`
	for _, tc := range []struct {
		name string
		doc  string
		want map[string][]string
	}{
		{
			name: "flat",
			doc:  hookApp("shop", hookComponent("db", "helmtemplate", url, ""), ""),
			want: map[string][]string{
				"shop":                    nil,
				"shop-db":                 nil,
				"shop-db-00-pre-install":  nil,
				"shop-db-01-main":         {"shop-db-00-pre-install"},
				"shop-db-02-post-install": {"shop-db-01-main"},
			},
		},
		{
			name: "ordered",
			doc:  hookApp("shop", hookComponent("db", "helmtemplate", url, "")+web, placed),
			want: map[string][]string{
				"shop":                    nil,
				"shop-infra":              nil,
				"shop-infra-db":           nil,
				"shop-db-00-pre-install":  nil,
				"shop-db-01-main":         {"shop-db-00-pre-install"},
				"shop-db-02-post-install": {"shop-db-01-main"},
				"shop-apps":               {"shop-infra"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := mustHookGroupTree(t, tc.doc, oam.TransformContext{})
			got := treeKustomizations(root)
			// The webservice members have no directory of their own under the default
			// grouping, so every Kustomization of the tree is listed.
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Kustomizations (name: dependsOn) = %v\nwant %v", got, tc.want)
			}
			if got, want := hookGroupDirs(t, root, "db"), []string{"shop-db-00-pre-install", "shop-db-01-main", "shop-db-02-post-install"}; !slices.Equal(got, want) {
				t.Errorf("hook-group directories = %v, want %v", got, want)
			}
		})
	}
}

// A long application and component name: each hook group's Kustomization name
// is shortened to 63 characters by the one rule with its suffix whole, the
// names differ per group, spec.dependsOn follows them, and the directories keep
// the whole name.
func TestHookGroupNames_LongDefaultsAreShortenedTo63(t *testing.T) {
	url := serveHookChart(t)
	application, component := strings.Repeat("a", 30), strings.Repeat("c", 30)
	root := mustHookGroupTree(t, hookApp(application, hookComponent(component, "helmtemplate", url, ""), ""), oam.TransformContext{})

	prefix := application + "-" + component
	var wantDirs, wantNames []string
	for _, suffix := range hookChartSuffixes {
		wantDirs = append(wantDirs, prefix+suffix)
		name := oam.ShortenNameWithSuffix(prefix, suffix, oam.ShortenLimitLabel)
		if len(name) > 63 || !strings.HasSuffix(name, suffix) || name == prefix+suffix {
			t.Fatalf("the expected name %q is not a shortened name of at most 63 characters ending in %q", name, suffix)
		}
		wantNames = append(wantNames, name)
	}
	if distinct := slices.Compact(slices.Sorted(slices.Values(wantNames))); len(distinct) != len(hookChartSuffixes) {
		t.Fatalf("the shortened names are not all different: %v", wantNames)
	}
	want := map[string][]string{
		wantNames[0]: nil,
		wantNames[1]: {wantNames[0]},
		wantNames[2]: {wantNames[1]},
	}
	if got := hookGroupsOnly(treeKustomizations(root)); !reflect.DeepEqual(got, want) {
		t.Errorf("hook-group Kustomizations (name: dependsOn) = %v\nwant %v", got, want)
	}
	if got := hookGroupDirs(t, root, component); !slices.Equal(got, wantDirs) {
		t.Errorf("hook-group directories = %v, want the whole names %v", got, wantDirs)
	}
}

// The prefix by its three sources. An authored prefix and the hook's answer
// name directory and Kustomization alike; the hook is asked once per component,
// with the default prefix, and not at all where the author wrote one.
func TestHookGroupNames_PrefixOrder(t *testing.T) {
	url := serveHookChart(t)
	const authored = "        hookGroupNamePrefix: mine\n"
	want := func(prefix string) map[string][]string {
		return map[string][]string{
			prefix + "-00-pre-install":  nil,
			prefix + "-01-main":         {prefix + "-00-pre-install"},
			prefix + "-02-post-install": {prefix + "-01-main"},
		}
	}
	for _, tc := range []struct {
		name       string
		components string
		answer     string // the hook's, "" to decline
		wantPrefix string
		wantAsked  bool
	}{
		{name: "the default", components: hookComponent("db", "helmtemplate", url, ""), wantPrefix: "shop-db", wantAsked: true},
		{name: "the hook", components: hookComponent("db", "helmtemplate", url, ""), answer: "theirs", wantPrefix: "theirs", wantAsked: true},
		{name: "the author", components: hookComponent("db", "helmtemplate", url, authored), answer: "theirs", wantPrefix: "mine"},
		{name: "the author of a helm component under delivery: template",
			components: hookComponent("db", "helm", url, "        delivery: template\n"+authored), answer: "theirs", wantPrefix: "mine"},
		{name: "the hook, for a helm component under delivery: template",
			components: hookComponent("db", "helm", url, "        delivery: template\n"), answer: "theirs", wantPrefix: "theirs", wantAsked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var asked []oam.NameRequest
			ctx := oam.TransformContext{Naming: func(req oam.NameRequest) (string, bool) {
				if req.Role != oam.NameRoleHookGroup {
					return "", false
				}
				asked = append(asked, req)
				return tc.answer, tc.answer != ""
			}}
			root := mustHookGroupTree(t, hookApp("shop", tc.components, ""), ctx)
			if got := hookGroupsOnly(treeKustomizations(root)); !reflect.DeepEqual(got, want(tc.wantPrefix)) {
				t.Errorf("hook-group Kustomizations (name: dependsOn) = %v\nwant %v", got, want(tc.wantPrefix))
			}
			wantDirs := slices.Sorted(maps.Keys(want(tc.wantPrefix)))
			if got := hookGroupDirs(t, root, "db"); !slices.Equal(got, wantDirs) {
				t.Errorf("hook-group directories = %v, want %v", got, wantDirs)
			}
			var wantAsked []oam.NameRequest
			if tc.wantAsked {
				wantAsked = []oam.NameRequest{{Application: "shop", Component: "db", Role: oam.NameRoleHookGroup, Default: "shop-db"}}
			}
			if !slices.Equal(asked, wantAsked) {
				t.Errorf("the hook was asked %+v, want %+v", asked, wantAsked)
			}
		})
	}
}

func TestHookGroupNames_Refusals(t *testing.T) {
	url := serveHookChart(t)
	prefixed := func(prefix string) string {
		return hookComponent("db", "helmtemplate", url, "        hookGroupNamePrefix: "+prefix+"\n")
	}
	// 48 characters: with the 15 of "-00-pre-install" the first name is 63, and
	// with the 16 of "-02-post-install" the last is 64.
	long := strings.Repeat("p", 48)
	overLimit := `helmtemplate: component "db": hook-group name "` + long + `-02-post-install" (role "hook-group") is 64 characters, and a Flux Kustomization name has at most 63; ` +
		`its prefix "` + long + `" was set by hookGroupNamePrefix or returned by the Naming hook and is never shortened: use a prefix of at most 47 characters, or none for the default, which is shortened`
	for _, tc := range []struct {
		name       string
		components string
		answer     string
		want       string
	}{
		{
			name:       "an authored prefix that makes a name over 63 characters",
			components: prefixed(long),
			want:       overLimit,
		},
		{
			name:       "a hook prefix that makes a name over 63 characters",
			components: hookComponent("db", "helmtemplate", url, ""),
			answer:     long,
			want:       overLimit,
		},
		{
			name:       "an authored prefix that is no subdomain",
			components: prefixed("Bad_Prefix"),
			want:       `component "db": hookGroupNamePrefix "Bad_Prefix" cannot be the name for role "hook-group": not a valid DNS-1123 subdomain: `,
		},
		{
			name:       "an empty authored prefix",
			components: prefixed(`""`),
			want:       `component "db": hookGroupNamePrefix "" cannot be the name for role "hook-group": it is empty; write a valid name, or leave the property out for the default "shop-db"`,
		},
		{
			name:       "a hook prefix that is no subdomain",
			components: hookComponent("db", "helmtemplate", url, ""),
			answer:     "Bad_Prefix",
			want:       `component "db": the Naming hook returned "Bad_Prefix" for role "hook-group" in place of "shop-db": not a valid DNS-1123 subdomain: `,
		},
		{
			name:       "two components with one prefix",
			components: hookComponent("cache", "helmtemplate", url, "") + prefixed("shop-cache"),
			want: `component "db": name collision: hook-group name prefix "shop-cache" is named by ` +
				`component "cache" (role "hook-group", its default) and by ` +
				`component "db" (role "hook-group", set by hookGroupNamePrefix); give one of them another name`,
		},
		{
			name:       "the hook gives two components one prefix",
			components: hookComponent("cache", "helmtemplate", url, "") + hookComponent("db", "helmtemplate", url, ""),
			answer:     "shared",
			want: `component "db": name collision: hook-group name prefix "shared" is named by ` +
				`component "cache" (role "hook-group", returned by the Naming hook in place of "shop-cache") and by ` +
				`component "db" (role "hook-group", returned by the Naming hook in place of "shop-db"); give one of them another name`,
		},
		{
			name:       "on a helm component delivered as a HelmRelease",
			components: hookComponent("db", "helm", url, "        hookGroupNamePrefix: mine\n"),
			want:       `helm: hookGroupNamePrefix: names the hook-group layouts of a chart rendered at build time, and under delivery: flux the chart is installed by a HelmRelease, so it names nothing; remove it, or set delivery: template`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := oam.TransformContext{Naming: func(req oam.NameRequest) (string, bool) {
				return tc.answer, tc.answer != "" && req.Role == oam.NameRoleHookGroup
			}}
			_, err := hookGroupTree(t, hookApp("shop", tc.components, ""), ctx)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v\nwant one containing %q", err, tc.want)
			}
		})
	}
}
