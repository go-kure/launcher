package components_test

import "testing"

// TestStatefulset_ClaimTemplateCollisions: a claim template shares the pod's
// volume namespace and the main container's mount paths with `volumes`, but
// each parser checks only its own entries. The apiserver does not catch the
// overlap either — StatefulSet validation skips the pod template's volumes —
// so it surfaces only in the controller: getPersistentVolumeClaims keys the
// templates by name, so a repeated name keeps one claim and drops the other,
// and updateStorage replaces a pod volume named like a template with that
// template's claim. A repeated mountPath fails pod creation instead.
func TestStatefulset_ClaimTemplateCollisions(t *testing.T) {
	k := statefulsetKind()
	data := map[string]any{"name": "data", "size": "1Gi", "mountPath": "/data"}
	cases := []struct {
		name    string
		props   map[string]any
		wantErr string
	}{
		{"two claim templates with one name", map[string]any{
			"volumeClaimTemplates": []any{data, map[string]any{"name": "data", "size": "2Gi", "mountPath": "/other"}},
		}, `volumeClaimTemplate "data": duplicate name`},
		{"claim template and an emptyDir volume", map[string]any{
			"volumeClaimTemplates": []any{data},
			"volumes":              []any{map[string]any{"name": "data", "type": "emptyDir", "mountPath": "/scratch"}},
		}, `volume "data" has the same name as a claim template`},
		{"claim template and a pvc volume", map[string]any{
			"volumeClaimTemplates": []any{data},
			"volumes":              []any{map[string]any{"name": "data", "type": "pvc", "claimName": "shared-data", "mountPath": "/shared", "accessModes": []any{"ReadWriteMany"}}},
		}, `volume "data" has the same name as a claim template`},
		{"two claim templates with one mountPath", map[string]any{
			"volumeClaimTemplates": []any{data, map[string]any{"name": "logs", "size": "1Gi", "mountPath": "/data"}},
		}, `duplicate mountPath "/data"`},
		{"claim template and a volume with one mountPath", map[string]any{
			"volumeClaimTemplates": []any{data},
			"volumes":              []any{map[string]any{"name": "scratch", "type": "emptyDir", "mountPath": "/data"}},
		}, `duplicate mountPath "/data"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := k.configure(t, tc.props)
			wantErrContaining(t, err, tc.wantErr)
		})
	}

	t.Run("distinct names and paths still build", func(t *testing.T) {
		g := k.generate(t, map[string]any{
			"volumeClaimTemplates": []any{data, map[string]any{"name": "logs", "size": "1Gi", "mountPath": "/logs"}},
			"volumes":              []any{map[string]any{"name": "scratch", "type": "emptyDir", "mountPath": "/scratch"}},
		})
		if len(g.vcts) != 2 || len(g.pod.Volumes) != 1 || len(g.pod.Containers[0].VolumeMounts) != 3 {
			t.Errorf("got %d claim templates, %d volumes, %d mounts; want 2, 1, 3",
				len(g.vcts), len(g.pod.Volumes), len(g.pod.Containers[0].VolumeMounts))
		}
	})
}
