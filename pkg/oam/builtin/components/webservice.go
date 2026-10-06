package components

import (
	"maps"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// WebserviceRule lowers a "webservice" component (D1 component position,
// oam.ComponentLoweringRule) into a same-name sibling group of terminal
// components: a "deployment", a "service" and, unless `serviceAccountName` is
// authored, a "serviceaccount", in that order, all carrying the component's
// name. It is the re-expression of the former
// WebserviceHandler: webservice was a Deployment, a ClusterIP Service in front
// of its pods, and launcher's own opinions, and `deployment` and `service` are
// the unopinionated projections of those two API kinds, so what webservice
// adds is the opinions — and those are what this rule evaluates. The group
// deploys as one component (pkg/oam sibling groups): one tier, one bundle,
// its objects in the order the handler
// generated them (Deployment, Service, ServiceAccount, claims).
//
// LowerComponent first runs webservice's full parse (parseWebservice, the
// former WebserviceHandler.ToApplicationConfig sequence unchanged), so every
// input the handler refused is still refused, first failure first, with the
// same cause text. It then emits:
//
//   - the deployment member, with the authored properties webservice declares
//     except `port`, `topologySpread` and `affinity`, plus the main
//     container's one port, `{name: http, containerPort: <port>}`. The four-key
//     `affinity` shorthand is evaluated as worker evaluates it (buildAffinity,
//     over the component's own app label) and emitted as the raw corev1 shape
//     `deployment` publishes; `topologySpread` becomes a synthesized
//     `topology-spread` trait in front of the deployment member's traits (none
//     when it is false), the innermost decorator, where the handler applied its
//     constraints;
//   - the service member, with one port `{name: http, port: <port>}` (its
//     `targetPort` defaults to the same number, its protocol to TCP) and the
//     default selector `app: <component name>`, the deployment member's pod
//     labels;
//   - unless `serviceAccountName` is authored, the serviceaccount member
//     (roleServiceAccount): the component's own ServiceAccount, with
//     `automountServiceAccountToken: false` as the handler generated it, and
//     the account's object name as `serviceAccountName` on the deployment
//     member so it generates none of its own.
//
// Every member carries the component's name. The object of each is named on its
// own (go-kure/launcher#787, nameRoleMember): by `deploymentObjectName`,
// `serviceObjectName` or `serviceAccountObjectName`, else by the Naming hook
// (roles oam.NameRoleWorkloadDeployment, oam.NameRoleWorkloadService,
// oam.NameRoleWorkloadServiceAccount), else after the component, so a document
// that sets none generates what it did. The three properties are the rule's and
// reach no member. A name moves the object alone: the labels, the selectors and
// the names the traits derive keep the component's, and what launcher writes to
// the object follows it (the scaler's target, a route's backend, the pods'
// account and the rbac subject). The Service's name is its DNS name in the
// cluster and launcher writes no such address, so an address written with the
// component name is the author's to change. The component name stays held to the
// Service-name rule whatever the Service is named.
//
// Keys webservice does not declare are dropped rather than forwarded, as
// WorkerRule drops them. Annotations go to every member, so a tier override
// places the whole group.
//
// Each authored trait is forwarded by value (it keeps its authored slot, so
// the group applies the traits in authored order across the members) to the
// member whose objects or contracts it acts on:
//
//   - `expose`, `ingress` and `httproute` to the service member: they route to
//     the component's Service and read its port, port name and name;
//   - `prune-protection` and `force-replace` to every member: they cover
//     every object a component generates, by a delivery intent on the
//     application, and the group's one application takes the intent any member
//     has;
//   - every other trait to the deployment member: the workload traits read the
//     pods, the ServiceAccount or the claims, and the delivery traits
//     (`fluxcd-patches`, `fluxcd-postbuild`) configure how the group's one
//     bundle is delivered. Launcher has no handler for those two: a consumer
//     that delivers through Flux registers its own, and it then sees each
//     trait once, on one member, instead of once per member. A trait type this
//     rule does not know — one an extension registered — goes to the
//     deployment member too, the side that holds the pods.
//
// Everything past the parse is the members' own: ApplyPolicy, NonRWXClaim,
// and ServiceAccountName are DeploymentConfig's, and the
// Service port, port name and routing target are ServiceConfig's; the group
// answers each from the one member that has a value. The synthesized inbound
// NetworkPolicy keeps the component label as its pod selector, because the
// service member selects its sibling's pods on ports mapped to themselves. An
// ingress or httproute port addressed by name is translated to the Service
// port's number on the way, as on a `service` component: `portName: http`
// opens the port's number where the handler opened the name `http`, which its
// container port also carried. A parse error surfaces through the lowering
// engine, so it names the component's type and document
// (`component "web" (type "webservice") in document …: <cause>`) instead of the
// former handler's `component "web": <cause>`; the cause text is unchanged.
type WebserviceRule struct{}

// ComponentType claims the "webservice" component type at the component
// lowering position. build.go registers this rule via
// RegisterComponentLowering instead of a dispatchable component handler, so
// "webservice" is reachable only here.
func (WebserviceRule) ComponentType() string { return "webservice" }

// Endpoints implements oam.EndpointProvider: a webservice's in-cluster endpoint is its own pods
// (labelled app=<component name>) on the declared container/service port. This lets a downstream
// platform consumer synthesize generic app→app connections whose target is a webservice, the same
// way it does for a postgresql target. The webservice's single `port` property drives both the
// container port and the Service port (TargetPort == Port), so there is one endpoint per component.
func (WebserviceRule) Endpoints(component *oam.Component) ([]netpol.Endpoint, error) {
	// Endpoints are collected separately from the build, so the rule's own
	// parse runs here too: a component it refuses has no endpoint, in the
	// parse's words.
	opinions, err := parseWebservice(component)
	if err != nil {
		return nil, err
	}
	return []netpol.Endpoint{{
		PodSelector: selectorFrom(appLabels(component.Name)),
		Ports:       []intstr.IntOrString{intstr.FromInt32(opinions.port)},
	}}, nil
}

// PropertySchema declares the webservice component's user-facing properties.
// Unchanged by the move to a lowering rule: HandlerSchemas publishes a rule's
// schema exactly as it publishes a handler's, and
// TestWebserviceRule_PropertySchemaUnchanged pins it byte for byte. The three
// object-name properties (schemaRoleObjectNames) were added since, by
// go-kure/launcher#787.
func (WebserviceRule) PropertySchema() map[string]oam.PropertySchema {
	m := map[string]oam.PropertySchema{
		"image":           {Type: oam.PropertyTypeString, Required: true, Description: "Container image reference for the main container."},
		"port":            {Type: oam.PropertyTypeInteger, Default: 80, Description: "Container port exposed by the Deployment and its ClusterIP Service."},
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
	maps.Copy(m, schemaRoleObjectNames(true))
	return m
}

// webserviceServiceTraits are the authored trait types the rule forwards to the
// service member; roleObjectTraits those it forwards to every member (see
// WebserviceRule).
var webserviceServiceTraits = map[string]bool{"expose": true, "ingress": true, "httproute": true}

// webservicePortName is the name of the main container's one port and of the
// Service's one port.
const webservicePortName = "http"

// webserviceContainerPorts is the main container's one port, as the deployment
// member's parseContainerPorts reads the `ports` the rule emits.
func webserviceContainerPorts(port int32) []corev1.ContainerPort {
	return []corev1.ContainerPort{{Name: webservicePortName, ContainerPort: port, Protocol: corev1.ProtocolTCP}}
}

// LowerComponent validates comp as a webservice and emits the equivalent
// deployment and service components (see WebserviceRule).
func (r WebserviceRule) LowerComponent(comp *oam.Component, lctx oam.LoweringContext) (oam.LoweringResult, error) {
	opinions, err := parseWebservice(comp)
	if err != nil {
		return oam.LoweringResult{}, err
	}

	schema := r.PropertySchema()
	depProps := make(map[string]any, len(comp.Properties))
	for k, v := range comp.Properties {
		if _, declared := schema[k]; declared {
			depProps[k] = v
		}
	}
	delete(depProps, "port")
	delete(depProps, "topologySpread")
	delete(depProps, "affinity")
	dropRoleObjectNames(depProps)
	depProps["ports"] = []any{map[string]any{"name": webservicePortName, "containerPort": int(opinions.port)}}
	if affinity := buildAffinity(opinions.affinity, appLabels(comp.Name)); affinity != nil {
		raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(affinity)
		if err != nil {
			return oam.LoweringResult{}, errors.Wrap(err, "affinity: converting the evaluated shorthand")
		}
		depProps["affinity"] = raw
		// As in WorkerRule: the deployment component validates the raw shape
		// more strictly than the shorthand's own parse does, so the check runs
		// here, where a refusal names the shorthand the author wrote.
		if _, err := parseRawAffinity(depProps); err != nil {
			return oam.LoweringResult{}, errors.Wrap(err, "affinity: the shorthand evaluates to an affinity the API server would refuse")
		}
	}

	claimTraits, err := roleClaims(comp, depProps, lctx)
	if err != nil {
		return oam.LoweringResult{}, err
	}

	var depTraits, svcTraits []oam.Trait
	if !opinions.topologySpreadDisabled {
		depTraits = append(depTraits, oam.Trait{Type: "topology-spread", Properties: map[string]any{}})
	}
	depTraits = append(depTraits, claimTraits...)
	for _, t := range comp.Traits {
		switch {
		case webserviceServiceTraits[t.Type]:
			svcTraits = append(svcTraits, t)
		case roleObjectTraits[t.Type]:
			depTraits = append(depTraits, t)
			svcTraits = append(svcTraits, t)
		default:
			depTraits = append(depTraits, t)
		}
	}

	members := []oam.Component{
		{
			Name:        comp.Name,
			Type:        "deployment",
			Properties:  depProps,
			Traits:      depTraits,
			Annotations: maps.Clone(comp.Annotations),
		},
		{
			Name: comp.Name,
			Type: "service",
			Properties: map[string]any{
				"ports": []any{map[string]any{"name": webservicePortName, "port": int(opinions.port)}},
			},
			Traits:      svcTraits,
			Annotations: maps.Clone(comp.Annotations),
		},
	}
	// Each member's object is named in emission order: the author's property,
	// else the Naming hook, else the component name. Every member keeps the
	// component's name, so the group, the labels and the selectors do not move;
	// what is written as a reference to a renamed object reads the member's
	// object name (the pods' serviceAccountName here, and in the members' own
	// configs the routing backend and the scale target).
	if err := nameRoleMember(comp, lctx, &members[0], &DeploymentHandler{}, oam.NameRoleWorkloadDeployment, deploymentObjectNameProperty, "Deployment"); err != nil {
		return oam.LoweringResult{}, err
	}
	if err := nameRoleMember(comp, lctx, &members[1], &ServiceHandler{}, oam.NameRoleWorkloadService, serviceObjectNameProperty, "Service"); err != nil {
		return oam.LoweringResult{}, err
	}
	sa, err := roleServiceAccount(comp, depProps, comp.Traits, lctx)
	if err != nil {
		return oam.LoweringResult{}, err
	}
	if sa != nil {
		members = append(members, *sa)
	}
	return oam.LoweringResult{Components: members}, nil
}

// webserviceOpinions is what parseWebservice keeps: the properties webservice
// evaluates itself rather than handing to the deployment component.
type webserviceOpinions struct {
	port                   int32
	affinity               AffinityConfig
	topologySpreadDisabled bool
}

// parseWebservice runs the former WebserviceHandler.ToApplicationConfig parse
// over comp, in its original order, so an invalid webservice is refused with
// the same first cause as before. Every other parsed value is discarded here —
// the deployment and service components parse the forwarded properties again,
// with the same helpers. The component name is its Service's name, so it is
// checked against the Service-name rule first (validateComponentServiceName).
func parseWebservice(comp *oam.Component) (webserviceOpinions, error) {
	out := webserviceOpinions{port: 80}
	if err := validateComponentServiceName(comp.Name); err != nil {
		return out, err
	}
	props := comp.Properties

	image, ok := props["image"].(string)
	if !ok {
		return out, errors.New("required property 'image' missing or not a string")
	}
	if err := ValidateImageRef(image); err != nil {
		return out, err
	}
	if p, present, err := parsePortField(props, "port", "port", 1); err != nil {
		return out, err
	} else if present {
		out.port = p
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
	// namedPortsAllowed=true, matchName="http": the main container always
	// declares the one port named "http" (the deployment member's `ports`,
	// unconditional — port defaults to 80), so a probe/lifecycle port resolves
	// only when it names that same "http" port.
	if _, err := parseProbes(props, true, webservicePortName); err != nil {
		return out, errors.Wrap(err, "invalid probe configuration")
	}
	if _, err := parseLifecycle(props, true, webservicePortName); err != nil {
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
	if err := checkPodPortNames(webserviceContainerPorts(out.port), sidecars); err != nil {
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
