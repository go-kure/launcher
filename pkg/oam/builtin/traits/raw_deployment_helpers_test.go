package traits_test

// The `deployment` kind takes the upstream DeploymentSpec, strictly decoded
// (go-kure/launcher#1629). A trait test that only needs a Deployment to act on
// builds one here, in one place, and a test of the reshaped form (the main
// container at the top level, `sidecars`, the fills of the kind) uses `worker`,
// `webservice` or the member of both, `role-deployment`.

// rawDeploymentProps is the smallest `deployment` the raw kind takes: a selector
// on the `app` label, which launcher puts on the pod template under the
// component's name, and one container named app. overrides is merged over it key
// by key, maps recursively, anything else replacing: {"replicas": 3} sets the
// replicas and {"template": {"spec": {"serviceAccountName": "x"}}} adds to the
// pod spec and keeps its containers.
func rawDeploymentProps(name, image string, overrides ...map[string]any) map[string]any {
	props := map[string]any{
		"selector": map[string]any{"matchLabels": map[string]any{"app": name}},
		"template": map[string]any{
			"spec": map[string]any{"containers": []any{
				map[string]any{"name": "app", "image": image},
			}},
		},
	}
	for _, o := range overrides {
		props = mergeRaw(props, o)
	}
	return props
}

// rawJobProps is the smallest `job` the raw kind takes: a pod template with one
// container named app and the restartPolicy a Job's pod needs. overrides merge as
// in rawDeploymentProps, and rawPod adds fields to the pod spec.
func rawJobProps(image string, overrides ...map[string]any) map[string]any {
	props := map[string]any{"template": map[string]any{"spec": map[string]any{
		"restartPolicy": "Never",
		"containers":    []any{map[string]any{"name": "app", "image": image}},
	}}}
	for _, o := range overrides {
		props = mergeRaw(props, o)
	}
	return props
}

// rawCronjobProps is rawJobProps under a cronjob's jobTemplate, with a schedule;
// podFields are added to the pod spec of the job template.
func rawCronjobProps(image string, podFields map[string]any) map[string]any {
	return map[string]any{
		"schedule":    "0 2 * * *",
		"jobTemplate": map[string]any{"spec": rawJobProps(image, rawPod(podFields))},
	}
}

// rawPod is the override that adds fields to the pod spec of a raw deployment.
func rawPod(fields map[string]any) map[string]any {
	return map[string]any{"template": map[string]any{"spec": fields}}
}

func mergeRaw(base, overrides map[string]any) map[string]any {
	for k, v := range overrides {
		if o, ok := v.(map[string]any); ok {
			if b, ok := base[k].(map[string]any); ok {
				base[k] = mergeRaw(b, o)
				continue
			}
		}
		base[k] = v
	}
	return base
}
