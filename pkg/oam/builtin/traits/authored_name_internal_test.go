package traits

import (
	"maps"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
)

// appliedSubApplicationName applies a trait to component "web" and returns the
// name of the one sub-application it adds, which is the name of its object.
func appliedSubApplicationName(h oam.TraitHandler, props map[string]any) (string, error) {
	bundle := &stack.Bundle{}
	app := stack.NewApplication("web", "ns", &mockServicePortConfig{port: 80})
	if err := h.Apply(&oam.Trait{Properties: props}, app, bundle); err != nil {
		return "", err
	}
	return bundle.Applications[len(bundle.Applications)-1].Name, nil
}

// with returns a copy of props with key set to value.
func with(props map[string]any, key, value string) map[string]any {
	out := maps.Clone(props)
	out[key] = value
	return out
}

// TestAuthoredObjectName_UsedAsWrittenOrRefused covers every trait property
// whose authored value becomes an object's name (go-kure/launcher#787): a name
// that is a valid object name is used as written, at the full 253 characters
// too; one that is too long or has a character an object name cannot hold is
// refused with the property in the error, never shortened or passed on for the
// API server to refuse. An authored empty string is refused as well.
func TestAuthoredObjectName_UsedAsWrittenOrRefused(t *testing.T) {
	app := stack.NewApplication("web", "ns", nil)
	volsync := map[string]any{"sourcePVC": "data", "schedule": "0 3 * * *"}
	certificate := map[string]any{
		"secretName": "tls",
		"issuerRef":  map[string]any{"name": "ca"},
		"dnsNames":   []any{"example.com"},
	}
	externalSecret := map[string]any{
		"secretName": "creds",
		"provider":   "vault",
		"remoteRef":  map[string]any{"key": "prod/db"},
	}
	cilium := map[string]any{
		"endpointSelector": map[string]any{},
		"egress":           []any{map[string]any{"toEntities": []any{"world"}}},
	}

	sites := []struct {
		site     string
		property string
		use      func(value string) (string, error)
	}{
		{"volsync repository Secret", "repository", func(v string) (string, error) {
			c, err := (&VolSyncHandler{}).parseProperties(with(volsync, "repository", v), app)
			if err != nil {
				return "", err
			}
			return c.Repository, nil
		}},
		{"volsync source claim", "sourcePVC", func(v string) (string, error) {
			c, err := (&VolSyncHandler{}).parseProperties(with(volsync, "sourcePVC", v), app)
			if err != nil {
				return "", err
			}
			return c.SourcePVC, nil
		}},
		{"certificate Certificate and Secret", "secretName", func(v string) (string, error) {
			c, err := (&CertificateHandler{}).parseProperties(with(certificate, "secretName", v), app)
			if err != nil {
				return "", err
			}
			return c.SecretName, nil
		}},
		{"external-secret ExternalSecret", "secretName", func(v string) (string, error) {
			c, err := (&ExternalSecretHandler{}).parseProperties(with(externalSecret, "secretName", v), app)
			if err != nil {
				return "", err
			}
			return c.SecretName, nil
		}},
		{"external-secret produced Secret", "targetSecretName", func(v string) (string, error) {
			c, err := (&ExternalSecretHandler{}).parseProperties(with(externalSecret, "targetSecretName", v), app)
			if err != nil {
				return "", err
			}
			return c.TargetSecretName, nil
		}},
		{"cilium-networkpolicy CiliumNetworkPolicy", "name", func(v string) (string, error) {
			c, err := (&CiliumNetworkPolicyHandler{}).parseProperties(with(cilium, "name", v), app)
			if err != nil {
				return "", err
			}
			return c.Name, nil
		}},
		{"configmap ConfigMap", "name", func(v string) (string, error) {
			return appliedSubApplicationName(&ConfigMapHandler{},
				map[string]any{"name": v, "data": map[string]any{"key": "val"}})
		}},
		{"ingress Ingress", "name", func(v string) (string, error) {
			return appliedSubApplicationName(&IngressHandler{}, with(scopedIngressProps(""), "name", v))
		}},
		{"httproute HTTPRoute", "name", func(v string) (string, error) {
			return appliedSubApplicationName(&HTTPRouteHandler{}, with(scopedHTTPRouteProps(""), "name", v))
		}},
	}

	fits := strings.Repeat("a", oam.ShortenLimitSubdomain)
	refused := map[string]string{
		"one character too long":    fits + "a",
		"an upper-case character":   "Web",
		"an underscore":             "web_tls",
		"a trailing hyphen":         "web-",
		"a slash":                   "team/web",
		"an empty DNS label (a..b)": "a..b",
	}
	for _, s := range sites {
		t.Run(s.site, func(t *testing.T) {
			for _, name := range []string{"web-tls", "a.b", fits} {
				got, err := s.use(name)
				if err != nil {
					t.Errorf("%s = %q: %v, want it accepted", s.property, name, err)
				} else if got != name {
					t.Errorf("%s = %q was used as %q, want it as written", s.property, name, got)
				}
			}
			for why, name := range refused {
				_, err := s.use(name)
				if err == nil {
					t.Errorf("%s with %s (%q) was accepted, want it refused", s.property, why, name)
					continue
				}
				if !strings.Contains(err.Error(), s.property+" ") || !strings.Contains(err.Error(), "DNS-1123 subdomain") {
					t.Errorf("%s with %s: error %q, want it to name the property and the rule", s.property, why, err)
				}
			}
			// The empty string is authored too: it is refused, never read
			// as a request for the default name.
			if got, err := s.use(""); err == nil {
				t.Errorf("%s = \"\" was accepted and gave %q, want it refused", s.property, got)
			} else if !strings.Contains(err.Error(), s.property) {
				t.Errorf("%s = \"\": error %q, want it to name the property", s.property, err)
			}
		})
	}
}

// A routing trait's scope is authored and ends the generated object name. The
// name is shortened when it is too long, which would hide an invalid character
// of the scope behind the digest, so the scope's characters are checked on the
// name as built. Its length is not: TestRoutingObjectName_OversizedScope.
func TestRoutingScope_InvalidCharacterRefused(t *testing.T) {
	handlers := map[string]struct {
		h     oam.TraitHandler
		props func(scope string) map[string]any
	}{
		"ingress":   {&IngressHandler{}, scopedIngressProps},
		"httproute": {&HTTPRouteHandler{}, scopedHTTPRouteProps},
	}
	for kind, tc := range handlers {
		for _, scope := range []string{"External", "ext_1", "external-", strings.Repeat("s", 300) + "_"} {
			_, err := appliedSubApplicationName(tc.h, tc.props(scope))
			if err == nil {
				t.Errorf("%s: scope %q was accepted, want it refused", kind, scope)
				continue
			}
			if !strings.Contains(err.Error(), "scope ") || !strings.Contains(err.Error(), "DNS-1123 subdomain") {
				t.Errorf("%s: scope %q: error %q, want it to name the property and the rule", kind, scope, err)
			}
		}
		if got, err := appliedSubApplicationName(tc.h, tc.props("external")); err != nil || got != "web-"+kind+"-external" {
			t.Errorf("%s: scope \"external\" = (%q, %v), want web-%s-external", kind, got, err, kind)
		}
		// Beside an authored name the scope is no part of any name: it only
		// selects a capability binding, and is not checked as a name.
		if got, err := appliedSubApplicationName(tc.h, with(tc.props("External_1"), "name", "edge")); err != nil || got != "edge" {
			t.Errorf("%s: scope \"External_1\" beside name \"edge\" = (%q, %v), want edge", kind, got, err)
		}
	}
}
