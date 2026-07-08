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
		Type:        oam.PropertyTypeArray,
		Description: "Environment variables to set on the container.",
		Items: &oam.PropertySchema{
			Type:        oam.PropertyTypeObject,
			Description: "A single environment variable.",
			Properties: map[string]oam.PropertySchema{
				"name":      {Type: oam.PropertyTypeString, Required: true, Description: "Environment variable name."},
				"value":     {Type: oam.PropertyTypeString, Description: "Literal value for the variable."},
				"valueFrom": {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Source the value from another object (e.g. secretKeyRef or configMapKeyRef)."},
			},
		},
	}
}

// schemaResources describes the shared `resources` property (see parseResources).
func schemaResources() oam.PropertySchema {
	// requests and limits each get their own map so the returned schema shares no
	// sub-map state (honoring the file-level freshness contract above).
	quantity := func() map[string]oam.PropertySchema {
		return map[string]oam.PropertySchema{
			"cpu":    {Type: oam.PropertyTypeString, Description: `CPU quantity (e.g. "500m" or "1").`},
			"memory": {Type: oam.PropertyTypeString, Description: `Memory quantity (e.g. "512Mi" or "1Gi").`},
		}
	}
	return oam.PropertySchema{
		Type:        oam.PropertyTypeObject,
		Description: "Compute resource requests and limits for the container.",
		Properties: map[string]oam.PropertySchema{
			"requests": {Type: oam.PropertyTypeObject, Description: "Minimum resources guaranteed to the container.", Properties: quantity()},
			"limits":   {Type: oam.PropertyTypeObject, Description: "Maximum resources the container may use.", Properties: quantity()},
		},
	}
}

// schemaStringArray describes an array-of-strings property (command/args).
func schemaStringArray() oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeArray,
		Description: "A list of string values (e.g. command or args).",
		Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A single string value."},
	}
}

// schemaProbes describes the shared `probes` property (see parseProbes). Each
// probe carries an int-or-string port and many optional K8s fields, so the
// individual probe objects are kept open.
func schemaProbes() oam.PropertySchema {
	probe := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: desc}
	}
	return oam.PropertySchema{
		Type:        oam.PropertyTypeObject,
		Description: "Health probes for the container.",
		Properties: map[string]oam.PropertySchema{
			"readiness": probe("Readiness probe determining when the container can receive traffic."),
			"liveness":  probe("Liveness probe determining when the container should be restarted."),
			"startup":   probe("Startup probe determining when the container has finished starting."),
		},
	}
}

// schemaVolumes describes the shared `volumes` property (see parseVolumes). The
// type-specific keys (path/size/configMapName/…) vary by `type`, so items stay
// open beyond the common fields.
func schemaVolumes() oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeArray,
		Description: "Volumes to attach and mount into the container.",
		Items: &oam.PropertySchema{
			Type:                 oam.PropertyTypeObject,
			AdditionalProperties: true,
			Description:          "A single volume and its mount.",
			Properties: map[string]oam.PropertySchema{
				"name":      {Type: oam.PropertyTypeString, Required: true, Description: "Volume name."},
				"type":      {Type: oam.PropertyTypeString, Enum: []any{"hostPath", "emptyDir", "pvc", "configMap", "secret"}, Description: "Volume source type."},
				"mountPath": {Type: oam.PropertyTypeString, Required: true, Description: "Path where the volume is mounted in the container."},
				"readOnly":  {Type: oam.PropertyTypeBoolean, Description: "Mount the volume read-only."},
			},
		},
	}
}

// schemaContainers describes the shared `initContainers`/`sidecars` properties
// (see parseInitContainers/parseSidecars). Their nested env/resources/
// volumeMounts/ports shapes are kept open on the item object.
func schemaContainers() oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeArray,
		Description: "Additional containers to run in the pod (init containers or sidecars).",
		Items: &oam.PropertySchema{
			Type:                 oam.PropertyTypeObject,
			AdditionalProperties: true,
			Description:          "A single container definition.",
			Properties: map[string]oam.PropertySchema{
				"name":  {Type: oam.PropertyTypeString, Required: true, Description: "Container name."},
				"image": {Type: oam.PropertyTypeString, Required: true, Description: "Container image reference."},
			},
		},
	}
}

// schemaAffinity describes the shared `affinity` property (see parseAffinity).
func schemaAffinity() oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeObject,
		Description: "Pod affinity and anti-affinity scheduling rules.",
		Properties: map[string]oam.PropertySchema{
			"enablePodAntiAffinity": {Type: oam.PropertyTypeBoolean, Description: "Spread pods across nodes using pod anti-affinity."},
			"topologyKey":           {Type: oam.PropertyTypeString, Default: "kubernetes.io/hostname", Description: "Node topology key the anti-affinity rule is evaluated against."},
			"podAntiAffinityType":   {Type: oam.PropertyTypeString, Default: "preferred", Enum: []any{"preferred", "required"}, Description: "Whether anti-affinity is a soft preference or a hard requirement."},
			"nodeSelector":          {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Node labels the pod must match to be scheduled."},
		},
	}
}

// schemaTolerations describes the shared `tolerations` property (see parseTolerations).
func schemaTolerations() oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeArray,
		Description: "Node taint tolerations allowing the pod to schedule onto tainted nodes.",
		Items: &oam.PropertySchema{
			Type:        oam.PropertyTypeObject,
			Description: "A single taint toleration.",
			Properties: map[string]oam.PropertySchema{
				"key":      {Type: oam.PropertyTypeString, Description: "Taint key to tolerate."},
				"operator": {Type: oam.PropertyTypeString, Enum: []any{"Exists", "Equal"}, Description: "How the taint key/value are matched."},
				"value":    {Type: oam.PropertyTypeString, Description: "Taint value to match when operator is Equal."},
				"effect":   {Type: oam.PropertyTypeString, Enum: []any{"NoSchedule", "PreferNoSchedule", "NoExecute", ""}, Description: "Taint effect to tolerate (empty matches all effects)."},
			},
		},
	}
}

// schemaVolumeClaimTemplates describes the shared `volumeClaimTemplates` property
// (see parseVolumeClaimTemplates).
func schemaVolumeClaimTemplates() oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeArray,
		Description: "PersistentVolumeClaim templates provisioned per replica.",
		Items: &oam.PropertySchema{
			Type:        oam.PropertyTypeObject,
			Description: "A single volume claim template.",
			Properties: map[string]oam.PropertySchema{
				"name":         {Type: oam.PropertyTypeString, Required: true, Description: "Claim name (also used as the mount name)."},
				"size":         {Type: oam.PropertyTypeString, Required: true, Description: `Requested storage size (e.g. "10Gi").`},
				"mountPath":    {Type: oam.PropertyTypeString, Required: true, Description: "Path where the claim is mounted in the container."},
				"storageClass": {Type: oam.PropertyTypeString, Description: "StorageClass used to provision the volume."},
				"accessModes":  {Type: oam.PropertyTypeArray, Description: "Requested access modes for the volume.", Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Enum: accessModesEnum(), Description: "A single access mode."}},
			},
		},
	}
}
