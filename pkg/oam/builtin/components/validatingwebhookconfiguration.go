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

// ValidatingWebhookConfigurationHandler handles OAM
// validatingwebhookconfiguration components: the kind-named projection of a
// cluster-scoped admissionregistration.k8s.io/v1 ValidatingWebhookConfiguration
// (go-kure/launcher#943).
//
// A ValidatingWebhookConfiguration has no spec: its one property, `webhooks`,
// is the object's own field, decoded strictly; its kind, apiVersion and
// metadata are launcher's to set and are refused. It emits the configuration,
// named after the component unless `objectName` names it, with no namespace,
// and nothing else. See admission_webhook_common.go for what gates it and what
// the build does not reach.
type ValidatingWebhookConfigurationHandler struct{}

// CanHandle returns true for the validatingwebhookconfiguration component
// type.
func (h *ValidatingWebhookConfigurationHandler) CanHandle(componentType string) bool {
	return componentType == "validatingwebhookconfiguration"
}

// PropertySchema declares every authorable
// admissionregistrationv1.ValidatingWebhookConfiguration field by its json
// name.
func (h *ValidatingWebhookConfigurationHandler) PropertySchema() map[string]oam.PropertySchema {
	return admissionWebhooksSchema("ValidatingWebhookConfiguration", "calls to admit or deny",
		"name (required), clientConfig (required), sideEffects (required), admissionReviewVersions (required), rules, failurePolicy, matchPolicy, namespaceSelector, objectSelector, timeoutSeconds, matchConditions")
}

// validatingWebhookConfigurationKind is the validatingwebhookconfiguration
// kind: see policyFreeKind and admission_webhook_common.go.
var validatingWebhookConfigurationKind = &policyFreeKind[admissionregistrationv1.ValidatingWebhookConfiguration]{
	upstream:    "admissionregistration.k8s.io/v1 ValidatingWebhookConfiguration (a validatingwebhookconfiguration component authors its fields other than kind, apiVersion and metadata)",
	wholeObject: true,
	required:    admissionWebhookRequired,
	validate: func(config *admissionregistrationv1.ValidatingWebhookConfiguration) error {
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
	build: func(name, _ string, authored *admissionregistrationv1.ValidatingWebhookConfiguration) client.Object {
		identity := kubernetes.CreateValidatingWebhookConfiguration(name)
		config := authored.DeepCopy()
		config.TypeMeta, config.ObjectMeta = identity.TypeMeta, identity.ObjectMeta
		return config
	},
}

// ToApplicationConfig decodes an OAM validatingwebhookconfiguration component
// into its config. The build namespace is not used: a
// ValidatingWebhookConfiguration is cluster-scoped.
func (h *ValidatingWebhookConfigurationHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return validatingWebhookConfigurationKind.config(component)
}

// ContractMetadata implements oam.ContractDescriber.
func (h *ValidatingWebhookConfigurationHandler) ContractMetadata() oam.ContractMetadata {
	return contract("validatingwebhookconfiguration")
}

// ComponentObject declares the validatingwebhookconfiguration kind's
// ValidatingWebhookConfiguration, which is cluster-scoped.
func (h *ValidatingWebhookConfigurationHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: admissionregistrationv1.GroupName, Kind: "ValidatingWebhookConfiguration"}, oam.ObjectScopeCluster
}
