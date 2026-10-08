package kurel

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests pin the name of the Flux Kustomization of a chart component's own
// layout under per-layout placement (go-kure/launcher#787): the base library
// names it "<bundle>-<component>", shortened by its own rule where that is over
// 63 characters (go-kure/launcher#941), and launcher sets it only where the
// author or the Naming hook names it.

// layoutPlaced puts component db in tier infra and web in tier apps, which
// makes the application ordered.
const layoutPlaced = `  policies:
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

// layoutWeb is a webservice named web, the second member of an ordered
// application.
const layoutWeb = `    - name: web
      type: webservice
      properties:
        image: ghcr.io/example/web:v1.0.0
        port: 8080
`

// hasKustomization reports whether the tree under root has a Flux
// Kustomization named name, and lists the names it has.
func hasKustomization(t *testing.T, doc string, ctx oam.TransformContext, name string) (bool, []string) {
	t.Helper()
	all := treeKustomizations(mustHookGroupTree(t, doc, ctx))
	_, ok := all[name]
	var names []string
	for n := range all {
		names = append(names, n)
	}
	slices.Sort(names)
	return ok, names
}

// A bundle and component whose "<bundle>-<component>" is over 63 characters
// build, with the name shortened to 63 by the base library's rule
// (baseLibraryLayoutName); one at 61 keeps it whole.
func TestLayoutKustomizationName_Defaults(t *testing.T) {
	url := serveHookChart(t)
	a30, a32 := strings.Repeat("a", 30), strings.Repeat("a", 32)
	c30, c32 := strings.Repeat("c", 30), strings.Repeat("c", 32)
	for _, tc := range []struct {
		name string
		doc  string
		want string
		size int
	}{
		{
			name: "flat, 32 and 32",
			doc:  hookApp(a32, hookComponent(c32, "helmtemplate", url, ""), ""),
			want: baseLibraryLayoutName(a32, c32),
			size: 63,
		},
		{
			name: "flat, 32 and 32, helm under delivery: template",
			doc:  hookApp(a32, hookComponent(c32, "helm", url, "        delivery: template\n"), ""),
			want: baseLibraryLayoutName(a32, c32),
			size: 63,
		},
		{
			name: "ordered, 30 and 30 under the group bundle",
			doc:  hookApp(a30, strings.ReplaceAll(hookComponent("db", "helmtemplate", url, ""), "name: db", "name: "+c30)+layoutWeb, strings.ReplaceAll(layoutPlaced, "component: db", "component: "+c30)),
			want: baseLibraryLayoutName(a30+"-infra", c30),
			size: 63,
		},
		{
			name: "flat, 30 and 30, unchanged",
			doc:  hookApp(a30, hookComponent(c30, "helmtemplate", url, ""), ""),
			want: a30 + "-" + c30,
			size: 61,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.want) != tc.size {
				t.Fatalf("the expected name %q is %d characters, want %d", tc.want, len(tc.want), tc.size)
			}
			if ok, names := hasKustomization(t, tc.doc, oam.TransformContext{}, tc.want); !ok {
				t.Errorf("no Kustomization %q; the tree has %v", tc.want, names)
			}
		})
	}
}

// The name by its three sources: the author's property wins over the hook, the
// hook over the default. The hook is asked once, with the default, and not at
// all where the author wrote one.
func TestLayoutKustomizationName_Order(t *testing.T) {
	url := serveHookChart(t)
	const authored = "        layoutKustomizationName: mine\n"
	for _, tc := range []struct {
		name       string
		components string
		tail       string
		answer     string
		want       string
		wantAsked  *oam.NameRequest
	}{
		{name: "the default", components: hookComponent("db", "helmtemplate", url, ""), want: "shop-db",
			wantAsked: &oam.NameRequest{Application: "shop", Component: "db", Role: oam.NameRoleLayout, Default: "shop-db"}},
		{name: "the hook", components: hookComponent("db", "helmtemplate", url, ""), answer: "theirs", want: "theirs",
			wantAsked: &oam.NameRequest{Application: "shop", Component: "db", Role: oam.NameRoleLayout, Default: "shop-db"}},
		{name: "the hook, ordered", components: hookComponent("db", "helmtemplate", url, "") + layoutWeb, tail: layoutPlaced, answer: "theirs", want: "theirs",
			wantAsked: &oam.NameRequest{Application: "shop", Component: "db", Role: oam.NameRoleLayout, Default: "shop-infra-db"}},
		{name: "the author", components: hookComponent("db", "helmtemplate", url, authored), answer: "theirs", want: "mine"},
		{name: "the author of a helm component under delivery: template",
			components: hookComponent("db", "helm", url, "        delivery: template\n"+authored), answer: "theirs", want: "mine"},
		{name: "the hook, for a helm component under delivery: template",
			components: hookComponent("db", "helm", url, "        delivery: template\n"), answer: "theirs", want: "theirs",
			wantAsked: &oam.NameRequest{Application: "shop", Component: "db", Role: oam.NameRoleLayout, Default: "shop-db"}},
		{name: "the author, on a chart without hooks", components: hookComponent("db", "helmtemplate", serveChartWithHooks(t), authored), want: "mine"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var asked []oam.NameRequest
			ctx := oam.TransformContext{Naming: func(req oam.NameRequest) (string, bool) {
				if req.Role != oam.NameRoleLayout {
					return "", false
				}
				asked = append(asked, req)
				return tc.answer, tc.answer != ""
			}}
			if ok, names := hasKustomization(t, hookApp("shop", tc.components, tc.tail), ctx, tc.want); !ok {
				t.Errorf("no Kustomization %q; the tree has %v", tc.want, names)
			}
			var wantAsked []oam.NameRequest
			if tc.wantAsked != nil {
				wantAsked = []oam.NameRequest{*tc.wantAsked}
			}
			if !slices.Equal(asked, wantAsked) {
				t.Errorf("the hook was asked %+v, want %+v", asked, wantAsked)
			}
		})
	}
}

// An override is validated and never shortened; the refusal names the
// component and the role.
func TestLayoutKustomizationName_Refusals(t *testing.T) {
	url := serveHookChart(t)
	named := func(name string) string {
		return hookComponent("db", "helmtemplate", url, "        layoutKustomizationName: "+name+"\n")
	}
	long := strings.Repeat("k", 64)
	for _, tc := range []struct {
		name       string
		components string
		answer     string
		want       string
	}{
		{
			name:       "an authored name over 63 characters",
			components: named(long),
			want:       `component "db": layoutKustomizationName "` + long + `" cannot be the name for role "layout": it is 64 characters long, and a Flux Kustomization name is at most 63; write a valid name, or leave the property out for the default "shop-db"`,
		},
		{
			name:       "an authored name that is no subdomain",
			components: named("Bad_Name"),
			want:       `component "db": layoutKustomizationName "Bad_Name" cannot be the name for role "layout": not a valid DNS-1123 subdomain: `,
		},
		{
			name:       "an empty authored name",
			components: named(`""`),
			want:       `component "db": layoutKustomizationName "" cannot be the name for role "layout": it is empty; write a valid name, or leave the property out for the default "shop-db"`,
		},
		{
			name:       "a hook name over 63 characters",
			components: hookComponent("db", "helmtemplate", url, ""),
			answer:     long,
			want:       `component "db": the Naming hook returned "` + long + `" for role "layout" in place of "shop-db": it is 64 characters long, and a Flux Kustomization name is at most 63; return a valid name, or false to keep the default`,
		},
		{
			name:       "a hook name that is no subdomain",
			components: hookComponent("db", "helmtemplate", url, ""),
			answer:     "Bad_Name",
			want:       `component "db": the Naming hook returned "Bad_Name" for role "layout" in place of "shop-db": not a valid DNS-1123 subdomain: `,
		},
		{
			name:       "two components with one name",
			components: hookComponent("cache", "helmtemplate", url, "") + named("shop-cache"),
			want: `component "db": name collision: layout Kustomization "shop-cache" is named by ` +
				`component "cache" (role "layout", its default) and by ` +
				`component "db" (role "layout", set by layoutKustomizationName); give one of them another name`,
		},
		{
			name:       "on a helm component delivered as a HelmRelease",
			components: hookComponent("db", "helm", url, "        layoutKustomizationName: mine\n"),
			want:       `helm: layoutKustomizationName: names the Flux Kustomization of the layout of a chart rendered at build time, and under delivery: flux the chart is installed by a HelmRelease, so it names nothing; remove it, or set delivery: template`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := oam.TransformContext{Naming: func(req oam.NameRequest) (string, bool) {
				return tc.answer, tc.answer != "" && req.Role == oam.NameRoleLayout
			}}
			_, err := hookGroupTree(t, hookApp("shop", tc.components, ""), ctx)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v\nwant one containing %q", err, tc.want)
			}
		})
	}
}
