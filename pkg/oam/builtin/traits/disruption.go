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
	hasMin = hasMin && !oam.IsNullValue(min)
	hasMax = hasMax && !oam.IsNullValue(max)
	switch {
	case hasMin && hasMax:
		return nil, errBothBudgets()
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
		return nil, errNoBudget()
	}

	if v, ok := props["unhealthyPodEvictionPolicy"]; ok && !oam.IsNullValue(v) {
		s, isString := v.(string)
		policy := policyv1.UnhealthyPodEvictionPolicyType(s)
		if !isString || checkEvictionPolicy(policy, v) != nil {
			return nil, evictionPolicyError(v)
		}
		config.UnhealthyPodEvictionPolicy = &policy
	}

	if v, ok := props["pdbName"]; ok && !oam.IsNullValue(v) {
		name, isString := v.(string)
		if !isString {
			return nil, errors.Errorf("pdbName must be a string, got %v", v)
		}
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
		if err := checkBudgetPercentage(key, s); err != nil {
			return intstr.IntOrString{}, err
		}
		return intstr.FromString(s), nil
	}
	n, err := oam.IntegerInRange(v, 0, math.MaxInt32)
	if err != nil {
		return intstr.IntOrString{}, errors.Errorf("%s: %w", key, err)
	}
	return intstr.FromInt32(int32(n)), nil //nolint:gosec // bounded to int32 by IntegerInRange
}

// checkBudgetPercentage refuses a budget string that is not a percentage from 0% to
// 100%. parseBudgetValue and DisruptionConfig.validateEffective share it, so a
// string the parser refuses is refused at Generate in the same words.
func checkBudgetPercentage(key, s string) error {
	digits, found := strings.CutSuffix(s, "%")
	if !found || digits == "" || strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return errors.Errorf("%s %q is neither a count nor a percentage: write a whole number, or a percentage such as \"25%%\"", key, s)
	}
	if pct, err := strconv.Atoi(digits); err != nil || pct > 100 {
		return errors.Errorf("%s %q is not a percentage from 0%% to 100%%", key, s)
	}
	return nil
}

// checkBudget is parseBudgetValue's check on a value already typed: a count from 0,
// or a percentage string. A type tag that is neither intstr.Int nor intstr.String is
// refused: the API cannot serialize it.
func checkBudget(key string, v intstr.IntOrString) error {
	switch v.Type {
	case intstr.String:
		return checkBudgetPercentage(key, v.StrVal)
	case intstr.Int:
		if _, err := oam.IntegerInRange(int64(v.IntVal), 0, math.MaxInt32); err != nil {
			return errors.Errorf("%s: %w", key, err)
		}
		return nil
	default:
		return errors.Errorf("%s: invalid IntOrString type %d: write a whole number, or a percentage such as \"25%%\"", key, v.Type)
	}
}

func errBothBudgets() error {
	return errors.Errorf("disruption: set minAvailable or maxUnavailable, not both")
}

func errNoBudget() error {
	return errors.Errorf("disruption: set minAvailable or maxUnavailable; the trait has no default budget")
}

// evictionPolicyError is the refusal for an unhealthyPodEvictionPolicy that is
// neither IfHealthyBudget nor AlwaysAllow; got is the value as it was written.
func evictionPolicyError(got any) error {
	return errors.Errorf("unhealthyPodEvictionPolicy must be %q or %q, got %v", policyv1.IfHealthyBudget, policyv1.AlwaysAllow, got)
}

func checkEvictionPolicy(policy policyv1.UnhealthyPodEvictionPolicyType, got any) error {
	if policy != policyv1.IfHealthyBudget && policy != policyv1.AlwaysAllow {
		return evictionPolicyError(got)
	}
	return nil
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

// validateEffective holds the config to the checks parseProperties makes on the
// properties: exactly one budget, a count or a percentage in range, a known eviction
// policy and a valid object name. Generate runs it so a config built by hand, or
// changed after it was parsed, cannot emit a PodDisruptionBudget the API server
// refuses or one that protects nothing, as ScalerConfig.validateEffective does for
// the scaler. It shares the parser's helpers, so the two cannot disagree on a value
// or on the words that refuse it.
func (c *DisruptionConfig) validateEffective() error {
	switch {
	case c.MinAvailable != nil && c.MaxUnavailable != nil:
		return errBothBudgets()
	case c.MinAvailable != nil:
		if err := checkBudget("minAvailable", *c.MinAvailable); err != nil {
			return err
		}
	case c.MaxUnavailable != nil:
		if err := checkBudget("maxUnavailable", *c.MaxUnavailable); err != nil {
			return err
		}
	default:
		return errNoBudget()
	}
	if c.UnhealthyPodEvictionPolicy != nil {
		if err := checkEvictionPolicy(*c.UnhealthyPodEvictionPolicy, *c.UnhealthyPodEvictionPolicy); err != nil {
			return err
		}
	}
	// The name Generate will use is held to the rule an authored one meets: the one
	// that was set, else the default. A config built by hand has no component name, so
	// its default is "-pdb", which the API server refuses.
	if err := checkAuthoredObjectName("pdbName", "the PodDisruptionBudget", c.effectiveName()); err != nil {
		return err
	}
	return nil
}

// effectiveName is the PodDisruptionBudget name Generate uses: PDBName, else the default.
func (c *DisruptionConfig) effectiveName() string {
	if c.PDBName != "" {
		return c.PDBName
	}
	return c.defaultPDBName()
}

// Generate creates the PodDisruptionBudget.
func (c *DisruptionConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	if err := c.validateEffective(); err != nil {
		return nil, err
	}
	if err := c.refuseScalerBudget(app.Namespace); err != nil {
		return nil, err
	}
	pdb := kubernetes.CreatePodDisruptionBudget(c.effectiveName(), app.Namespace)
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
