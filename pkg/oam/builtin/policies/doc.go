// Package policies implements oam.PolicyHandler for launcher's built-in
// application policy types: dependency and placement.
//
// A policy handler does not build resources. It reads one entry of an
// Application's spec.policies and records its effect on the shared
// oam.PolicyResult, which the transform pipeline then applies to the cluster
// tree it builds: tier overrides and dependency edges shape the tree. Each
// handler also implements oam.PropertySchemaProvider.
//
// Policies that configure delivery (health-checks, reconciliation) are not
// built-ins: launcher sets no delivery field on a bundle. A caller that
// delivers the application registers its own handlers for them and records
// what they say in oam.PolicyResult.Extensions.
//
// app-dependency is deliberately not a built-in: it orders one application
// after others, and launcher builds a single application, so it has nowhere to
// apply that ordering. A caller that orchestrates several applications
// registers its own handler for it.
package policies
