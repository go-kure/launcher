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
// registries (pkg/oam transform.go). The trait is this kind's twin and builds
// its ConfigMap through ParseConfigMapProperties and GenerateConfigMap.
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
	Name string
	// ObjectName names the ConfigMap (oam.Component.ObjectName); its labels
	// keep Name. Empty for the application's name.
	ObjectName string
	Namespace  string
	Data       map[string]string
	BinaryData map[string][]byte
	// Immutable is the authored immutable, nil when unauthored.
	Immutable *bool
}

// Generate creates the ConfigMap.
func (c *ConfigMapConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	return GenerateConfigMap(*c, kindObjectName(c.ObjectName, app.Name), app.Namespace, appLabels(app.Name))
}

// parseConfigMap reads a configmap component's properties.
func parseConfigMap(component *oam.Component) (*ConfigMapConfig, error) {
	c, err := ParseConfigMapProperties(component.Properties)
	if err != nil {
		return nil, err
	}
	c.Name = component.Name
	c.ObjectName = componentObjectName(component)
	return &c, nil
}

// The two functions below are the kind's whole ConfigMap path: parse and
// generate. The configmap trait, the kind's twin (go-kure/launcher#741), runs
// the same two, so the two build the same ConfigMap from the same properties
// and differ only in what ownership means: the ConfigMap's name, its labels,
// its namespace and the bundle it is placed in, all passed to
// GenerateConfigMap.

// ParseConfigMapProperties reads data, binaryData and immutable, applying the
// key and total-size checks ValidateConfigMap applies to the same fields. Keys
// are checked in sorted order, so with several bad entries the one reported
// does not depend on map iteration order. Keys it does not know are left to
// the caller: the configmap trait reads `name` and `mountPath`. The returned
// Name and Namespace are unset.
func ParseConfigMapProperties(props map[string]any) (ConfigMapConfig, error) {
	var c ConfigMapConfig

	if raw, present, err := parseObjectField(props, "data", "data"); err != nil {
		return ConfigMapConfig{}, err
	} else if present {
		for _, k := range slices.Sorted(maps.Keys(raw)) {
			v := raw[k]
			if err := ValidateConfigMapKey("data", k); err != nil {
				return ConfigMapConfig{}, err
			}
			s, ok := v.(string)
			if !ok {
				return ConfigMapConfig{}, errors.Errorf("data.%s: must be a string, got %T; quote the value", k, v)
			}
			if c.Data == nil {
				c.Data = map[string]string{}
			}
			c.Data[k] = s
		}
	}

	if raw, present, err := parseObjectField(props, "binaryData", "binaryData"); err != nil {
		return ConfigMapConfig{}, err
	} else if present {
		for _, k := range slices.Sorted(maps.Keys(raw)) {
			v := raw[k]
			if err := ValidateConfigMapKey("binaryData", k); err != nil {
				return ConfigMapConfig{}, err
			}
			if _, dup := c.Data[k]; dup {
				return ConfigMapConfig{}, errors.Errorf("binaryData.%s: key also appears in data; a key may appear in only one of them", k)
			}
			s, ok := v.(string)
			if !ok {
				return ConfigMapConfig{}, errors.Errorf("binaryData.%s: must be a base64-encoded string, got %T", k, v)
			}
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return ConfigMapConfig{}, errors.Errorf("binaryData.%s: invalid base64: %w", k, err)
			}
			if c.BinaryData == nil {
				c.BinaryData = map[string][]byte{}
			}
			c.BinaryData[k] = b
		}
	}

	if err := CheckConfigMapSize(c.Data, c.BinaryData); err != nil {
		return ConfigMapConfig{}, err
	}

	immutable, err := parseBoolField(props, "immutable", "immutable")
	if err != nil {
		return ConfigMapConfig{}, err
	}
	c.Immutable = immutable
	return c, nil
}

// GenerateConfigMap builds the ConfigMap under name in namespace, with its
// own copy of labels and its entries added in sorted key order.
func GenerateConfigMap(c ConfigMapConfig, name, namespace string, labels map[string]string) ([]*client.Object, error) {
	cm := kubernetes.CreateConfigMap(name, namespace)
	cm.Labels = maps.Clone(labels)
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

// CheckConfigMapSize refuses a ConfigMap payload the API server refuses for
// size: ValidateConfigMap caps the summed length of every data value and every
// decoded binaryData value at corev1.MaxSecretSize (1 MiB). The configmap
// component and the configmap trait both call it, so they refuse the same
// payloads with the same message.
func CheckConfigMapSize(data map[string]string, binaryData map[string][]byte) error {
	total := 0
	for _, v := range data {
		total += len(v)
	}
	for _, v := range binaryData {
		total += len(v)
	}
	if total > corev1.MaxSecretSize {
		return errors.Errorf("data and binaryData hold %d bytes, over the %d-byte limit the API server allows a ConfigMap", total, corev1.MaxSecretSize)
	}
	return nil
}

// ValidateConfigMapKey refuses a key the API server refuses in a ConfigMap
// (IsConfigMapKey). The configmap component and the configmap trait both call
// it, so they refuse the same keys with the same message.
func ValidateConfigMapKey(field, key string) error {
	if errs := validation.IsConfigMapKey(key); len(errs) > 0 {
		return errors.Errorf("%s: invalid key %q: %s", field, key, strings.Join(errs, "; "))
	}
	return nil
}
