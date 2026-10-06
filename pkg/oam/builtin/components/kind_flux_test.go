package components_test

// The kind components of the Flux APIs beside the sources, the HelmRelease and
// the Kustomization (go-kure/launcher#790): their fixtures, and the tests of
// what they add to a policy-free kind, the Flux namespace. The kinds are rows
// of policyFreeKinds (flux), and the tests of everything else are the shared
// ones.

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// fluxAlertSource is one source of events: every Kustomization of the Alert's
// namespace.
func fluxAlertSource() map[string]any {
	return map[string]any{"kind": "Kustomization", "name": "*"}
}

// fluxAlertWith is the properties of a fluxcd-alert with the provider
// reference and the sources.
func fluxAlertWith(provider map[string]any, sources ...any) map[string]any {
	return map[string]any{"providerRef": provider, "eventSources": append([]any{}, sources...)}
}

// fluxAlertMinimal is the least a fluxcd-alert may author.
func fluxAlertMinimal() map[string]any {
	return fluxAlertWith(map[string]any{"name": "slack"}, fluxAlertSource())
}

// fluxAlertFull sets every field of an AlertSpec. Its second source reaches
// into another namespace, by label.
func fluxAlertFull() map[string]any {
	return map[string]any{
		"providerRef":   map[string]any{"name": "slack"},
		"eventSeverity": "error",
		"eventSources": []any{
			map[string]any{"kind": "HelmRelease", "name": "web"},
			map[string]any{
				"apiVersion": "kustomize.toolkit.fluxcd.io/v1", "kind": "Kustomization", "name": "*",
				"namespace": "shop", "matchLabels": map[string]any{"team": "shop"},
			},
		},
		"inclusionList": []any{".*succeeded.*"},
		"exclusionList": []any{"^Dependencies do not meet ready condition.*"},
		"eventMetadata": map[string]any{"cluster": "east", "env": "production"},
		"summary":       "releases of the shop",
		"suspend":       true,
	}
}

// imagePolicySemver is the policy of an imagepolicy that selects the highest
// tag of a semantic version range.
func imagePolicySemver() map[string]any {
	return map[string]any{"semver": map[string]any{"range": ">=1.0.0"}}
}

// imagePolicyMinimal is the least an imagepolicy may author.
func imagePolicyMinimal() map[string]any {
	return map[string]any{"imageRepositoryRef": map[string]any{"name": "web"}, "policy": imagePolicySemver()}
}

// imagePolicyFull sets every field of an ImagePolicySpec, the three policies
// at once, which no marker of the API refuses. Its repository is one of
// another namespace.
func imagePolicyFull() map[string]any {
	return map[string]any{
		"imageRepositoryRef": map[string]any{"name": "web", "namespace": "registry"},
		"policy": map[string]any{
			"semver":       map[string]any{"range": "1.x"},
			"alphabetical": map[string]any{"order": "asc"},
			"numerical":    map[string]any{"order": "desc"},
		},
		"filterTags":             map[string]any{"pattern": "^main-[a-f0-9]+-(?P<ts>[0-9]+)", "extract": "$ts"},
		"digestReflectionPolicy": "Always",
		"interval":               "10m",
		"suspend":                true,
	}
}

// imageRepositoryMinimal is the least an imagerepository may author. Its
// registry is the one ptStrictPolicy allows.
func imageRepositoryMinimal() map[string]any {
	return map[string]any{"image": "registry.example/shop/web", "interval": "10m"}
}

// imageRepositoryFull sets every field of an ImageRepositorySpec. Its three
// Secrets are read from the namespace the object lands in, and its ACL opens
// it to the namespaces labelled for the shop team.
func imageRepositoryFull() map[string]any {
	return map[string]any{
		"image":              "registry.example/shop/web",
		"interval":           "10m",
		"timeout":            "90s",
		"secretRef":          map[string]any{"name": "registry-credentials"},
		"proxySecretRef":     map[string]any{"name": "registry-proxy"},
		"certSecretRef":      map[string]any{"name": "registry-tls"},
		"serviceAccountName": "scanner",
		"suspend":            true,
		"accessFrom":         map[string]any{"namespaceSelectors": []any{map[string]any{"matchLabels": map[string]any{"team": "shop"}}}},
		"exclusionList":      []any{`^.*\.sig$`, `^.*\.att$`},
		"provider":           "aws",
		"insecure":           true,
	}
}

// imageUpdateAutomationSource is the source of an imageupdateautomation: a
// GitRepository of the object's namespace.
func imageUpdateAutomationSource() map[string]any {
	return map[string]any{"kind": "GitRepository", "name": "fleet"}
}

// imageUpdateAutomationAuthor is the author of an automation's commits.
func imageUpdateAutomationAuthor() map[string]any {
	return map[string]any{"email": "fluxbot@example.com"}
}

// imageUpdateAutomationCommit is the least `git.commit` may author.
func imageUpdateAutomationCommit() map[string]any {
	return map[string]any{"author": imageUpdateAutomationAuthor()}
}

// imageUpdateAutomationGit is a `git` with the given commit and nothing else.
func imageUpdateAutomationGit(commit map[string]any) map[string]any {
	return map[string]any{"commit": commit}
}

// imageUpdateAutomationMinimal is the least an imageupdateautomation may
// author: no `git`, which the type documents as mandatory in practice and no
// marker requires.
func imageUpdateAutomationMinimal() map[string]any {
	return map[string]any{"sourceRef": imageUpdateAutomationSource(), "interval": "30m"}
}

// imageUpdateAutomationFull sets every field of an ImageUpdateAutomationSpec.
// Its source is a GitRepository of another namespace, and its commits are
// signed with the key of the Secret `signing-key`.
func imageUpdateAutomationFull() map[string]any {
	return map[string]any{
		"sourceRef": map[string]any{
			"apiVersion": "source.toolkit.fluxcd.io/v1", "kind": "GitRepository", "name": "fleet", "namespace": "platform",
		},
		"git": map[string]any{
			"checkout": map[string]any{"ref": map[string]any{"branch": "main"}},
			"commit": map[string]any{
				"author":                map[string]any{"name": "fluxbot", "email": "fluxbot@example.com"},
				"signingKey":            map[string]any{"secretRef": map[string]any{"name": "signing-key"}, "type": "ssh"},
				"messageTemplate":       "Update images of {{ .AutomationObject }}",
				"messageTemplateValues": map[string]any{"cluster": "east"},
			},
			"push": map[string]any{
				"branch": "image-updates", "refspec": "refs/heads/image-updates:refs/heads/staging",
				"options": map[string]any{"merge_request.create": ""},
			},
		},
		"interval": "30m",
		"policySelector": map[string]any{
			"matchLabels":      map[string]any{"team": "shop"},
			"matchExpressions": []any{map[string]any{"key": "tier", "operator": "In", "values": []any{"web"}}},
		},
		"update":  map[string]any{"strategy": "Setters", "path": "./clusters/east"},
		"suspend": true,
	}
}

// artifactGeneratorSource is one source of an artifactgenerator: a
// GitRepository of the object's namespace, under the alias `app`.
func artifactGeneratorSource() map[string]any {
	return map[string]any{"alias": "app", "kind": "GitRepository", "name": "app"}
}

// artifactGeneratorCopy copies the deploy directory of the source `app` to the
// root of the artifact.
func artifactGeneratorCopy() map[string]any {
	return map[string]any{"from": "@app/deploy/**", "to": "@artifact/"}
}

// artifactGeneratorArtifact is the least an artifact may author.
func artifactGeneratorArtifact() map[string]any {
	return map[string]any{"name": "app", "copy": []any{artifactGeneratorCopy()}}
}

// artifactGeneratorWith is the properties of an artifactgenerator with the
// sources and one artifact.
func artifactGeneratorWith(sources ...any) map[string]any {
	return map[string]any{"sources": append([]any{}, sources...), "artifacts": []any{artifactGeneratorArtifact()}}
}

// artifactGeneratorMinimal is the least an artifactgenerator may author.
func artifactGeneratorMinimal() map[string]any {
	return artifactGeneratorWith(artifactGeneratorSource())
}

// artifactGeneratorFull sets every field of an ArtifactGeneratorSpec. Its
// second source is an OCIRepository of another namespace.
func artifactGeneratorFull() map[string]any {
	return map[string]any{
		"commonMetadata": map[string]any{
			"labels":      map[string]any{"team": "shop"},
			"annotations": map[string]any{"example.com/owner": "shop"},
		},
		"sources": []any{
			map[string]any{"alias": "apps", "kind": "GitRepository", "name": "fleet"},
			map[string]any{"alias": "base", "kind": "OCIRepository", "name": "base-manifests", "namespace": "platform"},
		},
		"pathPattern": "@apps/apps/{app}",
		"artifacts": []any{
			map[string]any{
				"name": "{app}", "revision": "@apps", "originRevision": "@apps",
				"copy": []any{
					map[string]any{"from": "@base/**", "to": "@artifact/", "exclude": []any{"*.md"}, "strategy": "Overwrite"},
					map[string]any{"from": "@apps/apps/{app}/values.yaml", "to": "@artifact/values.yaml", "strategy": "Merge"},
				},
			},
		},
	}
}

// fluxKinds returns the rows of policyFreeKinds that move to the Flux
// namespace.
func fluxKinds(t *testing.T) []policyFreeKind {
	t.Helper()
	var kinds []policyFreeKind
	for _, kind := range policyFreeKinds {
		if kind.flux {
			kinds = append(kinds, kind)
		}
	}
	// Vacuity guard: the Flux kinds are these, each once. A row dropped from
	// policyFreeKinds would otherwise take the kind's tests with it, here and
	// in the tests every policy-free kind shares.
	want := []string{"artifactgenerator", "fluxcd-alert", "imagepolicy", "imagerepository", "imageupdateautomation"}
	got := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		got = append(got, kind.component)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("policyFreeKinds marks %v as Flux kinds, want %v", got, want)
	}
	return kinds
}

// fluxKindTransform transforms a document of the given components, all of one
// kind, for the build namespace "demo" and the Flux namespace fluxNS ("" for
// none), and returns every generated object.
func fluxKindTransform(kind policyFreeKind, fluxNS string, components ...oam.Component) ([]client.Object, error) {
	for i := range components {
		components[i].Type = kind.component
	}
	app := &oam.Application{Metadata: oam.Metadata{Name: "shop"}, Spec: oam.ApplicationSpec{Components: components}}
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{kind.component: kind.handler}, nil)
	cluster, err := tr.Transform(app, oam.TransformContext{Namespace: "demo", FluxNamespace: fluxNS})
	if err != nil {
		return nil, err
	}
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		return nil, err
	}
	var out []client.Object
	for _, a := range apps {
		for _, o := range a.Objects {
			out = append(out, *o)
		}
	}
	return out, nil
}

// TestFluxKinds_LandInTheFluxNamespace: through the transform, each kind's
// object lands in the Flux namespace when one is set and in the build
// namespace when none is, and but for its namespace it is the same object.
func TestFluxKinds_LandInTheFluxNamespace(t *testing.T) {
	for _, kind := range fluxKinds(t) {
		t.Run(kind.component, func(t *testing.T) {
			build := func(fluxNS string) client.Object {
				objs, err := fluxKindTransform(kind, fluxNS, oam.Component{Name: "web", Properties: maps.Clone(kind.full)})
				if err != nil {
					t.Fatalf("transform with the Flux namespace %q: %v", fluxNS, err)
				}
				if len(objs) != 1 {
					t.Fatalf("generated %d objects with the Flux namespace %q, want one", len(objs), fluxNS)
				}
				return objs[0]
			}
			plain, moved := build(""), build("flux-system")
			if plain.GetNamespace() != "demo" {
				t.Errorf("with no Flux namespace the object is in %q, want the build namespace demo", plain.GetNamespace())
			}
			if moved.GetNamespace() != "flux-system" {
				t.Errorf("with a Flux namespace the object is in %q, want flux-system", moved.GetNamespace())
			}
			moved.SetNamespace(plain.GetNamespace())
			if got, want := policyFreeJSON(t, moved), policyFreeJSON(t, plain); !reflect.DeepEqual(got, want) {
				t.Errorf("but for its namespace the moved object differs:\n got %v\nwant %v", got, want)
			}
		})
	}
}

// TestFluxKinds_ObjectNameIsClaimedInTheFluxNamespace: with a Flux namespace
// set, two components of one kind given one object name are refused, and the
// refusal names the object in the Flux namespace, where both would land.
func TestFluxKinds_ObjectNameIsClaimedInTheFluxNamespace(t *testing.T) {
	for _, kind := range fluxKinds(t) {
		t.Run(kind.component, func(t *testing.T) {
			named := func(name string) oam.Component {
				props := map[string]any{oam.ObjectNameProperty: "shared"}
				maps.Copy(props, kind.minimal)
				return oam.Component{Name: name, Properties: props}
			}
			_, err := fluxKindTransform(kind, "flux-system", named("a"), named("b"))
			want := fmt.Sprintf("name collision: %s %q is named by component %q", kind.gvk.GroupKind(), "flux-system/shared", "a")
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want it to contain %q", err, want)
			}
		})
	}
}

// TestFluxKinds_UnheldPassEveryPolicy: a Flux kind the environment policy does
// not reach passes a policy that allows one registry no fixture names and
// forbids explicit secrets, on the least it may author and on every field.
func TestFluxKinds_UnheldPassEveryPolicy(t *testing.T) {
	policy := esPolicy{stubPolicy: &stubPolicy{allowedRegistries: []string{"registry.invalid"}}}
	var unheld []string
	for _, kind := range fluxKinds(t) {
		if kind.held {
			continue
		}
		unheld = append(unheld, kind.component)
		for name, props := range map[string]map[string]any{"minimal": kind.minimal, "full": kind.full} {
			t.Run(kind.component+"/"+name, func(t *testing.T) {
				cfg, err := kind.handler.ToApplicationConfig(&oam.Component{Name: "web", Type: kind.component, Properties: maps.Clone(props)}, coreKindNamespace)
				if err != nil {
					t.Fatalf("ToApplicationConfig: %v", err)
				}
				if err := cfg.(policyApplier).ApplyPolicy(policy); err != nil {
					t.Errorf("ApplyPolicy refuses a kind the policy does not reach: %v", err)
				}
			})
		}
	}
	// Vacuity guard: these four are the kinds the policy does not reach.
	slices.Sort(unheld)
	if want := []string{"artifactgenerator", "fluxcd-alert", "imagepolicy", "imageupdateautomation"}; !slices.Equal(unheld, want) {
		t.Fatalf("the Flux kinds the policy does not reach are %v, want %v", unheld, want)
	}
}

// TestImageRepository_ImageIsHeldToAllowedRegistries: the registry of an
// imagerepository's `image` is held to the allowed registries by the rule an
// image of a pod is held to. A name with no registry is one of docker.io; no
// tag rule applies, since the field names a repository; and with no policy, or
// one that lists no registry, nothing is refused.
func TestImageRepository_ImageIsHeldToAllowedRegistries(t *testing.T) {
	allowing := func(registries ...string) oam.Policy {
		return esPolicy{stubPolicy: &stubPolicy{allowedRegistries: registries}}
	}
	cases := []struct {
		name, image string
		policy      oam.Policy
		refused     bool
	}{
		{"its registry is allowed", "registry.example/shop/web", allowing("registry.example"), false},
		{"its registry is allowed, with a port", "registry.example:5000/shop/web", allowing("registry.example:5000"), false},
		{"another registry", "ghcr.io/shop/web", allowing("registry.example"), true},
		{"the allowed registry on another port", "registry.example:5000/shop/web", allowing("registry.example"), true},
		{"a name with no registry", "shop/web", allowing("registry.example"), true},
		{"a name with no registry, docker.io allowed", "shop/web", allowing("docker.io"), false},
		{"a policy that lists no registry", "ghcr.io/shop/web", allowing(), false},
		{"no policy", "ghcr.io/shop/web", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := withProperty(imageRepositoryMinimal(), "image", tc.image)
			cfg, err := (&components.ImageRepositoryHandler{}).ToApplicationConfig(&oam.Component{Name: "web", Type: "imagerepository", Properties: props}, coreKindNamespace)
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			err = cfg.(policyApplier).ApplyPolicy(tc.policy)
			if !tc.refused {
				if err != nil {
					t.Errorf("ApplyPolicy = %v, want no refusal", err)
				}
				return
			}
			var refusal *oam.PolicyRefusal
			want := fmt.Sprintf("image %q is not from an allowed registry", tc.image)
			if !errors.As(err, &refusal) || refusal.Class != oam.RefusalRegistry || !strings.Contains(err.Error(), want) {
				t.Errorf("ApplyPolicy = %v, want a refusal of class %q that says %q", err, oam.RefusalRegistry, want)
			}
		})
	}
}

// TestImageRepository_TimeoutIsWrittenWithoutHours: the pattern of `timeout`
// takes no h, which is how the type writes an hour or more. An authored
// timeout of that length, given in the units the pattern takes, goes out in
// minutes, and the interval, whose pattern takes h, as the type writes it.
func TestImageRepository_TimeoutIsWrittenWithoutHours(t *testing.T) {
	props := withProperty(withProperty(imageRepositoryMinimal(), "timeout", "90m"), "interval", "90m")
	kind := fluxKindRow(t, "imagerepository")
	objs, err := fluxKindTransform(kind, "flux-system", oam.Component{Name: "web", Properties: props})
	if err != nil || len(objs) != 1 {
		t.Fatalf("transform = %d objects, %v; want one", len(objs), err)
	}
	spec, _ := policyFreeJSON(t, objs[0])["spec"].(map[string]any)
	if got, want := spec["timeout"], "90m0s"; got != want {
		t.Errorf("spec.timeout = %v, want %q", got, want)
	}
	if got, want := spec["interval"], "1h30m0s"; got != want {
		t.Errorf("spec.interval = %v, want %q", got, want)
	}
}

// fluxKindRow is the row of the named Flux kind.
func fluxKindRow(t *testing.T, component string) policyFreeKind {
	t.Helper()
	for _, kind := range fluxKinds(t) {
		if kind.component == component {
			return kind
		}
	}
	t.Fatalf("%s is no Flux kind", component)
	return policyFreeKind{}
}

// TestFluxKinds_ReportReads: each kind's config reports the ConfigMaps and
// Secrets its object reads by name from the namespace it lands in, so the
// objects of a trait that are named follow it to the Flux namespace. Every
// kind has a row, and a row that names none says the kind reads none.
func TestFluxKinds_ReportReads(t *testing.T) {
	type reads struct {
		props               map[string]any
		configMaps, secrets []string
	}
	cases := map[string][]reads{
		// An Alert names a Provider, which is the object that holds the
		// address and the credentials.
		"fluxcd-alert": {{props: fluxAlertFull()}},
		// An ImagePolicy names an ImageRepository, which is the object that
		// holds the credentials of the registry.
		"imagepolicy": {{props: imagePolicyFull()}},
		// An ImageRepository reads the Secrets of the registry's credentials,
		// of its proxy and of its certificates.
		"imagerepository": {
			{props: imageRepositoryFull(), secrets: []string{"registry-credentials", "registry-proxy", "registry-tls"}},
			{props: imageRepositoryMinimal()},
		},
		// An ImageUpdateAutomation reads the Secret of its signing key. The
		// credentials of the repository are the GitRepository's.
		"imageupdateautomation": {
			{props: imageUpdateAutomationFull(), secrets: []string{"signing-key"}},
			{props: imageUpdateAutomationMinimal()},
			{props: withProperty(imageUpdateAutomationMinimal(), "git", imageUpdateAutomationGit(imageUpdateAutomationCommit()))},
		},
		// An ArtifactGenerator names Flux sources, which are the objects that
		// hold the addresses and the credentials.
		"artifactgenerator": {{props: artifactGeneratorFull()}},
	}
	for _, kind := range fluxKinds(t) {
		if len(cases[kind.component]) == 0 {
			t.Errorf("%s has no case of what it reads", kind.component)
		}
		for i, tc := range cases[kind.component] {
			t.Run(fmt.Sprintf("%s/%d", kind.component, i), func(t *testing.T) {
				cfg, err := kind.handler.ToApplicationConfig(&oam.Component{Name: "web", Type: kind.component, Properties: tc.props}, coreKindNamespace)
				if err != nil {
					t.Fatalf("ToApplicationConfig: %v", err)
				}
				reader, ok := cfg.(interface {
					FluxNamespaceReads() (configMaps, secrets []string)
				})
				if !ok {
					t.Fatalf("%T reports no FluxNamespaceReads", cfg)
				}
				configMaps, secrets := reader.FluxNamespaceReads()
				if !slices.Equal(configMaps, tc.configMaps) || !slices.Equal(secrets, tc.secrets) {
					t.Errorf("reads ConfigMaps %v and Secrets %v, want %v and %v", configMaps, secrets, tc.configMaps, tc.secrets)
				}
			})
		}
	}
}
