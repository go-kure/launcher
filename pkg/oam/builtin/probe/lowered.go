package probe

import "github.com/go-kure/launcher/pkg/oam"

// inMember puts fills at the place of a member of a lowered component.
func inMember(member string, fills []Fill) []Fill {
	out := make([]Fill, len(fills))
	for i, f := range fills {
		f.Member = member
		out[i] = f
	}
	return out
}

// loweredProbes are the probes of the types that lower to other components:
// webservice and worker speak at the place of their deployment member, and
// postgresql at the cluster it builds.
func loweredProbes() []Probe {
	deployment := func(comp string, extra ...Fill) []Fill {
		return inMember("deployment", join([]Fill{replicas(comp)}, extra, resourceFills(comp, "")))
	}
	return []Probe{
		{Name: "webservice", Components: component("app", "webservice", nil),
			// The webservice authors one port and not a list, so the protocol the
			// rule fills has no value an author can write.
			Fills:      deployment("app", Fill{Component: "app", Path: "ports[0].protocol", WithPolicy: oam.SourceBuiltin, WithoutPolicy: oam.SourceBuiltin}),
			Unreported: topologyGap()},
		{Name: "worker", Components: component("app", "worker", nil),
			Fills: deployment("app"), Unreported: topologyGap()},
		{Name: "postgresql", Components: []oam.Component{{Name: "db", Type: "postgresql", Properties: map[string]any{}}},
			Fills: join([]Fill{
				{Component: "db", Path: "enablePDB", WithPolicy: oam.SourceBuiltin, WithoutPolicy: oam.SourceBuiltin},
				{Component: "db", Path: "instances", WithPolicy: oam.SourcePolicy},
				{Component: "db", Path: "storage.size", WithPolicy: oam.SourcePolicy, WithoutPolicy: oam.SourceBuiltin},
			}, withPolicyOnly(resourceFills("db", ""))),
			DefaultPlaces: map[string]string{"storageSize": "storage.size", "replicas": "instances"},
			Unreported: map[string]string{
				"provider": "the report holds no entry for it",
				"version":  "the report holds no entry for it",
				"replicas": "the report holds instances only under a policy default; without one the default of 1 is not reported",
			}},
	}
}

// withPolicyOnly keeps the policy side of fills: the document with no policy
// shows none of them.
func withPolicyOnly(fills []Fill) []Fill {
	out := make([]Fill, 0, len(fills))
	for _, f := range fills {
		if f.WithPolicy == "" {
			continue
		}
		f.WithoutPolicy, f.ValueWithoutPolicy = "", nil
		out = append(out, f)
	}
	return out
}

// topologyGap is the one declared default of webservice and worker the report does
// not name: the key switches the topology-spread trait, and trait fills are
// reported by a later step of go-kure/launcher#1580.
func topologyGap() map[string]string {
	return map[string]string{"topologySpread": "the trait the key switches reports no entry yet"}
}
