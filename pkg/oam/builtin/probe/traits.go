package probe

import (
	"slices"

	"github.com/go-kure/launcher/pkg/oam"
)

// withTrait is a deployment with one trait and the capability bindings the
// transform runs with.
func withTrait(name string, trait oam.Trait, caps map[string]oam.CapabilityBinding, fills []Fill) Probe {
	comps := component("app", "deployment", nil)
	comps[0].Traits = []oam.Trait{trait}
	return Probe{
		Name:         name,
		Components:   comps,
		Capabilities: caps,
		Fills:        join([]Fill{replicas("app"), automount("app")}, resourceFills("app", ""), fills),
	}
}

// traitFill is a fill the report holds under a trait, the same in both policy
// states: no policy default reaches a trait's own properties.
func traitFill(trait, path string, value, other any) Fill {
	return Fill{Component: "app", Trait: trait, TraitIndex: 0, Path: path, WithPolicy: oam.SourceBuiltin, WithoutPolicy: oam.SourceBuiltin,
		ValueWithPolicy: value, ValueWithoutPolicy: value, Other: other}
}

// traitProbes are the probes of the traits whose fills the report names. They
// show what these documents trigger. A fill of a trait the documents below do
// not meet is not seen, and a trait with no probe here is not covered.
func traitProbes() []Probe {
	tf := traitFill
	issuer := map[string]oam.CapabilityBinding{"certificate": {Rendering: map[string]any{
		"issuerRef": map[string]any{"name": "letsencrypt"},
	}}}
	peer := map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{"app": "client"}}}
	route := func(match map[string]any) map[string]any {
		return map[string]any{
			"servicePort": 8080,
			"parentRefs":  []any{map[string]any{"name": "gw", "namespace": "infra"}},
			"rules":       []any{map[string]any{"matches": []any{match}}},
		}
	}
	// The scaler owns the replica count, so the deployment reports no replicas
	// and the document refuses an authored one.
	scaler := withTrait("deployment with a scaler", oam.Trait{Type: "scaler", Properties: map[string]any{
		"minReplicas": 2, "maxReplicas": 5,
	}}, nil, []Fill{tf("scaler", "cpuUtilization", 80, 70)})
	scaler.Fills = slices.DeleteFunc(scaler.Fills, func(f Fill) bool { return f.Trait == "" && f.Path == "replicas" })
	scaler.NotAuthorable = map[string]string{"replicas": "owns the replica count"}
	return []Probe{
		withTrait("deployment with a certificate", oam.Trait{Type: "certificate", Properties: map[string]any{
			"secretName": "tls", "dnsNames": []any{"a.example.com"},
		}}, issuer, []Fill{
			tf("certificate", "duration", "2160h", "100h"),
			tf("certificate", "renewBefore", "360h", "10h"),
			// The issuer is rendered without a kind, so the handler fills it. An author
			// writes the kind with a name, which a lone path cannot: no value is stated.
			{Component: "app", Trait: "certificate", Path: "issuerRef.kind", WithPolicy: oam.SourceBuiltin, WithoutPolicy: oam.SourceBuiltin},
		}),
		withTrait("deployment with an external secret", oam.Trait{Type: "external-secret", Properties: map[string]any{
			"secretName":     "db",
			"secretStoreRef": map[string]any{"name": "vault"},
			"data":           []any{map[string]any{"secretKey": "password"}},
		}}, nil, []Fill{
			tf("external-secret", "refreshInterval", "1h", "30m"),
			tf("external-secret", "secretStoreRef.kind", "ClusterSecretStore", "SecretStore"),
		}),
		withTrait("deployment with a volsync backup", oam.Trait{Type: "volsync", Properties: map[string]any{
			"sourcePVC": "data", "schedule": "0 3 * * *",
		}}, nil, []Fill{
			tf("volsync", "copyMethod", "Snapshot", "Direct"),
			tf("volsync", "pruneIntervalDays", 14, 7),
			tf("volsync", "retain.daily", 7, 1),
			tf("volsync", "retain.weekly", 4, 1),
			tf("volsync", "retain.monthly", 3, 1),
		}),
		scaler,
		withTrait("deployment with a network policy", oam.Trait{Type: "networkpolicy", Properties: map[string]any{
			"ingress": []any{map[string]any{"from": []any{peer}, "ports": []any{map[string]any{"port": 8080}}}},
			"egress":  []any{map[string]any{"to": []any{peer}, "ports": []any{map[string]any{"port": 5432}}}},
		}}, nil, []Fill{
			tf("networkpolicy", "ingress[0].ports[0].protocol", "TCP", "UDP"),
			tf("networkpolicy", "egress[0].ports[0].protocol", "TCP", "UDP"),
		}),
		withTrait("deployment with an ingress", oam.Trait{Type: "ingress", Properties: map[string]any{
			"servicePort": 8080,
			"rules":       []any{map[string]any{"host": "a.example.com", "paths": []any{map[string]any{}}}},
		}}, nil, []Fill{
			tf("ingress", "rules[0].paths[0].path", "/", "/api"),
			tf("ingress", "rules[0].paths[0].pathType", "Prefix", "Exact"),
		}),
		withTrait("deployment with an http route", oam.Trait{Type: "httproute", Properties: route(map[string]any{
			"path":    map[string]any{},
			"headers": []any{map[string]any{"name": "x-tenant", "value": "a"}},
		})}, nil, []Fill{
			tf("httproute", "rules[0].matches[0].path.type", "PathPrefix", "Exact"),
			tf("httproute", "rules[0].matches[0].path.value", "/", "/api"),
			tf("httproute", "rules[0].matches[0].headers[0].type", "Exact", "RegularExpression"),
		}),
		// The platform's rendering names the gateway and not its namespace, so the
		// handler fills it. The property is the platform's to set, so no authored value
		// is stated.
		withTrait("deployment with an http route on a platform gateway", oam.Trait{Type: "httproute", Properties: map[string]any{
			"servicePort": 8080, "rules": []any{map[string]any{}},
		}}, map[string]oam.CapabilityBinding{"httproute": {Rendering: map[string]any{"gatewayName": "gw"}}}, []Fill{
			{Component: "app", Trait: "httproute", Path: "gatewayNamespace", WithPolicy: oam.SourceBuiltin, WithoutPolicy: oam.SourceBuiltin},
		}),
		withTrait("deployment with a claim", oam.Trait{Type: "pvc", Properties: map[string]any{
			"name": "data", "size": "1Gi",
		}}, nil, []Fill{tf("pvc", "accessModes", []any{"ReadWriteOnce"}, []any{"ReadWriteMany"})}),
	}
}
