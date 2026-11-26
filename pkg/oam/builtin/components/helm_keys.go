package components

import (
	"slices"

	"github.com/go-kure/launcher/pkg/oam"
)

// The member and trait types the helm rule's map names.
const (
	helmReleaseMember    = "helmrelease"
	helmRepositoryMember = "helmrepository"
	helmOCIMember        = "ocirepository"
	helmGitMember        = "gitrepository"
	helmBucketMember     = "bucket"
	helmConfigMapTrait   = "configmap"
	helmSecretTrait      = "secret"
)

// LoweringKeys implements oam.LoweringKeyDeclarer. The keys the rule forwards
// come from helmPassthroughKeys and helmFluxOnlyKeys, the slices it forwards and
// refuses with; the places of the keys it builds are declared here, beside
// lowerHelmFlux and lowerHelmTemplate. Every place is conditional on delivery.
//
// No condition names which source member is emitted: that follows the source's
// kind, which the rule may infer from its URL, so source and version name every
// one they can reach.
func (HelmRule) LoweringKeys() map[string][]oam.LoweredKey {
	delivery := func(values ...string) oam.LoweredWhen {
		return oam.LoweredWhen{Key: "delivery", Values: values}
	}
	valuesMode := func(values ...string) oam.LoweredWhen {
		return oam.LoweredWhen{Key: "valuesMode", Values: values}
	}
	// Each place gets conditions of its own: a caller's write to one place's
	// reaches no other.
	flux := func() []oam.LoweredWhen { return []oam.LoweredWhen{delivery("", "flux")} }
	inline := func() []oam.LoweredWhen { return append(flux(), valuesMode("", "inline")) }
	configMap := func() []oam.LoweredWhen { return append(flux(), valuesMode("configMap")) }
	template := func() []oam.LoweredWhen { return []oam.LoweredWhen{delivery("template")} }

	// The release's own places: a property it holds, and a trait it carries.
	release := func(path string, when []oam.LoweredWhen) oam.LoweredKey {
		return oam.LoweredKey{Member: helmReleaseMember, Path: path, When: when}
	}
	trait := func(typ, path string, when []oam.LoweredWhen) oam.LoweredKey {
		return oam.LoweredKey{Member: helmReleaseMember, Trait: typ, Path: path, When: when}
	}
	source := func(member, path string) oam.LoweredKey {
		return oam.LoweredKey{Member: member, Path: path, When: flux()}
	}
	// The terminal under delivery: template takes the key under its own name,
	// as written.
	rendered := func(key string) oam.LoweredKey {
		return oam.LoweredKey{Member: helmTemplateType, Path: key, Unchanged: true, When: template()}
	}
	// The release names a generated source through one of two properties, by the
	// source's kind.
	sourceRefs := []oam.LoweredKey{release("chart.spec.sourceRef", flux()), release("chartRef", flux())}

	keys := map[string][]oam.LoweredKey{
		// A switch: it chooses the members, and is no property of one.
		"delivery": nil,
		// A switch: it chooses where values go.
		"valuesMode": nil,
		// Names the HelmRelease object.
		"helmReleaseName": nil,
		// Launcher's own declaration, split off before the lowering.
		chartEndpointsKey: nil,

		"chart":   {release("chart.spec.chart", flux()), rendered("chart")},
		"version": {release("chart.spec.version", flux()), source(helmOCIMember, "ref"), release("chartRef", flux()), rendered("version")},
		"source": slices.Concat([]oam.LoweredKey{
			source(helmRepositoryMember, "url"),
			source(helmOCIMember, "url"),
			source(helmOCIMember, "ref"),
			source(helmGitMember, "url"),
			source(helmGitMember, "ref"),
			source(helmBucketMember, "endpoint"),
			source(helmBucketMember, "bucketName"),
			source(helmBucketMember, "provider"),
			source(helmBucketMember, "region"),
			source(helmBucketMember, "prefix"),
		}, sourceRefs, []oam.LoweredKey{{Member: helmTemplateType, Path: "source", When: template()}}),

		helmSecretValuesKey:   {trait(helmSecretTrait, "", flux()), release("valuesFrom", flux()), rendered(helmSecretValuesKey)},
		"valuesConfigMapName": {trait(helmConfigMapTrait, "name", configMap()), release("valuesFrom", configMap())},
		"valuesSecretName":    {trait(helmSecretTrait, "name", flux()), release("valuesFrom", flux())},

		oam.HookGroupNamePrefixProperty:     {rendered(oam.HookGroupNamePrefixProperty)},
		oam.LayoutKustomizationNameProperty: {rendered(oam.LayoutKustomizationNameProperty)},
		scopeOverridesKey:                   {rendered(scopeOverridesKey)},
	}
	for _, key := range helmPassthroughKeys {
		keys[key] = []oam.LoweredKey{{Member: helmReleaseMember, Path: key, Unchanged: true, When: flux()}}
		if !slices.Contains(helmFluxOnlyKeys, key) {
			keys[key] = append(keys[key], rendered(key))
		}
	}
	// values stays on the release only inline; under valuesMode: configMap it
	// moves to a configmap trait that a generated valuesFrom entry names.
	keys["values"] = []oam.LoweredKey{
		{Member: helmReleaseMember, Path: "values", Unchanged: true, When: inline()},
		trait(helmConfigMapTrait, "", configMap()),
		release("valuesFrom", configMap()),
		rendered("values"),
	}
	// The generated entries are put ahead of the authored ones.
	keys["valuesFrom"] = []oam.LoweredKey{release("valuesFrom", flux())}
	return keys
}
