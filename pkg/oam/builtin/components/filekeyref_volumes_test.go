package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// fileKeyRefKinds is every component kind whose env accepts a fileKeyRef, with
// the properties it requires besides `image`, and whether it takes sidecars.
var fileKeyRefKinds = []struct {
	kind     string
	handler  oam.ComponentHandler
	required map[string]any
	sidecars bool
}{
	{"webservice", webserviceViaRule{}, nil, true},
	{"worker", workerViaRule{}, nil, true},
	{"deployment", &components.DeploymentHandler{}, nil, true},
	{"statefulset", &components.StatefulsetHandler{}, nil, true},
	{"daemonset", &components.DaemonsetHandler{}, nil, true},
	{"job", &components.JobHandler{}, nil, false},
	{"cronjob", &components.CronjobHandler{}, map[string]any{"schedule": "0 2 * * *"}, false},
}

func fileKeyRefEnv(volumeName string) []any {
	return []any{map[string]any{
		"name": "API_KEY",
		"valueFrom": map[string]any{"fileKeyRef": map[string]any{
			"volumeName": volumeName,
			"path":       "api.env",
			"key":        "API_KEY",
		}},
	}}
}

// fileKeyRefVolumes declares an emptyDir "envfiles" and a configMap "settings".
func fileKeyRefVolumes() []any {
	return []any{
		map[string]any{"name": "envfiles", "type": "emptyDir", "mountPath": "/etc/envfiles"},
		map[string]any{"name": "settings", "type": "configMap", "configMapName": "app-settings", "mountPath": "/etc/settings"},
	}
}

// TestFileKeyRef_VolumeCrossCheck pins go-kure/launcher#667: a fileKeyRef's
// volumeName must name an emptyDir volume the component declares, in the main
// container, an init container and a sidecar alike. The API server refuses
// both a missing volume and a non-emptyDir one (validateFileKeyRefVolumes,
// called from ValidatePodSpec), so the build refuses them first.
func TestFileKeyRef_VolumeCrossCheck(t *testing.T) {
	const (
		notDeclared = `fileKeyRef.volumeName "missing": no volume of that name is declared`
		notEmptyDir = `fileKeyRef.volumeName "settings": the volume is not emptyDir`
	)
	type place struct {
		name    string
		sidecar bool
		build   func(env []any) map[string]any
		wantPfx string
	}
	places := []place{
		{name: "main", build: func(env []any) map[string]any {
			return map[string]any{"env": env}
		}, wantPfx: `env "API_KEY": `},
		{name: "initContainer", build: func(env []any) map[string]any {
			return map[string]any{"initContainers": []any{map[string]any{
				"name": "init", "image": "ghcr.io/org/init:v1.0.0", "env": env,
			}}}
		}, wantPfx: `initContainers[0] "init": env "API_KEY": `},
		{name: "sidecar", sidecar: true, build: func(env []any) map[string]any {
			return map[string]any{"sidecars": []any{map[string]any{
				"name": "side", "image": "ghcr.io/org/side:v1.0.0", "env": env,
			}}}
		}, wantPfx: `sidecars[0] "side": env "API_KEY": `},
	}
	cases := []struct {
		name       string
		volumeName string
		wantErr    string
	}{
		{"emptyDir accepted", "envfiles", ""},
		{"undeclared volume refused", "missing", notDeclared},
		{"non-emptyDir volume refused", "settings", notEmptyDir},
	}
	for _, k := range fileKeyRefKinds {
		for _, p := range places {
			if p.sidecar && !k.sidecars {
				continue
			}
			for _, tc := range cases {
				t.Run(k.kind+"/"+p.name+"/"+tc.name, func(t *testing.T) {
					props := p.build(fileKeyRefEnv(tc.volumeName))
					props["image"] = "ghcr.io/org/app:v1.0.0"
					props["volumes"] = fileKeyRefVolumes()
					for key, v := range k.required {
						props[key] = v
					}
					_, err := k.handler.ToApplicationConfig(&oam.Component{Name: "app", Type: k.kind, Properties: props}, "default")
					if tc.wantErr == "" {
						if err != nil {
							t.Fatalf("ToApplicationConfig: %v", err)
						}
						return
					}
					if err == nil {
						t.Fatalf("expected an error containing %q, got none", p.wantPfx+tc.wantErr)
					}
					if !strings.Contains(err.Error(), p.wantPfx+tc.wantErr) {
						t.Fatalf("error %q does not contain %q", err.Error(), p.wantPfx+tc.wantErr)
					}
				})
			}
		}
	}
}

// TestFileKeyRef_StatefulsetClaimTemplateRefused pins that a volumeClaimTemplate
// does not satisfy a fileKeyRef: it becomes a persistentVolumeClaim volume, not
// an emptyDir one, so the API server refuses the reference.
func TestFileKeyRef_StatefulsetClaimTemplateRefused(t *testing.T) {
	_, err := (&components.StatefulsetHandler{}).ToApplicationConfig(&oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image": "ghcr.io/org/postgres:v15",
			"env":   fileKeyRefEnv("data"),
			"volumeClaimTemplates": []any{
				map[string]any{"name": "data", "size": "10Gi", "mountPath": "/var/lib/data"},
			},
		},
	}, "default")
	want := `env "API_KEY": fileKeyRef.volumeName "data": no volume of that name is declared`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want one containing %q", err, want)
	}
}
