package oam

import "github.com/go-kure/kure/pkg/stack"

// The functions below let an external test in this directory pass real
// component configs (pkg/oam/builtin/components, which this package cannot
// import) through a sibling group's read sites of ServiceRoutingTarget.

// NewSiblingGroupForTest returns a sibling group's config over the given member
// configs, in order.
func NewSiblingGroupForTest(name, namespace string, members ...stack.ApplicationConfig) stack.ApplicationConfig {
	g := &siblingGroupConfig{}
	for _, m := range members {
		g.members = append(g.members, stack.NewApplication(name, namespace, m))
	}
	return g
}

// SiblingGroupRoutesToOwnPodsForTest is routesToOwnPods of a config
// NewSiblingGroupForTest returned.
func SiblingGroupRoutesToOwnPodsForTest(cfg stack.ApplicationConfig) bool {
	return cfg.(*siblingGroupConfig).routesToOwnPods()
}

// SiblingAnswersForTest is siblingAnswers.
func SiblingAnswersForTest(cfg stack.ApplicationConfig) []string { return siblingAnswers(cfg) }
