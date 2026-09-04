package traits

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// RBACHandler handles OAM rbac traits, generating a Role + RoleBinding scoped
// to the component's namespace, with an optional ClusterRole + ClusterRoleBinding
// for cluster-wide permissions.
type RBACHandler struct{}

// CanHandle returns true for the rbac trait type.
func (h *RBACHandler) CanHandle(traitType string) bool {
	return traitType == "rbac"
}

// PropertySchema declares the rbac trait's user-facing properties. Each rule is a
// closed K8s-PolicyRule-shaped object: only the enumerated fields are accepted and
// unknown keys are rejected. `resources` and `verbs` are required.
func (h *RBACHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"rules": {
			Type:        oam.PropertyTypeArray,
			Required:    true,
			Description: "Policy rules granted to the component's ServiceAccount.",
			Items: &oam.PropertySchema{
				Type:                 oam.PropertyTypeObject,
				AdditionalProperties: false,
				Description:          "A single RBAC policy rule.",
				Properties: map[string]oam.PropertySchema{
					"apiGroups": {Type: oam.PropertyTypeArray, Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "An API group the rule applies to."}, Description: "API groups the rule applies to."},
					"resources": {Type: oam.PropertyTypeArray, Required: true, Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A resource type the rule applies to."}, Description: "Resource types the rule applies to."},
					"verbs":     {Type: oam.PropertyTypeArray, Required: true, Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A verb the rule permits."}, Description: "Verbs the rule permits on the listed resources."},
				},
			},
		},
		"clusterWide": {Type: oam.PropertyTypeBoolean, Description: "When true, also generate a ClusterRole and ClusterRoleBinding for cluster-wide permissions."},
	}
}

// Apply parses the trait properties and appends a new stack.Application carrying
// an rbacTraitConfig to the bundle.
func (h *RBACHandler) Apply(trait *oam.Trait, app *stack.Application, bundle *stack.Bundle) error {
	config, err := h.parseProperties(trait.Properties, app)
	if err != nil {
		return err
	}
	rbacApp := stack.NewApplication(app.Name+"-rbac", app.Namespace, config)
	bundle.Applications = append(bundle.Applications, rbacApp)
	return nil
}

func (h *RBACHandler) parseProperties(props map[string]any, app *stack.Application) (*rbacTraitConfig, error) {
	rawRules, ok := props["rules"].([]any)
	if !ok || len(rawRules) == 0 {
		return nil, errors.New("rbac: required property 'rules' missing or empty")
	}

	rules := make([]rbacRule, 0, len(rawRules))
	for i, raw := range rawRules {
		ruleMap, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.Errorf("rbac: rules[%d]: expected object", i)
		}
		rule, err := parseRBACRule(ruleMap, i)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}

	var clusterWide bool
	if v, ok := props["clusterWide"].(bool); ok {
		clusterWide = v
	}

	// The binding subject is the ServiceAccount the component's pods actually
	// run as: an authored serviceAccountName when the component set one (see
	// oam.ServiceAccountNamer), else the per-component account named after
	// the component.
	serviceAccountName := app.Name
	if namer, ok := app.Config.(oam.ServiceAccountNamer); ok {
		if name := namer.ServiceAccountName(); name != "" {
			serviceAccountName = name
		}
	}

	return &rbacTraitConfig{
		componentName:      app.Name,
		serviceAccountName: serviceAccountName,
		Namespace:          app.Namespace,
		Rules:              rules,
		ClusterWide:        clusterWide,
	}, nil
}

func parseRBACRule(m map[string]any, idx int) (rbacRule, error) {
	apiGroups, err := parseRBACStringSlice(m, "apiGroups", idx)
	if err != nil {
		return rbacRule{}, err
	}
	resources, err := parseRBACStringSlice(m, "resources", idx)
	if err != nil {
		return rbacRule{}, err
	}
	if len(resources) == 0 {
		return rbacRule{}, errors.Errorf("rbac: rules[%d].resources must not be empty", idx)
	}
	verbs, err := parseRBACStringSlice(m, "verbs", idx)
	if err != nil {
		return rbacRule{}, err
	}
	if len(verbs) == 0 {
		return rbacRule{}, errors.Errorf("rbac: rules[%d].verbs must not be empty", idx)
	}
	return rbacRule{APIGroups: apiGroups, Resources: resources, Verbs: verbs}, nil
}

func parseRBACStringSlice(m map[string]any, field string, ruleIdx int) ([]string, error) {
	raw, ok := m[field]
	if !ok {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, errors.Errorf("rbac: rules[%d].%s: expected array", ruleIdx, field)
	}
	result := make([]string, 0, len(list))
	for j, v := range list {
		s, ok := v.(string)
		if !ok {
			return nil, errors.Errorf("rbac: rules[%d].%s[%d]: expected string", ruleIdx, field, j)
		}
		result = append(result, s)
	}
	return result, nil
}

type rbacRule struct {
	APIGroups []string
	Resources []string
	Verbs     []string
}

type rbacTraitConfig struct {
	componentName string
	// serviceAccountName is the RoleBinding/ClusterRoleBinding subject; the
	// Role/RoleBinding objects themselves stay named after the component.
	serviceAccountName string
	Namespace          string
	Rules              []rbacRule
	ClusterWide        bool
}

// ComponentName returns the OAM component this sub-app belongs to, for resource
// provenance attribution.
func (c *rbacTraitConfig) ComponentName() string { return c.componentName }

// subjectName is the ServiceAccount the bindings grant to; a config built
// directly (tests, older callers) without serviceAccountName falls back to the
// component name, the behaviour before go-kure/launcher#342.
func (c *rbacTraitConfig) subjectName() string {
	if c.serviceAccountName != "" {
		return c.serviceAccountName
	}
	return c.componentName
}

func (c *rbacTraitConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	// A label map per object, never one map shared between them: these leave
	// the package on objects a caller owns and edits, and a shared map turns a
	// label added to the Role into a label on the RoleBinding as well.
	role := kubernetes.CreateRole(c.componentName, c.Namespace)
	role.Labels = map[string]string{"app": c.componentName}
	role.Annotations = nil
	for _, r := range c.Rules {
		kubernetes.AddRoleRule(role, rbacv1.PolicyRule{
			APIGroups: r.APIGroups,
			Resources: r.Resources,
			Verbs:     r.Verbs,
		})
	}

	rb := kubernetes.CreateRoleBinding(c.componentName, c.Namespace)
	rb.Labels = map[string]string{"app": c.componentName}
	rb.Annotations = nil
	kubernetes.SetRoleBindingRoleRef(rb, rbacv1.RoleRef{
		APIGroup: rbacv1.GroupName,
		Kind:     "Role",
		Name:     c.componentName,
	})
	kubernetes.AddRoleBindingSubject(rb, rbacv1.Subject{
		Kind:      rbacv1.ServiceAccountKind,
		Name:      c.subjectName(),
		Namespace: c.Namespace,
	})

	roleObj := client.Object(role)
	rbObj := client.Object(rb)
	objects := []*client.Object{&roleObj, &rbObj}

	if !c.ClusterWide {
		return objects, nil
	}

	cr := kubernetes.CreateClusterRole(c.componentName)
	cr.Labels = map[string]string{"app": c.componentName}
	cr.Annotations = nil
	for _, r := range c.Rules {
		kubernetes.AddClusterRoleRule(cr, rbacv1.PolicyRule{
			APIGroups: r.APIGroups,
			Resources: r.Resources,
			Verbs:     r.Verbs,
		})
	}

	crb := kubernetes.CreateClusterRoleBinding(c.componentName)
	crb.Labels = map[string]string{"app": c.componentName}
	crb.Annotations = nil
	kubernetes.SetClusterRoleBindingRoleRef(crb, rbacv1.RoleRef{
		APIGroup: rbacv1.GroupName,
		Kind:     "ClusterRole",
		Name:     c.componentName,
	})
	kubernetes.AddClusterRoleBindingSubject(crb, rbacv1.Subject{
		Kind:      rbacv1.ServiceAccountKind,
		Name:      c.subjectName(),
		Namespace: c.Namespace,
	})

	crObj := client.Object(cr)
	crbObj := client.Object(crb)
	return append(objects, &crObj, &crbObj), nil
}
