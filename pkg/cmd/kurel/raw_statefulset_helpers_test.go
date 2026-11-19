package kurel

import "maps"

// rawStatefulsetProps is the smallest properties map the raw `statefulset` kind takes
// (go-kure/launcher#1629): the upstream StatefulSetSpec fields, with one container named
// "app" running image. Like rawDeploymentProps, the selector asks for a label the
// template authors itself, so it holds for any component name; launcher adds the `app`
// label to the template beside it. No serviceName and no claim template: the kind
// fills none. Tests that use statefulset as a vehicle for a trait, an endpoint, a label
// or the object's metadata take these and add to them with rawStatefulsetPropsWith.
func rawStatefulsetProps(image string) map[string]any {
	return map[string]any{
		"selector": map[string]any{"matchLabels": map[string]any{"tier": "web"}},
		"template": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{"tier": "web"}},
			"spec": map[string]any{"containers": []any{
				map[string]any{"name": "app", "image": image}}},
		},
	}
}

// rawStatefulsetPropsWith is rawStatefulsetProps for the usual test image with extra
// top-level StatefulSetSpec fields (replicas, serviceName, volumeClaimTemplates, ...)
// added.
func rawStatefulsetPropsWith(extra map[string]any) map[string]any {
	p := rawStatefulsetProps("ghcr.io/example/app:v1.0.0")
	maps.Copy(p, extra)
	return p
}

// rawStatefulsetClaimTemplate is one raw claim template named name that asks for
// storage and authors ReadWriteOnce, with no storage class: the kind fills none, and the
// ClusterProfile `pvc` capability's class lands on a template that authors none.
func rawStatefulsetClaimTemplate(name, storage string) map[string]any {
	return map[string]any{
		"metadata": map[string]any{"name": name},
		"spec": map[string]any{
			"accessModes": []any{"ReadWriteOnce"},
			"resources":   map[string]any{"requests": map[string]any{"storage": storage}},
		},
	}
}
