package traits

import (
	"maps"
	"slices"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// TopologySpreadHandler handles OAM topology-spread traits.
//
// The trait applies launcher's default topology-spread opinion — the one the
// webservice and worker kinds apply from their `topologySpread` property,
// components.BuildTopologySpreadConstraints — to every typed Deployment the
// component generates (built by a launcher kind, decoded from a manifests
// source or rendered from a helmtemplate chart; a Deployment passed through as
// raw, unstructured output is not inspected). It makes that opinion available
// on a kind that carries
// none of its own (`deployment`), so a document or a lowering rule can ask for
// it explicitly. The trait takes no properties of its own; the engine-owned
// ones every trait accepts (`scope`) are let through.
//
// The replica count is read from the generated Deployment's spec.replicas, so
// it is the count after the environment policy ran: the transformer applies
// the policy to the component config before any trait (pkg/oam transform.go,
// createApplications then applyTraits), and the decorator only sees the object
// at Generate time. That is the same count the role kinds evaluate their own
// opinion against.
type TopologySpreadHandler struct{}

// CanHandle returns true for the "topology-spread" trait type.
func (h *TopologySpreadHandler) CanHandle(traitType string) bool {
	return traitType == "topology-spread"
}

// PropertySchema declares the topology-spread trait's properties. It accepts
// none (it is a pure decorator), so the schema is empty.
func (h *TopologySpreadHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{}
}

// ValidateAndApplyDefaults rejects any key in a ClusterProfile capability
// rendering for this no-rendering trait, at profile evaluation. It is the only
// place a rendered engine-owned key (`scope`) can be told apart from an
// authored one: the rendering is merged into the trait's properties before
// Apply, after the engine already chose the "<type>.<scope>" binding, so a
// rendered `scope` selects nothing and would otherwise build silently.
func (h *TopologySpreadHandler) ValidateAndApplyDefaults(rendering map[string]any) (map[string]any, error) {
	if _, err := builtin.DecodeStrict[builtin.TopologySpreadRendering](rendering); err != nil {
		return nil, errors.Wrap(err, "topology-spread rendering")
	}
	return rendering, nil
}

// Apply wraps app.Config with a topologySpreadConfig decorator. A property is
// refused by name: the schema is empty, and a key that silently did nothing
// would read as a knob the trait does not have. That covers a key merged in
// from a ClusterProfile capability rendering as well as an authored one.
//
// The engine-owned keys (oam.IsEngineTraitProperty — today `scope`, which
// selects the "<type>.<scope>" capability binding) are not the trait's: they
// are legal on every authored trait and the engine has already consumed them
// by the time Apply runs, so they are let through. Apply cannot tell an
// authored `scope` from one merged in from a rendering; a rendered one is
// refused earlier, by ValidateAndApplyDefaults at profile evaluation.
func (h *TopologySpreadHandler) Apply(trait *oam.Trait, app *stack.Application, _ *stack.Bundle) error {
	if trait != nil {
		// Sorted, so a trait with several keys always names the same one.
		for _, key := range slices.Sorted(maps.Keys(trait.Properties)) {
			if !oam.IsEngineTraitProperty(key) {
				return errors.Errorf("topology-spread: unknown property %q; the trait takes no properties", key)
			}
		}
	}
	app.Config = wrapIfAugmenter(
		&topologySpreadConfig{decoratorBase: decoratorBase{Inner: app.Config}},
		app.Config)
	return nil
}

// topologySpreadConfig wraps an ApplicationConfig and stamps the default
// topology spread constraints onto every Deployment its Generate returns.
type topologySpreadConfig struct {
	decoratorBase
}

// Generate delegates to the inner config, then applies the constraints to each
// generated typed *appsv1.Deployment — one a launcher kind builds, or one a
// manifests source or a helmtemplate chart render decodes. A Deployment emitted
// as unstructured output (passthrough) is not inspected, like every other
// Deployment-decorating trait. A component with no typed Deployment is an
// error: the trait would otherwise be accepted and do nothing.
func (c *topologySpreadConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	objects, err := c.Inner.Generate(app)
	if err != nil {
		return nil, err
	}
	found := false
	for _, objPtr := range objects {
		if objPtr == nil {
			continue
		}
		dep, ok := (*objPtr).(*appsv1.Deployment)
		if !ok || dep == nil {
			continue
		}
		found = true
		if err := applyTopologySpread(dep); err != nil {
			return nil, err
		}
	}
	if !found {
		return nil, errors.Errorf("topology-spread: component %q generates no Deployment the trait can act on; it needs a Deployment built by a launcher kind, decoded from a manifests source or rendered from a helmtemplate chart, and a Deployment passed through as raw, unstructured output (passthrough) is not inspected", app.Name)
	}
	return objects, nil
}

// applyTopologySpread sets dep's pod-template topology spread constraints from
// its own replica count and selector.
//
// Constraints already on the pod template are refused rather than merged: they
// come either from the component's raw `topologySpreadConstraints` or from a
// role kind's own `topologySpread` default, and two sources for one field
// would leave the result depending on which a reader assumed won.
//
// The selector labels are the Deployment's own spec.selector.matchLabels —
// for the deployment, webservice and worker kinds that is the same
// `app: <name>` map the role kinds pass to BuildTopologySpreadConstraints. A
// selector that is missing, has no matchLabels, or also carries
// matchExpressions cannot be expressed through that helper without widening
// it, so it is refused rather than approximated. The selector is checked
// before the replica count is consulted: the count comes from the environment
// policy, and a check that only ran once constraints were due would let one
// document build in one environment and fail in another.
func applyTopologySpread(dep *appsv1.Deployment) error {
	if n := len(dep.Spec.Template.Spec.TopologySpreadConstraints); n > 0 {
		return errors.Errorf(
			"topology-spread: Deployment %q already carries %d topologySpreadConstraints (authored on the component, or from its own topologySpread default); remove them, or set topologySpread: false, or drop the trait",
			dep.Name, n)
	}
	sel := dep.Spec.Selector
	if sel == nil || len(sel.MatchLabels) == 0 || len(sel.MatchExpressions) > 0 {
		return errors.Errorf(
			"topology-spread: Deployment %q needs a selector made of matchLabels only to derive the spread selector from",
			dep.Name)
	}
	replicas := int32(1) // the API default for an unset spec.replicas
	if dep.Spec.Replicas != nil {
		replicas = *dep.Spec.Replicas
	}
	// BuildTopologySpreadConstraints copies the map it is given, so the
	// constraints never alias the Deployment's own selector. It returns nil at
	// one replica or fewer, which leaves the field unset.
	dep.Spec.Template.Spec.TopologySpreadConstraints = components.BuildTopologySpreadConstraints(replicas, sel.MatchLabels)
	return nil
}
