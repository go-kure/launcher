package components

// rawDeploymentInternalProps is the smallest raw `deployment` component
// (go-kure/launcher#1629) for the package's internal tests: a selector on a
// `tier` label the template carries, and one container running image. The
// external tests' twin is rawDeploymentProps in package components_test.
func rawDeploymentInternalProps(image string) map[string]any {
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
