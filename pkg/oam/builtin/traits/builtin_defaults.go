package traits

import (
	"fmt"

	"github.com/go-kure/launcher/pkg/oam"
)

// The fixed values a trait handler fills when the properties it is handed hold
// none are reported as built-in fills (oam.BuiltinDefaultReporter,
// go-kure/launcher#1580): the config of the sub-application the trait adds
// names each at the path the author would have written, with the ref
// "<trait>.<path>". The engine reads the report after the policy ran on that
// config and records it under the trait.
//
// A fill is reported exactly where the handler's value stands: the key is left
// out, or holds a value the handler replaces with its own (a null or an empty
// string, where the parser reads one as unset and does not refuse it, and an
// httproute match type that is not a known word). A value a capability rendered
// is in the properties the handler is handed, so it is not a fill.
//
// Not reported: a value a handler derives from the component or from another
// property (an object's name, a backend's service and port, the repository
// Secret of volsync), the fields security-context and topology-spread write
// into the workload, which add no sub-application, and what the expose rule
// fills before it emits its trait.

var (
	_ oam.BuiltinDefaultReporter = (*CertificateConfig)(nil)
	_ oam.BuiltinDefaultReporter = (*ExternalSecretConfig)(nil)
	_ oam.BuiltinDefaultReporter = (*VolsyncConfig)(nil)
	_ oam.BuiltinDefaultReporter = (*ScalerConfig)(nil)
	_ oam.BuiltinDefaultReporter = (*NetworkPolicyConfig)(nil)
	_ oam.BuiltinDefaultReporter = (*IngressConfig)(nil)
	_ oam.BuiltinDefaultReporter = (*HTTPRouteConfig)(nil)
	_ oam.BuiltinDefaultReporter = (*PVCTraitConfig)(nil)
)

// reportFill records in log that the handler of trait filled path.
func reportFill(log *oam.BuiltinDefaultLog, trait, path string) {
	log.ReportBuiltinDefault(path, trait+"."+path)
}

// BuiltinDefaults implements oam.BuiltinDefaultReporter: the CPU target the
// parser set because the author wrote no target at all.
func (c *ScalerConfig) BuiltinDefaults() []oam.BuiltinDefault {
	if !c.cpuFilled {
		return nil
	}
	return []oam.BuiltinDefault{{Path: "cpuUtilization", Ref: "scaler.cpuUtilization"}}
}

// BuiltinDefaults implements oam.BuiltinDefaultReporter: the TCP of each port
// the author gave no protocol.
func (c *NetworkPolicyConfig) BuiltinDefaults() []oam.BuiltinDefault {
	var out []oam.BuiltinDefault
	report := func(direction string, rule int, ports []npPort) {
		for j, port := range ports {
			if port.protocolFilled {
				path := fmt.Sprintf("%s[%d].ports[%d].protocol", direction, rule, j)
				out = append(out, oam.BuiltinDefault{Path: path, Ref: "networkpolicy." + path})
			}
		}
	}
	for i, rule := range c.Ingress {
		report("ingress", i, rule.Ports)
	}
	for i, rule := range c.Egress {
		report("egress", i, rule.Ports)
	}
	return out
}
