package components

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// containerPortEndpoints is the endpoints of a workload component (deployment,
// statefulset, daemonset): one per port its main container declares, in the
// authored order. The selector is the one the workload's pods carry,
// `app: oam.ComponentLabelValue(name)` (appLabels), never the object name. Name
// is the container port's name, empty for an unnamed port; Ports holds the
// numeric containerPort, so a name never has to be resolved in a NetworkPolicy;
// Protocol is the port's, an empty one meaning TCP. A component with no ports has
// no endpoint. Sidecar ports are not the component's service surface and are not
// listed.
func containerPortEndpoints(componentName string, ports []corev1.ContainerPort) []netpol.Endpoint {
	if len(ports) == 0 {
		return nil
	}
	eps := make([]netpol.Endpoint, 0, len(ports))
	for _, p := range ports {
		eps = append(eps, netpol.Endpoint{
			Name:        p.Name,
			PodSelector: selectorFrom(appLabels(componentName)),
			Ports:       []intstr.IntOrString{intstr.FromInt32(p.ContainerPort)},
			Protocol:    p.Protocol,
		})
	}
	return eps
}

// The kind-named workload components other than deployment — daemonset,
// statefulset, job and cronjob — publish the main container's `ports` list as
// deployment does (go-kure/launcher#334): container ports are a PodSpec field,
// and a kind component projects its API kind's fields. Declaring them emits no
// Service; an authored `service` component exposes the pods.

// parseMainContainerHandlers parses the main container's `probes` and
// `lifecycle` on a kind whose main container declares ports, the authored
// `ports` as parseContainerPorts returned them. Without them a named
// probe/lifecycle port is refused outright, since the kubelet has no
// ContainerPort to resolve it against. With them, any valid name is admitted
// and then checked against the declared names, as deployment checks its own.
func parseMainContainerHandlers(props map[string]any, ports []corev1.ContainerPort) (ProbeConfig, *corev1.Lifecycle, error) {
	namedPorts := len(ports) > 0
	probes, err := parseProbes(props, namedPorts, "")
	if err != nil {
		return ProbeConfig{}, nil, errors.Wrap(err, "invalid probe configuration")
	}
	lifecycle, err := parseLifecycle(props, namedPorts, "")
	if err != nil {
		return ProbeConfig{}, nil, errors.Wrap(err, "invalid lifecycle configuration")
	}
	if namedPorts {
		if err := checkNamedPortsDeclared(probes, lifecycle, ports, "the main container"); err != nil {
			return ProbeConfig{}, nil, err
		}
	}
	return probes, lifecycle, nil
}
