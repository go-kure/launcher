package components

import (
	"fmt"
	"strings"
	"testing"

	"github.com/blang/semver/v4"

	"github.com/go-kure/launcher/pkg/oam"
)

// alertmanagerGateFiles are the files of the operator's pkg/alertmanager
// that compare the Alertmanager version: the pods (statefulset.go), the
// configuration it generates (amcfg.go) and the reconcile (operator.go). No
// other file of the package does at v0.94.1 (its tests aside); a refresh of
// the excerpt checks that again.
var alertmanagerGateFiles = []string{
	"pkg/alertmanager/statefulset.go",
	"pkg/alertmanager/amcfg.go",
	"pkg/alertmanager/operator.go",
}

// alertmanagerGateRows classify the gates of alertmanagerGateFiles one by
// one, where a function holds gates of this kind's fields.
var alertmanagerGateRows = func() []gateRow {
	const (
		sts   = "pkg/alertmanager/statefulset.go"
		amcfg = "pkg/alertmanager/amcfg.go"
		op    = "pkg/alertmanager/operator.go"
		pods  = "makeStatefulSetSpec"
		g     = "alertmanagerConfiguration.global."
		h     = g + "httpConfig."
		// The conversion of the global block (convertGlobalConfig) sets none
		// of the *_file fields, the bot token or the Slack app fields of the
		// configuration it generates: only a configSecret Secret, which this
		// kind names and does not author, can.
		notGlobal = "a field of the generated global configuration that alertmanagerConfiguration.global cannot set; only the configSecret Secret can"
	)
	gate := func(file, fn, cond string, paths ...string) gateRow {
		return gateRow{file: file, fn: fn, cond: cond, paths: paths}
	}
	not := func(file, fn, cond, why string) gateRow {
		return gateRow{file: file, fn: fn, cond: cond, not: why}
	}
	global := func(field, cond string) gateRow {
		return not(amcfg, "(*globalConfig).sanitize", cond, notGlobal+" ("+field+")")
	}
	return []gateRow{
		gate(sts, pods, `version.GTE(semver.MustParse("0.27.0")) && len(a.Spec.EnableFeatures) > 0`, "enableFeatures"),
		gate(sts, pods, `version.GTE(semver.MustParse("0.17.0")) && web != nil && web.GetConcurrency != nil`, "web.getConcurrency"),
		gate(sts, pods, `version.GTE(semver.MustParse("0.17.0")) && web != nil && web.Timeout != nil`, "web.timeout"),
		gate(sts, pods, `version.GTE(semver.MustParse("0.28.0")) && limits != nil`, "limits.maxSilences", "limits.maxPerSilenceBytes"),
		gate(sts, pods, `version.GTE(semver.MustParse("0.30.0")) && a.Spec.MinReadySeconds != nil`, "minReadySeconds"),
		gate(sts, pods, `version.GTE(semver.MustParse("0.16.0"))`, "logFormat"),
		gate(sts, pods, `version.GTE(semver.MustParse("0.30.0"))`, "clusterPeerName"),
		gate(sts, pods, `version.GTE(semver.MustParse("0.26.0"))`, "clusterLabel"),
		gate(sts, pods, `a.Spec.Web != nil && a.Spec.Web.TLSConfig != nil && version.GTE(semver.MustParse("0.22.0"))`, "web.tlsConfig"),
		gate(sts, pods, `version.GTE(semver.MustParse("0.22.0"))`, "web.tlsConfig", "web.httpConfig"),
		gate(sts, pods, `version.GTE(semver.MustParse("0.24.0"))`, "clusterTLS"),
		{file: sts, fn: pods, cond: `version.GTE(semver.MustParse("0.30.0"))`, ordinal: 2,
			not: "the POD_NAME variable of the alertmanager container, which the default peer name of the clusterPeerName gate reads; no field"},

		{file: op, fn: "(*Operator).provisionAlertmanagerConfiguration", cond: `version.LT(semver.MustParse("0.15.0")) || version.Major > 0`, floor: true},

		gate(amcfg, "(*ConfigBuilder).checkGlobalSMTPConfig", `sc.ForceImplicitTLS != nil && cb.amVersion.LT(semver.MustParse("0.31.0"))`, g+"smtp.forceImplicitTLS"),
		gate(amcfg, "(*ConfigBuilder).checkGlobalTelegramConfig", `cb.amVersion.LT(semver.MustParse("0.24.0"))`, g+"telegram"),
		gate(amcfg, "(*ConfigBuilder).checkGlobalJiraConfig", `cb.amVersion.LT(semver.MustParse("0.28.0"))`, g+"jira"),
		gate(amcfg, "(*ConfigBuilder).checkGlobalRocketChatConfig", `cb.amVersion.LT(semver.MustParse("0.28.0"))`, g+"rocketChat"),
		gate(amcfg, "(*ConfigBuilder).checkGlobalWebexConfig", `cb.amVersion.LT(semver.MustParse("0.25.0"))`, g+"webex"),
		gate(amcfg, "(*ConfigBuilder).checkGlobalMattermostConfig", `cb.amVersion.LT(semver.MustParse("0.32.0"))`, g+"mattermost"),

		gate(amcfg, "(*globalConfig).sanitize", `gc.SMTPTLSConfig != nil && amVersion.LT(semver.MustParse("0.28.0"))`, g+"smtp.tlsConfig"),
		// Below 0.31.0 and 0.32.0 the global check refuses the object first;
		// the drop is the same field at the same minimum.
		gate(amcfg, "(*globalConfig).sanitize", `gc.SMTPForceImplicitTLS != nil && amVersion.LT(semver.MustParse("0.31.0"))`, g+"smtp.forceImplicitTLS"),
		gate(amcfg, "(*globalConfig).sanitize", `gc.MattermostWebhookURL != nil && amVersion.LT(semver.MustParse("0.32.0"))`, g+"mattermost"),
		global("slack_api_url_file", `amVersion.LT(semver.MustParse("0.22.0"))`),
		global("slack_app_token", `gc.SlackAppToken != "" && amVersion.LT(semver.MustParse("0.30.0"))`),
		global("slack_app_token_file", `gc.SlackAppTokenFile != "" && amVersion.LT(semver.MustParse("0.30.0"))`),
		global("slack_app_url", `gc.SlackAppURL != nil && amVersion.LT(semver.MustParse("0.30.0"))`),
		global("opsgenie_api_key_file", `gc.OpsGenieAPIKeyFile != "" && amVersion.LT(semver.MustParse("0.24.0"))`),
		global("smtp_auth_password_file", `gc.SMTPAuthPasswordFile != "" && amVersion.LT(semver.MustParse("0.25.0"))`),
		global("victorops_api_key_file", `gc.VictorOpsAPIKeyFile != "" && amVersion.LT(semver.MustParse("0.25.0"))`),
		global("wechat_api_secret_file", `gc.WeChatAPISecretFile != "" && amVersion.LT(semver.MustParse("0.31.0"))`),
		global("telegram_bot_token", `gc.TelegramBotToken != "" && amVersion.LT(semver.MustParse("0.31.0"))`),
		global("telegram_bot_token_file", `gc.TelegramBotTokenFile != "" && amVersion.LT(semver.MustParse("0.31.0"))`),
		global("smtp_auth_secret_file", `gc.SMTPAuthSecretFile != "" && amVersion.LT(semver.MustParse("0.31.0"))`),
		global("mattermost_webhook_url_file", `gc.MattermostWebhookURLFile != "" && amVersion.LT(semver.MustParse("0.32.0"))`),

		// The HTTP client, TLS, proxy and OAuth2 settings of the global
		// httpConfig; the same methods sanitize a receiver's, which is an
		// AlertmanagerConfig object's.
		gate(amcfg, "(*httpClientConfig).sanitize", `hc.Authorization != nil && !amVersion.GTE(semver.MustParse("0.22.0"))`, h+"authorization"),
		gate(amcfg, "(*httpClientConfig).sanitize", `hc.OAuth2 != nil && !amVersion.GTE(semver.MustParse("0.22.0"))`, h+"oauth2"),
		gate(amcfg, "(*httpClientConfig).sanitize", `hc.FollowRedirects != nil && !amVersion.GTE(semver.MustParse("0.22.0"))`, h+"followRedirects"),
		gate(amcfg, "(*httpClientConfig).sanitize", `hc.EnableHTTP2 != nil && !amVersion.GTE(semver.MustParse("0.25.0"))`, h+"enableHttp2"),
		not(amcfg, "(*httpClientConfig).sanitize", `!amVersion.GTE(semver.MustParse("0.28.0"))`,
			"http_headers: the global httpConfig has no field for it, and convertGlobalConfig sets none"),
		gate(amcfg, "(*tlsConfig).sanitize", `tc.MinVersion != "" && !amVersion.GTE(semver.MustParse("0.25.0"))`, h+"tlsConfig.minVersion"),
		gate(amcfg, "(*tlsConfig).sanitize", `tc.MaxVersion != "" && !amVersion.GTE(semver.MustParse("0.25.0"))`, h+"tlsConfig.maxVersion"),
		gate(amcfg, "(*proxyConfig).sanitize", `amVersion.GTE(semver.MustParse("0.26.0"))`, h+"proxyFromEnvironment", h+"noProxy", h+"proxyConnectHeader"),
		gate(amcfg, "(*oauth2).sanitize", `(o.ProxyURL != "" || o.NoProxy != "" || len(o.ProxyConnectHeader) > 0) && !amVersion.GTE(semver.MustParse("0.25.0"))`,
			h+"oauth2.proxyUrl", h+"oauth2.noProxy", h+"oauth2.proxyConnectHeader"),
	}
}()

// alertmanagerGateFuncs classify every gate of a function that holds none of
// this kind's fields.
var alertmanagerGateFuncs = func() map[string]string {
	const (
		amcfg = "pkg/alertmanager/amcfg.go "
		op    = "pkg/alertmanager/operator.go "
		// The receivers, routes, inhibition rules and time intervals of the
		// configuration come from the configSecret Secret or from
		// AlertmanagerConfig objects, never from this kind.
		config = "the configuration of a receiver, a route, an inhibition rule or a time interval, which comes from the configSecret Secret or an AlertmanagerConfig object, not from this kind"
		object = "the check of an AlertmanagerConfig object at reconcile (checkAlertmanagerConfigResource), which this kind does not author"
	)
	funcs := map[string]string{
		amcfg + "(*alertmanagerConfig).sanitize":     "the mute and time intervals of the configuration, which come from the configSecret Secret or an AlertmanagerConfig object, not from this kind",
		amcfg + "getEnforcer":                        "chooses the syntax of the matchers the operator adds for alertmanagerConfigMatcherStrategy; every strategy is read at every version",
		amcfg + "(*ConfigBuilder).convertMatchersV2": "chooses the syntax of an AlertmanagerConfig object's matchers",
		amcfg + "(*ConfigBuilder).convertSnsConfig":  config,
	}
	for _, fn := range []string{
		"discordConfig", "emailConfig", "incidentioConfig", "jiraConfig", "mattermostConfig", "msTeamsConfig", "msTeamsV2Config",
		"opsgenieConfig", "opsgenieResponder", "pagerdutyConfig", "pushoverConfig", "rocketChatConfig", "slackConfig", "snsConfig",
		"telegramConfig", "victorOpsConfig", "webexConfig", "webhookConfig", "weChatConfig", "inhibitRule", "route", "timeInterval",
	} {
		funcs[amcfg+"(*"+fn+").sanitize"] = config
	}
	for _, fn := range []string{
		"checkRoute", "checkHTTPConfig", "checkOpsGenieResponder", "checkDiscordConfigs", "checkRocketChatConfigs", "checkSlackConfigs",
		"checkWebhookConfigs", "checkWebexConfigs", "checkEmailConfigs", "checkSnsConfigs", "checkTelegramConfigs", "checkMSTeamsConfigs",
		"checkMSTeamsV2Configs", "checkInhibitRules",
	} {
		funcs[op+fn] = object
	}
	return funcs
}()

// TestAlertmanagerVersionGates_MatchVendoredSource holds
// alertmanagerVersionGates, the floor and the default version to the
// operator's source: every comparison of the Alertmanager version is a row of
// the table at its minimum or is listed with why it is not this kind's, and
// the default the kind holds an unset version as is the operator's.
func TestAlertmanagerVersionGates_MatchVendoredSource(t *testing.T) {
	src := loadVendoredPromOp(t)
	if got := src.stringConst(t, "pkg/operator/defaults.go", "DefaultAlertmanagerVersion"); got != alertmanagerDefaultVersion {
		t.Errorf("the operator's DefaultAlertmanagerVersion is %s, alertmanagerDefaultVersion %s", got, alertmanagerDefaultVersion)
	}
	holdVersionGates(t, src.versionGates(t, alertmanagerGateFiles...), alertmanagerGateRows, alertmanagerGateFuncs, alertmanagerVersionGates, alertmanagerMinimumVersion)
}

// alertmanagerGateFixtures author each gated field, alone, as the operator
// tests it.
func alertmanagerGateFixtures() map[string]map[string]any {
	ref := func(name, key string) map[string]any { return map[string]any{"name": name, "key": key} }
	tls := map[string]any{"keySecret": ref("alertmanager-tls", "tls.key"), "cert": map[string]any{"secret": ref("alertmanager-tls", "tls.crt")}}
	global := func(g map[string]any) map[string]any {
		return map[string]any{"alertmanagerConfiguration": map[string]any{"name": "main", "global": g}}
	}
	http := func(h map[string]any) map[string]any { return global(map[string]any{"httpConfig": h}) }
	oauth2 := func(extra map[string]any) map[string]any {
		o := map[string]any{"clientId": map[string]any{"secret": ref("oauth", "id")}, "clientSecret": ref("oauth", "secret"), "tokenUrl": "https://auth.example.com/token"}
		for k, v := range extra {
			o[k] = v
		}
		return http(map[string]any{"oauth2": o})
	}
	smtp := func(extra map[string]any) map[string]any {
		s := map[string]any{"from": "alerts@example.com", "smartHost": map[string]any{"host": "smtp.example.com", "port": "587"}}
		for k, v := range extra {
			s[k] = v
		}
		return global(map[string]any{"smtp": s})
	}
	proxy := "http://proxy.example.com:3128"
	header := map[string]any{"X-Team": []any{ref("proxy", "team")}}
	return map[string]map[string]any{
		"logFormat":                 {"logFormat": "json"},
		"web.getConcurrency":        {"web": map[string]any{"getConcurrency": 8}},
		"web.timeout":               {"web": map[string]any{"timeout": 30}},
		"web.tlsConfig":             {"web": map[string]any{"tlsConfig": tls}},
		"web.httpConfig":            {"web": map[string]any{"httpConfig": map[string]any{"http2": true}}},
		"clusterTLS":                {"clusterTLS": map[string]any{"server": tls, "client": tls}},
		"clusterLabel":              {"clusterLabel": "main"},
		"enableFeatures":            {"enableFeatures": []any{"classic-mode"}},
		"limits.maxSilences":        {"limits": map[string]any{"maxSilences": 100}},
		"limits.maxPerSilenceBytes": {"limits": map[string]any{"maxPerSilenceBytes": "1MB"}},
		"minReadySeconds":           {"minReadySeconds": 10},
		"clusterPeerName":           {"clusterPeerName": "$(POD_NAME)"},

		"alertmanagerConfiguration.global.httpConfig.authorization":        http(map[string]any{"authorization": map[string]any{"credentials": ref("token", "token")}}),
		"alertmanagerConfiguration.global.httpConfig.oauth2":               oauth2(nil),
		"alertmanagerConfiguration.global.httpConfig.followRedirects":      http(map[string]any{"followRedirects": true}),
		"alertmanagerConfiguration.global.telegram":                        global(map[string]any{"telegram": map[string]any{"apiURL": "https://api.telegram.org"}}),
		"alertmanagerConfiguration.global.webex":                           global(map[string]any{"webex": map[string]any{"apiURL": "https://webexapis.com/v1/messages"}}),
		"alertmanagerConfiguration.global.httpConfig.enableHttp2":          http(map[string]any{"enableHttp2": true}),
		"alertmanagerConfiguration.global.httpConfig.tlsConfig.minVersion": http(map[string]any{"tlsConfig": map[string]any{"minVersion": "TLS12"}}),
		"alertmanagerConfiguration.global.httpConfig.tlsConfig.maxVersion": http(map[string]any{"tlsConfig": map[string]any{"maxVersion": "TLS13"}}),
		"alertmanagerConfiguration.global.httpConfig.oauth2.proxyUrl":      oauth2(map[string]any{"proxyUrl": proxy}),
		// The operator drops the three fields of an oauth2 proxy on any one of
		// them, so each is authored without proxyUrl, which is gated too.
		"alertmanagerConfiguration.global.httpConfig.oauth2.noProxy":            oauth2(map[string]any{"noProxy": "internal.example.com"}),
		"alertmanagerConfiguration.global.httpConfig.oauth2.proxyConnectHeader": oauth2(map[string]any{"proxyConnectHeader": header}),
		"alertmanagerConfiguration.global.httpConfig.proxyFromEnvironment":      http(map[string]any{"proxyFromEnvironment": true}),
		"alertmanagerConfiguration.global.httpConfig.noProxy":                   http(map[string]any{"proxyUrl": proxy, "noProxy": "internal.example.com"}),
		"alertmanagerConfiguration.global.httpConfig.proxyConnectHeader":        http(map[string]any{"proxyUrl": proxy, "proxyConnectHeader": header}),
		"alertmanagerConfiguration.global.smtp.tlsConfig":                       smtp(map[string]any{"tlsConfig": map[string]any{"serverName": "smtp.example.com"}}),
		"alertmanagerConfiguration.global.jira":                                 global(map[string]any{"jira": map[string]any{"apiURL": "https://jira.example.com"}}),
		"alertmanagerConfiguration.global.rocketChat":                           global(map[string]any{"rocketChat": map[string]any{"apiURL": "https://rocketchat.example.com"}}),
		"alertmanagerConfiguration.global.smtp.forceImplicitTLS":                smtp(map[string]any{"forceImplicitTLS": true}),
		"alertmanagerConfiguration.global.mattermost":                           global(map[string]any{"mattermost": map[string]any{"webhookURL": ref("mattermost", "url")}}),
	}
}

// TestAlertmanagerVersionGates_Refused: each gated field, authored with a
// version one minor version under its minimum, is refused naming the field,
// the minimum and the version; at the minimum it builds, and with version
// unset too, where the operator's default, above every minimum, is compared.
func TestAlertmanagerVersionGates_Refused(t *testing.T) {
	h := &AlertmanagerHandler{}
	build := func(props map[string]any, version string) error {
		p := map[string]any{}
		for k, v := range props {
			p[k] = v
		}
		if version != "" {
			p["version"] = version
		}
		_, err := h.ToApplicationConfig(&oam.Component{Name: "main", Type: "alertmanager", Properties: p}, "monitoring")
		return err
	}
	fixtures := alertmanagerGateFixtures()
	for _, g := range alertmanagerVersionGates {
		t.Run(g.path, func(t *testing.T) {
			props, ok := fixtures[g.path]
			if !ok {
				t.Fatalf("no fixture authors %s", g.path)
			}
			delete(fixtures, g.path)
			minimum := semver.MustParse(g.minimum)
			if minimum.Major != 0 || minimum.Patch != 0 || minimum.Minor <= 15 {
				t.Fatalf("minimum %s: the case takes the minor version under it, which must be a supported one", g.minimum)
			}
			below := fmt.Sprintf("v0.%d.0", minimum.Minor-1)
			want := fmt.Sprintf("%s: read by the Prometheus operator only for Alertmanager %s and later; at version %s it ", g.path, g.minimum, below)
			if err := build(props, below); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("at %s: err = %v, want one mentioning %q", below, err, want)
			}
			if err := build(props, "v"+g.minimum); err != nil {
				t.Errorf("at v%s: err = %v, want it built", g.minimum, err)
			}
			if err := build(props, ""); err != nil {
				t.Errorf("version unset: err = %v, want it built at the default %s", err, alertmanagerDefaultVersion)
			}
		})
	}
	for path := range fixtures {
		t.Errorf("a fixture authors %s, which the table does not list", path)
	}
}
