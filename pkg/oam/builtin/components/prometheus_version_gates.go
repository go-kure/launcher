package components

import (
	"slices"
	"strconv"
	"strings"

	"github.com/blang/semver/v4"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"

	"github.com/go-kure/launcher/pkg/errors"
)

// The version gates of the prometheus kind (go-kure/launcher#980; the class
// is described in monitoring_version_gates.go): the fields of
// monitoringv1.PrometheusSpec the Prometheus operator reads only from some
// Prometheus version on, or only below one, and the fields of its Thanos
// sidecar it reads only from some Thanos version on, at prometheus-operator
// v0.94.1.
//
// TestPrometheusVersionGates_MatchVendoredSource holds the tables to the
// operator's source: every comparison of a version in the operator's packages
// pkg/prometheus, pkg/prometheus/server and pkg/prometheus/validation,
// vendored whole, is either a row here, at its bound, or listed in the test
// with the reason it is not this kind's (a field of a ServiceMonitor,
// PodMonitor, Probe or ScrapeConfig object, a choice of flag spelling, or no
// field at all). It holds the default versions and the floor to the same
// source.
//
// The operator's compatibility matrix starts at v2.45.0
// (PrometheusCompatibilityMatrix, pkg/operator/defaults.go). It still reads
// and compares versions below it, and the tables keep the gates under it: a
// version there is outside the range the operator is tested with, but the
// object states what the operator does with it.

const (
	// prometheusDefaultVersion is the version the operator configures for
	// where version is unset (DefaultPrometheusVersion, the last entry of
	// PrometheusCompatibilityMatrix, pkg/operator/defaults.go at v0.94.1).
	prometheusDefaultVersion = "v3.14.0"
	// prometheusThanosDefaultVersion is the version the operator configures
	// the Thanos sidecar for where thanos.version is unset
	// (DefaultThanosVersion, pkg/operator/defaults.go at v0.94.1).
	prometheusThanosDefaultVersion = "v0.42.4"
	// prometheusMajor2xMaximum is the least version of major 3: 3.0.0-0, as
	// 3.0.0-rc.0 is already of major 3 for a check of the major alone.
	prometheusMajor2xMaximum = "3.0.0-0"
)

const (
	// The consequences under a minimum, or from a maximum on, by what the
	// operator does there.
	promFlagDropped  = "does not pass it to Prometheus, and the pods run without it"
	promKeyDropped   = "drops it from the configuration it generates, and Prometheus runs without it"
	promRefused      = "refuses the object at reconcile, and the Prometheus is not updated"
	promWebPlainHTTP = "mounts no web configuration, and the web endpoint serves plain HTTP without the authored settings"
	// promWebTLSUnserved: the config reloader and the Thanos sidecar
	// (PrometheusURIScheme) turn to HTTPS on a tlsConfig at any version, and
	// so do the probes (BuildProbes) but under listenLocal, where they are an
	// exec of plain HTTP.
	promWebTLSUnserved = "mounts no web configuration, so the web endpoint serves plain HTTP while the config reloader and the Thanos sidecar it configures speak HTTPS to it, and their requests to it fail; without listenLocal the probes fail too and the pods never become ready"
	// promOperatorExits: nothing recovers a panic of Sync, which runs in an
	// errgroup goroutine (pkg/operator/resource_reconciler.go).
	promOperatorExits = ", and the operator exits, to panic again on each restart, so it reconciles only between crashes"
	// promAuthorizationPanics: addAuthorizationToYaml indexes the first item
	// of what addSafeAuthorizationToYaml returns, which is empty below
	// 2.26.0.
	promAuthorizationPanics = "panics while generating the configuration" + promOperatorExits
	promScrapeDropped       = "drops it from the scrape configurations it generates, and the targets are scraped without it"
	promSidecarDropped      = "does not pass it to the Thanos sidecar, and the sidecar runs without it"
)

// promAlertmanagers are the endpoints of alerting, none where alerting is not
// authored.
func promAlertmanagers(s *monitoringv1.PrometheusSpec) []monitoringv1.AlertmanagerEndpoints {
	if s.Alerting == nil {
		return nil
	}
	return s.Alerting.Alertmanagers
}

// anyOf reports whether f holds for an item of items.
func anyOf[T any](items []T, f func(*T) bool) bool {
	for i := range items {
		if f(&items[i]) {
			return true
		}
	}
	return false
}

// each is the non-nil values of f over items.
func each[T, U any](items []T, f func(*T) *U) []*U {
	var out []*U
	for i := range items {
		if v := f(&items[i]); v != nil {
			out = append(out, v)
		}
	}
	return out
}

// one is v alone, none where it is nil.
func one[T any](v *T) []*T {
	if v == nil {
		return nil
	}
	return []*T{v}
}

// safeTLS is the SafeTLSConfig of a TLSConfig, nil where it is nil.
func safeTLS(t *monitoringv1.TLSConfig) *monitoringv1.SafeTLSConfig {
	if t == nil {
		return nil
	}
	return &t.SafeTLSConfig
}

// promRemoteReadOAuth2 and promRemoteWriteOAuth2 are the oauth2 blocks of
// remoteRead and remoteWrite.
func promRemoteReadOAuth2(s *monitoringv1.PrometheusSpec) []*monitoringv1.OAuth2 {
	return each(s.RemoteRead, func(r *monitoringv1.RemoteReadSpec) *monitoringv1.OAuth2 { return r.OAuth2 })
}

func promRemoteWriteOAuth2(s *monitoringv1.PrometheusSpec) []*monitoringv1.OAuth2 {
	return each(s.RemoteWrite, func(r *monitoringv1.RemoteWriteSpec) *monitoringv1.OAuth2 { return r.OAuth2 })
}

// promTLSConfigs are the TLS settings of the spec the operator writes through
// addSafeTLStoYaml (pkg/prometheus/promcfg.go), by path: the scrape classes'
// as the fallback of a monitor without its own.
var promTLSConfigs = []struct {
	path string
	of   func(s *monitoringv1.PrometheusSpec) []*monitoringv1.SafeTLSConfig
}{
	{"apiserverConfig.tlsConfig", func(s *monitoringv1.PrometheusSpec) []*monitoringv1.SafeTLSConfig {
		if s.APIServerConfig == nil {
			return nil
		}
		return one(safeTLS(s.APIServerConfig.TLSConfig))
	}},
	{"alerting.alertmanagers[].tlsConfig", func(s *monitoringv1.PrometheusSpec) []*monitoringv1.SafeTLSConfig {
		return each(promAlertmanagers(s), func(a *monitoringv1.AlertmanagerEndpoints) *monitoringv1.SafeTLSConfig { return safeTLS(a.TLSConfig) })
	}},
	{"remoteRead[].tlsConfig", func(s *monitoringv1.PrometheusSpec) []*monitoringv1.SafeTLSConfig {
		return each(s.RemoteRead, func(r *monitoringv1.RemoteReadSpec) *monitoringv1.SafeTLSConfig { return safeTLS(r.TLSConfig) })
	}},
	{"remoteWrite[].tlsConfig", func(s *monitoringv1.PrometheusSpec) []*monitoringv1.SafeTLSConfig {
		return each(s.RemoteWrite, func(r *monitoringv1.RemoteWriteSpec) *monitoringv1.SafeTLSConfig { return safeTLS(r.TLSConfig) })
	}},
	{"remoteRead[].oauth2.tlsConfig", func(s *monitoringv1.PrometheusSpec) []*monitoringv1.SafeTLSConfig {
		return each(promRemoteReadOAuth2(s), func(o **monitoringv1.OAuth2) *monitoringv1.SafeTLSConfig { return (*o).TLSConfig })
	}},
	{"remoteWrite[].oauth2.tlsConfig", func(s *monitoringv1.PrometheusSpec) []*monitoringv1.SafeTLSConfig {
		return each(promRemoteWriteOAuth2(s), func(o **monitoringv1.OAuth2) *monitoringv1.SafeTLSConfig { return (*o).TLSConfig })
	}},
	{"tracingConfig.tlsConfig", func(s *monitoringv1.PrometheusSpec) []*monitoringv1.SafeTLSConfig {
		if s.TracingConfig == nil {
			return nil
		}
		return one(safeTLS(s.TracingConfig.TLSConfig))
	}},
	{"scrapeClasses[].tlsConfig", func(s *monitoringv1.PrometheusSpec) []*monitoringv1.SafeTLSConfig {
		return each(s.ScrapeClasses, func(c *monitoringv1.ScrapeClass) *monitoringv1.SafeTLSConfig { return safeTLS(c.TLSConfig) })
	}},
}

// promProxyConfigs are the proxy settings of the spec the operator writes
// through addProxyConfigtoYaml, by the path that holds them inline.
var promProxyConfigs = []struct {
	path string
	of   func(s *monitoringv1.PrometheusSpec) []*monitoringv1.ProxyConfig
}{
	{"apiserverConfig", func(s *monitoringv1.PrometheusSpec) []*monitoringv1.ProxyConfig {
		if s.APIServerConfig == nil {
			return nil
		}
		return one(&s.APIServerConfig.ProxyConfig)
	}},
	{"alerting.alertmanagers[]", func(s *monitoringv1.PrometheusSpec) []*monitoringv1.ProxyConfig {
		return each(promAlertmanagers(s), func(a *monitoringv1.AlertmanagerEndpoints) *monitoringv1.ProxyConfig { return &a.ProxyConfig })
	}},
	{"remoteRead[]", func(s *monitoringv1.PrometheusSpec) []*monitoringv1.ProxyConfig {
		return each(s.RemoteRead, func(r *monitoringv1.RemoteReadSpec) *monitoringv1.ProxyConfig { return &r.ProxyConfig })
	}},
	{"remoteWrite[]", func(s *monitoringv1.PrometheusSpec) []*monitoringv1.ProxyConfig {
		return each(s.RemoteWrite, func(r *monitoringv1.RemoteWriteSpec) *monitoringv1.ProxyConfig { return &r.ProxyConfig })
	}},
	{"remoteRead[].oauth2", func(s *monitoringv1.PrometheusSpec) []*monitoringv1.ProxyConfig {
		return each(promRemoteReadOAuth2(s), func(o **monitoringv1.OAuth2) *monitoringv1.ProxyConfig { return &(*o).ProxyConfig })
	}},
	{"remoteWrite[].oauth2", func(s *monitoringv1.PrometheusSpec) []*monitoringv1.ProxyConfig {
		return each(promRemoteWriteOAuth2(s), func(o **monitoringv1.OAuth2) *monitoringv1.ProxyConfig { return &(*o).ProxyConfig })
	}},
}

// promRelabelLists are the relabeling lists of the spec that the operator
// validates (validation.LabelConfigValidator): an alerting endpoint's
// (pkg/prometheus/server/operator.go) and a scrape class's
// (pkg/prometheus/promcfg.go).
var promRelabelLists = []struct {
	path string
	of   func(s *monitoringv1.PrometheusSpec) [][]monitoringv1.RelabelConfig
}{
	{"alerting.alertmanagers[].relabelings", func(s *monitoringv1.PrometheusSpec) [][]monitoringv1.RelabelConfig {
		var out [][]monitoringv1.RelabelConfig
		for _, a := range promAlertmanagers(s) {
			out = append(out, a.RelabelConfigs)
		}
		return out
	}},
	{"alerting.alertmanagers[].alertRelabelings", func(s *monitoringv1.PrometheusSpec) [][]monitoringv1.RelabelConfig {
		var out [][]monitoringv1.RelabelConfig
		for _, a := range promAlertmanagers(s) {
			out = append(out, a.AlertRelabelConfigs)
		}
		return out
	}},
	{"scrapeClasses[].relabelings", func(s *monitoringv1.PrometheusSpec) [][]monitoringv1.RelabelConfig {
		var out [][]monitoringv1.RelabelConfig
		for _, c := range s.ScrapeClasses {
			out = append(out, c.Relabelings)
		}
		return out
	}},
	{"scrapeClasses[].metricRelabelings", func(s *monitoringv1.PrometheusSpec) [][]monitoringv1.RelabelConfig {
		var out [][]monitoringv1.RelabelConfig
		for _, c := range s.ScrapeClasses {
			out = append(out, c.MetricRelabelings)
		}
		return out
	}},
}

// promGate is an idGate of the prometheus kind; an empty id is the path.
func promGate(id, path, minimum, below string, authored func(s *monitoringv1.PrometheusSpec) bool) idGate[monitoringv1.PrometheusSpec] {
	return idGate[monitoringv1.PrometheusSpec]{id, versionGate[monitoringv1.PrometheusSpec]{path, minimum, below, authored}}
}

// positive reports whether a limit is set above 0.
func positive(p *int64) bool { return p != nil && *p > 0 }

// prometheusVersionGates are the fields the operator reads only from some
// Prometheus version on, by id. Where the operator tests a field beside
// another condition, authored tests the same: a logFormat of logfmt is passed
// at no version, and serviceDiscoveryRole is read only as EndpointSlice.
var prometheusVersionGates = func() []idGate[monitoringv1.PrometheusSpec] {
	type spec = monitoringv1.PrometheusSpec
	type rw = monitoringv1.RemoteWriteSpec
	type rr = monitoringv1.RemoteReadSpec
	type am = monitoringv1.AlertmanagerEndpoints
	g := func(path, minimum, below string, authored func(s *spec) bool) idGate[spec] {
		return promGate("", path, minimum, below, authored)
	}
	remoteWrite := func(field, minimum, below string, authored func(r *rw) bool) idGate[spec] {
		return g("remoteWrite[]."+field, minimum, below, func(s *spec) bool { return anyOf(s.RemoteWrite, authored) })
	}
	remoteRead := func(field, minimum, below string, authored func(r *rr) bool) idGate[spec] {
		return g("remoteRead[]."+field, minimum, below, func(s *spec) bool { return anyOf(s.RemoteRead, authored) })
	}
	alertmanager := func(field, minimum, below string, authored func(a *am) bool) idGate[spec] {
		return g("alerting.alertmanagers[]."+field, minimum, below, func(s *spec) bool { return anyOf(promAlertmanagers(s), authored) })
	}
	azure := func(field, minimum, below string, authored func(a *monitoringv1.AzureAD) bool) idGate[spec] {
		return remoteWrite("azureAd."+field, minimum, below, func(r *rw) bool { return r.AzureAD != nil && authored(r.AzureAD) })
	}
	gates := []idGate[spec]{
		// The command-line flags of the prometheus container
		// (BuildCommonPrometheusArgs, pkg/prometheus/promcfg.go, and
		// buildServerArgs, pkg/prometheus/server/statefulset.go).
		g("web.pageTitle", "2.6.0", promFlagDropped, func(s *spec) bool { return s.Web != nil && s.Web.PageTitle != nil }),
		g("logFormat", "2.6.0", "does not pass it to Prometheus, which runs without it", func(s *spec) bool { return s.LogFormat != "" && s.LogFormat != "logfmt" }),
		g("enableFeatures", "2.25.0", promFlagDropped, func(s *spec) bool { return len(s.EnableFeatures) > 0 }),
		g("enableRemoteWriteReceiver", "2.33.0", promFlagDropped, func(s *spec) bool { return s.EnableRemoteWriteReceiver }),
		g("remoteWriteReceiverMessageVersions", "2.54.0", promFlagDropped, func(s *spec) bool {
			return s.EnableRemoteWriteReceiver && len(s.RemoteWriteReceiverMessageVersions) > 0
		}),
		g("enableOTLPReceiver", "2.47.0", promFlagDropped, func(s *spec) bool { return s.EnableOTLPReceiver != nil && *s.EnableOTLPReceiver }),
		g("rules.alert.forOutageTolerance", "2.4.0", promFlagDropped, func(s *spec) bool { return s.Rules.Alert.ForOutageTolerance != "" }),
		g("rules.alert.forGracePeriod", "2.4.0", promFlagDropped, func(s *spec) bool { return s.Rules.Alert.ForGracePeriod != "" }),
		g("rules.alert.resendDelay", "2.4.0", promFlagDropped, func(s *spec) bool { return s.Rules.Alert.ResendDelay != "" }),
		g("query.maxSamples", "2.5.0", promFlagDropped, func(s *spec) bool {
			return s.Query != nil && s.Query.MaxSamples != nil && *s.Query.MaxSamples > 0
		}),
		g("retentionSize", "2.7.0", promFlagDropped, func(s *spec) bool { return s.RetentionSize != "" }),
		g("allowOverlappingBlocks", "2.11.0", promFlagDropped, func(s *spec) bool {
			return s.AllowOverlappingBlocks //nolint:staticcheck // SA1019: deprecated upstream, still authorable and read from 2.11.0 to below 2.39.0.
		}),
		g("walCompression", "2.11.0", promFlagDropped, func(s *spec) bool { return s.WALCompression != nil }),
		g("web.tlsConfig", "2.24.0", promWebTLSUnserved, func(s *spec) bool { return s.Web != nil && s.Web.TLSConfig != nil }),
		g("web.httpConfig", "2.24.0", promWebPlainHTTP, func(s *spec) bool { return s.Web != nil && s.Web.HTTPConfig != nil }),
		g("tsdb.chunkEncoding", "3.13.0", promKeyDropped, func(s *spec) bool {
			return s.TSDB != nil && s.TSDB.ChunkEncoding != nil && s.TSDB.ChunkEncoding.Floats != nil
		}),

		// The global configuration and the settings of its own sections
		// (pkg/prometheus/promcfg.go).
		promGate("serviceDiscoveryRole=EndpointSlice", "serviceDiscoveryRole", "2.21.0",
			"discovers the targets of ServiceMonitors and the Alertmanager endpoints through Endpoints, not EndpointSlices", func(s *spec) bool {
				return s.ServiceDiscoveryRole != nil && *s.ServiceDiscoveryRole == monitoringv1.EndpointSliceRole
			}),
		g("overrideHonorTimestamps", "2.9.0", promScrapeDropped, func(s *spec) bool { return s.OverrideHonorTimestamps }),
		promGate("scrapeProtocols=PrometheusText1.0.0", "scrapeProtocols", "3.0.0-rc.0",
			"drops PrometheusText1.0.0 from the scrape protocols it generates, and Prometheus does not negotiate it", func(s *spec) bool {
				return slices.Contains(s.ScrapeProtocols, monitoringv1.PrometheusText1_0_0)
			}),
		g("scrapeProtocols", "2.49.0", promKeyDropped, func(s *spec) bool { return len(s.ScrapeProtocols) > 0 }),
		g("scrapeClasses[].fallbackScrapeProtocol", "3.0.0", promScrapeDropped, func(s *spec) bool {
			return anyOf(s.ScrapeClasses, func(c *monitoringv1.ScrapeClass) bool { return c.FallbackScrapeProtocol != nil })
		}),
		promGate("scrapeClasses[].attachMetadata@podMonitor", "scrapeClasses[].attachMetadata", "2.35.0",
			"drops it from the scrape configurations of the PodMonitors that take it from their scrape class", func(s *spec) bool {
				return anyOf(s.ScrapeClasses, func(c *monitoringv1.ScrapeClass) bool { return c.AttachMetadata != nil })
			}),
		promGate("scrapeClasses[].attachMetadata@serviceMonitor", "scrapeClasses[].attachMetadata", "2.37.0",
			"drops it from the scrape configurations of the ServiceMonitors that take it from their scrape class", func(s *spec) bool {
				return anyOf(s.ScrapeClasses, func(c *monitoringv1.ScrapeClass) bool { return c.AttachMetadata != nil })
			}),
		// A limit of 0 is written at no version: appendGlobalLimits writes a
		// limit, and getLimit takes an enforced one, only above 0
		// (pkg/prometheus/promcfg.go).
		g("enforcedTargetLimit", "2.21.0", promScrapeDropped, func(s *spec) bool { return positive(s.EnforcedTargetLimit) }),
		g("enforcedLabelLimit", "2.27.0", promScrapeDropped, func(s *spec) bool { return positive(s.EnforcedLabelLimit) }),
		g("enforcedLabelNameLengthLimit", "2.27.0", promScrapeDropped, func(s *spec) bool { return positive(s.EnforcedLabelNameLengthLimit) }),
		g("enforcedLabelValueLengthLimit", "2.27.0", promScrapeDropped, func(s *spec) bool { return positive(s.EnforcedLabelValueLengthLimit) }),
		g("enforcedKeepDroppedTargets", "2.47.0", promScrapeDropped, func(s *spec) bool { return positive(s.EnforcedKeepDroppedTargets) }),
		g("enforcedBodySizeLimit", "2.28.0", promScrapeDropped, func(s *spec) bool { return s.EnforcedBodySizeLimit != "" }),
		g("bodySizeLimit", "2.45.0", promKeyDropped, func(s *spec) bool { return s.BodySizeLimit != nil }),
		g("sampleLimit", "2.45.0", promKeyDropped, func(s *spec) bool { return positive(s.SampleLimit) }),
		g("targetLimit", "2.45.0", promKeyDropped, func(s *spec) bool { return positive(s.TargetLimit) }),
		g("labelLimit", "2.45.0", promKeyDropped, func(s *spec) bool { return positive(s.LabelLimit) }),
		g("labelNameLengthLimit", "2.45.0", promKeyDropped, func(s *spec) bool { return positive(s.LabelNameLengthLimit) }),
		g("labelValueLengthLimit", "2.45.0", promKeyDropped, func(s *spec) bool { return positive(s.LabelValueLengthLimit) }),
		g("keepDroppedTargets", "2.47.0", promKeyDropped, func(s *spec) bool { return positive(s.KeepDroppedTargets) }),
		g("exemplars", "2.29.0", promKeyDropped, func(s *spec) bool { return s.Exemplars != nil && s.Exemplars.MaxSize != nil }),
		g("tsdb.outOfOrderTimeWindow", "2.39.0", promKeyDropped, func(s *spec) bool { return s.TSDB != nil && s.TSDB.OutOfOrderTimeWindow != nil }),
		g("tsdb.staleSeriesCompactionThreshold", "3.10.0", promKeyDropped, func(s *spec) bool {
			return s.TSDB != nil && s.TSDB.StaleSeriesCompactionThreshold != nil
		}),
		g("retentionPercentage", "3.11.0", promKeyDropped, func(s *spec) bool { return s.RetentionPercentage != nil }),
		g("ruleQueryOffset", "2.53.0", promKeyDropped, func(s *spec) bool { return s.RuleQueryOffset != nil }),
		g("queryLogFile", "2.16.0", promKeyDropped, func(s *spec) bool { return s.QueryLogFile != "" }),
		g("scrapeFailureLogFile", "2.55.0", promKeyDropped, func(s *spec) bool { return s.ScrapeFailureLogFile != nil }),
		g("nameValidationScheme", "3.0.0", promKeyDropped, func(s *spec) bool { return s.NameValidationScheme != nil }),
		g("nameEscapingScheme", "3.4.0", promKeyDropped, func(s *spec) bool { return s.NameEscapingScheme != nil }),
		g("convertClassicHistogramsToNHCB", "3.4.0", promKeyDropped, func(s *spec) bool { return s.ConvertClassicHistogramsToNHCB != nil }),
		g("scrapeClassicHistograms", "3.5.0", promKeyDropped, func(s *spec) bool { return s.ScrapeClassicHistograms != nil }),
		g("scrapeNativeHistograms", "3.8.0", promKeyDropped, func(s *spec) bool { return s.ScrapeNativeHistograms != nil }),

		// otlp, refused at reconcile below 2.55.0 (appendOTLPConfig).
		g("otlp", "2.55.0", promRefused, func(s *spec) bool { return s.OTLP != nil }),
		g("otlp.promoteResourceAttributes", "2.55.0", promKeyDropped, func(s *spec) bool {
			return s.OTLP != nil && len(s.OTLP.PromoteResourceAttributes) > 0
		}),
		g("otlp.translationStrategy", "3.0.0", promKeyDropped, func(s *spec) bool { return s.OTLP != nil && s.OTLP.TranslationStrategy != nil }),
		promGate("otlp.translationStrategy=NoTranslation", "otlp.translationStrategy", "3.4.0", promRefused, func(s *spec) bool {
			return s.OTLP != nil && s.OTLP.TranslationStrategy != nil && *s.OTLP.TranslationStrategy == monitoringv1.NoTranslation
		}),
		promGate("otlp.translationStrategy=UnderscoreEscapingWithoutSuffixes", "otlp.translationStrategy", "3.6.0", promRefused, func(s *spec) bool {
			return s.OTLP != nil && s.OTLP.TranslationStrategy != nil && *s.OTLP.TranslationStrategy == monitoringv1.UnderscoreEscapingWithoutSuffixes
		}),
		g("otlp.keepIdentifyingResourceAttributes", "3.1.0", promKeyDropped, func(s *spec) bool {
			return s.OTLP != nil && s.OTLP.KeepIdentifyingResourceAttributes != nil
		}),
		g("otlp.convertHistogramsToNHCB", "3.4.0", promKeyDropped, func(s *spec) bool { return s.OTLP != nil && s.OTLP.ConvertHistogramsToNHCB != nil }),
		g("otlp.promoteAllResourceAttributes", "3.5.0", promKeyDropped, func(s *spec) bool {
			return s.OTLP != nil && s.OTLP.PromoteAllResourceAttributes != nil
		}),
		g("otlp.ignoreResourceAttributes", "3.5.0", promKeyDropped, func(s *spec) bool {
			return s.OTLP != nil && len(s.OTLP.IgnoreResourceAttributes) > 0
		}),
		g("otlp.promoteScopeMetadata", "3.6.0", promKeyDropped, func(s *spec) bool { return s.OTLP != nil && s.OTLP.PromoteScopeMetadata != nil }),
		g("otlp.labelNameUnderscoreSanitization", "3.8.0", promKeyDropped, func(s *spec) bool {
			return s.OTLP != nil && s.OTLP.LabelNameUnderscoreSanitization != nil
		}),
		g("otlp.labelNamePreserveMultipleUnderscores", "3.8.0", promKeyDropped, func(s *spec) bool {
			return s.OTLP != nil && s.OTLP.LabelNamePreserveMultipleUnderscores != nil
		}),

		// The Alertmanager endpoints (generateAlertmanagerConfig).
		alertmanager("enableHttp2", "2.35.0", promKeyDropped, func(a *am) bool { return a.EnableHttp2 != nil }),
		alertmanager("basicAuth", "2.26.0", promKeyDropped, func(a *am) bool { return a.BasicAuth != nil }),
		alertmanager("authorization", "2.26.0", promKeyDropped, func(a *am) bool { return a.Authorization != nil }),
		alertmanager("sigv4", "2.48.0", promKeyDropped, func(a *am) bool { return a.Sigv4 != nil }),
		alertmanager("sigv4.externalId", "3.11.0", promKeyDropped, func(a *am) bool { return a.Sigv4 != nil && a.Sigv4.ExternalID != "" }),
		alertmanager("sigv4.useFIPSSTSEndpoint", "2.54.0", promKeyDropped, func(a *am) bool {
			return a.Sigv4 != nil && a.Sigv4.UseFIPSSTSEndpoint != nil
		}),
		alertmanager("apiVersion", "2.11.0", promKeyDropped, func(a *am) bool { return promAlertmanagerAPIVersion(a) != "" }),
		alertmanager("alertRelabelings", "2.51.0", promKeyDropped, func(a *am) bool { return len(a.AlertRelabelConfigs) > 0 }),

		// The remote read endpoints (generateRemoteReadConfig).
		remoteRead("name", "2.15.0", promKeyDropped, func(r *rr) bool { return r.Name != "" }),
		remoteRead("headers", "2.26.0", promKeyDropped, func(r *rr) bool { return len(r.Headers) > 0 }),
		remoteRead("authorization", "2.26.0", promAuthorizationPanics, func(r *rr) bool { return r.Authorization != nil }),
		remoteRead("followRedirects", "2.26.0", promKeyDropped, func(r *rr) bool { return r.FollowRedirects != nil }),
		remoteRead("oauth2", "2.27.0", promKeyDropped, func(r *rr) bool { return r.OAuth2 != nil }),
		remoteRead("filterExternalLabels", "2.34.0", promKeyDropped, func(r *rr) bool { return r.FilterExternalLabels != nil }),

		// The remote write endpoints (GenerateRemoteWriteConfig), and the
		// Azure AD settings checked at reconcile (validateRemoteWriteSpec,
		// pkg/prometheus/operator.go).
		remoteWrite("headers", "2.15.0", promKeyDropped, func(r *rw) bool { return len(r.Headers) > 0 }),
		remoteWrite("name", "2.15.0", promKeyDropped, func(r *rw) bool { return r.Name != nil && *r.Name != "" }),
		remoteWrite("messageVersion", "2.54.0", promKeyDropped, func(r *rw) bool { return r.MessageVersion != nil }),
		remoteWrite("sendExemplars", "2.27.0", promKeyDropped, func(r *rw) bool { return r.SendExemplars != nil }),
		remoteWrite("sendNativeHistograms", "2.40.0", promKeyDropped, func(r *rw) bool { return r.SendNativeHistograms != nil }),
		remoteWrite("authorization", "2.26.0", promAuthorizationPanics, func(r *rw) bool { return r.Authorization != nil }),
		remoteWrite("oauth2", "2.27.0", promKeyDropped, func(r *rw) bool { return r.OAuth2 != nil }),
		remoteWrite("sigv4", "2.26.0", promKeyDropped, func(r *rw) bool { return r.Sigv4 != nil }),
		remoteWrite("sigv4.externalId", "3.11.0", promKeyDropped, func(r *rw) bool { return r.Sigv4 != nil && r.Sigv4.ExternalID != "" }),
		remoteWrite("sigv4.useFIPSSTSEndpoint", "2.54.0", promKeyDropped, func(r *rw) bool {
			return r.Sigv4 != nil && r.Sigv4.UseFIPSSTSEndpoint != nil
		}),
		// The first gate missed is the one refused: the operator refuses an
		// authentication method below its version at reconcile, before it
		// generates any configuration, so those gates precede azureAd's own,
		// which it drops below 2.45.0. An azureAd with no method, which the
		// operator refuses at every version, is refused ahead of every gate
		// (refuseAzureADWithoutMethod), so the gate of azureAd fires only on
		// one with a method.
		promGate(`remoteWrite[].azureAd.managedIdentity.clientId=""`, "remoteWrite[].azureAd.managedIdentity", "3.5.0",
			"refuses one without a clientId at reconcile, and the Prometheus is not updated", func(s *spec) bool {
				return anyOf(s.RemoteWrite, func(r *rw) bool {
					return r.AzureAD != nil && r.AzureAD.ManagedIdentity != nil &&
						(r.AzureAD.ManagedIdentity.ClientID == nil || *r.AzureAD.ManagedIdentity.ClientID == "")
				})
			}),
		azure("oauth", "2.48.0", promRefused, func(a *monitoringv1.AzureAD) bool { return a.OAuth != nil }),
		azure("sdk", "2.52.0", promRefused, func(a *monitoringv1.AzureAD) bool { return a.SDK != nil }),
		azure("workloadIdentity", "3.7.0", promRefused, func(a *monitoringv1.AzureAD) bool { return a.WorkloadIdentity != nil }),
		remoteWrite("azureAd", "2.45.0", promKeyDropped, func(r *rw) bool { return r.AzureAD != nil }),
		azure("scope", "3.9.0", promKeyDropped, func(a *monitoringv1.AzureAD) bool { return a.Scope != nil && *a.Scope != "" }),
		remoteWrite("followRedirects", "2.26.0", promKeyDropped, func(r *rw) bool { return r.FollowRedirects != nil }),
		remoteWrite("enableHTTP2", "2.35.0", promKeyDropped, func(r *rw) bool { return r.EnableHttp2 != nil }),
		remoteWrite("queueConfig.minShards", "2.6.0", promKeyDropped, func(r *rw) bool { return r.QueueConfig != nil && r.QueueConfig.MinShards != 0 }),
		remoteWrite("queueConfig.retryOnRateLimit", "2.26.0", promKeyDropped, func(r *rw) bool {
			return r.QueueConfig != nil && r.QueueConfig.RetryOnRateLimit
		}),
		remoteWrite("queueConfig.sampleAgeLimit", "2.50.0", promKeyDropped, func(r *rw) bool {
			return r.QueueConfig != nil && r.QueueConfig.SampleAgeLimit != nil
		}),
		remoteWrite("metadataConfig", "2.23.0", promKeyDropped, func(r *rw) bool { return r.MetadataConfig != nil }),
		remoteWrite("metadataConfig.maxSamplesPerSend", "2.29.0", promKeyDropped, func(r *rw) bool {
			return r.MetadataConfig != nil && r.MetadataConfig.MaxSamplesPerSend != nil
		}),
		remoteWrite("roundRobinDNS", "3.1.0", promKeyDropped, func(r *rw) bool { return r.RoundRobinDNS != nil }),

		// The API server's settings (generateK8SSDConfig) and the scrape
		// classes' authorization, the fallback of a monitor without its own.
		g("apiserverConfig.authorization", "2.26.0", "panics while generating the Kubernetes service discovery of a selected monitor, probe or Alertmanager endpoint"+promOperatorExits, func(s *spec) bool {
			return s.APIServerConfig != nil && s.APIServerConfig.Authorization != nil
		}),
		g("scrapeClasses[].authorization", "2.26.0", "panics while generating the scrape configuration of a selected monitor, probe or ScrapeConfig that falls back to it"+promOperatorExits, func(s *spec) bool {
			return anyOf(s.ScrapeClasses, func(c *monitoringv1.ScrapeClass) bool { return c.Authorization != nil })
		}),
	}

	// The OAuth2 proxy and TLS settings (addOAuth2ToYaml).
	for _, o := range []struct {
		path string
		of   func(s *spec) []*monitoringv1.OAuth2
	}{{"remoteRead[].oauth2", promRemoteReadOAuth2}, {"remoteWrite[].oauth2", promRemoteWriteOAuth2}} {
		gates = append(gates,
			g(o.path+".proxyUrl", "2.43.0", promKeyDropped, func(s *spec) bool {
				return anyOf(o.of(s), func(a **monitoringv1.OAuth2) bool { return (*a).ProxyURL != nil })
			}),
			g(o.path+".tlsConfig", "2.43.0", promKeyDropped, func(s *spec) bool {
				return anyOf(o.of(s), func(a **monitoringv1.OAuth2) bool { return (*a).TLSConfig != nil })
			}))
	}
	// The proxy settings other than proxyUrl (addProxyConfigtoYaml).
	for _, p := range promProxyConfigs {
		for _, f := range []struct {
			field string
			set   func(c *monitoringv1.ProxyConfig) bool
		}{
			{"noProxy", func(c *monitoringv1.ProxyConfig) bool { return c.NoProxy != nil }},
			{"proxyFromEnvironment", func(c *monitoringv1.ProxyConfig) bool { return c.ProxyFromEnvironment != nil }},
			{"proxyConnectHeader", func(c *monitoringv1.ProxyConfig) bool { return c.ProxyConnectHeader != nil }},
		} {
			gates = append(gates, g(p.path+"."+f.field, "2.43.0", promKeyDropped, func(s *spec) bool {
				return anyOf(p.of(s), func(c **monitoringv1.ProxyConfig) bool { return f.set(*c) })
			}))
		}
	}
	// The TLS versions (addSafeTLStoYaml).
	for _, t := range promTLSConfigs {
		gates = append(gates,
			g(t.path+".minVersion", "2.35.0", promKeyDropped, func(s *spec) bool {
				return anyOf(t.of(s), func(c **monitoringv1.SafeTLSConfig) bool { return (*c).MinVersion != nil })
			}),
			g(t.path+".maxVersion", "2.41.0", promKeyDropped, func(s *spec) bool {
				return anyOf(t.of(s), func(c **monitoringv1.SafeTLSConfig) bool { return (*c).MaxVersion != nil })
			}))
	}
	// The relabeling actions the validator refuses below their version
	// (validation.LabelConfigValidator): by value, case-insensitively, as it
	// compares them.
	for _, l := range promRelabelLists {
		for _, a := range []struct {
			values  string
			minimum string
		}{{"lowercase|uppercase", "2.36.0"}, {"keepequal|dropequal", "2.41.0"}} {
			gates = append(gates, promGate(l.path+"[].action="+a.values, l.path+"[].action", a.minimum,
				"refuses an action of "+strings.ReplaceAll(a.values, "|", " or ")+" at reconcile, and the Prometheus is not updated", func(s *spec) bool {
					for _, list := range l.of(s) {
						if anyOf(list, func(rc *monitoringv1.RelabelConfig) bool {
							return slices.Contains(strings.Split(a.values, "|"), strings.ToLower(rc.Action))
						}) {
							return true
						}
					}
					return false
				}))
		}
	}
	return gates
}()

// promAlertmanagerAPIVersion is the apiVersion of an Alertmanager endpoint as
// the operator writes it, V1 or V2 (generateAlertmanagerConfig, which
// compares it in upper case); "" where it writes none.
func promAlertmanagerAPIVersion(a *monitoringv1.AlertmanagerEndpoints) monitoringv1.AlertmanagerAPIVersion {
	if a.APIVersion == nil {
		return ""
	}
	switch v := monitoringv1.AlertmanagerAPIVersion(strings.ToUpper(string(*a.APIVersion))); v {
	case monitoringv1.AlertmanagerAPIVersion1, monitoringv1.AlertmanagerAPIVersion2:
		return v
	}
	return ""
}

// prometheusVersionCeilings are the fields the operator reads only below some
// Prometheus version, by id.
var prometheusVersionCeilings = []versionCeiling[monitoringv1.PrometheusSpec]{
	{"alerting.alertmanagers[].apiVersion=v1", "alerting.alertmanagers[].apiVersion", prometheusMajor2xMaximum, "Prometheus 2.x",
		"drops it from the configuration it generates, and Prometheus sends alerts through the Alertmanager API v2",
		func(s *monitoringv1.PrometheusSpec) bool {
			return anyOf(promAlertmanagers(s), func(a *monitoringv1.AlertmanagerEndpoints) bool {
				return promAlertmanagerAPIVersion(a) == monitoringv1.AlertmanagerAPIVersion1
			})
		}},
	{"", "remoteWrite[].queueConfig.maxRetries", "2.11.0", "", promKeyDropped, func(s *monitoringv1.PrometheusSpec) bool {
		return anyOf(s.RemoteWrite, func(r *monitoringv1.RemoteWriteSpec) bool {
			return r.QueueConfig != nil && r.QueueConfig.MaxRetries != 0
		})
	}},
	{"", "allowOverlappingBlocks", "2.39.0", "", promFlagDropped, func(s *monitoringv1.PrometheusSpec) bool {
		return s.AllowOverlappingBlocks //nolint:staticcheck // SA1019: deprecated upstream, still authorable; dropped from 2.39.0.
	}},
}

// prometheusThanosVersionGates are the fields of the Thanos sidecar the
// operator reads only from some Thanos version on (createThanosContainer,
// pkg/prometheus/server/statefulset.go), judged at thanos.version.
var prometheusThanosVersionGates = func() []idGate[monitoringv1.PrometheusSpec] {
	type spec = monitoringv1.PrometheusSpec
	tls := func(s *spec) *monitoringv1.GRPCServerTLSConfig {
		if s.Thanos == nil {
			return nil
		}
		return s.Thanos.GRPCServerTLSConfig
	}
	return []idGate[spec]{
		promGate("", "thanos.grpcServerTlsConfig.minVersion", "0.37.0", promSidecarDropped, func(s *spec) bool {
			return tls(s) != nil && set(tls(s).MinVersion)
		}),
		promGate("", "thanos.grpcServerTlsConfig.cipherSuites", "0.42.0", promSidecarDropped, func(s *spec) bool {
			return tls(s) != nil && len(tls(s).CipherSuites) > 0
		}),
		promGate("", "thanos.grpcServerTlsConfig.curves", "0.42.0", promSidecarDropped, func(s *spec) bool {
			return tls(s) != nil && len(tls(s).Curves) > 0
		}),
		promGate("", "thanos.getConfigTimeout", "0.29.0", promSidecarDropped, func(s *spec) bool {
			return s.Thanos != nil && s.Thanos.GetConfigTimeout != ""
		}),
		promGate("", "thanos.getConfigInterval", "0.29.0", promSidecarDropped, func(s *spec) bool {
			return s.Thanos != nil && s.Thanos.GetConfigInterval != ""
		}),
	}
}()

// prometheusMountsWebConfig reports whether the operator mounts the web
// configuration file in the prometheus container, which it does from
// Prometheus v2.24.0 (WithMinimumVersion, pkg/prometheus/server/
// statefulset.go:196-205 at prometheus-operator v0.94.1). A version it cannot
// parse builds no StatefulSet, so no mount is counted there.
func prometheusMountsWebConfig(spec *monitoringv1.PrometheusSpec) bool {
	v, ok := prometheusComparedVersion(spec)
	return ok && v.GTE(semver.MustParse("2.24.0"))
}

// prometheusComparedVersion is the Prometheus version the operator compares,
// parsed with semver.ParseTolerant as it parses it: version, or its default
// where version is unset. It reports false for a version that does not parse.
func prometheusComparedVersion(spec *monitoringv1.PrometheusSpec) (semver.Version, bool) {
	version := prometheusDefaultVersion
	if spec.Version != "" {
		version = spec.Version
	}
	v, err := semver.ParseTolerant(version)
	return v, err == nil
}

// prometheusVersionDrops reports whether the operator drops the field at path
// at the version it compares: spec authors it, and the version is below the
// minimum of one of its prometheusVersionGates. validatePrometheusVersion
// refuses such a field, naming the version it needs, so a check that would
// refuse it for another reason first leaves it to that gate. A version that
// does not parse drops nothing here.
func prometheusVersionDrops(spec *monitoringv1.PrometheusSpec, path string) bool {
	v, ok := prometheusComparedVersion(spec)
	if !ok {
		return false
	}
	for _, g := range prometheusVersionGates {
		if g.gate.path == path && g.gate.authored(spec) && v.LT(semver.MustParse(g.gate.minimum)) {
			return true
		}
	}
	return false
}

// validatePrometheusVersion refuses what the operator does not run at the
// version it compares. The operator parses version, or its own default where
// it is unset, with semver.ParseTolerant, fails the reconcile where that
// fails, and refuses a major other than 2 or 3 (NewConfigGenerator,
// pkg/prometheus/promcfg.go at v0.94.1). The kind refuses that major too.
//
// It then refuses a field the operator reads only from a later version than
// the one it compares (prometheusVersionGates), or only below an earlier one
// (prometheusVersionCeilings). That is version where set. Unset, it is judged
// against the default of the operator release launcher vendors, as the
// deployed operator's is unknown at build time. A version the operator cannot
// parse is left to it: no gate applies there. Where thanos is set, the
// sidecar's fields are judged the same way at thanos.version
// (prometheusThanosVersionGates), or at the operator's default Thanos
// version, and where both versions parse, thanos.blockSize is held to the
// compaction mode they choose (refuseDelayedCompactionBlockSize).
func validatePrometheusVersion(spec *monitoringv1.PrometheusSpec) error {
	version, at := prometheusDefaultVersion, "the operator's default version "+prometheusDefaultVersion
	if spec.Version != "" {
		version, at = spec.Version, "version "+spec.Version
	}
	prom, promParsed := semver.Version{}, false
	if v, err := semver.ParseTolerant(version); err == nil {
		prom, promParsed = v, true
		if v.Major != 2 && v.Major != 3 {
			return errors.Errorf("version: %q is not supported by the Prometheus operator, which runs Prometheus of major version 2 or 3 only; name one such as %s", spec.Version, prometheusDefaultVersion)
		}
		// Only here can the operator reach validateRemoteWriteSpec: it
		// fails in NewConfigGenerator on another major or on a version it
		// cannot parse. It reaches it where it generates the configuration,
		// not with every monitor selector unset unless the operator is run
		// with unmanaged configuration disabled, which the kind cannot see,
		// so the entry is refused wherever the operator may refuse it.
		if err := refuseAzureADWithoutMethod(spec.RemoteWrite, "one"); err != nil {
			return err
		}
		if err := refuseVersionGates(spec, v, at, "Prometheus", gatesOf(prometheusVersionGates)); err != nil {
			return err
		}
		if err := refuseVersionCeilings(spec, v, at, "Prometheus", "version", prometheusVersionCeilings); err != nil {
			return err
		}
	}
	if spec.Thanos == nil {
		return nil
	}
	thanosAt := "the operator's default Thanos version " + prometheusThanosDefaultVersion
	version = prometheusThanosDefaultVersion
	// The operator reads thanos.version with ptr.Deref, so an empty one is
	// not its default but a version it cannot parse.
	if spec.Thanos.Version != nil {
		version, thanosAt = *spec.Thanos.Version, "thanos.version "+*spec.Thanos.Version
	}
	if v, err := semver.ParseTolerant(version); err == nil {
		if err := refuseVersionGatesAt(spec, v, thanosAt, "Thanos", "thanos.version", gatesOf(prometheusThanosVersionGates)); err != nil {
			return err
		}
		if promParsed {
			return refuseDelayedCompactionBlockSize(spec, prom, at, v, thanosAt)
		}
	}
	return nil
}

// The least Prometheus and Thanos versions from which the operator, where the
// sidecar uploads to object storage and disableCompaction is unset, keeps
// local compaction on in step with the sidecar (compactionModeFor,
// pkg/prometheus/server/statefulset.go, minVersionPrometheusDelayedCompaction
// and minVersionThanosDelayedCompaction). It then passes no block duration:
// thanos.blockSize is read only where it disables compaction.
const (
	prometheusDelayedCompactionMinimum = "3.9.0"
	thanosDelayedCompactionMinimum     = "0.42.0"
)

// refuseDelayedCompactionBlockSize refuses thanos.blockSize where the operator
// keeps local compaction on, at prom and thanos, named promAt and thanosAt:
// object storage is set, disableCompaction is not, and both versions reach
// the delayed compaction minima. Without object storage and without
// disableCompaction the operator does not read thanos.blockSize at any
// version, which is not a version's doing and is not held here.
func refuseDelayedCompactionBlockSize(spec *monitoringv1.PrometheusSpec, prom semver.Version, promAt string, thanos semver.Version, thanosAt string) error {
	t := spec.Thanos
	// The CRD's default, which the API server writes where blockSize is
	// unset, makes the same object as leaving it unset.
	crdDefault := prometheusDefaultedZeroFields["thanos.blockSize"] == strconv.Quote(string(t.BlockDuration))
	if t.BlockDuration == "" || crdDefault || spec.DisableCompaction || (t.ObjectStorageConfig == nil && t.ObjectStorageConfigFile == nil) {
		return nil
	}
	if prom.LT(semver.MustParse(prometheusDelayedCompactionMinimum)) || thanos.LT(semver.MustParse(thanosDelayedCompactionMinimum)) {
		return nil
	}
	return errors.Errorf("thanos.blockSize: read by the Prometheus operator only where it disables local compaction: with disableCompaction, or with Prometheus below %s or Thanos below %s; at %s and %s, with object storage set, it keeps compaction on in step with the sidecar and passes no block duration; set disableCompaction, lower version below %s or thanos.version below %s, or leave thanos.blockSize unset",
		prometheusDelayedCompactionMinimum, thanosDelayedCompactionMinimum, promAt, thanosAt, prometheusDelayedCompactionMinimum, thanosDelayedCompactionMinimum)
}
