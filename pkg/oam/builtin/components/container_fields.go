package components

import (
	"maps"
	"math"
	"slices"

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
// An init container's own `restartPolicy` and `restartPolicyRules` are read
// apart from these, by parseInitContainerRestart: they are init-entry keys
// only, and restartPolicy Always is refused there.
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

// The restart policy and rules of an init container (parseInitContainerRestart)
// are held to upstream's validateInitContainerRestartPolicy and
// validateContainerRestartPolicy (k8s.io/kubernetes
// pkg/apis/core/validation/validation.go): a rule needs the container's own
// policy, a container takes at most 20 rules, a rule's action is Restart and
// its exitCodes are required, with an operator of In or NotIn and at most 255
// values. Upstream's Always is left out of initContainerRestartPolicies: it
// makes the init container restartable (a native sidecar), which this package
// does not model, and RestartAllContainers is left out of restartRuleActions:
// it needs the RestartAllContainersOnContainerExits gate.
const (
	maxRestartPolicyRules   = 20
	maxRestartRuleExitCodes = 255
)

var (
	initContainerRestartPolicies = []corev1.ContainerRestartPolicy{corev1.ContainerRestartPolicyNever, corev1.ContainerRestartPolicyOnFailure}

	restartRuleActions = []corev1.ContainerRestartRuleAction{corev1.ContainerRestartRuleActionRestart}

	restartRuleOperators = []corev1.ContainerRestartRuleOnExitCodesOperator{
		corev1.ContainerRestartRuleOnExitCodesOpIn, corev1.ContainerRestartRuleOnExitCodesOpNotIn,
	}

	restartRuleKeys          = []string{"action", "exitCodes"}
	restartRuleExitCodesKeys = []string{"operator", "values"}
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

// parseInitContainerRestart reads one `initContainers` entry's own
// `restartPolicy` and `restartPolicyRules` (go-kure/launcher#790), held to the
// upstream rules named above. Never and OnFailure override the pod's restart
// policy for that container; Always is refused, as it makes the container a
// native sidecar. Kubernetes accepts a policy other than Always on an init
// container only with the ContainerRestartRules feature gate, on by default
// from 1.35; the cluster's version is not known here, so that is documented,
// not checked. Errors name the field only; the caller adds the entry label.
func parseInitContainerRestart(raw map[string]any) (*corev1.ContainerRestartPolicy, []corev1.ContainerRestartRule, error) {
	var policy *corev1.ContainerRestartPolicy
	if v, present, err := parseRawStringField(raw, "restartPolicy", "restartPolicy"); err != nil {
		return nil, nil, err
	} else if present {
		p := corev1.ContainerRestartPolicy(v)
		if p == corev1.ContainerRestartPolicyAlways {
			return nil, nil, errors.Errorf("restartPolicy: %s is not accepted on an init container; it makes the container restartable (a native sidecar), which this package does not model; author a sidecar instead", p)
		}
		if !containsValue(initContainerRestartPolicies, p) {
			return nil, nil, errors.Errorf("restartPolicy: invalid value %q, must be one of %s", v, joinValues(initContainerRestartPolicies))
		}
		policy = &p
	}

	entries, _, err := parseObjectList(raw, "restartPolicyRules")
	if err != nil {
		return nil, nil, err
	}
	if len(entries) == 0 {
		return policy, nil, nil
	}
	if policy == nil {
		return nil, nil, errors.New("restartPolicyRules: restartPolicy is required when restart rules are used")
	}
	if len(entries) > maxRestartPolicyRules {
		return nil, nil, errors.Errorf("restartPolicyRules: %d rules, Kubernetes accepts at most %d", len(entries), maxRestartPolicyRules)
	}
	rules := make([]corev1.ContainerRestartRule, 0, len(entries))
	for i, entry := range entries {
		label := indexedLabel("restartPolicyRules", i)
		if err := rejectUnknownKeys(entry, restartRuleKeys, label); err != nil {
			return nil, nil, err
		}
		action, present, err := parseRawStringField(entry, "action", label+".action")
		if err != nil {
			return nil, nil, err
		}
		if !present {
			return nil, nil, errors.Errorf("%s: action is required", label)
		}
		if !containsValue(restartRuleActions, corev1.ContainerRestartRuleAction(action)) {
			return nil, nil, errors.Errorf("%s.action: invalid value %q, must be one of %s", label, action, joinValues(restartRuleActions))
		}
		exitCodes, present, err := parseObjectField(entry, "exitCodes", label+".exitCodes")
		if err != nil {
			return nil, nil, err
		}
		if !present {
			return nil, nil, errors.Errorf("%s: exitCodes is required", label)
		}
		onExit, err := parseRestartRuleExitCodes(exitCodes, label+".exitCodes")
		if err != nil {
			return nil, nil, err
		}
		rules = append(rules, corev1.ContainerRestartRule{Action: corev1.ContainerRestartRuleAction(action), ExitCodes: onExit})
	}
	return policy, rules, nil
}

// parseRestartRuleExitCodes reads the `exitCodes` of one restart rule; label
// is its path, for the errors.
func parseRestartRuleExitCodes(raw map[string]any, label string) (*corev1.ContainerRestartRuleOnExitCodes, error) {
	if err := rejectUnknownKeys(raw, restartRuleExitCodesKeys, label); err != nil {
		return nil, err
	}
	operator, present, err := parseRawStringField(raw, "operator", label+".operator")
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, errors.Errorf("%s: operator is required", label)
	}
	op := corev1.ContainerRestartRuleOnExitCodesOperator(operator)
	if !containsValue(restartRuleOperators, op) {
		return nil, errors.Errorf("%s.operator: invalid value %q, must be one of %s", label, operator, joinValues(restartRuleOperators))
	}
	out := &corev1.ContainerRestartRuleOnExitCodes{Operator: op}
	v, present := authoredValue(raw, "values")
	if !present {
		return out, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, errors.Errorf("%s.values: must be an array, got %T", label, v)
	}
	if len(arr) > maxRestartRuleExitCodes {
		return nil, errors.Errorf("%s.values: %d values, Kubernetes accepts at most %d", label, len(arr), maxRestartRuleExitCodes)
	}
	// values is a +listType=set list: a server-side apply refuses a repeated
	// value, so it is refused here, before the object is written.
	out.Values = make([]int32, 0, len(arr))
	for i, item := range arr {
		code, err := oam.IntegerInRange(item, math.MinInt32, math.MaxInt32)
		if err != nil {
			return nil, errors.Errorf("%s: %w", indexedLabel(label+".values", i), err)
		}
		value := int32(code) //nolint:gosec // bounded to int32 by IntegerInRange
		if slices.Contains(out.Values, value) {
			return nil, errors.Errorf("%s: duplicate value %d", indexedLabel(label+".values", i), value)
		}
		out.Values = append(out.Values, value)
	}
	return out, nil
}

// schemaInitContainerRestart describes the two keys parseInitContainerRestart
// reads, which only an `initContainers` entry takes.
func schemaInitContainerRestart() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"restartPolicy": {
			Type: oam.PropertyTypeString, Enum: enumValues(initContainerRestartPolicies),
			Description: "The init container's own restart policy, overriding the pod's for this container. Always is not accepted: it makes the container a native sidecar, which this package does not model. Needs the cluster's ContainerRestartRules feature gate, on by default from Kubernetes 1.35.",
		},
		"restartPolicyRules": {
			Type:        oam.PropertyTypeArray,
			Description: "Rules on the init container's exit code that decide whether it is restarted, checked in order; at most 20. They require restartPolicy.",
			Items: &oam.PropertySchema{
				Type:        oam.PropertyTypeObject,
				Description: "One restart rule.",
				Properties: map[string]oam.PropertySchema{
					"action": {Type: oam.PropertyTypeString, Required: true, Enum: enumValues(restartRuleActions), Description: "What is done when the rule matches: Restart restarts the container."},
					"exitCodes": {
						Type: oam.PropertyTypeObject, Required: true, Description: "The exit codes the rule matches.",
						Properties: map[string]oam.PropertySchema{
							"operator": {Type: oam.PropertyTypeString, Required: true, Enum: enumValues(restartRuleOperators), Description: "In matches an exit code among the values, NotIn one outside them."},
							"values": {
								Type: oam.PropertyTypeArray, Description: "The exit codes the operator compares against; at most 255, each once.",
								Items: &oam.PropertySchema{Type: oam.PropertyTypeInteger, Description: "One exit code."},
							},
						},
					},
				},
			},
		},
	}
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
