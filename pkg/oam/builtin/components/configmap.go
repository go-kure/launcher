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

// ConfigMapHandler handles OAM configmap components: the kind-named projection
// of corev1.ConfigMap (go-kure/launcher#702).
//
// It emits the ConfigMap and nothing else. The component name is the
// ConfigMap's name, so a workload's `configMap` volume or `envFrom` names it.
// It is a different type from the `configmap` trait, which attaches a
// ConfigMap to another component: component and trait types live in separate
// registries (pkg/oam transform.go).
//
// `data` values are strings, as the API's are: a number or a boolean is
// refused rather than stringified, so the stored text is exactly what was
// authored.
type ConfigMapHandler struct{}

// CanHandle returns true for the configmap component type.
func (h *ConfigMapHandler) CanHandle(componentType string) bool {
	return componentType == "configmap"
}

// PropertySchema declares the configmap component's user-facing properties.
func (h *ConfigMapHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"data": {
			Type:                 oam.PropertyTypeObject,
			AdditionalProperties: true,
			Description:          "UTF-8 entries, as key to string value. Keys may contain alphanumerics, '-', '_' and '.'.",
		},
		"binaryData": {
			Type:                 oam.PropertyTypeObject,
			AdditionalProperties: true,
			Description:          "Binary entries, as key to base64-encoded value. A key may not also appear in data.",
		},
		"immutable": {
			Type:        oam.PropertyTypeBoolean,
			Description: "When true, the API server refuses any later change to the data; the ConfigMap must be replaced instead.",
		},
	}
}

// ToApplicationConfig converts an OAM configmap component to a
// ConfigMapConfig.
func (h *ConfigMapHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	c, err := parseConfigMap(component)
	if err != nil {
		return nil, err
	}
	c.Namespace = namespace
	return c, nil
}

// ConfigMapConfig implements stack.ApplicationConfig for configmap components.
type ConfigMapConfig struct {
	Name       string
	Namespace  string
	Data       map[string]string
	BinaryData map[string][]byte
	// Immutable is the authored immutable, nil when unauthored.
	Immutable *bool
}

// Generate creates the ConfigMap.
func (c *ConfigMapConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	cm := kubernetes.CreateConfigMap(app.Name, app.Namespace)
	cm.Labels = maps.Clone(appLabels(app.Name))
	cm.Annotations = nil
	for _, k := range slices.Sorted(maps.Keys(c.Data)) {
		kubernetes.AddConfigMapData(cm, k, c.Data[k])
	}
	for _, k := range slices.Sorted(maps.Keys(c.BinaryData)) {
		kubernetes.AddConfigMapBinaryData(cm, k, slices.Clone(c.BinaryData[k]))
	}
	if c.Immutable != nil {
		kubernetes.SetConfigMapImmutable(cm, *c.Immutable)
	}
	obj := client.Object(cm)
	return []*client.Object{&obj}, nil
}

// parseConfigMap reads a configmap component's properties, applying the key
// and total-size checks ValidateConfigMap applies to the same fields.
func parseConfigMap(component *oam.Component) (*ConfigMapConfig, error) {
	props := component.Properties
	c := &ConfigMapConfig{Name: component.Name}

	if raw, present, err := parseObjectField(props, "data", "data"); err != nil {
		return nil, err
	} else if present {
		for k, v := range raw {
			if err := validateConfigMapKey("data", k); err != nil {
				return nil, err
			}
			s, ok := v.(string)
			if !ok {
				return nil, errors.Errorf("data.%s: must be a string, got %T; quote the value", k, v)
			}
			if c.Data == nil {
				c.Data = map[string]string{}
			}
			c.Data[k] = s
		}
	}

	if raw, present, err := parseObjectField(props, "binaryData", "binaryData"); err != nil {
		return nil, err
	} else if present {
		for k, v := range raw {
			if err := validateConfigMapKey("binaryData", k); err != nil {
				return nil, err
			}
			if _, dup := c.Data[k]; dup {
				return nil, errors.Errorf("binaryData.%s: key also appears in data; a key may appear in only one of them", k)
			}
			s, ok := v.(string)
			if !ok {
				return nil, errors.Errorf("binaryData.%s: must be a base64-encoded string, got %T", k, v)
			}
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return nil, errors.Errorf("binaryData.%s: invalid base64: %w", k, err)
			}
			if c.BinaryData == nil {
				c.BinaryData = map[string][]byte{}
			}
			c.BinaryData[k] = b
		}
	}

	// ValidateConfigMap also caps the summed size of every data value and every
	// decoded binaryData value; over it the API server refuses the ConfigMap.
	total := 0
	for _, v := range c.Data {
		total += len(v)
	}
	for _, v := range c.BinaryData {
		total += len(v)
	}
	if total > corev1.MaxSecretSize {
		return nil, errors.Errorf("data and binaryData hold %d bytes, over the %d-byte limit the API server allows a ConfigMap", total, corev1.MaxSecretSize)
	}

	immutable, err := parseBoolField(props, "immutable", "immutable")
	if err != nil {
		return nil, err
	}
	c.Immutable = immutable
	return c, nil
}

// validateConfigMapKey refuses a key the API server refuses in a ConfigMap
// (IsConfigMapKey).
func validateConfigMapKey(field, key string) error {
	if errs := validation.IsConfigMapKey(key); len(errs) > 0 {
		return errors.Errorf("%s: invalid key %q: %s", field, key, strings.Join(errs, "; "))
	}
	return nil
}
