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

// schemaEnv describes the shared `env` property (see parseEnv/parseEnvVarSource).
// `valueFrom` models its five mutually-exclusive sources; each selector object
// stays shallow (a handful of flat string fields — no further nesting needed).
//
// reserved is a required argument (D3), threaded through per schemaSecurityContext's
// precedent below: every built-in call site in this package passes false today.
func schemaEnv(reserved bool) oam.PropertySchema {
	keySelector := func(refDesc string) oam.PropertySchema {
		return oam.PropertySchema{
			Type:        oam.PropertyTypeObject,
			Description: refDesc,
			Properties: map[string]oam.PropertySchema{
				"name":     {Type: oam.PropertyTypeString, Required: true, Description: "Name of the referenced object."},
				"key":      {Type: oam.PropertyTypeString, Required: true, Description: "Key within the referenced object's data."},
				"optional": {Type: oam.PropertyTypeBoolean, Description: "Whether the referenced object or key may be absent."},
			},
		}
	}
	return oam.PropertySchema{
		Type:             oam.PropertyTypeArray,
		PlatformReserved: reserved,
		Description:      "Environment variables to set on the container.",
		Items: &oam.PropertySchema{
			Type:        oam.PropertyTypeObject,
			Description: "A single environment variable.",
			Properties: map[string]oam.PropertySchema{
				"name":  {Type: oam.PropertyTypeString, Required: true, Description: "Environment variable name."},
				"value": {Type: oam.PropertyTypeString, Description: "Literal value for the variable."},
				"valueFrom": {
					Type:        oam.PropertyTypeObject,
					Description: "Source the value from another object. Exactly one of the four fields below may be set.",
					Properties: map[string]oam.PropertySchema{
						"secretKeyRef":    keySelector("Select a key of a Secret in the component's namespace."),
						"configMapKeyRef": keySelector("Select a key of a ConfigMap in the component's namespace."),
						"fieldRef": {
							Type:        oam.PropertyTypeObject,
							Description: "Select a field of the pod (e.g. metadata.name, status.podIP).",
							Properties: map[string]oam.PropertySchema{
								"fieldPath":  {Type: oam.PropertyTypeString, Required: true, Description: "Path of the field to select (e.g. \"metadata.name\")."},
								"apiVersion": {Type: oam.PropertyTypeString, Description: "Schema version the fieldPath is written in terms of; defaults to \"v1\"."},
							},
						},
						"resourceFieldRef": {
							Type:        oam.PropertyTypeObject,
							Description: "Select a container resource request/limit (e.g. limits.cpu) as the value.",
							Properties: map[string]oam.PropertySchema{
								"resource":      {Type: oam.PropertyTypeString, Required: true, Description: `Resource to select (e.g. "limits.cpu", "requests.memory").`},
								"containerName": {Type: oam.PropertyTypeString, Description: "Container to select the resource from; defaults to this container."},
								"divisor":       {Type: oam.PropertyTypeString, Description: `Output format divisor (e.g. "1", "1Mi"); defaults to "1".`},
							},
						},
						"fileKeyRef": {
							Type:        oam.PropertyTypeObject,
							Description: "Select a key of an env file mounted via a volume (corev1 EnvFiles feature; requires the cluster's EnvFiles feature gate).",
							Properties: map[string]oam.PropertySchema{
								"volumeName": {Type: oam.PropertyTypeString, Required: true, Description: "Name of the volume mount containing the env file."},
								"path":       {Type: oam.PropertyTypeString, Required: true, Description: "Path within the volume to the env file."},
								"key":        {Type: oam.PropertyTypeString, Required: true, Description: "Key within the env file."},
								"optional":   {Type: oam.PropertyTypeBoolean, Description: "Whether the file or key may be absent."},
							},
						},
					},
				},
			},
		},
	}
}

// schemaEnvFrom describes the shared `envFrom` property (see parseEnvFrom):
// bulk-import a ConfigMap's or Secret's keys as environment variables.
//
// reserved is a required argument (D3); see schemaEnv's doc comment above.
func schemaEnvFrom(reserved bool) oam.PropertySchema {
	return oam.PropertySchema{
		Type:             oam.PropertyTypeArray,
		PlatformReserved: reserved,
		Description:      "Bulk-import a ConfigMap's or Secret's keys as environment variables.",
		Items: &oam.PropertySchema{
			Type:        oam.PropertyTypeObject,
			Description: "A single envFrom source. Exactly one of configMapRef or secretRef must be set.",
			Properties: map[string]oam.PropertySchema{
				"prefix": {Type: oam.PropertyTypeString, Description: "Text prepended to each imported variable's name."},
				"configMapRef": {
					Type:        oam.PropertyTypeObject,
					Description: "Import every key of this ConfigMap.",
					Properties: map[string]oam.PropertySchema{
						"name":     {Type: oam.PropertyTypeString, Required: true, Description: "ConfigMap name."},
						"optional": {Type: oam.PropertyTypeBoolean, Description: "Whether the ConfigMap may be absent."},
					},
				},
				"secretRef": {
					Type:        oam.PropertyTypeObject,
					Description: "Import every key of this Secret.",
					Properties: map[string]oam.PropertySchema{
						"name":     {Type: oam.PropertyTypeString, Required: true, Description: "Secret name."},
						"optional": {Type: oam.PropertyTypeBoolean, Description: "Whether the Secret may be absent."},
					},
				},
			},
		},
	}
}

// schemaResources describes the shared `resources` property (see parseResources).
// requests/limits accept arbitrary named resources beyond cpu/memory (e.g.
// ephemeral-storage, nvidia.com/gpu) via AdditionalProperties — parseResources
// collects any such key into ResourceRequirements.Extra{Requests,Limits}.
// reserved is a required argument (D3); see schemaEnv's doc comment above.
func schemaResources(reserved bool) oam.PropertySchema {
	// requests and limits each get their own map so the returned schema shares no
	// sub-map state (honoring the file-level freshness contract above).
	quantity := func() map[string]oam.PropertySchema {
		return map[string]oam.PropertySchema{
			"cpu":    {Type: oam.PropertyTypeString, Description: `CPU quantity (e.g. "500m" or "1").`},
			"memory": {Type: oam.PropertyTypeString, Description: `Memory quantity (e.g. "512Mi" or "1Gi").`},
		}
	}
	return oam.PropertySchema{
		Type:             oam.PropertyTypeObject,
		PlatformReserved: reserved,
		Description:      "Compute resource requests and limits for the container.",
		Properties: map[string]oam.PropertySchema{
			"requests": {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Minimum resources guaranteed to the container. Additional named resources (e.g. ephemeral-storage, nvidia.com/gpu) beyond cpu/memory are also accepted.", Properties: quantity()},
			"limits":   {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Maximum resources the container may use. Additional named resources (e.g. ephemeral-storage, nvidia.com/gpu) beyond cpu/memory are also accepted.", Properties: quantity()},
		},
	}
}

// schemaLifecycle describes the shared `lifecycle` property (see parseLifecycle).
// postStart/preStop are kept open (AdditionalProperties), matching schemaProbes'
// precedent just below: each handler carries an int-or-string port and several
// optional K8s fields, so the constrained vocabulary describes the user-facing
// surface (postStart/preStop exist, are objects) without modeling every nested
// handler field.
// reserved is a required argument (D3); see schemaEnv's doc comment above.
func schemaLifecycle(reserved bool) oam.PropertySchema {
	handler := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: desc}
	}
	return oam.PropertySchema{
		Type:             oam.PropertyTypeObject,
		PlatformReserved: reserved,
		Description:      "Lifecycle hooks run by the kubelet around container start/stop (exec, httpGet, or sleep).",
		Properties: map[string]oam.PropertySchema{
			"postStart": handler("Executed immediately after the container is created."),
			"preStop":   handler("Executed immediately before the container is terminated."),
		},
	}
}

// schemaSecurityContext describes the shared `securityContext` property (see
// parseSecurityContext).
//
// reserved is a required argument (D3), never defaulted here, mirroring
// pkg/oam/builtin/traits/schema.go's schemaNetworkPolicy(reserved bool): every
// built-in call site in this package passes false today (nothing here decides
// that securityContext should be platform-managed — that decision, if ever
// made, belongs to a consumer-side call site, not this shared fragment; see
// R5 in the launcher#278 ledger). The parameter exists so a future call site
// CAN say otherwise without this fragment silently drifting underneath it.
func schemaSecurityContext(reserved bool) oam.PropertySchema {
	return oam.PropertySchema{
		Type:             oam.PropertyTypeObject,
		PlatformReserved: reserved,
		Description:      "Container-level security context (runAsUser, capabilities, seccompProfile, …). Setting any field here opts this container out of a downstream platform's nil-only default backfill for ALL securityContext fields — see parseSecurityContext's doc comment.",
		Properties: map[string]oam.PropertySchema{
			"runAsUser":                {Type: oam.PropertyTypeInteger, Description: "UID to run the container process as."},
			"runAsGroup":               {Type: oam.PropertyTypeInteger, Description: "GID to run the container process as."},
			"runAsNonRoot":             {Type: oam.PropertyTypeBoolean, Description: "Whether the container must run as a non-root user."},
			"readOnlyRootFilesystem":   {Type: oam.PropertyTypeBoolean, Description: "Whether the container's root filesystem is mounted read-only."},
			"allowPrivilegeEscalation": {Type: oam.PropertyTypeBoolean, Description: "Whether a process can gain more privileges than its parent."},
			"privileged":               {Type: oam.PropertyTypeBoolean, Description: "Run the container in privileged mode. Subject to the environment policy's AllowPrivileged flag."},
			"capabilities": {
				Type:        oam.PropertyTypeObject,
				Description: "Linux capabilities to add or drop.",
				Properties: map[string]oam.PropertySchema{
					"add":  {Type: oam.PropertyTypeArray, Description: "Capabilities to add (e.g. \"NET_BIND_SERVICE\").", Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A single capability name."}},
					"drop": {Type: oam.PropertyTypeArray, Description: "Capabilities to drop (e.g. \"ALL\").", Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A single capability name."}},
				},
			},
			"seccompProfile": {
				Type:        oam.PropertyTypeObject,
				Description: "Seccomp options for the container.",
				Properties: map[string]oam.PropertySchema{
					"type":             {Type: oam.PropertyTypeString, Enum: []any{"Localhost", "RuntimeDefault", "Unconfined"}, Description: "Which kind of seccomp profile to apply."},
					"localhostProfile": {Type: oam.PropertyTypeString, Description: "Profile file path relative to the kubelet's seccomp root; required (and only valid) when type is \"Localhost\"."},
				},
			},
			"seLinuxOptions": {
				Type:        oam.PropertyTypeObject,
				Description: "SELinux context to apply to the container.",
				Properties: map[string]oam.PropertySchema{
					"user":  {Type: oam.PropertyTypeString, Description: "SELinux user label."},
					"role":  {Type: oam.PropertyTypeString, Description: "SELinux role label."},
					"type":  {Type: oam.PropertyTypeString, Description: "SELinux type label."},
					"level": {Type: oam.PropertyTypeString, Description: "SELinux level label."},
				},
			},
			"appArmorProfile": {
				Type:        oam.PropertyTypeObject,
				Description: "AppArmor options for the container.",
				Properties: map[string]oam.PropertySchema{
					"type":             {Type: oam.PropertyTypeString, Enum: []any{"Localhost", "RuntimeDefault", "Unconfined"}, Description: "Which kind of AppArmor profile to apply."},
					"localhostProfile": {Type: oam.PropertyTypeString, Description: "Profile loaded on the node; required (and only valid) when type is \"Localhost\"."},
				},
			},
			"procMount": {Type: oam.PropertyTypeString, Enum: []any{"Default", "Unmasked"}, Description: "Procfs mount behavior for the container (Linux-only)."},
		},
	}
}

// schemaWorkingDir describes the shared `workingDir` property (see the
// `workingDir` handling in each kind's ToApplicationConfig).
//
// reserved is a required argument (D3); see schemaEnv's doc comment above.
func schemaWorkingDir(reserved bool) oam.PropertySchema {
	return oam.PropertySchema{
		Type:             oam.PropertyTypeString,
		PlatformReserved: reserved,
		Description:      "Working directory for the container process.",
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
//
// reserved is a required argument (D3); see schemaEnv's doc comment above.
func schemaProbes(reserved bool) oam.PropertySchema {
	probe := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: desc}
	}
	return oam.PropertySchema{
		Type:             oam.PropertyTypeObject,
		PlatformReserved: reserved,
		Description:      "Health probes for the container.",
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
