package components

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

func enforceMaxReplicas(current int32, max *int32) error {
	if max == nil {
		return nil
	}
	if current > *max {
		return fmt.Errorf("replicas %d exceeds enforced maximum %d", current, *max)
	}
	return nil
}

func enforceMaxResource(current, max, label string) error {
	if max == "" || current == "" {
		return nil
	}
	currentQty, err := resource.ParseQuantity(current)
	if err != nil {
		return fmt.Errorf("invalid %s value %q: %w", label, current, err)
	}
	maxQty, err := resource.ParseQuantity(max)
	if err != nil {
		return fmt.Errorf("invalid enforced max %s value %q: %w", label, max, err)
	}
	if currentQty.Cmp(maxQty) > 0 {
		return fmt.Errorf("%s %q exceeds enforced maximum %q", label, current, max)
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
	return fmt.Errorf("image %q is not from an allowed registry %v", image, allowed)
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
