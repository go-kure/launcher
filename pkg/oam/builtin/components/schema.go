package components

import "github.com/go-kure/launcher/pkg/oam"

// This file holds PropertySchema fragments shared by the container-workload
// component handlers (webservice/worker/cronjob/daemonset/statefulset). Each
// fragment mirrors a shared parser in common.go / podspec.go (parseEnv,
// parseResources, parsePodSpec, …). The target is full corev1 fidelity: a
// nested shape is modeled field-by-field, with a Description on every key,
// wherever its parser is strict (env, envFrom, resources, securityContext, the
// pod-level fragment). The objects still declared open (AdditionalProperties —
// probes, lifecycle handlers, volume type-specific keys, initContainers/sidecars
// item extras) are a known gap awaiting their own strict parsers, not a design
// choice to stay shallow. Each accessor returns a fresh value so consumers can't
// mutate shared state.

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
					Description: "Source the value from another object. Exactly one of the five fields below may be set.",
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
// collects any such key directly into the real corev1.ResourceList map on
// ResourceRequirements.Requests/Limits (no separate "extra" bucket).
//
// `claims` (corev1.ResourceRequirements.Claims, Dynamic Resource Allocation) is
// deliberately not projected: the pinned k8s.io/api@v0.36.3 type carries
// `+featureGate=DynamicResourceAllocation` on that field (unlike procMount, whose
// alpha rationale proved false — see parseSecurityContext), and a Claims entry
// only means anything paired with a PodSpec.ResourceClaims entry. The pod-level
// `resourceClaims` property now exists (schemaPodSpec), so the remaining work is
// the container side: a `claims` entry must name a declared pod-level claim, a
// cross-field check parseResources has no access to today. Left for a
// follow-up rather than validating a shape that can silently produce an
// invalid corev1 object.
// reserved is a required argument (D3); see schemaEnv's doc comment above.
func schemaResources(reserved bool) oam.PropertySchema {
	// requests and limits each get their own map so the returned schema shares no
	// sub-map state (honoring the file-level freshness contract above).
	// cpu/memory are deliberately left with no declared Type. parseResourceList
	// accepts either a quantity string ("500m") or a bare YAML/JSON number
	// (0.5) — see its doc comment — but PropertySchema has no string-or-number
	// union type, and a declared Type: PropertyTypeString would make
	// validatePropertyValue reject the numeric form the parser and README both
	// promise. Leaving Type unset matches how any other, non-cpu/memory
	// resource name already validates today: AdditionalProperties skips a
	// per-key type check for those entirely.
	quantity := func() map[string]oam.PropertySchema {
		return map[string]oam.PropertySchema{
			"cpu":    {Description: `CPU quantity as a string or bare number of cores (e.g. "500m", "1", or 0.5).`},
			"memory": {Description: `Memory quantity as a string (e.g. "512Mi", "1Gi") or a bare number of bytes.`},
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
			"seccompProfile":  schemaSeccompProfile("the container"),
			"seLinuxOptions":  schemaSELinuxOptions("the container"),
			"appArmorProfile": schemaAppArmorProfile("the container"),
			"procMount":       {Type: oam.PropertyTypeString, Enum: []any{"Default", "Unmasked"}, Description: "Procfs mount behavior for the container (Linux-only)."},
		},
	}
}

// schemaSeccompProfile / schemaSELinuxOptions / schemaAppArmorProfile describe
// the three security sub-objects shared verbatim by the container-level
// securityContext and the pod-level podSecurityContext (see parseSeccompProfile
// and friends in common.go). scope is the noun the description applies to
// ("the container" / "all containers in the pod").
func schemaSeccompProfile(scope string) oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeObject,
		Description: "Seccomp options for " + scope + ".",
		Properties: map[string]oam.PropertySchema{
			"type":             {Type: oam.PropertyTypeString, Enum: []any{"Localhost", "RuntimeDefault", "Unconfined"}, Description: "Which kind of seccomp profile to apply."},
			"localhostProfile": {Type: oam.PropertyTypeString, Description: "Profile file path relative to the kubelet's seccomp root; required (and only valid) when type is \"Localhost\"."},
		},
	}
}

func schemaSELinuxOptions(scope string) oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeObject,
		Description: "SELinux context to apply to " + scope + ".",
		Properties: map[string]oam.PropertySchema{
			"user":  {Type: oam.PropertyTypeString, Description: "SELinux user label."},
			"role":  {Type: oam.PropertyTypeString, Description: "SELinux role label."},
			"type":  {Type: oam.PropertyTypeString, Description: "SELinux type label."},
			"level": {Type: oam.PropertyTypeString, Description: "SELinux level label."},
		},
	}
}

func schemaAppArmorProfile(scope string) oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeObject,
		Description: "AppArmor options for " + scope + ".",
		Properties: map[string]oam.PropertySchema{
			"type":             {Type: oam.PropertyTypeString, Enum: []any{"Localhost", "RuntimeDefault", "Unconfined"}, Description: "Which kind of AppArmor profile to apply."},
			"localhostProfile": {Type: oam.PropertyTypeString, Description: "Profile loaded on the node; required (and only valid) when type is \"Localhost\"."},
		},
	}
}

// schemaPodSpec describes the pod-level properties shared by every container
// workload kind (see parsePodSpec in podspec.go). A map, like schemaJobSpec,
// because all five kinds must expose the identical set — TestPodSpecSchema
// pins its key set to podSpecPropertyKeys. Every key, nested or not, carries a
// Description; enum-valued fields declare their Enum so schema validation
// rejects a bad value before the parser does.
//
// Fields corev1.PodSpec has that are deliberately absent here, and why
// parsePodSpec rejects them if authored: `ephemeralContainers` (only settable
// through a running pod's ephemeralcontainers subresource), `priority` and
// `overhead` (populated by the Priority / RuntimeClass admission controllers
// from priorityClassName / runtimeClassName; authoring them is rejected by
// admission), `serviceAccount` (deprecated alias of serviceAccountName).
//
// jobPods selects whether podSpecJobOnlyKeys (podActiveDeadlineSeconds) are
// published: apps/v1 forbids activeDeadlineSeconds on Deployment, StatefulSet
// and DaemonSet pod templates, so only cronjob passes true.
//
// reserved is a required argument (D3); see schemaEnv's doc comment above.
func schemaPodSpec(reserved, jobPods bool) map[string]oam.PropertySchema {
	str := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeString, PlatformReserved: reserved, Description: desc}
	}
	boolean := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeBoolean, PlatformReserved: reserved, Description: desc}
	}
	integer := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeInteger, PlatformReserved: reserved, Description: desc}
	}
	nameList := func(desc, itemDesc string) oam.PropertySchema {
		return oam.PropertySchema{
			Type: oam.PropertyTypeArray, PlatformReserved: reserved, Description: desc,
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, Description: itemDesc,
				Properties: map[string]oam.PropertySchema{
					"name": {Type: oam.PropertyTypeString, Required: true, Description: "Name of the referenced object."},
				},
			},
		}
	}
	podResources := schemaResources(reserved)
	// The requests/limits maps stay open (their keys are resource names, e.g.
	// hugepages-2Mi), but the container-level descriptions advertise
	// ephemeral-storage and extended resources, which parsePodSpec rejects at
	// pod level — override them so the schema promises only what parses.
	podResources.Properties["requests"] = oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true,
		Description: "Minimum pod-level resources guaranteed to the pod as a whole. Only cpu, memory and hugepages-<size> keys are accepted; ephemeral-storage and extended resources are rejected.",
		Properties:  podResources.Properties["requests"].Properties}
	podResources.Properties["limits"] = oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true,
		Description: "Maximum pod-level resources the pod as a whole may use. Only cpu, memory and hugepages-<size> keys are accepted; ephemeral-storage and extended resources are rejected.",
		Properties:  podResources.Properties["limits"].Properties}
	podResources.Description = "Pod-level compute resources shared by all containers (corev1.PodSpec.resources). Only cpu, memory and hugepages-<size> are valid here; requires the cluster's PodLevelResources feature gate. Distinct from the container-level `resources` property."

	m := map[string]oam.PropertySchema{
		"terminationGracePeriodSeconds": integer("Seconds the pod is given to terminate gracefully after a delete request. Must be >= 0; the API default is 30."),
		"dnsPolicy": {Type: oam.PropertyTypeString, PlatformReserved: reserved, Enum: []any{"ClusterFirstWithHostNet", "ClusterFirst", "Default", "None"},
			Description: "DNS policy for the pod. \"None\" requires dnsConfig.nameservers; use \"ClusterFirstWithHostNet\" together with hostNetwork to keep cluster DNS."},
		"nodeSelector":                 {Type: oam.PropertyTypeObject, PlatformReserved: reserved, AdditionalProperties: true, Description: "Node labels the pod must match to be scheduled (label key to label value)."},
		"serviceAccountName":           str("Name of an existing ServiceAccount the pod runs as. When set, the component's own per-component ServiceAccount is NOT generated, and identity-binding traits (rbac) target this account instead."),
		"automountServiceAccountToken": boolean("Whether the ServiceAccount token is mounted into the pod's containers."),
		"nodeName":                     str("Bind the pod to this specific node, bypassing the scheduler."),
		"hostNetwork":                  boolean("Use the node's network namespace. Subject to the environment policy's AllowHostNetwork flag."),
		"hostPID":                      boolean("Use the node's PID namespace. Subject to the environment policy's AllowHostPID flag; mutually exclusive with shareProcessNamespace."),
		"hostIPC":                      boolean("Use the node's IPC namespace. Subject to the environment policy's AllowHostIPC flag."),
		"shareProcessNamespace":        boolean("Share a single process namespace between all containers in the pod. Mutually exclusive with hostPID."),
		"podSecurityContext": {
			Type: oam.PropertyTypeObject, PlatformReserved: reserved,
			Description: "Pod-level security context (corev1.PodSecurityContext) applying to all containers. Distinct from the container-level `securityContext` property, which takes precedence for its own fields.",
			Properties: map[string]oam.PropertySchema{
				"seLinuxOptions": schemaSELinuxOptions("all containers in the pod"),
				"windowsOptions": {
					Type: oam.PropertyTypeObject, Description: "Windows-specific options applied to all containers (ignored on Linux).",
					Properties: map[string]oam.PropertySchema{
						"gmsaCredentialSpecName": {Type: oam.PropertyTypeString, Description: "Name of the GMSA credential spec to use."},
						"gmsaCredentialSpec":     {Type: oam.PropertyTypeString, Description: "Inline GMSA credential spec contents (normally populated by the GMSA admission webhook)."},
						"runAsUserName":          {Type: oam.PropertyTypeString, Description: "Windows user name to run the container entrypoint as."},
						"hostProcess":            {Type: oam.PropertyTypeBoolean, Description: "Run the containers as Windows HostProcess containers."},
					},
				},
				"runAsUser":                {Type: oam.PropertyTypeInteger, Description: "UID to run each container's entrypoint as; a container-level runAsUser takes precedence."},
				"runAsGroup":               {Type: oam.PropertyTypeInteger, Description: "Primary GID for each container's entrypoint; a container-level runAsGroup takes precedence."},
				"runAsNonRoot":             {Type: oam.PropertyTypeBoolean, Description: "Whether the containers must run as a non-root user."},
				"supplementalGroups":       {Type: oam.PropertyTypeArray, Description: "Additional GIDs applied to the first process in each container.", Items: &oam.PropertySchema{Type: oam.PropertyTypeInteger, Description: "A single supplemental group ID."}},
				"supplementalGroupsPolicy": {Type: oam.PropertyTypeString, Enum: []any{"Merge", "Strict"}, Description: "How supplementalGroups combine with the container image's own group memberships."},
				"fsGroup":                  {Type: oam.PropertyTypeInteger, Description: "Supplemental group that owns volumes supporting ownership management."},
				"sysctls": {
					Type: oam.PropertyTypeArray, Description: "Namespaced kernel parameters to set for the pod.",
					Items: &oam.PropertySchema{
						Type: oam.PropertyTypeObject, Description: "A single sysctl.",
						Properties: map[string]oam.PropertySchema{
							"name":  {Type: oam.PropertyTypeString, Required: true, Description: "Sysctl name (e.g. \"net.core.somaxconn\")."},
							"value": {Type: oam.PropertyTypeString, Required: true, Description: "Sysctl value."},
						},
					},
				},
				"fsGroupChangePolicy": {Type: oam.PropertyTypeString, Enum: []any{"OnRootMismatch", "Always"}, Description: "When to change ownership/permissions of a volume to match fsGroup."},
				"seccompProfile":      schemaSeccompProfile("all containers in the pod"),
				"appArmorProfile":     schemaAppArmorProfile("all containers in the pod"),
				"seLinuxChangePolicy": {Type: oam.PropertyTypeString, Enum: []any{"MountOption", "Recursive"}, Description: "How the SELinux label is applied to the pod's volumes."},
			},
		},
		"imagePullSecrets": nameList("Secrets in the component's namespace holding registry credentials for pulling the pod's images.", "A reference to one image-pull Secret."),
		"hostname":         str("Hostname of the pod (a DNS-1123 label). Defaults to the pod's own name."),
		"subdomain":        str("Subdomain of the pod (a DNS-1123 label), giving the FQDN <hostname>.<subdomain>.<namespace>.svc.<cluster domain>."),
		"schedulerName":    str("Name of the scheduler that dispatches this pod; defaults to the default scheduler."),
		"hostAliases": {
			Type: oam.PropertyTypeArray, PlatformReserved: reserved, Description: "Extra /etc/hosts entries injected into the pod.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, Description: "One IP with the hostnames that resolve to it.",
				Properties: map[string]oam.PropertySchema{
					"ip":        {Type: oam.PropertyTypeString, Required: true, Description: "IP address of the host file entry."},
					"hostnames": {Type: oam.PropertyTypeArray, Required: true, Description: "Hostnames resolving to the IP; at least one.", Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A single hostname (DNS-1123 subdomain)."}},
				},
			},
		},
		"priorityClassName": str("Name of the PriorityClass the pod is scheduled with. The pod's numeric priority is derived from it by admission — `priority` itself is not authorable."),
		"dnsConfig": {
			Type: oam.PropertyTypeObject, PlatformReserved: reserved, Description: "DNS parameters merged into the pod's resolver configuration according to dnsPolicy.",
			Properties: map[string]oam.PropertySchema{
				"nameservers": {Type: oam.PropertyTypeArray, Description: "DNS server IP addresses (at most 3).", Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A single nameserver IP."}},
				"searches":    {Type: oam.PropertyTypeArray, Description: "DNS search domains (at most 32).", Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A single search domain."}},
				"options": {
					Type: oam.PropertyTypeArray, Description: "Resolver options.",
					Items: &oam.PropertySchema{
						Type: oam.PropertyTypeObject, Description: "A single resolver option.",
						Properties: map[string]oam.PropertySchema{
							"name":  {Type: oam.PropertyTypeString, Required: true, Description: "Option name (e.g. \"ndots\")."},
							"value": {Type: oam.PropertyTypeString, Description: "Option value; omit for flag-style options."},
						},
					},
				},
			},
		},
		"readinessGates": {
			Type: oam.PropertyTypeArray, PlatformReserved: reserved, Description: "Extra pod conditions that must be True before the pod counts as ready.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, Description: "A single readiness gate.",
				Properties: map[string]oam.PropertySchema{
					"conditionType": {Type: oam.PropertyTypeString, Required: true, Description: "Pod condition type (a qualified name) that must be True."},
				},
			},
		},
		"runtimeClassName":   str("Name of the RuntimeClass the pod runs under. Pod overhead is derived from it by admission — `overhead` itself is not authorable."),
		"enableServiceLinks": boolean("Whether information about services is injected into the pod's environment as Docker-style links. The API default is true."),
		"preemptionPolicy":   {Type: oam.PropertyTypeString, PlatformReserved: reserved, Enum: []any{"PreemptLowerPriority", "Never"}, Description: "Whether the pod may preempt lower-priority pods. The API default is PreemptLowerPriority."},
		"setHostnameAsFQDN":  boolean("Set the pod's hostname to its fully qualified domain name. Mutually exclusive with hostnameOverride."),
		"os": {
			Type: oam.PropertyTypeObject, PlatformReserved: reserved, Description: "Operating system of the pod's containers.",
			Properties: map[string]oam.PropertySchema{
				"name": {Type: oam.PropertyTypeString, Required: true, Enum: []any{"linux", "windows"}, Description: "OS name."},
			},
		},
		"hostUsers":       boolean("Use the host's user namespace (true, the default) or a new user namespace for the pod (false)."),
		"schedulingGates": nameList("Named gates that must all be removed before the scheduler considers the pod (gate names must be unique).", "A single scheduling gate."),
		"resourceClaims": {
			Type: oam.PropertyTypeArray, PlatformReserved: reserved, Description: "Dynamic Resource Allocation claims made available to the pod's containers (names must be unique).",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, Description: "A single pod resource claim; exactly one of resourceClaimName or resourceClaimTemplateName must be set.",
				Properties: map[string]oam.PropertySchema{
					"name":                      {Type: oam.PropertyTypeString, Required: true, Description: "Claim name containers refer to (a DNS-1123 label)."},
					"resourceClaimName":         {Type: oam.PropertyTypeString, Description: "Name of an existing ResourceClaim in the pod's namespace."},
					"resourceClaimTemplateName": {Type: oam.PropertyTypeString, Description: "Name of a ResourceClaimTemplate a claim is created from per pod."},
				},
			},
		},
		"podResources":     podResources,
		"hostnameOverride": str("Fully overrides the pod's hostname (a DNS-1123 subdomain of at most 64 characters). Mutually exclusive with hostNetwork and setHostnameAsFQDN; requires the cluster's HostnameOverride feature gate."),
		"schedulingGroup": {
			Type: oam.PropertyTypeObject, PlatformReserved: reserved, Description: "Gang-scheduling group the pod belongs to (requires the cluster's scheduling-group support).",
			Properties: map[string]oam.PropertySchema{
				"podGroupName": {Type: oam.PropertyTypeString, Required: true, Description: "Name of the PodGroup this pod is scheduled together with."},
			},
		},
	}
	if jobPods {
		m["podActiveDeadlineSeconds"] = integer("Seconds the pod itself may be active before the system terminates it (corev1.PodSpec.activeDeadlineSeconds; distinct from the job-level activeDeadlineSeconds). Must be between 1 and 2147483647. Only Job pods may set it.")
	}
	return m
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
				"name":            {Type: oam.PropertyTypeString, Required: true, Description: "Container name."},
				"image":           {Type: oam.PropertyTypeString, Required: true, Description: "Container image reference."},
				"securityContext": schemaSecurityContext(false),
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

// schemaJobSpec describes the batchv1.JobSpec property fragments shared by the
// cronjob handler's jobTemplate today and (later) the job component (#279 PR 6) —
// see parseJobSpec/applyJobSpec in common.go. A map, not per-field accessors (unlike
// this file's usual style), precisely because job and cronjob must expose an
// identical set — per-field accessors would let them drift.
//
// reserved is explicit per this package's D3 convention (see schemaEnv's doc
// comment above); every call site in this package passes false today.
func schemaJobSpec(reserved bool) map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"backoffLimit":            {Type: oam.PropertyTypeInteger, PlatformReserved: reserved, Description: "Number of retries before marking the job as failed."},
		"completions":             {Type: oam.PropertyTypeInteger, PlatformReserved: reserved, Description: "Desired number of successfully finished pods the job should be run with."},
		"parallelism":             {Type: oam.PropertyTypeInteger, PlatformReserved: reserved, Description: "Maximum number of pods the job should run concurrently. Must be <= 100000 when completionMode is \"Indexed\"."},
		"activeDeadlineSeconds":   {Type: oam.PropertyTypeInteger, PlatformReserved: reserved, Description: "Duration in seconds the job may be active before it is terminated. Must be a positive integer."},
		"ttlSecondsAfterFinished": {Type: oam.PropertyTypeInteger, PlatformReserved: reserved, Description: "Seconds after the job finishes before it (and its pods) is automatically deleted."},
		"completionMode":          {Type: oam.PropertyTypeString, PlatformReserved: reserved, Enum: []any{"NonIndexed", "Indexed"}, Description: "How pod completions are tracked. \"Indexed\" requires completions to also be set."},
	}
}

// schemaCronJobConcurrencyPolicy describes the CronJobSpec-level `concurrencyPolicy`
// property (see the cronjob handler's ToApplicationConfig/createCronJob).
// batchv1.CronJobSpec.ConcurrencyPolicy is `omitempty`, but "Allow" is the API's own
// default when the field is left unset entirely — the parser only calls
// SetCronJobConcurrencyPolicy when authored, so the Default documented here is
// descriptive of the API's behavior, not something the parser applies itself.
func schemaCronJobConcurrencyPolicy() oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeString,
		Default:     "Allow",
		Enum:        []any{"Allow", "Forbid", "Replace"},
		Description: "How to treat concurrent executions of this CronJob. Not written to output unless authored — the CronJob API's own default is \"Allow\".",
	}
}

// schemaCronJobSuspend describes the CronJobSpec-level `suspend` property.
func schemaCronJobSuspend() oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeBoolean,
		Description: "Suspend subsequent executions of this CronJob. Not written to output unless authored.",
	}
}

// schemaCronJobStartingDeadlineSeconds describes the CronJobSpec-level
// `startingDeadlineSeconds` property.
func schemaCronJobStartingDeadlineSeconds() oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeInteger,
		Description: "Deadline in seconds for starting a job if it misses its scheduled time, for any reason. Must be >= 0.",
	}
}

// schemaCronJobTimeZone describes the CronJobSpec-level `timeZone` property.
func schemaCronJobTimeZone() oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeString,
		Description: "IANA time zone name (e.g. \"Europe/Brussels\") the schedule is interpreted in. Must be a real zone name — an empty string and \"Local\" (case-insensitive) are rejected.",
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
