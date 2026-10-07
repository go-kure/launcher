package components_test

// The fixtures of the Gateway API's routes that carry no HTTP: tcproute,
// udproute and tlsroute (go-kure/launcher#790). The kinds are rows of policyFreeKinds, and the tests
// that hold them are the shared ones.

// gatewayRouteBackend is a backend of a route: a Service of the route's
// namespace, with the port the API requires of one.
func gatewayRouteBackend() map[string]any {
	return map[string]any{"name": "db", "port": 5432}
}

// gatewayRouteWith is the properties of a route with one rule that holds the
// backends.
func gatewayRouteWith(backends ...any) map[string]any {
	return map[string]any{"rules": []any{map[string]any{"backendRefs": append([]any{}, backends...)}}}
}

// gatewayRouteMinimal is the least a tcproute and a udproute may author.
func gatewayRouteMinimal() map[string]any {
	return gatewayRouteWith(gatewayRouteBackend())
}

// gatewayRouteParents is two parents that between them set every field of
// one.
func gatewayRouteParents() []any {
	return []any{
		map[string]any{
			"group": "gateway.networking.k8s.io", "kind": "Gateway", "namespace": "edge",
			"name": "public", "sectionName": "postgres", "port": 5432,
		},
		map[string]any{"name": "internal"},
	}
}

// gatewayRouteBackendsFull is three backends that between them set every
// field of one: a Service of the route's namespace, a Service of another
// namespace that takes no traffic, and an object that is no Service, which
// needs no port.
func gatewayRouteBackendsFull() []any {
	return []any{
		withProperty(gatewayRouteBackend(), "weight", 90),
		map[string]any{"group": "", "kind": "Service", "namespace": "data", "name": "db-replica", "port": 5432, "weight": 0},
		map[string]any{"group": "multicluster.x-k8s.io", "kind": "ServiceImport", "name": "db-remote", "weight": 10},
	}
}

// gatewayRouteFull sets every top-level field of a TCPRouteSpec and of a
// UDPRouteSpec, which hold the same.
func gatewayRouteFull() map[string]any {
	return map[string]any{
		"parentRefs":         gatewayRouteParents(),
		"useDefaultGateways": "All",
		"rules":              []any{map[string]any{"name": "primary", "backendRefs": gatewayRouteBackendsFull()}},
	}
}

// tlsRouteHostnames is the host names of a tlsroute: one name, and one that
// starts with the wildcard label.
func tlsRouteHostnames() []any {
	return []any{"db.example.com", "*.db.example.com"}
}

// tlsRouteMinimal is the least a tlsroute may author: beside the rule of every
// route, the host names the API requires of it.
func tlsRouteMinimal() map[string]any {
	return withProperty(gatewayRouteMinimal(), "hostnames", []any{"db.example.com"})
}

// tlsRouteFull sets every top-level field of a TLSRouteSpec.
func tlsRouteFull() map[string]any {
	return withProperty(gatewayRouteFull(), "hostnames", tlsRouteHostnames())
}
