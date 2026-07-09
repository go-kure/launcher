package traits

import (
	"math"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// ScalerHandler handles OAM scaler traits.
type ScalerHandler struct{}

// CanHandle returns true for scaler trait type.
func (h *ScalerHandler) CanHandle(traitType string) bool {
	return traitType == "scaler"
}

// PropertySchema declares the scaler trait's user-facing properties.
func (h *ScalerHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		// minReplicas/maxReplicas are not schema-required: an EnvironmentPolicy may
		// supply them via DefaultScalerMinReplicas/DefaultScalerMaxReplicas. When
		// neither the trait nor a policy default provides a value, ApplyPolicy errors.
		"minReplicas":       {Type: oam.PropertyTypeInteger, Description: "Minimum replica count for the HorizontalPodAutoscaler; may instead come from an EnvironmentPolicy scaler default."},
		"maxReplicas":       {Type: oam.PropertyTypeInteger, Description: "Maximum replica count for the HorizontalPodAutoscaler; may instead come from an EnvironmentPolicy scaler default."},
		"cpuUtilization":    {Type: oam.PropertyTypeInteger, Default: 80, Description: "Target average CPU utilization percentage (1-100) that triggers scaling."},
		"memoryUtilization": {Type: oam.PropertyTypeInteger, Description: "Target average memory utilization percentage (1-100) that triggers scaling."},
		"enablePDB":         {Type: oam.PropertyTypeBoolean, Default: false, Description: "When true, also generate a PodDisruptionBudget (requires minReplicas >= 2)."},
	}
}

// Apply creates HPA and optionally PDB resources for the component.
func (h *ScalerHandler) Apply(trait *oam.Trait, app *stack.Application, bundle *stack.Bundle) error {
	config, err := h.parseProperties(trait.Properties, app)
	if err != nil {
		return err
	}

	scalerApp := stack.NewApplication(
		app.Name+"-scaler",
		app.Namespace,
		config,
	)
	bundle.Applications = append(bundle.Applications, scalerApp)
	return nil
}

func (h *ScalerHandler) parseProperties(props map[string]any, app *stack.Application) (*ScalerConfig, error) {
	config := &ScalerConfig{
		componentName: app.Name,
	}

	// minReplicas/maxReplicas are optional at parse time: a policy default may
	// supply them. When present they must be whole numbers; the effective-value
	// checks (>=1, max>=min, PDB) run in validateEffective after ApplyPolicy.
	if _, exists := props["minReplicas"]; exists {
		minReplicas, ok := toInt32ForScaler(props["minReplicas"])
		if !ok {
			return nil, errors.New("minReplicas must be a whole number")
		}
		config.MinReplicas = minReplicas
		config.explicitMinReplicas = true
	}

	if _, exists := props["maxReplicas"]; exists {
		maxReplicas, ok := toInt32ForScaler(props["maxReplicas"])
		if !ok {
			return nil, errors.New("maxReplicas must be a whole number")
		}
		config.MaxReplicas = maxReplicas
		config.explicitMaxReplicas = true
	}

	if _, exists := props["cpuUtilization"]; exists {
		cpu, ok := toInt32ForScaler(props["cpuUtilization"])
		if !ok {
			return nil, errors.New("cpuUtilization must be a whole number")
		}
		if cpu < 1 || cpu > 100 {
			return nil, errors.Errorf("cpuUtilization must be between 1 and 100, got %d", cpu)
		}
		config.CPUUtilization = &cpu
	}

	if _, exists := props["memoryUtilization"]; exists {
		mem, ok := toInt32ForScaler(props["memoryUtilization"])
		if !ok {
			return nil, errors.New("memoryUtilization must be a whole number")
		}
		if mem < 1 || mem > 100 {
			return nil, errors.Errorf("memoryUtilization must be between 1 and 100, got %d", mem)
		}
		config.MemoryUtilization = &mem
	}

	if config.CPUUtilization == nil && config.MemoryUtilization == nil {
		defaultCPU := int32(80)
		config.CPUUtilization = &defaultCPU
	}

	if pdb, ok := props["enablePDB"].(bool); ok {
		config.EnablePDB = pdb
	}
	// The enablePDB/minReplicas cross-check runs in validateEffective, since
	// minReplicas may still be filled by a policy default.

	return config, nil
}

// toInt32ForScaler parses v as int32, requiring a whole number.
func toInt32ForScaler(v any) (int32, bool) {
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) {
			return 0, false
		}
		if n < math.MinInt32 || n > math.MaxInt32 {
			return 0, false
		}
		return int32(n), true //nolint:gosec
	case int:
		if n < math.MinInt32 || n > math.MaxInt32 {
			return 0, false
		}
		return int32(n), true //nolint:gosec
	case int32:
		return n, true
	case int64:
		if n < math.MinInt32 || n > math.MaxInt32 {
			return 0, false
		}
		return int32(n), true //nolint:gosec
	default:
		return 0, false
	}
}

// ScalerConfig implements stack.ApplicationConfig for scaler traits.
type ScalerConfig struct {
	componentName     string
	MinReplicas       int32
	MaxReplicas       int32
	CPUUtilization    *int32
	MemoryUtilization *int32
	EnablePDB         bool

	explicitMinReplicas bool
	explicitMaxReplicas bool
}

// ComponentName returns the OAM component this sub-app belongs to, for resource
// provenance attribution.
func (c *ScalerConfig) ComponentName() string { return c.componentName }

// ApplyPolicy fills minReplicas/maxReplicas from the policy defaults when the
// trait omitted them, validates the effective values, and enforces the policy
// replica ceiling. Precedence: authored > policy default > (error, no handler
// default for scaler bounds). A nil policy means no defaults and no caps.
func (c *ScalerConfig) ApplyPolicy(p oam.Policy) error {
	if p != nil {
		c.MinReplicas = applyDefaultReplicas(c.MinReplicas, c.explicitMinReplicas, p.DefaultScalerMinReplicas())
		c.MaxReplicas = applyDefaultReplicas(c.MaxReplicas, c.explicitMaxReplicas, p.DefaultScalerMaxReplicas())
	}
	if err := c.validateEffective(); err != nil {
		return err
	}
	if p != nil {
		if err := enforceMaxReplicas(c.MaxReplicas, p.MaxReplicas()); err != nil {
			return err
		}
	}
	return nil
}

// validateEffective owns all effective-value validation for the scaler: it runs
// after defaults are applied (from ApplyPolicy) and defensively from Generate so
// a config built without ApplyPolicy cannot emit invalid HPA bounds.
func (c *ScalerConfig) validateEffective() error {
	if c.MinReplicas < 1 {
		return errors.Errorf("minReplicas must be >= 1, got %d (set it on the trait or via an EnvironmentPolicy scaler default)", c.MinReplicas)
	}
	if c.MaxReplicas < c.MinReplicas {
		return errors.Errorf("maxReplicas (%d) must be >= minReplicas (%d)", c.MaxReplicas, c.MinReplicas)
	}
	if c.EnablePDB && c.MinReplicas < 2 {
		return errors.Errorf("enablePDB requires minReplicas >= 2 (got %d); with 1 replica PDB blocks all voluntary disruptions", c.MinReplicas)
	}
	return nil
}

// Generate creates HPA and optionally PDB Kubernetes resources.
func (c *ScalerConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	// Defensive: reject invalid effective bounds even if ApplyPolicy was bypassed.
	if err := c.validateEffective(); err != nil {
		return nil, err
	}

	labels := map[string]string{"app": c.componentName}

	var resources []*client.Object

	hpa := c.buildHPA(app, labels)
	hpaObj := client.Object(hpa)
	resources = append(resources, &hpaObj)

	if c.EnablePDB {
		pdb := c.buildPDB(app, labels)
		pdbObj := client.Object(pdb)
		resources = append(resources, &pdbObj)
	}

	return resources, nil
}

func (c *ScalerConfig) buildHPA(app *stack.Application, labels map[string]string) *autoscalingv2.HorizontalPodAutoscaler {
	hpa := kubernetes.CreateHorizontalPodAutoscaler(c.componentName+"-hpa", app.Namespace)
	hpa.Labels = labels
	hpa.Annotations = nil
	kubernetes.SetHPAScaleTargetRef(hpa, "apps/v1", "Deployment", c.componentName)
	kubernetes.SetHPAMinMaxReplicas(hpa, c.MinReplicas, c.MaxReplicas)
	if c.CPUUtilization != nil {
		kubernetes.AddHPACPUMetric(hpa, *c.CPUUtilization)
	}
	if c.MemoryUtilization != nil {
		kubernetes.AddHPAMemoryMetric(hpa, *c.MemoryUtilization)
	}
	return hpa
}

func (c *ScalerConfig) buildPDB(app *stack.Application, labels map[string]string) *policyv1.PodDisruptionBudget {
	pdb := kubernetes.CreatePodDisruptionBudget(c.componentName+"-pdb", app.Namespace)
	pdb.Labels = labels
	pdb.Annotations = nil
	kubernetes.SetPDBMinAvailable(pdb, intstr.FromString("50%"))
	kubernetes.SetPDBSelector(pdb, &metav1.LabelSelector{
		MatchLabels: map[string]string{"app": c.componentName},
	})
	return pdb
}
