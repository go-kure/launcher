package builtin

// CertificateRendering defines the platform values for the certificate capability.
// All fields are valid rendering keys; unknown fields are rejected at
// ClusterProfile evaluation time via DecodeStrict.
type CertificateRendering struct {
	// IssuerRefName is the name of the cert-manager issuer. Required.
	IssuerRefName string `yaml:"issuerRefName" json:"issuerRefName"`

	// IssuerRefKind is the kind of the cert-manager issuer.
	// Defaults to "ClusterIssuer" when omitted.
	IssuerRefKind string `yaml:"issuerRefKind,omitempty" json:"issuerRefKind,omitempty"`
}
