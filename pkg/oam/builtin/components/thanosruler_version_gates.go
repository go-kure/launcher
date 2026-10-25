package components

import (
	"github.com/blang/semver/v4"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

// The version gates of the thanosruler kind (go-kure/launcher#976; the class
// is described in monitoring_version_gates.go): the fields of
// monitoringv1.ThanosRulerSpec the Prometheus operator reads only from some
// Thanos version on, at prometheus-operator v0.94.1.
//
// TestThanosRulerVersionGates_MatchVendoredSource holds the table to the
// operator's source: every comparison of a version in the operator's package
// pkg/thanos, vendored whole, is a row here, at its bound. An unset version
// is judged at the operator's default, prometheusThanosDefaultVersion, which
// the prometheus kind's sidecar shares (DefaultThanosVersion,
// pkg/operator/defaults.go).

// The kind's texts of what the operator does with a field below its minimum,
// completing "at <version> it ...".
const (
	thanosRulerFlagDropped        = "does not pass it to Thanos Ruler, and the ruler runs without it"
	thanosRulerRefused            = "refuses the object at reconcile, and the ThanosRuler is not updated"
	thanosRulerRemoteWriteDropped = "drops it from the remote write configuration, and the ruler writes without it"
	thanosRulerRemoteWriteKept    = "logs that it ignores it, yet writes it into the remote write configuration all the same, for a Thanos that does not read it"
	thanosRulerRemoteWriteNoAuth  = "drops it, and where that leaves azureAd with no authentication method refuses the object at reconcile"
	thanosRulerRemoteWriteAzureAD = "logs that it ignores it, yet writes it into the remote write configuration all the same where a managed identity remains, for a Thanos that does not read it, and refuses the object at reconcile where no authentication method remains"
	thanosRulerWebPlainHTTP       = "mounts no web configuration, and the web endpoint serves plain HTTP without the authored settings"
)

// thanosRulerGate is an idGate of the thanosruler kind; an empty id is the
// path.
func thanosRulerGate(id, path, minimum, below string, authored func(s *monitoringv1.ThanosRulerSpec) bool) idGate[monitoringv1.ThanosRulerSpec] {
	return idGate[monitoringv1.ThanosRulerSpec]{id, versionGate[monitoringv1.ThanosRulerSpec]{path, minimum, below, authored}}
}

// thanosRulerVersionGates are the fields the operator reads only from some
// Thanos version on, by id. Where the operator tests a field beside another
// condition, authored tests the same: an empty ruleQueryOffset, or a
// ruleConcurrentEval of 0, is passed at no version.
var thanosRulerVersionGates = func() []idGate[monitoringv1.ThanosRulerSpec] {
	type spec = monitoringv1.ThanosRulerSpec
	type rw = monitoringv1.RemoteWriteSpec
	g := func(path, minimum, below string, authored func(s *spec) bool) idGate[spec] {
		return thanosRulerGate("", path, minimum, below, authored)
	}
	remoteWrite := func(field, minimum, below string, authored func(r *rw) bool) idGate[spec] {
		return g("remoteWrite[]."+field, minimum, below, func(s *spec) bool { return anyOf(s.RemoteWrite, authored) })
	}
	duration := func(d *monitoringv1.Duration) bool { return d != nil && *d != "" }
	tls := func(s *spec) *monitoringv1.GRPCServerTLSConfig { return s.GRPCServerTLSConfig }
	return []idGate[spec]{
		// The flags of the thanos-ruler container (makeStatefulSetSpec,
		// pkg/thanos/statefulset.go).
		g("ruleOutageTolerance", "0.30.0", thanosRulerFlagDropped, func(s *spec) bool { return duration(s.RuleOutageTolerance) }),
		g("ruleGracePeriod", "0.30.0", thanosRulerFlagDropped, func(s *spec) bool { return duration(s.RuleGracePeriod) }),
		g("ruleConcurrentEval", "0.37.0", thanosRulerFlagDropped, func(s *spec) bool { return s.RuleConcurrentEval != nil && *s.RuleConcurrentEval > 0 }),
		g("ruleQueryOffset", "0.38.0", thanosRulerFlagDropped, func(s *spec) bool { return duration(s.RuleQueryOffset) }),
		g("enableFeatures", "0.39.0", thanosRulerFlagDropped, func(s *spec) bool { return len(s.EnableFeatures) > 0 }),
		g("grpcServerTlsConfig.minVersion", "0.37.0", thanosRulerFlagDropped, func(s *spec) bool { return tls(s) != nil && set(tls(s).MinVersion) }),
		g("grpcServerTlsConfig.cipherSuites", "0.42.0", thanosRulerFlagDropped, func(s *spec) bool { return tls(s) != nil && len(tls(s).CipherSuites) > 0 }),
		g("grpcServerTlsConfig.curves", "0.42.0", thanosRulerFlagDropped, func(s *spec) bool { return tls(s) != nil && len(tls(s).Curves) > 0 }),
		g("web.tlsConfig", "0.21.0", thanosRulerWebPlainHTTP, func(s *spec) bool { return s.Web != nil && s.Web.TLSConfig != nil }),
		g("web.httpConfig", "0.21.0", thanosRulerWebPlainHTTP, func(s *spec) bool { return s.Web != nil && s.Web.HTTPConfig != nil }),

		// The remote write endpoints, refused at reconcile below
		// minRemoteWriteVersion, and the fields of an endpoint the operator
		// resets below the Thanos version that reads them, or, for a managed
		// identity without a client id, refuses (pkg/thanos/operator.go).
		// The reset is of a copy of the entry (the loop ranges over
		// tr.Spec.RemoteWrite by value): it drops only a field reached
		// through a pointer, of tlsConfig, queueConfig or azureAd; a field of
		// the entry itself is logged as ignored and written all the same.
		//
		// The first gate missed is the one refused, so the gates of azureAd's
		// methods precede azureAd's own, in the order the operator acts on
		// them: a managed identity without a client id refused, then sdk and
		// oauth dropped, which leaves the object refused where no method
		// remains. azureAd itself is written, though ignored, where a managed
		// identity survives; with only a workloadIdentity, which the operator
		// drops at every version (go-kure/launcher#1020), the object is
		// refused.
		g("remoteWrite", "0.24.0", thanosRulerRefused, func(s *spec) bool { return len(s.RemoteWrite) > 0 }),
		thanosRulerGate(`remoteWrite[].azureAd.managedIdentity.clientId=""`, "remoteWrite[].azureAd.managedIdentity", "0.40.0",
			"refuses one without a clientId at reconcile, and the ThanosRuler is not updated", func(s *spec) bool {
				return anyOf(s.RemoteWrite, func(r *rw) bool {
					return r.AzureAD != nil && r.AzureAD.ManagedIdentity != nil &&
						(r.AzureAD.ManagedIdentity.ClientID == nil || *r.AzureAD.ManagedIdentity.ClientID == "")
				})
			}),
		remoteWrite("azureAd.sdk", "0.36.0", thanosRulerRemoteWriteNoAuth, func(r *rw) bool { return r.AzureAD != nil && r.AzureAD.SDK != nil }),
		remoteWrite("azureAd.oauth", "0.31.0", thanosRulerRemoteWriteNoAuth, func(r *rw) bool { return r.AzureAD != nil && r.AzureAD.OAuth != nil }),
		remoteWrite("azureAd", "0.31.0", thanosRulerRemoteWriteAzureAD, func(r *rw) bool { return r.AzureAD != nil }),
		remoteWrite("tlsConfig.minVersion", "0.28.0", thanosRulerRemoteWriteDropped, func(r *rw) bool { return r.TLSConfig != nil && r.TLSConfig.MinVersion != nil }),
		remoteWrite("tlsConfig.maxVersion", "0.30.0", thanosRulerRemoteWriteDropped, func(r *rw) bool { return r.TLSConfig != nil && r.TLSConfig.MaxVersion != nil }),
		remoteWrite("sendNativeHistograms", "0.30.0", thanosRulerRemoteWriteKept, func(r *rw) bool { return r.SendNativeHistograms != nil }),
		remoteWrite("queueConfig.sampleAgeLimit", "0.32.0", thanosRulerRemoteWriteDropped, func(r *rw) bool { return r.QueueConfig != nil && r.QueueConfig.SampleAgeLimit != nil }),
		remoteWrite("noProxy", "0.32.0", thanosRulerRemoteWriteKept, func(r *rw) bool { return r.NoProxy != nil }),
		remoteWrite("proxyFromEnvironment", "0.32.0", thanosRulerRemoteWriteKept, func(r *rw) bool { return r.ProxyFromEnvironment != nil }),
		remoteWrite("proxyConnectHeader", "0.32.0", thanosRulerRemoteWriteKept, func(r *rw) bool { return r.ProxyConnectHeader != nil }),
		remoteWrite("messageVersion", "0.37.0", thanosRulerRemoteWriteKept, func(r *rw) bool { return r.MessageVersion != nil }),
		remoteWrite("roundRobinDNS", "0.38.0", thanosRulerRemoteWriteKept, func(r *rw) bool { return r.RoundRobinDNS != nil }),
	}
}()

// validateThanosRulerVersion refuses a field of thanosRulerVersionGates that
// spec authors below its minimum, at version, or the operator's default
// where it is unset. A version the operator cannot parse is left to it: it
// fails the reconcile on one (makeStatefulSetSpec), and no gate applies.
func validateThanosRulerVersion(spec *monitoringv1.ThanosRulerSpec) error {
	version, at := prometheusThanosDefaultVersion, "the operator's default version "+prometheusThanosDefaultVersion
	if set(spec.Version) {
		version, at = *spec.Version, "version "+*spec.Version
	}
	if v, err := semver.ParseTolerant(version); err == nil {
		return refuseVersionGates(spec, v, at, "Thanos", gatesOf(thanosRulerVersionGates))
	}
	return nil
}
