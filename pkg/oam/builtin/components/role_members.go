package components

import (
	"maps"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// roleObjectTraits are the authored trait types a role rule forwards to every
// member, because they decorate every object a component generates and each
// member generates its own (see WebserviceRule).
var roleObjectTraits = map[string]bool{"prune-protection": true, "force-replace": true}

// roleServiceAccount moves a role component's per-component ServiceAccount out
// of its deployment member into a `serviceaccount` sibling member
// (go-kure/launcher#702). When serviceAccountName is authored, the pod runs as
// that existing account and nothing is emitted, as before. Otherwise the member
// is the account deployment generated itself — the component's name, its
// labels, automountServiceAccountToken false — and the deployment member is
// handed that name, so it emits none of its own. traits are the authored
// traits forwarded to the member: the object decorators, which acted on the
// account when deployment generated it.
func roleServiceAccount(comp *oam.Component, depProps map[string]any, traits []oam.Trait) *oam.Component {
	if _, authored := authoredValue(depProps, "serviceAccountName"); authored {
		return nil
	}
	depProps["serviceAccountName"] = comp.Name
	var saTraits []oam.Trait
	for _, t := range traits {
		if roleObjectTraits[t.Type] {
			saTraits = append(saTraits, t)
		}
	}
	return &oam.Component{
		Name:        comp.Name,
		Type:        "serviceaccount",
		Properties:  map[string]any{"automountServiceAccountToken": false},
		Traits:      saTraits,
		Annotations: maps.Clone(comp.Annotations),
	}
}

// roleClaims moves the claims a role component's `pvc` volumes generate out of
// its deployment member into synthesized `pvc` traits (go-kure/launcher#702),
// one per volume that does not reference an existing claim, in volume order.
// Each trait carries the claim the deployment kind built before it stopped
// generating claims — the component-qualified name (escapeForPVCQualification),
// size, storageClassName (an authored "" included), accessModes and
// volumeMode — and its volume is rewritten to
// reference that claim by claimName, keeping accessModes so the non-RWX
// constraints still see it. depProps must be the rule's own copy: its
// `volumes` list is replaced, never edited in place. parseVolumes has already
// accepted every volume, so only the shape this rewrite reads is assumed.
func roleClaims(comp *oam.Component, depProps map[string]any) ([]oam.Trait, error) {
	vols, ok := depProps["volumes"].([]any)
	if !ok {
		return nil, nil
	}
	var traits []oam.Trait
	rewritten := make([]any, len(vols))
	for i, v := range vols {
		rewritten[i] = v
		m, ok := nullElem(v).(map[string]any)
		if !ok || m["type"] != "pvc" {
			continue
		}
		if _, ref := authoredValue(m, "claimName"); ref {
			continue
		}
		volName, _ := m["name"].(string)
		claim := escapeForPVCQualification(comp.Name) + "-" + escapeForPVCQualification(volName)
		// Kubernetes PersistentVolumeClaim names must be DNS-1123 subdomains.
		if errs := validation.IsDNS1123Subdomain(claim); len(errs) > 0 {
			return nil, errors.Errorf("PVC name %q is not a valid DNS-1123 subdomain: %s", claim, strings.Join(errs, "; "))
		}
		props := map[string]any{"name": claim, "size": m["size"]}
		if sc, present := authoredValue(m, "storageClass"); present {
			props["storageClassName"] = sc
		}
		for _, key := range []string{"accessModes", "volumeMode"} {
			if val, present := authoredValue(m, key); present {
				props[key] = val
			}
		}
		traits = append(traits, oam.Trait{Type: "pvc", Properties: props})

		vol := maps.Clone(m)
		delete(vol, "size")
		delete(vol, "storageClass")
		vol["claimName"] = claim
		rewritten[i] = vol
	}
	if len(traits) > 0 {
		depProps["volumes"] = rewritten
	}
	return traits, nil
}
