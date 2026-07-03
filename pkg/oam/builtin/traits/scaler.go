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
		"minReplicas":       {Type: oam.PropertyTypeInteger, Required: true},
		"maxReplicas":       {Type: oam.PropertyTypeInteger, Required: true},
		"cpuUtilization":    {Type: oam.PropertyTypeInteger, Default: 80},
		"memoryUtilization": {Type: oam.PropertyTypeInteger},
		"enablePDB":         {Type: oam.PropertyTypeBoolean, Default: false},
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
		ComponentName: app.Name,
	}

	minReplicas, ok := toInt32ForScaler(props["minReplicas"])
	if !ok {
		return nil, errors.New("required property 'minReplicas' missing or not a number")
	}
	config.MinReplicas = minReplicas

	maxReplicas, ok := toInt32ForScaler(props["maxReplicas"])
	if !ok {
		return nil, errors.New("required property 'maxReplicas' missing or not a number")
	}
	config.MaxReplicas = maxReplicas

	if config.MinReplicas < 1 {
		return nil, errors.Errorf("minReplicas must be >= 1, got %d", config.MinReplicas)
	}
	if config.MaxReplicas < config.MinReplicas {
		return nil, errors.Errorf("maxReplicas (%d) must be >= minReplicas (%d)", config.MaxReplicas, config.MinReplicas)
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
	if config.EnablePDB && config.MinReplicas < 2 {
		return nil, errors.Errorf("enablePDB requires minReplicas >= 2 (got %d); with 1 replica PDB blocks all voluntary disruptions", config.MinReplicas)
	}

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
	ComponentName     string
	MinReplicas       int32
	MaxReplicas       int32
	CPUUtilization    *int32
	MemoryUtilization *int32
	EnablePDB         bool
}

// ApplyPolicy is a no-op: the scaler trait has no enforceable policy fields.
func (c *ScalerConfig) ApplyPolicy(_ oam.Policy) error { return nil }

// Generate creates HPA and optionally PDB Kubernetes resources.
func (c *ScalerConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	labels := map[string]string{"app": c.ComponentName}

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
	hpa := kubernetes.CreateHorizontalPodAutoscaler(c.ComponentName+"-hpa", app.Namespace)
	hpa.Labels = labels
	hpa.Annotations = nil
	kubernetes.SetHPAScaleTargetRef(hpa, "apps/v1", "Deployment", c.ComponentName)
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
	pdb := kubernetes.CreatePodDisruptionBudget(c.ComponentName+"-pdb", app.Namespace)
	pdb.Labels = labels
	pdb.Annotations = nil
	kubernetes.SetPDBMinAvailable(pdb, intstr.FromString("50%"))
	kubernetes.SetPDBSelector(pdb, &metav1.LabelSelector{
		MatchLabels: map[string]string{"app": c.ComponentName},
	})
	return pdb
}
