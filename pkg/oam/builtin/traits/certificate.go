package traits

import (
	"time"

	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	"github.com/go-kure/kure/pkg/kubernetes/certmanager"
	"github.com/go-kure/kure/pkg/stack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// CertificateHandler handles OAM certificate traits.
type CertificateHandler struct{}

// CanHandle returns true for certificate trait type.
func (h *CertificateHandler) CanHandle(traitType string) bool {
	return traitType == "certificate"
}

// CapabilityRequired returns true: the certificate trait needs issuerRef from
// a ClusterProfile capability and cannot produce a valid Certificate CR without it.
func (h *CertificateHandler) CapabilityRequired() bool { return true }

// ValidateAndApplyDefaults validates and normalises the certificate capability rendering.
func (h *CertificateHandler) ValidateAndApplyDefaults(rendering map[string]any) (map[string]any, error) {
	r, err := builtin.DecodeStrict[builtin.CertificateRendering](rendering)
	if err != nil {
		return nil, errors.Wrap(err, "certificate rendering")
	}
	if r.IssuerRef.Name == "" {
		return nil, errors.New("certificate rendering: issuerRef.name is required")
	}
	kind := r.IssuerRef.Kind
	if kind == "" {
		kind = "ClusterIssuer"
	}
	// Normalise to the nested shape with the kind defaulted in place.
	rendering["issuerRef"] = map[string]any{"name": r.IssuerRef.Name, "kind": kind}
	return rendering, nil
}

// PropertySchema declares the certificate trait's user-facing properties.
func (h *CertificateHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"secretName": {Type: oam.PropertyTypeString, Required: true},
		// issuerRef is capability-injected (CapabilityRequired; validated in
		// ValidateAndApplyDefaults), so the parent is NOT user-required. If a user does
		// supply issuerRef, its name is still required so `issuerRef: {}` fails in schema
		// preflight rather than later in parseProperties.
		"issuerRef": {
			Type: oam.PropertyTypeObject,
			Properties: map[string]oam.PropertySchema{
				"name": {Type: oam.PropertyTypeString, Required: true},
				"kind": {Type: oam.PropertyTypeString, Default: "ClusterIssuer"},
			},
		},
		"dnsNames":    {Type: oam.PropertyTypeArray, Required: true, Items: &oam.PropertySchema{Type: oam.PropertyTypeString}},
		"duration":    {Type: oam.PropertyTypeString, Default: "2160h"},
		"renewBefore": {Type: oam.PropertyTypeString, Default: "360h"},
	}
}

// Apply creates a cert-manager Certificate resource appended to the bundle.
func (h *CertificateHandler) Apply(trait *oam.Trait, app *stack.Application, bundle *stack.Bundle) error {
	config, err := h.parseProperties(trait.Properties, app)
	if err != nil {
		return err
	}

	certApp := stack.NewApplication(
		app.Name+"-certificate",
		app.Namespace,
		config,
	)
	bundle.Applications = append(bundle.Applications, certApp)
	return nil
}

func (h *CertificateHandler) parseProperties(props map[string]any, app *stack.Application) (*CertificateConfig, error) {
	config := &CertificateConfig{
		ComponentName: app.Name,
		Duration:      "2160h",
		RenewBefore:   "360h",
	}

	secretName, ok := props["secretName"].(string)
	if !ok || secretName == "" {
		return nil, errors.New("required property 'secretName' missing or not a string")
	}
	config.SecretName = secretName

	issuerRef, ok := props["issuerRef"].(map[string]any)
	if !ok {
		return nil, errors.New("required property 'issuerRef' missing or not a map")
	}
	issuerName, ok := issuerRef["name"].(string)
	if !ok || issuerName == "" {
		return nil, errors.New("required property 'issuerRef.name' missing or not a string")
	}
	config.IssuerName = issuerName

	config.IssuerKind = "ClusterIssuer"
	if kind, ok := issuerRef["kind"].(string); ok && kind != "" {
		config.IssuerKind = kind
	}

	dnsNamesRaw, ok := props["dnsNames"].([]any)
	if !ok || len(dnsNamesRaw) == 0 {
		return nil, errors.New("required property 'dnsNames' missing or empty")
	}
	dnsNames := make([]string, 0, len(dnsNamesRaw))
	for i, v := range dnsNamesRaw {
		s, ok := v.(string)
		if !ok {
			return nil, errors.Errorf("dnsNames[%d]: expected string, got %T", i, v)
		}
		dnsNames = append(dnsNames, s)
	}
	config.DNSNames = dnsNames

	if dur, ok := props["duration"].(string); ok && dur != "" {
		config.Duration = dur
	}
	if renew, ok := props["renewBefore"].(string); ok && renew != "" {
		config.RenewBefore = renew
	}

	return config, nil
}

// CertificateConfig implements stack.ApplicationConfig for certificate traits.
type CertificateConfig struct {
	SecretName    string
	ComponentName string
	IssuerName    string
	IssuerKind    string
	DNSNames      []string
	Duration      string
	RenewBefore   string
}

// ApplyPolicy is a no-op: certificates have no enforceable policy fields.
func (c *CertificateConfig) ApplyPolicy(_ oam.Policy) error { return nil }

// Generate creates a cert-manager Certificate resource.
func (c *CertificateConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	dur, err := time.ParseDuration(c.Duration)
	if err != nil {
		return nil, errors.Errorf("invalid duration %q: %w", c.Duration, err)
	}
	renewBefore, err := time.ParseDuration(c.RenewBefore)
	if err != nil {
		return nil, errors.Errorf("invalid renewBefore %q: %w", c.RenewBefore, err)
	}

	cert := certmanager.Certificate(&certmanager.CertificateConfig{
		Name:       c.SecretName,
		Namespace:  app.Namespace,
		SecretName: c.SecretName,
		IssuerRef: cmmeta.IssuerReference{
			Name: c.IssuerName,
			Kind: c.IssuerKind,
		},
		DNSNames:    c.DNSNames,
		Duration:    &metav1.Duration{Duration: dur},
		RenewBefore: &metav1.Duration{Duration: renewBefore},
	})

	obj := client.Object(cert)
	return []*client.Object{&obj}, nil
}
