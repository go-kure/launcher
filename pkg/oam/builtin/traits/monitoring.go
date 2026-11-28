package traits

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// The VictoriaMetrics operator's API group and version, and the three kinds the
// monitoring trait writes (operator release v0.76.0). The objects are written
// unstructured: the operator's typed API module pulls the whole VictoriaMetrics
// tree into launcher's go.mod, and monitoring_crd_test.go holds every field the
// trait writes to the release's CRD instead (go-kure/launcher#1736).
const (
	vmAPIGroup   = "operator.victoriametrics.com"
	vmAPIVersion = "v1beta1"
)

var (
	vmServiceScrapeKind = schema.GroupKind{Group: vmAPIGroup, Kind: "VMServiceScrape"}
	vmPodScrapeKind     = schema.GroupKind{Group: vmAPIGroup, Kind: "VMPodScrape"}
	vmRuleKind          = schema.GroupKind{Group: vmAPIGroup, Kind: "VMRule"}
)

// MonitoringHandler handles OAM monitoring traits: the VictoriaMetrics scrape
// of a workload and, when rule groups are given, its VMRule
// (go-kure/launcher#1736). A component with a Service gets a VMServiceScrape
// that selects the Service by the component's `app` label; one without, a
// VMPodScrape that selects its pods by the same label. VictoriaMetrics kinds
// only: launcher supports no native Prometheus backend.
//
// The trait raises no refusal of a policy class (it writes no image, resource,
// storage, capability, host namespace or secret), so its schema declares no
// boundedBy. The object kind policy still holds its objects, and refuses them
// where it does not allow cluster-scoped objects, because the scope of these
// kinds is unknown to the build (go-kure/launcher#1744).
type MonitoringHandler struct{}

// CanHandle returns true for the monitoring trait type.
func (h *MonitoringHandler) CanHandle(traitType string) bool {
	return traitType == "monitoring"
}

// PropertySchema declares the monitoring trait's user-facing properties, each
// named after the VictoriaMetrics field it fills. The selector is not a
// property: it is the component's own `app` label.
func (h *MonitoringHandler) PropertySchema() map[string]oam.PropertySchema {
	stringMap := &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "One value."}
	return map[string]oam.PropertySchema{
		"port": {
			Types:       []oam.PropertyType{oam.PropertyTypeInteger, oam.PropertyTypeString},
			Description: "The port to scrape. A name is the Service's port name on a component with a Service (endpoints[].port), else a container port's name (podMetricsEndpoints[].port); a number from 1 to 65535 is the pod's port (targetPort, or portNumber without a Service). Default on a component with a Service: its Service port. Required on a component without one, which declares no port of its own.",
		},
		"path": {
			Type:        oam.PropertyTypeString,
			Description: "VictoriaMetrics endpoint path (path): where the metrics are served, starting with a slash. Left out, nothing is written and VictoriaMetrics reads /metrics.",
		},
		"interval": {
			Type:        oam.PropertyTypeString,
			Description: "VictoriaMetrics endpoint interval (interval): how often the endpoint is scraped, a duration such as 30s or 1m. Left out, nothing is written.",
		},
		"rules": {
			Type:        oam.PropertyTypeArray,
			Description: "VMRule spec.groups: the rule groups to write. None, or an empty list, writes no VMRule.",
			Items: &oam.PropertySchema{
				Type:        oam.PropertyTypeObject,
				Description: "One rule group (VMRule group).",
				Properties: map[string]oam.PropertySchema{
					"name":     {Type: oam.PropertyTypeString, Required: true, Description: "Group name (name), unique among the groups."},
					"interval": {Type: oam.PropertyTypeString, Description: "How often the group is evaluated (interval), a duration such as 30s."},
					"rules": {
						Type: oam.PropertyTypeArray, Required: true,
						Description: "The group's rules (rules), at least one.",
						Items: &oam.PropertySchema{
							Type:        oam.PropertyTypeObject,
							Description: "One rule: an alert or a recording rule.",
							Exclusive:   []oam.ExclusiveGroup{{Keys: []string{"alert", "record"}, Required: true}},
							Properties: map[string]oam.PropertySchema{
								"alert":       {Type: oam.PropertyTypeString, Description: "Alert name (alert). Exactly one of alert and record."},
								"record":      {Type: oam.PropertyTypeString, Description: "Recorded series name (record). Exactly one of alert and record."},
								"expr":        {Type: oam.PropertyTypeString, Required: true, Description: "The MetricsQL expression (expr). Not parsed here: the operator refuses an invalid one."},
								"for":         {Type: oam.PropertyTypeString, Description: "How long the expression holds before the alert fires (for), a duration such as 5m."},
								"labels":      {Type: oam.PropertyTypeObject, AdditionalPropertiesSchema: stringMap, Description: "Labels added to the alert or the recorded series (labels)."},
								"annotations": {Type: oam.PropertyTypeObject, AdditionalPropertiesSchema: stringMap, Description: "Annotations of the alert (annotations)."},
							},
						},
					},
				},
			},
		},
		"scrapeName": {
			Type:        oam.PropertyTypeString,
			Description: "Name of the VMServiceScrape or VMPodScrape, used as written or refused; defaults to <component>.",
		},
		"ruleName": {
			Type:        oam.PropertyTypeString,
			Description: "Name of the VMRule, used as written or refused; defaults to <component>-rules.",
		},
	}
}

// Apply adds the sub-application that writes the scrape, and the VMRule when
// rule groups are given, to the bundle. An unknown property is refused by name;
// the engine-owned keys are not the trait's and pass.
func (h *MonitoringHandler) Apply(trait *oam.Trait, app *stack.Application, bundle *stack.Bundle) error {
	config, err := h.parseProperties(trait.Properties, app)
	if err != nil {
		return err
	}
	if err := config.readService(app); err != nil {
		return err
	}

	def := oam.ShortenName(app.Name, oam.ShortenLimitSubdomain)
	if config.scrapeName, config.scrapeNameFilled, err = resolveObjectName(trait, oam.NameRoleMonitoringScrape, config.scrapeKind(),
		app.Namespace, "scrapeName", config.authoredScrapeName, def); err != nil {
		return err
	}
	if len(config.groups) > 0 {
		def := oam.ShortenNameWithSuffix(app.Name, "-rules", oam.ShortenLimitSubdomain)
		if config.ruleName, config.ruleNameFilled, err = resolveObjectName(trait, oam.NameRoleMonitoringRule, vmRuleKind,
			app.Namespace, "ruleName", config.authoredRuleName, def); err != nil {
			return err
		}
	}
	subAppName, err := resolveSubApplicationName(trait, oam.ShortenNameWithSuffix(app.Name, "-monitoring", oam.ShortenLimitSubdomain))
	if err != nil {
		return err
	}
	bundle.Applications = append(bundle.Applications, stack.NewApplication(subAppName, app.Namespace, config))
	return nil
}

func (h *MonitoringHandler) parseProperties(props map[string]any, app *stack.Application) (*MonitoringConfig, error) {
	for key := range props {
		switch key {
		case "port", "path", "interval", "rules", "scrapeName", "ruleName":
		default:
			if !oam.IsEngineTraitProperty(key) {
				return nil, errors.Errorf("monitoring: unknown property %q; the trait takes port, path, interval, rules, scrapeName and ruleName", key)
			}
		}
	}
	config := &MonitoringConfig{componentName: app.Name}
	var err error

	if v, ok := props["port"]; ok && !oam.IsNullValue(v) {
		if config.portName, config.portNumber, err = parseMonitoringPort(v); err != nil {
			return nil, err
		}
	}
	if config.path, err = optionalString(props, "path"); err != nil {
		return nil, err
	}
	if config.path != "" && !strings.HasPrefix(config.path, "/") {
		return nil, errors.Errorf("monitoring: path %q must start with a slash, such as /metrics", config.path)
	}
	if config.interval, err = optionalDuration(props, "interval", "interval"); err != nil {
		return nil, err
	}
	if config.groups, err = parseMonitoringGroups(props["rules"]); err != nil {
		return nil, err
	}
	for _, name := range []struct {
		key    string
		object string
		into   *string
	}{
		{"scrapeName", "the scrape object", &config.authoredScrapeName},
		{"ruleName", "the VMRule", &config.authoredRuleName},
	} {
		v, ok := props[name.key]
		if !ok || oam.IsNullValue(v) {
			continue
		}
		s, isString := v.(string)
		if !isString {
			return nil, errors.Errorf("monitoring: %s must be a string, got %v", name.key, v)
		}
		if err := checkAuthoredObjectName(name.key, name.object, s); err != nil {
			return nil, err
		}
		*name.into = s
	}
	return config, nil
}

// parseMonitoringPort reads `port`: a port name, or a number from 1 to 65535.
func parseMonitoringPort(v any) (name string, number int64, err error) {
	if s, ok := v.(string); ok {
		if errs := validation.IsDNS1123Label(s); len(errs) > 0 {
			return "", 0, errors.Errorf("monitoring: port %q is neither a port name nor a number: %s", s, strings.Join(errs, "; "))
		}
		return s, 0, nil
	}
	n, err := oam.IntegerInRange(v, 1, 65535)
	if err != nil {
		return "", 0, errors.Errorf("monitoring: port: %w", err)
	}
	return "", n, nil
}

// optionalString reads a string property the trait takes; absent or null is "".
// An empty string is refused: it is not a way to ask for the default.
func optionalString(props map[string]any, key string) (string, error) {
	v, ok := props[key]
	if !ok || oam.IsNullValue(v) {
		return "", nil
	}
	s, isString := v.(string)
	if !isString || s == "" {
		return "", errors.Errorf("monitoring: %s must be a non-empty string, got %v", key, v)
	}
	return s, nil
}

// optionalDuration reads a duration property: a non-empty string without
// whitespace, such as 30s. Its value is the operator's to read.
func optionalDuration(m map[string]any, key, where string) (string, error) {
	s, err := optionalString(m, key)
	if err != nil {
		return "", errors.Errorf("%s: %w", where, err)
	}
	if strings.IndexFunc(s, unicode.IsSpace) >= 0 {
		return "", errors.Errorf("monitoring: %s %q must be a duration without spaces, such as 30s", where, s)
	}
	return s, nil
}

// monitoringRule is one rule of a group, as the VMRule writes it.
type monitoringRule struct {
	alert, record, expr, forDuration string
	labels, annotations              map[string]any
}

// monitoringGroup is one rule group, as the VMRule writes it.
type monitoringGroup struct {
	name, interval string
	rules          []monitoringRule
}

// parseMonitoringGroups reads `rules`. None, null or an empty list is no group.
func parseMonitoringGroups(v any) ([]monitoringGroup, error) {
	if v == nil || oam.IsNullValue(v) {
		return nil, nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil, errors.Errorf("monitoring: rules must be a list of rule groups, got %v", v)
	}
	var groups []monitoringGroup
	seen := map[string]bool{}
	for i, item := range list {
		where := "rules[" + strconv.Itoa(i) + "]"
		m, ok := item.(map[string]any)
		if !ok {
			return nil, errors.Errorf("monitoring: %s must be an object with name and rules, got %v", where, item)
		}
		if err := checkMonitoringKeys(where, m, "name", "interval", "rules"); err != nil {
			return nil, err
		}
		var g monitoringGroup
		var err error
		if g.name, err = optionalString(m, "name"); err != nil || g.name == "" {
			return nil, errors.Errorf("monitoring: %s.name is required and must be a non-empty string", where)
		}
		if seen[g.name] {
			return nil, errors.Errorf("monitoring: group name %q is used twice in rules; a VMRule's groups are named apart", g.name)
		}
		seen[g.name] = true
		if g.interval, err = optionalDuration(m, "interval", where+".interval"); err != nil {
			return nil, err
		}
		rules, ok := m["rules"].([]any)
		if !ok || len(rules) == 0 {
			return nil, errors.Errorf("monitoring: %s.rules is required and must list at least one rule", where)
		}
		for j, r := range rules {
			rule, err := parseMonitoringRule(where+".rules["+strconv.Itoa(j)+"]", r)
			if err != nil {
				return nil, err
			}
			g.rules = append(g.rules, rule)
		}
		groups = append(groups, g)
	}
	return groups, nil
}

func parseMonitoringRule(where string, v any) (monitoringRule, error) {
	var rule monitoringRule
	m, ok := v.(map[string]any)
	if !ok {
		return rule, errors.Errorf("monitoring: %s must be an object with alert or record, and expr, got %v", where, v)
	}
	if err := checkMonitoringKeys(where, m, "alert", "record", "expr", "for", "labels", "annotations"); err != nil {
		return rule, err
	}
	var err error
	if rule.alert, err = optionalString(m, "alert"); err != nil {
		return rule, err
	}
	if rule.record, err = optionalString(m, "record"); err != nil {
		return rule, err
	}
	if (rule.alert == "") == (rule.record == "") {
		return rule, errors.Errorf("monitoring: %s needs exactly one of alert or record", where)
	}
	if rule.expr, err = optionalString(m, "expr"); err != nil || rule.expr == "" {
		return rule, errors.Errorf("monitoring: %s.expr is required and must be a non-empty string", where)
	}
	if rule.forDuration, err = optionalDuration(m, "for", where+".for"); err != nil {
		return rule, err
	}
	if rule.labels, err = parseStringMap(where+".labels", m["labels"]); err != nil {
		return rule, err
	}
	if rule.annotations, err = parseStringMap(where+".annotations", m["annotations"]); err != nil {
		return rule, err
	}
	return rule, nil
}

// checkMonitoringKeys refuses a key of an object that is not one the trait
// takes there.
func checkMonitoringKeys(where string, m map[string]any, allowed ...string) error {
	for key := range m {
		known := false
		for _, a := range allowed {
			known = known || key == a
		}
		if !known {
			return errors.Errorf("monitoring: %s: unknown property %q; it takes %s", where, key, wordList(allowed))
		}
	}
	return nil
}

// parseStringMap reads a map of string values; absent or null is none.
func parseStringMap(where string, v any) (map[string]any, error) {
	if v == nil || oam.IsNullValue(v) {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.Errorf("monitoring: %s must be a map of strings, got %v", where, v)
	}
	out := make(map[string]any, len(m))
	for k, val := range m {
		s, ok := val.(string)
		if !ok {
			return nil, errors.Errorf("monitoring: %s[%q] must be a string, got %v", where, k, val)
		}
		out[k] = s
	}
	return out, nil
}

// MonitoringConfig implements stack.ApplicationConfig for monitoring traits.
type MonitoringConfig struct {
	componentName string

	portName   string
	portNumber int64
	path       string
	interval   string
	groups     []monitoringGroup

	authoredScrapeName, authoredRuleName string
	scrapeName, ruleName                 string
	scrapeNameFilled, ruleNameFilled     bool

	// hasService says the component generates a Service (servicePortNamer knows
	// its port names), read from the component's config by readService; the
	// Service's port name and number are what `port` defaults to.
	hasService       bool
	servicePortName  string
	servicePortValue int32
}

// readService reads from the component's config whether it has a Service and,
// if so, the port the Service exposes. A component with a Service that has no
// port is refused; one without a Service has `port` required.
func (c *MonitoringConfig) readService(app *stack.Application) error {
	if pn, ok := app.Config.(servicePortNamer); ok {
		if name, known := pn.ServicePortName(); known {
			c.hasService = true
			c.servicePortName = name
			c.servicePortValue = resolveDefaultPort(app)
		}
	}
	switch {
	case c.hasService && c.servicePortValue == 0 && c.portName == "" && c.portNumber == 0:
		return errors.Errorf("monitoring: component %q has a Service with no port, so there is no port to scrape; set port", c.componentName)
	case !c.hasService && c.portName == "" && c.portNumber == 0:
		return errors.Errorf("monitoring: component %q has no Service, so its pods are scraped and port is required: a worker declares no port of its own", c.componentName)
	}
	return nil
}

func (c *MonitoringConfig) scrapeKind() schema.GroupKind {
	if c.hasService {
		return vmServiceScrapeKind
	}
	return vmPodScrapeKind
}

// ComponentName returns the OAM component this sub-app belongs to, for resource
// provenance attribution.
func (c *MonitoringConfig) ComponentName() string { return c.componentName }

// BuiltinDefaults implements oam.BuiltinDefaultReporter: each object name that
// is launcher's default.
func (c *MonitoringConfig) BuiltinDefaults() []oam.BuiltinDefault {
	out := filled(nil, c.scrapeNameFilled, "monitoring", "scrapeName")
	return filled(out, c.ruleNameFilled, "monitoring", "ruleName")
}

// Generate writes the scrape, and the VMRule when rule groups were given.
func (c *MonitoringConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	objs := []*client.Object{c.scrape(app)}
	if len(c.groups) > 0 {
		objs = append(objs, c.vmRule(app))
	}
	return objs, nil
}

func (c *MonitoringConfig) newObject(app *stack.Application, kind schema.GroupKind, name string, spec map[string]any) *client.Object {
	u := &unstructured.Unstructured{Object: map[string]any{"spec": spec}}
	u.SetAPIVersion(vmAPIGroup + "/" + vmAPIVersion)
	u.SetKind(kind.Kind)
	u.SetName(name)
	u.SetNamespace(app.Namespace)
	u.SetLabels(componentLabels(c.componentName))
	obj := client.Object(u)
	return &obj
}

// selector is what the scrape selects: the Service or the pods that carry the
// component's `app` label.
func (c *MonitoringConfig) selector() map[string]any {
	return map[string]any{"matchLabels": map[string]any{"app": componentLabels(c.componentName)["app"]}}
}

// endpoint is one endpoint of the scrape, with the fields of the properties
// the author wrote; a path and an interval left out are left out.
func (c *MonitoringConfig) endpoint() map[string]any {
	ep := map[string]any{}
	switch {
	case c.hasService && c.portName != "":
		ep["port"] = c.portName
	case c.hasService && c.portNumber != 0:
		ep["targetPort"] = c.portNumber
	case c.hasService && c.servicePortName != "":
		ep["port"] = c.servicePortName
	case c.hasService:
		ep["targetPort"] = int64(c.servicePortValue)
	case c.portName != "":
		ep["port"] = c.portName
	default:
		ep["portNumber"] = c.portNumber
	}
	if c.path != "" {
		ep["path"] = c.path
	}
	if c.interval != "" {
		ep["interval"] = c.interval
	}
	return ep
}

func (c *MonitoringConfig) scrape(app *stack.Application) *client.Object {
	if c.hasService {
		return c.newObject(app, vmServiceScrapeKind, c.scrapeName, map[string]any{
			"selector":  c.selector(),
			"endpoints": []any{c.endpoint()},
		})
	}
	return c.newObject(app, vmPodScrapeKind, c.scrapeName, map[string]any{
		"selector":            c.selector(),
		"podMetricsEndpoints": []any{c.endpoint()},
	})
}

func (c *MonitoringConfig) vmRule(app *stack.Application) *client.Object {
	groups := make([]any, 0, len(c.groups))
	for _, g := range c.groups {
		rules := make([]any, 0, len(g.rules))
		for _, r := range g.rules {
			rule := map[string]any{"expr": r.expr}
			setIf(rule, "alert", r.alert)
			setIf(rule, "record", r.record)
			setIf(rule, "for", r.forDuration)
			if len(r.labels) > 0 {
				rule["labels"] = r.labels
			}
			if len(r.annotations) > 0 {
				rule["annotations"] = r.annotations
			}
			rules = append(rules, rule)
		}
		group := map[string]any{"name": g.name, "rules": rules}
		setIf(group, "interval", g.interval)
		groups = append(groups, group)
	}
	return c.newObject(app, vmRuleKind, c.ruleName, map[string]any{"groups": groups})
}

// setIf sets key to value when value is not empty.
func setIf(m map[string]any, key, value string) {
	if value != "" {
		m[key] = value
	}
}
