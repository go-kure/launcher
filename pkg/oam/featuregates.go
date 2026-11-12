package oam

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/go-kure/launcher/pkg/errors"
)

// KubernetesVersion is a cluster's Kubernetes release at major.minor, the
// granularity at which feature gates change their default. A patch level is
// accepted when parsing and dropped, so the string a profile carries can be the
// one `helm template --kube-version` takes.
type KubernetesVersion struct {
	Major, Minor int
}

// String returns "major.minor", the form a refusal and the docs use.
func (v KubernetesVersion) String() string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

// AtLeast reports whether v is the same release as o or a later one.
func (v KubernetesVersion) AtLeast(o KubernetesVersion) bool {
	if v.Major != o.Major {
		return v.Major > o.Major
	}
	return v.Minor >= o.Minor
}

// ParseKubernetesVersion parses "1.35", "v1.35" or "v1.35.2" (a patch part is read
// and ignored; a pre-release or build suffix is refused). Only major 1 is accepted:
// the Kubernetes API this library models has no other major, and a "2.x" would make
// every gate row's comparison meaningless.
//
// A minor above the k8s.io/api version launcher is built against is accepted: a
// newer cluster is a valid target, and every gate the table knows is then on. The
// table cannot know a gate introduced after it was last read, so a newer version
// asserts nothing about those.
func ParseKubernetesVersion(s string) (KubernetesVersion, error) {
	parts := strings.Split(strings.TrimPrefix(s, "v"), ".")
	if len(parts) < 2 || len(parts) > 3 {
		return KubernetesVersion{}, errors.Errorf("%q is not a Kubernetes version: expected major.minor, such as 1.35", s)
	}
	nums := make([]int, len(parts))
	for i, p := range parts {
		// Atoi accepts a sign and a leading zero; neither is a version part.
		if p == "" || strings.TrimLeft(p, "0123456789") != "" || (len(p) > 1 && p[0] == '0') {
			return KubernetesVersion{}, errors.Errorf("%q is not a Kubernetes version: %q is not a number", s, p)
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return KubernetesVersion{}, errors.Errorf("%q is not a Kubernetes version: %q is not a number", s, p)
		}
		nums[i] = n
	}
	if nums[0] != 1 {
		return KubernetesVersion{}, errors.Errorf("%q is not a supported Kubernetes version: the major version must be 1", s)
	}
	return KubernetesVersion{Major: nums[0], Minor: nums[1]}, nil
}

// featureGate is one row of the lifecycle table: a Kubernetes feature gate that
// gates a field launcher authors, and the release where it is on by default.
type featureGate struct {
	// Name is the gate's name in Kubernetes, the value of PropertySchema.Gate.
	Name string
	// OnByDefault is the first release where the gate defaults to on (beta or
	// later). Before it the gate is off by default (alpha) or does not exist.
	OnByDefault KubernetesVersion
	// Source is where the row was read, so a refresh knows what to re-read.
	Source string
}

// featureGateSource is the upstream reference every row below was read from: the
// Default column of each gate's version ranges, not its Stage. A gate can turn on
// by default at a release other than the one that names its beta stage.
const featureGateSource = "https://kubernetes.io/docs/reference/command-line-tools-reference/feature-gates/ (Default column, Since)"

// featureGates is the lifecycle table. Each row says when a gate the schemas name
// turns on by default; a profile whose target version is below it has the gated
// field refused (go-kure/launcher#1183). Rows are read from upstream, never from
// memory, and re-read whenever the k8s.io/api pin moves (DEVELOPMENT.md,
// Renovate Management).
//
// Two tests hold the table to the schemas: every PropertySchema.Gate the built-in
// handlers publish names a row, and every row is used by at least one schema or
// listed in CodeCheckedGates.
var featureGates = []featureGate{
	// Alpha (default off) 1.34, beta (default on) 1.35.
	{Name: "EnvFiles", OnByDefault: KubernetesVersion{1, 35}, Source: featureGateSource},
	// Alpha 1.34, beta 1.35.
	{Name: "ContainerRestartRules", OnByDefault: KubernetesVersion{1, 35}, Source: featureGateSource},
	// Alpha 1.32 to 1.33, beta 1.34.
	{Name: "PodLevelResources", OnByDefault: KubernetesVersion{1, 34}, Source: featureGateSource},
	// Alpha 1.34, beta 1.35 to 1.36, stable 1.37.
	{Name: "HostnameOverride", OnByDefault: KubernetesVersion{1, 35}, Source: featureGateSource},
	// Alpha 1.33, beta 1.34, stable 1.35. Gates two Service trafficDistribution
	// values, not a whole field (PropertySchema.ValueGates, go-kure/launcher#1230).
	{Name: "PreferSameTrafficDistribution", OnByDefault: KubernetesVersion{1, 34}, Source: featureGateSource},
	// Alpha 1.34 to 1.35, beta 1.36, stable 1.37. With it off a Service name is a
	// DNS-1035 label and must start with a letter. No schema names it: code checks the
	// names of the Services it generates (CodeCheckedGates, go-kure/launcher#1230).
	{Name: serviceNameGate, OnByDefault: KubernetesVersion{1, 36}, Source: featureGateSource},
}

// FeatureGateNames returns the gates the lifecycle table knows, in table order. A
// test in a package that can see the built-in schemas holds them to the
// PropertySchema.Gate values those carry.
func FeatureGateNames() []string {
	names := make([]string, len(featureGates))
	for i, g := range featureGates {
		names[i] = g.Name
	}
	return names
}

// FeatureGateOnByDefault returns the first release where the named gate defaults to
// on, and false when the table has no such gate.
func FeatureGateOnByDefault(name string) (KubernetesVersion, bool) {
	g, ok := lookupFeatureGate(name)
	return g.OnByDefault, ok
}

// lookupFeatureGate returns the table row named name.
func lookupFeatureGate(name string) (featureGate, bool) {
	for _, g := range featureGates {
		if g.Name == name {
			return g, true
		}
	}
	return featureGate{}, false
}

// gateEnabled reports whether gate is on by default at version. overrides is the
// profile's spec.featureGates (go-kure/launcher#1231); an entry wins over the table. An unknown gate is an error: a
// schema naming a gate the table lacks is a launcher defect, never a pass.
func gateEnabled(gate string, version KubernetesVersion, overrides map[string]bool) (bool, error) {
	if on, ok := overrides[gate]; ok {
		return on, nil
	}
	g, ok := lookupFeatureGate(gate)
	if !ok {
		return false, errors.Errorf("feature gate %q is not in the lifecycle table", gate)
	}
	return version.AtLeast(g.OnByDefault), nil
}

// gateTarget is the cluster a document is built for, as far as the gate checks read
// it: the release its profile states and the gates the profile sets by hand
// (spec.featureGates, go-kure/launcher#1231). A gate in overrides is as it says; any
// other follows the lifecycle table at version. The zero overrides is the table.
type gateTarget struct {
	version   KubernetesVersion
	overrides map[string]bool
}

// String is the release, the form the refusals have always printed.
func (g gateTarget) String() string { return g.version.String() }

// enabled reports whether gate is on for this cluster (gateEnabled).
func (g gateTarget) enabled(gate string) (bool, error) {
	return gateEnabled(gate, g.version, g.overrides)
}

// setOff reports whether the profile sets gate off by hand, so a refusal names that
// setting and not the release it would not have followed.
func (g gateTarget) setOff(gate string) bool {
	on, set := g.overrides[gate]
	return set && !on
}
