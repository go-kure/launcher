package components_test

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// A policy that forbids explicit secrets (oam.ExplicitSecretPolicy,
// go-kure/launcher#786) refuses a Secret a document carries through the
// passthrough and manifests components, as it refuses the secret trait
// (go-kure/launcher#794, items 2 and 10).

// esSentinel is the value no refusal may carry.
const esSentinel = "s3cr3t-sentinel-value"

// esPolicy is stubPolicy with the optional explicit-secret answer.
type esPolicy struct {
	*stubPolicy
	allow bool
}

func (p esPolicy) AllowExplicitSecrets() bool { return p.allow }

// esForbidding forbids explicit secrets and restricts nothing else.
func esForbidding() esPolicy { return esPolicy{stubPolicy: &stubPolicy{}} }

// esSecret is the YAML of a v1 Secret named creds, followed by body.
func esSecret(body string) string {
	return "apiVersion: v1\nkind: Secret\nmetadata:\n  name: creds\n" + body
}

// esWantSecret fails unless objs holds exactly one object, the Secret creds.
func esWantSecret(t *testing.T, objs []client.Object, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("the build failed: %v", err)
	}
	if len(objs) != 1 || objs[0].GetObjectKind().GroupVersionKind().Kind != "Secret" || objs[0].GetName() != "creds" {
		t.Fatalf("built %v, want the Secret creds alone", objs)
	}
}

// esWantRefused fails unless err is the component's violation naming the
// Secret, without the value it carries.
func esWantRefused(t *testing.T, err error, prefix string) {
	t.Helper()
	htWantViolation(t, err, prefix+`: object Secret "`, `creds"`, "is a Secret, and the environment policy forbids explicit secrets")
	if strings.Contains(err.Error(), esSentinel) {
		t.Errorf("the refusal carries the value: %v", err)
	}
}

// TestPassthrough_ExplicitSecret: a Secret a passthrough component emits is
// refused under a policy that forbids explicit secrets, whatever it holds, and
// built under one that allows them or does not answer the question.
func TestPassthrough_ExplicitSecret(t *testing.T) {
	for name, body := range map[string]string{
		"stringData":          "stringData:\n  password: " + esSentinel + "\n",
		"data":                "data:\n  password: czNjcjN0\n",
		"no entry":            "type: kubernetes.io/service-account-token\n",
		"an undeclared field": "futureField: true\nstringData:\n  password: " + esSentinel + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ptTransform(ptObject(t, esSecret(body)), esForbidding())
			esWantRefused(t, err, "passthrough")
		})
	}

	doc := esSecret("stringData:\n  password: " + esSentinel + "\n")
	t.Run("allowed by the policy", func(t *testing.T) {
		objs, err := ptTransform(ptObject(t, doc), esPolicy{stubPolicy: &stubPolicy{}, allow: true})
		esWantSecret(t, objs, err)
	})
	t.Run("a policy without the optional interface", func(t *testing.T) {
		objs, err := ptTransform(ptObject(t, doc), &stubPolicy{})
		esWantSecret(t, objs, err)
	})
	t.Run("no policy", func(t *testing.T) {
		objs, err := ptTransform(ptObject(t, doc), nil)
		esWantSecret(t, objs, err)
	})
	t.Run("another kind under the forbidding policy", func(t *testing.T) {
		cm := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\ndata:\n  key: value\n"
		if _, err := ptTransform(ptObject(t, cm), esForbidding()); err != nil {
			t.Errorf("a ConfigMap was refused: %v", err)
		}
	})
	t.Run("a Secret of another group", func(t *testing.T) {
		other := "apiVersion: example.com/v1\nkind: Secret\nmetadata:\n  name: creds\n  namespace: demo\nspec: {}\n"
		if _, err := ptTransform(ptObject(t, other), esForbidding()); err != nil {
			t.Errorf("a custom resource of kind Secret was refused: %v", err)
		}
	})
}

// TestManifests_ExplicitSecret: a Secret a manifests source yields, inline or
// fetched, is refused under a policy that forbids explicit secrets; the fetched
// one at generation, where its objects are first known.
func TestManifests_ExplicitSecret(t *testing.T) {
	doc := esSecret("stringData:\n  password: " + esSentinel + "\n")
	cm := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\ndata:\n  key: value\n"

	t.Run("inline", func(t *testing.T) {
		_, err := mfTransform("manifests", mfInline(doc), esForbidding())
		esWantRefused(t, err, "manifest source")
	})
	t.Run("inline, after another document", func(t *testing.T) {
		_, err := mfTransform("manifests", mfInline(cm+"---\n"+doc), esForbidding())
		esWantRefused(t, err, "manifest source")
	})
	t.Run("inline, with an undeclared field", func(t *testing.T) {
		_, err := mfTransform("manifests", mfInline(esSecret("futureField: true\n")), esForbidding())
		esWantRefused(t, err, "manifest source")
	})
	t.Run("fetched", func(t *testing.T) {
		srv, _ := mfServe(t, doc)
		policy := esPolicy{stubPolicy: &stubPolicy{allowedRegistries: []string{htServerHost(t, srv.URL)}}}
		_, err := mfTransform("manifests", map[string]any{"url": srv.URL + "/manifests.yaml"}, policy)
		esWantRefused(t, err, "manifest source")
	})
	t.Run("allowed by the policy", func(t *testing.T) {
		objs, err := mfTransform("manifests", mfInline(doc), esPolicy{stubPolicy: &stubPolicy{}, allow: true})
		esWantSecret(t, objs, err)
		if s, ok := objs[0].(*corev1.Secret); !ok || s.StringData["password"] != esSentinel {
			t.Errorf("built %T %v, want the Secret as written", objs[0], objs[0])
		}
	})
	t.Run("a policy without the optional interface", func(t *testing.T) {
		objs, err := mfTransform("manifests", mfInline(doc), &stubPolicy{})
		esWantSecret(t, objs, err)
	})
	t.Run("another kind under the forbidding policy", func(t *testing.T) {
		if _, err := mfTransform("manifests", mfInline(cm), esForbidding()); err != nil {
			t.Errorf("a ConfigMap was refused: %v", err)
		}
	})
}
