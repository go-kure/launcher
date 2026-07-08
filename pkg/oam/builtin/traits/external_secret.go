package traits

import (
	"fmt"
	"time"

	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	"github.com/go-kure/kure/pkg/kubernetes/externalsecrets"
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// ExternalSecretHandler handles OAM external-secret traits.
type ExternalSecretHandler struct{}

// CanHandle returns true for external-secret trait type.
func (h *ExternalSecretHandler) CanHandle(traitType string) bool {
	return traitType == "external-secret"
}

// CapabilityRequired returns false: secretStoreRef can be provided inline
// (via trait properties or crane's provider: shorthand) without a ClusterProfile
// capability. When both inline and capability are absent, parseProperties returns
// a clear error.
func (h *ExternalSecretHandler) CapabilityRequired() bool { return false }

// ValidateAndApplyDefaults validates the external-secret capability rendering.
func (h *ExternalSecretHandler) ValidateAndApplyDefaults(rendering map[string]any) (map[string]any, error) {
	r, err := builtin.DecodeStrict[builtin.ExternalSecretRendering](rendering)
	if err != nil {
		return nil, errors.Wrap(err, "external-secret rendering")
	}
	rawRef, _ := rendering["secretStoreRef"].(map[string]any)
	if rawRef == nil || r.SecretStoreRef.Name == "" {
		return nil, errors.New("external-secret rendering: secretStoreRef.name is required")
	}
	if r.SecretStoreRef.Kind == "" {
		rawRef["kind"] = "ClusterSecretStore"
	}
	return rendering, nil
}

// Apply creates an ExternalSecret resource appended to the bundle.
func (h *ExternalSecretHandler) Apply(trait *oam.Trait, app *stack.Application, bundle *stack.Bundle) error {
	config, err := h.parseProperties(trait.Properties, app)
	if err != nil {
		return err
	}

	esApp := stack.NewApplication(
		app.Name+"-external-secret-"+config.SecretName,
		app.Namespace,
		config,
	)
	bundle.Applications = append(bundle.Applications, esApp)
	return nil
}

// PropertySchema declares the external-secret trait's user-facing properties.
func (h *ExternalSecretHandler) PropertySchema() map[string]oam.PropertySchema {
	remoteRef := oam.PropertySchema{
		Type:        oam.PropertyTypeObject,
		Description: "Reference to a key in the external secret store.",
		Properties: map[string]oam.PropertySchema{
			"key":              {Type: oam.PropertyTypeString, Required: true, Description: "Key or path of the secret in the remote store."},
			"property":         {Type: oam.PropertyTypeString, Description: "Specific property within the remote secret to extract."},
			"version":          {Type: oam.PropertyTypeString, Description: "Version of the remote secret to fetch."},
			"decodingStrategy": {Type: oam.PropertyTypeString, Description: "How the fetched value is decoded (e.g. Base64, None)."},
		},
	}
	requiredRemoteRef := remoteRef
	requiredRemoteRef.Required = true
	return map[string]oam.PropertySchema{
		"secretName": {Type: oam.PropertyTypeString, Required: true, Description: "Name of the generated ExternalSecret and the default target Secret."},
		"secretStoreRef": {
			Type:        oam.PropertyTypeObject,
			Description: "Reference to the SecretStore or ClusterSecretStore backing this secret.",
			Properties: map[string]oam.PropertySchema{
				"name": {Type: oam.PropertyTypeString, Required: true, Description: "Name of the referenced secret store."},
				"kind": {Type: oam.PropertyTypeString, Default: "ClusterSecretStore", Description: "Kind of the referenced store (SecretStore or ClusterSecretStore)."},
			},
		},
		"provider":         {Type: oam.PropertyTypeString, Description: "Shorthand naming a ClusterSecretStore, used when secretStoreRef is not set."},
		"refreshInterval":  {Type: oam.PropertyTypeString, Default: "1h", Description: "How often the secret is re-fetched from the store (e.g. 1h)."},
		"targetSecretName": {Type: oam.PropertyTypeString, Description: "Overrides the name of the produced Kubernetes Secret (defaults to secretName)."},
		"target": {
			Type:        oam.PropertyTypeObject,
			Description: "Configuration for the Kubernetes Secret this ExternalSecret produces.",
			Properties: map[string]oam.PropertySchema{
				"creationPolicy": {Type: oam.PropertyTypeString, Enum: []any{"Owner", "Orphan", "Merge", "None"}, Description: "Controls how the target Secret is created and owned."},
				"deletionPolicy": {Type: oam.PropertyTypeString, Enum: []any{"Delete", "Merge", "Retain"}, Description: "Controls what happens to the target Secret when source data is removed."},
				"template": {
					Type:        oam.PropertyTypeObject,
					Description: "Template shaping the generated Secret's type and data.",
					Properties: map[string]oam.PropertySchema{
						"type": {Type: oam.PropertyTypeString, Description: "Type of the generated Kubernetes Secret (e.g. Opaque)."},
						"data": {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Templated key/value entries rendered into the Secret."},
					},
				},
			},
		},
		"data": {
			Type:        oam.PropertyTypeArray,
			Description: "Explicit mappings from remote store keys to entries in the target Secret.",
			Items: &oam.PropertySchema{
				Type:        oam.PropertyTypeObject,
				Description: "A single mapping of a remote reference to a target Secret key.",
				Properties: map[string]oam.PropertySchema{
					"secretKey": {Type: oam.PropertyTypeString, Required: true, Description: "Key in the target Secret to populate."},
					"remoteRef": requiredRemoteRef,
				},
			},
		},
		"dataFrom": {
			Type:        oam.PropertyTypeArray,
			Description: "Bulk imports of secret data via extract or find queries.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "A single extract or find query pulling multiple keys."},
		},
		"remoteRef": remoteRef,
	}
}

func (h *ExternalSecretHandler) parseProperties(props map[string]any, app *stack.Application) (*ExternalSecretConfig, error) {
	config := &ExternalSecretConfig{
		ComponentName:   app.Name,
		RefreshInterval: "1h",
	}

	secretName, ok := props["secretName"].(string)
	if !ok || secretName == "" {
		return nil, errors.New("required property 'secretName' missing or not a string")
	}
	config.SecretName = secretName

	rawStoreRef, _ := props["secretStoreRef"].(map[string]any)
	if rawStoreRef == nil {
		// Accept crane's bare provider: string shorthand.
		if provider, ok := props["provider"].(string); ok && provider != "" {
			config.StoreRefName = provider
			config.StoreRefKind = "ClusterSecretStore"
		} else {
			return nil, errors.New("external-secret: secretStoreRef (or provider) is required; set inline or via ClusterProfile capability")
		}
	} else {
		storeName, _ := rawStoreRef["name"].(string)
		if storeName == "" {
			return nil, errors.New("external-secret: secretStoreRef.name is required")
		}
		config.StoreRefName = storeName
		config.StoreRefKind = "ClusterSecretStore"
		if kind, ok := rawStoreRef["kind"].(string); ok && kind != "" {
			config.StoreRefKind = kind
		}
	}

	if ri, ok := props["refreshInterval"].(string); ok && ri != "" {
		config.RefreshInterval = ri
	}

	config.TargetSecretName = secretName
	if tsn, ok := props["targetSecretName"].(string); ok && tsn != "" {
		config.TargetSecretName = tsn
	}

	if rawTarget, ok := props["target"].(map[string]any); ok {
		if cp, ok := rawTarget["creationPolicy"].(string); ok && cp != "" {
			validCreationPolicies := map[creationPolicy]bool{
				creationPolicyOwner:  true,
				creationPolicyOrphan: true,
				creationPolicyMerge:  true,
				creationPolicyNone:   true,
			}
			if !validCreationPolicies[creationPolicy(cp)] {
				return nil, errors.Errorf("target.creationPolicy %q is invalid; must be one of Owner, Orphan, Merge, None", cp)
			}
			config.CreationPolicy = creationPolicy(cp)
		}
		if dp, ok := rawTarget["deletionPolicy"].(string); ok && dp != "" {
			validDeletionPolicies := map[deletionPolicy]bool{
				deletionPolicyDelete: true,
				deletionPolicyMerge:  true,
				deletionPolicyRetain: true,
			}
			if !validDeletionPolicies[deletionPolicy(dp)] {
				return nil, errors.Errorf("target.deletionPolicy %q is invalid; must be one of Delete, Merge, Retain", dp)
			}
			config.DeletionPolicy = deletionPolicy(dp)
		}
		if rawTemplate, ok := rawTarget["template"].(map[string]any); ok {
			tmpl := &esTemplate{}
			if t, ok := rawTemplate["type"].(string); ok {
				tmpl.Type = t
			}
			if rawData, ok := rawTemplate["data"].(map[string]any); ok {
				tmpl.Data = make(map[string]string, len(rawData))
				for k, v := range rawData {
					tmpl.Data[k] = fmt.Sprintf("%v", v)
				}
			}
			config.Template = tmpl
		}
	}

	if rawData, ok := props["data"].([]any); ok {
		for i, item := range rawData {
			entry, ok := item.(map[string]any)
			if !ok {
				return nil, errors.Errorf("data[%d]: expected object", i)
			}
			secretKey, _ := entry["secretKey"].(string)
			if secretKey == "" {
				return nil, errors.Errorf("data[%d]: required field 'secretKey' missing or empty", i)
			}
			rawRef, ok := entry["remoteRef"].(map[string]any)
			if !ok {
				return nil, errors.Errorf("data[%d]: required field 'remoteRef' missing or not an object", i)
			}
			key, _ := rawRef["key"].(string)
			if key == "" {
				return nil, errors.Errorf("data[%d].remoteRef: required field 'key' missing or empty", i)
			}
			ref := esRemoteRef{Key: key}
			if p, ok := rawRef["property"].(string); ok {
				ref.Property = p
			}
			if v, ok := rawRef["version"].(string); ok {
				ref.Version = v
			}
			if ds, ok := rawRef["decodingStrategy"].(string); ok {
				ref.DecodingStrategy = ds
			}
			config.Data = append(config.Data, esDataEntry{
				SecretKey: secretKey,
				RemoteRef: ref,
			})
		}
	}

	if rawDataFrom, ok := props["dataFrom"].([]any); ok {
		for i, item := range rawDataFrom {
			entry, ok := item.(map[string]any)
			if !ok {
				return nil, errors.Errorf("dataFrom[%d]: expected object", i)
			}
			dfEntry := esDataFromEntry{}
			if rawExtract, ok := entry["extract"].(map[string]any); ok {
				key, _ := rawExtract["key"].(string)
				if key == "" {
					return nil, errors.Errorf("dataFrom[%d].extract: required field 'key' missing or empty", i)
				}
				ref := &esExtractRef{Key: key}
				if ds, ok := rawExtract["decodingStrategy"].(string); ok {
					ref.DecodingStrategy = ds
				}
				if cs, ok := rawExtract["conversionStrategy"].(string); ok {
					ref.ConversionStrategy = cs
				}
				if mp, ok := rawExtract["metadataPolicy"].(string); ok {
					ref.MetadataPolicy = mp
				}
				dfEntry.Extract = ref
			}
			if rawFind, ok := entry["find"].(map[string]any); ok {
				find := &esFind{}
				if rawName, ok := rawFind["name"].(map[string]any); ok {
					if re, ok := rawName["regexp"].(string); ok {
						find.Name = &esFindName{RegExp: re}
					}
				}
				if rawTags, ok := rawFind["tags"].(map[string]any); ok {
					find.Tags = make(map[string]string, len(rawTags))
					for k, v := range rawTags {
						find.Tags[k] = fmt.Sprintf("%v", v)
					}
				}
				dfEntry.Find = find
				if find.Name == nil && len(find.Tags) == 0 {
					return nil, errors.Errorf("dataFrom[%d].find: must have at least 'name' or 'tags'", i)
				}
			}
			if dfEntry.Extract == nil && dfEntry.Find == nil {
				return nil, errors.Errorf("dataFrom[%d]: must have 'extract' or 'find'", i)
			}
			config.DataFrom = append(config.DataFrom, dfEntry)
		}
	}

	// Shorthand: top-level remoteRef maps to a single data entry where secretKey=secretName.
	// Matches the shape used in launcher examples (e.g. examples/04-webservice-full.yaml).
	if rawRef, ok := props["remoteRef"].(map[string]any); ok {
		if len(config.Data) > 0 || len(config.DataFrom) > 0 {
			return nil, errors.New("external-secret: 'remoteRef' cannot be combined with 'data' or 'dataFrom'")
		}
		key, _ := rawRef["key"].(string)
		if key == "" {
			return nil, errors.New("external-secret: remoteRef.key is required")
		}
		ref := esRemoteRef{Key: key}
		if p, ok := rawRef["property"].(string); ok {
			ref.Property = p
		}
		if v, ok := rawRef["version"].(string); ok {
			ref.Version = v
		}
		if ds, ok := rawRef["decodingStrategy"].(string); ok {
			ref.DecodingStrategy = ds
		}
		config.Data = append(config.Data, esDataEntry{
			SecretKey: config.SecretName,
			RemoteRef: ref,
		})
	}

	return config, nil
}

type creationPolicy string

const (
	creationPolicyOwner  creationPolicy = "Owner"
	creationPolicyOrphan creationPolicy = "Orphan"
	creationPolicyMerge  creationPolicy = "Merge"
	creationPolicyNone   creationPolicy = "None"
)

type deletionPolicy string

const (
	deletionPolicyDelete deletionPolicy = "Delete"
	deletionPolicyMerge  deletionPolicy = "Merge"
	deletionPolicyRetain deletionPolicy = "Retain"
)

type esDataEntry struct {
	SecretKey string
	RemoteRef esRemoteRef
}

type esRemoteRef struct {
	Key              string
	Property         string
	Version          string
	DecodingStrategy string
}

type esExtractRef struct {
	Key                string
	DecodingStrategy   string
	ConversionStrategy string
	MetadataPolicy     string
}

type esDataFromEntry struct {
	Extract *esExtractRef
	Find    *esFind
}

type esFind struct {
	Name *esFindName
	Tags map[string]string
}

type esFindName struct {
	RegExp string
}

type esTemplate struct {
	Type string
	Data map[string]string
}

// ExternalSecretConfig implements stack.ApplicationConfig for external-secret traits.
type ExternalSecretConfig struct {
	SecretName       string
	ComponentName    string
	StoreRefName     string
	StoreRefKind     string
	RefreshInterval  string
	TargetSecretName string
	CreationPolicy   creationPolicy
	DeletionPolicy   deletionPolicy
	Template         *esTemplate
	Data             []esDataEntry
	DataFrom         []esDataFromEntry
}

// Generate creates an ExternalSecret CRD resource.
func (c *ExternalSecretConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	dur, err := time.ParseDuration(c.RefreshInterval)
	if err != nil {
		return nil, errors.Errorf("invalid refreshInterval %q: %w", c.RefreshInterval, err)
	}

	es := externalsecrets.ExternalSecret(&externalsecrets.ExternalSecretConfig{
		Name:      c.SecretName,
		Namespace: app.Namespace,
		SecretStoreRef: esv1.SecretStoreRef{
			Name: c.StoreRefName,
			Kind: c.StoreRefKind,
		},
	})
	externalsecrets.AddExternalSecretLabel(es, "app", c.ComponentName)
	externalsecrets.SetRefreshInterval(es, metav1.Duration{Duration: dur})

	target := esv1.ExternalSecretTarget{Name: c.TargetSecretName}
	if c.CreationPolicy != "" {
		target.CreationPolicy = esv1.ExternalSecretCreationPolicy(c.CreationPolicy)
	}
	if c.DeletionPolicy != "" {
		target.DeletionPolicy = esv1.ExternalSecretDeletionPolicy(c.DeletionPolicy)
	}
	if c.Template != nil {
		target.Template = &esv1.ExternalSecretTemplate{
			Type: corev1.SecretType(c.Template.Type),
			Data: c.Template.Data,
		}
	}
	externalsecrets.SetTarget(es, target)

	for _, d := range c.Data {
		ref := esv1.ExternalSecretDataRemoteRef{Key: d.RemoteRef.Key}
		if d.RemoteRef.Property != "" {
			ref.Property = d.RemoteRef.Property
		}
		if d.RemoteRef.Version != "" {
			ref.Version = d.RemoteRef.Version
		}
		if d.RemoteRef.DecodingStrategy != "" {
			ref.DecodingStrategy = esv1.ExternalSecretDecodingStrategy(d.RemoteRef.DecodingStrategy)
		}
		externalsecrets.AddExternalSecretData(es, esv1.ExternalSecretData{
			SecretKey: d.SecretKey,
			RemoteRef: ref,
		})
	}

	for _, df := range c.DataFrom {
		entry := esv1.ExternalSecretDataFromRemoteRef{}
		if df.Extract != nil {
			extract := &esv1.ExternalSecretDataRemoteRef{Key: df.Extract.Key}
			if df.Extract.DecodingStrategy != "" {
				extract.DecodingStrategy = esv1.ExternalSecretDecodingStrategy(df.Extract.DecodingStrategy)
			}
			if df.Extract.ConversionStrategy != "" {
				extract.ConversionStrategy = esv1.ExternalSecretConversionStrategy(df.Extract.ConversionStrategy)
			}
			if df.Extract.MetadataPolicy != "" {
				extract.MetadataPolicy = esv1.ExternalSecretMetadataPolicy(df.Extract.MetadataPolicy)
			}
			entry.Extract = extract
		}
		if df.Find != nil {
			find := &esv1.ExternalSecretFind{Tags: df.Find.Tags}
			if df.Find.Name != nil {
				find.Name = &esv1.FindName{RegExp: df.Find.Name.RegExp}
			}
			entry.Find = find
		}
		externalsecrets.AddDataFrom(es, entry)
	}

	obj := client.Object(es)
	return []*client.Object{&obj}, nil
}
