package components

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/go-kure/launcher/pkg/errors"
)

func enforceMaxReplicas(current int32, max *int32) error {
	if max == nil {
		return nil
	}
	if current > *max {
		return errors.Errorf("replicas %d exceeds enforced maximum %d", current, *max)
	}
	return nil
}

func enforceMaxResource(current, max, label string) error {
	if max == "" || current == "" {
		return nil
	}
	currentQty, err := resource.ParseQuantity(current)
	if err != nil {
		return errors.Wrapf(err, "invalid %s value %q", label, current)
	}
	maxQty, err := resource.ParseQuantity(max)
	if err != nil {
		return errors.Wrapf(err, "invalid enforced max %s value %q", label, max)
	}
	if currentQty.Cmp(maxQty) > 0 {
		return errors.Errorf("%s %q exceeds enforced maximum %q", label, current, max)
	}
	return nil
}

func enforceAllowedRegistries(image string, allowed []string) error {
	if len(allowed) == 0 {
		return nil
	}
	imageHost := registryHost(image)
	for _, registry := range allowed {
		if imageHost == strings.TrimRight(registry, "/") {
			return nil
		}
	}
	return errors.Errorf("image %q is not from an allowed registry %v", image, allowed)
}

func registryHost(image string) string {
	ref := image
	if at := strings.IndexByte(ref, '@'); at != -1 {
		ref = ref[:at]
	}
	if colon := strings.LastIndexByte(ref, ':'); colon != -1 {
		if !strings.Contains(ref[colon:], "/") {
			ref = ref[:colon]
		}
	}
	before, _, ok := strings.Cut(ref, "/")
	if !ok {
		return "docker.io"
	}
	candidate := before
	if strings.ContainsAny(candidate, ".:") || candidate == "localhost" {
		return candidate
	}
	return "docker.io"
}

func enforceMaxStorageSize(current, max string) error {
	return enforceMaxResource(current, max, "storageSize")
}

// enforcePrivileged rejects an authored `securityContext.privileged: true` when
// the environment policy does not allow it. This is the one securityContext
// field with a matching pre-existing Policy hook (oam.Policy.AllowPrivileged,
// declared but previously uncallable from this package since no property could
// ever produce a non-nil SecurityContext) — reusing it here, rather than adding
// a new mechanism, closes the gap the new `securityContext` property would
// otherwise open: an author could set privileged:true with nothing checking it.
// The pod-level fields (hostNetwork/hostPID/hostIPC — not part of this
// container-level property at all) have no existing Policy hook and none is
// added here; see the launcher#278 ledger. Of securityContext.capabilities,
// Add is enforced separately (enforceContainerCapabilities below); Drop is
// intentionally never checked against policy — dropping a capability is
// strictly hardening and can never violate one.
//
// NOTE on a naming collision: oam.Policy already declares AllowedCapabilities/
// ForbiddenCapabilities/RequiredCapabilities, but those gate OAM trait-type
// usage (e.g. "ingress", "autoscaling"), not corev1 Linux capability strings
// (e.g. "NET_ADMIN") — their only call site is
// enforceCapabilityConstraints(authoredTraitTypes, policy) in transform.go,
// operating on Component.Traits[].Type. Wiring securityContext.capabilities.
// add/drop through those three methods, as suggested in a launcher#278 review
// round, would check container capability strings against a policy list of
// trait-type names — always-fail or always-no-op depending on the data, never
// correct. Container capabilities are enforced separately, by
// enforceContainerCapabilities below, against the dedicated
// AllowedContainerCapabilities/ForbiddenContainerCapabilities accessor pair
// (go-kure/launcher#305).
func enforcePrivileged(sc *corev1.SecurityContext, allowed bool) error {
	if sc == nil || sc.Privileged == nil || !*sc.Privileged || allowed {
		return nil
	}
	return errors.New("securityContext.privileged is not allowed by environment policy")
}

// enforceHostPathVolumes rejects an authored hostPath volume when the
// environment policy does not allow it. Same shape and same reused-not-new-
// mechanism rationale as enforcePrivileged above: oam.Policy.AllowHostPathVolumes
// is a pre-existing method (default-deny even for NoopPolicy, see pkg/oam/policy.go)
// that had nothing to call it — parseVolumes could not produce a hostPath source
// before the shared pod/container schema landed. A hostPath volume mounts an
// arbitrary path from the node's own filesystem into the Pod, so an unenforced
// policy denial here is a container-escape-adjacent gap, not merely a style one.
func enforceHostPathVolumes(volumes []corev1.Volume, allowed bool) error {
	if allowed {
		return nil
	}
	for _, v := range volumes {
		if v.HostPath != nil {
			return errors.Errorf("volume %q: hostPath volumes are not allowed by environment policy", v.Name)
		}
	}
	return nil
}

// normalizeCapability upper-cases a Linux capability string and strips a
// leading "CAP_" prefix, so "NET_ADMIN", "CAP_NET_ADMIN" and "net_admin" all
// compare equal. parseCapabilityList (common.go) does no normalisation of its
// own, so authored values reach here in any of these spellings; this policy's
// own normalisation treats them as the same capability for allow/forbid
// matching. This is a deliberate, policy-defined aliasing, not a runtime
// equivalence Kubernetes itself enforces — contrast the exact-string
// CAP_SYS_ADMIN check in parseSecurityContext (common.go), which intentionally
// does not alias because it mirrors real Kubernetes admission's own
// exact-string scope. Applied to both authored values and policy-list entries:
// a forbidden-list entry spelled "NET_ADMIN" must still catch an authored
// "CAP_NET_ADMIN", and a policy list itself spelled non-canonically
// ("cap_net_admin") must still match a canonically-authored value.
func normalizeCapability(capability string) string {
	return strings.TrimPrefix(strings.ToUpper(capability), "CAP_")
}

// containsCapability reports whether normalizeCapability(capability) appears
// in list after normalising every entry of list the same way.
func containsCapability(list []string, capability string) bool {
	target := normalizeCapability(capability)
	for _, c := range list {
		if normalizeCapability(c) == target {
			return true
		}
	}
	return false
}

// enforceContainerCapabilities rejects an authored securityContext.capabilities.add
// entry that the environment policy does not permit. Default-allow,
// forbidden-list-first semantics (go-kure/launcher#305): nil/empty allowed
// means no restriction; nil/empty forbidden means no forbids; forbidden wins
// when an entry appears in both.
//
// Capabilities.Drop is never checked — dropping a capability is strictly
// hardening and can never violate a policy.
//
// "ALL" is special-cased, and only on the forbidden side: ALL grants every
// Linux capability, so it necessarily includes whatever is in forbidden.
// A normalised ALL entry is rejected whenever forbidden is non-empty,
// regardless of whether "ALL" literally appears in forbidden — checking only
// literal membership would let an author bypass a forbidden-list entry like
// NET_ADMIN simply by authoring ALL instead. When forbidden is empty, ALL is
// checked against allowed like any other entry — an opt-in allowlist that
// omits "ALL" already rejects it with no special-casing needed.
func enforceContainerCapabilities(sc *corev1.SecurityContext, allowed, forbidden []string) error {
	if sc == nil || sc.Capabilities == nil {
		return nil
	}
	for _, raw := range sc.Capabilities.Add {
		capability := string(raw)
		normalized := normalizeCapability(capability)

		if normalized == "ALL" && len(forbidden) > 0 {
			return errors.Errorf("securityContext.capabilities.add: %q is forbidden by environment policy", capability)
		}
		if containsCapability(forbidden, capability) {
			return errors.Errorf("securityContext.capabilities.add: %q is forbidden by environment policy", capability)
		}
		if len(allowed) > 0 && !containsCapability(allowed, capability) {
			return errors.Errorf("securityContext.capabilities.add: %q is not allowed by environment policy", capability)
		}
	}
	return nil
}

func applyDefaultReplicas(current int32, explicit bool, dflt *int32) int32 {
	if explicit || dflt == nil {
		return current
	}
	return *dflt
}

// quantityString returns the string form of rl[name], or "" if rl is nil or
// name is absent — bridging corev1.ResourceList (the real
// map[ResourceName]resource.Quantity type ResourceRequirements now embeds) to
// enforceMaxResource's string-based comparison.
func quantityString(rl corev1.ResourceList, name corev1.ResourceName) string {
	if rl == nil {
		return ""
	}
	if q, ok := rl[name]; ok {
		return q.String()
	}
	return ""
}

// enforceMaxResources rejects a config whose *effective* cpu/memory requests or
// limits exceed the policy maxima. "Effective" is what Generate will emit:
// authored values, then the policy defaults ApplyPolicy just applied, then this
// package's intrinsic fallbacks (buildResourceRequirements — 100m cpu request,
// 128Mi memory request, memory limit mirroring the memory request). Enforcing
// against res directly let an omitted value ship above the cap, since the
// intrinsic fallback is injected after ApplyPolicy runs (launcher#251).
//
// Read-only: buildResourceRequirements deep-copies its maps, so the caller's
// Resources are untouched and generated output is unchanged.
func enforceMaxResources(res ResourceRequirements, maxCPU, maxMemory string) error {
	if maxCPU == "" && maxMemory == "" {
		return nil
	}
	eff := buildResourceRequirements(res)

	checks := []struct {
		effective corev1.ResourceList
		authored  corev1.ResourceList
		name      corev1.ResourceName
		max       string
		label     string
	}{
		{eff.Requests, res.Requests, corev1.ResourceCPU, maxCPU, "cpu request"},
		{eff.Limits, res.Limits, corev1.ResourceCPU, maxCPU, "cpu limit"},
		{eff.Requests, res.Requests, corev1.ResourceMemory, maxMemory, "memory request"},
		{eff.Limits, res.Limits, corev1.ResourceMemory, maxMemory, "memory limit"},
	}
	for _, c := range checks {
		effVal := quantityString(c.effective, c.name)
		label := c.label
		// Absent from the authored/policy-defaulted value but present in the
		// effective one: this package's intrinsic fallback produced it, and
		// the author has no idea where the number came from.
		if effVal != "" && quantityString(c.authored, c.name) == "" {
			label += " (generated default)"
		}
		if err := enforceMaxResource(effVal, c.max, label); err != nil {
			return err
		}
	}
	return nil
}

// applyDefaultQuantity sets (*rl)[name] = dflt when rl has no entry for name
// (the author left this specific resource name unmentioned — map-key presence
// is ResourceRequirements' equivalent of the old explicitResourceFlags bool,
// now removed) and dflt is non-empty (the policy has a default for it); *rl
// is allocated on first write if nil.
func applyDefaultQuantity(rl *corev1.ResourceList, name corev1.ResourceName, dflt string) error {
	if _, ok := (*rl)[name]; ok || dflt == "" {
		return nil
	}
	q, err := resource.ParseQuantity(dflt)
	if err != nil {
		return errors.Errorf("policy default for %s: invalid quantity %q: %w", name, dflt, err)
	}
	if *rl == nil {
		*rl = corev1.ResourceList{}
	}
	(*rl)[name] = q
	return nil
}
