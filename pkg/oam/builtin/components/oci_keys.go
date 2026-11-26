package components

import "github.com/go-kure/launcher/pkg/oam"

// ociSourceMember and ociKustomizationMember are the component types an oci
// component lowers into (LoweringTargets).
const (
	ociSourceMember        = "ocirepository"
	ociKustomizationMember = fluxcdKustomizationType
)

// LoweringKeys implements oam.LoweringKeyDeclarer: where each property of an oci
// component lands in the ocirepository and fluxcd-kustomization members.
//
// The rule has no loop over its schema, so the places are declared here, beside
// sourceProperties and kustomizationProperties, which build the two members. The
// access keys of source are listed once, in ociSourceAccessKeys, which the
// schema and this map read; sourceProperties and parseSourceAccess name each of
// them again, so the list is shared with the schema and not with the lowering.
// What the rule emits holds the declaration to account: the test of
// pkg/oam/builtin/registry authors every key and fails for a place the key does
// not change and for a change at a place it does not declare, in either
// direction (go-kure/launcher#1580, comment 6903630690: places declared beside
// the rule and held to what it emits by tests).
//
// A place that is Unchanged is one the rule hands the authored value to as
// written; every other place is built from the key.
func (OCIRule) LoweringKeys() map[string][]oam.LoweredKey {
	// source.url and the access keys land on the ocirepository under the same
	// names but one level up, so they are built places of source, not unchanged
	// ones; sourceRef on the Kustomization names the source object, which
	// source.name or source.objectName can rename.
	source := []oam.LoweredKey{{Member: ociSourceMember, Path: "url"}}
	for _, key := range ociSourceAccessKeys {
		source = append(source, oam.LoweredKey{Member: ociSourceMember, Path: key})
	}
	source = append(source, oam.LoweredKey{Member: ociKustomizationMember, Path: "sourceRef"})
	// version and interval can change which source the Kustomization names.
	sourceRef := oam.LoweredKey{Member: ociKustomizationMember, Path: "sourceRef"}
	return map[string][]oam.LoweredKey{
		"source": source,
		// Names the Kustomization object; it is no property of a member.
		"kustomizationName": nil,
		// A tag becomes ref.tag, a sha256: digest ref.digest.
		"version":         {{Member: ociSourceMember, Path: "ref"}, sourceRef},
		"path":            {{Member: ociKustomizationMember, Path: "path", Unchanged: true}},
		"prune":           {{Member: ociKustomizationMember, Path: "prune", Unchanged: true}},
		"interval":        {{Member: ociSourceMember, Path: "interval", Unchanged: true}, {Member: ociKustomizationMember, Path: "interval", Unchanged: true}, sourceRef},
		"targetNamespace": {{Member: ociKustomizationMember, Path: "targetNamespace", Unchanged: true}},
		"wait":            {{Member: ociKustomizationMember, Path: "wait", Unchanged: true}},
		// Each entry is rebuilt from its four fields.
		"healthChecks": {{Member: ociKustomizationMember, Path: "healthChecks"}},
	}
}
