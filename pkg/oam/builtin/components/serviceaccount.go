package components

import (
	"maps"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// ServiceAccountHandler handles OAM serviceaccount components: the kind-named
// projection of corev1.ServiceAccount (go-kure/launcher#702).
//
// It emits the ServiceAccount and nothing else. The component name is the
// account's name, so a workload component's `serviceAccountName` names it.
//
// It is a projection, not an opinion: an unauthored
// `automountServiceAccountToken` stays unset, which Kubernetes reads as true.
// The account a workload kind generates for itself sets it to false; a
// `serviceaccount` component standing in for that account authors false.
type ServiceAccountHandler struct{}

// CanHandle returns true for the serviceaccount component type.
func (h *ServiceAccountHandler) CanHandle(componentType string) bool {
	return componentType == "serviceaccount"
}

// PropertySchema declares the serviceaccount component's user-facing properties.
func (h *ServiceAccountHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"automountServiceAccountToken": {
			Type:        oam.PropertyTypeBoolean,
			Description: "Whether pods running as this account get its API token mounted. Unset leaves it to Kubernetes, which mounts it; a pod's own automountServiceAccountToken overrides it.",
		},
		"imagePullSecrets": {
			Type:        oam.PropertyTypeArray,
			Description: "Secrets in the component's namespace holding registry credentials, added to every pod running as this account.",
			Items: &oam.PropertySchema{
				Type:        oam.PropertyTypeObject,
				Description: "A reference to one image-pull Secret.",
				Properties: map[string]oam.PropertySchema{
					"name": {Type: oam.PropertyTypeString, Required: true, Description: "Name of the referenced object."},
				},
			},
		},
	}
}

// ToApplicationConfig converts an OAM serviceaccount component to a
// ServiceAccountConfig.
func (h *ServiceAccountHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	c, err := parseServiceAccount(component)
	if err != nil {
		return nil, err
	}
	c.Namespace = namespace
	return c, nil
}

// ServiceAccountConfig implements stack.ApplicationConfig for serviceaccount
// components.
type ServiceAccountConfig struct {
	Name string
	// ObjectName names the ServiceAccount (oam.Component.ObjectName); its
	// labels keep the application's name. Empty for Name.
	ObjectName string
	Namespace  string
	// AutomountToken is the authored automountServiceAccountToken, nil when
	// unauthored.
	AutomountToken   *bool
	ImagePullSecrets []corev1.LocalObjectReference
}

// ServiceAccountName implements oam.ServiceAccountNamer for an account named
// by ObjectName: the `rbac` trait on this component then grants its rules to
// the account as it is named. Without one the answer is "", and the trait keeps
// the component name, which is the account's. A ServiceAccount runs no pods.
func (c *ServiceAccountConfig) ServiceAccountName() (name string, runsPods bool) {
	if c.ObjectName != c.Name {
		return c.ObjectName, false
	}
	return "", false
}

// Generate creates the ServiceAccount.
func (c *ServiceAccountConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	// Named from the component, so a role rule's deployment member, which is
	// handed that same name as its serviceAccountName, runs as exactly this
	// account; the Application's name is the fallback for a config built
	// without one. An authored component's `objectName` names it instead.
	name := kindObjectName(c.ObjectName, c.Name)
	if name == "" {
		name = app.Name
	}
	sa := kubernetes.CreateServiceAccount(name, app.Namespace)
	sa.Labels = maps.Clone(appLabels(app.Name))
	sa.Annotations = nil
	if c.AutomountToken != nil {
		kubernetes.SetServiceAccountAutomountToken(sa, *c.AutomountToken)
	}
	for _, s := range c.ImagePullSecrets {
		kubernetes.AddServiceAccountImagePullSecret(sa, s)
	}
	obj := client.Object(sa)
	return []*client.Object{&obj}, nil
}

// parseServiceAccount reads a serviceaccount component's properties.
func parseServiceAccount(component *oam.Component) (*ServiceAccountConfig, error) {
	props := component.Properties
	c := &ServiceAccountConfig{Name: component.Name, ObjectName: componentObjectName(component)}

	automount, err := parseBoolField(props, "automountServiceAccountToken", "automountServiceAccountToken")
	if err != nil {
		return nil, err
	}
	c.AutomountToken = automount

	list, present, err := parseObjectList(props, "imagePullSecrets")
	if err != nil {
		return nil, err
	}
	if present {
		for i, m := range list {
			label := indexedLabel("imagePullSecrets", i)
			if err := rejectUnknownKeys(m, []string{"name"}, label); err != nil {
				return nil, err
			}
			name, err := requireDNS1123Subdomain(m, "name", label)
			if err != nil {
				return nil, err
			}
			c.ImagePullSecrets = append(c.ImagePullSecrets, corev1.LocalObjectReference{Name: name})
		}
	}
	return c, nil
}
