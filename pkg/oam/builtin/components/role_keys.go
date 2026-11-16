package components

import (
	"slices"

	"github.com/go-kure/launcher/pkg/oam"
)

// roleDeploymentMember is the component type of the member a role rule
// (webservice, worker) hands its workload properties to.
const roleDeploymentMember = "deployment"

// roleKeys is how a role rule treats the keys of its own schema, held once: the
// rule forwards with it (forward) and publishes it (loweringKeys,
// oam.LoweringKeyDeclarer), so what the rule does and what it declares are one
// decision (forwards) and cannot disagree.
type roleKeys struct {
	schema map[string]oam.PropertySchema
	// evaluated holds the keys the rule reads itself instead of handing them to
	// its deployment member, each with the places what the rule builds from it
	// lands. A key that reaches no property of a member maps to none.
	evaluated map[string][]oam.LoweredKey
}

// forwards reports whether the rule hands key to its deployment member as
// written: a key of its schema that it does not evaluate itself.
func (k roleKeys) forwards(key string) bool {
	_, declared := k.schema[key]
	_, own := k.evaluated[key]
	return declared && !own
}

// forward returns the properties of props the rule hands its deployment member
// as written, in a map of the rule's own.
func (k roleKeys) forward(props map[string]any) map[string]any {
	out := make(map[string]any, len(props))
	for key, v := range props {
		if k.forwards(key) {
			out[key] = v
		}
	}
	return out
}

// loweringKeys is the key map the rule publishes: for every key of its schema,
// the places its value lands. A forwarded key lands at the same key of the
// deployment member, unchanged, except one roleRewrittenKeys names; an
// evaluated key lands where evaluated says.
func (k roleKeys) loweringKeys() map[string][]oam.LoweredKey {
	out := make(map[string][]oam.LoweredKey, len(k.schema))
	for key := range k.schema {
		switch places, rewritten := roleRewrittenKeys[key]; {
		case !k.forwards(key):
			out[key] = slices.Clone(k.evaluated[key])
		case rewritten:
			out[key] = slices.Clone(places)
		default:
			out[key] = []oam.LoweredKey{{Member: roleDeploymentMember, Path: key, Unchanged: true}}
		}
	}
	return out
}

// roleRewrittenKeys holds the keys a role rule forwards and then rewrites in
// the deployment member's properties, with the places the result lands.
// `volumes` is the one: roleClaims replaces each `pvc` volume that generates a
// claim by a reference to it and moves the claim into a `pvc` trait, so the
// list is no longer the authored one.
var roleRewrittenKeys = map[string][]oam.LoweredKey{
	"volumes": {
		{Member: roleDeploymentMember, Path: "volumes"},
		{Member: roleDeploymentMember, Trait: "pvc"},
	},
}

// roleEvaluatedKeys is the evaluated set of a role rule: the spread switch, the
// affinity shorthand and the object names, and for a type that generates a
// Service (service) its `port` and the Service's object name. An object name
// reaches a member's properties only where another member refers to the object.
func roleEvaluatedKeys(service bool) map[string][]oam.LoweredKey {
	m := map[string][]oam.LoweredKey{
		// Switches the topology-spread trait the rule attaches, which holds no
		// property.
		"topologySpread": {{Member: roleDeploymentMember, Trait: "topology-spread"}},
		// The four-key shorthand, evaluated to a Kubernetes affinity under the
		// same key.
		"affinity": {{Member: roleDeploymentMember, Path: "affinity"}},
		// Names the Deployment the member generates; it is no property of a
		// member.
		deploymentObjectNameProperty: nil,
		// Names the ServiceAccount the component generates, and the deployment's
		// pods run as that account: roleServiceAccount writes the resolved name
		// there, which is the Naming hook's answer or the component name when the
		// key is left out.
		serviceAccountObjectNameProperty: {{Member: roleDeploymentMember, Path: "serviceAccountName"}},
	}
	if service {
		m["port"] = []oam.LoweredKey{
			{Member: roleDeploymentMember, Path: "ports[0].containerPort"},
			{Member: "service", Path: "ports[0].port"},
		}
		m[serviceObjectNameProperty] = nil
	}
	return m
}

// keys is the key split of the webservice rule.
func (r WebserviceRule) keys() roleKeys {
	return roleKeys{schema: r.PropertySchema(), evaluated: roleEvaluatedKeys(true)}
}

// keys is the key split of the worker rule.
func (r WorkerRule) keys() roleKeys {
	return roleKeys{schema: r.PropertySchema(), evaluated: roleEvaluatedKeys(false)}
}

// LoweringKeys implements oam.LoweringKeyDeclarer: where each property of a
// webservice lands in its deployment and service members.
func (r WebserviceRule) LoweringKeys() map[string][]oam.LoweredKey { return r.keys().loweringKeys() }

// LoweringKeys implements oam.LoweringKeyDeclarer: where each property of a
// worker lands in its deployment member.
func (r WorkerRule) LoweringKeys() map[string][]oam.LoweredKey { return r.keys().loweringKeys() }
