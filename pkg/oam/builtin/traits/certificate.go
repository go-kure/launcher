package traits

import (
	"time"

	certv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
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
		"secretName": {Type: oam.PropertyTypeString, Required: true, Description: "Name of the Secret that stores the issued certificate and its private key."},
		// issuerRef is capability-injected (CapabilityRequired; validated in
		// ValidateAndApplyDefaults), so the parent is NOT user-required. If a user does
		// supply issuerRef, its name is still required so `issuerRef: {}` fails in schema
		// preflight rather than later in parseProperties.
		"issuerRef": {
			Type:        oam.PropertyTypeObject,
			Description: "Reference to the cert-manager issuer that signs the certificate (normally capability-injected).",
			Properties: map[string]oam.PropertySchema{
				"name": {Type: oam.PropertyTypeString, Required: true, Description: "Name of the issuer to sign the certificate."},
				"kind": {Type: oam.PropertyTypeString, Default: "ClusterIssuer", Description: "Issuer kind (Issuer or ClusterIssuer)."},
			},
		},
		"dnsNames":    {Type: oam.PropertyTypeArray, Required: true, Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A DNS name (SAN) to include in the certificate."}, Description: "DNS names the certificate is issued for."},
		"duration":    {Type: oam.PropertyTypeString, Default: "2160h", Description: "Total validity duration of the certificate as a Go duration (e.g. 2160h)."},
		"renewBefore": {Type: oam.PropertyTypeString, Default: "360h", Description: "How long before expiry cert-manager renews the certificate, as a Go duration."},
		// privateKey is user-authored (not capability-injected) and optional; when
		// omitted cert-manager applies its own defaults (RSA 2048). algorithm-specific
		// size validation is enforced in parseProperties, not the schema.
		"privateKey": {
			Type:        oam.PropertyTypeObject,
			Description: "Private key options; when omitted cert-manager applies its own defaults (RSA 2048).",
			Properties: map[string]oam.PropertySchema{
				"algorithm":      {Type: oam.PropertyTypeString, Enum: []any{"RSA", "ECDSA", "Ed25519"}, Description: "Private key algorithm (RSA, ECDSA, or Ed25519)."},
				"size":           {Type: oam.PropertyTypeInteger, Description: "Private key size in bits; valid values depend on the algorithm."},
				"encoding":       {Type: oam.PropertyTypeString, Enum: []any{"PKCS1", "PKCS8"}, Description: "Private key encoding (PKCS1 or PKCS8)."},
				"rotationPolicy": {Type: oam.PropertyTypeString, Enum: []any{"Never", "Always"}, Description: "Whether cert-manager regenerates the private key on renewal (Never or Always)."},
			},
		},
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

	if err := parsePrivateKey(props, config); err != nil {
		return nil, err
	}

	return config, nil
}

// parsePrivateKey reads the optional privateKey block into config, validating
// algorithm-specific size constraints so launcher rejects shapes cert-manager
// would later reject. The block and every field are optional; omitted fields
// leave cert-manager's own defaults in place.
func parsePrivateKey(props map[string]any, config *CertificateConfig) error {
	pk, ok := props["privateKey"].(map[string]any)
	if !ok {
		return nil
	}

	if alg, ok := pk["algorithm"].(string); ok && alg != "" {
		if alg != "RSA" && alg != "ECDSA" && alg != "Ed25519" {
			return errors.Errorf("privateKey.algorithm %q is invalid (allowed: RSA, ECDSA, Ed25519)", alg)
		}
		config.PKAlgorithm = alg
	}
	if enc, ok := pk["encoding"].(string); ok && enc != "" {
		if enc != "PKCS1" && enc != "PKCS8" {
			return errors.Errorf("privateKey.encoding %q is invalid (allowed: PKCS1, PKCS8)", enc)
		}
		config.PKEncoding = enc
	}
	if rot, ok := pk["rotationPolicy"].(string); ok && rot != "" {
		if rot != "Never" && rot != "Always" {
			return errors.Errorf("privateKey.rotationPolicy %q is invalid (allowed: Never, Always)", rot)
		}
		config.PKRotationPolicy = rot
	}

	if raw, exists := pk["size"]; exists {
		size, ok := toInt32ForScaler(raw)
		if !ok {
			return errors.New("privateKey.size must be a whole number")
		}
		if size <= 0 {
			return errors.Errorf("privateKey.size must be positive, got %d", size)
		}
		config.PKSize = int(size)
	}

	if config.PKSize != 0 {
		// A bare size (no algorithm) is ambiguous: cert-manager would default to
		// RSA, where ECDSA sizes are invalid. Require an explicit algorithm.
		if config.PKAlgorithm == "" {
			return errors.New("privateKey.size requires privateKey.algorithm to be set")
		}
		if err := validatePrivateKeySize(config.PKAlgorithm, config.PKSize); err != nil {
			return err
		}
	}

	return nil
}

// validatePrivateKeySize enforces cert-manager's per-algorithm key sizes.
func validatePrivateKeySize(algorithm string, size int) error {
	switch algorithm {
	case "RSA":
		if size != 2048 && size != 4096 && size != 8192 {
			return errors.Errorf("privateKey.size %d is invalid for RSA (allowed: 2048, 4096, 8192)", size)
		}
	case "ECDSA":
		if size != 256 && size != 384 && size != 521 {
			return errors.Errorf("privateKey.size %d is invalid for ECDSA (allowed: 256, 384, 521)", size)
		}
	case "Ed25519":
		return errors.New("privateKey.size must not be set for Ed25519 (key size is fixed)")
	}
	return nil
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

	// PrivateKey options; zero values leave cert-manager's defaults in place.
	PKAlgorithm      string
	PKSize           int
	PKEncoding       string
	PKRotationPolicy string
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

	// The kure helper cannot carry privateKey; set it directly. Only populate the
	// sub-fields the user authored so cert-manager defaults the rest.
	if c.PKAlgorithm != "" || c.PKSize != 0 || c.PKEncoding != "" || c.PKRotationPolicy != "" {
		cert.Spec.PrivateKey = &certv1.CertificatePrivateKey{
			Algorithm:      certv1.PrivateKeyAlgorithm(c.PKAlgorithm),
			Size:           c.PKSize,
			Encoding:       certv1.PrivateKeyEncoding(c.PKEncoding),
			RotationPolicy: certv1.PrivateKeyRotationPolicy(c.PKRotationPolicy),
		}
	}

	obj := client.Object(cert)
	return []*client.Object{&obj}, nil
}
