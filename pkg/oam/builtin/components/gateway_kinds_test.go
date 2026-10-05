package components_test

// The fixtures of the Gateway API's infrastructure kinds: gatewayclass,
// gateway, listenerset, referencegrant and backendtlspolicy
// (go-kure/launcher#790). The kinds are rows of policyFreeKinds, and the
// tests that hold them are the shared ones.

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// gatewayGVK is the group, version and kind of an object of the Gateway API's
// v1. The module's GroupVersion is the metav1 type, which builds none.
func gatewayGVK(kind string) schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: gatewayv1.GroupVersion.Group, Version: gatewayv1.GroupVersion.Version, Kind: kind}
}

// gatewayListener is the least a listener of a Gateway, and of a ListenerSet,
// may author.
func gatewayListener() map[string]any {
	return map[string]any{"name": "http", "port": 80, "protocol": "HTTP"}
}

// gatewayListenerWith is gatewayListener with one more field.
func gatewayListenerWith(name string, value any) map[string]any {
	return withProperty(gatewayListener(), name, value)
}

// gatewayWith is the properties of a gateway with the listeners.
func gatewayWith(listeners ...any) map[string]any {
	return map[string]any{"gatewayClassName": "public", "listeners": append([]any{}, listeners...)}
}

// gatewayMinimal is the least a gateway may author.
func gatewayMinimal() map[string]any {
	return gatewayWith(gatewayListener())
}

// gatewayTLS is gatewayMinimal with the Gateway-wide TLS configuration.
func gatewayTLS(tls map[string]any) map[string]any {
	return withProperty(gatewayMinimal(), "tls", tls)
}

// gatewayCARef is a reference to a ConfigMap of CA certificates.
func gatewayCARef() map[string]any {
	return map[string]any{"group": "", "kind": "ConfigMap", "name": "client-ca"}
}

// gatewayListenersFull is three listeners that between them set every field of
// one, with an authored empty hostname left out: the type omits it.
func gatewayListenersFull() []any {
	return []any{
		gatewayListener(),
		map[string]any{
			"name": "https", "port": 443, "protocol": "HTTPS", "hostname": "*.example.com",
			"tls": map[string]any{
				"mode": "Terminate",
				"certificateRefs": []any{
					map[string]any{"name": "wildcard-tls"},
					map[string]any{"group": "", "kind": "Secret", "name": "shop-tls", "namespace": "certs"},
				},
				"options": map[string]any{"example.com/min-version": "1.3"},
			},
			"allowedRoutes": map[string]any{
				"namespaces": map[string]any{
					"from":     "Selector",
					"selector": map[string]any{"matchLabels": map[string]any{"gateway": "public"}},
				},
				"kinds": []any{map[string]any{"kind": "HTTPRoute"}, map[string]any{"group": "gateway.networking.k8s.io", "kind": "GRPCRoute"}},
			},
		},
		map[string]any{
			"name": "passthrough", "port": 8443, "protocol": "TLS",
			"tls": map[string]any{"mode": "Passthrough"},
		},
	}
}

// gatewayFull sets every top-level field of a GatewaySpec.
func gatewayFull() map[string]any {
	return map[string]any{
		"gatewayClassName": "public",
		"listeners":        gatewayListenersFull(),
		"addresses": []any{
			map[string]any{"type": "IPAddress", "value": "192.0.2.10"},
			map[string]any{"type": "Hostname", "value": "gateway.example.com"},
			map[string]any{"value": "192.0.2.11"},
		},
		"infrastructure": map[string]any{
			"labels":        map[string]any{"team": "edge"},
			"annotations":   map[string]any{"example.com/load-balancer": "internal"},
			"parametersRef": map[string]any{"group": "", "kind": "ConfigMap", "name": "gateway-config"},
		},
		"allowedListeners": map[string]any{"namespaces": map[string]any{
			"from":     "Selector",
			"selector": map[string]any{"matchLabels": map[string]any{"listeners": "allowed"}},
		}},
		"tls": map[string]any{
			"backend": map[string]any{"clientCertificateRef": map[string]any{"name": "gateway-client"}},
			"frontend": map[string]any{
				"default": map[string]any{"validation": map[string]any{
					"caCertificateRefs": []any{gatewayCARef()}, "mode": "AllowValidOnly",
				}},
				"perPort": []any{
					map[string]any{"port": 8443, "tls": map[string]any{"validation": map[string]any{
						"caCertificateRefs": []any{withProperty(gatewayCARef(), "namespace", "certs")},
						"mode":              "AllowInsecureFallback",
					}}},
					// No validation on a port: its listeners ask for no client certificate.
					map[string]any{"port": 9443, "tls": map[string]any{}},
				},
			},
		},
		"defaultScope": "All",
	}
}

// gatewayClassFull sets every top-level field of a GatewayClassSpec.
func gatewayClassFull() map[string]any {
	return map[string]any{
		"controllerName": "example.net/gateway-controller",
		"parametersRef":  map[string]any{"group": "example.net", "kind": "GatewayConfig", "name": "public", "namespace": "gateway-system"},
		"description":    "Internet-facing gateways",
	}
}

// listenerSetWith is the properties of a listenerset with the listeners.
func listenerSetWith(listeners ...any) map[string]any {
	return map[string]any{"parentRef": map[string]any{"name": "public"}, "listeners": append([]any{}, listeners...)}
}

// listenerSetFull sets every top-level field of a ListenerSetSpec. Its
// listeners take the fields of a Gateway's.
func listenerSetFull() map[string]any {
	return map[string]any{
		"parentRef": map[string]any{"group": "gateway.networking.k8s.io", "kind": "Gateway", "name": "public", "namespace": "edge"},
		"listeners": gatewayListenersFull(),
	}
}

// referenceGrantWith is the properties of a referencegrant with one source and
// the targets.
func referenceGrantWith(from map[string]any, to ...any) map[string]any {
	return map[string]any{"from": []any{from}, "to": append([]any{}, to...)}
}

// referenceGrantFrom is a source of references: the HTTPRoutes of a namespace.
func referenceGrantFrom() map[string]any {
	return map[string]any{"group": "gateway.networking.k8s.io", "kind": "HTTPRoute", "namespace": "shop"}
}

// referenceGrantMinimal is the least a referencegrant may author.
func referenceGrantMinimal() map[string]any {
	return referenceGrantWith(referenceGrantFrom(), map[string]any{"group": "", "kind": "Service"})
}

// referenceGrantFull sets every top-level field of a ReferenceGrantSpec, and
// every field of a source and of a target.
func referenceGrantFull() map[string]any {
	return map[string]any{
		"from": []any{
			referenceGrantFrom(),
			map[string]any{"group": "gateway.networking.k8s.io", "kind": "Gateway", "namespace": "edge"},
		},
		"to": []any{
			map[string]any{"group": "", "kind": "Service"},
			map[string]any{"group": "", "kind": "Secret", "name": "wildcard-tls"},
		},
	}
}

// backendTLSTarget is a reference to a Service the policy applies to.
func backendTLSTarget() map[string]any {
	return map[string]any{"group": "", "kind": "Service", "name": "payments"}
}

// backendTLSPolicyWith is the properties of a backendtlspolicy with one target
// and the validation.
func backendTLSPolicyWith(validation map[string]any) map[string]any {
	return map[string]any{"targetRefs": []any{backendTLSTarget()}, "validation": validation}
}

// backendTLSValidation is the least a validation may author, with one more
// field.
func backendTLSValidation(name string, value any) map[string]any {
	return map[string]any{"hostname": "payments.internal.example.com", name: value}
}

// backendTLSPolicyMinimal is the least a backendtlspolicy may author. The API
// wants one of caCertificateRefs and wellKnownCACertificates too, by a rule it
// writes as an expression, which the kind leaves to it.
func backendTLSPolicyMinimal() map[string]any {
	return backendTLSPolicyWith(backendTLSValidation("wellKnownCACertificates", "System"))
}

// backendTLSPolicyFull sets every top-level field of a BackendTLSPolicySpec.
func backendTLSPolicyFull() map[string]any {
	return map[string]any{
		"targetRefs": []any{
			backendTLSTarget(),
			withProperty(backendTLSTarget(), "sectionName", "https"),
		},
		"validation": map[string]any{
			"hostname":          "payments.internal.example.com",
			"caCertificateRefs": []any{map[string]any{"group": "", "kind": "ConfigMap", "name": "internal-ca"}},
			"subjectAltNames": []any{
				map[string]any{"type": "Hostname", "hostname": "payments.internal.example.com"},
				map[string]any{"type": "URI", "uri": "spiffe://example.com/payments"},
			},
		},
		"options": map[string]any{"example.com/min-version": "1.3"},
	}
}
