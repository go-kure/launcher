package traits_test

// The `statefulset` kind takes the upstream StatefulSetSpec, strictly decoded
// (go-kure/launcher#1629), as `deployment` and `daemonset` do. A trait test that
// needs a StatefulSet only as the workload to act on builds one here, with the
// helpers of raw_deployment_helpers_test.go doing the merging.

// rawStatefulsetProps is the smallest `statefulset` the raw kind takes: a
// selector on the `app` label and one container named app. overrides is merged
// over it as rawDeploymentProps does ({"serviceName": "db-headless"}, or a
// `volumeClaimTemplates` list), and rawPod adds to its pod spec.
func rawStatefulsetProps(name, image string, overrides ...map[string]any) map[string]any {
	return rawDeploymentProps(name, image, overrides...)
}

// rawClaimTemplate is one entry of `volumeClaimTemplates`: a claim named name
// that asks for storage, with the ReadWriteOnce mode the raw kind does not fill.
func rawClaimTemplate(name, storage string) map[string]any {
	return map[string]any{
		"metadata": map[string]any{"name": name},
		"spec": map[string]any{
			"accessModes": []any{"ReadWriteOnce"},
			"resources":   map[string]any{"requests": map[string]any{"storage": storage}},
		},
	}
}
