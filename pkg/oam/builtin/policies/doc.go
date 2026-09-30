// Package policies implements oam.PolicyHandler for launcher's built-in
// application policy types: dependency, placement, reconciliation and
// health-checks.
//
// A policy handler does not build resources. It reads one entry of an
// Application's spec.policies and records its effect on the shared
// oam.PolicyResult, which the transform pipeline then applies to the cluster
// tree it builds: tier overrides and dependency edges shape the tree,
// health-check overrides and reconciliation settings decorate every leaf
// bundle. Each handler also implements oam.PropertySchemaProvider.
//
// app-dependency is deliberately not a built-in: it orders one application
// after others, and launcher builds a single application, so it has nowhere to
// apply that ordering. A caller that orchestrates several applications
// registers its own handler for it.
package policies
