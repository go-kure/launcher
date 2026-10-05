package kurel

import (
	"slices"
	"strings"
	"testing"

	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests pin the names of the two objects an oci component names after
// itself (go-kure/launcher#787) through kurel's own transformer: the
// Kustomization, and the OCIRepository the component keeps to itself. Each is
// named by the author (kustomizationName, source.objectName), else by the Naming
// hook (roles oci-kustomization, oci-source), else after the component, and what
// the rule writes as a reference to the source follows the name it got.

const (
	ociKustomizationKindName = "Kustomization.kustomize.toolkit.fluxcd.io"
	ociRepositoryKindName    = "OCIRepository.source.toolkit.fluxcd.io"
)

// ociNamesComponent is an oci component on artifact; source is appended under
// its `source` and props under its properties, each a block of YAML lines at
// the right indent.
func ociNamesComponent(name, artifact, source, props string) string {
	return `    - name: ` + name + `
      type: oci
      properties:
        version: 1.4.0
        source:
          url: oci://registry.example.com/org/` + artifact + `
` + source + props
}

// ociKustomizationRefs lists what each generated Kustomization reads, as
// "<application>: Kustomization/<name>-><kind>/<name>", in order.
func ociKustomizationRefs(apps []oam.GeneratedApplication) []string {
	var out []string
	for _, a := range apps {
		for _, p := range a.Objects {
			if p == nil {
				continue
			}
			if k, ok := (*p).(*kustv1.Kustomization); ok {
				out = append(out, a.Name+": Kustomization/"+k.Name+"->"+k.Spec.SourceRef.Kind+"/"+k.Spec.SourceRef.Name)
			}
		}
	}
	return out
}

// ociRoleRequests keeps the requests of the two oci roles.
func ociRoleRequests(requests []oam.NameRequest) []oam.NameRequest {
	var out []oam.NameRequest
	for _, req := range requests {
		if req.Role == oam.NameRoleOCIKustomization || req.Role == oam.NameRoleOCISource {
			out = append(out, req)
		}
	}
	return out
}

// ociNamesDoc holds a component that keeps its source (manifests) and two that
// share one (base, addons), manifests placed in the tier ahead of the others'.
func ociNamesDoc(manifestsSource, manifestsProps, baseProps string) string {
	return helmNamesApp(
		ociNamesComponent("manifests", "manifests", manifestsSource, manifestsProps)+
			ociNamesComponent("base", "platform", "", baseProps)+
			ociNamesComponent("addons", "platform", "", "")) + `  policies:
    - name: manifests-first
      type: placement
      properties:
        component: manifests
        tier: infra
    - name: base-after
      type: placement
      properties:
        component: base
        tier: apps
    - name: addons-after
      type: placement
      properties:
        component: addons
        tier: apps
`
}

// A hook that declines every name changes nothing, and is asked for each of the
// names with the component that owns it: the source a component keeps and its
// Kustomization, and the Kustomization of each component that shares a source.
// The shared source is the document's (role helm-source), as before.
func TestOCINames_DecliningHookChangesNothing(t *testing.T) {
	doc := ociNamesDoc("", "", "")
	cluster, apps := namingTransform(t, doc, oam.TransformContext{})
	without, withoutRefs := generatedNames(cluster, apps), ociKustomizationRefs(apps)
	shared := ociSharedSourceName("1h0m0s")
	wantRefs := []string{
		"manifests: Kustomization/manifests->OCIRepository/manifests",
		"base: Kustomization/base->OCIRepository/" + shared,
		"addons: Kustomization/addons->OCIRepository/" + shared,
	}
	if got := slices.Clone(withoutRefs); !sameSet(got, wantRefs) {
		t.Errorf("without a hook the Kustomizations read %v, want %v", withoutRefs, wantRefs)
	}

	var requests []oam.NameRequest
	cluster, apps = namingTransform(t, doc, oam.TransformContext{Naming: declineEveryName(&requests)})
	if with := generatedNames(cluster, apps); !slices.Equal(without, with) {
		t.Errorf("a hook that declines every name changed the output:\nwithout: %s\nwith:    %s",
			strings.Join(without, "\n         "), strings.Join(with, "\n         "))
	}
	if withRefs := ociKustomizationRefs(apps); !slices.Equal(withoutRefs, withRefs) {
		t.Errorf("a hook that declines every name changed the source references: %v, want %v", withRefs, withoutRefs)
	}

	want := []oam.NameRequest{
		{Application: "shop", Component: "manifests", Role: oam.NameRoleOCISource, Kind: ociRepositoryKindName, Default: "manifests"},
		{Application: "shop", Component: "manifests", Role: oam.NameRoleOCIKustomization, Kind: ociKustomizationKindName, Default: "manifests"},
		{Application: "shop", Component: "base", Role: oam.NameRoleOCIKustomization, Kind: ociKustomizationKindName, Default: "base"},
		{Application: "shop", Component: "addons", Role: oam.NameRoleOCIKustomization, Kind: ociKustomizationKindName, Default: "addons"},
	}
	if got := ociRoleRequests(requests); !slices.Equal(got, want) {
		t.Errorf("the hook was asked\n  %+v\nwant\n  %+v", got, want)
	}
}

// sameSet reports whether a and b hold the same strings, in any order.
func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// The hook names the kept source and each Kustomization. The objects take the
// names, the Kustomization reads its source by the name the source got, and
// nothing else moves: every member keeps its component's name, so the pair
// stays one unit in the tier its component is placed in, and the Kustomizations
// of the sharing components stay ordered after the document's source.
func TestOCINames_HookAnswerIsUsed(t *testing.T) {
	doc := ociNamesDoc("", "", "")
	hook := renameBy(map[string]string{
		"oci-source manifests":        "manifests-artifact",
		"oci-kustomization manifests": "manifests-delivery",
		"oci-kustomization base":      "platform-base",
	})
	cluster, apps := namingTransform(t, doc, oam.TransformContext{Naming: hook})
	shared := ociSharedSourceName("1h0m0s")

	got := generatedNames(cluster, apps)
	for _, line := range []string{
		"manifests: OCIRepository default/manifests-artifact",
		"manifests: Kustomization default/manifests-delivery",
		"base: Kustomization default/platform-base",
		"addons: Kustomization default/addons",
		shared + ": OCIRepository default/" + shared,
	} {
		if !slices.Contains(got, line) {
			t.Errorf("missing %q in\n  %s", line, strings.Join(got, "\n  "))
		}
	}
	wantRefs := []string{
		"manifests: Kustomization/manifests-delivery->OCIRepository/manifests-artifact",
		"base: Kustomization/platform-base->OCIRepository/" + shared,
		"addons: Kustomization/addons->OCIRepository/" + shared,
	}
	if refs := ociKustomizationRefs(apps); !sameSet(refs, wantRefs) {
		t.Errorf("the Kustomizations read %v, want %v", refs, wantRefs)
	}

	// The same tree as without a hook.
	plain, _ := namingTransform(t, doc, oam.TransformContext{})
	assertSourcesInApplicationBundle(t, cluster, shared)
	wantGroups := []string{"shop-infra: manifests", "shop-apps: base addons"}
	if got, plainGroups := groupNames(t, cluster), groupNames(t, plain); !slices.Equal(got, wantGroups) || !slices.Equal(plainGroups, wantGroups) {
		t.Errorf("groups = %v, and %v without a hook; want %v for both", got, plainGroups, wantGroups)
	}
	if got, want := ociObjectsOf(t, cluster.Node.Bundle.Children[0]), []string{"OCIRepository/manifests-artifact", "Kustomization/manifests-delivery"}; !slices.Equal(got, want) {
		t.Errorf("the first group holds %v, want the renamed pair %v", got, want)
	}
}

// An authored name is used as written and the hook is not asked for it, through
// the whole build: kurel writes the objects under the authored names, the
// Kustomization reading the source by its own.
func TestOCINames_AuthoredWinsAndHookIsNotAsked(t *testing.T) {
	doc := ociNamesDoc("          objectName: manifests-artifact\n", "        kustomizationName: manifests-delivery\n", "        kustomizationName: platform-base\n")
	shared := ociSharedSourceName("1h0m0s")

	var requests []oam.NameRequest
	hook := func(req oam.NameRequest) (string, bool) {
		requests = append(requests, req)
		return "theirs", req.Role == oam.NameRoleOCIKustomization && req.Component != "addons" || req.Role == oam.NameRoleOCISource
	}
	_, apps := namingTransform(t, doc, oam.TransformContext{Naming: hook})
	wantAsked := []oam.NameRequest{
		{Application: "shop", Component: "addons", Role: oam.NameRoleOCIKustomization, Kind: ociKustomizationKindName, Default: "addons"},
	}
	if got := ociRoleRequests(requests); !slices.Equal(got, wantAsked) {
		t.Errorf("the hook was asked\n  %+v\nwant only the name no author set:\n  %+v", got, wantAsked)
	}
	wantRefs := []string{
		"manifests: Kustomization/manifests-delivery->OCIRepository/manifests-artifact",
		"base: Kustomization/platform-base->OCIRepository/" + shared,
		"addons: Kustomization/addons->OCIRepository/" + shared,
	}
	if refs := ociKustomizationRefs(apps); !sameSet(refs, wantRefs) {
		t.Errorf("the Kustomizations read %v, want %v", refs, wantRefs)
	}

	docs, out := buildStdoutDocs(t, doc)
	sources, refs := ociSourcesAndRefs(docs)
	if want := map[string]string{"manifests-artifact": "1h0m0s", shared: "1h0m0s"}; len(sources) != len(want) || sources["manifests-artifact"] == "" || sources[shared] == "" {
		t.Fatalf("OCIRepositories = %v, want %v\noutput:\n%s", sources, want, out)
	}
	wantBuilt := []string{
		"Kustomization/addons->OCIRepository/" + shared,
		"Kustomization/manifests-delivery->OCIRepository/manifests-artifact",
		"Kustomization/platform-base->OCIRepository/" + shared,
	}
	if !slices.Equal(refs, wantBuilt) {
		t.Errorf("kurel build wrote source references %v, want %v", refs, wantBuilt)
	}
}

// source.objectName keeps the source with its component: of three components on
// one artifact the one that writes it has a source of its own, and the other two
// share the document's.
func TestOCINames_SourceObjectNameKeepsTheSource(t *testing.T) {
	doc := helmNamesApp(
		ociNamesComponent("base", "platform", "          objectName: platform-base\n", "") +
			ociNamesComponent("addons", "platform", "", "") +
			ociNamesComponent("extras", "platform", "", ""))
	shared := ociSharedSourceName("1h0m0s")
	cluster, apps := namingTransform(t, doc, oam.TransformContext{})
	got := generatedNames(cluster, apps)
	for _, line := range []string{
		"base: OCIRepository default/platform-base",
		shared + ": OCIRepository default/" + shared,
	} {
		if !slices.Contains(got, line) {
			t.Errorf("missing %q in\n  %s", line, strings.Join(got, "\n  "))
		}
	}
	if n := strings.Count(strings.Join(got, "\n"), ": OCIRepository "); n != 2 {
		t.Errorf("%d OCIRepository objects generated, want base's own and the shared one:\n  %s", n, strings.Join(got, "\n  "))
	}
	wantRefs := []string{
		"base: Kustomization/base->OCIRepository/platform-base",
		"addons: Kustomization/addons->OCIRepository/" + shared,
		"extras: Kustomization/extras->OCIRepository/" + shared,
	}
	if refs := ociKustomizationRefs(apps); !sameSet(refs, wantRefs) {
		t.Errorf("the Kustomizations read %v, want %v", refs, wantRefs)
	}
}

// The two objects are Flux objects: with a Flux namespace they land there, under
// their names, and are claimed there.
func TestOCINames_FluxNamespace(t *testing.T) {
	doc := helmNamesApp(ociNamesComponent("manifests", "manifests", "          objectName: artifact\n", "        kustomizationName: delivery\n"))
	cluster, apps := namingTransform(t, doc, oam.TransformContext{FluxNamespace: fluxNSTarget})
	got := generatedNames(cluster, apps)
	for _, line := range []string{
		"manifests: OCIRepository " + fluxNSTarget + "/artifact",
		"manifests: Kustomization " + fluxNSTarget + "/delivery",
	} {
		if !slices.Contains(got, line) {
			t.Errorf("missing %q in\n  %s", line, strings.Join(got, "\n  "))
		}
	}
}

func TestOCINames_Refusals(t *testing.T) {
	// An authored OCIRepository and an authored Kustomization, each a kind
	// component whose object is named name.
	repository := func(name string) string {
		return `    - name: repo
      type: ocirepository
      properties:
        objectName: ` + name + `
        url: oci://registry.example.com/org/other
        ref:
          tag: 1.0.0
`
	}
	kustomization := func(name string) string {
		return `    - name: sync
      type: fluxcd-kustomization
      properties:
        objectName: ` + name + `
        path: ./
        prune: true
        sourceRef:
          kind: OCIRepository
          name: manifests
`
	}
	manifests := ociNamesComponent("manifests", "manifests", "", "")
	answer := func(role oam.NameRole, name string) oam.TransformContext {
		return oam.TransformContext{Naming: func(req oam.NameRequest) (string, bool) { return name, req.Role == role }}
	}
	for _, tc := range []struct {
		name, doc string
		ctx       oam.TransformContext
		want      string
	}{
		{
			name: "source.objectName beside source.name",
			doc:  helmNamesApp(ociNamesComponent("manifests", "manifests", "          objectName: artifact\n          name: artifact-source\n", "")),
			want: `oci: source.objectName and source.name are both set, and each names the OCIRepository in another way: write source.objectName to rename the source the component keeps to itself`,
		},
		{
			name: "an authored Kustomization name that is no subdomain",
			doc:  helmNamesApp(ociNamesComponent("manifests", "manifests", "", "        kustomizationName: Not_A_Name\n")),
			want: `oci: naming the Kustomization: kustomizationName "Not_A_Name" cannot be the name for role "oci-kustomization": not a valid DNS-1123 subdomain: `,
		},
		{
			name: "a hook answer for the source that is no subdomain",
			doc:  helmNamesApp(manifests), ctx: answer(oam.NameRoleOCISource, "Not_A_Name"),
			want: `oci: naming the source: the Naming hook returned "Not_A_Name" for role "oci-source" in place of "manifests": not a valid DNS-1123 subdomain: `,
		},
		{
			name: "a hook answer for the Kustomization over 253 characters",
			doc:  helmNamesApp(manifests), ctx: answer(oam.NameRoleOCIKustomization, strings.Repeat("k", 254)),
			want: `for role "oci-kustomization" in place of "manifests": not a valid DNS-1123 subdomain: must be no more than 253`,
		},
		{
			name: "two Kustomizations given one name",
			doc: helmNamesApp(ociNamesComponent("manifests", "manifests", "", "        kustomizationName: delivery\n") +
				ociNamesComponent("other", "other", "", "        kustomizationName: delivery\n")),
			want: `oci: naming the Kustomization: name collision: ` + ociKustomizationKindName + ` "delivery" is named by ` +
				`component "manifests" (role "oci-kustomization", set by kustomizationName) and by ` +
				`component "other" (role "oci-kustomization", set by kustomizationName); give one of them another name`,
		},
		{
			name: "one answer for every Kustomization",
			doc:  helmNamesApp(manifests + ociNamesComponent("other", "other", "", "")), ctx: answer(oam.NameRoleOCIKustomization, "delivery"),
			want: `name collision: ` + ociKustomizationKindName + ` "delivery" is named by ` +
				`component "manifests" (role "oci-kustomization", returned by the Naming hook in place of "manifests") and by ` +
				`component "other" (role "oci-kustomization", returned by the Naming hook in place of "other"); give one of them another name`,
		},
		{
			name: "a kept source named as a source another component names",
			doc: helmNamesApp(ociNamesComponent("manifests", "manifests", "          objectName: platform\n", "") +
				ociNamesComponent("other", "other", "          name: platform\n", "")),
			want: `name collision: ` + ociRepositoryKindName + ` "platform" is named by ` +
				`component "manifests" (role "oci-source", set by source.objectName) and by ` +
				`component "other" (role "helm-source", set by source.name); give one of them another name`,
		},
		{
			name: "an authored OCIRepository named as the component's source",
			doc:  helmNamesApp(manifests + repository("manifests")),
			want: `component "repo": name collision: ` + ociRepositoryKindName + ` "default/manifests" is named by ` +
				`component "manifests" (role "oci-source", its default) and by ` +
				`component "repo" (role "object", set by properties.objectName); give one of them another name`,
		},
		{
			name: "an authored Kustomization named as the component's",
			doc:  helmNamesApp(manifests + kustomization("manifests")),
			want: `component "sync": name collision: ` + ociKustomizationKindName + ` "default/manifests" is named by ` +
				`component "manifests" (role "oci-kustomization", its default) and by ` +
				`component "sync" (role "object", set by properties.objectName); give one of them another name`,
		},
		{
			name: "an authored Kustomization named as the component's, in the Flux namespace",
			doc:  helmNamesApp(ociNamesComponent("manifests", "manifests", "", "        kustomizationName: delivery\n") + kustomization("delivery")),
			ctx:  oam.TransformContext{FluxNamespace: fluxNSTarget},
			want: `component "sync": name collision: ` + ociKustomizationKindName + ` "` + fluxNSTarget + `/delivery" is named by ` +
				`component "manifests" (role "oci-kustomization", set by kustomizationName) and by ` +
				`component "sync" (role "object", set by properties.objectName); give one of them another name`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := transformErr(t, tc.doc, tc.ctx)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v\nwant one containing %s", err, tc.want)
			}
		})
	}
}
