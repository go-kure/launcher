package components_test

// rawDaemonsetProps is the smallest raw `daemonset` component
// (go-kure/launcher#1629): the upstream DaemonSetSpec fields `selector` and
// `template`, one container running image. The selector names a `tier` label
// the template carries, so the document holds whatever the component is named:
// the handler adds the `app` label to the template, and a selector on it would
// depend on the name. Tests that use `daemonset` as a vehicle for something
// else (traits, endpoints, object metadata, every-kind loops) build on it.
func rawDaemonsetProps(image string) map[string]any {
	return rawDaemonsetPropsWithPorts(image, nil)
}

// rawDaemonsetPropsWithPorts is rawDaemonsetProps with ports on the container:
// the upstream ContainerPort list, as the raw kind reads it. A nil list leaves
// the container portless.
func rawDaemonsetPropsWithPorts(image string, ports []any) map[string]any {
	container := map[string]any{"name": "app", "image": image}
	if ports != nil {
		container["ports"] = ports
	}
	return map[string]any{
		"selector": map[string]any{"matchLabels": map[string]any{"tier": "web"}},
		"template": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{"tier": "web"}},
			"spec": map[string]any{
				"containers": []any{container},
			},
		},
	}
}
