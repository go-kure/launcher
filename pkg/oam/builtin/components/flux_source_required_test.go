package components_test

import (
	"slices"
	"testing"

	"github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"

	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// ociVerifiedSpec is an OCIRepository spec naming every Secret it can read,
// verified by provider ("" for none authored).
func ociVerifiedSpec(provider string) sourcev1.OCIRepositorySpec {
	ref := func(name string) *meta.LocalObjectReference { return &meta.LocalObjectReference{Name: name} }
	return sourcev1.OCIRepositorySpec{
		SecretRef:      ref("pull"),
		CertSecretRef:  ref("tls"),
		ProxySecretRef: ref("proxy"),
		Verify: &sourcev1.OCIRepositoryVerification{
			Provider:             provider,
			SecretRef:            ref("keys"),
			TrustedRootSecretRef: ref("root"),
		},
	}
}

// TestOCIRepositoryConfig_FluxNamespaceReads: the source reports every Secret
// it can read, the verification's included, whatever the provider: each moves
// a trait Secret of that name with it to the Flux namespace.
func TestOCIRepositoryConfig_FluxNamespaceReads(t *testing.T) {
	want := []string{"pull", "tls", "proxy", "keys", "root"}
	for _, provider := range []string{"", "cosign", "notation"} {
		cfg := &components.OCIRepositoryConfig{Spec: ociVerifiedSpec(provider)}
		configMaps, secrets := cfg.FluxNamespaceReads()
		if len(configMaps) != 0 || !slices.Equal(secrets, want) {
			t.Errorf("provider %q: reads ConfigMaps %v and Secrets %v, want none and %v", provider, configMaps, secrets, want)
		}
	}
}

// TestFluxSourceRequiredSecrets: an OCIRepository requires its trustedRoot
// Secret only under the cosign provider, the one an omitted provider is
// generated with, and every other Secret it names under any provider; a
// HelmRepository requires its secretRef and certSecretRef (go-kure/launcher#1039).
func TestFluxSourceRequiredSecrets(t *testing.T) {
	for _, tc := range []struct {
		provider string
		want     []string
	}{
		{"", []string{"pull", "tls", "proxy", "keys", "root"}},
		{"cosign", []string{"pull", "tls", "proxy", "keys", "root"}},
		{"notation", []string{"pull", "tls", "proxy", "keys"}},
	} {
		cfg := &components.OCIRepositoryConfig{Spec: ociVerifiedSpec(tc.provider)}
		if got := cfg.FluxSourceRequiredSecrets(); !slices.Equal(got, tc.want) {
			t.Errorf("OCIRepository, provider %q: FluxSourceRequiredSecrets = %v, want %v", tc.provider, got, tc.want)
		}
	}
	unverified := &components.OCIRepositoryConfig{Spec: sourcev1.OCIRepositorySpec{SecretRef: &meta.LocalObjectReference{Name: "pull"}}}
	if got := unverified.FluxSourceRequiredSecrets(); !slices.Equal(got, []string{"pull"}) {
		t.Errorf("unverified OCIRepository: FluxSourceRequiredSecrets = %v, want [pull]", got)
	}
	helm := &components.HelmRepositoryConfig{Spec: sourcev1.HelmRepositorySpec{
		SecretRef:     &meta.LocalObjectReference{Name: "auth"},
		CertSecretRef: &meta.LocalObjectReference{Name: "tls"},
	}}
	if got := helm.FluxSourceRequiredSecrets(); !slices.Equal(got, []string{"auth", "tls"}) {
		t.Errorf("HelmRepository: FluxSourceRequiredSecrets = %v, want [auth tls]", got)
	}
}
