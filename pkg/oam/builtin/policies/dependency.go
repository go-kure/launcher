package policies

import (
	"sort"
	"strings"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// DependencyHandler processes OAM dependency policies.
// A dependency policy specifies deployment ordering between components of the
// same application:
//
//	policies:
//	  - name: deploy-order
//	    type: dependency
//	    properties:
//	      rules:
//	        - component: web
//	          dependsOn: [db]
type DependencyHandler struct{}

// CanHandle returns true for the "dependency" policy type.
func (h *DependencyHandler) CanHandle(policyType string) bool {
	return policyType == "dependency"
}

// Apply records each rule's edges in result.Dependencies. Every referenced
// component must exist in components, a component may not depend on itself, and
// the accumulated graph — including edges recorded by an earlier dependency
// policy on the same result — must be acyclic.
func (h *DependencyHandler) Apply(policy *oam.ApplicationPolicy, components []string, result *oam.PolicyResult) error {
	rules, err := parseDependencyRules(policy.Properties)
	if err != nil {
		return errors.Wrapf(err, "policy %q", policy.Name)
	}

	componentSet := toSet(components)

	for _, rule := range rules {
		if !componentSet[rule.Component] {
			return errors.Errorf("policy %q references unknown component %q", policy.Name, rule.Component)
		}
		for _, dep := range rule.DependsOn {
			if !componentSet[dep] {
				return errors.Errorf("policy %q: component %q depends on unknown component %q",
					policy.Name, rule.Component, dep)
			}
			if dep == rule.Component {
				return errors.Errorf("policy %q: component %q cannot depend on itself",
					policy.Name, rule.Component)
			}
		}
		result.Dependencies[rule.Component] = append(result.Dependencies[rule.Component], rule.DependsOn...)
	}

	if err := detectCycles(result.Dependencies); err != nil {
		return errors.Wrapf(err, "policy %q", policy.Name)
	}

	return nil
}

// PropertySchema declares the dependency policy's property surface. The graph
// constraints the handler additionally enforces — referenced components must
// exist, no self-dependency, no cycles — are not expressible in this vocabulary.
func (h *DependencyHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"rules": {
			Type:        oam.PropertyTypeArray,
			Required:    true,
			Description: "Intra-application ordering rules; each entry names a component and the sibling components it must be deployed after.",
			Items: &oam.PropertySchema{
				Type:        oam.PropertyTypeObject,
				Description: "One ordering rule: a component and the siblings it depends on.",
				Properties: map[string]oam.PropertySchema{
					"component": {Type: oam.PropertyTypeString, Required: true, Description: "Name of the component that has dependencies (must exist in this application)."},
					"dependsOn": {
						Type:        oam.PropertyTypeArray,
						Required:    true,
						Description: "Names of sibling components this component must be deployed after.",
						Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "Name of a sibling component in this application."},
					},
				},
			},
		},
	}
}

// dependencyRule is a single entry in a dependency policy.
type dependencyRule struct {
	Component string
	DependsOn []string
}

// parseDependencyRules extracts dependency rules from untyped policy properties.
func parseDependencyRules(props map[string]any) ([]dependencyRule, error) {
	rawRules, ok := props["rules"]
	if !ok {
		return nil, errors.New("missing required property 'rules'")
	}

	ruleList, ok := rawRules.([]any)
	if !ok {
		return nil, errors.New("property 'rules' must be a list")
	}

	var rules []dependencyRule
	for i, rawRule := range ruleList {
		ruleMap, ok := rawRule.(map[string]any)
		if !ok {
			return nil, errors.Errorf("rules[%d] must be a map", i)
		}

		component, ok := ruleMap["component"].(string)
		if !ok || component == "" {
			return nil, errors.Errorf("rules[%d].component is required and must be a string", i)
		}

		rawDeps, ok := ruleMap["dependsOn"]
		if !ok {
			return nil, errors.Errorf("rules[%d].dependsOn is required", i)
		}

		depList, ok := rawDeps.([]any)
		if !ok {
			return nil, errors.Errorf("rules[%d].dependsOn must be a list", i)
		}

		deps := make([]string, 0, len(depList))
		for j, rawDep := range depList {
			dep, ok := rawDep.(string)
			if !ok || dep == "" {
				return nil, errors.Errorf("rules[%d].dependsOn[%d] must be a non-empty string", i, j)
			}
			deps = append(deps, dep)
		}

		rules = append(rules, dependencyRule{
			Component: component,
			DependsOn: deps,
		})
	}

	return rules, nil
}

// detectCycles checks for circular dependencies using DFS.
//
// The DFS roots are walked in sorted order, and that is load-bearing rather than
// tidiness: the error embeds the traversal that reached the cycle, so the root
// the walk starts from decides which rotation of the cycle the author is shown.
// Ranging the map directly would make `a -> b -> c -> a` come back as any of its
// three rotations across runs of the same input — a diagnostic that changes when
// nothing about the document did. Ordering only the roots is enough: each node's
// dependency list is already a slice, so the rest of the walk is fixed.
func detectCycles(deps map[string][]string) error {
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)

	state := make(map[string]int)

	var visit func(node string, path []string) error
	visit = func(node string, path []string) error {
		if state[node] == visited {
			return nil
		}
		if state[node] == visiting {
			return errors.Errorf("circular dependency detected: %s -> %s",
				formatCyclePath(path), node)
		}

		state[node] = visiting
		path = append(path, node)

		for _, dep := range deps[node] {
			if err := visit(dep, path); err != nil {
				return err
			}
		}

		state[node] = visited
		return nil
	}

	roots := make([]string, 0, len(deps))
	for node := range deps {
		roots = append(roots, node)
	}
	sort.Strings(roots)

	for _, node := range roots {
		if state[node] == unvisited {
			if err := visit(node, nil); err != nil {
				return err
			}
		}
	}

	return nil
}

func formatCyclePath(path []string) string {
	return strings.Join(path, " -> ")
}

// toSet converts a string slice to a set for O(1) lookups.
func toSet(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, item := range items {
		set[item] = true
	}
	return set
}
