package components

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	notificationv1beta3 "github.com/fluxcd/notification-controller/api/v1beta3"
)

// TestProviderAddressCredentialTypes_InUpstreamEnum: every type
// enforceProviderPolicy names is a value of the Enum marker on the type of a
// ProviderSpec in the linked module's source, so a type the API renamed or
// dropped fails here rather than refusing nothing.
func TestProviderAddressCredentialTypes_InUpstreamEnum(t *testing.T) {
	file := filepath.Join(linkedModuleDir(t, "github.com/fluxcd/notification-controller/api"), "v1beta3", "provider_types.go")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read the Provider types: %v", err)
	}
	marker := regexp.MustCompile(`(?m)^\s*// \+kubebuilder:validation:Enum=(\S+)\n(?:\s*//.*\n)*\s*Type string ` + "`json:\"type\"`")
	m := marker.FindSubmatch(data)
	if m == nil {
		t.Fatalf("%s: no Enum marker on the Type field of ProviderSpec", file)
	}
	enum := strings.Split(string(m[1]), ";")
	for _, typ := range append(slices.Clone(providerAddressCredentialTypes), notificationv1beta3.AzureEventHubProvider) {
		if !slices.Contains(enum, typ) {
			t.Errorf("%q is not a Provider type the API takes: %v", typ, enum)
		}
	}
	// Vacuity guard: the marker read is the list of types, not one value.
	if len(enum) < len(providerAddressCredentialTypes)+1 {
		t.Fatalf("the Enum marker reads as %v", enum)
	}
}
