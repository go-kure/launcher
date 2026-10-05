package components

import (
	"encoding/base64"
	"maps"
	"slices"
	"strings"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// SecretHandler handles OAM secret components: the kind-named projection of
// corev1.Secret (go-kure/launcher#790).
//
// It emits the Secret and nothing else. The component name is the Secret's
// name, so a workload's `secret` volume, `envFrom` or `secretKeyRef` names it.
// It is a different type from the `secret` trait, which attaches a Secret to
// another component: component and trait types live in separate registries
// (pkg/oam transform.go). The trait is this kind's twin and builds its Secret
// through ParseSecretProperties and GenerateSecret.
//
// The Secret is written into the build output with its values base64-encoded,
// not encrypted, so the output is as sensitive as the document. An environment
// policy may forbid the component (oam.ExplicitSecretPolicy), as it may the
// trait. No error this kind raises carries a value.
type SecretHandler struct{}

// secretType is the secret component type.
const secretType = "secret"

// CanHandle returns true for the secret component type.
func (h *SecretHandler) CanHandle(componentType string) bool {
	return componentType == secretType
}

// PropertySchema declares the secret component's user-facing properties.
func (h *SecretHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
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

// ToApplicationConfig converts an OAM secret component to a
// SecretComponentConfig.
func (h *SecretHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	secret, err := ParseSecretProperties(component.Properties)
	if err != nil {
		return nil, err
	}
	return &SecretComponentConfig{
		Name:       component.Name,
		ObjectName: componentObjectName(component),
		Metadata:   component.ObjectMetadata(),
		Namespace:  namespace,
		Secret:     secret,
	}, nil
}

// SecretComponentConfig implements stack.ApplicationConfig for secret
// components.
type SecretComponentConfig struct {
	Name string
	// ObjectName names the Secret (oam.Component.ObjectName); its labels keep
	// Name. Empty for the application's name.
	ObjectName string
	// Metadata is the labels and annotations authored for the Secret
	// (oam.Component.ObjectMetadata).
	Metadata  oam.ObjectMetadata
	Namespace string
	// Secret carries the entries, type and immutable as ParseSecretProperties
	// parses them.
	Secret SecretConfig
}

// ApplyPolicy refuses the Secret under a policy that forbids explicit secrets
// (oam.ExplicitSecretPolicy), as the secret trait is refused. A policy that
// does not implement that interface allows it, and so does none. The transform
// reports the refusal as a violation naming the component.
func (c *SecretComponentConfig) ApplyPolicy(policy oam.Policy) error {
	if !oam.ExplicitSecretsAllowed(policy) {
		return errors.Errorf("%s: the environment policy forbids explicit secrets; reference a Secret created out of band instead", secretType)
	}
	return nil
}

// Generate creates the Secret.
func (c *SecretComponentConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	objs, err := GenerateSecret(c.Secret, kindObjectName(c.ObjectName, app.Name), app.Namespace, appLabels(app.Name))
	if err != nil {
		return nil, err
	}
	return kindObject(*objs[0], c.Metadata)
}

var _ oam.Enforceable = (*SecretComponentConfig)(nil)

// The two functions below are the one Secret path: parse and generate
// (go-kure/launcher#786). The secret kind and the secret trait both run them,
// as the configmap kind and trait share ParseConfigMapProperties and
// GenerateConfigMap, so the two build the same Secret from the same properties
// and differ only in what ownership means: the Secret's name, its labels, its
// namespace and the bundle it is placed in, all passed to GenerateSecret. No
// message they raise carries a value: an entry is named by its key, a wrong
// value by its type.

// SecretConfig is a Secret's content as authored: its entries, its type and
// immutable. Name and namespace are the caller's (GenerateSecret).
type SecretConfig struct {
	// Data holds every entry decoded: a stringData value as its bytes, a data
	// value base64-decoded.
	Data map[string][]byte
	// Type is the authored type, empty when unauthored (the API server then
	// stores Opaque).
	Type corev1.SecretType
	// Immutable is the authored immutable, nil when unauthored.
	Immutable *bool
}

// ParseSecretProperties reads stringData, data, type and immutable, applying
// the key and total-size checks ValidateSecret applies to the same fields.
// stringData values are plain strings and data values base64, as in the API; a
// key may appear in only one of them, where the API server would let
// stringData win silently. Keys are checked in sorted order, so with several
// bad entries the one reported does not depend on map iteration order. Keys it
// does not know are left to the caller: the secret trait reads `name`.
func ParseSecretProperties(props map[string]any) (SecretConfig, error) {
	var c SecretConfig

	if raw, present, err := parseObjectField(props, "stringData", "stringData"); err != nil {
		return SecretConfig{}, err
	} else if present {
		for _, k := range slices.Sorted(maps.Keys(raw)) {
			if err := ValidateSecretKey("stringData", k); err != nil {
				return SecretConfig{}, err
			}
			s, ok := raw[k].(string)
			if !ok {
				return SecretConfig{}, errors.Errorf("stringData.%s: must be a string, got %T; quote the value", k, raw[k])
			}
			if c.Data == nil {
				c.Data = map[string][]byte{}
			}
			c.Data[k] = []byte(s)
		}
	}

	if raw, present, err := parseObjectField(props, "data", "data"); err != nil {
		return SecretConfig{}, err
	} else if present {
		for _, k := range slices.Sorted(maps.Keys(raw)) {
			if err := ValidateSecretKey("data", k); err != nil {
				return SecretConfig{}, err
			}
			if _, dup := c.Data[k]; dup {
				return SecretConfig{}, errors.Errorf("data.%s: key also appears in stringData; a key may appear in only one of them", k)
			}
			s, ok := raw[k].(string)
			if !ok {
				return SecretConfig{}, errors.Errorf("data.%s: must be a base64-encoded string, got %T", k, raw[k])
			}
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				// base64's own error names the offset of the bad byte, which says
				// something about the value; the key is enough to find it.
				return SecretConfig{}, errors.Errorf("data.%s: invalid base64", k)
			}
			if c.Data == nil {
				c.Data = map[string][]byte{}
			}
			c.Data[k] = b
		}
	}

	if err := CheckSecretSize(c.Data); err != nil {
		return SecretConfig{}, err
	}

	typ, _, err := parseStringField(props, "type", "type")
	if err != nil {
		return SecretConfig{}, err
	}
	c.Type = corev1.SecretType(typ)

	immutable, err := parseBoolField(props, "immutable", "immutable")
	if err != nil {
		return SecretConfig{}, err
	}
	c.Immutable = immutable
	return c, nil
}

// GenerateSecret builds the Secret under name in namespace, with its own copy
// of labels. Every entry is emitted under data, base64-encoded as the API
// serializes it, never under stringData: the object written is then the object
// the API server stores, with nothing left for it to merge.
func GenerateSecret(c SecretConfig, name, namespace string, labels map[string]string) ([]*client.Object, error) {
	secret := kubernetes.CreateSecret(name, namespace)
	secret.Labels = maps.Clone(labels)
	secret.Annotations = nil
	secret.Type = c.Type
	if len(c.Data) > 0 {
		secret.Data = make(map[string][]byte, len(c.Data))
		for k, v := range c.Data {
			secret.Data[k] = slices.Clone(v)
		}
	}
	if c.Immutable != nil {
		immutable := *c.Immutable
		secret.Immutable = &immutable
	}
	obj := client.Object(secret)
	return []*client.Object{&obj}, nil
}

// CheckSecretSize refuses a Secret payload the API server refuses for size:
// ValidateSecret caps the summed length of every decoded value at
// corev1.MaxSecretSize (1 MiB).
func CheckSecretSize(data map[string][]byte) error {
	total := 0
	for _, v := range data {
		total += len(v)
	}
	if total > corev1.MaxSecretSize {
		return errors.Errorf("stringData and data hold %d bytes, over the %d-byte limit the API server allows a Secret", total, corev1.MaxSecretSize)
	}
	return nil
}

// ValidateSecretKey refuses a key the API server refuses in a Secret
// (IsConfigMapKey, which ValidateSecret applies to a Secret's keys too).
func ValidateSecretKey(field, key string) error {
	if errs := validation.IsConfigMapKey(key); len(errs) > 0 {
		return errors.Errorf("%s: invalid key %q: %s", field, key, strings.Join(errs, "; "))
	}
	return nil
}
