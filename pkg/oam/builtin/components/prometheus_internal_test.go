package components

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// TestThanosDroppedMountFields: the fields thanosDroppedMountFields names are
// every field of corev1.VolumeMount but name and mountPath, which the operator
// copies into the sidecar's mount (pkg/prometheus/server/statefulset.go:642-647
// at prometheus-operator v0.94.1). A field a later Kubernetes release adds to
// the type fails here until it is listed. Each reads unset on an empty mount
// and set on one that writes every field.
func TestThanosDroppedMountFields(t *testing.T) {
	var fields []string
	for f := range reflect.TypeFor[corev1.VolumeMount]().Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name != "name" && name != "mountPath" {
			fields = append(fields, name)
		}
	}
	var listed []string
	for _, f := range thanosDroppedMountFields(corev1.VolumeMount{}) {
		listed = append(listed, f.name)
		if f.set {
			t.Errorf("%s reads set on an empty mount", f.name)
		}
	}
	slices.Sort(fields)
	slices.Sort(listed)
	if !slices.Equal(fields, listed) {
		t.Fatalf("listed %v, want every field of VolumeMount but name and mountPath: %v", listed, fields)
	}
	propagation, recursive := corev1.MountPropagationNone, corev1.RecursiveReadOnlyDisabled
	full := corev1.VolumeMount{
		Name: "data", MountPath: "/data",
		ReadOnly: true, RecursiveReadOnly: &recursive, SubPath: "a", MountPropagation: &propagation,
		SubPathExpr: "$(B)", BindMountOptions: []string{"noexec"},
	}
	for _, f := range thanosDroppedMountFields(full) {
		if !f.set {
			t.Errorf("%s reads unset on a mount that writes it", f.name)
		}
	}
}
