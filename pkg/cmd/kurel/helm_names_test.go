package kurel

import (
	"slices"
	"strings"
	"testing"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests pin the names a helm component gives the objects it generates
// (go-kure/launcher#787) through kurel's own transformer: the generated source,
// the values ConfigMap and the values Secret, each by the author, the Naming
// hook or the default, and where each is claimed under a Flux namespace.

// namingChart is a helm component with an inline source, a values ConfigMap and
// a values Secret: the names the helm rule resolves.
const namingChart = `    - name: chart
      type: helm
      properties:
        chart: podinfo
        source:
          url: https://charts.example.com
        valuesMode: configMap
        values:
          replicaCount: 2
        secretValues:
          password: not-a-real-one
`

// The defaults of namingChart's three names. Each ends in the digest of what it
// names, so these also pin that a change of go-kure/launcher#787 moved none.
const (
	chartSourceDefault    = "shop-source-d3f34901c4"
	chartConfigMapDefault = "chart-values-7048598917"
	chartSecretDefault    = "chart-secret-values-7f745ae1e1"
)

// helmNamesApp is a document named shop, in namespace default, holding
// components.
func helmNamesApp(components string) string {
	return `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
` + components
}

// chartRelease returns the HelmRelease named name among apps.
func chartRelease(t *testing.T, apps []oam.GeneratedApplication, name string) *helmv2.HelmRelease {
	t.Helper()
	for _, a := range apps {
		for _, p := range a.Objects {
			if p == nil {
				continue
			}
			if hr, ok := (*p).(*helmv2.HelmRelease); ok && hr.Name == name {
				return hr
			}
		}
	}
	t.Fatalf("no HelmRelease %q generated", name)
	return nil
}

// releaseNames lists what hr reads by name: its chart source, then each
// valuesFrom entry, as "Kind/name".
func releaseNames(hr *helmv2.HelmRelease) []string {
	var out []string
	if hr.Spec.Chart != nil {
		out = append(out, hr.Spec.Chart.Spec.SourceRef.Kind+"/"+hr.Spec.Chart.Spec.SourceRef.Name)
	}
	if hr.Spec.ChartRef != nil {
		out = append(out, hr.Spec.ChartRef.Kind+"/"+hr.Spec.ChartRef.Name)
	}
	for _, from := range hr.Spec.ValuesFrom {
		out = append(out, from.Kind+"/"+from.Name)
	}
	return out
}

// TestHelmNames_Defaults: with no hook and no authored name, the three objects
// carry their defaults and the release reads them by those names.
func TestHelmNames_Defaults(t *testing.T) {
	cluster, apps := namingTransform(t, helmNamesApp(namingChart), oam.TransformContext{})
	got := generatedNames(cluster, apps)
	for _, line := range []string{
		chartSourceDefault + ": HelmRepository default/" + chartSourceDefault,
		"chart: HelmRelease default/chart",
		chartConfigMapDefault + ": ConfigMap default/" + chartConfigMapDefault,
		chartSecretDefault + ": Secret default/" + chartSecretDefault,
	} {
		if !slices.Contains(got, line) {
			t.Errorf("missing %q in\n  %s", line, strings.Join(got, "\n  "))
		}
	}
	want := []string{"HelmRepository/" + chartSourceDefault, "ConfigMap/" + chartConfigMapDefault, "Secret/" + chartSecretDefault}
	if names := releaseNames(chartRelease(t, apps, "chart")); !slices.Equal(names, want) {
		t.Errorf("the release reads %v, want %v", names, want)
	}
}

// TestHelmNames_HookAnswerIsUsed: the hook names the generated source, the
// values ConfigMap and the values Secret, each used as returned, and the
// release reads them by the returned names. The source is the document's: the
// hook is asked for it once, with no component, and a second helm component on
// the same repository adopts it under the answer.
func TestHelmNames_HookAnswerIsUsed(t *testing.T) {
	const second = `    - name: other
      type: helm
      properties:
        chart: other
        source:
          url: https://charts.example.com
`
	var sourceRequests []oam.NameRequest
	rename := renameBy(map[string]string{
		"helm-source " + chartSourceDefault:         "upstream-charts",
		"values-configmap " + chartConfigMapDefault: "chart-settings",
		"values-secret " + chartSecretDefault:       "chart-credentials",
	})
	hook := func(req oam.NameRequest) (string, bool) {
		if req.Role == oam.NameRoleHelmSource {
			sourceRequests = append(sourceRequests, req)
		}
		return rename(req)
	}
	cluster, apps := namingTransform(t, helmNamesApp(namingChart+second), oam.TransformContext{Naming: hook})

	wantRequest := oam.NameRequest{Application: "shop", Role: oam.NameRoleHelmSource, Kind: "HelmRepository.source.toolkit.fluxcd.io", Default: chartSourceDefault}
	if !slices.Equal(sourceRequests, []oam.NameRequest{wantRequest}) {
		t.Errorf("the hook was asked for the source\n  %+v\nwant once, with no component:\n  %+v", sourceRequests, wantRequest)
	}

	got := generatedNames(cluster, apps)
	for _, line := range []string{
		"upstream-charts: HelmRepository default/upstream-charts",
		"chart-settings: ConfigMap default/chart-settings",
		"chart-credentials: Secret default/chart-credentials",
	} {
		if !slices.Contains(got, line) {
			t.Errorf("missing %q in\n  %s", line, strings.Join(got, "\n  "))
		}
	}
	if n := strings.Count(strings.Join(got, "\n"), ": HelmRepository "); n != 1 {
		t.Errorf("%d HelmRepository objects generated, want the one both components share:\n  %s", n, strings.Join(got, "\n  "))
	}
	want := []string{"HelmRepository/upstream-charts", "ConfigMap/chart-settings", "Secret/chart-credentials"}
	if names := releaseNames(chartRelease(t, apps, "chart")); !slices.Equal(names, want) {
		t.Errorf("chart's release reads %v, want %v", names, want)
	}
	if names := releaseNames(chartRelease(t, apps, "other")); !slices.Equal(names, []string{"HelmRepository/upstream-charts"}) {
		t.Errorf("other's release reads %v, want the adopted source only", names)
	}
}

// namedChart is namingChart with each of the three names authored.
const namedChart = `    - name: chart
      type: helm
      properties:
        chart: podinfo
        source:
          url: https://charts.example.com
          name: charts
        valuesMode: configMap
        values:
          replicaCount: 2
        valuesConfigMapName: settings
        secretValues:
          password: not-a-real-one
        valuesSecretName: credentials
`

// TestHelmNames_AuthoredWinsAndHookIsNotAsked: an authored name is used as
// written and the hook is not asked for it. A second helm component on the same
// repository that names no source does not share the named one: it gets the
// document's, for which the hook is asked.
func TestHelmNames_AuthoredWinsAndHookIsNotAsked(t *testing.T) {
	const second = `    - name: other
      type: helm
      properties:
        chart: other
        source:
          url: https://charts.example.com
`
	var requests []oam.NameRequest
	cluster, apps := namingTransform(t, helmNamesApp(namedChart+second), oam.TransformContext{Naming: declineEveryName(&requests)})

	var asked []oam.NameRequest
	for _, req := range requests {
		if req.Role == oam.NameRoleHelmSource || req.Role == oam.NameRoleValuesConfigMap || req.Role == oam.NameRoleValuesSecret {
			asked = append(asked, req)
		}
	}
	wantAsked := []oam.NameRequest{{Application: "shop", Role: oam.NameRoleHelmSource, Kind: "HelmRepository.source.toolkit.fluxcd.io", Default: chartSourceDefault}}
	if !slices.Equal(asked, wantAsked) {
		t.Errorf("the hook was asked\n  %+v\nwant only the unnamed component's source:\n  %+v", asked, wantAsked)
	}

	got := generatedNames(cluster, apps)
	for _, line := range []string{
		"charts: HelmRepository default/charts",
		chartSourceDefault + ": HelmRepository default/" + chartSourceDefault,
		"settings: ConfigMap default/settings",
		"credentials: Secret default/credentials",
	} {
		if !slices.Contains(got, line) {
			t.Errorf("missing %q in\n  %s", line, strings.Join(got, "\n  "))
		}
	}
	want := []string{"HelmRepository/charts", "ConfigMap/settings", "Secret/credentials"}
	if names := releaseNames(chartRelease(t, apps, "chart")); !slices.Equal(names, want) {
		t.Errorf("chart's release reads %v, want %v", names, want)
	}
	if names := releaseNames(chartRelease(t, apps, "other")); !slices.Equal(names, []string{"HelmRepository/" + chartSourceDefault}) {
		t.Errorf("other's release reads %v, want the document's own source", names)
	}
}

// TestHelmNames_FluxNamespace: the generated source and the values ConfigMap
// and Secret follow the HelmRelease to the Flux namespace, and their names are
// claimed there. An object of the same kind and name in the application
// namespace is another object and is accepted; one that lands in the Flux
// namespace is the same object and is refused. Without a Flux namespace both
// land in the application namespace and both are refused.
func TestHelmNames_FluxNamespace(t *testing.T) {
	// A ConfigMap of the application namespace named as the values ConfigMap.
	const configMap = `    - name: cfg
      type: configmap
      properties:
        objectName: settings
        data:
          a: "1"
`
	// A HelmRepository, which lands in the Flux namespace, named as the
	// generated source.
	const repository = `    - name: repo
      type: helmrepository
      properties:
        objectName: charts
        url: https://other.example.com
`
	flux := oam.TransformContext{FluxNamespace: fluxNSTarget}

	t.Run("the generated objects land in the Flux namespace", func(t *testing.T) {
		cluster, apps := namingTransform(t, helmNamesApp(namedChart+configMap), flux)
		got := generatedNames(cluster, apps)
		for _, line := range []string{
			"charts: HelmRepository " + fluxNSTarget + "/charts",
			"chart: HelmRelease " + fluxNSTarget + "/chart",
			"settings: ConfigMap " + fluxNSTarget + "/settings",
			"credentials: Secret " + fluxNSTarget + "/credentials",
			// The same kind and name in the application namespace: another object.
			"cfg: ConfigMap default/settings",
		} {
			if !slices.Contains(got, line) {
				t.Errorf("missing %q in\n  %s", line, strings.Join(got, "\n  "))
			}
		}
	})

	for _, tc := range []struct {
		name, doc string
		ctx       oam.TransformContext
		want      string
	}{
		{
			"a duplicate in the Flux namespace", helmNamesApp(namedChart + repository), flux,
			`component "repo": name collision: HelmRepository.source.toolkit.fluxcd.io "` + fluxNSTarget + `/charts" is named by ` +
				`component "chart" (role "helm-source", set by source.name) and by ` +
				`component "repo" (role "object", set by properties.objectName); give one of them another name`,
		},
		{
			"without a Flux namespace, the source", helmNamesApp(namedChart + repository), oam.TransformContext{},
			`component "repo": name collision: HelmRepository.source.toolkit.fluxcd.io "default/charts" is named by ` +
				`component "chart" (role "helm-source", set by source.name) and by ` +
				`component "repo" (role "object", set by properties.objectName); give one of them another name`,
		},
		{
			"without a Flux namespace, the values ConfigMap", helmNamesApp(namedChart + configMap), oam.TransformContext{},
			`component "cfg": name collision: ConfigMap "default/settings" is named by ` +
				`component "chart" (role "values-configmap", set by valuesConfigMapName) and by ` +
				`component "cfg" (role "object", set by properties.objectName); give one of them another name`,
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

// TestHelmNames_SourceCannotTakeAComponentName: the generated source is a
// component of the lowered document under its name, so it cannot carry the name
// of a component the document holds, its consumer's own included, whether the
// author wrote that name or the hook returned it.
func TestHelmNames_SourceCannotTakeAComponentName(t *testing.T) {
	const agent = `    - name: charts
      type: daemonset
      properties:
        image: ghcr.io/example/agent:v1.0.0
`
	sourceHook := func(name string) oam.TransformContext {
		return oam.TransformContext{Naming: func(req oam.NameRequest) (string, bool) {
			return name, req.Role == oam.NameRoleHelmSource
		}}
	}
	for _, tc := range []struct {
		name, doc string
		ctx       oam.TransformContext
		want      string
	}{
		{
			"authored, another component after it", helmNamesApp(namedChart + agent), oam.TransformContext{},
			`component "chart" (type "helm") in document "shop" (kind "Application"): helm: source.name "charts" is the name of a daemonset component of the document; the generated source is a component of the document too, so give it another name`,
		},
		{
			"authored, another component before it", helmNamesApp(agent + namedChart), oam.TransformContext{},
			`helm: source.name "charts" is the name of a daemonset component of the document`,
		},
		{
			"the hook's answer, another component", helmNamesApp(namingChart + agent), sourceHook("charts"),
			`helm: the generated source would be named "charts", the name of a daemonset component of the document; the generated source is a component of the document too, so rename that component, name the source with source.name, or have the Naming hook return another name for role "helm-source"`,
		},
		{
			"the hook's answer, the consumer's own name", helmNamesApp(namingChart), sourceHook("chart"),
			`helm: the generated source would be named "chart", the component's own name; the generated source is a component of the document too, so rename that component, name the source with source.name, or have the Naming hook return another name for role "helm-source"`,
		},
		{
			"the hook's answer is no object name", helmNamesApp(namingChart), sourceHook("Charts"),
			`helm: naming the generated source: the Naming hook returned "Charts" for role "helm-source" in place of "` + chartSourceDefault + `": not a valid DNS-1123 subdomain: `,
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

// TestOCINames_SourceName: an oci component's source.name names its
// OCIRepository, which is then generated apart from the Kustomization and
// claimed under role helm-source; two components that write the name for one
// artifact share it. The hook names the source two unnamed components share,
// and is not asked for the source a component keeps to itself.
func TestOCINames_SourceName(t *testing.T) {
	oci := func(name, extra string) string {
		return `    - name: ` + name + `
      type: oci
      properties:
        version: 1.4.0
        source:
          url: oci://registry.example.com/org/platform
` + extra
	}
	const sourceName = "          name: platform\n"

	t.Run("authored", func(t *testing.T) {
		var requests []oam.NameRequest
		cluster, apps := namingTransform(t, helmNamesApp(oci("base", sourceName)+oci("addons", sourceName)), oam.TransformContext{Naming: declineEveryName(&requests)})
		got := generatedNames(cluster, apps)
		if n := strings.Count(strings.Join(got, "\n"), ": OCIRepository "); n != 1 || !slices.Contains(got, "platform: OCIRepository default/platform") {
			t.Errorf("want the one OCIRepository default/platform, got %d:\n  %s", n, strings.Join(got, "\n  "))
		}
		for _, req := range requests {
			if req.Role == oam.NameRoleHelmSource {
				t.Errorf("the hook was asked for the name source.name sets: %+v", req)
			}
		}
	})

	t.Run("the hook names the shared source", func(t *testing.T) {
		var asked []oam.NameRequest
		hook := func(req oam.NameRequest) (string, bool) {
			if req.Role != oam.NameRoleHelmSource {
				return "", false
			}
			asked = append(asked, req)
			return "platform", true
		}
		cluster, apps := namingTransform(t, helmNamesApp(oci("base", "")+oci("addons", "")), oam.TransformContext{Naming: hook})
		got := generatedNames(cluster, apps)
		if n := strings.Count(strings.Join(got, "\n"), ": OCIRepository "); n != 1 || !slices.Contains(got, "platform: OCIRepository default/platform") {
			t.Errorf("want the one OCIRepository default/platform, got %d:\n  %s", n, strings.Join(got, "\n  "))
		}
		if len(asked) != 1 || asked[0].Component != "" || asked[0].Kind != "OCIRepository.source.toolkit.fluxcd.io" || !strings.HasPrefix(asked[0].Default, "shop-source-") {
			t.Errorf("the hook was asked %+v, want once for the document's shop-source-<digest>, with no component", asked)
		}
	})

	t.Run("the hook is not asked for a component's own source", func(t *testing.T) {
		var requests []oam.NameRequest
		cluster, apps := namingTransform(t, helmNamesApp(oci("base", "")), oam.TransformContext{Naming: declineEveryName(&requests)})
		if got := generatedNames(cluster, apps); !slices.Contains(got, "base: OCIRepository default/base") {
			t.Errorf("want the component's own OCIRepository default/base:\n  %s", strings.Join(got, "\n  "))
		}
		for _, req := range requests {
			if req.Role == oam.NameRoleHelmSource {
				t.Errorf("the hook was asked for the source a component keeps to itself: %+v", req)
			}
		}
	})
}
