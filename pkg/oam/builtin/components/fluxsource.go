package components

import (
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// The kind-named Flux source components — helmrepository, ocirepository,
// gitrepository and bucket (go-kure/launcher#347) — each project one
// source-controller spec type 1:1. ToApplicationConfig decodes the component's
// properties strictly into that type (builtin.DecodeStrictJSON, no owned keys),
// so an unknown or wrongly typed key at any depth is a build error, and checks
// what the CRD would otherwise reject only at apply time: its required fields,
// and the url scheme where the CRD has a pattern for it. Generate emits exactly
// one CR, named after the component, carrying a deep copy of that spec plus the
// interval default. Every other constraint is left to the CRD's own admission.
//
// This file holds what the four share; each kind has its own file.

// fluxSourceDefaultInterval is spec.interval when a source component leaves it
// unset (a zero duration counts as unset). OCIRepository, GitRepository and
// Bucket require the field; 60m matches the helmrelease default.
const fluxSourceDefaultInterval = 60 * time.Minute

// defaultFluxSourceInterval sets d to fluxSourceDefaultInterval when it is zero.
func defaultFluxSourceInterval(d *metav1.Duration) {
	if d.Duration == 0 {
		d.Duration = fluxSourceDefaultInterval
	}
}

// fluxSourceNamespace returns the namespace a source CR lands in: the Flux
// namespace when one is set (SetFluxNamespace), else the application namespace.
func fluxSourceNamespace(namespace, fluxNS string) string {
	if fluxNS != "" {
		return fluxNS
	}
	return namespace
}

// checkFluxSourceURL is the build-time form of a source CRD's url pattern: value
// must be non-empty and start with one of schemes. typ and field name the
// component type and the property in the error.
func checkFluxSourceURL(typ, field, value string, schemes ...string) error {
	if value == "" {
		return errors.Errorf("%s: %s is required", typ, field)
	}
	for _, s := range schemes {
		if strings.HasPrefix(value, s) {
			return nil
		}
	}
	return errors.Errorf("%s: %s %q must start with %s", typ, field, value, strings.Join(schemes, " or "))
}

// enforceFluxSourceHost checks the host a source is fetched from against the
// policy's allowed registries: the allowlist, and the exact host match, that the
// oci, crd and manifests components apply to their own URLs (urlHost). No
// policy, or an empty allowlist, permits every host.
func enforceFluxSourceHost(typ, field, value string, p oam.Policy) error {
	if p == nil {
		return nil
	}
	return errors.Wrapf(enforceAllowedURLHosts(value, p.AllowedRegistries()), "%s: %s", typ, field)
}

// enforceFluxSourceOCIHost is enforceFluxSourceHost for an oci:// url, whose
// registry is not always its first path segment. go-containerregistry's
// name.NewRepository — how source-controller parses an OCIRepository url, and
// the parse behind a Helm OCI chart's signature verification — takes the first
// segment as the registry only when a "/" follows it and it is localhost or
// contains "." or ":"; otherwise the whole reference is a Docker Hub repository,
// so oci://ghcr.io and oci://registry/app are pulled from Docker Hub, whatever
// host the allowlist matched. The Helm registry client that pulls an OCI chart
// always takes the first segment, so the two agree only on an explicit registry.
//
// Under a non-empty allowlist the url must therefore name its registry
// explicitly (ociNamesRegistry), and is then checked like any other host. With
// requireRepository — an OCIRepository url, which is the whole artifact
// repository — a non-empty path must follow the registry; a HelmRepository url
// may stop at the registry, since Flux appends the chart name to it. No policy,
// or an empty allowlist, permits every url, as for the other sources.
func enforceFluxSourceOCIHost(typ, field, value string, requireRepository bool, p oam.Policy) error {
	if p == nil || len(p.AllowedRegistries()) == 0 {
		return nil
	}
	if !ociNamesRegistry(value, requireRepository) {
		form := "oci://<registry>[/<path>]"
		if requireRepository {
			form = "oci://<registry>/<repository>"
		}
		return errors.Errorf("%s: %s: %q does not name its registry explicitly, so Flux may resolve it against Docker Hub: "+
			"under an allowed-registries policy write %s with a registry that is localhost or contains \".\" or \":\" "+
			"(e.g. oci://docker.io/library/app, oci://registry.example:5000/org/app)", typ, field, value, form)
	}
	return enforceFluxSourceHost(typ, field, value, p)
}

// The PropertySchema building blocks of the four source components. Their
// schemas declare every top-level key of the spec type, with nested Flux shapes
// as open objects: the strict decode in ToApplicationConfig checks those.

func fluxSourceString(desc string) oam.PropertySchema {
	return oam.PropertySchema{Type: oam.PropertyTypeString, Description: desc}
}

func fluxSourceBool(desc string) oam.PropertySchema {
	return oam.PropertySchema{Type: oam.PropertyTypeBoolean, Description: desc}
}

func fluxSourceObject(desc string) oam.PropertySchema {
	return oam.PropertySchema{Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: desc}
}

// fluxSourceRequiredString is a string property the CRD requires.
func fluxSourceRequiredString(desc string) oam.PropertySchema {
	return oam.PropertySchema{Type: oam.PropertyTypeString, Required: true, Description: desc}
}
