package components

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file is secretValues, the sensitive part of a Helm values tree
// (go-kure/launcher#786): what the helm rule emits for it under delivery: flux,
// and the two operations both deliveries share, the shared-path refusal and the
// merge the helmtemplate renders with.
//
// No error raised here carries a value of secretValues, or the text of an
// encoding error that could repeat one: a path is named by its keys.

// helmSecretValuesTrait implements secretValues under delivery: flux. values is
// the component's authored values, still on the release, and secretValues the
// decoded property. With non-empty secretValues it returns the secret trait
// that emits a Secret named helmSecretValuesName, for the helmrelease to carry,
// and the valuesFrom entry naming it; absent or empty secretValues return a nil
// trait and no entry.
//
// The trait is the secret trait as authored documents use it, so the Secret
// follows the HelmRelease to a Flux namespace (it reads the Secret through
// valuesFrom), is the helmrelease component's object for pruning and
// replacement, and is refused by a policy that forbids explicit secrets with a
// violation naming the component.
//
// The values are serialized once, as the values ConfigMap's are
// (helmValuesConfigMap). Those exact bytes are stored in the Secret and hashed
// into its name, so the name changes whenever the content does: the
// HelmRelease's spec changes with it, which makes Flux reconcile an edit of
// secretValues alone. The name therefore carries ten hex digits of the content's
// digest, and so does the HelmRelease that names it; whoever can read either
// can test a guess of the whole secretValues tree against it.
//
// A path set in both values and secretValues is refused (refuseSharedValuePath).
// Without that, which one wins would depend on the values mode: Flux merges
// valuesFrom in order and applies inline values last, so inline values would
// win where a values ConfigMap would lose.
func helmSecretValuesTrait(name string, values any, secretValues map[string]any) (*oam.Trait, map[string]any, error) {
	if len(secretValues) == 0 {
		return nil, nil, nil
	}
	secret, err := jsonObject(secretValues)
	if err != nil {
		return nil, nil, errors.Errorf("%s: %s is not representable as JSON", helmType, helmSecretValuesKey)
	}
	if len(secret) == 0 {
		return nil, nil, nil
	}
	// Values that do not encode as a JSON object are the helmrelease's to
	// refuse, with its own message; there is then nothing to compare.
	if plain, err := jsonObject(values); err == nil {
		if err := refuseSharedValuePath(helmType, plain, secret); err != nil {
			return nil, nil, err
		}
	}
	data, err := json.MarshalIndent(secret, "", "  ")
	if err != nil {
		return nil, nil, errors.Errorf("%s: %s is not representable as JSON", helmType, helmSecretValuesKey)
	}
	sum := sha256.Sum256(data)
	secretName := helmSecretValuesName(name, hex.EncodeToString(sum[:]))

	entry := map[string]any{"kind": "Secret", "name": secretName, "valuesKey": helmValuesKey}
	return &oam.Trait{
		Type: "secret",
		Properties: map[string]any{
			"name":       secretName,
			"stringData": map[string]any{helmValuesKey: string(data)},
		},
	}, entry, nil
}

// helmSecretValuesName names the values Secret of component name whose
// serialized secretValues have the hex digest valuesDigest: name with the
// suffix "-secret-values-<first 10 digest digits>", shortened by the one
// shortening rule exactly as helmValuesConfigMapName shortens the values
// ConfigMap's name.
func helmSecretValuesName(name, valuesDigest string) string {
	return oam.ShortenNameWithSuffix(name, "-secret-values-"+valuesDigest[:helmValuesHashLen], oam.ShortenLimitSubdomain)
}

// jsonObject encodes v as JSON and decodes it into a map, numbers kept exact
// (json.Number). A nil v is a nil map. It fails when v does not encode, or
// encodes as anything but one JSON object; the error says only that, since an
// encoding error can repeat a value.
func jsonObject(v any) (map[string]any, error) {
	if v == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, errors.New("not representable as JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(encoded))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, errors.New("not a JSON object")
	}
	return m, nil
}

// refuseSharedValuePath refuses a path set in both values and secretValues.
// Two objects at the same key are not a shared path: they are compared key by
// key, since a merge keeps both. Anything else at a key both trees have is: a
// scalar, a list or a null on either side would replace, or be replaced by,
// what the other side set. Only a map[string]any is read as an object.
//
// The first shared path in sorted key order is named, by its keys joined with
// dots and never by a value; an empty key is written "". owner is the component
// type that prefixes the message.
func refuseSharedValuePath(owner string, values, secretValues map[string]any) error {
	if path, shared := sharedValuePath(values, secretValues); shared {
		return errors.Errorf("%s: %s is set in both values and %s; a path may be set in only one of them", owner, path, helmSecretValuesKey)
	}
	return nil
}

// sharedValuePath returns the first path, in sorted key order, that a and b
// both set (see refuseSharedValuePath), and whether there is one. The path
// alone cannot say so: a shared empty key is a path too.
func sharedValuePath(a, b map[string]any) (string, bool) {
	for _, k := range slices.Sorted(maps.Keys(b)) {
		av, ok := a[k]
		if !ok {
			continue
		}
		name := k
		if name == "" {
			name = `""`
		}
		am, aok := av.(map[string]any)
		bm, bok := b[k].(map[string]any)
		if !aok || !bok {
			return name, true
		}
		if sub, shared := sharedValuePath(am, bm); shared {
			return name + "." + sub, true
		}
	}
	return "", false
}

// mergeSecretValues returns values with secretValues merged over it: the tree a
// chart is rendered with under delivery: template. Objects are merged key by
// key into new maps, so neither input is changed; every other value is carried
// over as it is, with its authored type. The two trees share no path
// (refuseSharedValuePath), so nothing is overwritten. With no secretValues it
// returns values itself.
func mergeSecretValues(values, secretValues map[string]any) map[string]any {
	if len(secretValues) == 0 {
		return values
	}
	merged := make(map[string]any, len(values)+len(secretValues))
	maps.Copy(merged, values)
	for k, sv := range secretValues {
		vm, vok := merged[k].(map[string]any)
		sm, sok := sv.(map[string]any)
		if vok && sok {
			merged[k] = mergeSecretValues(vm, sm)
			continue
		}
		merged[k] = sv
	}
	return merged
}
