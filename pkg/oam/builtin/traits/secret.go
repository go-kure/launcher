package traits

import (
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// secretTraitType is the secret trait type.
const secretTraitType = "secret"

// SecretHandler handles OAM secret traits (go-kure/launcher#786): a Secret
// whose content the document carries itself, owned by the component. It is
// what a helm component's secretValues lowers to, and is authorable too.
//
// The Secret is built by components.ParseSecretProperties and GenerateSecret;
// the trait adds only what ownership means: the Secret's name (its `name`
// property), the owner's labels and namespace, and the owner's bundle. It
// mounts nothing into the owner's workload.
//
// The Secret is written into the build output with its values base64-encoded,
// not encrypted, so the output is as sensitive as the document. An environment
// policy may forbid the trait (oam.ExplicitSecretPolicy); the violation names
// the component. No error this trait raises carries a value.
type SecretHandler struct{}

// CanHandle returns true for the secret trait type.
func (h *SecretHandler) CanHandle(traitType string) bool {
	return traitType == secretTraitType
}

// PropertySchema declares the secret trait's user-facing properties.
func (h *SecretHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"name": {Type: oam.PropertyTypeString, Required: true, Description: "Name of the Secret resource to create."},
		"stringData": {
			Type:                 oam.PropertyTypeObject,
			AdditionalProperties: true,
			Description:          "Entries as key to plain string value. Keys may contain alphanumerics, '-', '_' and '.'. Emitted base64-encoded under data, not encrypted.",
		},
		"data": {
			Type:                 oam.PropertyTypeObject,
			AdditionalProperties: true,
			Description:          "Entries as key to base64-encoded value. A key may not also appear in stringData.",
		},
		"type": {Type: oam.PropertyTypeString, Description: "Secret type, e.g. kubernetes.io/tls. Unset means Opaque. The keys a type requires are left to the API server."},
		"immutable": {
			Type:        oam.PropertyTypeBoolean,
			Description: "When true, the API server refuses any later change to the entries; the Secret must be replaced instead.",
		},
	}
}

// ValidateAndApplyDefaults rejects any rendering key for this no-rendering trait.
func (h *SecretHandler) ValidateAndApplyDefaults(rendering map[string]any) (map[string]any, error) {
	if _, err := builtin.DecodeStrict[builtin.SecretRendering](rendering); err != nil {
		return nil, errors.Wrap(err, "secret rendering")
	}
	return rendering, nil
}

// Apply appends the Secret's sub-application to the bundle.
func (h *SecretHandler) Apply(trait *oam.Trait, app *stack.Application, bundle *stack.Bundle) error {
	props := trait.Properties

	name, ok := props["name"].(string)
	if !ok || name == "" {
		return errors.New("required property 'name' missing or not a string")
	}

	secret, err := components.ParseSecretProperties(props)
	if err != nil {
		return errors.Wrapf(err, "secret trait %q", name)
	}
	cfg := &SecretConfig{
		Name:          name,
		componentName: app.Name,
		Secret:        secret,
	}
	bundle.Applications = append(bundle.Applications, stack.NewApplication(name, app.Namespace, cfg))
	return nil
}

// SecretConfig implements stack.ApplicationConfig for secret traits.
type SecretConfig struct {
	// Name is the Secret's name.
	Name          string
	componentName string
	// Secret carries the entries, type and immutable as
	// components.ParseSecretProperties parses them.
	Secret components.SecretConfig
}

// ComponentName returns the OAM component this sub-app belongs to, for resource
// provenance attribution.
func (c *SecretConfig) ComponentName() string { return c.componentName }

// FluxNamespaceInput names the Secret, so it follows its component's Flux
// object to the Flux namespace when that object reads it (a helmrelease's
// valuesFrom). Satisfies pkg/oam.fluxNamespaceInput.
func (c *SecretConfig) FluxNamespaceInput() (kind, name string) { return "Secret", c.Name }

// ApplyPolicy refuses the Secret under a policy that forbids explicit secrets
// (oam.ExplicitSecretPolicy). A policy that does not implement that interface
// allows it. The transform reports the refusal as a violation naming the
// component.
func (c *SecretConfig) ApplyPolicy(policy oam.Policy) error {
	if !oam.ExplicitSecretsAllowed(policy) {
		return errors.Errorf("secret %q: the environment policy forbids explicit secrets; reference a Secret created out of band instead", c.Name)
	}
	return nil
}

// Generate builds the Secret through components.GenerateSecret, under the
// trait's name and with the owning component's labels.
func (c *SecretConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	return components.GenerateSecret(c.Secret, app.Name, app.Namespace, componentLabels(c.componentName))
}

var _ oam.Enforceable = (*SecretConfig)(nil)
