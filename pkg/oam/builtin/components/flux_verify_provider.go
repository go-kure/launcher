package components

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/go-kure/launcher/pkg/errors"
)

// fluxVerifyProviderDefault is the provider the Flux API gives a verification
// that names none: the default marker on the provider of a HelmRelease's
// chart.spec.verify and of an OCIRepository's and a HelmChart's verify. The Go
// types always encode the field (no omitempty), so an unauthored provider
// would reach the API server as "", which the API's enum refuses, and the
// default would never apply. The three kinds write the default there, which is
// what the API server would have made of the omitted field.
// TestKindComponents_OmittedRequiredAndWrittenDefaults holds the value to the
// marker in the source of the linked modules.
const fluxVerifyProviderDefault = "cosign"

// fillFluxVerifyProvider writes the API's default into an empty provider. An
// authored "" never reaches it from a component
// (refuseEmptyFluxVerifyProvider), so an empty provider is unauthored, or empty
// in a config built directly in Go, where the two cannot be told apart.
func fillFluxVerifyProvider(provider *string) {
	if *provider == "" {
		*provider = fluxVerifyProviderDefault
	}
}

// refuseEmptyFluxVerifyProvider refuses an authored "" for the provider of the
// verification at path in a Flux kind's properties. The Go type cannot tell it
// from an unauthored one, so Generate would write the API's default over it,
// while the API's enum refuses it as written. The properties are read in their
// JSON form, as the decode reads them (numbers kept exact), so a typed map or a
// named string type a caller builds them with is read as what it encodes. Keys
// match case-insensitively, as the decode's do.
func refuseEmptyFluxVerifyProvider(authored map[string]any, path ...string) error {
	raw, err := json.Marshal(authored)
	if err != nil {
		return errors.Wrapf(err, "%s.provider: properties do not encode as JSON", strings.Join(path, "."))
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var props map[string]any
	if err := dec.Decode(&props); err != nil {
		return errors.Wrapf(err, "%s.provider: properties do not read as a JSON object", strings.Join(path, "."))
	}
	nodes := []map[string]any{props}
	for _, key := range path {
		var next []map[string]any
		for _, node := range nodes {
			for _, k := range slices.Sorted(maps.Keys(node)) {
				if m, ok := node[k].(map[string]any); ok && strings.EqualFold(k, key) {
					next = append(next, m)
				}
			}
		}
		nodes = next
	}
	for _, node := range nodes {
		for _, k := range slices.Sorted(maps.Keys(node)) {
			if strings.EqualFold(k, "provider") && node[k] == "" {
				return errors.Errorf(`%s.%s: "" is refused by the Flux API, whose enum is cosign or notation; omit the field for the API's default, %s`,
					strings.Join(path, "."), k, fluxVerifyProviderDefault)
			}
		}
	}
	return nil
}
