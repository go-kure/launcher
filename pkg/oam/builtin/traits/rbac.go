package traits

import (
	"strings"

	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
		"name":        {Type: oam.PropertyTypeString, Description: "Name of every object the trait generates (Role, RoleBinding and, with clusterWide, ClusterRole and ClusterRoleBinding), used as written or refused; defaults to the component name."},
	}
}

// Apply parses the trait properties and appends a new stack.Application carrying
// an rbacTraitConfig to the bundle.
func (h *RBACHandler) Apply(trait *oam.Trait, app *stack.Application, bundle *stack.Bundle) error {
	config, err := h.parseProperties(trait.Properties, app)
	if err != nil {
		return err
	}
	if err := config.resolveNames(trait); err != nil {
		return errors.Wrap(err, "rbac")
	}
	subAppName, err := resolveSubApplicationName(trait, app.Name+"-rbac")
	if err != nil {
		return errors.Wrap(err, "rbac")
	}
	rbacApp := stack.NewApplication(subAppName, app.Namespace, config)
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
		if !ok || ruleMap == nil {
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
	// run as: the authored serviceAccountName (see oam.ServiceAccountNamer).
	// A pod kind without one runs as the namespace's `default` account, which
	// no kind generates (go-kure/launcher#702), so binding rules to it is
	// refused rather than granted to every pod in the namespace. A config
	// that runs no pods keeps the account named after the component.
	serviceAccountName := app.Name
	if namer, ok := app.Config.(oam.ServiceAccountNamer); ok {
		name, runsPods := namer.ServiceAccountName()
		switch {
		case name != "":
			serviceAccountName = name
		case runsPods:
			return nil, errors.Errorf("rbac: component %q runs as no ServiceAccount of its own; set serviceAccountName to the existing ServiceAccount the rules are granted to", app.Name)
		}
	}

	// One authored name for all four objects, used as written or refused
	// (go-kure/launcher#787). The check is the DNS-1123 subdomain rule, as for
	// every other authored name: stricter than the cluster's rule for the RBAC
	// kinds, on purpose (see the README).
	var objectName string
	if name, ok := props["name"].(string); ok {
		if err := checkAuthoredObjectName("name", "the Role, RoleBinding, ClusterRole and ClusterRoleBinding", name); err != nil {
			return nil, errors.Wrap(err, "rbac")
		}
		objectName = name
	}

	return &rbacTraitConfig{
		componentName:      app.Name,
		objectName:         objectName,
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
	if !ok || list == nil {
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
	// objectName is the authored name of every object the trait generates and
	// of both bindings' roleRef; "" leaves them named after the component.
	objectName string
	// resolved holds each object's name as Apply resolved it (resolveNames), by
	// kind: the authored one, else the consumer hook's, else the component's. A
	// kind with no entry (a config built directly) is named by name().
	resolved map[string]string
	// serviceAccountName is the RoleBinding/ClusterRoleBinding subject; it never
	// names the Role/RoleBinding objects themselves.
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

// name is the name of every object the trait generates: the authored one, or
// the component's.
func (c *rbacTraitConfig) name() string {
	if c.objectName != "" {
		return c.objectName
	}
	return c.componentName
}

// rbacKinds are the kinds the trait generates, in generation order; the last
// two only with clusterWide, and cluster-scoped.
var rbacKinds = []string{"Role", "RoleBinding", "ClusterRole", "ClusterRoleBinding"}

// resolveNames resolves the name of each object the trait generates
// (go-kure/launcher#787). The one authored `name` names them all; the consumer
// hook is asked once per object, so it may name each apart. A binding's roleRef
// follows the name its role resolved to.
func (c *rbacTraitConfig) resolveNames(trait *oam.Trait) error {
	c.resolved = make(map[string]string, len(rbacKinds))
	for _, kind := range rbacKinds {
		clusterScoped := strings.HasPrefix(kind, "Cluster")
		if clusterScoped && !c.ClusterWide {
			continue
		}
		groupKind := schema.GroupKind{Group: rbacv1.GroupName, Kind: kind}
		var name string
		var err error
		if clusterScoped {
			name, err = resolveClusterObjectName(trait, oam.NameRoleRBAC, groupKind, "name", c.objectName, c.componentName)
		} else {
			name, err = resolveObjectName(trait, oam.NameRoleRBAC, groupKind, c.Namespace, "name", c.objectName, c.componentName)
		}
		if err != nil {
			return err
		}
		c.resolved[kind] = name
	}
	return nil
}

// nameOf is the name of the trait's object of kind: the resolved one, or name()
// on a config Apply did not build.
func (c *rbacTraitConfig) nameOf(kind string) string {
	if name, ok := c.resolved[kind]; ok {
		return name
	}
	return c.name()
}

func (c *rbacTraitConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	// A label map per object, never one map shared between them: these leave
	// the package on objects a caller owns and edits, and a shared map turns a
	// label added to the Role into a label on the RoleBinding as well.
	role := kubernetes.CreateRole(c.nameOf("Role"), c.Namespace)
	role.Labels = componentLabels(c.componentName)
	role.Annotations = nil
	for _, r := range c.Rules {
		kubernetes.AddRoleRule(role, rbacv1.PolicyRule{
			APIGroups: r.APIGroups,
			Resources: r.Resources,
			Verbs:     r.Verbs,
		})
	}

	rb := kubernetes.CreateRoleBinding(c.nameOf("RoleBinding"), c.Namespace)
	rb.Labels = componentLabels(c.componentName)
	rb.Annotations = nil
	rb.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.GroupName,
		Kind:     "Role",
		Name:     c.nameOf("Role"),
	}
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

	cr := kubernetes.CreateClusterRole(c.nameOf("ClusterRole"))
	cr.Labels = componentLabels(c.componentName)
	cr.Annotations = nil
	for _, r := range c.Rules {
		kubernetes.AddClusterRoleRule(cr, rbacv1.PolicyRule{
			APIGroups: r.APIGroups,
			Resources: r.Resources,
			Verbs:     r.Verbs,
		})
	}

	crb := kubernetes.CreateClusterRoleBinding(c.nameOf("ClusterRoleBinding"))
	crb.Labels = componentLabels(c.componentName)
	crb.Annotations = nil
	crb.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.GroupName,
		Kind:     "ClusterRole",
		Name:     c.nameOf("ClusterRole"),
	}
	kubernetes.AddClusterRoleBindingSubject(crb, rbacv1.Subject{
		Kind:      rbacv1.ServiceAccountKind,
		Name:      c.subjectName(),
		Namespace: c.Namespace,
	})

	crObj := client.Object(cr)
	crbObj := client.Object(crb)
	return append(objects, &crObj, &crbObj), nil
}
