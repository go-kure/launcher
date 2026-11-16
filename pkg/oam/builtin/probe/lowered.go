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
			Fills:      join(deployment("app", Fill{Component: "app", Path: "ports[0].protocol", WithPolicy: oam.SourceBuiltin, WithoutPolicy: oam.SourceBuiltin}), []Fill{topologyFill()}),
			Unreported: topologyGap()},
		{Name: "worker", Components: component("app", "worker", nil),
			Fills: join(deployment("app"), []Fill{topologyFill()}), Unreported: topologyGap()},
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

// spreadPath is the Deployment field the topology-spread trait fills, the path
// the report holds under the trait.
const spreadPath = "topologySpreadConstraints"

// topologyFill is the fill of the trait that `topologySpread` switches on
// webservice and worker. The trait writes constraints from two replicas, and
// the document with no policy runs one, so only the document with the policy
// (two replicas) holds it.
func topologyFill() Fill {
	return Fill{Component: "app", Member: "deployment", Trait: "topology-spread", Path: spreadPath, WithPolicy: oam.SourceBuiltin}
}

// topologyGap is the one declared default of webservice and worker that check A
// reads as unreported: with no policy the document has one replica, the trait
// writes nothing at one, and the report holds nothing. topologyFill states the
// entry the document with the policy holds.
func topologyGap() map[string]string {
	return map[string]string{"topologySpread": "at one replica, which the document with no policy has, the trait writes no constraints and the report holds no entry"}
}
