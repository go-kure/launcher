package kurel

import (
	"slices"
	"strings"
	"testing"

	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// The Secrets the trait objects make another controller write
// (go-kure/launcher#787): the managed TLS Secret of an expose trait's Ingress
// (role tls-secret) and the Secret an external-secret trait's ExternalSecret
// produces (role external-secret). Each is resolved before anything launcher
// writes refers to it, so the hook's answer reaches every such reference. The
// certificate trait's Secret and the ExternalSecret are named by a required
// property: no hook is asked for them, and their names are claimed.

// namingSecretTraits are web's expose trait, whose ingress rendering manages
// TLS under withManagedTLS, and an external-secret trait whose produced Secret
// web's Deployment reads through envFrom and a secret volume.
const namingSecretTraits = `        - type: expose
          properties:
            hostnames: [shop.example.com]
        - type: external-secret
          properties:
            secretName: web-creds
            secretStoreRef:
              name: vault
            envFrom: true
            mountPath: /secrets
            data:
              - secretKey: DB_PASSWORD
                remoteRef:
                  key: prod/web/db
`

// withManagedTLS gives ctx the expose capability of an ingress controller with
// a cert-manager cluster issuer, so the expose trait manages its Ingress's TLS,
// and the certificate capability's issuer.
func withManagedTLS(ctx oam.TransformContext) oam.TransformContext {
	ctx.Capabilities = map[string]oam.CapabilityBinding{
		"expose": {Rendering: map[string]any{
			"controllerType":           "ingress",
			"ingressClassName":         "nginx",
			"certManagerClusterIssuer": "letsencrypt",
		}},
		"certificate": {Rendering: map[string]any{
			"issuerRef": map[string]any{"name": "letsencrypt"},
		}},
	}
	return ctx
}

// secretRolesErr transforms and generates appYAML under ctx, as namingTransform
// does, and returns the first error.
func secretRolesErr(t *testing.T, appYAML string, ctx oam.TransformContext) error {
	t.Helper()
	transformer := newBuiltinTransformer()
	app, err := oam.ParseWithExtraTypes([]byte(appYAML), nil, transformer.LowerableTypes())
	if err != nil {
		return err
	}
	if err := transformer.ValidateAuthoredProperties(app); err != nil {
		return err
	}
	ctx.Domain = kurelDomain
	cluster, err := transformer.Transform(app, ctx)
	if err != nil {
		return err
	}
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		return err
	}
	return oam.CheckInDocumentCollisions(apps)
}

// generatedOf returns the generated objects of type T.
func generatedOf[T any](apps []oam.GeneratedApplication) []T {
	var out []T
	for _, a := range apps {
		for _, p := range a.Objects {
			if p == nil {
				continue
			}
			if obj, ok := (*p).(T); ok {
				out = append(out, obj)
			}
		}
	}
	return out
}

// onlyOne returns the one element of objs, failing the test otherwise.
func onlyOne[T any](t *testing.T, what string, objs []T) T {
	t.Helper()
	if len(objs) != 1 {
		t.Fatalf("%d %s generated, want 1", len(objs), what)
	}
	return objs[0]
}

// The hook's answers name the Secrets, and every reference launcher writes to
// them carries the answer: the Ingress's TLS entry, the ExternalSecret's
// target, and the envFrom and secret volume of web's Deployment. The
// ExternalSecret keeps its own name.
func TestSecretRoles_HookAnswerReachesEveryReference(t *testing.T) {
	_, apps := namingTransform(t, namingApp(namingSecretTraits, ""), withManagedTLS(namingContext(renameBy(map[string]string{
		"tls-secret web-tls":        "web-cert",
		"external-secret web-creds": "web-db",
	}))))

	ingress := onlyOne(t, "Ingresses", generatedOf[*networkingv1.Ingress](apps))
	if len(ingress.Spec.TLS) != 1 || ingress.Spec.TLS[0].SecretName != "web-cert" {
		t.Errorf("Ingress TLS = %+v, want one entry with secretName web-cert", ingress.Spec.TLS)
	}

	es := onlyOne(t, "ExternalSecrets", generatedOf[*esv1.ExternalSecret](apps))
	if es.Name != "web-creds" || es.Spec.Target.Name != "web-db" {
		t.Errorf("ExternalSecret %q with target %q, want web-creds with target web-db", es.Name, es.Spec.Target.Name)
	}

	deployment := onlyOne(t, "Deployments", generatedOf[*appsv1.Deployment](apps))
	pod := deployment.Spec.Template.Spec
	if refs := pod.Containers[0].EnvFrom; len(refs) != 1 || refs[0].SecretRef == nil || refs[0].SecretRef.Name != "web-db" {
		t.Errorf("envFrom = %+v, want one secretRef to web-db", refs)
	}
	if !slices.ContainsFunc(pod.Volumes, func(v corev1.Volume) bool { return v.Secret != nil && v.Secret.SecretName == "web-db" }) {
		t.Errorf("no secret volume of web-db in %+v", pod.Volumes)
	}
}

// An authored name is used as written, and the hook is not asked for it: the
// expose trait's secretName, the external-secret trait's targetSecretName.
func TestSecretRoles_AuthoredNamesNotAsked(t *testing.T) {
	traits := strings.Replace(namingSecretTraits, "            hostnames: [shop.example.com]\n",
		"            hostnames: [shop.example.com]\n            secretName: shop-cert\n", 1)
	traits = strings.Replace(traits, "            secretName: web-creds\n",
		"            secretName: web-creds\n            targetSecretName: shop-db\n", 1)
	var requests []oam.NameRequest
	_, apps := namingTransform(t, namingApp(traits, ""), withManagedTLS(namingContext(declineEveryName(&requests))))
	for _, req := range requests {
		if req.Role == oam.NameRoleTLSSecret || req.Role == oam.NameRoleExternalSecret {
			t.Errorf("the hook was asked for an authored name: %+v", req)
		}
	}
	if tls := onlyOne(t, "Ingresses", generatedOf[*networkingv1.Ingress](apps)).Spec.TLS; len(tls) != 1 || tls[0].SecretName != "shop-cert" {
		t.Errorf("Ingress TLS = %+v, want secretName shop-cert", tls)
	}
	if target := onlyOne(t, "ExternalSecrets", generatedOf[*esv1.ExternalSecret](apps)).Spec.Target.Name; target != "shop-db" {
		t.Errorf("ExternalSecret target = %q, want shop-db", target)
	}
}

// An ingress trait's own tls entries are used as written: the hook is not
// asked for their Secrets, and nothing is added to them.
func TestSecretRoles_AuthoredIngressTLSUntouched(t *testing.T) {
	trait := claimIngressTrait + `            tls:
              - hosts: [shop.example.com]
                secretName: own-tls
`
	var requests []oam.NameRequest
	_, apps := namingTransform(t, namingApp(trait, ""), namingContext(declineEveryName(&requests)))
	for _, req := range requests {
		if req.Role == oam.NameRoleTLSSecret {
			t.Errorf("the hook was asked for an authored tls entry's Secret: %+v", req)
		}
	}
	if tls := onlyOne(t, "Ingresses", generatedOf[*networkingv1.Ingress](apps)).Spec.TLS; len(tls) != 1 || tls[0].SecretName != "own-tls" {
		t.Errorf("Ingress TLS = %+v, want the one authored entry, own-tls", tls)
	}
}

// A rendering of the ingress capability may supply managedTLS, the consumer's
// path to it: its Secret is named under tls-secret like the expose trait's,
// after the authored entries.
func TestSecretRoles_ManagedTLSFromTheCapability(t *testing.T) {
	trait := claimIngressTrait + `            tls:
              - hosts: [own.example.com]
                secretName: own-tls
`
	ctx := namingContext(renameBy(map[string]string{"tls-secret web-tls": "web-cert"}))
	ctx.Capabilities = map[string]oam.CapabilityBinding{"ingress": {Rendering: map[string]any{
		"managedTLS": map[string]any{"hosts": []any{"shop.example.com"}},
	}}}
	_, apps := namingTransform(t, namingApp(trait, ""), ctx)
	tls := onlyOne(t, "Ingresses", generatedOf[*networkingv1.Ingress](apps)).Spec.TLS
	if len(tls) != 2 || tls[0].SecretName != "own-tls" || tls[1].SecretName != "web-cert" ||
		!slices.Equal(tls[1].Hosts, []string{"shop.example.com"}) {
		t.Errorf("Ingress TLS = %+v, want own-tls, then web-cert for shop.example.com", tls)
	}
}

// managedTLS is platform-reserved: an author who writes it on an ingress trait
// is refused.
func TestSecretRoles_ManagedTLSNotAuthorable(t *testing.T) {
	trait := claimIngressTrait + `            managedTLS:
              hosts: [shop.example.com]
`
	err := secretRolesErr(t, namingApp(trait, ""), namingContext(nil))
	if err == nil || !strings.Contains(err.Error(), "managedTLS") {
		t.Fatalf("err = %v, want a refusal naming managedTLS", err)
	}
}

// The hook's answer is held to what the trait does with the name: a produced
// Secret that is mounted must be named by a DNS-1123 label, the volume's name.
func TestSecretRoles_HookAnswerCheckedForTheMount(t *testing.T) {
	err := secretRolesErr(t, namingApp(namingSecretTraits, ""), withManagedTLS(namingContext(renameBy(map[string]string{
		"external-secret web-creds": "web.db",
	}))))
	if err == nil || !strings.Contains(err.Error(), `the produced Secret name "web.db" cannot be used as a volume name`) {
		t.Fatalf("err = %v, want the volume-name refusal of the hook's answer", err)
	}
}

// Under creationPolicy Merge the ExternalSecret writes into a Secret that
// exists apart from it: the name refers to that Secret and is neither put to
// the hook nor changed.
func TestSecretRoles_MergeTargetNotAsked(t *testing.T) {
	traits := strings.Replace(namingSecretTraits, "            secretStoreRef:\n",
		"            target:\n              creationPolicy: Merge\n            secretStoreRef:\n", 1)
	var requests []oam.NameRequest
	_, apps := namingTransform(t, namingApp(traits, ""), withManagedTLS(namingContext(declineEveryName(&requests))))
	for _, req := range requests {
		if req.Role == oam.NameRoleExternalSecret {
			t.Errorf("the hook was asked for the Secret a Merge writes into: %+v", req)
		}
	}
	if target := onlyOne(t, "ExternalSecrets", generatedOf[*esv1.ExternalSecret](apps)).Spec.Target.Name; target != "web-creds" {
		t.Errorf("ExternalSecret target = %q, want web-creds", target)
	}
}

// The certificate trait's Secret and the ExternalSecret are claimed: a second
// owner of either is refused with both named.
func TestSecretRoles_RequiredNamesClaimed(t *testing.T) {
	certificate := `        - type: certificate
          properties:
            secretName: web-creds
            dnsNames: [shop.example.com]
`
	for _, tt := range []struct {
		name, traits, want string
	}{
		{"the certificate's Secret is the produced Secret", certificate + namingSecretTraits,
			`name collision: Secret "default/web-creds" is named by component "web" traits[4] "certificate" (its own object, set by secretName)`},
		{"two ExternalSecrets of one name", namingSecretTraits + `        - type: external-secret
          properties:
            secretName: web-creds
            targetSecretName: other
            secretStoreRef:
              name: vault
            data:
              - secretKey: API_KEY
                remoteRef:
                  key: prod/web/api
`, `name collision: ExternalSecret.external-secrets.io "default/web-creds" is named by component "web" traits[5] "external-secret" (its own object, set by secretName)`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := secretRolesErr(t, namingApp(tt.traits, ""), withManagedTLS(namingContext(nil)))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want a refusal of the second owner naming %q", err, tt.want)
			}
		})
	}
}
