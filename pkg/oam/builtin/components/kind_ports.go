package components

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// The kind-named workload components other than deployment — daemonset,
// statefulset, job and cronjob — publish the main container's `ports` list as
// deployment does (go-kure/launcher#334): container ports are a PodSpec field,
// and a kind component projects its API kind's fields. Declaring them emits no
// Service. daemonset and statefulset keep their single `port` and the Service
// it drives; `ports` only adds container ports beside it.

// schemaMainContainerPortsBeside describes `ports` on a kind whose `port`
// property already declares one main-container port, named portName.
func schemaMainContainerPortsBeside(portName string) oam.PropertySchema {
	return schemaContainerPorts(fmt.Sprintf("Further ports the main container declares, beside the one `port` declares as %q; these emit no Service. A named port is what the main container's probes and lifecycle hooks may address by name. Names and containerPort/protocol pairs must be unique, also against `port`; a name must also be unique across the pod's containers.", portName))
}

// parseMainContainerPorts reads a kind's authored `ports`. fixed is the port
// the kind's own `port` property declares (nil when the kind has none or it is
// unset); the main container declares fixed first, then the authored entries.
// Each entry is checked by parseContainerPorts; one that repeats fixed's name
// or containerPort/protocol pair is refused, as parseContainerPorts refuses a
// repeat within the list, since the container would then declare it twice.
func parseMainContainerPorts(props map[string]any, fixed []corev1.ContainerPort) ([]corev1.ContainerPort, error) {
	ports, err := parseContainerPorts(props)
	if err != nil {
		return nil, err
	}
	for i, p := range ports {
		for _, f := range fixed {
			if p.Name != "" && p.Name == f.Name {
				return nil, errors.Errorf("%s.name: port name %q is already declared by `port`", indexedLabel("ports", i), p.Name)
			}
			if p.ContainerPort == f.ContainerPort && p.Protocol == f.Protocol {
				return nil, errors.Errorf("%s: port %d/%s is already declared by `port`", indexedLabel("ports", i), p.ContainerPort, p.Protocol)
			}
		}
	}
	return ports, nil
}

// joinContainerPorts is the main container's whole port list: fixed, then
// ports. It returns nil when both are empty, so a portless container stays
// portless.
func joinContainerPorts(fixed, ports []corev1.ContainerPort) []corev1.ContainerPort {
	if len(fixed)+len(ports) == 0 {
		return nil
	}
	all := make([]corev1.ContainerPort, 0, len(fixed)+len(ports))
	all = append(all, fixed...)
	return append(all, ports...)
}

// parseMainContainerHandlers parses the main container's `probes` and
// `lifecycle` on a kind that may carry authored `ports` (authored, as
// parseMainContainerPorts returned them). Without them, namedPortsAllowed and
// matchName reach parseProbes and parseLifecycle exactly as the kind passed
// them before `ports` existed, so a document without `ports` is accepted and
// refused as it was. With them, any valid name is admitted and then checked
// against the main container's whole port list (all), as deployment checks its
// own.
func parseMainContainerHandlers(props map[string]any, authored, all []corev1.ContainerPort, namedPortsAllowed bool, matchName string) (ProbeConfig, *corev1.Lifecycle, error) {
	withPorts := len(authored) > 0
	if withPorts {
		namedPortsAllowed, matchName = true, ""
	}
	probes, err := parseProbes(props, namedPortsAllowed, matchName)
	if err != nil {
		return ProbeConfig{}, nil, errors.Wrap(err, "invalid probe configuration")
	}
	lifecycle, err := parseLifecycle(props, namedPortsAllowed, matchName)
	if err != nil {
		return ProbeConfig{}, nil, errors.Wrap(err, "invalid lifecycle configuration")
	}
	if withPorts {
		if err := checkNamedPortsDeclared(probes, lifecycle, all, "the main container"); err != nil {
			return ProbeConfig{}, nil, err
		}
	}
	return probes, lifecycle, nil
}
