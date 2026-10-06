package components

import (
	"maps"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// ContainerFields holds the corev1.Container fields every container of a
// hand-parsed workload kind accepts beside the ones its config names itself
// (go-kure/launcher#790): the main container, where they are top-level
// properties of the kind, and every `initContainers` and `sidecars` entry.
//
// Nothing is inferred from them and none is defaulted here: an unauthored
// field stays at its zero value and is omitted from the generated container,
// which leaves the API server's own defaults in force (SetDefaults_Container,
// k8s.io/kubernetes pkg/apis/core/v1/defaults.go: the pull policy from the
// image reference, /dev/termination-log, File).
//
// Two corev1.Container fields stay unread:
//   - `restartPolicy` on an init container is what makes it restartable (a
//     native sidecar), a container this package does not model: the probes and
//     lifecycle hooks such a container may carry are what
//     initContainerRejectedKeys refuses on an init entry.
//   - `restartPolicyRules` requires `restartPolicy` ("must specify
//     restartPolicy when restart rules are used",
//     validateContainerRestartPolicy), so it waits with it.
type ContainerFields struct {
	ImagePullPolicy          corev1.PullPolicy
	TerminationMessagePath   string
	TerminationMessagePolicy corev1.TerminationMessagePolicy
	Stdin                    bool
	StdinOnce                bool
	TTY                      bool
	ResizePolicy             []corev1.ContainerResizePolicy
}

// containerFieldKeys is the key set parseContainerFields reads. It is pinned to
// schemaContainerFields by TestContainerFieldsSchemaMatchesParser, appended to
// initContainerPropertyKeys and sidecarPropertyKeys, and copied into every
// workload kind's schema.
var containerFieldKeys = []string{
	"imagePullPolicy", "terminationMessagePath", "terminationMessagePolicy",
	"stdin", "stdinOnce", "tty", "resizePolicy",
}

// resizePolicyEntryKeys is the closed key set of one `resizePolicy` entry.
var resizePolicyEntryKeys = []string{"resourceName", "restartPolicy"}

// The enums below are the upstream sets: validatePullPolicy,
// validateContainerCommon (terminationMessagePolicy) and validateResizePolicy
// in k8s.io/kubernetes pkg/apis/core/validation/validation.go.
var (
	pullPolicies = []corev1.PullPolicy{corev1.PullAlways, corev1.PullIfNotPresent, corev1.PullNever}

	terminationMessagePolicies = []corev1.TerminationMessagePolicy{
		corev1.TerminationMessageReadFile, corev1.TerminationMessageFallbackToLogsOnError,
	}

	resizeResources = []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory}

	resizeRestartPolicies = []corev1.ResourceResizeRestartPolicy{corev1.NotRequired, corev1.RestartContainer}
)

// enumValues returns a typed string set as the []any a PropertySchema.Enum takes.
func enumValues[T ~string](set []T) []any {
	out := make([]any, len(set))
	for i, v := range set {
		out[i] = string(v)
	}
	return out
}

// schemaContainerFields describes the properties parseContainerFields reads.
// The same fragment is published for the main container, an init container and
// a sidecar: the one difference between them is a value, not a key (an init
// container's resize restart policy), so it is stated in the description.
func schemaContainerFields() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"imagePullPolicy": {
			Type: oam.PropertyTypeString, Enum: enumValues(pullPolicies),
			Description: "When the kubelet pulls the container image. Omitted, the API server defaults it from the image reference. No environment policy gates it: Never and IfNotPresent run whatever image a node already holds under that reference.",
		},
		"terminationMessagePath": {
			Type:        oam.PropertyTypeString,
			Description: "Path in the container the termination message is read from. The API default is /dev/termination-log.",
		},
		"terminationMessagePolicy": {
			Type: oam.PropertyTypeString, Enum: enumValues(terminationMessagePolicies),
			Description: "Where the termination message comes from: File reads terminationMessagePath, FallbackToLogsOnError uses the tail of the container log when that file is empty and the container failed. The API default is File.",
		},
		"stdin": {
			Type:        oam.PropertyTypeBoolean,
			Description: "Keep a stdin buffer allocated for the container. The API default is false.",
		},
		"stdinOnce": {
			Type:        oam.PropertyTypeBoolean,
			Description: "Close the stdin channel after the first attach session ends. It has an effect only with stdin: true.",
		},
		"tty": {
			Type:        oam.PropertyTypeBoolean,
			Description: "Allocate a TTY for the container. It needs stdin: true.",
		},
		"resizePolicy": {
			Type:        oam.PropertyTypeArray,
			Description: "How the container reacts to an in-place resize of each resource. On an init container and on a pod whose restartPolicy is Never only NotRequired is accepted.",
			Items: &oam.PropertySchema{
				Type:        oam.PropertyTypeObject,
				Description: "The resize policy of one resource; a resource may be named once.",
				Properties: map[string]oam.PropertySchema{
					"resourceName":  {Type: oam.PropertyTypeString, Required: true, Enum: enumValues(resizeResources), Description: "The resource this policy applies to."},
					"restartPolicy": {Type: oam.PropertyTypeString, Required: true, Enum: enumValues(resizeRestartPolicies), Description: "Whether the container is restarted when the resource is resized."},
				},
			},
		},
	}
}

// withContainerFields copies schemaContainerFields into a kind's or an entry's
// property map and returns it.
func withContainerFields(m map[string]oam.PropertySchema) map[string]oam.PropertySchema {
	maps.Copy(m, schemaContainerFields())
	return m
}

// parseMainContainerFields is parseContainerFields for a type's main container,
// whose keys are the type's own top-level properties. It first refuses the
// corev1.Container fields a main container is not authored with
// (mainContainerRejectedKeys); an entry's key set is closed by its own parser,
// and there several of those names are keys of the entry.
func parseMainContainerFields(props map[string]any) (ContainerFields, error) {
	if err := refusedProperty(props, mainContainerRejectedKeys); err != nil {
		return ContainerFields{}, err
	}
	return parseContainerFields(props, false)
}

// parseContainerFields reads the containerFieldKeys from raw: a kind's
// top-level properties for its main container, or one `initContainers` or
// `sidecars` entry. Errors name the field only; the caller adds the entry
// label.
//
// initContainer applies the one rule that differs for an init container:
// upstream refuses a resize restart policy of RestartContainer on an init
// container that is not restartable ("must not be set to 'RestartContainer'
// for non-sidecar initContainers", validateInitContainers), and this package
// models no restartable one. The rule that depends on the pod's restart policy
// is buildPodSpec's to check (checkResizePolicyRestart): the kind decides that
// policy, not the container.
func parseContainerFields(raw map[string]any, initContainer bool) (ContainerFields, error) {
	var out ContainerFields

	// The two enums read "" as a value, so it is refused here as the published
	// enum refuses it in a document: one answer on both paths.
	if v, present, err := parseRawStringField(raw, "imagePullPolicy", "imagePullPolicy"); err != nil {
		return out, err
	} else if present {
		if !containsValue(pullPolicies, corev1.PullPolicy(v)) {
			return out, errors.Errorf("imagePullPolicy: invalid value %q, must be one of %s", v, joinValues(pullPolicies))
		}
		out.ImagePullPolicy = corev1.PullPolicy(v)
	}
	if v, present, err := parseStringField(raw, "terminationMessagePath", "terminationMessagePath"); err != nil {
		return out, err
	} else if present {
		out.TerminationMessagePath = v
	}
	if v, present, err := parseRawStringField(raw, "terminationMessagePolicy", "terminationMessagePolicy"); err != nil {
		return out, err
	} else if present {
		if !containsValue(terminationMessagePolicies, corev1.TerminationMessagePolicy(v)) {
			return out, errors.Errorf("terminationMessagePolicy: invalid value %q, must be one of %s", v, joinValues(terminationMessagePolicies))
		}
		out.TerminationMessagePolicy = corev1.TerminationMessagePolicy(v)
	}
	for _, f := range []struct {
		key string
		dst *bool
	}{
		{"stdin", &out.Stdin},
		{"stdinOnce", &out.StdinOnce},
		{"tty", &out.TTY},
	} {
		v, err := parseBoolField(raw, f.key, f.key)
		if err != nil {
			return out, err
		}
		if v != nil {
			*f.dst = *v
		}
	}

	entries, _, err := parseObjectList(raw, "resizePolicy")
	if err != nil {
		return out, err
	}
	seen := make(map[corev1.ResourceName]bool, len(entries))
	for i, entry := range entries {
		label := indexedLabel("resizePolicy", i)
		if err := rejectUnknownKeys(entry, resizePolicyEntryKeys, label); err != nil {
			return out, err
		}
		resource, err := requiredStringField(entry, "resourceName", label)
		if err != nil {
			return out, err
		}
		name := corev1.ResourceName(resource)
		if !containsValue(resizeResources, name) {
			return out, errors.Errorf("%s.resourceName: invalid value %q, must be one of %s", label, resource, joinValues(resizeResources))
		}
		if seen[name] {
			return out, errors.Errorf("%s.resourceName: %q is named by an earlier entry; a resource takes one resize policy", label, resource)
		}
		seen[name] = true
		restart, err := requiredStringField(entry, "restartPolicy", label)
		if err != nil {
			return out, err
		}
		policy := corev1.ResourceResizeRestartPolicy(restart)
		if !containsValue(resizeRestartPolicies, policy) {
			return out, errors.Errorf("%s.restartPolicy: invalid value %q, must be one of %s", label, restart, joinValues(resizeRestartPolicies))
		}
		if initContainer && policy == corev1.RestartContainer {
			return out, errors.Errorf("%s.restartPolicy: %s is not accepted on an init container; Kubernetes allows it only on a restartable one, which this package does not model", label, corev1.RestartContainer)
		}
		out.ResizePolicy = append(out.ResizePolicy, corev1.ContainerResizePolicy{ResourceName: name, RestartPolicy: policy})
	}
	return out, nil
}

// apply writes the authored fields onto a container under construction. The
// resize policy is copied: the config is reusable and a generated object may be
// edited in place (see buildPodSpec).
func (f ContainerFields) apply(container *corev1.Container) {
	container.ImagePullPolicy = f.ImagePullPolicy
	container.TerminationMessagePath = f.TerminationMessagePath
	container.TerminationMessagePolicy = f.TerminationMessagePolicy
	container.Stdin = f.Stdin
	container.StdinOnce = f.StdinOnce
	container.TTY = f.TTY
	if len(f.ResizePolicy) > 0 {
		container.ResizePolicy = append([]corev1.ContainerResizePolicy(nil), f.ResizePolicy...)
	}
}

// checkResizePolicyRestart refuses a resize restart policy other than
// NotRequired on a pod whose restartPolicy is Never — upstream's "must be
// 'NotRequired' when pod `restartPolicy` is 'Never'" (validateResizePolicy).
// Only job and cronjob can author that pod policy; it runs on the assembled pod
// spec so every container list is covered by one check.
func checkResizePolicyRestart(ps *corev1.PodSpec) error {
	if ps.RestartPolicy != corev1.RestartPolicyNever {
		return nil
	}
	check := func(list string, containers []corev1.Container) error {
		for _, c := range containers {
			for i, p := range c.ResizePolicy {
				if p.RestartPolicy != corev1.NotRequired {
					return errors.Errorf("%s %q: %s.restartPolicy: %s is not accepted when the pod's restartPolicy is Never; Kubernetes requires %s there",
						list, c.Name, indexedLabel("resizePolicy", i), p.RestartPolicy, corev1.NotRequired)
				}
			}
		}
		return nil
	}
	if err := check("initContainers", ps.InitContainers); err != nil {
		return err
	}
	return check("containers", ps.Containers)
}
