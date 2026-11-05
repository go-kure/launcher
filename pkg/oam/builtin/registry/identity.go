package registry

import (
	"fmt"
	"strings"

	"github.com/go-kure/launcher/pkg/oam"
)

// LauncherDomain is the domain of every built-in type that is not a Kubernetes
// kind: `<name>.launcher.gokure.dev`.
const LauncherDomain = "launcher.gokure.dev"

// launcherVersion is the version every built-in declares that is not a
// Kubernetes kind. A kind declares the API version it emits.
const launcherVersion = "v1"

// ComponentTypeIDs returns the identity every built-in component type declares,
// keyed by the plain type name it registers under (the keys of ComponentHandlers
// and ComponentLoweringRules together). Each call returns a new map.
//
// A type whose name is the lowercase of the one Kubernetes kind it generates (a
// kind component: `deployment`, `service`, `tlsroute`) is that kind's identity,
// `kind.group@<API version>`, a core kind with no group part. Every other type
// is `<name>.launcher.gokure.dev@v1`: `webservice`, and the types named for a
// product (`cnpg-cluster`, `cilium-bgppeerconfig`, `fluxcd-alert`) whose name is
// not the kind's.
func ComponentTypeIDs() map[string]oam.TypeID {
	ids := make(map[string]oam.TypeID)
	for name, h := range ComponentHandlers() {
		ids[name] = componentTypeID(name, h)
	}
	for name := range ComponentLoweringRules() {
		ids[name] = launcherTypeID(name)
	}
	return ids
}

// TraitTypeIDs returns the identity every built-in trait type declares, keyed by
// the plain type name (the keys of TraitHandlers and TraitLoweringRules
// together). A trait is not a Kubernetes kind, so each is
// `<name>.launcher.gokure.dev@v1`. Each call returns a new map.
func TraitTypeIDs() map[string]oam.TypeID {
	ids := make(map[string]oam.TypeID)
	for name := range TraitHandlers() {
		ids[name] = launcherTypeID(name)
	}
	for name := range TraitLoweringRules() {
		ids[name] = launcherTypeID(name)
	}
	return ids
}

// PolicyTypeIDs returns the identity every built-in policy type declares, keyed
// by the plain type name: `<name>.launcher.gokure.dev@v1`. Each call returns a
// new map.
func PolicyTypeIDs() map[string]oam.TypeID {
	ids := make(map[string]oam.TypeID)
	for name := range PolicyHandlers() {
		ids[name] = launcherTypeID(name)
	}
	return ids
}

// DeclareTypes declares on t the identity of every built-in type: ComponentTypeIDs,
// TraitTypeIDs and PolicyTypeIDs. Call it after t has registered the built-ins
// (the recipe above); it is refused, and t left as it was for that position, for a
// built-in type t does not hold, as when a consumer deleted one from the maps it
// registered. A consumer that did declares what it kept, from the same maps with
// its deletions made.
func DeclareTypes(t *oam.Transformer) error {
	if err := t.DeclareComponentTypes(ComponentTypeIDs()); err != nil {
		return err
	}
	if err := t.DeclareTraitTypes(TraitTypeIDs()); err != nil {
		return err
	}
	return t.DeclarePolicyTypes(PolicyTypeIDs())
}

func launcherTypeID(name string) oam.TypeID {
	return oam.TypeID{Name: name + "." + LauncherDomain, Version: launcherVersion}
}

func componentTypeID(name string, h oam.ComponentHandler) oam.TypeID {
	p, ok := h.(oam.ComponentObjectProvider)
	if !ok {
		return launcherTypeID(name)
	}
	gk, _ := p.ComponentObject()
	if strings.ToLower(gk.Kind) != name {
		return launcherTypeID(name)
	}
	version, ok := kindAPIVersion(gk.Group)
	if !ok {
		// A new kind component's group has no version here: add it to
		// groupAPIVersions, which TestTypeIDs_EveryKindGroupHasAVersion holds to.
		panic(fmt.Sprintf("registry: no API version declared for the group %q of the %s type", gk.Group, name))
	}
	if gk.Group == "" {
		return oam.TypeID{Name: name, Version: version}
	}
	return oam.TypeID{Name: name + "." + gk.Group, Version: version}
}

// groupAPIVersions is the API version each Kubernetes group's built-in kind
// components emit. A handler documents it as `upstream: "<group>/<version>
// <Kind>Spec"` (a kind component's decode target) or imports its API package;
// every kind of one group emits one version. Only the groups of a type named
// for its kind are here: a type named for a product (`cnpg-cluster`,
// `metallb-bgppeer`) is a launcher type and needs no group. A new kind
// component in another group panics until its group is added.
// TestTypeIDs_MatchTheLinkedAPIPackages compares each entry with the
// GroupVersion of the Go package the handlers use.
var groupAPIVersions = map[string]string{
	"":                             "v1",
	"apps":                         "v1",
	"batch":                        "v1",
	"autoscaling":                  "v2",
	"policy":                       "v1",
	"networking.k8s.io":            "v1",
	"storage.k8s.io":               "v1",
	"scheduling.k8s.io":            "v1",
	"node.k8s.io":                  "v1",
	"discovery.k8s.io":             "v1",
	"rbac.authorization.k8s.io":    "v1",
	"admissionregistration.k8s.io": "v1",
	"apiregistration.k8s.io":       "v1",
	"gateway.networking.k8s.io":    "v1",
	"cert-manager.io":              "v1",
	"external-secrets.io":          "v1",
	"monitoring.coreos.com":        "v1",
	"volsync.backube":              "v1alpha1",
	"source.toolkit.fluxcd.io":     "v1",
	"helm.toolkit.fluxcd.io":       "v2",
	"image.toolkit.fluxcd.io":      "v1",
	"source.extensions.fluxcd.io":  "v1beta1",
	"fluxcd.controlplane.io":       "v1",
}

func kindAPIVersion(group string) (string, bool) {
	v, ok := groupAPIVersions[group]
	return v, ok
}
