package components

import (
	"fmt"

	cmacme "github.com/cert-manager/cert-manager/pkg/apis/acme/v1"
	certv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file holds what the kind components of cert-manager's cert-manager.io/v1
// API share (go-kure/launcher#790): issuer, clusterissuer and certificate. Each
// is a policyHeldKind: the object runs no pod and holds no image, but an
// Issuer's ACME HTTP01 solver sizes the pod cert-manager starts for a
// challenge, and a Certificate's keystore can hold its password in the clear.
//
// The API's types publish no field descriptions; the module ships the CRDs its
// chart installs. TestCertManagerKinds_RequiredMatchCRD holds each kind's
// required list to them, and TestCertManagerKinds_NoDefaultedZeros the claim
// that no authored 0 or false is lost on these types.
//
// cert-manager's validating webhook refuses more than the CRDs do: an Issuer
// that configures no issuer type or more than one, a keystore with a password
// beside a reference that names a Secret or with neither, a Certificate that
// names no subject. Those are the webhook's, and launcher repeats none of them.
//
// A host these objects name is one cert-manager reaches, not an artifact
// source: an ACME directory, a Vault or a certificate platform, a DNS server
// or a DNS provider's API, a CRL or OCSP endpoint. None is held to the
// environment policy's allowed registries.

// issuerSchema returns the properties of the issuer and clusterissuer kinds:
// the top-level fields of certv1.IssuerSpec, which the two objects share. kind
// names the object in each description ("Issuer").
func issuerSchema(kind string) map[string]oam.PropertySchema {
	spec := kind + " spec."
	const decoded = " Decoded strictly into cert-manager's API type: see "
	return map[string]oam.PropertySchema{
		"acme": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "acme: issue from an ACME server such as Let's Encrypt. server (the directory URL) and privateKeySecretRef (the Secret the account key is kept in) are required; email, preferredChain, profile, caBundle, skipTLSVerify, externalAccountBinding, disableAccountKeyGeneration, enableDurationFeature and the solvers (selector, http01, dns01) are optional. The cpu and memory of an http01 solver's podTemplate are held to the EnvironmentPolicy maxima." + decoded + "ACMEIssuer in its API reference.",
		},
		"ca": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "ca: sign with the CA certificate and private key in the Secret secretName (required), with the optional crlDistributionPoints, ocspServers and issuingCertificateURLs written into the certificates it issues." + decoded + "CAIssuer in its API reference.",
		},
		"vault": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "vault: sign through a Vault PKI backend. server, path (the mount path of the sign endpoint) and auth (one of tokenSecretRef, appRole, clientCertificate, kubernetes, aws) are required; namespace, serverName, caBundle, caBundleSecretRef, clientCertSecretRef and clientKeySecretRef are optional." + decoded + "VaultIssuer in its API reference.",
		},
		"selfSigned": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "selfSigned: sign each certificate with its own private key; {} is a complete self-signed issuer. crlDistributionPoints is optional." + decoded + "SelfSignedIssuer in its API reference.",
		},
		"venafi": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "venafi: issue from a CyberArk (Venafi) certificate platform. zone is required, and exactly one of tpp, cloud and ngts." + decoded + "VenafiIssuer in its API reference.",
		},
	}
}

// issuerRequired is the required list of the issuer and clusterissuer kinds:
// the fields the Issuer and ClusterIssuer CRDs require that the Go types write
// whether or not they were authored. None is at the top level: an issuer type
// the author left out holds nothing to refuse, and of one that is authored the
// API requires these. TestCertManagerKinds_RequiredMatchCRD holds the list to
// the CRDs.
//
// Three of them are fields of a Kubernetes or Gateway API type that an ACME
// HTTP01 solver embeds, each of which the CRDs refuse as the type writes it
// unauthored: the terms of a required node affinity of a solver pod, written
// null, which the API server drops before it validates, and the name of a
// parent reference, written empty, which is below its minimum length.
// TestKindComponents_NullRequired shows each refusal on the linked CRDs.
//
// The others of such a type are the key and the operator of a match
// expression, in the label selectors a solver's pod template holds: those of
// its pod affinity and anti-affinity (labelSelectorRequired).
// TestLabelSelectorKinds_CoverEverySelector holds them to the CRDs, path by
// path.
var issuerRequired = func() map[string]string {
	const (
		dns01  = "acme.solvers[].dns01."
		http01 = "acme.solvers[].http01."
		terms  = ".podTemplate.spec.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms"
	)
	return requiredFields(
		labelSelectorRequired(affinityLabelSelectors(http01+"ingress.podTemplate.spec.affinity")...),
		labelSelectorRequired(affinityLabelSelectors(http01+"gatewayHTTPRoute.podTemplate.spec.affinity")...),
		map[string]string{
			http01 + "gatewayHTTPRoute.parentRefs[].name": "the name of the Gateway the route attaches to",
			http01 + "gatewayHTTPRoute" + terms:           "the node selector terms, of which a node must match one",
			http01 + "ingress" + terms:                    "the node selector terms, of which a node must match one",
		},
		map[string]string{
			"acme.server":                       "the URL of the ACME server's directory endpoint",
			"acme.externalAccountBinding.keyID": "the ID of the CA key the external account is bound to",
			"ca.secretName":                     "the name of the Secret that holds the CA's certificate and private key",
			"vault.auth":                        "how cert-manager authenticates to the Vault server",
			"vault.path":                        "the mount path of the Vault PKI backend's sign endpoint",
			"vault.server":                      "the address of the Vault server",
			"vault.auth.appRole.path":           "the mount path of the App Role authentication backend",
			"vault.auth.appRole.roleId":         "the RoleID of the App Role",
			"vault.auth.aws.role":               "the Vault role to assume",
			"vault.auth.kubernetes.role":        "the Vault role to assume",
			"venafi.zone":                       "the policy zone the requests are restricted to",
			"venafi.tpp.url":                    "the base URL of the platform's vedsdk endpoint",
			"venafi.ngts.tsgID":                 "the tenant service group ID that scopes the access token",

			dns01 + "acmeDNS.host":                              "the address of the ACME-DNS server",
			dns01 + "akamai.serviceConsumerDomain":              "the host of the Akamai API client",
			dns01 + "azureDNS.resourceGroupName":                "the resource group the DNS zone is in",
			dns01 + "azureDNS.subscriptionID":                   "the ID of the subscription the DNS zone is in",
			dns01 + "cloudDNS.project":                          "the Google Cloud project the DNS zone is in",
			dns01 + "rfc2136.nameserver":                        "the authoritative DNS server, as host:port",
			dns01 + "route53.auth.kubernetes":                   "the service account token the role is assumed with",
			dns01 + "route53.auth.kubernetes.serviceAccountRef": "the ServiceAccount a token is requested for",
			dns01 + "webhook.groupName":                         "the API group of the webhook solver",
			dns01 + "webhook.solverName":                        "the name of the solver, as the webhook defines it",
		},
		secretRefRequired("acme.privateKeySecretRef", "the Secret the ACME account's private key is kept in"),
		secretRefRequired("acme.externalAccountBinding.keySecretRef", "the Secret key that holds the MAC key of the external account binding"),
		secretRefRequired(dns01+"acmeDNS.accountSecretRef", "the Secret key that holds the ACME-DNS account"),
		secretRefRequired(dns01+"akamai.accessTokenSecretRef", "the Secret key that holds the Akamai access token"),
		secretRefRequired(dns01+"akamai.clientSecretSecretRef", "the Secret key that holds the Akamai client secret"),
		secretRefRequired(dns01+"akamai.clientTokenSecretRef", "the Secret key that holds the Akamai client token"),
		secretRefRequired(dns01+"digitalocean.tokenSecretRef", "the Secret key that holds the DigitalOcean API token"),
		secretRefRequired("vault.auth.appRole.secretRef", "the Secret key that holds the App Role's SecretID"),
		secretRefRequired("venafi.cloud.apiTokenSecretRef", "the Secret key that holds the platform's API token"),
		secretRefRequired("venafi.ngts.credentialsRef", "the Secret that holds the OAuth 2.0 client ID and client secret"),
		secretRefRequired("venafi.tpp.credentialsRef", "the Secret that holds the platform's API credentials"),
		refNamesRequired("Secret",
			dns01+"azureDNS.clientSecretSecretRef",
			dns01+"cloudDNS.serviceAccountSecretRef",
			dns01+"cloudflare.apiKeySecretRef",
			dns01+"cloudflare.apiTokenSecretRef",
			dns01+"rfc2136.tsigSecretSecretRef",
			dns01+"route53.accessKeyIDSecretRef",
			dns01+"route53.secretAccessKeySecretRef",
			"vault.auth.kubernetes.secretRef",
			"vault.auth.tokenSecretRef",
			"vault.caBundleSecretRef",
			"vault.clientCertSecretRef",
			"vault.clientKeySecretRef",
			"venafi.tpp.caBundleSecretRef",
		),
		refNamesRequired("ServiceAccount",
			dns01+"route53.auth.kubernetes.serviceAccountRef",
			"vault.auth.aws.serviceAccountRef",
			"vault.auth.kubernetes.serviceAccountRef",
		),
	)
}()

// certificateRequired is the required list of the certificate kind: the fields
// the Certificate CRD requires that the Go types write whether or not they
// were authored. TestCertManagerKinds_RequiredMatchCRD holds the list to the
// CRD.
var certificateRequired = requiredFields(
	map[string]string{
		"secretName":                     "the name of the Secret cert-manager keeps the private key and the certificate in",
		"issuerRef":                      "the issuer that signs the certificate; no default issuer is filled",
		"issuerRef.name":                 "the name of the issuer",
		"additionalOutputFormats[].type": "the format: DER or CombinedPEM",
		"keystores.jks.create":           "whether the JKS keystore is created; no default is filled",
		"keystores.pkcs12.create":        "whether the PKCS12 keystore is created; no default is filled",
	},
	refNamesRequired("Secret", "keystores.jks.passwordSecretRef", "keystores.pkcs12.passwordSecretRef"),
)

// secretRefRequired is the required list of one Secret key selector the API
// requires under the path at: the selector, which the Go type writes whether
// or not it was authored, and its name. what says what the Secret holds.
func secretRefRequired(at, what string) map[string]string {
	return map[string]string{
		at:           what,
		at + ".name": "the name of the Secret",
	}
}

// refNamesRequired is the required list of the references at the paths at
// that are optional themselves: of each one that is authored, the API requires
// the name, which the Go type would write empty. kind is the kind of the
// object a reference names ("Secret").
func refNamesRequired(kind string, at ...string) map[string]string {
	out := make(map[string]string, len(at))
	for _, path := range at {
		out[path+".name"] = "the name of the " + kind
	}
	return out
}

// enforceIssuerPolicy holds an Issuer's or a ClusterIssuer's spec to the
// environment policy. One part of it sizes a pod: the pod template of an ACME
// HTTP01 solver, from which cert-manager builds the pod that answers a
// challenge. Its cpu and memory limits and requests are held to the policy's
// maxima, as a container's are.
//
// Nothing else of the pod kind's policy checks has a field here: the template
// names no image (the solver's is a flag of the cert-manager controller), no
// container security context, no volume and no host namespace. The request is
// not held to the limit either: cert-manager lays the authored block over the
// controller's own defaults key by key, so the pair that reaches the pod is
// not the authored one.
func enforceIssuerPolicy(spec *certv1.IssuerSpec, p oam.Policy) error {
	if spec.ACME == nil {
		return nil
	}
	for i, solver := range spec.ACME.Solvers {
		if solver.HTTP01 == nil {
			continue
		}
		if ingress := solver.HTTP01.Ingress; ingress != nil {
			if err := enforceSolverPodResources(fmt.Sprintf("acme.solvers[%d].http01.ingress", i), ingress.PodTemplate, p); err != nil {
				return err
			}
		}
		if route := solver.HTTP01.GatewayHTTPRoute; route != nil {
			if err := enforceSolverPodResources(fmt.Sprintf("acme.solvers[%d].http01.gatewayHTTPRoute", i), route.PodTemplate, p); err != nil {
				return err
			}
		}
	}
	return nil
}

// enforceSolverPodResources holds the resources of one solver pod template,
// under the path at, to the policy's cpu and memory maxima.
func enforceSolverPodResources(at string, tmpl *cmacme.ACMEChallengeSolverHTTP01IngressPodTemplate, p oam.Policy) error {
	if tmpl == nil || tmpl.Spec.Resources == nil {
		return nil
	}
	res := corev1.ResourceRequirements{Limits: tmpl.Spec.Resources.Limits, Requests: tmpl.Spec.Resources.Requests}
	if err := enforceMaxContainerResources(res, p); err != nil {
		return errors.Wrap(err, at+".podTemplate.spec.resources")
	}
	return nil
}

// enforceCertificatePolicy holds a Certificate's spec to the environment
// policy: under one that forbids explicit secrets (oam.ExplicitSecretPolicy) a
// keystore password written into the object is refused, since the object, and
// with it the password, is in the build's output. The message quotes nothing
// of the value. A policy that does not implement that interface allows it.
func enforceCertificatePolicy(spec *certv1.CertificateSpec, p oam.Policy) error {
	if oam.ExplicitSecretsAllowed(p) || spec.Keystores == nil {
		return nil
	}
	var at string
	switch ks := spec.Keystores; {
	case ks.JKS != nil && ks.JKS.Password != nil:
		at = "keystores.jks"
	case ks.PKCS12 != nil && ks.PKCS12.Password != nil:
		at = "keystores.pkcs12"
	default:
		return nil
	}
	return oam.NewPolicyRefusal(oam.RefusalExplicitSecret, fmt.Sprintf("%s.password: holds the keystore password in the object, and the environment policy forbids explicit secrets; name the key of a Secret created out of band in %s.passwordSecretRef instead", at, at))
}
