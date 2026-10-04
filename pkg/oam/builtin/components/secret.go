package components

import (
	"encoding/base64"
	"maps"
	"slices"
	"strings"

	"github.com/go-kure/kure/pkg/kubernetes"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
)

// The two functions below are the one Secret path: parse and generate
// (go-kure/launcher#786). The secret trait runs them, and a kind-named secret
// component wraps the same two, as the configmap kind and trait share
// ParseConfigMapProperties and GenerateConfigMap. No message they raise carries
// a value: an entry is named by its key, a wrong value by its type.

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
