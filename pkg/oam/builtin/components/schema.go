package components

import "github.com/go-kure/launcher/pkg/oam"

// This file holds PropertySchema fragments shared by the container-workload
// component handlers (webservice/worker/cronjob/daemonset/statefulset). Each
// fragment mirrors a shared parser in common.go (parseEnv, parseResources, …).
// Deeply nested or K8s-adjacent shapes are intentionally kept shallow/open
// (AdditionalProperties) rather than modeled field-by-field — the constrained
// PropertySchema vocabulary describes the user-facing surface, not every nested
// object. Each accessor returns a fresh value so consumers can't mutate shared
// state.

func accessModesEnum() []any {
	return []any{"ReadWriteOnce", "ReadOnlyMany", "ReadWriteMany", "ReadWriteOncePod"}
}

// schemaEnv describes the shared `env` property (see parseEnv). `valueFrom` is
// kept open — it carries secretKeyRef/configMapKeyRef sub-objects.
func schemaEnv() oam.PropertySchema {
	return oam.PropertySchema{
		Type: oam.PropertyTypeArray,
		Items: &oam.PropertySchema{
			Type: oam.PropertyTypeObject,
			Properties: map[string]oam.PropertySchema{
				"name":      {Type: oam.PropertyTypeString, Required: true},
				"value":     {Type: oam.PropertyTypeString},
				"valueFrom": {Type: oam.PropertyTypeObject, AdditionalProperties: true},
			},
		},
	}
}

// schemaResources describes the shared `resources` property (see parseResources).
func schemaResources() oam.PropertySchema {
	quantity := map[string]oam.PropertySchema{
		"cpu":    {Type: oam.PropertyTypeString},
		"memory": {Type: oam.PropertyTypeString},
	}
	return oam.PropertySchema{
		Type: oam.PropertyTypeObject,
		Properties: map[string]oam.PropertySchema{
			"requests": {Type: oam.PropertyTypeObject, Properties: quantity},
			"limits":   {Type: oam.PropertyTypeObject, Properties: quantity},
		},
	}
}

// schemaStringArray describes an array-of-strings property (command/args).
func schemaStringArray() oam.PropertySchema {
	return oam.PropertySchema{
		Type:  oam.PropertyTypeArray,
		Items: &oam.PropertySchema{Type: oam.PropertyTypeString},
	}
}

// schemaProbes describes the shared `probes` property (see parseProbes). Each
// probe carries an int-or-string port and many optional K8s fields, so the
// individual probe objects are kept open.
func schemaProbes() oam.PropertySchema {
	probe := oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true}
	return oam.PropertySchema{
		Type: oam.PropertyTypeObject,
		Properties: map[string]oam.PropertySchema{
			"readiness": probe,
			"liveness":  probe,
			"startup":   probe,
		},
	}
}

// schemaVolumes describes the shared `volumes` property (see parseVolumes). The
// type-specific keys (path/size/configMapName/…) vary by `type`, so items stay
// open beyond the common fields.
func schemaVolumes() oam.PropertySchema {
	return oam.PropertySchema{
		Type: oam.PropertyTypeArray,
		Items: &oam.PropertySchema{
			Type:                 oam.PropertyTypeObject,
			AdditionalProperties: true,
			Properties: map[string]oam.PropertySchema{
				"name":      {Type: oam.PropertyTypeString, Required: true},
				"type":      {Type: oam.PropertyTypeString, Enum: []any{"hostPath", "emptyDir", "pvc", "configMap", "secret"}},
				"mountPath": {Type: oam.PropertyTypeString, Required: true},
				"readOnly":  {Type: oam.PropertyTypeBoolean},
			},
		},
	}
}

// schemaContainers describes the shared `initContainers`/`sidecars` properties
// (see parseInitContainers/parseSidecars). Their nested env/resources/
// volumeMounts/ports shapes are kept open on the item object.
func schemaContainers() oam.PropertySchema {
	return oam.PropertySchema{
		Type: oam.PropertyTypeArray,
		Items: &oam.PropertySchema{
			Type:                 oam.PropertyTypeObject,
			AdditionalProperties: true,
			Properties: map[string]oam.PropertySchema{
				"name":  {Type: oam.PropertyTypeString, Required: true},
				"image": {Type: oam.PropertyTypeString, Required: true},
			},
		},
	}
}

// schemaAffinity describes the shared `affinity` property (see parseAffinity).
func schemaAffinity() oam.PropertySchema {
	return oam.PropertySchema{
		Type: oam.PropertyTypeObject,
		Properties: map[string]oam.PropertySchema{
			"enablePodAntiAffinity": {Type: oam.PropertyTypeBoolean},
			"topologyKey":           {Type: oam.PropertyTypeString, Default: "kubernetes.io/hostname"},
			"podAntiAffinityType":   {Type: oam.PropertyTypeString, Default: "preferred", Enum: []any{"preferred", "required"}},
			"nodeSelector":          {Type: oam.PropertyTypeObject, AdditionalProperties: true},
		},
	}
}

// schemaTolerations describes the shared `tolerations` property (see parseTolerations).
func schemaTolerations() oam.PropertySchema {
	return oam.PropertySchema{
		Type: oam.PropertyTypeArray,
		Items: &oam.PropertySchema{
			Type: oam.PropertyTypeObject,
			Properties: map[string]oam.PropertySchema{
				"key":      {Type: oam.PropertyTypeString},
				"operator": {Type: oam.PropertyTypeString, Enum: []any{"Exists", "Equal"}},
				"value":    {Type: oam.PropertyTypeString},
				"effect":   {Type: oam.PropertyTypeString, Enum: []any{"NoSchedule", "PreferNoSchedule", "NoExecute", ""}},
			},
		},
	}
}

// schemaVolumeClaimTemplates describes the shared `volumeClaimTemplates` property
// (see parseVolumeClaimTemplates).
func schemaVolumeClaimTemplates() oam.PropertySchema {
	return oam.PropertySchema{
		Type: oam.PropertyTypeArray,
		Items: &oam.PropertySchema{
			Type: oam.PropertyTypeObject,
			Properties: map[string]oam.PropertySchema{
				"name":         {Type: oam.PropertyTypeString, Required: true},
				"size":         {Type: oam.PropertyTypeString, Required: true},
				"mountPath":    {Type: oam.PropertyTypeString, Required: true},
				"storageClass": {Type: oam.PropertyTypeString},
				"accessModes":  {Type: oam.PropertyTypeArray, Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Enum: accessModesEnum()}},
			},
		},
	}
}
