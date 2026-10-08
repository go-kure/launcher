package components

import (
	"fmt"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// MutatingWebhookConfigurationHandler handles OAM mutatingwebhookconfiguration
// components: the kind-named projection of a cluster-scoped
// admissionregistration.k8s.io/v1 MutatingWebhookConfiguration
// (go-kure/launcher#943).
//
// A MutatingWebhookConfiguration has no spec: its one property, `webhooks`, is
// the object's own field, decoded strictly; its kind, apiVersion and metadata
// are launcher's to set and are refused. It emits the configuration, named
// after the component unless `objectName` names it, with no namespace, and
// nothing else. See admission_webhook_common.go for what gates it and what the
// build does not reach: a mutating webhook changes objects after the build, so
// what the build checked of them need not hold of what is stored.
type MutatingWebhookConfigurationHandler struct{}

// CanHandle returns true for the mutatingwebhookconfiguration component type.
func (h *MutatingWebhookConfigurationHandler) CanHandle(componentType string) bool {
	return componentType == "mutatingwebhookconfiguration"
}

// PropertySchema declares every authorable
// admissionregistrationv1.MutatingWebhookConfiguration field by its json name.
func (h *MutatingWebhookConfigurationHandler) PropertySchema() map[string]oam.PropertySchema {
	return admissionWebhooksSchema("MutatingWebhookConfiguration", "calls to change, admit or deny objects",
		"name (required), clientConfig (required), sideEffects (required), admissionReviewVersions (required), rules, failurePolicy, matchPolicy, namespaceSelector, objectSelector, timeoutSeconds, reinvocationPolicy, matchConditions")
}

// mutatingWebhookConfigurationKind is the mutatingwebhookconfiguration kind:
// see policyFreeKind and admission_webhook_common.go.
var mutatingWebhookConfigurationKind = &policyFreeKind[admissionregistrationv1.MutatingWebhookConfiguration]{
	upstream:    "admissionregistration.k8s.io/v1 MutatingWebhookConfiguration (a mutatingwebhookconfiguration component authors its fields other than kind, apiVersion and metadata)",
	wholeObject: true,
	required:    admissionWebhookRequired,
	validate: func(config *admissionregistrationv1.MutatingWebhookConfiguration) error {
		for i, hook := range config.Webhooks {
			if err := validateAdmissionWebhook(fmt.Sprintf("webhooks[%d]", i), admissionWebhook{
				clientConfig: hook.ClientConfig, rules: hook.Rules, admissionReviewVersions: hook.AdmissionReviewVersions,
				namespaceSelector: hook.NamespaceSelector, objectSelector: hook.ObjectSelector,
			}); err != nil {
				return err
			}
		}
		return nil
	},
	build: func(name, _ string, authored *admissionregistrationv1.MutatingWebhookConfiguration) client.Object {
		identity := kubernetes.CreateMutatingWebhookConfiguration(name)
		config := authored.DeepCopy()
		config.TypeMeta, config.ObjectMeta = identity.TypeMeta, identity.ObjectMeta
		return config
	},
}

// ToApplicationConfig decodes an OAM mutatingwebhookconfiguration component
// into its config. The build namespace is not used: a
// MutatingWebhookConfiguration is cluster-scoped.
func (h *MutatingWebhookConfigurationHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return mutatingWebhookConfigurationKind.config(component)
}

// ContractMetadata implements oam.ContractDescriber.
func (h *MutatingWebhookConfigurationHandler) ContractMetadata() oam.ContractMetadata {
	return contract("mutatingwebhookconfiguration")
}

// ComponentObject declares the mutatingwebhookconfiguration kind's
// MutatingWebhookConfiguration, which is cluster-scoped.
func (h *MutatingWebhookConfigurationHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: admissionregistrationv1.GroupName, Kind: "MutatingWebhookConfiguration"}, oam.ObjectScopeCluster
}
