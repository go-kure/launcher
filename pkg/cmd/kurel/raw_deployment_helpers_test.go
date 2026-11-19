package kurel

import "maps"

// rawDeploymentProps is the smallest properties map the raw `deployment` kind takes
// (go-kure/launcher#1629): the upstream DeploymentSpec fields, with one container named
// "app" running image. The selector asks for a label the template authors itself, so it
// holds for any component name, as the replicaset fixtures' does; launcher adds the
// `app` label to the template beside it. Tests that use deployment as a vehicle for a
// trait, an endpoint, a label or the object's metadata take these and add to them with
// rawDeploymentPropsWith.
func rawDeploymentProps(image string) map[string]any {
	return map[string]any{
		"selector": map[string]any{"matchLabels": map[string]any{"tier": "web"}},
		"template": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{"tier": "web"}},
			"spec": map[string]any{"containers": []any{
				map[string]any{"name": "app", "image": image}}},
		},
	}
}

// rawDeploymentPropsWith is rawDeploymentProps for the usual test image with extra
// top-level DeploymentSpec fields (replicas, strategy, ...) added.
func rawDeploymentPropsWith(extra map[string]any) map[string]any {
	p := rawDeploymentProps("ghcr.io/example/app:v1.0.0")
	maps.Copy(p, extra)
	return p
}

// rawJobProps is the smallest properties map the raw `job` kind takes: the upstream
// JobSpec fields, a pod template with one container named "app" running image, and the
// restartPolicy the API server requires of a Job's pod. rawCronjobProps is the same
// under a cronjob's jobTemplate, with the schedule.
func rawJobProps(image string) map[string]any {
	return map[string]any{"template": map[string]any{"spec": map[string]any{
		"restartPolicy": "Never",
		"containers":    []any{map[string]any{"name": "app", "image": image}},
	}}}
}

func rawCronjobProps(image string) map[string]any {
	return map[string]any{
		"schedule":    "0 2 * * *",
		"jobTemplate": map[string]any{"spec": rawJobProps(image)},
	}
}

// rawDeploymentYAML is rawDeploymentProps as a properties block for an app.yaml, with
// its keys at indent spaces, ending in a newline. image names the one container.
func rawDeploymentYAML(indent int, image string) string {
	block := "selector:\n  matchLabels:\n    tier: web\n" +
		"template:\n  metadata:\n    labels:\n      tier: web\n  spec:\n    containers:\n" +
		"      - name: app\n        image: " + image + "\n"
	return indentLines(block, indent)
}
