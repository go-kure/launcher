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
// The other securityContext fields (capabilities add/drop, hostNetwork/hostPID/
// hostIPC — the latter three are pod-level and not part of this container-level
// property at all) have no existing Policy hook and none is added here; see the
// launcher#278 ledger.
func enforcePrivileged(sc *corev1.SecurityContext, allowed bool) error {
	if sc == nil || sc.Privileged == nil || !*sc.Privileged || allowed {
		return nil
	}
	return errors.New("securityContext.privileged is not allowed by environment policy")
}

func applyDefaultReplicas(current int32, explicit bool, dflt *int32) int32 {
	if explicit || dflt == nil {
		return current
	}
	return *dflt
}

func applyDefaultResource(current, dflt string) string {
	if current != "" {
		return current
	}
	return dflt
}
