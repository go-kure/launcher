package traits

import (
	"fmt"

	"github.com/go-kure/launcher/pkg/oam"
)

// The values a trait handler fills when the properties it is handed hold none
// are reported as built-in fills (oam.BuiltinDefaultReporter,
// go-kure/launcher#1580): the config of the sub-application the trait adds
// names each at the path the author would have written, with the ref
// "<trait>.<path>". The engine reads the report after the policy ran on that
// config and records it under the trait. A fill is a fixed value, or one the
// handler computes from the component or from another property (an object's
// name, a backend's service and port, the repository Secret of volsync).
//
// A fill is reported exactly where the handler's value stands: the key is left
// out, or holds a value the handler replaces with its own (a null or an empty
// string, where the parser reads one as unset and does not refuse it). A value
// a capability rendered is in the properties the handler is handed: it is no
// fill where the handler keeps it, and a fill where the handler replaces it in
// that same way.
//
// An object's name is reported only where launcher's default stood
// (oam.Trait.ResolveNameDefaulted): a name the consumer's Naming hook gave is
// not launcher's fill, and the report has no source for it.
//
// Not reported: the name of the sub-application a trait adds, the trait-level
// serviceName and servicePort of ingress and httproute, the targetSecretName
// of external-secret where the trait makes no Secret (creationPolicy Merge or
// None), the fields security-context and topology-spread write into the
// workload, which add no sub-application, and what the expose rule fills
// before it emits its trait.

var (
	_ oam.BuiltinDefaultReporter = (*CertificateConfig)(nil)
	_ oam.BuiltinDefaultReporter = (*ExternalSecretConfig)(nil)
	_ oam.BuiltinDefaultReporter = (*VolsyncConfig)(nil)
	_ oam.BuiltinDefaultReporter = (*ScalerConfig)(nil)
	_ oam.BuiltinDefaultReporter = (*DisruptionConfig)(nil)
	_ oam.BuiltinDefaultReporter = (*rbacTraitConfig)(nil)
	_ oam.BuiltinDefaultReporter = (*NetworkPolicyConfig)(nil)
	_ oam.BuiltinDefaultReporter = (*IngressConfig)(nil)
	_ oam.BuiltinDefaultReporter = (*HTTPRouteConfig)(nil)
	_ oam.BuiltinDefaultReporter = (*PVCTraitConfig)(nil)
)

// reportFill records in log that the handler of trait filled path.
func reportFill(log *oam.BuiltinDefaultLog, trait, path string) {
	log.ReportBuiltinDefault(path, trait+"."+path)
}

// filled returns the fill of trait at path when stood is true, for a config
// that keeps a plain field per fill.
func filled(out []oam.BuiltinDefault, stood bool, trait, path string) []oam.BuiltinDefault {
	if !stood {
		return out
	}
	return append(out, oam.BuiltinDefault{Path: path, Ref: trait + "." + path})
}

// BuiltinDefaults implements oam.BuiltinDefaultReporter: the CPU target the
// parser set because the author wrote no target at all, and each object name
// that is launcher's default.
func (c *ScalerConfig) BuiltinDefaults() []oam.BuiltinDefault {
	out := filled(nil, c.cpuFilled, "scaler", "cpuUtilization")
	out = filled(out, c.hpaNameFilled, "scaler", "hpaName")
	return filled(out, c.pdbNameFilled, "scaler", "pdbName")
}

// BuiltinDefaults implements oam.BuiltinDefaultReporter: the budget's name,
// where it is launcher's default.
func (c *DisruptionConfig) BuiltinDefaults() []oam.BuiltinDefault {
	return filled(nil, c.pdbNameFilled, "disruption", "pdbName")
}

// BuiltinDefaults implements oam.BuiltinDefaultReporter: the one `name` of the
// trait's objects, where every one of them kept launcher's default.
func (c *rbacTraitConfig) BuiltinDefaults() []oam.BuiltinDefault {
	return filled(nil, c.nameFilled, "rbac", "name")
}

// BuiltinDefaults implements oam.BuiltinDefaultReporter: the policy's name,
// where it is launcher's default, and the TCP of each port the author gave no
// protocol.
func (c *NetworkPolicyConfig) BuiltinDefaults() []oam.BuiltinDefault {
	out := filled(nil, c.nameFilled, "networkpolicy", "name")
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
