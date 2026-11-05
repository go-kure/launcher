package traits

import (
	"math"
	"strconv"
	"strings"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// DisruptionHandler handles OAM disruption traits: a PodDisruptionBudget for a
// workload, without an autoscaler (go-kure/launcher#1266). The scaler trait
// generates a budget too, but only beside a HorizontalPodAutoscaler and only as
// minAvailable 50%; this trait takes the budget from the author and works on the
// kinds a scaler does not scale. The two do not combine: a component that sets
// scaler.enablePDB and has a disruption trait is refused, because it would ask
// for two budgets.
type DisruptionHandler struct{}

// CanHandle returns true for the disruption trait type.
func (h *DisruptionHandler) CanHandle(traitType string) bool {
	return traitType == "disruption"
}

// PropertySchema declares the disruption trait's user-facing properties. The
// budget has no default: a default would be a silent choice.
func (h *DisruptionHandler) PropertySchema() map[string]oam.PropertySchema {
	intOrPercent := []oam.PropertyType{oam.PropertyTypeInteger, oam.PropertyTypeString}
	return map[string]oam.PropertySchema{
		"minAvailable": {
			Types:       intOrPercent,
			Description: "PodDisruptionBudget spec.minAvailable: an eviction is allowed while at least this many of the component's pods stay available, a count or a percentage (\"50%\"). Exactly one of minAvailable and maxUnavailable is required.",
		},
		"maxUnavailable": {
			Types:       intOrPercent,
			Description: "PodDisruptionBudget spec.maxUnavailable: an eviction is allowed while at most this many of the component's pods are unavailable, a count or a percentage (\"25%\"); 0 allows no voluntary eviction. Exactly one of minAvailable and maxUnavailable is required.",
		},
		"unhealthyPodEvictionPolicy": {
			Type:        oam.PropertyTypeString,
			Description: "PodDisruptionBudget spec.unhealthyPodEvictionPolicy: when a running pod that is not yet healthy may be evicted: IfHealthyBudget or AlwaysAllow. The API server reads an unset one as IfHealthyBudget.",
		},
		"pdbName": {
			Type:        oam.PropertyTypeString,
			Description: "Name of the PodDisruptionBudget, used as written or refused; defaults to <component>-pdb.",
		},
	}
}

// ExclusiveProperties implements oam.ExclusivePropertiesProvider: the budget is
// one of the two forms, never both and never neither.
func (h *DisruptionHandler) ExclusiveProperties() []oam.ExclusiveGroup {
	return []oam.ExclusiveGroup{{Keys: []string{"minAvailable", "maxUnavailable"}, Required: true}}
}

// Apply adds the PodDisruptionBudget's sub-application to the bundle. An unknown
// property is refused by name; the engine-owned keys are not the trait's and
// pass.
func (h *DisruptionHandler) Apply(trait *oam.Trait, app *stack.Application, bundle *stack.Bundle) error {
	config, err := h.parseProperties(trait.Properties, app)
	if err != nil {
		return err
	}
	name, err := resolveObjectName(trait, oam.NameRolePDB,
		schema.GroupKind{Group: policyv1.GroupName, Kind: "PodDisruptionBudget"},
		app.Namespace, "pdbName", config.PDBName, config.defaultPDBName())
	if err != nil {
		return err
	}
	config.PDBName = name
	config.bundle = bundle
	subAppName, err := resolveSubApplicationName(trait, app.Name+"-disruption")
	if err != nil {
		return err
	}
	bundle.Applications = append(bundle.Applications, stack.NewApplication(subAppName, app.Namespace, config))
	return nil
}

func (h *DisruptionHandler) parseProperties(props map[string]any, app *stack.Application) (*DisruptionConfig, error) {
	for key := range props {
		switch key {
		case "minAvailable", "maxUnavailable", "unhealthyPodEvictionPolicy", "pdbName":
		default:
			if !oam.IsEngineTraitProperty(key) {
				return nil, errors.Errorf("disruption: unknown property %q; the trait takes minAvailable or maxUnavailable, unhealthyPodEvictionPolicy and pdbName", key)
			}
		}
	}
	config := &DisruptionConfig{componentName: app.Name}

	min, hasMin := props["minAvailable"]
	max, hasMax := props["maxUnavailable"]
	hasMin = hasMin && min != nil
	hasMax = hasMax && max != nil
	switch {
	case hasMin && hasMax:
		return nil, errors.Errorf("disruption: set minAvailable or maxUnavailable, not both")
	case hasMin:
		v, err := parseBudgetValue("minAvailable", min)
		if err != nil {
			return nil, err
		}
		config.MinAvailable = &v
	case hasMax:
		v, err := parseBudgetValue("maxUnavailable", max)
		if err != nil {
			return nil, err
		}
		config.MaxUnavailable = &v
	default:
		return nil, errors.Errorf("disruption: set minAvailable or maxUnavailable; the trait has no default budget")
	}

	if v, ok := props["unhealthyPodEvictionPolicy"]; ok && v != nil {
		s, isString := v.(string)
		policy := policyv1.UnhealthyPodEvictionPolicyType(s)
		if !isString || (policy != policyv1.IfHealthyBudget && policy != policyv1.AlwaysAllow) {
			return nil, errors.Errorf("unhealthyPodEvictionPolicy must be %q or %q, got %v", policyv1.IfHealthyBudget, policyv1.AlwaysAllow, v)
		}
		config.UnhealthyPodEvictionPolicy = &policy
	}

	if name, ok := props["pdbName"].(string); ok {
		if err := checkAuthoredObjectName("pdbName", "the PodDisruptionBudget", name); err != nil {
			return nil, err
		}
		config.PDBName = name
	}
	return config, nil
}

// parseBudgetValue reads a count or a percentage: a whole number from 0, or a
// string of digits and a percent sign from 0% to 100%.
func parseBudgetValue(key string, v any) (intstr.IntOrString, error) {
	if s, ok := v.(string); ok {
		digits, found := strings.CutSuffix(s, "%")
		if !found || digits == "" || strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			return intstr.IntOrString{}, errors.Errorf("%s %q is neither a count nor a percentage: write a whole number, or a percentage such as \"25%%\"", key, s)
		}
		if pct, err := strconv.Atoi(digits); err != nil || pct > 100 {
			return intstr.IntOrString{}, errors.Errorf("%s %q is not a percentage from 0%% to 100%%", key, s)
		}
		return intstr.FromString(s), nil
	}
	n, err := oam.IntegerInRange(v, 0, math.MaxInt32)
	if err != nil {
		return intstr.IntOrString{}, errors.Errorf("%s: %w", key, err)
	}
	return intstr.FromInt32(int32(n)), nil //nolint:gosec // bounded to int32 by IntegerInRange
}

// budgetOwner is implemented by the config of a sub-application that generates
// a PodDisruptionBudget for a component: the scaler's, with enablePDB. The
// disruption trait reads it at Generate to refuse a second budget.
type budgetOwner interface {
	ComponentName() string
	GeneratesPDB() bool
}

// DisruptionConfig implements stack.ApplicationConfig for disruption traits.
type DisruptionConfig struct {
	componentName              string
	MinAvailable               *intstr.IntOrString
	MaxUnavailable             *intstr.IntOrString
	UnhealthyPodEvictionPolicy *policyv1.UnhealthyPodEvictionPolicyType

	// PDBName is the object name, resolved by Apply. "" on a config built
	// directly leaves the default, <component>-pdb, shortened to fit.
	PDBName string

	// bundle is the bundle the trait was applied to. Generate reads the scaler
	// among its applications; the pointer stays valid when the transform
	// reorders the applications.
	bundle *stack.Bundle
}

// ComponentName returns the OAM component this sub-app belongs to, for resource
// provenance attribution.
func (c *DisruptionConfig) ComponentName() string { return c.componentName }

func (c *DisruptionConfig) defaultPDBName() string {
	return oam.ShortenNameWithSuffix(c.componentName, "-pdb", oam.ShortenLimitSubdomain)
}

// refuseScalerBudget refuses the component when a scaler of the same component
// also generates a PodDisruptionBudget. It runs at Generate, when every
// application of the bundle exists and every property is final: a check at
// Apply sees only the traits applied before it, and a check at validation
// misses a scaler's enablePDB that a ClusterProfile rendering sets. The scaler
// is found through oam.UnwrapConfig; a consumer wrapper that is no
// oam.ConfigWrapper hides it, and the conflict is then not seen.
func (c *DisruptionConfig) refuseScalerBudget(namespace string) error {
	if c.bundle == nil {
		return nil
	}
	for _, a := range c.bundle.Applications {
		if a == nil || a.Namespace != namespace {
			continue
		}
		s, ok := oam.UnwrapConfig(a.Config).(budgetOwner)
		if !ok || s.ComponentName() != c.componentName || !s.GeneratesPDB() {
			continue
		}
		return errors.Errorf("component %q has a scaler with enablePDB: true and a disruption trait, and each generates a PodDisruptionBudget; drop one: remove enablePDB from the scaler, or remove the disruption trait", c.componentName)
	}
	return nil
}

// Generate creates the PodDisruptionBudget.
func (c *DisruptionConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	if err := c.refuseScalerBudget(app.Namespace); err != nil {
		return nil, err
	}
	name := c.PDBName
	if name == "" {
		name = c.defaultPDBName()
	}
	pdb := kubernetes.CreatePodDisruptionBudget(name, app.Namespace)
	pdb.Labels = componentLabels(c.componentName)
	pdb.Annotations = nil
	if c.MinAvailable != nil {
		kubernetes.SetPDBMinAvailable(pdb, *c.MinAvailable)
	}
	if c.MaxUnavailable != nil {
		kubernetes.SetPDBMaxUnavailable(pdb, *c.MaxUnavailable)
	}
	if c.UnhealthyPodEvictionPolicy != nil {
		policy := *c.UnhealthyPodEvictionPolicy
		pdb.Spec.UnhealthyPodEvictionPolicy = &policy
	}
	kubernetes.SetPDBSelector(pdb, &metav1.LabelSelector{MatchLabels: componentLabels(c.componentName)})
	obj := client.Object(pdb)
	return []*client.Object{&obj}, nil
}
