package registry_test

import "github.com/go-kure/launcher/pkg/oam"

// The `deployment` kind takes the upstream DeploymentSpec, strictly decoded
// (go-kure/launcher#1629). A test that only needs a Deployment as the vehicle for
// a trait, a report or a bound builds it here, in one place. A test of the
// reshaped form (a main container at the top level, `sidecars`, the fills of the
// kind) uses `worker`, `webservice` or the member of both, `role-deployment`.

// rawImage is the image of the one container rawDeploymentProps writes.
const rawImage = "ghcr.io/org/app:v1"

// rawDeploymentProps is the smallest `deployment` the raw kind takes: a selector
// on the `app` label and one container named app. overrides is merged over it
// key by key, maps recursively, anything else replacing: {"replicas": 3} sets
// the replicas, {"template": {"spec": {"serviceAccountName": "x"}}} adds to the
// pod spec and keeps its containers.
func rawDeploymentProps(name, image string, overrides map[string]any) map[string]any {
	base := map[string]any{
		"selector": map[string]any{"matchLabels": map[string]any{"app": name}},
		"template": map[string]any{
			"spec": map[string]any{"containers": []any{
				map[string]any{"name": "app", "image": image},
			}},
		},
	}
	return mergeRaw(base, overrides)
}

// rawDeployment is a deployment component named name, with the default image.
func rawDeployment(name string, overrides map[string]any) oam.Component {
	return oam.Component{Name: name, Type: "deployment", Properties: rawDeploymentProps(name, rawImage, overrides)}
}

// rawWorkload is a deployment component named app, with its traits, for a test
// that writes fields of one container and of the pod: container is added to the
// first container, pod to the pod spec (a key replaces the one of the same name),
// and more are the containers after the first.
func rawWorkload(container, pod map[string]any, more []any, traits ...oam.Trait) oam.Component {
	first := map[string]any{"name": "app", "image": "registry.example/app:1"}
	for k, v := range container {
		first[k] = v
	}
	spec := map[string]any{"containers": append([]any{first}, more...)}
	for k, v := range pod {
		spec[k] = v
	}
	return oam.Component{Name: "app", Type: "deployment", Traits: traits, Properties: map[string]any{
		"selector": map[string]any{"matchLabels": map[string]any{"app": "app"}},
		"template": map[string]any{"spec": spec},
	}}
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
