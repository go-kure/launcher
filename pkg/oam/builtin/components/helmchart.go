package components

import (
	"strings"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	"github.com/go-kure/kure/pkg/kubernetes/fluxcd"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// HelmchartHandler handles OAM helmchart components.
type HelmchartHandler struct{}

// CanHandle returns true for helmchart component type.
func (h *HelmchartHandler) CanHandle(componentType string) bool {
	return componentType == "helmchart"
}

// ToApplicationConfig converts an OAM helmchart component to a HelmchartConfig.
func (h *HelmchartHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	cfg := &HelmchartConfig{
		Name:      component.Name,
		Namespace: namespace,
	}

	props := component.Properties

	cfg.Chart, _ = props["chart"].(string)
	cfg.Version, _ = props["version"].(string)
	cfg.Delivery, _ = props["delivery"].(string)
	cfg.Interval, _ = props["interval"].(string)
	cfg.ReleaseName, _ = props["releaseName"].(string)
	cfg.TargetNamespace, _ = props["targetNamespace"].(string)

	if dd, ok := props["driftDetection"].(map[string]any); ok {
		mode, _ := dd["mode"].(string)
		switch mode {
		case "enabled", "warn", "disabled":
			cfg.DriftMode = mode
		case "":
			// not configured
		default:
			return nil, errors.Errorf("helmchart: driftDetection.mode %q is invalid; must be enabled, warn, or disabled", mode)
		}
	}
	if install, ok := props["install"].(map[string]any); ok {
		crds, _ := install["crds"].(string)
		switch crds {
		case "Skip", "Create", "CreateReplace":
			cfg.InstallCRDs = crds
		case "":
			// not configured
		default:
			return nil, errors.Errorf("helmchart: install.crds %q is invalid; must be Skip, Create, or CreateReplace", crds)
		}
	}
	if upgrade, ok := props["upgrade"].(map[string]any); ok {
		crds, _ := upgrade["crds"].(string)
		switch crds {
		case "Skip", "Create", "CreateReplace":
			cfg.UpgradeCRDs = crds
		case "":
			// not configured
		default:
			return nil, errors.Errorf("helmchart: upgrade.crds %q is invalid; must be Skip, Create, or CreateReplace", crds)
		}
	}
	if vals, ok := props["values"].(map[string]any); ok {
		cfg.Values = vals
	}
	if vfList, ok := props["valuesFrom"].([]any); ok {
		for i, vf := range vfList {
			m, ok := vf.(map[string]any)
			if !ok {
				return nil, errors.Errorf("valuesFrom[%d]: expected object, got %T", i, vf)
			}
			vfc := fluxcd.ValuesFromConfig{}
			vfc.Kind, _ = m["kind"].(string)
			switch vfc.Kind {
			case "ConfigMap", "Secret":
				// ok
			default:
				return nil, errors.Errorf("valuesFrom[%d]: kind %q is invalid; must be ConfigMap or Secret", i, vfc.Kind)
			}
			vfc.Name, _ = m["name"].(string)
			if vfc.Name == "" {
				return nil, errors.Errorf("valuesFrom[%d]: name is required", i)
			}
			vfc.ValuesKey, _ = m["valuesKey"].(string)
			vfc.TargetPath, _ = m["targetPath"].(string)
			cfg.ValuesFrom = append(cfg.ValuesFrom, vfc)
		}
	}

	// Validate delivery
	switch cfg.Delivery {
	case "", "native":
		// ok
	case "template":
		return nil, errors.New("helmchart: delivery: template is not yet implemented; see issue #83")
	default:
		return nil, errors.Errorf("helmchart: unsupported delivery %q; supported values: native", cfg.Delivery)
	}

	// Parse source block
	src, ok := props["source"].(map[string]any)
	if !ok {
		return nil, errors.New("helmchart: source is required")
	}
	srcURL, _ := src["url"].(string)
	srcName, _ := src["name"].(string)
	srcKind, _ := src["kind"].(string)
	srcNamespace, _ := src["namespace"].(string)

	if srcURL != "" && srcName != "" {
		return nil, errors.New("helmchart: source.url and source.name are mutually exclusive")
	}
	if srcURL == "" && srcName == "" {
		return nil, errors.New("helmchart: source requires either source.url (inline) or source.name (reference)")
	}

	if srcURL != "" {
		// Form A: inline source — launcher creates the source CR
		cfg.SourceURL = srcURL

		if srcKind == "" {
			if strings.HasPrefix(srcURL, "oci://") {
				cfg.SourceKind = "OCIRepository"
			} else {
				cfg.SourceKind = "HelmRepository"
			}
		} else {
			cfg.SourceKind = srcKind
		}

		switch cfg.SourceKind {
		case "HelmRepository":
			if strings.HasPrefix(srcURL, "oci://") {
				return nil, errors.Errorf("helmchart: source.kind HelmRepository is incompatible with oci:// URL")
			}
			if !strings.HasPrefix(srcURL, "https://") && !strings.HasPrefix(srcURL, "http://") {
				return nil, errors.Errorf("helmchart: HelmRepository source.url must start with https:// or http://")
			}
			if cfg.Chart == "" {
				return nil, errors.New("helmchart: source.kind HelmRepository requires chart to be specified")
			}
		case "OCIRepository":
			if !strings.HasPrefix(srcURL, "oci://") {
				return nil, errors.Errorf("helmchart: source.kind OCIRepository requires an oci:// URL")
			}
		default:
			return nil, errors.Errorf("helmchart: source.kind %q is not valid for inline source; must be HelmRepository or OCIRepository", cfg.SourceKind)
		}
	} else {
		// Form B: reference existing source CR
		if srcKind == "" {
			return nil, errors.New("helmchart: source.kind is required when source.name is set")
		}
		switch srcKind {
		case "HelmRepository", "OCIRepository", "HelmChart":
			// ok
		default:
			return nil, errors.Errorf("helmchart: source.kind %q is not valid for source reference; must be HelmRepository, OCIRepository, or HelmChart", srcKind)
		}
		if srcKind == "HelmRepository" && cfg.Chart == "" {
			return nil, errors.New("helmchart: source.kind HelmRepository requires chart to be specified")
		}
		cfg.SourceRefName = srcName
		cfg.SourceRefKind = srcKind
		cfg.SourceRefNamespace = srcNamespace
	}

	return cfg, nil
}

// HelmchartConfig implements stack.ApplicationConfig for helmchart components.
type HelmchartConfig struct {
	Name      string
	Namespace string
	Chart     string
	Version   string
	Delivery  string

	// Form A: inline source — URL is set, launcher creates the source CR.
	SourceURL  string
	SourceKind string // "HelmRepository" or "OCIRepository"

	// Form B: reference — name is set, source CR already exists.
	SourceRefName      string
	SourceRefKind      string // "HelmRepository", "OCIRepository", or "HelmChart"
	SourceRefNamespace string

	// HelmRelease options
	Interval        string
	ReleaseName     string
	TargetNamespace string
	DriftMode       string
	InstallCRDs     string
	UpgradeCRDs     string
	Values          map[string]any
	ValuesFrom      []fluxcd.ValuesFromConfig

	// dedup state (Form A only)
	suppressSource bool
	sharedSrcName  string
}

// ApplyPolicy is a no-op for helmchart (Helm releases have no resource-limit policy).
func (c *HelmchartConfig) ApplyPolicy(_ oam.Policy) error { return nil }

// GetSourceKey returns the dedup key for Form A sources.
// For HelmRepository: "helm:<url>". For OCIRepository: "oci:<url>:<version>".
// Returns "" for Form B (reference) so the dedup loop skips this config.
// First component wins when multiple components share the same source key.
func (c *HelmchartConfig) GetSourceKey() string {
	if c.SourceURL == "" {
		return ""
	}
	if c.SourceKind == "OCIRepository" {
		return "oci:" + c.SourceURL + ":" + c.Version
	}
	return "helm:" + c.SourceURL
}

// GetSourceRefName returns the name to use when referencing this component's source CR.
func (c *HelmchartConfig) GetSourceRefName() string { return c.Name }

// SuppressSourceGeneration instructs this config to skip emitting its own source CR
// and reference the named shared source instead.
func (c *HelmchartConfig) SuppressSourceGeneration(refName string) {
	c.suppressSource = true
	c.sharedSrcName = refName
}

// Generate produces the Kubernetes objects for this helmchart component.
// Output order: source CR (Form A only, unless suppressed by dedup) then HelmRelease.
func (c *HelmchartConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	var objects []*client.Object

	if c.SourceURL != "" {
		// Form A: inline source
		srcName := c.Name
		if c.suppressSource && c.sharedSrcName != "" {
			srcName = c.sharedSrcName
		}

		if !c.suppressSource {
			switch c.SourceKind {
			case "HelmRepository":
				repo := fluxcd.HelmRepository(&fluxcd.HelmRepositoryConfig{
					Name:      c.Name,
					Namespace: c.Namespace,
					URL:       c.SourceURL,
					Interval:  effectiveInterval(c.Interval),
				})
				obj := client.Object(repo)
				objects = append(objects, &obj)
			case "OCIRepository":
				repo := fluxcd.OCIRepository(&fluxcd.OCIRepositoryConfig{
					Name:      c.Name,
					Namespace: c.Namespace,
					URL:       c.SourceURL,
					Ref:       c.Version,
					Interval:  effectiveInterval(c.Interval),
				})
				obj := client.Object(repo)
				objects = append(objects, &obj)
			}
		}

		relCfg := c.baseReleaseConfig()
		switch c.SourceKind {
		case "HelmRepository":
			relCfg.Chart = c.Chart
			relCfg.Version = c.Version
			relCfg.SourceRef = helmv2.CrossNamespaceObjectReference{
				Kind: "HelmRepository",
				Name: srcName,
			}
		case "OCIRepository":
			relCfg.ChartRef = &fluxcd.ChartRefConfig{
				Kind: "OCIRepository",
				Name: srcName,
			}
		}
		hr := fluxcd.HelmRelease(relCfg)
		obj := client.Object(hr)
		objects = append(objects, &obj)
	} else {
		// Form B: reference existing source CR
		relCfg := c.baseReleaseConfig()
		switch c.SourceRefKind {
		case "HelmRepository":
			relCfg.Chart = c.Chart
			relCfg.Version = c.Version
			relCfg.SourceRef = helmv2.CrossNamespaceObjectReference{
				Kind:      "HelmRepository",
				Name:      c.SourceRefName,
				Namespace: c.SourceRefNamespace,
			}
		default: // "OCIRepository" or "HelmChart"
			relCfg.ChartRef = &fluxcd.ChartRefConfig{
				Kind:      c.SourceRefKind,
				Name:      c.SourceRefName,
				Namespace: c.SourceRefNamespace,
			}
		}
		hr := fluxcd.HelmRelease(relCfg)
		obj := client.Object(hr)
		objects = append(objects, &obj)
	}

	return objects, nil
}

func (c *HelmchartConfig) baseReleaseConfig() *fluxcd.HelmReleaseConfig {
	return &fluxcd.HelmReleaseConfig{
		Name:               c.Name,
		Namespace:          c.Namespace,
		Interval:           effectiveInterval(c.Interval),
		ReleaseName:        c.ReleaseName,
		TargetNamespace:    c.TargetNamespace,
		DriftDetectionMode: c.DriftMode,
		InstallCRDs:        c.InstallCRDs,
		UpgradeCRDs:        c.UpgradeCRDs,
		Values:             c.Values,
		ValuesFrom:         c.ValuesFrom,
	}
}

func effectiveInterval(interval string) string {
	if interval == "" {
		return "10m"
	}
	return interval
}
