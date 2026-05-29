package builtin

// CertificateRendering defines the platform values for the certificate capability.
// All fields are valid rendering keys; unknown fields are rejected at
// ClusterProfile evaluation time via DecodeStrict.
type CertificateRendering struct {
	// IssuerRef is the cert-manager issuer reference (nested, mirroring
	// cert-manager's own IssuerRef shape). Required.
	IssuerRef CertificateIssuerRef `yaml:"issuerRef" json:"issuerRef"`
}

// CertificateIssuerRef is the nested issuer reference: {name, kind}.
type CertificateIssuerRef struct {
	// Name is the cert-manager issuer name. Required.
	Name string `yaml:"name" json:"name"`

	// Kind is the cert-manager issuer kind. Defaults to "ClusterIssuer" when omitted.
	Kind string `yaml:"kind,omitempty" json:"kind,omitempty"`
}
