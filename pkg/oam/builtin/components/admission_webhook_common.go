package components

import (
	"fmt"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file holds what the two webhook configuration kinds share
// (go-kure/launcher#943): validatingwebhookconfiguration and
// mutatingwebhookconfiguration. Neither has a spec type, so each is a
// policyFreeKind over the whole object, whose one authorable field is
// `webhooks`.
//
// No dimension of the environment policy reads a webhook. The kinds are gated
// as every emitted object is, by the policy's object kind rules
// (oam.ObjectKindPolicy): a policy that forbids the group or the kind, or
// cluster-scoped objects, refuses them. What a webhook does once applied is
// out of the build's reach, and the kinds' README entries say so: the API
// server calls it on the requests its rules match, in any namespace; a
// mutating one changes objects after the build, so what the build checked
// need not hold of what is stored; its clientConfig is not resolved at build;
// and a match condition's CEL expression is compiled by the API server, not
// here.
//
// Each kind's required list is derived from the markers of the linked
// k8s.io/api source (TestBuiltinMarkerKinds_RequiredMatchMarkers). The API
// server requires more than the markers say. The presence rules of that
// validation are checked here by hand (validateAdmissionWebhook), read from
// pkg/apis/admissionregistration/validation/validation.go of
// k8s.io/kubernetes at Kubernetes v1.37.1. The form of every other value it
// checks is left to the API server: that a webhook's name is fully qualified
// and unique, the values of failurePolicy, matchPolicy, sideEffects,
// reinvocationPolicy and a rule's operations and scope, the timeout's range,
// a URL's form, the admission review versions it recognises, and how
// wildcards combine in a rule.

// admissionWebhookRequired is the required list of a webhook configuration's
// webhooks: the fields of a webhook the API marks required and the Go type
// writes whether or not they were authored, and the key and operator of a
// match expression of its two selectors.
var admissionWebhookRequired = requiredFields(map[string]string{
	"webhooks[].name":                           "the webhook's name, fully qualified (imagepolicy.example.com)",
	"webhooks[].clientConfig":                   "how the API server reaches the webhook: a url or a service",
	"webhooks[].clientConfig.service.name":      "the name of the Service that serves the webhook",
	"webhooks[].clientConfig.service.namespace": "the namespace of the Service that serves the webhook",
	"webhooks[].sideEffects":                    "whether calling the webhook has side effects: None or NoneOnDryRun",
	"webhooks[].admissionReviewVersions":        "the AdmissionReview versions the webhook accepts, in order of preference, at least one (v1)",
	"webhooks[].matchConditions[].name":         "the match condition's name",
	"webhooks[].matchConditions[].expression":   "the match condition's CEL expression",
}, labelSelectorRequired("webhooks[].namespaceSelector", "webhooks[].objectSelector"))

// admissionWebhook is what validateAdmissionWebhook reads of one webhook, of
// either kind.
type admissionWebhook struct {
	clientConfig            admissionregistrationv1.WebhookClientConfig
	rules                   []admissionregistrationv1.RuleWithOperations
	admissionReviewVersions []string
	namespaceSelector       *metav1.LabelSelector
	objectSelector          *metav1.LabelSelector
}

// validateAdmissionWebhook refuses what the API server requires of the webhook
// at the path at beyond the type's markers, and a defective match expression
// of its two selectors (validateLabelSelector).
//
// Read from validateValidatingWebhook and validateMutatingWebhook,
// validateRuleWithOperations, validateRule, validateResources and
// validateAdmissionReviewVersions, Kubernetes v1.37.1: a webhook's
// clientConfig holds exactly one of url and service; it names at least one
// admission review version; and each rule names at least one operation, API
// group, API version and resource. A selector is checked only where it is
// authored, as there.
func validateAdmissionWebhook(at string, hook admissionWebhook) error {
	if (hook.clientConfig.URL == nil) == (hook.clientConfig.Service == nil) {
		return errors.Errorf("%s.clientConfig: exactly one of url and service is required (how the API server reaches the webhook)", at)
	}
	if len(hook.admissionReviewVersions) == 0 {
		return errors.Errorf("%s.admissionReviewVersions: required (the AdmissionReview versions the webhook accepts, at least one: v1)", at)
	}
	for i, rule := range hook.rules {
		ruleAt := fmt.Sprintf("%s.rules[%d]", at, i)
		switch {
		case len(rule.Operations) == 0:
			return errors.Errorf("%s.operations: required (the operations the rule matches, at least one: CREATE, UPDATE, DELETE, CONNECT or *)", ruleAt)
		case len(rule.APIGroups) == 0:
			return errors.Errorf(`%s.apiGroups: required (the API groups the rule matches, at least one; "" is the core group)`, ruleAt)
		case len(rule.APIVersions) == 0:
			return errors.Errorf("%s.apiVersions: required (the API versions the rule matches, at least one)", ruleAt)
		case len(rule.Resources) == 0:
			return errors.Errorf("%s.resources: required (the resources the rule matches, at least one)", ruleAt)
		}
	}
	if err := validateLabelSelector(at+".namespaceSelector", hook.namespaceSelector); err != nil {
		return err
	}
	return validateLabelSelector(at+".objectSelector", hook.objectSelector)
}

// admissionWebhooksSchema is the `webhooks` property of a webhook
// configuration of the given kind. calls says what the API server does with a
// matched request.
func admissionWebhooksSchema(kind, calls, fields string) map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"webhooks": {
			Type:        oam.PropertyTypeArray,
			Description: kind + " webhooks: the webhooks the API server " + calls + " on the requests their rules match, in any namespace. Nothing restricts what a webhook matches. Unauthored, the configuration holds no webhook.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One webhook: " + fields + ". clientConfig holds exactly one of url and service, and a service its namespace and name; each rule names at least one of operations, apiGroups, apiVersions and resources. A selector's match expression takes a key and an operator. Neither the url nor the service is resolved at build, and a match condition's CEL expression is compiled by the API server. Decoded strictly into the Kubernetes API type.",
			},
		},
	}
}
