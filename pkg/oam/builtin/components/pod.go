package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// PodHandler handles OAM pod components: the kind-named projection of a v1 Pod
// (go-kure/launcher#790).
//
// Its properties are the top-level json fields of corev1.PodSpec, decoded
// strictly (decodeKindSpec), less the three no pod can be created with:
// ephemeralContainers, priority and overhead (validateAuthoredPodSpec). It
// emits the Pod, named after the component in the build namespace, with the
// authored spec and the `app` label every workload kind gives its pods, which
// launcher's traits and Services select on. Nothing else is added: no
// annotation, no ServiceAccount and no default of launcher's.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags, less those three.
type PodHandler struct{}

// CanHandle returns true for the pod component type.
func (h *PodHandler) CanHandle(componentType string) bool {
	return componentType == "pod"
}

// podObjectSchema is the schema of a structured PodSpec field: an open object
// whose content is checked by the strict decode, not by the schema.
func podObjectSchema(desc, apiType string) oam.PropertySchema {
	return oam.PropertySchema{
		Type: oam.PropertyTypeObject, AdditionalProperties: true,
		Description: desc + " Decoded strictly into the Kubernetes API type: see " + apiType + " in the Kubernetes API reference.",
	}
}

// podListSchema is the schema of a PodSpec field that lists structured items.
func podListSchema(desc, item, apiType string) oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeArray,
		Description: desc + " Decoded strictly into the Kubernetes API type: see " + apiType + " in the Kubernetes API reference.",
		Items:       &oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: item},
	}
}

// PropertySchema declares every authorable top-level corev1.PodSpec field by
// its json name. Structured fields are open objects whose content is checked
// by the strict decode, not by this schema.
func (h *PodHandler) PropertySchema() map[string]oam.PropertySchema {
	const container = "One container: name, image (with a tag other than latest, or a digest) and any other field of the API type."
	str := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeString, Description: desc}
	}
	boolean := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeBoolean, Description: desc}
	}
	integer := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeInteger, Description: desc}
	}
	containers := podListSchema("Required. Pod spec.containers: the containers of the pod. Each image is held to the image rule and, under an environment policy, to its registry allowlist, resource maxima and privilege gates.", container, "Container")
	containers.Required = true
	return map[string]oam.PropertySchema{
		"volumes":                       podListSchema("Pod spec.volumes: the volumes the containers can mount. A hostPath volume is refused unless the environment policy allows hostPath volumes.", "One volume: name and exactly one volume source.", "Volume"),
		"initContainers":                podListSchema("Pod spec.initContainers: the containers run to completion, in order, before the regular ones. Held to the same rules as containers.", container, "Container"),
		"containers":                    containers,
		"restartPolicy":                 str("Pod spec.restartPolicy: Always, OnFailure or Never. The API server defaults it to Always."),
		"terminationGracePeriodSeconds": integer("Pod spec.terminationGracePeriodSeconds: seconds the pod is given to stop before it is killed. The API server defaults it to 30."),
		"activeDeadlineSeconds":         integer("Pod spec.activeDeadlineSeconds: seconds the pod may be active before it is marked failed and its containers are killed."),
		"dnsPolicy":                     str("Pod spec.dnsPolicy: ClusterFirst, ClusterFirstWithHostNet, Default or None. The API server defaults it to ClusterFirst."),
		"nodeSelector": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "Pod spec.nodeSelector: node labels a node must carry for the pod to be scheduled on it.",
		},
		"serviceAccountName":           str("Pod spec.serviceAccountName: the ServiceAccount the pod runs as. Unset, the pod runs as the namespace's default account; no account is generated."),
		"serviceAccount":               str("Pod spec.serviceAccount: deprecated alias of serviceAccountName, which the API server reads only when serviceAccountName is unset."),
		"automountServiceAccountToken": boolean("Pod spec.automountServiceAccountToken: whether the ServiceAccount token is mounted into the pod. Unset, the ServiceAccount's own setting applies."),
		"nodeName":                     str("Pod spec.nodeName: the node the pod is bound to, bypassing the scheduler."),
		"hostNetwork":                  boolean("Pod spec.hostNetwork: run in the node's network namespace. Refused unless the environment policy allows it."),
		"hostPID":                      boolean("Pod spec.hostPID: run in the node's process namespace. Refused unless the environment policy allows it."),
		"hostIPC":                      boolean("Pod spec.hostIPC: run in the node's IPC namespace. Refused unless the environment policy allows it."),
		"shareProcessNamespace":        boolean("Pod spec.shareProcessNamespace: share one process namespace between the pod's containers."),
		"securityContext":              podObjectSchema("Pod spec.securityContext: the pod-level security attributes. windowsOptions.hostProcess is refused unless the environment policy allows privileged containers.", "PodSecurityContext"),
		"imagePullSecrets":             podListSchema("Pod spec.imagePullSecrets: the Secrets used to pull the pod's images.", "One reference: name.", "LocalObjectReference"),
		"hostname":                     str("Pod spec.hostname: the pod's hostname. Unset, it is the pod's name."),
		"subdomain":                    str("Pod spec.subdomain: with hostname, gives the pod the DNS name <hostname>.<subdomain>.<namespace>.svc.<cluster domain>."),
		"affinity":                     podObjectSchema("Pod spec.affinity: the node affinity, pod affinity and pod anti-affinity scheduling rules.", "Affinity"),
		"schedulerName":                str("Pod spec.schedulerName: the scheduler that places the pod. The API server defaults it to default-scheduler."),
		"tolerations":                  podListSchema("Pod spec.tolerations: the node taints the pod tolerates.", "One toleration: key, operator, value, effect and tolerationSeconds.", "Toleration"),
		"hostAliases":                  podListSchema("Pod spec.hostAliases: entries added to the containers' hosts file.", "One entry: ip and hostnames.", "HostAlias"),
		"priorityClassName":            str("Pod spec.priorityClassName: the PriorityClass the pod's priority is taken from."),
		"dnsConfig":                    podObjectSchema("Pod spec.dnsConfig: DNS parameters merged with those dnsPolicy generates.", "PodDNSConfig"),
		"readinessGates":               podListSchema("Pod spec.readinessGates: the extra conditions that must be true for the pod to be ready.", "One gate: conditionType.", "PodReadinessGate"),
		"runtimeClassName":             str("Pod spec.runtimeClassName: the RuntimeClass the pod runs with."),
		"enableServiceLinks":           boolean("Pod spec.enableServiceLinks: whether the namespace's Services are injected as environment variables. The API server defaults it to true."),
		"preemptionPolicy":             str("Pod spec.preemptionPolicy: Never or PreemptLowerPriority. The API server defaults it to PreemptLowerPriority."),
		"topologySpreadConstraints":    podListSchema("Pod spec.topologySpreadConstraints: how pods matching a selector are spread across topology domains.", "One constraint: maxSkew, topologyKey, whenUnsatisfiable, labelSelector and the fields that refine them.", "TopologySpreadConstraint"),
		"setHostnameAsFQDN":            boolean("Pod spec.setHostnameAsFQDN: make the pod's hostname its fully qualified domain name."),
		"os":                           podObjectSchema("Pod spec.os: the operating system of the pod's containers (name: linux or windows).", "PodOS"),
		"hostUsers":                    boolean("Pod spec.hostUsers: false runs the pod in a user namespace of its own. The API server defaults it to true."),
		"schedulingGates":              podListSchema("Pod spec.schedulingGates: gates that hold the pod unscheduled until each is removed.", "One gate: name.", "PodSchedulingGate"),
		"resourceClaims":               podListSchema("Pod spec.resourceClaims: the ResourceClaims the pod's containers can consume.", "One claim: name and resourceClaimName or resourceClaimTemplateName.", "PodResourceClaim"),
		"resources":                    podObjectSchema("Pod spec.resources: the cpu and memory the pod's containers share. Held to the environment policy's cpu and memory maxima.", "ResourceRequirements"),
		"hostnameOverride":             str("Pod spec.hostnameOverride: the hostname the pod reports, replacing hostname, subdomain and setHostnameAsFQDN."),
		"schedulingGroup":              podObjectSchema("Pod spec.schedulingGroup: the scheduling group the pod belongs to (podGroupName).", "PodSchedulingGroup"),
		"evictionResponders":           podListSchema("Pod spec.evictionResponders: the responders that act on an eviction request for the pod, at most ten.", "One responder: name and priority.", "EvictionResponder"),
	}
}

// probeDefaultedZeroFields are the corev1.Probe fields on which an authored 0
// cannot be carried: the type omits a zero there, and the API server then
// applies the non-zero default its field comment states (k8s.io/api
// core/v1/types.go, Probe: "Defaults to 1 second", "Default to 10 seconds",
// "Defaults to 1", "Defaults to 3"), where the author wrote a value it would
// refuse as below the minimum of 1. initialDelaySeconds is not one: its
// omitted value is 0.
var probeDefaultedZeroFields = map[string]string{
	"timeoutSeconds":   "1",
	"periodSeconds":    "10",
	"successThreshold": "1",
	"failureThreshold": "3",
}

// podSpecDefaultedZeros is the defaulted-zero list of a corev1.PodSpec whose
// fields sit under prefix in the authored properties ("" for the pod kind): the
// probeDefaultedZeroFields of the three probes of every init and regular
// container. TestPodSpecDefaultedZeros_MatchFieldDocs holds it to the linked
// type: every other omitempty number or boolean under PodSpec defaults to its
// zero.
func podSpecDefaultedZeros(prefix string) defaultedZeroFields {
	fields := map[string]string{}
	for _, list := range []string{"initContainers", "containers"} {
		for _, probe := range []string{"livenessProbe", "readinessProbe", "startupProbe"} {
			for field, def := range probeDefaultedZeroFields {
				fields[prefix+list+"[]."+probe+"."+field] = def
			}
		}
	}
	return defaultedZeroFields{api: "Kubernetes", defaulter: "API server", fields: fields}
}

// validateAuthoredPodSpec refuses, with or without an environment policy, what
// a pod spec authored through a kind component may not hold. prefix is the
// spec's path in the component's properties, ending in a dot, or "" when the
// properties are the spec's own fields.
//
// containers must be authored: the Go type cannot omit the list, so an unset
// one would be written as null. ephemeralContainers, priority and overhead are
// refused with the texts the workload kinds give (podSpecRejectedKeys), as
// cnpg-pooler refuses them on its template: a pod cannot be created with
// ephemeral containers, and the Priority and RuntimeClass admission controllers
// set the other two. An empty ephemeralContainers or overhead carries nothing
// and is read as unset. Every init and regular container's image is held to
// ValidateImageRef: no untagged image and no :latest. So is the reference of
// every image volume (validateImageVolumeRefs). The API's other value rules
// are left to the API server.
func validateAuthoredPodSpec(prefix string, ps *corev1.PodSpec) error {
	if ps.Containers == nil {
		return errors.Errorf("%scontainers: required (the containers the pod runs)", prefix)
	}
	switch {
	case len(ps.EphemeralContainers) > 0:
		return errors.New(prefix + podSpecRejectedKeys["ephemeralContainers"])
	case ps.Priority != nil:
		return errors.New(prefix + podSpecRejectedKeys["priority"])
	case len(ps.Overhead) > 0:
		return errors.New(prefix + podSpecRejectedKeys["overhead"])
	}
	for i, ctr := range ps.InitContainers {
		if err := ValidateImageRef(ctr.Image); err != nil {
			return errors.Wrapf(err, "%sinitContainers[%d] %q", prefix, i, ctr.Name)
		}
	}
	for i, ctr := range ps.Containers {
		if err := ValidateImageRef(ctr.Image); err != nil {
			return errors.Wrapf(err, "%scontainers[%d] %q", prefix, i, ctr.Name)
		}
	}
	return validateImageVolumeRefs(prefix, ps)
}

// validateImageVolumeRefs holds the reference of every image volume of a pod
// spec to ValidateImageRef, with or without an environment policy: the kubelet
// pulls it as it pulls a container's image. A volume that names no reference
// names no image, and nothing is checked for it, as the registry allowlist
// checks nothing for it (enforcePodTemplatePolicy). prefix is the spec's path,
// as in validateAuthoredPodSpec.
func validateImageVolumeRefs(prefix string, ps *corev1.PodSpec) error {
	for i, v := range ps.Volumes {
		if v.Image == nil || v.Image.Reference == "" {
			continue
		}
		if err := ValidateImageRef(v.Image.Reference); err != nil {
			return errors.Wrapf(err, "%svolumes[%d] %q image.reference", prefix, i, v.Name)
		}
	}
	return nil
}

// ToApplicationConfig decodes an OAM pod component into a PodConfig, under the
// package's null contract and the strict decode every spec-projecting kind
// uses. What a pod may not hold is refused here, whatever the environment
// policy: see validateAuthoredPodSpec and podSpecDefaultedZeros.
func (h *PodHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	spec, props, err := decodeKindSpec[corev1.PodSpec](component.Properties, "v1 PodSpec")
	if err != nil {
		return nil, err
	}
	if err := refuseUncarriedSpecValues(props, spec, podSpecDefaultedZeros("")); err != nil {
		return nil, err
	}
	cfg := &PodConfig{Name: component.Name, ObjectName: componentObjectName(component), Metadata: component.ObjectMetadata(), Namespace: namespace, Spec: *spec}
	if err := validateAuthoredPodSpec("", &cfg.Spec); err != nil {
		return nil, err
	}
	return cfg, nil
}

// PodConfig implements stack.ApplicationConfig for pod components. Spec is the
// decoded PodSpec exactly as authored.
type PodConfig struct {
	Name string
	// ObjectName names the Pod (oam.Component.ObjectName); its labels keep
	// Name. Empty for the application's name.
	ObjectName string
	// Metadata is the labels and annotations authored for the Pod
	// (oam.Component.ObjectMetadata).
	Metadata  oam.ObjectMetadata
	Namespace string
	Spec      corev1.PodSpec
}

// ServiceAccountName implements oam.ServiceAccountNamer: the account the pod
// runs as, which is the authored serviceAccountName, or the deprecated
// serviceAccount where that one is unset, as the API server reads the two. No
// account is generated, so "" means the namespace's default account.
func (c *PodConfig) ServiceAccountName() (string, bool) {
	if c.Spec.ServiceAccountName != "" {
		return c.Spec.ServiceAccountName, true
	}
	return c.Spec.DeprecatedServiceAccount, true
}

// ApplyPolicy holds the pod to the environment policy, through the check the
// rendered-object check runs on a Pod a chart renders, a passthrough component
// holds or a manifests source yields (enforcePodTemplatePolicy): host
// namespaces, hostPath volumes, the storage and resource maxima, and per init
// and regular container the registry allowlist, the cpu and memory maxima and
// the privileged, hostProcess and capability gates. It fills no default: a
// container without resources stays without. A nil policy checks nothing.
func (c *PodConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}
	return enforcePodTemplatePolicy("", &c.Spec, p)
}

// Generate emits the Pod: kure's identity-only constructor, the `app` label
// (appLabels) and a deep copy of the spec. The label is what a trait's
// NetworkPolicy selects, so the pod is a trait target like a workload kind's
// pods. The parse-time refusals the typed spec can show are repeated, since
// the config is exported.
func (c *PodConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	if err := validateAuthoredPodSpec("", &c.Spec); err != nil {
		return nil, err
	}
	pod := kubernetes.CreatePod(kindObjectName(c.ObjectName, app.Name), app.Namespace)
	pod.Labels = appLabels(app.Name)
	c.Spec.DeepCopyInto(&pod.Spec)
	return kindObject(pod, c.Metadata)
}
