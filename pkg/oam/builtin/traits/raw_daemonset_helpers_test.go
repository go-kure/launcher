package traits_test

// The `daemonset` kind takes the upstream DaemonSetSpec, strictly decoded
// (go-kure/launcher#1629), as `deployment` does. A trait test that needs a
// DaemonSet only as the workload to act on builds one here, with the helpers of
// raw_deployment_helpers_test.go doing the merging.

// rawDaemonsetProps is the smallest `daemonset` the raw kind takes: a selector
// on the `app` label and one container named app. overrides is merged over it as
// rawDeploymentProps does, and rawPod adds to its pod spec.
func rawDaemonsetProps(name, image string, overrides ...map[string]any) map[string]any {
	return rawDeploymentProps(name, image, overrides...)
}
