package components_test

import (
	"maps"
	"testing"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
)

// The validatingwebhookconfiguration and mutatingwebhookconfiguration kinds
// (go-kure/launcher#943) are rows of policyFreeKinds, which holds what the
// shared helper promises of each. This file holds their fixtures and what is
// theirs alone.

// admissionWebhook is a whole webhook reached through a Service: the least
// the API takes, with the given fields set over it.
func admissionWebhook(fields map[string]any) map[string]any {
	hook := map[string]any{
		"name":                    "policy.example.com",
		"clientConfig":            map[string]any{"service": map[string]any{"namespace": "policy", "name": "webhook"}},
		"sideEffects":             "None",
		"admissionReviewVersions": []any{"v1"},
	}
	maps.Copy(hook, fields)
	return hook
}

// admissionWebhooks is the properties of a webhook configuration of the given
// webhooks.
func admissionWebhooks(hooks ...any) map[string]any {
	return map[string]any{"webhooks": hooks}
}

// admissionWebhookFull is a webhook configuration that sets every field of a
// webhook, the mutating one's reinvocationPolicy when mutating says so: the
// first webhook is reached through a Service, the second through a URL.
func admissionWebhookFull(mutating bool) map[string]any {
	first := admissionWebhook(map[string]any{
		"clientConfig": map[string]any{
			"service":  map[string]any{"namespace": "policy", "name": "webhook", "path": "/validate", "port": 8443},
			"caBundle": "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0t",
		},
		"rules": []any{map[string]any{
			"operations": []any{"CREATE", "UPDATE"}, "apiGroups": []any{"apps"}, "apiVersions": []any{"v1"},
			"resources": []any{"deployments"}, "scope": "Namespaced",
		}},
		"failurePolicy":     "Fail",
		"matchPolicy":       "Equivalent",
		"namespaceSelector": map[string]any{"matchLabels": map[string]any{"policy.example.com/enforce": "true"}},
		"objectSelector": map[string]any{"matchExpressions": []any{
			map[string]any{"key": "tier", "operator": "In", "values": []any{"web", "api"}},
		}},
		"timeoutSeconds":          5,
		"admissionReviewVersions": []any{"v1", "v1beta1"},
		"matchConditions":         []any{map[string]any{"name": "not-system", "expression": "!request.userInfo.username.startsWith('system:')"}},
	})
	if mutating {
		first["reinvocationPolicy"] = "IfNeeded"
	}
	second := admissionWebhook(map[string]any{
		"name":         "audit.example.com",
		"clientConfig": map[string]any{"url": "https://audit.example.com/review"},
	})
	return admissionWebhooks(first, second)
}

// TestAdmissionWebhookKinds_AuthoredValuesArriveTyped: each webhook arrives in
// the typed object as authored, the mutating one's reinvocation policy
// included, and what the API server defaults stays unset.
func TestAdmissionWebhookKinds_AuthoredValuesArriveTyped(t *testing.T) {
	kinds := map[string]policyFreeKind{}
	for _, kind := range policyFreeKinds {
		kinds[kind.component] = kind
	}
	validating := kinds["validatingwebhookconfiguration"].generate(t, "fast", admissionWebhookFull(false)).(*admissionregistrationv1.ValidatingWebhookConfiguration)
	if len(validating.Webhooks) != 2 {
		t.Fatalf("webhooks = %d, want 2", len(validating.Webhooks))
	}
	first := validating.Webhooks[0]
	if svc := first.ClientConfig.Service; svc == nil || svc.Namespace != "policy" || svc.Name != "webhook" || svc.Path == nil || *svc.Path != "/validate" || svc.Port == nil || *svc.Port != 8443 {
		t.Errorf("first webhook's service = %+v, want policy/webhook at /validate:8443", svc)
	}
	if first.FailurePolicy == nil || *first.FailurePolicy != admissionregistrationv1.Fail ||
		first.SideEffects == nil || *first.SideEffects != admissionregistrationv1.SideEffectClassNone ||
		first.TimeoutSeconds == nil || *first.TimeoutSeconds != 5 {
		t.Errorf("first webhook = %+v, want failurePolicy Fail, sideEffects None, timeoutSeconds 5", first)
	}
	if second := validating.Webhooks[1]; second.ClientConfig.URL == nil || *second.ClientConfig.URL != "https://audit.example.com/review" || second.ClientConfig.Service != nil {
		t.Errorf("second webhook's clientConfig = %+v, want the URL alone", second.ClientConfig)
	}
	// What the API server defaults is left to it.
	plain := kinds["validatingwebhookconfiguration"].generate(t, "fast", admissionWebhooks(admissionWebhook(nil))).(*admissionregistrationv1.ValidatingWebhookConfiguration)
	if hook := plain.Webhooks[0]; hook.FailurePolicy != nil || hook.MatchPolicy != nil || hook.TimeoutSeconds != nil || hook.NamespaceSelector != nil || hook.ObjectSelector != nil {
		t.Errorf("unauthored defaults = %+v, want them unset for the API server", hook)
	}

	mutating := kinds["mutatingwebhookconfiguration"].generate(t, "fast", admissionWebhookFull(true)).(*admissionregistrationv1.MutatingWebhookConfiguration)
	if policy := mutating.Webhooks[0].ReinvocationPolicy; policy == nil || *policy != admissionregistrationv1.IfNeededReinvocationPolicy {
		t.Errorf("reinvocationPolicy = %v, want IfNeeded", policy)
	}
	// The validating kind's type has no reinvocation policy.
	err := coreKindErr(kinds["validatingwebhookconfiguration"].handler, "validatingwebhookconfiguration", "fast",
		admissionWebhooks(admissionWebhook(map[string]any{"reinvocationPolicy": "IfNeeded"})))
	if err == nil {
		t.Error("a validating webhook with a reinvocationPolicy built, want it refused")
	}
}

// TestAdmissionWebhookKinds_ValuesLeftToTheAPIServer: the form of a value the
// kinds do not read builds, as their README entries say; the API server
// judges it.
func TestAdmissionWebhookKinds_ValuesLeftToTheAPIServer(t *testing.T) {
	for _, kind := range policyFreeKinds {
		if kind.component != "validatingwebhookconfiguration" && kind.component != "mutatingwebhookconfiguration" {
			continue
		}
		for what, props := range map[string]map[string]any{
			"a name not fully qualified": admissionWebhooks(admissionWebhook(map[string]any{"name": "policy"})),
			"two webhooks of one name":   admissionWebhooks(admissionWebhook(nil), admissionWebhook(nil)),
			"an unknown side effect":     admissionWebhooks(admissionWebhook(map[string]any{"sideEffects": "Some"})),
			"an unknown failure policy":  admissionWebhooks(admissionWebhook(map[string]any{"failurePolicy": "Retry"})),
			"a timeout out of range":     admissionWebhooks(admissionWebhook(map[string]any{"timeoutSeconds": 90})),
			"a URL without TLS":          admissionWebhooks(admissionWebhook(map[string]any{"clientConfig": map[string]any{"url": "http://policy"}})),
			"an unknown review version":  admissionWebhooks(admissionWebhook(map[string]any{"admissionReviewVersions": []any{"v9"}})),
			"an unknown operation": admissionWebhooks(admissionWebhook(map[string]any{"rules": []any{map[string]any{
				"operations": []any{"PATCH"}, "apiGroups": []any{""}, "apiVersions": []any{"v1"}, "resources": []any{"pods"},
			}}})),
			"a CEL expression that does not compile": admissionWebhooks(admissionWebhook(map[string]any{
				"matchConditions": []any{map[string]any{"name": "broken", "expression": "request.("}},
			})),
			"no webhook": {},
		} {
			if err := coreKindErr(kind.handler, kind.component, "fast", props); err != nil {
				t.Errorf("%s: %s: %v, want it built", kind.component, what, err)
			}
		}
	}
}

// TestAdmissionWebhookKinds_ObjectKindPolicy: the object kind rules gate the
// two kinds as every emitted object; nothing else of the environment policy
// does.
func TestAdmissionWebhookKinds_ObjectKindPolicy(t *testing.T) {
	for _, kind := range policyFreeKinds {
		if kind.component != "validatingwebhookconfiguration" && kind.component != "mutatingwebhookconfiguration" {
			continue
		}
		t.Run(kind.component, func(t *testing.T) {
			clusterWideObjectKindPolicy(t, kind.component, kind.handler, admissionWebhooks(admissionWebhook(nil)),
				kind.gvk.GroupKind(), kind.gvk.Kind+` "web"`)
		})
	}
}
