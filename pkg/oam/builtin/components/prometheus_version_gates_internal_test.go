package components

import (
	"cmp"
	"fmt"
	"go/ast"
	"maps"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// prometheusGateFiles are the files of the operator's packages
// pkg/prometheus, pkg/prometheus/server and pkg/prometheus/validation in the
// excerpt, which scripts/vendor-prometheus-operator-gates.sh vendors whole,
// their tests aside. The test walks each one, so a comparison in any file of
// the three packages is classified or fails.
func prometheusGateFiles(t *testing.T, src *vendoredPromOp) []string {
	t.Helper()
	dirs := []string{"pkg/prometheus/", "pkg/prometheus/server/", "pkg/prometheus/validation/"}
	var files []string
	seen := map[string]bool{}
	for rel := range src.files {
		if dir, name := path.Split(rel); slices.Contains(dirs, dir) && !strings.HasSuffix(name, "_test.go") {
			files = append(files, rel)
			seen[dir] = true
		}
	}
	slices.Sort(files)
	for _, dir := range dirs {
		if !seen[dir] {
			t.Fatalf("the excerpt holds no file of %s", dir)
		}
	}
	return files
}

// Paths of the ids the rows name in groups: the holders of the TLS, proxy
// and relabeling settings, as the kind's tables build them.
func promTLSIDs(field string) []string {
	var ids []string
	for _, h := range promTLSConfigs {
		ids = append(ids, h.path+"."+field)
	}
	return ids
}

func promProxyIDs() []string {
	var ids []string
	for _, h := range promProxyConfigs {
		for _, f := range []string{"noProxy", "proxyFromEnvironment", "proxyConnectHeader"} {
			ids = append(ids, h.path+"."+f)
		}
	}
	return ids
}

func promRelabelIDs(values string) []string {
	var ids []string
	for _, l := range promRelabelLists {
		ids = append(ids, l.path+"[].action="+values)
	}
	return ids
}

// prometheusGateRows classify the gates of prometheusGateFiles one by one,
// where a function holds gates of this kind's fields. Two rows read the
// bound of the version they compare from the source (bounds): the limit keys
// of AddLimitsToYAML, and the minimum each monitor kind passes for the
// attach_metadata of its scrape class.
func prometheusGateRows(t *testing.T, src *vendoredPromOp) []gateRow {
	t.Helper()
	const (
		cfg = "pkg/prometheus/promcfg.go"
		op  = "pkg/prometheus/operator.go"
		sts = "pkg/prometheus/server/statefulset.go"
		val = "pkg/prometheus/validation/validator.go"
		cg  = "(*ConfigGenerator)."

		monitor   = "a field of a ServiceMonitor, PodMonitor or Probe object, which this kind does not author"
		scrape    = "a field of a ScrapeConfig object, which this kind does not author"
		spelling  = "chooses how the operator spells a flag or a setting it passes at every version; no field of this kind is dropped"
		retention = "chooses whether retention and retentionSize are passed as flags or written to the configuration; read at every version"
	)
	gate := func(file, fn, cond string, paths ...string) gateRow {
		return gateRow{file: file, fn: fn, cond: cond, paths: paths}
	}
	not := func(file, fn, cond, why string) gateRow {
		return gateRow{file: file, fn: fn, cond: cond, not: why}
	}
	rw := func(fn, cond string, fields ...string) gateRow {
		paths := make([]string, len(fields))
		for i, f := range fields {
			paths[i] = "remoteWrite[]." + f
		}
		return gate(cfg, cg+fn, cond, paths...)
	}
	am := func(cond string, fields ...string) gateRow {
		paths := make([]string, len(fields))
		for i, f := range fields {
			paths[i] = "alerting.alertmanagers[]." + f
		}
		return gate(cfg, cg+"generateAlertmanagerConfig", cond, paths...)
	}

	// The limit keys each scrape configuration takes its enforced limit
	// under, at the key's minimum; sampleLimitKey has none.
	limit := func(key string) string { return src.structStringField(t, cfg, key, "minVersion") }
	limits := map[string]string{
		"enforcedSampleLimit":           limit("sampleLimitKey"),
		"enforcedTargetLimit":           limit("targetLimitKey"),
		"enforcedLabelLimit":            limit("labelLimitKey"),
		"enforcedLabelNameLengthLimit":  limit("labelNameLengthLimitKey"),
		"enforcedLabelValueLengthLimit": limit("labelValueLengthLimitKey"),
		"enforcedKeepDroppedTargets":    limit("keepDroppedTargetsKey"),
	}
	// The minimum each monitor kind reads a scrape class's attachMetadata
	// from, and the same minimum for the node metadata of topology sharding.
	attach := map[string]string{}
	for _, c := range src.callStringArgs(t, cfg, "mergeAttachMetadataWithScrapeClass", 2) {
		switch c.fn {
		case cg + "generatePodMonitorConfig":
			attach["scrapeClasses[].attachMetadata@podMonitor"] = c.arg
		case cg + "generateServiceMonitorConfig":
			attach["scrapeClasses[].attachMetadata@serviceMonitor"] = c.arg
		default:
			t.Errorf("%s calls mergeAttachMetadataWithScrapeClass: name the id of its scrape class's attachMetadata", c.fn)
		}
	}
	if len(attach) != 2 {
		t.Errorf("mergeAttachMetadataWithScrapeClass is called for %d monitor kinds, want the PodMonitor's and the ServiceMonitor's", len(attach))
	}
	topology := src.callStringArgs(t, cfg, "mergeAttachMetadataForTopology", 1)
	if want := src.callStringArgs(t, cfg, "mergeAttachMetadataWithScrapeClass", 2); !slices.Equal(topology, want) {
		t.Errorf("mergeAttachMetadataForTopology is called %v; want the minima of mergeAttachMetadataWithScrapeClass, %v", topology, want)
	}

	return []gateRow{
		// pkg/prometheus/operator.go: the checks of a remote write's Azure AD
		// settings at reconcile.
		gate(op, cg+"validateRemoteWriteSpec", `!cg.WithMinimumVersion("2.48.0").IsCompatible()`, "remoteWrite[].azureAd.oauth"),
		gate(op, cg+"validateRemoteWriteSpec", `!cg.WithMinimumVersion("2.52.0").IsCompatible()`, "remoteWrite[].azureAd.sdk"),
		gate(op, cg+"validateRemoteWriteSpec", `!cg.WithMinimumVersion("3.7.0").IsCompatible()`, "remoteWrite[].azureAd.workloadIdentity"),
		gate(op, cg+"checkAzureADManagedIdentity", `cg.WithMinimumVersion("3.5.0").IsCompatible()`, `remoteWrite[].azureAd.managedIdentity.clientId=""`),

		// pkg/prometheus/promcfg.go: the floor, and the configuration the
		// operator generates.
		{file: cfg, fn: "NewConfigGenerator", cond: `version.Major != 2 && version.Major != 3`, floor: true},
		gate(cfg, cg+"endpointRoleFlavor", `cg.version.LT(semver.MustParse("2.21.0"))`, "serviceDiscoveryRole=EndpointSlice"),
		{file: cfg, fn: cg + "AddLimitsToYAML", cond: `return cg.WithMinimumVersion(k.minVersion).AppendMapItem(cfg, k.prometheusField, finalLimit)`, bounds: limits},
		gate(cfg, cg+"AddHonorTimestamps", `return cg.WithMinimumVersion("2.9.0").AppendMapItem(cfg, "honor_timestamps", honor && !cpf.OverrideHonorTimestamps)`, "overrideHonorTimestamps"),
		not(cfg, cg+"AddTrackTimestampsStaleness", `return cg.WithMinimumVersion("2.48.0").AppendMapItem(cfg, "track_timestamps_staleness", *trackTimestampsStaleness)`, monitor),
		gate(cfg, cg+"addScrapeProtocols", `sp == monitoringv1.PrometheusText1_0_0 && !cg.WithMinimumVersion("3.0.0-rc.0").IsCompatible()`, "scrapeProtocols=PrometheusText1.0.0"),
		gate(cfg, cg+"addScrapeProtocols", `return cg.WithMinimumVersion("2.49.0").AppendMapItem(cfg, "scrape_protocols", sps)`, "scrapeProtocols"),
		gate(cfg, cg+"addFallbackScrapeProtocol", `return cg.WithMinimumVersion("3.0.0").AppendMapItem(cfg, "fallback_scrape_protocol", fallbackScrapeProtocol)`, "scrapeClasses[].fallbackScrapeProtocol"),
		not(cfg, cg+"addNativeHistogramConfig", `cg.WithMinimumVersion("3.8.0").AppendMapItem(cfg, "scrape_native_histograms", nhc.ScrapeNativeHistograms)`, monitor),
		not(cfg, cg+"addNativeHistogramConfig", `cg.WithMinimumVersion("2.45.0").AppendMapItem(cfg, "native_histogram_bucket_limit", nhc.NativeHistogramBucketLimit)`, monitor),
		not(cfg, cg+"addNativeHistogramConfig", `cg.WithMinimumVersion("2.50.0").AppendMapItem(cfg, "native_histogram_min_bucket_factor", nhc.NativeHistogramMinBucketFactor.AsApproximateFloat64())`, monitor),
		not(cfg, cg+"addNativeHistogramConfig", `cg.version.Major`, monitor),
		not(cfg, cg+"addNativeHistogramConfig", `cg.WithMinimumVersion("2.45.0").AppendMapItem(cfg, "scrape_classic_histograms", nhc.ScrapeClassicHistograms)`, monitor),
		not(cfg, cg+"addNativeHistogramConfig", `cg.WithMinimumVersion("3.0.0").AppendMapItem(cfg, "convert_classic_histograms_to_nhcb", nhc.ConvertClassicHistogramsToNHCB)`, monitor),
		gate(cfg, cg+"addSigv4ToYaml", `cg.WithMinimumVersion("3.11.0").AppendMapItem(sigv4Cfg, "external_id", sigv4.ExternalID)`,
			"alerting.alertmanagers[].sigv4.externalId", "remoteWrite[].sigv4.externalId"),
		gate(cfg, cg+"addSigv4ToYaml", `cg.WithMinimumVersion("2.54.0").AppendMapItem(sigv4Cfg, "use_fips_sts_endpoint", *sigv4.UseFIPSSTSEndpoint)`,
			"alerting.alertmanagers[].sigv4.useFIPSSTSEndpoint", "remoteWrite[].sigv4.useFIPSSTSEndpoint"),
		gate(cfg, cg+"addSafeAuthorizationToYaml", `return cg.WithMinimumVersion("2.26.0").AppendMapItem(cfg, "authorization", authCfg)`,
			"alerting.alertmanagers[].authorization", "apiserverConfig.authorization", "remoteRead[].authorization", "remoteWrite[].authorization", "scrapeClasses[].authorization"),
		gate(cfg, cg+"addAuthorizationToYaml", `return cg.WithMinimumVersion("2.26.0").AppendMapItem(cfg, "authorization", authCfg)`,
			"apiserverConfig.authorization", "remoteRead[].authorization", "remoteWrite[].authorization", "scrapeClasses[].authorization"),
		gate(cfg, cg+"addProxyConfigtoYaml", `cg.WithMinimumVersion("2.43.0")`, promProxyIDs()...),
		gate(cfg, cg+"addSafeTLStoYaml", `cg.WithMinimumVersion("2.35.0").AppendMapItem(safetlsConfig, "min_version", *safetls.MinVersion)`, promTLSIDs("minVersion")...),
		gate(cfg, cg+"addSafeTLStoYaml", `cg.WithMinimumVersion("2.41.0").AppendMapItem(safetlsConfig, "max_version", *safetls.MaxVersion)`, promTLSIDs("maxVersion")...),
		not(cfg, cg+"addHTTPConfigToYAML", `cg.WithMinimumVersion("2.26.0").AppendMapItem(cfg, "follow_redirects", *httpConfig.FollowRedirects)`, monitor),
		not(cfg, cg+"addHTTPConfigToYAML", `cg.WithMinimumVersion("2.35.0").AppendMapItem(cfg, "enable_http2", *httpConfig.EnableHTTP2)`, monitor),
		gate(cfg, cg+"appendStorageSettingsConfig", `cg.WithMinimumVersion("2.29.0")`, "exemplars"),
		gate(cfg, cg+"appendStorageSettingsConfig", `cg.WithMinimumVersion("2.39.0").AppendMapItem(tsdbSlice, "out_of_order_time_window", *tsdb.OutOfOrderTimeWindow)`, "tsdb.outOfOrderTimeWindow"),
		gate(cfg, cg+"appendStorageSettingsConfig", `cg.WithMinimumVersion("3.10.0").AppendMapItem(tsdbSlice, "stale_series_compaction_threshold", tsdb.StaleSeriesCompactionThreshold.AsApproximateFloat64())`,
			"tsdb.staleSeriesCompactionThreshold"),
		gate(cfg, cg+"appendStorageSettingsConfig", `cg.WithMinimumVersion("3.13.0").AppendMapItem(tsdbSlice, "chunk_encoding", yaml.MapSlice{ {Key: "floats", Value: strings.ToLower(string(*tsdb.ChunkEncoding.Floats))}, })`,
			"tsdb.chunkEncoding"),
		// The retention settings move from flags to the configuration at
		// 3.11.0; retentionPercentage has no flag.
		gate(cfg, cg+"appendStorageSettingsConfig", `cg.WithMinimumVersion("3.11.0")`, "retentionPercentage"),

		not(cfg, cg+"BuildCommonPrometheusArgs", `cg.version.Major == 2`, spelling),
		gate(cfg, cg+"BuildCommonPrometheusArgs", `cg.WithMinimumVersion("2.6.0").AppendCommandlineArgument(promArgs, monitoringv1.Argument{Name: "web.page-title", Value: *cpf.Web.PageTitle})`, "web.pageTitle"),
		gate(cfg, cg+"BuildCommonPrometheusArgs", `cg.WithMinimumVersion("2.33.0").AppendCommandlineArgument(promArgs, monitoringv1.Argument{Name: "web.enable-remote-write-receiver"})`, "enableRemoteWriteReceiver"),
		gate(cfg, cg+"BuildCommonPrometheusArgs", `cg.WithMinimumVersion("2.54.0").AppendCommandlineArgument( promArgs, monitoringv1.Argument{ Name: "web.remote-write-receiver.accepted-protobuf-messages", Value: strings.Join(versions, ","), }, )`,
			"remoteWriteReceiverMessageVersions"),
		// The metadata-wal-records feature a remote write of messageVersion
		// V2.0 needs, which the version also gates in the remote write itself.
		gate(cfg, cg+"BuildCommonPrometheusArgs", `cg.WithMinimumVersion("2.54.0")`, "remoteWrite[].messageVersion"),
		not(cfg, cg+"BuildCommonPrometheusArgs", `cg.Version().LT(semver.MustParse("3.4.0"))`,
			"the metadata-wal-records feature flag, which Prometheus needs for messageVersion V2.0 only below 3.4.0; no field"),
		not(cfg, cg+"BuildCommonPrometheusArgs", `cg.version.Major >= 3`, spelling),
		gate(cfg, cg+"BuildCommonPrometheusArgs", `cg.WithMinimumVersion("2.47.0").AppendCommandlineArgument(promArgs, monitoringv1.Argument{Name: "enable-feature", Value: "otlp-write-receiver"})`, "enableOTLPReceiver"),
		gate(cfg, cg+"BuildCommonPrometheusArgs", `cg.WithMinimumVersion("2.25.0").AppendCommandlineArgument(promArgs, monitoringv1.Argument{Name: "enable-feature", Value: strings.Join(efs, ",")})`, "enableFeatures"),
		gate(cfg, cg+"BuildCommonPrometheusArgs", `cg.WithMinimumVersion("3.13.0").AppendCommandlineArgument(promArgs, monitoringv1.Argument{Name: "enable-feature", Value: "xor2-encoding"})`, "tsdb.chunkEncoding"),
		gate(cfg, cg+"BuildCommonPrometheusArgs", `cg.WithMinimumVersion("2.6.0").AppendCommandlineArgument(promArgs, monitoringv1.Argument{Name: "log.format", Value: cpf.LogFormat})`, "logFormat"),

		// The scrape configurations of the monitor kinds, which take the
		// enforced body size limit from this kind.
		gate(cfg, cg+"generatePodMonitorConfig", `cg.WithMinimumVersion("2.28.0").AppendMapItem(cfg, "body_size_limit", bodySizeLimit)`, "enforcedBodySizeLimit"),
		gate(cfg, cg+"generateProbeConfig", `cg.WithMinimumVersion("2.28.0").AppendMapItem(cfg, "body_size_limit", cpf.EnforcedBodySizeLimit)`, "enforcedBodySizeLimit"),
		not(cfg, cg+"generateServiceMonitorConfig", `cg.WithMinimumVersion("2.26.0").AppendMapItem(cfg, "follow_redirects", *ep.FollowRedirects)`, monitor),
		not(cfg, cg+"generateServiceMonitorConfig", `cg.WithMinimumVersion("2.35.0").AppendMapItem(cfg, "enable_http2", *ep.EnableHTTP2)`, monitor),
		gate(cfg, cg+"generateServiceMonitorConfig", `cg.WithMinimumVersion("2.28.0").AppendMapItem(cfg, "body_size_limit", bodySizeLimit)`, "enforcedBodySizeLimit"),
		gate(cfg, cg+"generateScrapeConfig", `cg.WithMinimumVersion("2.28.0").AppendMapItem(cfg, "body_size_limit", bodySizeLimit)`, "enforcedBodySizeLimit"),
		not(cfg, cg+"getLimit", `cg.version.GTE(semver.MustParse("2.45.0"))`,
			"chooses whether an enforced limit is written per scrape configuration or, from 2.45.0, also globally; the enforced limits are gated where AddLimitsToYAML writes them"),
		{file: cfg, fn: cg + "generateK8SSDConfig", cond: `cg.WithMinimumVersion(attachMetadataConfig.MinimumVersion).AppendMapItem( k8sSDConfig, "attach_metadata", yaml.MapSlice{ {Key: "node", Value: attachMetadataConfig.node()}, })`,
			bounds: attach},

		am(`cg.WithMinimumVersion("2.35.0").AppendMapItem(cfg, "enable_http2", *am.EnableHttp2)`, "enableHttp2"),
		am(`cg.WithMinimumVersion("2.26.0").addBasicAuthToYaml(cfg, store, am.BasicAuth)`, "basicAuth"),
		am(`cg.WithMinimumVersion("2.48.0").addSigv4ToYaml(cfg, fmt.Sprintf("alertmanager/auth/%d", i), store, am.Sigv4)`, "sigv4"),
		am(`cg.WithMinimumVersion("2.11.0")`, "apiVersion"),
		{file: cfg, fn: cg + "generateAlertmanagerConfig", cond: `cg.version.Major <= 2`, paths: []string{"alerting.alertmanagers[].apiVersion=v1"}, major: prometheusMajor2xMaximum},
		am(`cg.WithMinimumVersion("2.51.0").AppendMapItem(cfg, "alert_relabel_configs", generateRelabelConfig(am.AlertRelabelConfigs))`, "alertRelabelings"),

		gate(cfg, cg+"generateRemoteReadConfig", `cg.WithMinimumVersion("2.26.0").AppendMapItem(cfg, "headers", stringMapToMapSlice(spec.Headers))`, "remoteRead[].headers"),
		gate(cfg, cg+"generateRemoteReadConfig", `cg.WithMinimumVersion("2.15.0").AppendMapItem(cfg, "name", spec.Name)`, "remoteRead[].name"),
		gate(cfg, cg+"generateRemoteReadConfig", `cg.WithMinimumVersion("2.26.0").AppendMapItem(cfg, "follow_redirects", spec.FollowRedirects)`, "remoteRead[].followRedirects"),
		gate(cfg, cg+"generateRemoteReadConfig", `cg.WithMinimumVersion("2.34.0").AppendMapItem(cfg, "filter_external_labels", spec.FilterExternalLabels)`, "remoteRead[].filterExternalLabels"),

		gate(cfg, cg+"addOAuth2ToYaml", `cg.WithMinimumVersion("2.43.0").addProxyConfigtoYaml(oauth2Cfg, store, oauth2.ProxyConfig)`,
			"remoteRead[].oauth2.proxyUrl", "remoteRead[].oauth2.noProxy", "remoteRead[].oauth2.proxyFromEnvironment", "remoteRead[].oauth2.proxyConnectHeader",
			"remoteWrite[].oauth2.proxyUrl", "remoteWrite[].oauth2.noProxy", "remoteWrite[].oauth2.proxyFromEnvironment", "remoteWrite[].oauth2.proxyConnectHeader"),
		gate(cfg, cg+"addOAuth2ToYaml", `cg.WithMinimumVersion("2.43.0").addSafeTLStoYaml(oauth2Cfg, store, oauth2.TLSConfig)`, "remoteRead[].oauth2.tlsConfig", "remoteWrite[].oauth2.tlsConfig"),
		gate(cfg, cg+"addOAuth2ToYaml", `return cg.WithMinimumVersion("2.27.0").AppendMapItem(cfg, "oauth2", oauth2Cfg)`, "remoteRead[].oauth2", "remoteWrite[].oauth2"),

		rw("GenerateRemoteWriteConfig", `cg.WithMinimumVersion("2.15.0").AppendMapItem(cfg, "headers", stringMapToMapSlice(spec.Headers))`, "headers"),
		rw("GenerateRemoteWriteConfig", `cg.WithMinimumVersion("2.15.0").AppendMapItem(cfg, "name", *spec.Name)`, "name"),
		rw("GenerateRemoteWriteConfig", `cg.WithMinimumVersion("2.54.0").AppendMapItem(cfg, "protobuf_message", toProtobufMessageVersion(*spec.MessageVersion))`, "messageVersion"),
		rw("GenerateRemoteWriteConfig", `cg.WithMinimumVersion("2.27.0").AppendMapItem(cfg, "send_exemplars", spec.SendExemplars)`, "sendExemplars"),
		rw("GenerateRemoteWriteConfig", `cg.WithMinimumVersion("2.40.0").AppendMapItem(cfg, "send_native_histograms", spec.SendNativeHistograms)`, "sendNativeHistograms"),
		rw("GenerateRemoteWriteConfig", `cg.WithMinimumVersion("2.26.0").addSigv4ToYaml(cfg, fmt.Sprintf("remoteWrite/%d", i), s, spec.Sigv4)`, "sigv4"),
		rw("GenerateRemoteWriteConfig", `cg.WithMinimumVersion("2.48.0").AppendMapItem(azureAd, "oauth", yaml.MapSlice{ {Key: "client_id", Value: spec.AzureAD.OAuth.ClientID}, {Key: "client_secret", Value: string(b)}, {Key: "tenant_id", Value: spec.AzureAD.OAuth.TenantID}, })`,
			"azureAd.oauth"),
		rw("GenerateRemoteWriteConfig", `cg.WithMinimumVersion("2.52.0").AppendMapItem( azureAd, "sdk", yaml.MapSlice{ {Key: "tenant_id", Value: ptr.Deref(spec.AzureAD.SDK.TenantID, "")}, })`, "azureAd.sdk"),
		rw("GenerateRemoteWriteConfig", `cg.WithMinimumVersion("3.7.0").AppendMapItem(azureAd, "workload_identity", workloadIdentityConfig)`, "azureAd.workloadIdentity"),
		rw("GenerateRemoteWriteConfig", `cg.WithMinimumVersion("3.9.0").AppendMapItem(azureAd, "scope", scope)`, "azureAd.scope"),
		rw("GenerateRemoteWriteConfig", `cg.WithMinimumVersion("2.45.0").AppendMapItem(cfg, "azuread", azureAd)`, "azureAd"),
		rw("GenerateRemoteWriteConfig", `cg.WithMinimumVersion("2.26.0").AppendMapItem(cfg, "follow_redirects", spec.FollowRedirects)`, "followRedirects"),
		rw("GenerateRemoteWriteConfig", `cg.WithMinimumVersion("2.35.0").AppendMapItem(cfg, "enable_http2", *spec.EnableHttp2)`, "enableHTTP2"),
		rw("GenerateRemoteWriteConfig", `cg.WithMinimumVersion("2.6.0").AppendMapItem(queueConfig, "min_shards", spec.QueueConfig.MinShards)`, "queueConfig.minShards"),
		rw("GenerateRemoteWriteConfig", `cg.WithMaximumVersion("2.11.0").AppendMapItem(queueConfig, "max_retries", spec.QueueConfig.MaxRetries)`, "queueConfig.maxRetries"),
		rw("GenerateRemoteWriteConfig", `cg.WithMinimumVersion("2.26.0").AppendMapItem(queueConfig, "retry_on_http_429", spec.QueueConfig.RetryOnRateLimit)`, "queueConfig.retryOnRateLimit"),
		rw("GenerateRemoteWriteConfig", `cg.WithMinimumVersion("2.50.0").AppendMapItem(queueConfig, "sample_age_limit", string(*spec.QueueConfig.SampleAgeLimit))`, "queueConfig.sampleAgeLimit"),
		rw("GenerateRemoteWriteConfig", `cg.WithMinimumVersion("2.29.0").AppendMapItem(metadataConfig, "max_samples_per_send", *spec.MetadataConfig.MaxSamplesPerSend)`, "metadataConfig.maxSamplesPerSend"),
		rw("GenerateRemoteWriteConfig", `cg.WithMinimumVersion("2.23.0").AppendMapItem(cfg, "metadata_config", metadataConfig)`, "metadataConfig"),
		rw("GenerateRemoteWriteConfig", `cg.WithMinimumVersion("3.1.0").AppendMapItem(cfg, "round_robin_dns", spec.RoundRobinDNS)`, "roundRobinDNS"),

		// Below 2.53.0 the operator passes runtime.goGC, the one field of
		// runtime, as the GOGC variable of the container instead
		// (server/statefulset.go), so the field is read at every version.
		not(cfg, cg+"appendRuntime", `!cg.WithMinimumVersion("2.53.0").IsCompatible()`,
			"runtime.goGC is written to the configuration from 2.53.0 and passed as the GOGC variable of the container below it; read at every version"),
		gate(cfg, cg+"appendScrapeLimits", `cg.WithMinimumVersion("2.45.0").AppendMapItem(slice, "body_size_limit", cpf.BodySizeLimit)`, "bodySizeLimit"),
		not(cfg, cg+"appendScrapeLimits", `cg.WithMinimumVersion("2.45.0").AppendMapItem(slice, "body_size_limit", cpf.EnforcedBodySizeLimit)`,
			"the global form of enforcedBodySizeLimit, which every scrape configuration also takes from 2.28.0, where it is gated"),
		gate(cfg, cg+"appendScrapeLimits", `cg.WithMinimumVersion("2.45.0").appendGlobalLimits(slice, "sample_limit", cpf.SampleLimit, cpf.EnforcedSampleLimit)`, "sampleLimit"),
		gate(cfg, cg+"appendScrapeLimits", `cg.WithMinimumVersion("2.45.0").appendGlobalLimits(slice, "target_limit", cpf.TargetLimit, cpf.EnforcedTargetLimit)`, "targetLimit"),
		gate(cfg, cg+"appendScrapeLimits", `cg.WithMinimumVersion("2.45.0").appendGlobalLimits(slice, "label_limit", cpf.LabelLimit, cpf.EnforcedLabelLimit)`, "labelLimit"),
		gate(cfg, cg+"appendScrapeLimits", `cg.WithMinimumVersion("2.45.0").appendGlobalLimits(slice, "label_name_length_limit", cpf.LabelNameLengthLimit, cpf.EnforcedLabelNameLengthLimit)`, "labelNameLengthLimit"),
		gate(cfg, cg+"appendScrapeLimits", `cg.WithMinimumVersion("2.45.0").appendGlobalLimits(slice, "label_value_length_limit", cpf.LabelValueLengthLimit, cpf.EnforcedLabelValueLengthLimit)`, "labelValueLengthLimit"),
		gate(cfg, cg+"appendScrapeLimits", `cg.WithMinimumVersion("2.47.0").appendGlobalLimits(slice, "keep_dropped_targets", cpf.KeepDroppedTargets, cpf.EnforcedKeepDroppedTargets)`, "keepDroppedTargets"),
		gate(cfg, cg+"appendRuleQueryOffset", `return cg.WithMinimumVersion("2.53.0").AppendMapItem(slice, "rule_query_offset", ruleQueryOffset)`, "ruleQueryOffset"),
		gate(cfg, cg+"appendQueryLogFile", `return cg.WithMinimumVersion("2.16.0").AppendMapItem(slice, "query_log_file", logFilePath(queryLogFile))`, "queryLogFile"),
		gate(cfg, cg+"appendScrapeFailureLogFile", `return cg.WithMinimumVersion("2.55.0").AppendMapItem(slice, "scrape_failure_log_file", logFilePath(*scrapeFailureLogFile))`, "scrapeFailureLogFile"),

		not(cfg, cg+"generateScrapeConfig", `cg.WithMinimumVersion("2.49.0").AppendMapItem(cfg, "enable_compression", *sc.Spec.EnableCompression)`, scrape),
		not(cfg, cg+"generateScrapeConfig", `cg.WithMinimumVersion("2.35.0").AppendMapItem(cfg, "enable_http2", *sc.Spec.EnableHTTP2)`, scrape),
		not(cfg, cg+"generateScrapeConfig", `cg.WithMinimumVersion("2.21.0").AppendMapItem(configs[i], "role", strings.ToLower(string(config.Role)))`, scrape),
		not(cfg, cg+"generateScrapeConfig", `cg.WithMinimumVersion("2.17.0").AppendMapItem(configs[i], "selectors", selectors)`, scrape),
		not(cfg, cg+"generateScrapeConfig", `cg.WithMinimumVersion("2.35.0").AppendMapItem(configs[i], "attach_metadata", config.AttachMetadata)`, scrape),
		not(cfg, cg+"generateScrapeConfig", `cg.WithMinimumVersion("2.37.0").AppendMapItem(configs[i], "attach_metadata", config.AttachMetadata)`, scrape),
		not(cfg, cg+"generateScrapeConfig", `typecg.WithMinimumVersion(minVersion)`, scrape),
		not(cfg, cg+"generateScrapeConfig", `cg.WithMinimumVersion("2.3.0").addFiltersToYaml(configs[i], config.Filters)`, scrape),
		not(cfg, cg+"generateScrapeConfig", `cg.WithMinimumVersion("2.41.0")`, scrape),
		not(cfg, cg+"generateScrapeConfig", `cg.WithMinimumVersion("2.54.1").AppendMapItem(configs[i], "match_first_network", config.MatchFirstNetwork)`, scrape),
		not(cfg, cg+"generateScrapeConfig", `cg.WithMinimumVersion("3.5.0").AppendMapItem(configs[i], "label_selector", config.LabelSelector)`, scrape),

		gate(cfg, cg+"appendOTLPConfig", `cg.version.LT(semver.MustParse("2.55.0"))`, "otlp"),
		gate(cfg, cg+"appendOTLPConfig", `cg.version.LT(semver.MustParse("3.4.0")) && ptr.Deref(otlpConfig.TranslationStrategy, "") == monitoringv1.NoTranslation`, "otlp.translationStrategy=NoTranslation"),
		gate(cfg, cg+"appendOTLPConfig", `cg.version.LT(semver.MustParse("3.6.0")) && ptr.Deref(otlpConfig.TranslationStrategy, "") == monitoringv1.UnderscoreEscapingWithoutSuffixes`,
			"otlp.translationStrategy=UnderscoreEscapingWithoutSuffixes"),
		not(cfg, cg+"appendOTLPConfig", `cg.version.GTE(semver.MustParse("3.5.0"))`,
			"validates the combination of otlp's fields from 3.5.0 on, which the fields' own gates already require; no field is dropped"),
		gate(cfg, cg+"appendOTLPConfig", `cg.WithMinimumVersion("2.55.0").AppendMapItem(otlp, "promote_resource_attributes", otlpConfig.PromoteResourceAttributes)`, "otlp.promoteResourceAttributes"),
		gate(cfg, cg+"appendOTLPConfig", `cg.WithMinimumVersion("3.0.0").AppendMapItem(otlp, "translation_strategy", otlpConfig.TranslationStrategy)`, "otlp.translationStrategy"),
		gate(cfg, cg+"appendOTLPConfig", `cg.WithMinimumVersion("3.1.0").AppendMapItem(otlp, "keep_identifying_resource_attributes", otlpConfig.KeepIdentifyingResourceAttributes)`,
			"otlp.keepIdentifyingResourceAttributes"),
		gate(cfg, cg+"appendOTLPConfig", `cg.WithMinimumVersion("3.4.0").AppendMapItem(otlp, "convert_histograms_to_nhcb", otlpConfig.ConvertHistogramsToNHCB)`, "otlp.convertHistogramsToNHCB"),
		gate(cfg, cg+"appendOTLPConfig", `cg.WithMinimumVersion("3.5.0").AppendMapItem(otlp, "promote_all_resource_attributes", otlpConfig.PromoteAllResourceAttributes)`,
			"otlp.promoteAllResourceAttributes"),
		gate(cfg, cg+"appendOTLPConfig", `cg.WithMinimumVersion("3.5.0").AppendMapItem(otlp, "ignore_resource_attributes", otlpConfig.IgnoreResourceAttributes)`, "otlp.ignoreResourceAttributes"),
		gate(cfg, cg+"appendOTLPConfig", `cg.WithMinimumVersion("3.6.0").AppendMapItem(otlp, "promote_scope_metadata", otlpConfig.PromoteScopeMetadata)`, "otlp.promoteScopeMetadata"),
		gate(cfg, cg+"appendOTLPConfig", `cg.WithMinimumVersion("3.8.0").AppendMapItem(otlp, "label_name_underscore_sanitization", otlpConfig.LabelNameUnderscoreSanitization)`,
			"otlp.labelNameUnderscoreSanitization"),
		gate(cfg, cg+"appendOTLPConfig", `cg.WithMinimumVersion("3.8.0").AppendMapItem(otlp, "label_name_preserve_multiple_underscores", otlpConfig.LabelNamePreserveMultipleUnderscores)`,
			"otlp.labelNamePreserveMultipleUnderscores"),

		gate(cfg, cg+"appendNameValidationScheme", `return cg.WithMinimumVersion("3.0.0").AppendMapItem(cfg, "metric_name_validation_scheme", strings.ToLower(nameValidationSchemeValue))`, "nameValidationScheme"),
		gate(cfg, cg+"appendNameEscapingScheme", `return cg.WithMinimumVersion("3.4.0").AppendMapItem(cfg, "metric_name_escaping_scheme", v)`, "nameEscapingScheme"),
		gate(cfg, cg+"appendConvertClassicHistogramsToNHCB", `return cg.WithMinimumVersion("3.4.0").AppendMapItem(cfg, "convert_classic_histograms_to_nhcb", *cpf.ConvertClassicHistogramsToNHCB)`,
			"convertClassicHistogramsToNHCB"),
		gate(cfg, cg+"appendConvertScrapeClassicHistograms", `return cg.WithMinimumVersion("3.5.0").AppendMapItem(cfg, "always_scrape_classic_histograms", *cpf.ScrapeClassicHistograms)`,
			"scrapeClassicHistograms"),
		gate(cfg, cg+"appendScrapeNativeHistograms", `return cg.WithMinimumVersion("3.8.0").AppendMapItem(cfg, "scrape_native_histograms", *cpf.ScrapeNativeHistograms)`, "scrapeNativeHistograms"),

		// pkg/prometheus/server/statefulset.go: the pods.
		gate(sts, "makeStatefulSetSpec", `cg.WithMinimumVersion("2.24.0")`, "web.tlsConfig", "web.httpConfig"),
		not(sts, "makeStatefulSetSpec", `cpf.TSDB != nil && cpf.TSDB.OutOfOrderTimeWindow != nil && compactionMode != compactionModeDefault && cg.WithMinimumVersion("2.55.0").IsCompatible()`,
			"a safety flag the operator adds for out-of-order samples beside the Thanos sidecar's compaction; tsdb.outOfOrderTimeWindow itself is gated where it is written"),
		not(sts, "makeStatefulSetSpec", `p.Spec.Runtime != nil && p.Spec.Runtime.GoGC != nil && !cg.WithMinimumVersion("2.53.0").IsCompatible()`,
			"passes runtime.goGC as the GOGC variable below 2.53.0, where the configuration cannot hold it; read at every version"),
		not(sts, "buildServerArgs", `cg.WithMaximumVersion("2.7.0").IsCompatible()`, spelling),
		not(sts, "buildServerArgs", `retentionTimeFlagValue != "" && cg.Version().LT(semver.MustParse("3.11.0"))`, retention),
		not(sts, "buildServerArgs", `p.Spec.RetentionSize != "" && cg.Version().LT(semver.MustParse("3.11.0"))`, retention),
		gate(sts, "buildServerArgs", `cg.WithMinimumVersion("2.7.0").AppendCommandlineArgument(promArgs, retentionSizeFlag)`, "retentionSize"),
		gate(sts, "buildServerArgs", `cg.WithMinimumVersion("2.4.0").AppendCommandlineArgument(promArgs, monitoringv1.Argument{Name: "rules.alert.for-outage-tolerance", Value: rules.Alert.ForOutageTolerance})`,
			"rules.alert.forOutageTolerance"),
		gate(sts, "buildServerArgs", `cg.WithMinimumVersion("2.4.0").AppendCommandlineArgument(promArgs, monitoringv1.Argument{Name: "rules.alert.for-grace-period", Value: rules.Alert.ForGracePeriod})`,
			"rules.alert.forGracePeriod"),
		gate(sts, "buildServerArgs", `cg.WithMinimumVersion("2.4.0").AppendCommandlineArgument(promArgs, monitoringv1.Argument{Name: "rules.alert.resend-delay", Value: rules.Alert.ResendDelay})`,
			"rules.alert.resendDelay"),
		gate(sts, "buildServerArgs", `cg.WithMinimumVersion("2.5.0").AppendCommandlineArgument(promArgs, monitoringv1.Argument{Name: "query.max-samples", Value: fmt.Sprintf("%d", *query.MaxSamples)})`,
			"query.maxSamples"),
		gate(sts, "buildServerArgs", `cg.WithMinimumVersion("2.11.0").WithMaximumVersion("2.39.0").AppendCommandlineArgument(promArgs, monitoringv1.Argument{Name: "storage.tsdb.allow-overlapping-blocks"})`,
			"allowOverlappingBlocks"),
		gate(sts, "buildServerArgs", `cg.WithMinimumVersion("2.11.0").AppendCommandlineArgument(promArgs, arg)`, "walCompression"),

		// The Thanos sidecar, judged at thanos.version.
		gate(sts, "createThanosContainer", `tlsMinVersion != "" && thanosVersion.GTE(semver.MustParse("0.37.0"))`, "thanos.grpcServerTlsConfig.minVersion"),
		gate(sts, "createThanosContainer", `len(tls.CipherSuites) > 0 && thanosVersion.GTE(semver.MustParse("0.42.0"))`, "thanos.grpcServerTlsConfig.cipherSuites"),
		gate(sts, "createThanosContainer", `len(tls.Curves) > 0 && thanosVersion.GTE(semver.MustParse("0.42.0"))`, "thanos.grpcServerTlsConfig.curves"),
		gate(sts, "createThanosContainer", `thanos.GetConfigTimeout != "" && thanosVersion.GTE(semver.MustParse("0.29.0"))`, "thanos.getConfigTimeout"),
		gate(sts, "createThanosContainer", `thanos.GetConfigInterval != "" && thanosVersion.GTE(semver.MustParse("0.29.0"))`, "thanos.getConfigInterval"),
		not(sts, "createThanosContainer", `thanosVersion.GTE(semver.MustParse(thanosSupportedVersionHTTPClientFlag))`,
			"chooses how the sidecar is given its HTTP client configuration, a file the operator mounts; no field"),
		not(sts, "compactionModeFor", `promVersion.GTE(semver.MustParse(minVersionPrometheusDelayedCompaction)) && thanosVersion.GTE(semver.MustParse(minVersionThanosDelayedCompaction))`,
			"chooses how the operator has Prometheus and the Thanos sidecar share compaction; from both minima on it passes no block duration, so thanos.blockSize is held by refuseDelayedCompactionBlockSize, whose minima the test holds to these constants"),

		// pkg/prometheus/validation/validator.go: the relabeling actions,
		// checked at reconcile.
		gate(val, "(*LabelConfigValidator).ValidateRelabelConfig", `lcv.v.GTE(semver.MustParse("2.36.0"))`, promRelabelIDs("lowercase|uppercase")...),
		gate(val, "(*LabelConfigValidator).ValidateRelabelConfig", `lcv.v.GTE(semver.MustParse("2.41.0"))`, promRelabelIDs("keepequal|dropequal")...),
	}
}

// prometheusGateFuncs classify every gate of a function that holds none of
// this kind's fields.
var prometheusGateFuncs = func() map[string]string {
	const (
		cfg = "pkg/prometheus/promcfg.go (*ConfigGenerator)."
		sel = "pkg/prometheus/resource_selector.go (*ResourceSelector)."
	)
	funcs := map[string]string{
		cfg + "GenerateAgentConfiguration": "the configuration of a PrometheusAgent, another kind",
		cfg + "WithMinimumVersion":         "the definition of the gate the rows classify where it is called",
		cfg + "WithMaximumVersion":         "the definition of the gate the rows classify where it is called",
	}
	for _, fn := range []string{
		"validateMonitorSelectorMechanism", "validateKubernetesSDConfigs", "validateConsulSDConfigs", "validateHTTPSDConfigs",
		"validateAzureSDConfigs", "validateOpenStackSDConfigs", "validateDigitalOceanSDConfigs", "validateDockerSDConfigs",
		"validateLinodeSDConfigs", "validateKumaSDConfigs", "validateHetznerSDConfigs", "validateDockerSwarmSDConfigs",
		"validatePuppetDBSDConfigs", "validateLightSailSDConfigs", "validateOVHCloudSDConfigs", "validateScalewaySDConfigs",
		"validateIonosSDConfigs",
	} {
		funcs[sel+fn] = "the check of a ServiceMonitor, PodMonitor, Probe or ScrapeConfig object at reconcile, which this kind does not author"
	}
	return funcs
}()

// TestPrometheusVersionGates_MatchVendoredSource holds the kind's tables,
// the floor and the default versions to the operator's source: every
// comparison of a version is a row at its bound or is listed with why it is
// not this kind's, and the defaults the kind judges an unset version at are
// the operator's.
func TestPrometheusVersionGates_MatchVendoredSource(t *testing.T) {
	src := loadVendoredPromOp(t)
	const defaults = "pkg/operator/defaults.go"
	if got := src.stringSliceElem(t, defaults, "PrometheusCompatibilityMatrix", -1); got != prometheusDefaultVersion {
		t.Errorf("the last entry of the operator's PrometheusCompatibilityMatrix is %s, prometheusDefaultVersion %s", got, prometheusDefaultVersion)
	}
	if got, want := src.text(src.packageDecl(t, defaults, "DefaultPrometheusVersion")), "PrometheusCompatibilityMatrix[len(PrometheusCompatibilityMatrix)-1]"; got != want {
		t.Errorf("the operator's DefaultPrometheusVersion is %s, not the last entry of its compatibility matrix (%s)", got, want)
	}
	if got := src.stringConst(t, defaults, "DefaultThanosVersion"); got != prometheusThanosDefaultVersion {
		t.Errorf("the operator's DefaultThanosVersion is %s, prometheusThanosDefaultVersion %s", got, prometheusThanosDefaultVersion)
	}
	for name, want := range map[string]string{
		"minVersionPrometheusDelayedCompaction": prometheusDelayedCompactionMinimum,
		"minVersionThanosDelayedCompaction":     thanosDelayedCompactionMinimum,
	} {
		if got := src.stringConst(t, "pkg/prometheus/server/statefulset.go", name); got != want {
			t.Errorf("the operator's %s is %s; refuseDelayedCompactionBlockSize compares against %s", name, got, want)
		}
	}

	rows := prometheusGateRows(t, src)
	// The sidecar's gates compare the Thanos version: their rows name only
	// paths under thanos, which only the Thanos table holds.
	for _, r := range rows {
		for _, p := range r.paths {
			if sidecar := r.fn == "createThanosContainer"; sidecar != strings.HasPrefix(p, "thanos.") {
				t.Errorf("the row of %s %s names %s: the rows of createThanosContainer, and only they, name paths under thanos", r.file, r.fn, p)
			}
		}
	}
	for _, g := range prometheusVersionGates {
		if strings.HasPrefix(g.gate.path, "thanos.") {
			t.Errorf("prometheusVersionGates lists %s, which the Thanos version gates", g.gate.path)
		}
	}
	for _, g := range prometheusThanosVersionGates {
		if !strings.HasPrefix(g.gate.path, "thanos.") {
			t.Errorf("prometheusThanosVersionGates lists %s, which is not a field of the sidecar", g.gate.path)
		}
	}

	minima := append(idGateEntriesOf(prometheusVersionGates), idGateEntriesOf(prometheusThanosVersionGates)...)
	holdVersionGates(t, src.versionGates(t, prometheusGateFiles(t, src)...), rows, prometheusGateFuncs,
		minima, ceilingEntriesOf(prometheusVersionCeilings), []string{})
}

// promGateFixture authors one id of the kind's tables alone, as the operator
// tests it.
type promGateFixture struct {
	props map[string]any
	// below is a version under the id's own minimum, named explicitly: its
	// patch version less one, else its minor version less one, else the last
	// entry of the operator's compatibility matrix of the previous major
	// (v2.55.1).
	below string
	// path and bound are, where they are not the id's own path and minimum,
	// another field the fixture authors with it below its version, a parent
	// or a field it needs, which the refusal names with the id's as two
	// fields outside their versions (masked), or the highest minimum on the
	// id's path; bound is also the version the fixture builds at.
	path, bound string
}

// prometheusGateFixtures are the fixtures of prometheusVersionGates and
// prometheusThanosVersionGates, by id.
func prometheusGateFixtures() map[string]promGateFixture {
	type m = map[string]any
	ref := func(name, key string) m { return m{"name": name, "key": key} }
	merge := func(base, extra m) m {
		out := maps.Clone(base)
		maps.Copy(out, extra)
		return out
	}
	am := func(extra m) m {
		return m{"alerting": m{"alertmanagers": []any{merge(m{"namespace": "monitoring", "name": "alertmanager-operated", "port": "web"}, extra)}}}
	}
	rr := func(extra m) m {
		return m{"remoteRead": []any{merge(m{"url": "https://metrics.example.com/api/v1/read"}, extra)}}
	}
	rw := func(extra m) m {
		return m{"remoteWrite": []any{merge(m{"url": "https://metrics.example.com/api/v1/write"}, extra)}}
	}
	sc := func(extra m) m { return m{"scrapeClasses": []any{merge(m{"name": "default"}, extra)}} }
	api := func(extra m) m {
		return m{"apiserverConfig": merge(m{"host": "https://kubernetes.default.svc"}, extra)}
	}
	tracing := func(extra m) m { return m{"tracingConfig": merge(m{"endpoint": "tempo.monitoring.svc:4317"}, extra)} }
	oauth2 := func(extra m) m {
		return merge(m{"clientId": m{"secret": ref("oauth", "id")}, "clientSecret": ref("oauth", "secret"), "tokenUrl": "https://auth.example.com/token"}, extra)
	}
	azure := func(extra m) m { return rw(m{"azureAd": extra}) }
	otlp := func(extra m) m { return m{"otlp": extra} }
	thanos := func(extra m) m { return m{"thanos": extra} }
	credentials := m{"credentials": ref("token", "token")}
	proxy := "http://proxy.example.com:3128"
	header := m{"X-Team": []any{ref("proxy", "team")}}
	f := func(props m, below string) promGateFixture { return promGateFixture{props: props, below: below} }
	masked := func(props m, below, path, bound string) promGateFixture {
		return promGateFixture{props: props, below: below, path: path, bound: bound}
	}

	fixtures := map[string]promGateFixture{
		"web.pageTitle":                      f(m{"web": m{"pageTitle": "Payments"}}, "v2.5.0"),
		"logFormat":                          f(m{"logFormat": "json"}, "v2.5.0"),
		"enableFeatures":                     f(m{"enableFeatures": []any{"exemplar-storage"}}, "v2.24.0"),
		"enableRemoteWriteReceiver":          f(m{"enableRemoteWriteReceiver": true}, "v2.32.0"),
		"remoteWriteReceiverMessageVersions": f(m{"enableRemoteWriteReceiver": true, "remoteWriteReceiverMessageVersions": []any{"V1.0", "V2.0"}}, "v2.53.0"),
		"enableOTLPReceiver":                 f(m{"enableOTLPReceiver": true}, "v2.46.0"),
		"rules.alert.forOutageTolerance":     f(m{"rules": m{"alert": m{"forOutageTolerance": "1h"}}}, "v2.3.0"),
		"rules.alert.forGracePeriod":         f(m{"rules": m{"alert": m{"forGracePeriod": "10m"}}}, "v2.3.0"),
		"rules.alert.resendDelay":            f(m{"rules": m{"alert": m{"resendDelay": "1m"}}}, "v2.3.0"),
		"query.maxSamples":                   f(m{"query": m{"maxSamples": 50000000}}, "v2.4.0"),
		"retentionSize":                      f(m{"retentionSize": "8GB"}, "v2.6.0"),
		"allowOverlappingBlocks":             f(m{"allowOverlappingBlocks": true}, "v2.10.0"),
		"walCompression":                     f(m{"walCompression": true}, "v2.10.0"),
		"web.tlsConfig": f(m{"web": m{"tlsConfig": m{
			"keySecret": ref("prometheus-tls", "tls.key"), "cert": m{"secret": ref("prometheus-tls", "tls.crt")},
		}}}, "v2.23.0"),
		"web.httpConfig":     f(m{"web": m{"httpConfig": m{"http2": true}}}, "v2.23.0"),
		"tsdb.chunkEncoding": f(m{"tsdb": m{"chunkEncoding": m{"floats": "Xor2"}}}, "v3.12.0"),

		"serviceDiscoveryRole=EndpointSlice":     f(m{"serviceDiscoveryRole": "EndpointSlice"}, "v2.20.0"),
		"overrideHonorTimestamps":                f(m{"overrideHonorTimestamps": true}, "v2.8.0"),
		"scrapeProtocols=PrometheusText1.0.0":    f(m{"scrapeProtocols": []any{"PrometheusText1.0.0"}}, "v2.55.1"),
		"scrapeProtocols":                        f(m{"scrapeProtocols": []any{"PrometheusProto"}}, "v2.48.0"),
		"scrapeClasses[].fallbackScrapeProtocol": f(sc(m{"fallbackScrapeProtocol": "PrometheusText0.0.4"}), "v2.55.1"),
		// One field, read from a version by monitor kind: refused below the
		// highest.
		"scrapeClasses[].attachMetadata@podMonitor":     masked(sc(m{"attachMetadata": m{"node": true}}), "v2.34.0", "", "2.37.0"),
		"scrapeClasses[].attachMetadata@serviceMonitor": f(sc(m{"attachMetadata": m{"node": true}}), "v2.36.0"),
		"enforcedTargetLimit":                           f(m{"enforcedTargetLimit": 1000}, "v2.20.0"),
		"enforcedLabelLimit":                            f(m{"enforcedLabelLimit": 64}, "v2.26.0"),
		"enforcedLabelNameLengthLimit":                  f(m{"enforcedLabelNameLengthLimit": 128}, "v2.26.0"),
		"enforcedLabelValueLengthLimit":                 f(m{"enforcedLabelValueLengthLimit": 1024}, "v2.26.0"),
		"enforcedKeepDroppedTargets":                    f(m{"enforcedKeepDroppedTargets": 100}, "v2.46.0"),
		"enforcedBodySizeLimit":                         f(m{"enforcedBodySizeLimit": "10MB"}, "v2.27.0"),
		"bodySizeLimit":                                 f(m{"bodySizeLimit": "5MB"}, "v2.44.0"),
		"sampleLimit":                                   f(m{"sampleLimit": 50000}, "v2.44.0"),
		"targetLimit":                                   f(m{"targetLimit": 500}, "v2.44.0"),
		"labelLimit":                                    f(m{"labelLimit": 32}, "v2.44.0"),
		"labelNameLengthLimit":                          f(m{"labelNameLengthLimit": 64}, "v2.44.0"),
		"labelValueLengthLimit":                         f(m{"labelValueLengthLimit": 512}, "v2.44.0"),
		"keepDroppedTargets":                            f(m{"keepDroppedTargets": 50}, "v2.46.0"),
		"exemplars":                                     f(m{"exemplars": m{"maxSize": 100000}}, "v2.28.0"),
		"tsdb.outOfOrderTimeWindow":                     f(m{"tsdb": m{"outOfOrderTimeWindow": "10m"}}, "v2.38.0"),
		"tsdb.staleSeriesCompactionThreshold":           f(m{"tsdb": m{"staleSeriesCompactionThreshold": "0.5"}}, "v3.9.0"),
		"retentionPercentage":                           f(m{"retentionPercentage": 80}, "v3.10.0"),
		"ruleQueryOffset":                               f(m{"ruleQueryOffset": "30s"}, "v2.52.0"),
		"queryLogFile":                                  f(m{"queryLogFile": "/dev/stdout"}, "v2.15.0"),
		"scrapeFailureLogFile":                          f(m{"scrapeFailureLogFile": "scrape-failures.log"}, "v2.54.0"),
		"nameValidationScheme":                          f(m{"nameValidationScheme": "UTF8"}, "v2.55.1"),
		"nameEscapingScheme":                            f(m{"nameEscapingScheme": "Underscores"}, "v3.3.0"),
		"convertClassicHistogramsToNHCB":                f(m{"convertClassicHistogramsToNHCB": true}, "v3.3.0"),
		"scrapeClassicHistograms":                       f(m{"scrapeClassicHistograms": false}, "v3.4.0"),
		"scrapeNativeHistograms":                        f(m{"scrapeNativeHistograms": true}, "v3.7.0"),

		"otlp": f(otlp(m{}), "v2.54.0"),
		// otlp is refused first, at the same minimum.
		"otlp.promoteResourceAttributes":                             masked(otlp(m{"promoteResourceAttributes": []any{"service.instance.id"}}), "v2.54.0", "otlp", ""),
		"otlp.translationStrategy":                                   f(otlp(m{"translationStrategy": "UnderscoreEscapingWithSuffixes"}), "v2.55.1"),
		"otlp.translationStrategy=NoTranslation":                     f(otlp(m{"translationStrategy": "NoTranslation"}), "v3.3.0"),
		"otlp.translationStrategy=UnderscoreEscapingWithoutSuffixes": f(otlp(m{"translationStrategy": "UnderscoreEscapingWithoutSuffixes"}), "v3.5.0"),
		"otlp.keepIdentifyingResourceAttributes":                     f(otlp(m{"keepIdentifyingResourceAttributes": true}), "v3.0.0"),
		"otlp.convertHistogramsToNHCB":                               f(otlp(m{"convertHistogramsToNHCB": true}), "v3.3.0"),
		"otlp.promoteAllResourceAttributes":                          f(otlp(m{"promoteAllResourceAttributes": true}), "v3.4.0"),
		"otlp.ignoreResourceAttributes":                              f(otlp(m{"ignoreResourceAttributes": []any{"service.instance.id"}}), "v3.4.0"),
		"otlp.promoteScopeMetadata":                                  f(otlp(m{"promoteScopeMetadata": true}), "v3.5.0"),
		"otlp.labelNameUnderscoreSanitization":                       f(otlp(m{"labelNameUnderscoreSanitization": true}), "v3.7.0"),
		"otlp.labelNamePreserveMultipleUnderscores":                  f(otlp(m{"labelNamePreserveMultipleUnderscores": true}), "v3.7.0"),

		"alerting.alertmanagers[].enableHttp2":   f(am(m{"enableHttp2": true}), "v2.34.0"),
		"alerting.alertmanagers[].basicAuth":     f(am(m{"basicAuth": m{"username": ref("am", "username"), "password": ref("am", "password")}}), "v2.25.0"),
		"alerting.alertmanagers[].authorization": f(am(m{"authorization": credentials}), "v2.25.0"),
		"alerting.alertmanagers[].sigv4":         f(am(m{"sigv4": m{"region": "eu-west-1"}}), "v2.47.0"),
		"alerting.alertmanagers[].sigv4.externalId": f(am(m{"sigv4": m{"region": "eu-west-1", "roleArn": "arn:aws:iam::123456789012:role/alerts", "externalId": "payments"}}),
			"v3.10.0"),
		"alerting.alertmanagers[].sigv4.useFIPSSTSEndpoint": f(am(m{"sigv4": m{"region": "eu-west-1", "useFIPSSTSEndpoint": true}}), "v2.53.0"),
		"alerting.alertmanagers[].apiVersion":               f(am(m{"apiVersion": "v2"}), "v2.10.0"),
		"alerting.alertmanagers[].alertRelabelings": f(am(m{"alertRelabelings": []any{m{"action": "drop", "sourceLabels": []any{"severity"}, "regex": "none"}}}),
			"v2.50.0"),

		"remoteRead[].name":                 f(rr(m{"name": "primary"}), "v2.14.0"),
		"remoteRead[].headers":              f(rr(m{"headers": m{"X-Scope-OrgID": "payments"}}), "v2.25.0"),
		"remoteRead[].authorization":        f(rr(m{"authorization": credentials}), "v2.25.0"),
		"remoteRead[].followRedirects":      f(rr(m{"followRedirects": true}), "v2.25.0"),
		"remoteRead[].oauth2":               f(rr(m{"oauth2": oauth2(nil)}), "v2.26.0"),
		"remoteRead[].filterExternalLabels": f(rr(m{"filterExternalLabels": false}), "v2.33.0"),

		"remoteWrite[].headers":                  f(rw(m{"headers": m{"X-Scope-OrgID": "payments"}}), "v2.14.0"),
		"remoteWrite[].name":                     f(rw(m{"name": "primary"}), "v2.14.0"),
		"remoteWrite[].messageVersion":           f(rw(m{"messageVersion": "V2.0"}), "v2.53.0"),
		"remoteWrite[].sendExemplars":            f(rw(m{"sendExemplars": true}), "v2.26.0"),
		"remoteWrite[].sendNativeHistograms":     f(rw(m{"sendNativeHistograms": true}), "v2.39.0"),
		"remoteWrite[].authorization":            f(rw(m{"authorization": credentials}), "v2.25.0"),
		"remoteWrite[].oauth2":                   f(rw(m{"oauth2": oauth2(nil)}), "v2.26.0"),
		"remoteWrite[].sigv4":                    f(rw(m{"sigv4": m{"region": "eu-west-1"}}), "v2.25.0"),
		"remoteWrite[].sigv4.externalId":         f(rw(m{"sigv4": m{"region": "eu-west-1", "roleArn": "arn:aws:iam::123456789012:role/metrics", "externalId": "payments"}}), "v3.10.0"),
		"remoteWrite[].sigv4.useFIPSSTSEndpoint": f(rw(m{"sigv4": m{"region": "eu-west-1", "useFIPSSTSEndpoint": true}}), "v2.53.0"),
		"remoteWrite[].azureAd":                  f(azure(m{"managedIdentity": m{"clientId": "00000000-0000-0000-0000-000000000000"}}), "v2.44.0"),
		"remoteWrite[].azureAd.oauth": f(azure(m{"oauth": m{
			"clientId": "00000000-0000-0000-0000-000000000000", "clientSecret": ref("azure", "secret"), "tenantId": "00000000-0000-0000-0000-000000000001",
		}}), "v2.47.0"),
		"remoteWrite[].azureAd.sdk": f(azure(m{"sdk": m{"tenantId": "00000000-0000-0000-0000-000000000001"}}), "v2.51.0"),
		"remoteWrite[].azureAd.workloadIdentity": f(azure(m{"workloadIdentity": m{
			"clientId": "00000000-0000-0000-0000-000000000000", "tenantId": "00000000-0000-0000-0000-000000000001",
		}}), "v3.6.0"),
		"remoteWrite[].azureAd.scope": f(azure(m{"managedIdentity": m{"clientId": "00000000-0000-0000-0000-000000000000"}, "scope": "https://monitor.azure.com//.default"}),
			"v3.8.0"),
		`remoteWrite[].azureAd.managedIdentity.clientId=""`: f(azure(m{"managedIdentity": m{}}), "v3.4.0"),
		"remoteWrite[].followRedirects":                     f(rw(m{"followRedirects": true}), "v2.25.0"),
		"remoteWrite[].enableHTTP2":                         f(rw(m{"enableHTTP2": true}), "v2.34.0"),
		"remoteWrite[].queueConfig.minShards":               f(rw(m{"queueConfig": m{"minShards": 1}}), "v2.5.0"),
		"remoteWrite[].queueConfig.retryOnRateLimit":        f(rw(m{"queueConfig": m{"retryOnRateLimit": true}}), "v2.25.0"),
		"remoteWrite[].queueConfig.sampleAgeLimit":          f(rw(m{"queueConfig": m{"sampleAgeLimit": "5m"}}), "v2.49.0"),
		"remoteWrite[].metadataConfig":                      f(rw(m{"metadataConfig": m{"send": true}}), "v2.22.0"),
		"remoteWrite[].metadataConfig.maxSamplesPerSend":    f(rw(m{"metadataConfig": m{"maxSamplesPerSend": 500}}), "v2.28.0"),
		"remoteWrite[].roundRobinDNS":                       f(rw(m{"roundRobinDNS": true}), "v3.0.0"),

		"apiserverConfig.authorization": f(api(m{"authorization": credentials}), "v2.25.0"),
		"scrapeClasses[].authorization": f(sc(m{"authorization": credentials}), "v2.25.0"),

		"thanos.grpcServerTlsConfig.minVersion":   f(thanos(m{"grpcServerTlsConfig": m{"minVersion": "TLS12"}}), "v0.36.0"),
		"thanos.grpcServerTlsConfig.cipherSuites": f(thanos(m{"grpcServerTlsConfig": m{"cipherSuites": []any{"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"}}}), "v0.41.0"),
		"thanos.grpcServerTlsConfig.curves":       f(thanos(m{"grpcServerTlsConfig": m{"curves": []any{"X25519"}}}), "v0.41.0"),
		"thanos.getConfigTimeout":                 f(thanos(m{"getConfigTimeout": "30s"}), "v0.28.0"),
		"thanos.getConfigInterval":                f(thanos(m{"getConfigInterval": "1m"}), "v0.28.0"),
	}

	// The OAuth2 proxy and TLS settings: oauth2 itself is gated lower.
	for _, o := range []struct {
		path   string
		holder func(m) m
	}{{"remoteRead[].oauth2", rr}, {"remoteWrite[].oauth2", rw}} {
		fixtures[o.path+".proxyUrl"] = f(o.holder(m{"oauth2": oauth2(m{"proxyUrl": proxy})}), "v2.42.0")
		fixtures[o.path+".tlsConfig"] = f(o.holder(m{"oauth2": oauth2(m{"tlsConfig": m{"insecureSkipVerify": true}})}), "v2.42.0")
	}
	// The proxy settings. noProxy and proxyConnectHeader need proxyUrl,
	// which is refused with them, at the same minimum, where it is gated too.
	proxies := map[string]func(m) m{
		"apiserverConfig":          api,
		"alerting.alertmanagers[]": am,
		"remoteRead[]":             rr,
		"remoteWrite[]":            rw,
		"remoteRead[].oauth2":      func(p m) m { return rr(m{"oauth2": oauth2(p)}) },
		"remoteWrite[].oauth2":     func(p m) m { return rw(m{"oauth2": oauth2(p)}) },
	}
	for holder, author := range proxies {
		gatedURL := ""
		if strings.HasSuffix(holder, ".oauth2") {
			gatedURL = holder + ".proxyUrl"
		}
		fixtures[holder+".noProxy"] = masked(author(m{"proxyUrl": proxy, "noProxy": "internal.example.com"}), "v2.42.0", gatedURL, "")
		fixtures[holder+".proxyConnectHeader"] = masked(author(m{"proxyUrl": proxy, "proxyConnectHeader": header}), "v2.42.0", gatedURL, "")
		fixtures[holder+".proxyFromEnvironment"] = f(author(m{"proxyFromEnvironment": true}), "v2.42.0")
	}
	// The TLS versions. Under oauth2, its tlsConfig is gated higher.
	tlsHolders := map[string]func(m) m{
		"apiserverConfig.tlsConfig":          func(t m) m { return api(m{"tlsConfig": t}) },
		"alerting.alertmanagers[].tlsConfig": func(t m) m { return am(m{"tlsConfig": t}) },
		"remoteRead[].tlsConfig":             func(t m) m { return rr(m{"tlsConfig": t}) },
		"remoteWrite[].tlsConfig":            func(t m) m { return rw(m{"tlsConfig": t}) },
		"remoteRead[].oauth2.tlsConfig":      func(t m) m { return rr(m{"oauth2": oauth2(m{"tlsConfig": t})}) },
		"remoteWrite[].oauth2.tlsConfig":     func(t m) m { return rw(m{"oauth2": oauth2(m{"tlsConfig": t})}) },
		"tracingConfig.tlsConfig":            func(t m) m { return tracing(m{"tlsConfig": t}) },
		"scrapeClasses[].tlsConfig":          func(t m) m { return sc(m{"tlsConfig": t}) },
	}
	for holder, author := range tlsHolders {
		parent, bound := "", ""
		if strings.Contains(holder, ".oauth2.") {
			parent, bound = holder, "2.43.0"
		}
		fixtures[holder+".minVersion"] = masked(author(m{"minVersion": "TLS12"}), "v2.34.0", parent, bound)
		fixtures[holder+".maxVersion"] = masked(author(m{"maxVersion": "TLS13"}), "v2.40.0", parent, bound)
	}
	// The relabeling actions. alertRelabelings is gated higher.
	relabelings := map[string]func(l []any) m{
		"alerting.alertmanagers[].relabelings":      func(l []any) m { return am(m{"relabelings": l}) },
		"alerting.alertmanagers[].alertRelabelings": func(l []any) m { return am(m{"alertRelabelings": l}) },
		"scrapeClasses[].relabelings":               func(l []any) m { return sc(m{"relabelings": l}) },
		"scrapeClasses[].metricRelabelings":         func(l []any) m { return sc(m{"metricRelabelings": l}) },
	}
	for list, author := range relabelings {
		parent, bound := "", ""
		if list == "alerting.alertmanagers[].alertRelabelings" {
			parent, bound = list, "2.51.0"
		}
		fixtures[list+"[].action=lowercase|uppercase"] = masked(author([]any{m{"action": "lowercase", "sourceLabels": []any{"job"}, "targetLabel": "job_lower"}}),
			"v2.35.0", parent, bound)
		fixtures[list+"[].action=keepequal|dropequal"] = masked(author([]any{m{"action": "keepequal", "sourceLabels": []any{"job"}, "targetLabel": "job"}}),
			"v2.40.0", parent, bound)
	}
	return fixtures
}

// promBuild builds props as the prometheus kind, with version, where not
// empty, as field: version or thanos.version.
func promBuild(t *testing.T, props map[string]any, field, version string) error {
	t.Helper()
	p := maps.Clone(props)
	switch {
	case version == "":
	case field == "thanos.version":
		th, _ := p["thanos"].(map[string]any)
		th = maps.Clone(th)
		if th == nil {
			th = map[string]any{}
		}
		th["version"] = version
		p["thanos"] = th
	default:
		p["version"] = version
	}
	_, err := (&PrometheusHandler{}).ToApplicationConfig(&oam.Component{Name: "main", Type: "prometheus", Properties: p}, "monitoring")
	return err
}

// TestPrometheusVersionGates_Refused: each id of the gate tables, authored
// alone with a version under its minimum, is refused naming the path, the
// product, the bound and the version, or, authored with another field below
// its version, naming both with their versions (refuseVersionMisses); at its
// bound it builds, and with the version unset too, where the operator's
// default, above every minimum, is compared. A field that is also a
// ceiling's is refused unset instead.
func TestPrometheusVersionGates_Refused(t *testing.T) {
	fixtures := prometheusGateFixtures()
	for _, table := range []struct {
		product, field string
		gates          []idGate[monitoringv1.PrometheusSpec]
	}{
		{"Prometheus", "version", prometheusVersionGates},
		{"Thanos", "thanos.version", prometheusThanosVersionGates},
	} {
		for _, g := range table.gates {
			id := g.id
			if id == "" {
				id = g.gate.path
			}
			// Taken here, not in the subtest, so the leftover check below
			// holds whichever subtests -run selects.
			fx, ok := fixtures[id]
			delete(fixtures, id)
			t.Run(id, func(t *testing.T) {
				if !ok {
					t.Fatalf("no fixture authors %s", id)
				}
				minimum := semver.MustParse(g.gate.minimum)
				below, err := semver.ParseTolerant(fx.below)
				if err != nil || !below.LT(minimum) {
					t.Fatalf("below %s: want a version that parses and is under %s (err %v)", fx.below, g.gate.minimum, err)
				}
				path, bound := cmp.Or(fx.path, g.gate.path), cmp.Or(fx.bound, g.gate.minimum)
				if semver.MustParse(bound).LT(minimum) {
					t.Fatalf("bound %s is under the id's minimum %s", bound, g.gate.minimum)
				}
				want := []string{fmt.Sprintf("%s: read by the Prometheus operator only for %s %s and later; at %s %s it ", path, table.product, bound, table.field, fx.below)}
				if fx.path != "" {
					want = []string{
						fmt.Sprintf("%s: refused: fields outside their versions at %s %s: ", table.field, table.field, fx.below),
						fmt.Sprintf("%s (read from %s %s)", path, table.product, bound),
						fmt.Sprintf("%s (read from %s %s)", g.gate.path, table.product, g.gate.minimum),
					}
				}
				err = promBuild(t, fx.props, table.field, fx.below)
				for _, w := range want {
					if err == nil || !strings.Contains(err.Error(), w) {
						t.Errorf("at %s: err = %v, want one mentioning %q", fx.below, err, w)
					}
				}
				if err := promBuild(t, fx.props, table.field, "v"+bound); err != nil {
					t.Errorf("at v%s: err = %v, want it built", bound, err)
				}
				ceiling := slices.ContainsFunc(prometheusVersionCeilings, func(c versionCeiling[monitoringv1.PrometheusSpec]) bool { return cmp.Or(c.id, c.path) == id })
				err = promBuild(t, fx.props, table.field, "")
				switch {
				case ceiling && (err == nil || !strings.Contains(err.Error(), g.gate.path+": read by the Prometheus operator only for Prometheus below ")):
					t.Errorf("version unset: err = %v, want the ceiling of %s at the default %s", err, g.gate.path, prometheusDefaultVersion)
				case !ceiling && err != nil:
					t.Errorf("version unset: err = %v, want it built at the default", err)
				}
			})
		}
	}
	for id := range fixtures {
		t.Errorf("a fixture authors %s, which the tables do not list", id)
	}
}

// TestPrometheusVersionGates_Ceilings: each field of
// prometheusVersionCeilings is refused at its maximum, and with version
// unset, at the default; it builds under its maximum.
func TestPrometheusVersionGates_Ceilings(t *testing.T) {
	type m = map[string]any
	fixtures := map[string]struct {
		props       m
		at, builds  string
		bound, says string
	}{
		"alerting.alertmanagers[].apiVersion=v1": {
			m{"alerting": m{"alertmanagers": []any{m{"namespace": "monitoring", "name": "alertmanager-operated", "port": "web", "apiVersion": "v1"}}}},
			// read from 2.11.0 on too: the fix names both bounds
			// (go-kure/launcher#1047).
			"v3.0.0-rc.0", "v2.55.1", "Prometheus 2.x", "name a version from 2.11.0 on and within Prometheus 2.x, or leave alerting.alertmanagers[].apiVersion unset",
		},
		"remoteWrite[].queueConfig.maxRetries": {
			m{"remoteWrite": []any{m{"url": "https://metrics.example.com/api/v1/write", "queueConfig": m{"maxRetries": 3}}}},
			"v2.11.0", "v2.10.0", "Prometheus below 2.11.0", "lower version below 2.11.0, or leave remoteWrite[].queueConfig.maxRetries unset",
		},
		"allowOverlappingBlocks": {m{"allowOverlappingBlocks": true}, "v2.39.0", "v2.38.0", "Prometheus below 2.39.0", "name a version from 2.11.0 on and below 2.39.0, or leave allowOverlappingBlocks unset"},
	}
	for _, c := range prometheusVersionCeilings {
		id := cmp.Or(c.id, c.path)
		fx, ok := fixtures[id]
		delete(fixtures, id)
		t.Run(id, func(t *testing.T) {
			if !ok {
				t.Fatalf("no fixture authors %s", id)
			}
			for at, judged := range map[string]string{fx.at: "version " + fx.at, "": "the operator's default version " + prometheusDefaultVersion} {
				want := fmt.Sprintf("%s: read by the Prometheus operator only for %s; at %s it %s; %s", c.path, fx.bound, judged, c.above, fx.says)
				if err := promBuild(t, fx.props, "version", at); err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("at %q: err = %v, want one mentioning %q", at, err, want)
				}
			}
			if err := promBuild(t, fx.props, "version", fx.builds); err != nil {
				t.Errorf("at %s: err = %v, want it built", fx.builds, err)
			}
		})
	}
	for id := range fixtures {
		t.Errorf("a fixture authors %s, which prometheusVersionCeilings does not list", id)
	}
}

// TestPrometheusVersionGates_Cases holds the version check's cases beside
// the tables: the floor, an unparsable version, the value and major rows,
// and the default named where the version is unset.
func TestPrometheusVersionGates_Cases(t *testing.T) {
	type m = map[string]any
	am := func(apiVersion string) m {
		return m{"alerting": m{"alertmanagers": []any{m{"namespace": "monitoring", "name": "alertmanager-operated", "port": "web", "apiVersion": apiVersion}}}}
	}
	sc := m{"scrapeClasses": []any{m{"name": "default", "attachMetadata": m{"node": true}}}}
	merge := func(a, b m) m {
		out := maps.Clone(a)
		maps.Copy(out, b)
		return out
	}
	// blockSize is a thanos block with object storage and blockSize, and the
	// fields of thanos.
	blockSize := func(thanos m) m {
		return m{"thanos": merge(m{"objectStorageConfig": m{"name": "objstore", "key": "objstore.yml"}, "blockSize": "4h"}, thanos)}
	}
	rw := func(extra m) m {
		return m{"remoteWrite": []any{merge(m{"url": "https://metrics.example.com/api/v1/write"}, extra)}}
	}
	cases := []struct {
		name    string
		props   m
		field   string
		version string
		want    string // "" builds
	}{
		{"major 4", m{}, "version", "v4.0.0", `version: "v4.0.0" is not supported by the Prometheus operator, which runs Prometheus of major version 2 or 3 only`},
		{"major 1", m{}, "version", "v1.8.0", `version: "v1.8.0" is not supported by the Prometheus operator, which runs Prometheus of major version 2 or 3 only`},
		{"unparsable version admits a gated field", m{"retentionPercentage": 80}, "version", "latest", ""},
		{"unparsable thanos.version admits a gated field", m{"thanos": m{"getConfigTimeout": "30s"}}, "thanos.version", "main", ""},
		{"unset thanos.version is the default", m{"thanos": m{"grpcServerTlsConfig": m{"curves": []any{"X25519"}}}}, "thanos.version", "", ""},
		{"unset version names the default", m{"allowOverlappingBlocks": true}, "version", "", "at the operator's default version v3.14.0 it "},
		{"apiVersion v1 at v2.55.1", am("v1"), "version", "v2.55.1", ""},
		{"apiVersion v1 at v3.0.0-rc.0", am("v1"), "version", "v3.0.0-rc.0", "only for Prometheus 2.x; at version v3.0.0-rc.0 it "},
		{"apiVersion v1 at v3.0.0", am("v1"), "version", "v3.0.0", "only for Prometheus 2.x; at version v3.0.0 it "},
		{"apiVersion v2 at v3.14.0", am("v2"), "version", "v3.14.0", ""},
		{"EndpointSlice at v2.20.1", m{"serviceDiscoveryRole": "EndpointSlice"}, "version", "v2.20.1", "serviceDiscoveryRole: read by the Prometheus operator only for Prometheus 2.21.0 and later"},
		{"Endpoints at v2.20.1", m{"serviceDiscoveryRole": "Endpoints"}, "version", "v2.20.1", ""},
		{"enforcedTargetLimit at v2.20.1", m{"enforcedTargetLimit": 1000}, "version", "v2.20.1", "enforcedTargetLimit: read by the Prometheus operator only for Prometheus 2.21.0 and later"},
		{"enforcedTargetLimit at v2.21.0", m{"enforcedTargetLimit": 1000}, "version", "v2.21.0", ""},
		{"enforcedTargetLimit of 0 is written at no version", m{"enforcedTargetLimit": 0}, "version", "v2.20.1", ""},
		{"sampleLimit of 0 is written at no version", m{"sampleLimit": 0}, "version", "v2.44.0", ""},
		{"keepDroppedTargets of 0 is written at no version", m{"keepDroppedTargets": 0}, "version", "v2.46.0", ""},
		{"blockSize with object storage at the defaults", blockSize(m{}), "version", "",
			"thanos.blockSize: read by the Prometheus operator only where it disables local compaction: with disableCompaction, or with Prometheus below 3.9.0 or Thanos below 0.42.0; at the operator's default version v3.14.0 and the operator's default Thanos version v0.42.4, with object storage set, it "},
		{"blockSize with object storage at v3.9.0 and v0.42.0", blockSize(m{"version": "v0.42.0"}), "version", "v3.9.0", "at version v3.9.0 and thanos.version v0.42.0, with object storage set"},
		{"blockSize with object storage below v3.9.0", blockSize(m{}), "version", "v3.8.1", ""},
		{"blockSize with object storage below Thanos v0.42.0", blockSize(m{"version": "v0.41.0"}), "version", "v3.14.0", ""},
		{"blockSize with object storage and disableCompaction", merge(blockSize(m{}), m{"disableCompaction": true}), "version", "", ""},
		{"blockSize without object storage", m{"thanos": m{"blockSize": "4h"}}, "version", "", ""},
		{"blockSize of the CRD's default 2h with object storage", blockSize(m{"blockSize": "2h"}), "version", "", ""},
		{"blockSize with an object storage file", m{"thanos": m{"objectStorageConfigFile": "/etc/thanos/objstore.yml", "blockSize": "4h"}}, "version", "",
			"thanos.blockSize: read by the Prometheus operator only where it disables local compaction"},
		{"blockSize with an unparsable thanos.version", blockSize(m{"version": "main"}), "version", "", ""},
		{"blockSize with an empty thanos.version, which the operator cannot parse", blockSize(m{"version": ""}), "version", "", ""},
		{"an azureAd method below both its version and azureAd's names both", m{"remoteWrite": []any{m{
			"url": "https://metrics.example.com/api/v1/write", "azureAd": m{"sdk": m{"tenantId": "00000000-0000-0000-0000-000000000001"}},
		}}}, "version", "v2.44.0", "version: refused: fields outside their versions at version v2.44.0: remoteWrite[].azureAd.sdk (read from Prometheus 2.52.0), remoteWrite[].azureAd (read from Prometheus 2.45.0); the Prometheus operator does not run the object as authored there; raise version to 2.52.0 or later, or leave those fields unset"},
		// go-kure/launcher#1022: of several fields below their versions, the
		// operator may reach any consequence first, so none is claimed.
		{"a dropped and a refused field name both, and no consequence", rw(m{"messageVersion": "V1.0", "azureAd": m{"sdk": m{"tenantId": "00000000-0000-0000-0000-000000000001"}}}), "version", "v2.50.0",
			"version: refused: fields outside their versions at version v2.50.0: remoteWrite[].messageVersion (read from Prometheus 2.54.0), remoteWrite[].azureAd.sdk (read from Prometheus 2.52.0); the Prometheus operator does not run the object as authored there; raise version to 2.54.0 or later, or leave those fields unset"},
		{"a panicking and a refused field name both", rw(m{"authorization": m{"credentials": m{"name": "token", "key": "token"}}, "azureAd": m{"sdk": m{"tenantId": "00000000-0000-0000-0000-000000000001"}}}), "version", "v2.25.0",
			"version: refused: fields outside their versions at version v2.25.0: "},
		{"otlp and a panicking remote write authorization name both", merge(m{"otlp": m{"promoteResourceAttributes": []any{"service.instance.id"}}}, rw(m{"authorization": m{"credentials": m{"name": "token", "key": "token"}}})), "version", "v2.25.0",
			"(read from Prometheus 2.26.0)"},
		{"a field below its minimum and one at its ceiling name both, and no single version", rw(m{"messageVersion": "V1.0", "queueConfig": m{"maxRetries": 5}}), "version", "v2.40.0",
			"version: refused: fields outside their versions at version v2.40.0: remoteWrite[].messageVersion (read from Prometheus 2.54.0), remoteWrite[].queueConfig.maxRetries (read below Prometheus 2.11.0); the Prometheus operator does not run the object as authored there; the operator reads the fields authored at no single version (from 2.54.0 on and below 2.11.0): leave those fields unset"},
		// The version suggested keeps every field authored read, a ceiling
		// the version named does not miss included.
		{"a ceiling not missed bounds the version suggested", merge(m{"allowOverlappingBlocks": true}, rw(m{"followRedirects": true})), "version", "v2.10.0",
			"; the Prometheus operator does not run the object as authored there; name a version from 2.26.0 on and below 2.39.0, or leave those fields unset"},
		{"a ceiling not missed leaves no version to suggest", merge(m{"allowOverlappingBlocks": true}, rw(m{"messageVersion": "V1.0"})), "version", "v2.10.0",
			"; the Prometheus operator does not run the object as authored there; the operator reads the fields authored at no single version (from 2.54.0 on and below 2.39.0): leave those fields unset"},
		{"a ceiling shown as a major bounds the version suggested", merge(am("v1"), rw(m{"messageVersion": "V1.0", "azureAd": m{"sdk": m{"tenantId": "00000000-0000-0000-0000-000000000001"}}})), "version", "v2.50.0",
			"; the Prometheus operator does not run the object as authored there; name a version from 2.54.0 on and within Prometheus 2.x, or leave those fields unset"},
		{"two ceilings missed suggest below the lower, from the minima met", merge(am("v1"), m{"allowOverlappingBlocks": true}), "version", "v3.0.0",
			"version: refused: fields outside their versions at version v3.0.0: alerting.alertmanagers[].apiVersion (read for Prometheus 2.x), allowOverlappingBlocks (read below Prometheus 2.39.0); the Prometheus operator does not run the object as authored there; name a version from 2.11.0 on and below 2.39.0, or leave those fields unset"},
		// go-kure/launcher#1047: a single field outside its versions is fixed
		// within the same bounds, of the fields authored but not missed too, a
		// minimum or a ceiling.
		{"a single field below its minimum suggests no version a ceiling not missed excludes", merge(m{"allowOverlappingBlocks": true}, rw(m{"messageVersion": "V1.0"})), "version", "v2.20.0",
			"remoteWrite[].messageVersion: read by the Prometheus operator only for Prometheus 2.54.0 and later; at version v2.20.0 it drops it from the configuration it generates, and Prometheus runs without it; the operator reads the fields authored at no single version (from 2.54.0 on and below 2.39.0): leave remoteWrite[].messageVersion unset"},
		{"a single field below its minimum is bounded by a ceiling not missed", merge(m{"allowOverlappingBlocks": true}, rw(m{"followRedirects": true})), "version", "v2.20.0",
			"; name a version from 2.26.0 on and below 2.39.0, or leave remoteWrite[].followRedirects unset"},
		{"a single field at its ceiling is bounded by a minimum met", merge(am("v1"), rw(m{"messageVersion": "V1.0"})), "version", "v3.0.0",
			"alerting.alertmanagers[].apiVersion: read by the Prometheus operator only for Prometheus 2.x; at version v3.0.0 it drops it from the configuration it generates, and Prometheus sends alerts through the Alertmanager API v2; name a version from 2.54.0 on and within Prometheus 2.x, or leave alerting.alertmanagers[].apiVersion unset"},
		{"a single field at its ceiling suggests no version a minimum met excludes", merge(m{"allowOverlappingBlocks": true}, rw(m{"messageVersion": "V1.0"})), "version", "v3.0.0",
			"allowOverlappingBlocks: read by the Prometheus operator only for Prometheus below 2.39.0; at version v3.0.0 it does not pass it to Prometheus, and the pods run without it; the operator reads the fields authored at no single version (from 2.54.0 on and below 2.39.0): leave allowOverlappingBlocks unset"},
		{"a remote write authorization below 2.26.0 names the panic", m{"remoteWrite": []any{m{
			"url": "https://metrics.example.com/api/v1/write", "authorization": m{"credentials": m{"name": "token", "key": "token"}},
		}}}, "version", "v2.25.0", "at version v2.25.0 it panics while generating the configuration"},
		{"attachMetadata at v2.34.0", sc, "version", "v2.34.0", "scrapeClasses[].attachMetadata: read by the Prometheus operator only for Prometheus 2.37.0 and later"},
		{"attachMetadata at v2.36.0", sc, "version", "v2.36.0", "at version v2.36.0 it drops it from the scrape configurations of the ServiceMonitors"},
		{"attachMetadata at v2.37.0", sc, "version", "v2.37.0", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := promBuild(t, c.props, c.field, c.version)
			switch {
			case c.want == "" && err != nil:
				t.Errorf("err = %v, want it built", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Errorf("err = %v, want one mentioning %q", err, c.want)
			}
		})
	}
}

// TestPrometheusAzureADWithoutMethod_MatchVendoredSource holds
// thanosRulerAzureADMethods to the operator's source: validateRemoteWriteSpec
// (pkg/prometheus/operator.go) refuses an azureAd where each of them, and no
// other field, is nil. TestPrometheusAzureADWithoutMethod_Refused builds an
// azureAd with each of them in turn, so refuseAzureADWithoutMethod accepts
// every method the operator does.
func TestPrometheusAzureADWithoutMethod_MatchVendoredSource(t *testing.T) {
	src := loadVendoredPromOp(t)
	const op = "pkg/prometheus/operator.go"
	file := src.files[op]
	if file == nil {
		t.Fatalf("the excerpt holds no %s", op)
	}
	jsonName := map[string]string{}
	for f := range reflect.TypeFor[monitoringv1.AzureAD]().Fields() {
		jsonName[f.Name], _, _ = strings.Cut(f.Tag.Get("json"), ",")
	}
	var conds [][]string
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || funcName(fd) != "(*ConfigGenerator).validateRemoteWriteSpec" {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			s, isIf := n.(*ast.IfStmt)
			if !isIf {
				return true
			}
			cond := src.text(s.Cond)
			if !strings.HasPrefix(cond, "spec.AzureAD.") || !strings.Contains(cond, "== nil") || strings.Contains(cond, "!=") {
				return true
			}
			var methods []string
			for term := range strings.SplitSeq(cond, "&&") {
				field, isNil := strings.CutSuffix(strings.TrimSpace(term), " == nil")
				name, inAzure := strings.CutPrefix(field, "spec.AzureAD.")
				if !isNil || !inAzure || jsonName[name] == "" {
					t.Errorf("%s: %q is not a test of a field of azureAd being nil", op, term)
					continue
				}
				methods = append(methods, jsonName[name])
			}
			slices.Sort(methods)
			conds = append(conds, methods)
			return true
		})
	}
	if len(conds) != 1 {
		t.Fatalf("%s: validateRemoteWriteSpec tests azureAd for no method %d times, want once", op, len(conds))
	}
	if want := slices.Sorted(slices.Values(thanosRulerAzureADMethods)); !slices.Equal(conds[0], want) {
		t.Errorf("the operator requires one of %v, refuseAzureADWithoutMethod one of %v", conds[0], want)
	}
}

// TestPrometheusAzureADWithoutMethod_Refused: an azureAd that sets no
// authentication method is refused at every version the operator generates a
// configuration for, ahead of the version gates, naming the entry; one that
// sets any one method builds at the operator's default. The object sets a
// monitor selector, so that the operator generates the configuration whatever
// it is run with; the kind, which cannot see that, refuses one without as
// well.
func TestPrometheusAzureADWithoutMethod_Refused(t *testing.T) {
	type m = map[string]any
	const url = "https://metrics.example.com/api/v1/write"
	entry := func(azure m) m { return m{"url": url, "azureAd": azure} }
	none := m{"remoteWrite": []any{m{"url": url}, entry(m{"cloud": "AzurePublic"})}, "serviceMonitorSelector": m{}}
	want := "remoteWrite[1].azureAd: no authentication method: the Prometheus operator requires one of managedIdentity, oauth, sdk and workloadIdentity, and refuses the object at reconcile without one, at every version, wherever it generates the remote write configuration; set one, or leave azureAd unset"
	for _, version := range []string{"", "v2.44.0", "v2.45.0", "v3.14.0"} {
		if err := promBuild(t, none, "version", version); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("at %q: err = %v, want one mentioning %q", version, err, want)
		}
	}
	// The operator fails in NewConfigGenerator first, before it reads the
	// entries: the major is refused, and an unparsable version left to it.
	if err := promBuild(t, none, "version", "v1.0.0"); err == nil || !strings.HasPrefix(err.Error(), `version: "v1.0.0" is not supported`) {
		t.Errorf("at v1.0.0: err = %v, want the major refused", err)
	}
	if err := promBuild(t, none, "version", "main"); err != nil {
		t.Errorf("at an unparsable version: err = %v, want it left to the operator", err)
	}
	methods := map[string]m{
		"managedIdentity":  {"clientId": "00000000-0000-0000-0000-000000000000"},
		"oauth":            {"clientId": "00000000-0000-0000-0000-000000000000", "clientSecret": m{"name": "azure", "key": "secret"}, "tenantId": "00000000-0000-0000-0000-000000000001"},
		"sdk":              {"tenantId": "00000000-0000-0000-0000-000000000001"},
		"workloadIdentity": {"clientId": "00000000-0000-0000-0000-000000000000", "tenantId": "00000000-0000-0000-0000-000000000001"},
	}
	for _, method := range thanosRulerAzureADMethods {
		v, ok := methods[method]
		if !ok {
			t.Errorf("no fixture sets the method %s", method)
			continue
		}
		if err := promBuild(t, m{"remoteWrite": []any{entry(m{method: v})}}, "version", ""); err != nil {
			t.Errorf("azureAd with %s: err = %v, want it built", method, err)
		}
	}
}
