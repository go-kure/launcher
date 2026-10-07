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

// fluxReceiverResource is one resource of a fluxcd-receiver, of another
// namespace, with every field a resource takes.
func fluxReceiverResource() map[string]any {
	return map[string]any{
		"apiVersion":  "source.toolkit.fluxcd.io/v1",
		"kind":        "GitRepository",
		"name":        "*",
		"namespace":   "fleet",
		"matchLabels": map[string]any{"team": "shop"},
		"filter":      "res.metadata.name.startsWith('shop-')",
	}
}

// fluxReceiverMinimal is the least a fluxcd-receiver may author.
func fluxReceiverMinimal() map[string]any {
	return map[string]any{
		"type":      "github",
		"resources": []any{map[string]any{"kind": "GitRepository", "name": "fleet"}},
	}
}

// fluxReceiverFull sets every field of a ReceiverSpec. It breaks two of the
// API's expression rules, which tie secretRef and oidcProviders to the type:
// the kind checks none of them (fluxRulesLeft), so it builds. Its Secret is
// read from the namespace the object lands in.
func fluxReceiverFull() map[string]any {
	return map[string]any{
		"type":           "generic-oidc",
		"interval":       "5m",
		"events":         []any{"push"},
		"resources":      []any{fluxReceiverResource()},
		"resourceFilter": "req.ref == 'refs/heads/main'",
		"secretRef":      map[string]any{"name": "webhook-token"},
		"oidcProviders": []any{map[string]any{
			"issuerURL":   "https://token.actions.example",
			"audience":    "fleet",
			"variables":   []any{map[string]any{"name": "owner", "expression": "claims.repository_owner"}},
			"validations": []any{map[string]any{"expression": "vars.owner == 'shop'", "message": "not the shop's repository"}},
		}},
		"suspend": true,
	}
}

// resourceSetInputProviderMinimal is the least a resourcesetinputprovider may
// author: a Static provider names no url.
func resourceSetInputProviderMinimal() map[string]any {
	return map[string]any{"type": "Static"}
}

// resourceSetInputProviderFull sets every field of a
// ResourceSetInputProviderSpec. It breaks one of the API's expression rules,
// which refuses selectors on a provider of another type than ExternalArtifact:
// the kind checks none of them (fluxRulesLeft), so it builds. Its url names
// the registry the strict policy allows, its second selector lists another
// namespace and its first every namespace, and its two Secrets are read from
// the namespace the object lands in.
func resourceSetInputProviderFull() map[string]any {
	return map[string]any{
		"type":               "OCIArtifactTag",
		"url":                "oci://registry.example/shop/app",
		"serviceAccountName": "inputs-reader",
		"secretRef":          map[string]any{"name": "provider-credentials"},
		"certSecretRef":      map[string]any{"name": "provider-tls"},
		"insecure":           true,
		"defaultValues":      map[string]any{"env": "staging", "replicas": 2},
		"filter": map[string]any{
			"includeBranch": "^feat/", "excludeBranch": "^feat/wip-", "includeTag": "^v", "excludeTag": "-rc",
			"includeEnvironment": "^prod", "excludeEnvironment": "-old",
			"labels": []any{"deploy/preview"}, "limit": 10, "semver": ">=1.0.0",
		},
		"skip":     map[string]any{"labels": []any{"!deploy/preview"}},
		"schedule": []any{map[string]any{"cron": "0 * * * *", "timeZone": "Europe/Brussels", "window": "1h"}},
		"selectors": []any{
			map[string]any{"name": "bundle", "namespace": "*"},
			map[string]any{
				"matchLabels":      map[string]any{"team": "shop"},
				"matchExpressions": []any{map[string]any{"key": "tier", "operator": "In", "values": []any{"web"}}},
				"namespace":        "fleet",
			},
		},
	}
}

// fluxProviderMinimal is the least a fluxcd-provider may author.
func fluxProviderMinimal() map[string]any {
	return map[string]any{"type": "slack"}
}

// fluxProviderFull sets every field of a ProviderSpec. Its address and its
// proxy name hosts no fixture's policy allows, and its three Secrets are read
// from the namespace the object lands in.
func fluxProviderFull() map[string]any {
	return map[string]any{
		"type":               "github",
		"interval":           "10m",
		"channel":            "releases",
		"username":           "flux",
		"address":            "https://github.other.example/shop/fleet",
		"timeout":            "20s",
		"proxy":              "http://proxy.other.example:3128",
		"proxySecretRef":     map[string]any{"name": "provider-proxy"},
		"secretRef":          map[string]any{"name": "provider-token"},
		"serviceAccountName": "notifier",
		"certSecretRef":      map[string]any{"name": "provider-tls"},
		"suspend":            true,
		"commitStatusExpr":   "(event.involvedObject.kind + '/' + event.involvedObject.name)",
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
	want := []string{"artifactgenerator", "fluxcd-alert", "fluxcd-provider", "fluxcd-receiver", "imagepolicy", "imagerepository", "imageupdateautomation", "resourcesetinputprovider"}
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
	// Vacuity guard: these are the kinds the policy does not reach.
	slices.Sort(unheld)
	if want := []string{"artifactgenerator", "fluxcd-alert", "fluxcd-provider", "fluxcd-receiver", "imagepolicy", "imageupdateautomation"}; !slices.Equal(unheld, want) {
		t.Fatalf("the Flux kinds the policy does not reach are %v, want %v", unheld, want)
	}
}

// TestFluxProvider_RefusesAUserOrPassword: a user or a password in a
// fluxcd-provider's `address` or `proxy` is refused when the component is
// read, so under every policy and under none, and the refusal does not repeat
// the value. An address that is no URL with a host is written as authored
// unless it holds an `@`, and so is a credential in a path or a query, which
// the kind cannot tell from a path.
func TestFluxProvider_RefusesAUserOrPassword(t *testing.T) {
	const secret = "s3cr3t-token"
	cases := []struct {
		name, field, value string
		wantErr            string
	}{
		{"a user and a password in the address", "address", "https://bot:" + secret + "@hooks.example/services", "fluxcd-provider: address must not carry a user or password"},
		{"a user alone in the address", "address", "https://" + secret + "@hooks.example/services", "fluxcd-provider: address must not carry a user or password"},
		{"an address with an @ that is no URL", "address", "https://bot:" + secret + "%zz@hooks.example", "fluxcd-provider: address holds an @ and is no URL with a host"},
		{"a user and a password in an address with no scheme", "address", "bot:" + secret + "@hooks.example", "fluxcd-provider: address holds an @ and is no URL with a host"},
		{"a user in an address with no scheme", "address", secret + "@hooks.example", "fluxcd-provider: address holds an @ and is no URL with a host"},
		{"a user in a URL with no host", "address", "https://bot:" + secret + "@/services", "fluxcd-provider: address holds an @ and is no URL with a host"},
		{"an @ in the path of a URL", "address", "https://hooks.example/team@shop", ""},
		{"a user and a password in the proxy", "proxy", "http://bot:" + secret + "@proxy.example:3128", "fluxcd-provider: proxy must not carry a user or password"},
		{"a proxy that is no URL", "proxy", "http://proxy.example:" + secret, "fluxcd-provider: proxy is not a valid URL"},
		{"an address with no user", "address", "https://hooks.example/services", ""},
		{"a project ID", "address", "shop-fleet", ""},
		{"a host and a port that parse as no URL", "address", "10.0.0.7:4222", ""},
		{"a token in the path", "address", "https://hooks.example/services/" + secret, ""},
		{"a token in the query", "address", "https://hooks.example/notify?token=" + secret, ""},
		{"a proxy with no user", "proxy", "http://proxy.example:3128", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := withProperty(fluxProviderMinimal(), tc.field, tc.value)
			_, err := (&components.FluxcdProviderHandler{}).ToApplicationConfig(&oam.Component{Name: "slack", Type: "fluxcd-provider", Properties: props}, coreKindNamespace)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("ToApplicationConfig = %v, want no refusal", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ToApplicationConfig = %v, want an error that says %q", err, tc.wantErr)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("the refusal repeats the credential: %v", err)
			}
		})
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

// TestResourceSetInputProvider_URLIsHeldToAllowedRegistries: the host of a
// resourcesetinputprovider's `url` is held to the allowed registries, whatever
// the type: an http(s) url by the host rule of the Flux sources, an oci:// url
// by the OCI host rule, which wants its registry named explicitly. An unset
// url names no host, and with no policy, or one that lists no registry,
// nothing is refused.
func TestResourceSetInputProvider_URLIsHeldToAllowedRegistries(t *testing.T) {
	allowing := func(registries ...string) oam.Policy {
		return esPolicy{stubPolicy: &stubPolicy{allowedRegistries: registries}}
	}
	cases := []struct {
		name, typ, url string
		policy         oam.Policy
		refused        string
	}{
		{"a Git host that is allowed", "GitHubPullRequest", "https://git.example/shop/fleet", allowing("git.example"), ""},
		{"another Git host", "GitHubPullRequest", "https://git.other.example/shop/fleet", allowing("git.example"), "source registry"},
		{"the allowed host on another port", "GitLabBranch", "https://git.example:8443/shop/fleet", allowing("git.example"), "source registry"},
		{"an ExternalService host that is allowed", "ExternalService", "https://inputs.example/api", allowing("inputs.example"), ""},
		{"another ExternalService host", "ExternalService", "http://inputs.other.example/api", allowing("inputs.example"), "source registry"},
		{"a type a later operator version adds", "SomeNewProvider", "https://inputs.other.example", allowing("inputs.example"), "source registry"},
		{"an OCI registry that is allowed", "OCIArtifactTag", "oci://registry.example/shop/app", allowing("registry.example"), ""},
		{"another OCI registry", "ACRArtifactTag", "oci://registry.other.example/shop/app", allowing("registry.example"), "source registry"},
		{"an OCI url whose registry is not explicit", "OCIArtifactTag", "oci://registry/shop/app", allowing("registry"), "does not name its registry explicitly"},
		{"an OCI url with no repository", "OCIArtifactTag", "oci://registry.example", allowing("registry.example"), "does not name its registry explicitly"},
		{"a Static provider", "Static", "", allowing("registry.example"), ""},
		{"a policy that lists no registry", "GitHubPullRequest", "https://git.other.example/shop/fleet", allowing(), ""},
		{"no policy", "GitHubPullRequest", "https://git.other.example/shop/fleet", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := map[string]any{"type": tc.typ}
			if tc.url != "" {
				props["url"] = tc.url
			}
			cfg, err := (&components.ResourceSetInputProviderHandler{}).ToApplicationConfig(&oam.Component{Name: "inputs", Type: "resourcesetinputprovider", Properties: props}, coreKindNamespace)
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			err = cfg.(policyApplier).ApplyPolicy(tc.policy)
			if tc.refused == "" {
				if err != nil {
					t.Errorf("ApplyPolicy = %v, want no refusal", err)
				}
				return
			}
			var refusal *oam.PolicyRefusal
			if !errors.As(err, &refusal) || refusal.Class != oam.RefusalRegistry || !strings.Contains(err.Error(), "resourcesetinputprovider: url") || !strings.Contains(err.Error(), tc.refused) {
				t.Errorf("ApplyPolicy = %v, want a refusal of class %q of url that says %q", err, oam.RefusalRegistry, tc.refused)
			}
		})
	}
}

// TestResourceSetInputProvider_RefusesAUserOrPassword: a user or a password in
// a resourcesetinputprovider's `url` is refused when the component is read, so
// under every policy and under none, and the refusal does not repeat the
// value. A token in a path or a query is not something the kind can tell.
func TestResourceSetInputProvider_RefusesAUserOrPassword(t *testing.T) {
	const secret = "s3cr3t-token"
	cases := []struct {
		name, url, wantErr string
	}{
		{"a user and a password in an https url", "https://bot:" + secret + "@git.example/shop/fleet", "resourcesetinputprovider: url must not carry a user or password"},
		{"a user alone in an oci url", "oci://" + secret + "@registry.example/shop/app", "resourcesetinputprovider: url must not carry a user or password"},
		{"a url that is no URL", "https://git.example:" + secret, "resourcesetinputprovider: url is not a valid URL"},
		{"a url with no user", "https://git.example/shop/fleet", ""},
		{"a token in the query", "https://inputs.example/api?token=" + secret, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := map[string]any{"type": "ExternalService", "url": tc.url}
			_, err := (&components.ResourceSetInputProviderHandler{}).ToApplicationConfig(&oam.Component{Name: "inputs", Type: "resourcesetinputprovider", Properties: props}, coreKindNamespace)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("ToApplicationConfig = %v, want no refusal", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ToApplicationConfig = %v, want an error that says %q", err, tc.wantErr)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("the refusal repeats the credential: %v", err)
			}
		})
	}
}

// TestResourceSetInputProvider_ScheduleWindowIsCheckedAsAuthored: a
// schedule's `window` is a duration under a list, held to its pattern by its
// authored text in every schedule, and written as the type writes it.
func TestResourceSetInputProvider_ScheduleWindowIsCheckedAsAuthored(t *testing.T) {
	schedule := func(windows ...string) map[string]any {
		var items []any
		for _, w := range windows {
			items = append(items, map[string]any{"cron": "0 * * * *", "window": w})
		}
		return withProperty(resourceSetInputProviderMinimal(), "schedule", items)
	}
	_, err := (&components.ResourceSetInputProviderHandler{}).ToApplicationConfig(&oam.Component{Name: "inputs", Type: "resourcesetinputprovider", Properties: schedule("1h", "500us")}, coreKindNamespace)
	if want := `resourcesetinputprovider: schedule[].window "500us" is invalid: must be a Flux duration`; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("a second schedule's window of 500us: err = %v, want %q", err, want)
	}

	objs, err := fluxKindTransform(fluxKindRow(t, "resourcesetinputprovider"), "flux-system", oam.Component{Name: "inputs", Properties: schedule("90m", "30s")})
	if err != nil || len(objs) != 1 {
		t.Fatalf("transform = %d objects, %v; want one", len(objs), err)
	}
	spec, _ := policyFreeJSON(t, objs[0])["spec"].(map[string]any)
	items, _ := spec["schedule"].([]any)
	var got []any
	for _, item := range items {
		got = append(got, item.(map[string]any)["window"])
	}
	if want := []any{"1h30m0s", "30s"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the schedules' windows = %v, want %v", got, want)
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
		// A Provider reads the Secrets of its credentials, of its proxy and of
		// its certificates.
		"fluxcd-provider": {
			{props: fluxProviderFull(), secrets: []string{"provider-token", "provider-proxy", "provider-tls"}},
			{props: fluxProviderMinimal()},
		},
		// A Receiver reads the Secret of the token a request is validated
		// with.
		"fluxcd-receiver": {
			{props: fluxReceiverFull(), secrets: []string{"webhook-token"}},
			{props: fluxReceiverMinimal()},
		},
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
		// A ResourceSetInputProvider reads the Secrets of the provider's
		// credentials and of its certificates. The ExternalArtifacts its
		// selectors list are no ConfigMap and no Secret.
		"resourcesetinputprovider": {
			{props: resourceSetInputProviderFull(), secrets: []string{"provider-credentials", "provider-tls"}},
			{props: resourceSetInputProviderMinimal()},
		},
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
