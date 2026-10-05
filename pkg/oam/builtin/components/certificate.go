package components

import (
	certv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/go-kure/kure/pkg/kubernetes/certmanager"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// CertificateHandler handles OAM certificate components: the kind-named
// projection of a cert-manager.io/v1 Certificate (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of certv1.CertificateSpec,
// under their json names, decoded strictly (decodeKindSpec). It emits the
// Certificate, named after the component unless `objectName` names it, in the
// build namespace, and nothing else: cert-manager creates the Secret
// `secretName` names. `issuerRef` is the author's: launcher points it at no
// component and does not check that the issuer exists. The `certificate` trait
// is the Certificate launcher derives for a workload; this kind is the authored
// object. TestCoreKindSchemas_CoverSpec keeps the published key set equal to
// the upstream json tags.
type CertificateHandler struct{}

// CanHandle returns true for the certificate component type.
func (h *CertificateHandler) CanHandle(componentType string) bool {
	return componentType == "certificate"
}

// PropertySchema declares every top-level certv1.CertificateSpec field by its
// json name. Structured fields are open objects whose content is checked by
// the strict decode, not by this schema.
func (h *CertificateHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "Certificate spec."
	const decoded = " Decoded strictly into cert-manager's API type: see "
	names := func(what string) *oam.PropertySchema {
		return &oam.PropertySchema{Type: oam.PropertyTypeString, Description: what}
	}
	return map[string]oam.PropertySchema{
		"subject": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "subject: the requested X.509 subject attributes other than the common name: organizations, countries, organizationalUnits, localities, provinces, streetAddresses, postalCodes, serialNumber. Not together with literalSubject." + decoded + "X509Subject in its API reference.",
		},
		"literalSubject": {
			Type:        oam.PropertyTypeString,
			Description: spec + "literalSubject: the requested X.509 subject as an LDAP distinguished name, which fixes the order of its attributes (\"CN=foo,DC=corp,DC=example,DC=com\"). Not together with subject or commonName.",
		},
		"commonName": {
			Type:        oam.PropertyTypeString,
			Description: spec + "commonName: the requested common name of the subject, of at most 64 characters. A TLS client ignores it when a subject alternative name is set. Not together with literalSubject.",
		},
		"duration": {
			Type:        oam.PropertyTypeString,
			Description: spec + "duration: the requested lifetime of the certificate, as a Go duration (\"2160h\"), at least 1h; the issuer may ignore it. Unset, cert-manager requests 90 days. The object carries it in Go's own spelling (2160h0m0s).",
		},
		"renewBefore": {
			Type:        oam.PropertyTypeString,
			Description: spec + "renewBefore: how long before the issued certificate expires cert-manager renews it, as a Go duration (\"360h\"), at least 5m. Unset, a third of the certificate's lifetime. Not together with renewBeforePercentage. The object carries it in Go's own spelling.",
		},
		"renewBeforePercentage": {
			Type:        oam.PropertyTypeInteger,
			Description: spec + "renewBeforePercentage: renewBefore as a percentage of the issued certificate's lifetime that remains when it is renewed, between 1 and 99. Not together with renewBefore.",
		},
		"renewal": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "renewal: how the certificate is renewed: policy (RenewBefore or Disabled) and windows, each with a cron expression and a windowDuration, both required, and a timezone." + decoded + "CertificateRenewal in its API reference.",
		},
		"dnsNames": {
			Type:        oam.PropertyTypeArray,
			Description: spec + "dnsNames: the requested DNS subject alternative names.",
			Items:       names("One DNS name."),
		},
		"ipAddresses": {
			Type:        oam.PropertyTypeArray,
			Description: spec + "ipAddresses: the requested IP address subject alternative names.",
			Items:       names("One IP address."),
		},
		"uris": {
			Type:        oam.PropertyTypeArray,
			Description: spec + "uris: the requested URI subject alternative names.",
			Items:       names("One URI."),
		},
		"otherNames": {
			Type:        oam.PropertyTypeArray,
			Description: spec + "otherNames: the requested otherName subject alternative names, of a UTF-8 string type.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One otherName: oid (the object identifier, as a dotted string) and utf8Value." + decoded + "OtherName in its API reference.",
			},
		},
		"emailAddresses": {
			Type:        oam.PropertyTypeArray,
			Description: spec + "emailAddresses: the requested email subject alternative names.",
			Items:       names("One email address."),
		},
		"secretName": {
			Type: oam.PropertyTypeString, Required: true,
			Description: "Required. " + spec + "secretName: the name of the Secret, in the Certificate's namespace, that cert-manager creates and keeps the private key and the signed certificate in.",
		},
		"secretTemplate": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "secretTemplate: the annotations and labels copied to the Certificate's Secret." + decoded + "CertificateSecretTemplate in its API reference.",
		},
		"keystores": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "keystores: the keystores written into the Certificate's Secret beside the PEM files: jks and pkcs12, each with create (required) and the password that encrypts it, as passwordSecretRef (the key of a Secret) or as password (the password itself, in the object). A password is refused under an EnvironmentPolicy that forbids explicit secrets." + decoded + "CertificateKeystores in its API reference.",
		},
		"issuerRef": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true, Required: true,
			Description: "Required. " + spec + "issuerRef: the issuer that signs the certificate: name (required), kind (Issuer, the default, or ClusterIssuer) and group (cert-manager.io by default). An Issuer is one of the Certificate's namespace." + decoded + "IssuerReference in its API reference.",
		},
		"isCA": {
			Type:        oam.PropertyTypeBoolean,
			Description: spec + "isCA: whether the certificate is requested as a certificate authority; true adds the `cert sign` usage. The issuer may ignore it.",
		},
		"usages": {
			Type:        oam.PropertyTypeArray,
			Description: spec + "usages: the requested key usages and extended key usages. Unset, cert-manager requests `digital signature` and `key encipherment`.",
			Items:       names("One usage, as cert-manager names it (\"server auth\", \"client auth\", \"digital signature\")."),
		},
		"privateKey": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "privateKey: the private key's options: algorithm (RSA, ECDSA or Ed25519), size, encoding (PKCS1 or PKCS8) and rotationPolicy (Always, the default since cert-manager v1.18, or Never)." + decoded + "CertificatePrivateKey in its API reference.",
		},
		"signatureAlgorithm": {
			Type:        oam.PropertyTypeString,
			Description: spec + "signatureAlgorithm: the signature algorithm to request, one that fits the key: SHA256WithRSA, SHA384WithRSA, SHA512WithRSA, ECDSAWithSHA256, ECDSAWithSHA384, ECDSAWithSHA512 or PureEd25519.",
		},
		"encodeUsagesInRequest": {
			Type:        oam.PropertyTypeBoolean,
			Description: spec + "encodeUsagesInRequest: whether the key usage extensions are written into the signing request. Unset, true; false is for an issuer that does not accept them.",
		},
		"revisionHistoryLimit": {
			Type:        oam.PropertyTypeInteger,
			Description: spec + "revisionHistoryLimit: how many of the Certificate's CertificateRequests are kept, at least 1. Unset, 1.",
		},
		"additionalOutputFormats": {
			Type:        oam.PropertyTypeArray,
			Description: spec + "additionalOutputFormats: the extra formats of the key and the certificate chain written into the Certificate's Secret.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One format: type (required), DER or CombinedPEM." + decoded + "CertificateAdditionalOutputFormat in its API reference.",
			},
		},
		"nameConstraints": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "nameConstraints: the X.509 name constraints of a CA certificate: critical, permitted and excluded, each with dnsDomains, ipRanges, emailAddresses and uriDomains. An alpha feature, behind cert-manager's NameConstraints feature gate." + decoded + "NameConstraints in its API reference.",
		},
	}
}

// certificateKind is the certificate kind: see policyHeldKind. The API requires
// `secretName` and `issuerRef` with its `name`, and of what is authored below
// them the fields certificateRequired lists; the type would write each one
// empty. Of a renewal window it requires `cron` and `windowDuration`, which
// the type leaves out when they are empty, so that the object would show the
// omission: validate refuses a window without either. The API's value rules
// and cert-manager's webhook are left to them (certmanager_common.go). The
// policy reaches a keystore password written into the object
// (enforceCertificatePolicy).
var certificateKind = &policyHeldKind[certv1.CertificateSpec]{
	policyFreeKind: policyFreeKind[certv1.CertificateSpec]{
		upstream: "cert-manager.io/v1 CertificateSpec",
		required: certificateRequired,
		validate: func(spec *certv1.CertificateSpec) error {
			if spec.Renewal == nil {
				return nil
			}
			for i, window := range spec.Renewal.Windows {
				switch {
				case window.WindowDuration == nil:
					return errors.Errorf("renewal.windows[%d].windowDuration: required (how long the window stays open from each moment the cron expression names, such as 2h)", i)
				case window.Cron == "":
					return errors.Errorf("renewal.windows[%d].cron: required (the cron expression of the moments the window opens)", i)
				}
			}
			return nil
		},
		build: func(name, namespace string, spec *certv1.CertificateSpec) client.Object {
			cert := certmanager.CreateCertificate(name, namespace)
			spec.DeepCopyInto(&cert.Spec)
			return cert
		},
	},
	enforce: enforceCertificatePolicy,
}

// ToApplicationConfig decodes an OAM certificate component into its config.
// The object takes the namespace of the application it is generated in.
func (h *CertificateHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return certificateKind.config(component)
}
