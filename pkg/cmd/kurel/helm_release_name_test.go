package kurel

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests pin the name of the HelmRelease a helm component generates
// (go-kure/launcher#787) through kurel's own transformer: the author's
// helmReleaseName, else the Naming hook's answer for role helm-release, else
// the component name. It is the object's name alone: the Helm release name
// (spec.releaseName) and the values ConfigMap and Secret keep following the
// component.

const helmReleaseKindName = "HelmRelease.helm.toolkit.fluxcd.io"

// componentRelease returns the HelmRelease generated for component, whatever
// the object is named.
func componentRelease(t *testing.T, apps []oam.GeneratedApplication, component string) *helmv2.HelmRelease {
	t.Helper()
	for _, a := range apps {
		if a.Name != component {
			continue
		}
		for _, p := range a.Objects {
			if p == nil {
				continue
			}
			if hr, ok := (*p).(*helmv2.HelmRelease); ok {
				return hr
			}
		}
	}
	t.Fatalf("no HelmRelease generated for component %q", component)
	return nil
}

// helmReleaseRequests keeps the requests of role helm-release.
func helmReleaseRequests(requests []oam.NameRequest) []oam.NameRequest {
	var out []oam.NameRequest
	for _, req := range requests {
		if req.Role == oam.NameRoleHelmRelease {
			out = append(out, req)
		}
	}
	return out
}

// With neither the property nor an answer, the output is what it was: the hook
// is asked once, with the component, and the HelmRelease is the same object.
func TestHelmReleaseName_DecliningHookChangesNothing(t *testing.T) {
	doc := helmNamesApp(namingChart)
	plainCluster, plainApps := namingTransform(t, doc, oam.TransformContext{})

	var requests []oam.NameRequest
	cluster, apps := namingTransform(t, doc, oam.TransformContext{Naming: declineEveryName(&requests)})

	want := []oam.NameRequest{{Application: "shop", Component: "chart", Role: oam.NameRoleHelmRelease, Kind: helmReleaseKindName, Default: "chart"}}
	if got := helmReleaseRequests(requests); !slices.Equal(got, want) {
		t.Errorf("the hook was asked\n  %+v\nwant once, with the component:\n  %+v", got, want)
	}
	if got, plain := generatedNames(cluster, apps), generatedNames(plainCluster, plainApps); !slices.Equal(got, plain) {
		t.Errorf("a declining hook changed the names:\n  %s\nwithout a hook:\n  %s", strings.Join(got, "\n  "), strings.Join(plain, "\n  "))
	}
	hr, plain := componentRelease(t, apps, "chart"), componentRelease(t, plainApps, "chart")
	if hr.Name != "chart" || !reflect.DeepEqual(hr, plain) {
		t.Errorf("a declining hook changed the HelmRelease:\n  %+v\nwithout a hook:\n  %+v", hr, plain)
	}
}

// helmReleaseName, and the hook's answer, name the HelmRelease object and
// nothing else: the member keeps the component's name, spec.releaseName stays
// the component's (or the authored releaseName), and the values ConfigMap and
// Secret keep the names they had, which the release still reads.
func TestHelmReleaseName_NamesTheObjectAlone(t *testing.T) {
	const (
		renamed     = "shop-podinfo"
		authored    = "        helmReleaseName: " + renamed + "\n"
		releaseName = "        releaseName: podinfo\n"
	)
	answer := renameBy(map[string]string{"helm-release chart": renamed})
	for _, tc := range []struct {
		name        string
		base, extra string // the document without a renamed object, and what the author adds to rename it
		hook        func(oam.NameRequest) (string, bool)
		wantAsked   bool
		wantRelease string // spec.releaseName
	}{
		{name: "the author", base: namingChart, extra: authored, hook: answer, wantRelease: "chart"},
		{name: "the hook", base: namingChart, hook: answer, wantAsked: true, wantRelease: "chart"},
		{name: "the author, beside releaseName", base: namingChart + releaseName, extra: authored, hook: answer, wantRelease: "podinfo"},
		{name: "the hook, beside releaseName", base: namingChart + releaseName, hook: answer, wantAsked: true, wantRelease: "podinfo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plainCluster, plainApps := namingTransform(t, helmNamesApp(tc.base), oam.TransformContext{})
			plain := componentRelease(t, plainApps, "chart")
			if plain.Name != "chart" || plain.Spec.ReleaseName != tc.wantRelease {
				t.Fatalf("without a renamed object the HelmRelease is %q with spec.releaseName %q, want chart and %q", plain.Name, plain.Spec.ReleaseName, tc.wantRelease)
			}

			var requests []oam.NameRequest
			hook := func(req oam.NameRequest) (string, bool) {
				requests = append(requests, req)
				return tc.hook(req)
			}
			cluster, apps := namingTransform(t, helmNamesApp(tc.base+tc.extra), oam.TransformContext{Naming: hook})
			if asked := len(helmReleaseRequests(requests)) == 1; asked != tc.wantAsked {
				t.Errorf("the hook was asked for the HelmRelease: %+v, want asked = %v", helmReleaseRequests(requests), tc.wantAsked)
			}

			hr := componentRelease(t, apps, "chart")
			if hr.Name != renamed {
				t.Errorf("the HelmRelease is named %q, want %q", hr.Name, renamed)
			}
			if hr.Spec.ReleaseName != tc.wantRelease {
				t.Errorf("spec.releaseName = %q, want %q: the Helm release name does not follow the object's", hr.Spec.ReleaseName, tc.wantRelease)
			}
			wantReads := []string{"HelmRepository/" + chartSourceDefault, "ConfigMap/" + chartConfigMapDefault, "Secret/" + chartSecretDefault}
			if reads := releaseNames(hr); !slices.Equal(reads, wantReads) || !slices.Equal(releaseNames(plain), wantReads) {
				t.Errorf("the release reads %v, and %v without a renamed object; want %v for both", reads, releaseNames(plain), wantReads)
			}

			// Every other name is where it was: only the HelmRelease's line moves.
			want := slices.Clone(generatedNames(plainCluster, plainApps))
			i := slices.Index(want, "chart: HelmRelease default/chart")
			if i < 0 {
				t.Fatalf("no HelmRelease default/chart in\n  %s", strings.Join(want, "\n  "))
			}
			want[i] = "chart: HelmRelease default/" + renamed
			if got := generatedNames(cluster, apps); !slices.Equal(got, want) {
				t.Errorf("the names are\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
			}

			// Apart from its name the object is the one generated without it.
			same := hr.DeepCopy()
			same.Name = plain.Name
			if !reflect.DeepEqual(same, plain) {
				t.Errorf("renaming the HelmRelease changed more than its name:\n  %+v\nwithout:\n  %+v", hr, plain)
			}
		})
	}
}

// The name is claimed where the HelmRelease lands: the Flux namespace when the
// transform has one, else the application namespace. An authored helmrelease
// component whose objectName is the same name is refused in the transform, the
// default included.
func TestHelmReleaseName_Claimed(t *testing.T) {
	release := func(name string) string {
		return `    - name: rel
      type: helmrelease
      properties:
        objectName: ` + name + `
        chart:
          spec:
            chart: other
            sourceRef:
              kind: HelmRepository
              name: somewhere
`
	}
	const authored = "        helmReleaseName: shop-podinfo\n"
	flux := oam.TransformContext{FluxNamespace: fluxNSTarget}

	t.Run("the HelmRelease lands in the Flux namespace under its name", func(t *testing.T) {
		cluster, apps := namingTransform(t, helmNamesApp(namingChart+authored), flux)
		if got, line := generatedNames(cluster, apps), "chart: HelmRelease "+fluxNSTarget+"/shop-podinfo"; !slices.Contains(got, line) {
			t.Errorf("missing %q in\n  %s", line, strings.Join(got, "\n  "))
		}
	})

	for _, tc := range []struct {
		name, doc string
		ctx       oam.TransformContext
		want      string
	}{
		{
			name: "an authored HelmRelease named as the component's",
			doc:  helmNamesApp(namingChart + release("chart")),
			want: `component "rel": name collision: ` + helmReleaseKindName + ` "default/chart" is named by ` +
				`component "chart" (role "helm-release", its default) and by ` +
				`component "rel" (role "object", set by properties.objectName); give one of them another name`,
		},
		{
			name: "an authored HelmRelease named as the component's, in the Flux namespace",
			doc:  helmNamesApp(namingChart + authored + release("shop-podinfo")), ctx: flux,
			want: `component "rel": name collision: ` + helmReleaseKindName + ` "` + fluxNSTarget + `/shop-podinfo" is named by ` +
				`component "chart" (role "helm-release", set by helmReleaseName) and by ` +
				`component "rel" (role "object", set by properties.objectName); give one of them another name`,
		},
		{
			name: "the hook's answer, the name of an authored HelmRelease",
			doc:  helmNamesApp(namingChart + release("theirs")),
			ctx:  oam.TransformContext{Naming: renameBy(map[string]string{"helm-release chart": "theirs"})},
			want: `component "rel": name collision: ` + helmReleaseKindName + ` "default/theirs" is named by ` +
				`component "chart" (role "helm-release", returned by the Naming hook in place of "chart") and by ` +
				`component "rel" (role "object", set by properties.objectName); give one of them another name`,
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

func TestHelmReleaseName_Refusals(t *testing.T) {
	chart := func(name, extra string) string {
		return `    - name: ` + name + `
      type: helm
      properties:
        chart: podinfo
        source:
          url: https://charts.example.com
` + extra
	}
	answer := func(name string) oam.TransformContext {
		return oam.TransformContext{Naming: func(req oam.NameRequest) (string, bool) { return name, req.Role == oam.NameRoleHelmRelease }}
	}
	for _, tc := range []struct {
		name, doc string
		ctx       oam.TransformContext
		want      string
	}{
		{
			name: "under delivery: template",
			doc:  helmNamesApp(chart("chart", "        delivery: template\n        helmReleaseName: shop-podinfo\n")),
			want: `helm: delivery: template does not support helmReleaseName (the chart is rendered at build time, so no HelmRelease is generated; releaseName names the render's release)`,
		},
		{
			name: "an empty name",
			doc:  helmNamesApp(chart("chart", "        helmReleaseName: \"\"\n")),
			want: `helm: naming the HelmRelease: helmReleaseName "" cannot be the name for role "helm-release": it is empty; write a valid name, or leave the property out for the default "chart"`,
		},
		{
			name: "an authored name that is no subdomain",
			doc:  helmNamesApp(chart("chart", "        helmReleaseName: Not_A_Name\n")),
			want: `helm: naming the HelmRelease: helmReleaseName "Not_A_Name" cannot be the name for role "helm-release": not a valid DNS-1123 subdomain: `,
		},
		{
			name: "an authored name over 253 characters",
			doc:  helmNamesApp(chart("chart", "        helmReleaseName: "+strings.Repeat("r", 254)+"\n")),
			want: `cannot be the name for role "helm-release": not a valid DNS-1123 subdomain: must be no more than 253`,
		},
		{
			name: "a hook answer that is no subdomain",
			doc:  helmNamesApp(chart("chart", "")), ctx: answer("Not_A_Name"),
			want: `helm: naming the HelmRelease: the Naming hook returned "Not_A_Name" for role "helm-release" in place of "chart": not a valid DNS-1123 subdomain: `,
		},
		{
			name: "two HelmReleases given one name",
			doc:  helmNamesApp(chart("chart", "        helmReleaseName: shared\n") + chart("other", "        helmReleaseName: shared\n")),
			want: `helm: naming the HelmRelease: name collision: ` + helmReleaseKindName + ` "shared" is named by ` +
				`component "chart" (role "helm-release", set by helmReleaseName) and by ` +
				`component "other" (role "helm-release", set by helmReleaseName); give one of them another name`,
		},
		{
			name: "one answer for every HelmRelease",
			doc:  helmNamesApp(chart("chart", "") + chart("other", "")), ctx: answer("shared"),
			want: `name collision: ` + helmReleaseKindName + ` "shared" is named by ` +
				`component "chart" (role "helm-release", returned by the Naming hook in place of "chart") and by ` +
				`component "other" (role "helm-release", returned by the Naming hook in place of "other"); give one of them another name`,
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

// Under delivery: template no HelmRelease is generated, so the hook is not
// asked for one.
func TestHelmReleaseName_NotAskedUnderTemplateDelivery(t *testing.T) {
	var requests []oam.NameRequest
	doc := hookApp("shop", hookComponent("db", "helm", serveHookChart(t), "        delivery: template\n"), "")
	namingTransform(t, doc, oam.TransformContext{Naming: declineEveryName(&requests)})
	if got := helmReleaseRequests(requests); len(got) != 0 {
		t.Errorf("the hook was asked for a HelmRelease no rule generates: %+v", got)
	}
}

// A hook that renames all three objects a helm and an oci component name after
// themselves: each object carries its answer, what reads the source reads it by
// the answer, the members stay where they were, and the tree builds.
func TestMemberNames_HookRenamesAllThree(t *testing.T) {
	doc := helmNamesApp(namingChart + ociNamesComponent("manifests", "manifests", "", ""))
	ctx := oam.TransformContext{Naming: renameBy(map[string]string{
		"helm-release chart":          "shop-podinfo",
		"oci-kustomization manifests": "manifests-delivery",
		"oci-source manifests":        "manifests-artifact",
	})}
	cluster, apps := namingTransform(t, doc, ctx)
	plainCluster, plainApps := namingTransform(t, doc, oam.TransformContext{})

	// Only the three objects' lines move.
	want := slices.Clone(generatedNames(plainCluster, plainApps))
	for from, to := range map[string]string{
		"chart: HelmRelease default/chart":           "chart: HelmRelease default/shop-podinfo",
		"manifests: Kustomization default/manifests": "manifests: Kustomization default/manifests-delivery",
		"manifests: OCIRepository default/manifests": "manifests: OCIRepository default/manifests-artifact",
	} {
		i := slices.Index(want, from)
		if i < 0 {
			t.Fatalf("no %q in\n  %s", from, strings.Join(want, "\n  "))
		}
		want[i] = to
	}
	if got := generatedNames(cluster, apps); !slices.Equal(got, want) {
		t.Errorf("the names are\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}

	if refs, wantRefs := ociKustomizationRefs(apps), []string{"manifests: Kustomization/manifests-delivery->OCIRepository/manifests-artifact"}; !slices.Equal(refs, wantRefs) {
		t.Errorf("the Kustomization reads %v, want %v", refs, wantRefs)
	}
	hr := componentRelease(t, apps, "chart")
	wantReads := []string{"HelmRepository/" + chartSourceDefault, "ConfigMap/" + chartConfigMapDefault, "Secret/" + chartSecretDefault}
	if reads := releaseNames(hr); hr.Spec.ReleaseName != "chart" || !slices.Equal(reads, wantReads) {
		t.Errorf("the release is named %q by Helm and reads %v, want chart and %v", hr.Spec.ReleaseName, reads, wantReads)
	}
	if got, plain := groupNames(t, cluster), groupNames(t, plainCluster); !slices.Equal(got, plain) {
		t.Errorf("groups = %v, and %v without a hook; want the same", got, plain)
	}

	// The whole tree, walked and integrated with Flux as a consumer does.
	if _, err := hookGroupTree(t, doc, ctx); err != nil {
		t.Fatalf("building the tree with all three renamed: %v", err)
	}
}
