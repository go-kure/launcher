package components_test

// rawDeploymentProps is the smallest raw `deployment` component
// (go-kure/launcher#1629): the upstream DeploymentSpec fields `selector` and
// `template`, one container running image. The selector names a `tier` label
// the template carries, so the document holds whatever the component is named:
// the handler adds the `app` label to the template, and a selector on it would
// depend on the name. Tests that use `deployment` as a vehicle for something
// else (traits, endpoints, scaler, object metadata) build on it.
func rawDeploymentProps(image string) map[string]any {
	return map[string]any{
		"selector": map[string]any{"matchLabels": map[string]any{"tier": "web"}},
		"template": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{"tier": "web"}},
			"spec": map[string]any{
				"containers": []any{map[string]any{"name": "app", "image": image}},
			},
		},
	}
}
