package components

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/errors"
)

// The sentences a refusal of lifecycle or a probe on an init container that is
// not restartable ends in. The app.yaml parser (restartableOnlyInitKeys) and
// the typed pod specs (refuseRegularInitFields) share them, so one rule has one
// text; the app.yaml refusal adds the tail that names its own `sidecars` key
// (appYAMLSidecarTail).
const (
	regularInitProbesReason    = "Kubernetes forbids probes on an init container that is not restartable, which runs to completion before the app containers start; set restartPolicy: Always to make it a native sidecar"
	regularInitLifecycleReason = "Kubernetes forbids lifecycle hooks on an init container that is not restartable, which runs to completion before the app containers start; set restartPolicy: Always to make it a native sidecar"
	appYAMLSidecarTail         = ", or author them on a sidecar instead"
)

// refuseRegularInitFields refuses a lifecycle or a liveness, readiness or
// startup probe on an init container whose restartPolicy is not Always: upstream's
// validateInitContainers forbids all four there, since the container runs to
// completion before the app containers start, and validates them on a
// restartable one (a native sidecar). It is the typed counterpart of the
// app.yaml parser's restartableOnlyInitKeys refusal, for the pod specs the
// typed readers hold (go-kure/launcher#1237). Ports and resizePolicy are not
// read: upstream does not forbid them. prefix is the pod spec's path in the
// object, ending in a dot, or "" when the object is the pod spec's own, as in
// validateTypedVolumeMounts.
func refuseRegularInitFields(prefix string, ps *corev1.PodSpec) error {
	for i := range ps.InitContainers {
		ctr := &ps.InitContainers[i]
		if ctr.RestartPolicy != nil && *ctr.RestartPolicy == corev1.ContainerRestartPolicyAlways {
			continue
		}
		for _, f := range []struct {
			key    string
			set    bool
			reason string
		}{
			{"lifecycle", ctr.Lifecycle != nil, regularInitLifecycleReason},
			{"livenessProbe", ctr.LivenessProbe != nil, regularInitProbesReason},
			{"readinessProbe", ctr.ReadinessProbe != nil, regularInitProbesReason},
			{"startupProbe", ctr.StartupProbe != nil, regularInitProbesReason},
		} {
			if f.set {
				return errors.Errorf("%s: %s: not supported on an init container that is not restartable — %s",
					fmt.Sprintf("%sinitContainers[%d] %q", prefix, i, ctr.Name), f.key, f.reason)
			}
		}
	}
	return nil
}
