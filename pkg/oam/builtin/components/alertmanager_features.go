package components

import (
	"slices"
	"strings"

	"github.com/blang/semver/v4"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"

	"github.com/go-kure/launcher/pkg/errors"
)

// alertmanagerFeatureFlagsOfMinor are the names --enable-feature takes at one
// minor version of Alertmanager 0.x: its releases and prereleases take the
// same names.
type alertmanagerFeatureFlagsOfMinor struct {
	minor uint64
	names []string
}

// alertmanagerFeatureFlags are those names, by minor, from 0.27, the first the
// operator passes enableFeatures to (alertmanagerVersionGates), to 0.34.
//
// TestAlertmanagerFeatureFlags_MatchVendoredSource holds the table to
// Alertmanager's source: for each row, the names NewFlags takes in
// featurecontrol/featurecontrol.go of the minor's latest release, vendored by
// scripts/vendor-alertmanager-features.sh, are exactly the row's, and the
// vendored minors are exactly the rows'.
var alertmanagerFeatureFlags = []alertmanagerFeatureFlagsOfMinor{
	{27, []string{"classic-mode", "receiver-name-in-metrics", "utf8-strict-mode"}},
	{28, []string{"auto-gomaxprocs", "auto-gomemlimit", "classic-mode", "receiver-name-in-metrics", "utf8-strict-mode"}},
	{29, []string{"auto-gomaxprocs", "auto-gomemlimit", "classic-mode", "receiver-name-in-metrics", "utf8-strict-mode"}},
	{30, []string{"auto-gomaxprocs", "auto-gomemlimit", "classic-mode", "receiver-name-in-metrics", "utf8-strict-mode"}},
	{31, []string{"alert-names-in-metrics", "auto-gomaxprocs", "auto-gomemlimit", "classic-mode", "receiver-name-in-metrics", "utf8-strict-mode"}},
	{32, []string{"alert-names-in-metrics", "auto-gomaxprocs", "auto-gomemlimit", "classic-mode", "receiver-name-in-metrics", "utf8-strict-mode"}},
	{33, []string{"alert-names-in-metrics", "auto-gomemlimit", "classic-mode", "event-recorder", "group-key-in-metrics", "receiver-name-in-metrics", "utf8-strict-mode"}},
	{34, []string{"alert-names-in-metrics", "auto-gomemlimit", "classic-mode", "event-recorder", "group-key-in-metrics", "receiver-name-in-metrics", "utf8-strict-mode"}},
}

// refuseUnusableAlertmanagerFeatures refuses an enableFeatures Alertmanager
// exits on at startup, at version, the version the operator runs (the spec's,
// or its default where unset), written in messages as at. The operator passes
// the list from 0.27.0 on, joined with ",", as --enable-feature
// (pkg/alertmanager/statefulset.go:311-315 at prometheus-operator v0.94.1).
// Alertmanager takes an empty value as no feature; it splits any other on ","
// and fails on an element that is not a feature flag of its version, an empty
// one included, and on classic-mode with utf8-strict-mode (NewFlags,
// featurecontrol/featurecontrol.go:135-180 at v0.34.0), and then exits
// (cmd/alertmanager/main.go:104-108 at v0.34.0). So an element with a "," in
// it is read as two, and a list of one empty element is no feature. Below
// 0.27.0 the version gate has refused a nonempty list already; above the
// table's last minor the names are not known, and are not refused. No message
// names the value, as refuseUnservableExternalURL names none.
func refuseUnusableAlertmanagerFeatures(spec *monitoringv1.AlertmanagerSpec, version semver.Version, at string) error {
	joined := strings.Join(spec.EnableFeatures, ",")
	if joined == "" || version.Major != 0 {
		return nil
	}
	i := slices.IndexFunc(alertmanagerFeatureFlags, func(r alertmanagerFeatureFlagsOfMinor) bool { return r.minor == version.Minor })
	if i < 0 {
		return nil
	}
	names := alertmanagerFeatureFlags[i].names
	classic, utf8Strict := false, false
	for name := range strings.SplitSeq(joined, ",") {
		if !slices.Contains(names, name) {
			return errors.Errorf("enableFeatures: not a feature flag of Alertmanager 0.%d, at %s: the Prometheus operator passes the list joined with \",\" as --enable-feature, and Alertmanager exits at startup on an element it does not take, an empty one included; it takes %s", version.Minor, at, strings.Join(names, ", "))
		}
		classic = classic || name == "classic-mode"
		utf8Strict = utf8Strict || name == "utf8-strict-mode"
	}
	if classic && utf8Strict {
		return errors.New("enableFeatures: classic-mode with utf8-strict-mode: Alertmanager exits at startup on both; name one of them")
	}
	return nil
}
