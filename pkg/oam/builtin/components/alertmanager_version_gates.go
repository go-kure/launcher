package components

import (
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

// The version gates of the alertmanager kind (go-kure/launcher#935; the
// class is described in monitoring_version_gates.go): the fields of
// monitoringv1.AlertmanagerSpec the Prometheus operator reads only from some
// Alertmanager version on, at prometheus-operator v0.94.1.
//
// TestAlertmanagerVersionGates_MatchVendoredSource holds the table to the
// operator's source: every comparison of the Alertmanager version in the
// operator's package pkg/alertmanager, vendored whole, is either a row
// here, at its minimum, or listed in the test with the reason it is not this
// kind's (a field of an AlertmanagerConfig object or of the configSecret
// Secret, or no field at all). It holds alertmanagerDefaultVersion and the
// floor to the same source.

const (
	// alertmanagerDefaultVersion is the version the operator configures for
	// where version is unset (DefaultAlertmanagerVersion,
	// pkg/operator/defaults.go at v0.94.1).
	alertmanagerDefaultVersion = "v0.34.0"
	// alertmanagerMinimumVersion is the lowest version the operator builds
	// the pods for; it refuses a version of a major above 0 too
	// (provisionAlertmanagerConfiguration, pkg/alertmanager/operator.go).
	alertmanagerMinimumVersion = "0.15.0"
	// alertmanagerURLSchemeVersion is the least version whose Alertmanager
	// exits on an externalUrl not of scheme http or https: the least
	// prerelease of v0.19.0, as v0.19.0-rc.0 already does
	// (alertmanagerURLSchemes).
	alertmanagerURLSchemeVersion = "0.19.0-0"
	// alertmanagerGo123Version is the least version whose Alertmanager is
	// built with Go 1.23 or later: the least prerelease of v0.28.0, as
	// v0.28.0-rc.0 already is (alertmanagerParsesIPAsGo123).
	alertmanagerGo123Version = "0.28.0-0"
)

// alertmanagerGlobal is the global block of alertmanagerConfiguration, nil
// where it is not authored.
func alertmanagerGlobal(s *monitoringv1.AlertmanagerSpec) *monitoringv1.AlertmanagerGlobalConfig {
	if s.AlertmanagerConfiguration == nil {
		return nil
	}
	return s.AlertmanagerConfiguration.Global
}

// alertmanagerGlobalHTTP is the httpConfig of that global block, nil where it
// is not authored.
func alertmanagerGlobalHTTP(s *monitoringv1.AlertmanagerSpec) *monitoringv1.HTTPConfigWithProxy {
	if g := alertmanagerGlobal(s); g != nil {
		return g.HTTPConfigWithProxy
	}
	return nil
}

// alertmanagerGlobalSMTP is the smtp of that global block, nil where it is
// not authored.
func alertmanagerGlobalSMTP(s *monitoringv1.AlertmanagerSpec) *monitoringv1.GlobalSMTPConfig {
	if g := alertmanagerGlobal(s); g != nil {
		return g.SMTPConfig
	}
	return nil
}

// alertmanagerGlobalOAuth2 is the oauth2 of the global httpConfig, nil where
// it is not authored.
func alertmanagerGlobalOAuth2(s *monitoringv1.AlertmanagerSpec) *monitoringv1.OAuth2 {
	if h := alertmanagerGlobalHTTP(s); h != nil {
		return h.OAuth2
	}
	return nil
}

// set reports whether a string pointer holds a value other than "".
func set[T ~string](v *T) bool { return v != nil && *v != "" }

const (
	// The consequences under the minimum, by what the operator does there.
	amFlagDropped    = "does not pass it to Alertmanager, and the pods run without it"
	amRefused        = "refuses the object at reconcile, and the Alertmanager is not updated"
	amGlobalDropped  = "drops it from the configuration it generates, and Alertmanager runs without it"
	amWebPlainHTTP   = "mounts no web configuration, and the web endpoint serves plain HTTP without the authored settings"
	amClusterNoTLS   = "mounts no cluster TLS configuration, and the peers gossip without TLS"
	amGlobalCheckRef = "refuses the object at reconcile (checkAlertmanagerGlobalConfigResource), and the Alertmanager is not updated"
)

// alertmanagerVersionGates are the gated fields, by path. Where the operator
// tests a field beside another condition, authored tests the same: a logFormat
// of logfmt, the format Alertmanager uses without the flag, is passed at no
// version, and an oauth2 proxy is dropped only by its proxyUrl, noProxy and
// proxyConnectHeader.
var alertmanagerVersionGates = []versionGate[monitoringv1.AlertmanagerSpec]{
	// The pods (makeStatefulSetSpec, pkg/alertmanager/statefulset.go).
	{"logFormat", "0.16.0", amFlagDropped, func(s *monitoringv1.AlertmanagerSpec) bool {
		return s.LogFormat != "" && s.LogFormat != "logfmt"
	}},
	{"web.getConcurrency", "0.17.0", amFlagDropped, func(s *monitoringv1.AlertmanagerSpec) bool {
		return s.Web != nil && s.Web.GetConcurrency != nil
	}},
	{"web.timeout", "0.17.0", amFlagDropped, func(s *monitoringv1.AlertmanagerSpec) bool {
		return s.Web != nil && s.Web.Timeout != nil
	}},
	{"web.tlsConfig", "0.22.0", amWebPlainHTTP, func(s *monitoringv1.AlertmanagerSpec) bool {
		return s.Web != nil && s.Web.TLSConfig != nil
	}},
	{"web.httpConfig", "0.22.0", amWebPlainHTTP, func(s *monitoringv1.AlertmanagerSpec) bool {
		return s.Web != nil && s.Web.HTTPConfig != nil
	}},
	{"clusterTLS", "0.24.0", amClusterNoTLS, func(s *monitoringv1.AlertmanagerSpec) bool {
		return s.ClusterTLS != nil
	}},
	{"clusterLabel", "0.26.0", amFlagDropped, func(s *monitoringv1.AlertmanagerSpec) bool {
		return s.ClusterLabel != nil
	}},
	{"enableFeatures", "0.27.0", amFlagDropped, func(s *monitoringv1.AlertmanagerSpec) bool {
		return len(s.EnableFeatures) > 0
	}},
	{"limits.maxSilences", "0.28.0", amFlagDropped, func(s *monitoringv1.AlertmanagerSpec) bool {
		return s.Limits != nil && s.Limits.MaxSilences != nil
	}},
	{"limits.maxPerSilenceBytes", "0.28.0", amFlagDropped, func(s *monitoringv1.AlertmanagerSpec) bool {
		return s.Limits != nil && !s.Limits.MaxPerSilenceBytes.IsEmpty()
	}},
	{"clusterPeerName", "0.30.0", amFlagDropped, func(s *monitoringv1.AlertmanagerSpec) bool {
		return set(s.ClusterPeerName)
	}},

	// The global block of alertmanagerConfiguration, as the operator converts
	// it (convertGlobalConfig) and then checks it
	// (checkAlertmanagerGlobalConfigResource) and sanitizes it (the sanitize
	// methods of globalConfig, httpClientConfig, tlsConfig, proxyConfig and
	// oauth2, pkg/alertmanager/amcfg.go).
	{"alertmanagerConfiguration.global.httpConfig.authorization", "0.22.0", amRefused, func(s *monitoringv1.AlertmanagerSpec) bool {
		h := alertmanagerGlobalHTTP(s)
		return h != nil && h.Authorization != nil
	}},
	{"alertmanagerConfiguration.global.httpConfig.oauth2", "0.22.0", amRefused, func(s *monitoringv1.AlertmanagerSpec) bool {
		return alertmanagerGlobalOAuth2(s) != nil
	}},
	{"alertmanagerConfiguration.global.httpConfig.followRedirects", "0.22.0", amGlobalDropped, func(s *monitoringv1.AlertmanagerSpec) bool {
		h := alertmanagerGlobalHTTP(s)
		return h != nil && h.FollowRedirects != nil
	}},
	{"alertmanagerConfiguration.global.telegram", "0.24.0", amGlobalCheckRef, func(s *monitoringv1.AlertmanagerSpec) bool {
		g := alertmanagerGlobal(s)
		return g != nil && g.TelegramConfig != nil
	}},
	{"alertmanagerConfiguration.global.webex", "0.25.0", amGlobalCheckRef, func(s *monitoringv1.AlertmanagerSpec) bool {
		g := alertmanagerGlobal(s)
		return g != nil && g.WebexConfig != nil
	}},
	{"alertmanagerConfiguration.global.httpConfig.enableHttp2", "0.25.0", amGlobalDropped, func(s *monitoringv1.AlertmanagerSpec) bool {
		h := alertmanagerGlobalHTTP(s)
		return h != nil && h.EnableHTTP2 != nil
	}},
	{"alertmanagerConfiguration.global.httpConfig.tlsConfig.minVersion", "0.25.0", amGlobalDropped, func(s *monitoringv1.AlertmanagerSpec) bool {
		h := alertmanagerGlobalHTTP(s)
		return h != nil && h.TLSConfig != nil && set(h.TLSConfig.MinVersion)
	}},
	{"alertmanagerConfiguration.global.httpConfig.tlsConfig.maxVersion", "0.25.0", amGlobalDropped, func(s *monitoringv1.AlertmanagerSpec) bool {
		h := alertmanagerGlobalHTTP(s)
		return h != nil && h.TLSConfig != nil && set(h.TLSConfig.MaxVersion)
	}},
	{"alertmanagerConfiguration.global.httpConfig.oauth2.proxyUrl", "0.25.0", amGlobalDropped, func(s *monitoringv1.AlertmanagerSpec) bool {
		o := alertmanagerGlobalOAuth2(s)
		return o != nil && set(o.ProxyURL)
	}},
	{"alertmanagerConfiguration.global.httpConfig.oauth2.noProxy", "0.25.0", amGlobalDropped, func(s *monitoringv1.AlertmanagerSpec) bool {
		o := alertmanagerGlobalOAuth2(s)
		return o != nil && set(o.NoProxy)
	}},
	{"alertmanagerConfiguration.global.httpConfig.oauth2.proxyConnectHeader", "0.25.0", amGlobalDropped, func(s *monitoringv1.AlertmanagerSpec) bool {
		o := alertmanagerGlobalOAuth2(s)
		return o != nil && len(o.ProxyConnectHeader) > 0
	}},
	{"alertmanagerConfiguration.global.httpConfig.proxyFromEnvironment", "0.26.0", amGlobalDropped, func(s *monitoringv1.AlertmanagerSpec) bool {
		h := alertmanagerGlobalHTTP(s)
		return h != nil && h.ProxyFromEnvironment != nil && *h.ProxyFromEnvironment
	}},
	{"alertmanagerConfiguration.global.httpConfig.noProxy", "0.26.0", amGlobalDropped, func(s *monitoringv1.AlertmanagerSpec) bool {
		h := alertmanagerGlobalHTTP(s)
		return h != nil && set(h.NoProxy)
	}},
	{"alertmanagerConfiguration.global.httpConfig.proxyConnectHeader", "0.26.0", amGlobalDropped, func(s *monitoringv1.AlertmanagerSpec) bool {
		h := alertmanagerGlobalHTTP(s)
		return h != nil && len(h.ProxyConnectHeader) > 0
	}},
	{"alertmanagerConfiguration.global.smtp.tlsConfig", "0.28.0", "drops it from the configuration it generates, and Alertmanager sends mail without the authored TLS settings", func(s *monitoringv1.AlertmanagerSpec) bool {
		m := alertmanagerGlobalSMTP(s)
		return m != nil && m.TLSConfig != nil
	}},
	{"alertmanagerConfiguration.global.jira", "0.28.0", amGlobalCheckRef, func(s *monitoringv1.AlertmanagerSpec) bool {
		g := alertmanagerGlobal(s)
		return g != nil && g.JiraConfig != nil
	}},
	{"alertmanagerConfiguration.global.rocketChat", "0.28.0", amGlobalCheckRef, func(s *monitoringv1.AlertmanagerSpec) bool {
		g := alertmanagerGlobal(s)
		return g != nil && g.RocketChatConfig != nil
	}},
	{"alertmanagerConfiguration.global.smtp.forceImplicitTLS", "0.31.0", amGlobalCheckRef, func(s *monitoringv1.AlertmanagerSpec) bool {
		m := alertmanagerGlobalSMTP(s)
		return m != nil && m.ForceImplicitTLS != nil
	}},
	{"alertmanagerConfiguration.global.mattermost", "0.32.0", amGlobalCheckRef, func(s *monitoringv1.AlertmanagerSpec) bool {
		g := alertmanagerGlobal(s)
		return g != nil && g.MattermostConfig != nil
	}},
}
