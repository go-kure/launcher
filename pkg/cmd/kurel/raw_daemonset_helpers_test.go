package kurel

// rawDaemonsetProps is the smallest properties map the raw `daemonset` kind takes
// (go-kure/launcher#1629): the upstream DaemonSetSpec fields, with one container named
// "app" running image. Like rawDeploymentProps, the selector asks for a label the
// template authors itself, so it holds for any component name; launcher adds the `app`
// label to the template beside it. Tests that use daemonset as a vehicle for a trait, an
// endpoint, a label or the object's metadata take these.
func rawDaemonsetProps(image string) map[string]any {
	return map[string]any{
		"selector": map[string]any{"matchLabels": map[string]any{"tier": "web"}},
		"template": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{"tier": "web"}},
			"spec": map[string]any{"containers": []any{
				map[string]any{"name": "app", "image": image}}},
		},
	}
}
