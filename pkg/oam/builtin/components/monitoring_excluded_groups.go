package components

import (
	"maps"
	"slices"
	"strings"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"

	"github.com/go-kure/launcher/pkg/errors"
)

// monitoringExcludedGroup is the group the Prometheus operator's API gives an
// entry of excludedFromEnforcement that names none, and the one value it
// allows.
const monitoringExcludedGroup = "monitoring.coreos.com"

// withExcludedGroups writes monitoringExcludedGroup into each entry of refs
// that names no group. The type writes the field whether or not it was
// authored, so an entry without one would carry an empty group, which the API
// refuses instead of defaulting. An authored empty group never reaches here:
// refuseEmptyExcludedGroups refuses it, so only an omitted (or null) group is
// filled. TestKindComponents_OmittedRequiredAndWrittenDefaults derives the
// field from the type's markers, and TestMonitoringExcludedGroup_MatchesMarkers
// holds the value to the module's default and enum.
func withExcludedGroups(refs []monitoringv1.ObjectReference) {
	for i := range refs {
		if refs[i].Group == "" {
			refs[i].Group = monitoringExcludedGroup
		}
	}
}

// refuseEmptyExcludedGroups refuses an excludedFromEnforcement entry that
// authors its group as the empty string, by the entry's index: the API admits
// only monitoringExcludedGroup there, and an invalid authored value is
// refused, not repaired. props is the authored properties' JSON tree
// (jsonProperties, before the null strip), the one tree the decode then reads,
// so a direct caller's typed collections and pointers are seen as the decode
// sees them; the decoded value does not tell an empty group from an omitted
// one. Keys match as the strict decode matches them, case-insensitively; a null
// is an absent value (the null contract), and a value of another shape is left
// to the decode.
func refuseEmptyExcludedGroups(props map[string]any) error {
	for _, key := range slices.Sorted(maps.Keys(props)) {
		if !strings.EqualFold(key, "excludedFromEnforcement") {
			continue
		}
		refs, _ := props[key].([]any)
		for i, raw := range refs {
			entry, _ := raw.(map[string]any)
			for _, field := range slices.Sorted(maps.Keys(entry)) {
				if strings.EqualFold(field, "group") && entry[field] == "" {
					return errors.Errorf("%s[%d].%s: empty: the API admits only %s; leave the field out to have it written",
						key, i, field, monitoringExcludedGroup)
				}
			}
		}
	}
	return nil
}
