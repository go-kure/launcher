package components

import (
	"maps"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// WorkerRule lowers a "worker" component (D1 component position,
// oam.ComponentLoweringRule) into a terminal "deployment" component carrying
// the same name and, unless `serviceAccountName` is authored, a same-name
// "serviceaccount" sibling (roleServiceAccount): the component's own
// ServiceAccount, with `automountServiceAccountToken: false` as the handler
// generated it, while the deployment member is handed the account's object name
// as `serviceAccountName` so it generates none of its own. The objects of both
// members are named as WebserviceRule names its members' (go-kure/launcher#787):
// by `deploymentObjectName` and `serviceAccountObjectName`, else by the Naming
// hook, else after the component. It
// is the first production component-position rule, and the
// re-expression of the former WorkerHandler: worker was a Deployment with no
// Service plus two of launcher's own opinions, and `deployment` is the
// unopinionated projection of that same API kind, so what worker adds is
// exactly the two opinions — and those are what this rule evaluates.
//
// LowerComponent first runs worker's full parse (parseWorker, the former
// WorkerHandler.ToApplicationConfig sequence unchanged), so every input the
// handler refused is still refused, first failure first, with the same cause
// text. The handler's last check, that the `affinity` shorthand evaluates to
// label selectors the API server accepts (topology key, node selector keys and
// values), runs next on the raw shape the rule forwards, with the same text. It
// then emits the authored properties to the deployment component, changing
// only:
//
//   - `affinity`: the four-key shorthand is evaluated exactly as worker
//     evaluated it (buildAffinity, over the component's own app label) and
//     emitted as the raw corev1 shape `deployment` publishes; omitted when the
//     shorthand evaluates to nothing.
//   - `topologySpread`: removed. Unless it was explicitly false, the rule
//     attaches a `topology-spread` trait instead — the trait that applies the
//     same default constraints (BuildTopologySpreadConstraints) to the same
//     post-policy replica count and the same selector.
//
// Keys worker does not declare are dropped rather than forwarded: worker's
// parse never read them, while `deployment` would honor two of them
// (`tolerations`, `topologySpreadConstraints`) and refuse the rest. The kurel
// CLI refuses such keys before lowering in any case
// (Transformer.ValidateAuthoredProperties); dropping them keeps a caller that
// skips that validation exactly where it was.
//
// The authored traits are forwarded unchanged, and the synthesized
// `topology-spread` goes in front of them: it is then the innermost trait
// decorator, which is where worker applied its constraints — inside its own
// Generate, before any trait saw the Deployment. An authored `topology-spread`
// on a worker therefore still refuses with the same message whenever the
// default already produced constraints, and still does nothing when it did
// not. Annotations (the tier override, among others) are forwarded too, to
// both members; the serviceaccount member also gets the authored
// `prune-protection` and `force-replace` traits, which cover every object the
// component generates: they set a delivery intent on each member's application,
// and the group's one application takes the intent any member has.
//
// Everything past the parse is the deployment component's: ApplyPolicy,
// NonRWXClaim, ServiceAccountName, labels and the
// generated objects are the ones DeploymentConfig implements, which worker's
// implementations matched line for line. A parse error surfaces through the
// lowering engine, so it names the component's type and document
// (`component "w" (type "worker") in document …: <cause>`) instead of the
// former handler's `component "w": <cause>`; the cause text is unchanged.
type WorkerRule struct{}

// ComponentType claims the "worker" component type at the component lowering
// position. build.go registers this rule via RegisterComponentLowering instead
// of a dispatchable component handler, so "worker" is reachable only here.
func (WorkerRule) ComponentType() string { return "worker" }

// PropertySchema declares the worker component's user-facing properties. Like
// webservice minus `port` (worker emits no Service). Unchanged by the move to a
// lowering rule: HandlerSchemas publishes a rule's schema exactly as it
// publishes a handler's, and TestWorkerRule_PropertySchemaUnchanged pins it
// byte for byte. The two object-name properties (schemaRoleObjectNames) were
// added since, by go-kure/launcher#787.
func (WorkerRule) PropertySchema() map[string]oam.PropertySchema {
	m := map[string]oam.PropertySchema{
		"image":           {Type: oam.PropertyTypeString, Required: true, Description: "Container image reference for the main container."},
		"replicas":        {Type: oam.PropertyTypeInteger, Default: 1, Description: "Number of Deployment pod replicas."},
		"topologySpread":  {Type: oam.PropertyTypeBoolean, Default: true, Description: "Whether default topology spread constraints are applied across nodes."},
		"env":             schemaEnv(false),
		"envFrom":         schemaEnvFrom(false),
		"resources":       schemaResources(false),
		"command":         schemaStringArray(),
		"args":            schemaStringArray(),
		"probes":          schemaProbes(false),
		"lifecycle":       schemaLifecycle(false),
		"securityContext": schemaSecurityContext(false),
		"workingDir":      schemaWorkingDir(false),
		"volumes":         schemaVolumes(),
		"initContainers":  schemaInitContainers(),
		"sidecars":        schemaSidecars(),
		"affinity":        schemaAffinity(),
	}
	maps.Copy(m, schemaContainerFields())
	maps.Copy(m, schemaPodSpec(false, false))
	maps.Copy(m, schemaDeploymentSpec())
	maps.Copy(m, schemaRoleObjectNames(false))
	return m
}

// LowerComponent validates comp as a worker and emits the equivalent
// deployment component (see WorkerRule).
func (r WorkerRule) LowerComponent(comp *oam.Component, lctx oam.LoweringContext) (oam.LoweringResult, error) {
	opinions, err := parseWorker(comp.Properties)
	if err != nil {
		return oam.LoweringResult{}, err
	}

	schema := r.PropertySchema()
	props := make(map[string]any, len(comp.Properties))
	for k, v := range comp.Properties {
		if _, declared := schema[k]; declared {
			props[k] = v
		}
	}
	delete(props, "topologySpread")
	delete(props, "affinity")
	dropRoleObjectNames(props)
	if affinity := buildAffinity(opinions.affinity, appLabels(comp.Name)); affinity != nil {
		raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(affinity)
		if err != nil {
			return oam.LoweringResult{}, errors.Wrap(err, "affinity: converting the evaluated shorthand")
		}
		props["affinity"] = raw
		// The deployment component validates the raw shape more strictly than
		// the shorthand's own parse does (label-key and label-value syntax,
		// which the API server enforces too). Checked here, as the former
		// handler did, so a refusal names the shorthand the author wrote rather
		// than a raw path they never did.
		if _, err := parseRawAffinity(props); err != nil {
			return oam.LoweringResult{}, errors.Wrap(err, "affinity: the shorthand evaluates to an affinity the API server would refuse")
		}
	}

	claimTraits, err := roleClaims(comp, props, lctx)
	if err != nil {
		return oam.LoweringResult{}, err
	}

	var synthesized []oam.Trait
	if !opinions.topologySpreadDisabled {
		synthesized = append(synthesized, oam.Trait{Type: "topology-spread", Properties: map[string]any{}})
	}
	synthesized = append(synthesized, claimTraits...)
	traits := comp.Traits
	if len(synthesized) > 0 {
		traits = append(synthesized, comp.Traits...)
	}

	members := []oam.Component{{
		Name:        comp.Name,
		Type:        "deployment",
		Properties:  props,
		Traits:      traits,
		Annotations: comp.Annotations,
	}}
	// Each member's object is named in emission order, as WebserviceRule names
	// its members': the member keeps the component's name, and the pods'
	// serviceAccountName reads the account's object name.
	if err := nameRoleMember(comp, lctx, &members[0], &DeploymentHandler{}, oam.NameRoleWorkloadDeployment, deploymentObjectNameProperty, "Deployment"); err != nil {
		return oam.LoweringResult{}, err
	}
	sa, err := roleServiceAccount(comp, props, comp.Traits, lctx)
	if err != nil {
		return oam.LoweringResult{}, err
	}
	if sa != nil {
		members = append(members, *sa)
	}
	return oam.LoweringResult{Components: members}, nil
}

// workerOpinions is what parseWorker keeps: the two properties worker
// evaluates itself rather than handing to the deployment component.
type workerOpinions struct {
	affinity               AffinityConfig
	topologySpreadDisabled bool
}

// parseWorker runs the former WorkerHandler.ToApplicationConfig parse over
// props, in its original order, so an invalid worker is refused with the same
// first cause as before. Every other parsed value is discarded here — the
// deployment component parses the forwarded properties again, with the same
// helpers.
func parseWorker(props map[string]any) (workerOpinions, error) {
	var out workerOpinions

	image, ok := props["image"].(string)
	if !ok {
		return out, errors.New("required property 'image' missing or not a string")
	}
	if err := ValidateImageRef(image); err != nil {
		return out, err
	}
	if _, _, err := parseReplicas(props, 1); err != nil {
		return out, err
	}
	env, err := parseEnv(props)
	if err != nil {
		return out, err
	}
	if _, err := parseEnvFrom(props); err != nil {
		return out, err
	}
	if resources, present, err := parseObjectField(props, "resources", "resources"); err != nil {
		return out, err
	} else if present {
		if _, err := parseResources(resources); err != nil {
			return out, errors.Wrap(err, "invalid resources configuration")
		}
	}
	if _, err := parseCommand(props); err != nil {
		return out, err
	}
	if _, err := parseArgs(props); err != nil {
		return out, err
	}
	// namedPortsAllowed=false: worker exposes no port property at all, so its
	// main container never declares a ContainerPort for the kubelet to
	// resolve a named probe/lifecycle port against.
	if _, err := parseProbes(props, false, ""); err != nil {
		return out, errors.Wrap(err, "invalid probe configuration")
	}
	if _, err := parseLifecycle(props, false, ""); err != nil {
		return out, errors.Wrap(err, "invalid lifecycle configuration")
	}
	if _, err := parseSecurityContext(props); err != nil {
		return out, errors.Wrap(err, "invalid securityContext configuration")
	}
	if _, _, err := parseStringField(props, "workingDir", "workingDir"); err != nil {
		return out, err
	}
	if _, err := parseContainerFields(props, false); err != nil {
		return out, err
	}

	parsed, err := parseVolumes(props)
	if err != nil {
		return out, err
	}
	initContainers, err := parseInitContainers(props)
	if err != nil {
		return out, err
	}
	if ts, err := parseBoolField(props, "topologySpread", "topologySpread"); err != nil {
		return out, err
	} else if ts != nil && !*ts {
		out.topologySpreadDisabled = true
	}
	affinity, err := parseAffinity(props)
	if err != nil {
		return out, err
	}
	out.affinity = affinity

	sidecars, err := parseSidecars(props)
	if err != nil {
		return out, err
	}
	if err := checkExtraContainerVolumeModes(declaredVolumeModes(parsed, nil), initContainers, sidecars); err != nil {
		return out, err
	}
	// The main container declares no ports, so only sidecars can collide.
	if err := checkPodPortNames(nil, sidecars); err != nil {
		return out, err
	}
	if err := checkFileKeyRefVolumes(parsed.Volumes, env, initContainers, sidecars); err != nil {
		return out, err
	}
	if _, err := parsePodSpec(props, false); err != nil {
		return out, err
	}
	if _, err := parseDeploymentSpec(props); err != nil {
		return out, err
	}
	return out, nil
}
