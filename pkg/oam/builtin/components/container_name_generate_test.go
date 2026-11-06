package components_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// These tests cover go-kure/launcher#1278 for container names: the parser refuses an
// initContainers or sidecars name that is not a DNS-1123 label, and Generate refuses
// it too, so a config built by hand or changed after it was parsed cannot emit a pod
// the API server then rejects. Same text as the parser's, with the entry label.

// setContainerName renames entry i of the init container list (list "initContainers")
// or of the sidecar list (list "sidecars") on the config a kind's handler built.
func setContainerName(t *testing.T, cfg stack.ApplicationConfig, list string, i int, name string) {
	t.Helper()
	switch c := cfg.(type) {
	case *components.DeploymentConfig:
		if list == "sidecars" {
			c.Sidecars[i].Name = name
		} else {
			c.InitContainers[i].Name = name
		}
	case *components.StatefulsetConfig:
		if list == "sidecars" {
			c.Sidecars[i].Name = name
		} else {
			c.InitContainers[i].Name = name
		}
	case *components.DaemonsetConfig:
		if list == "sidecars" {
			c.Sidecars[i].Name = name
		} else {
			c.InitContainers[i].Name = name
		}
	case *components.JobConfig:
		c.InitContainers[i].Name = name
	case *components.CronjobConfig:
		c.InitContainers[i].Name = name
	default:
		t.Fatalf("no container list known for config %T", cfg)
	}
}

func TestContainerNameLabel_RefusedAtGenerate(t *testing.T) {
	for _, k := range []string{"deployment", "statefulset", "daemonset", "job", "cronjob"} {
		var kind struct {
			name    string
			handler oam.ComponentHandler
			props   map[string]any
		}
		for _, w := range workloadKinds {
			if w.name == k {
				kind.name, kind.handler, kind.props = w.name, w.handler, w.props
			}
		}
		for _, list := range []string{"initContainers", "sidecars"} {
			if list == "sidecars" && !sidecarKinds[k] {
				continue
			}
			for _, c := range cnlBad {
				t.Run(k+"/"+list+"/"+c.name, func(t *testing.T) {
					extra := map[string]any{list: []any{cnEntry("fine"), cnEntry("other")}}
					cfg, err := kind.handler.ToApplicationConfig(&oam.Component{Name: "app", Type: k, Properties: withProps(kind.props, extra)}, "default")
					if err != nil {
						t.Fatalf("ToApplicationConfig: %v", err)
					}
					// The parsed config is valid. Change one name the way a caller of the
					// exported config can, then generate.
					setContainerName(t, cfg, list, 1, c.value)
					_, err = cfg.Generate(stack.NewApplication("app", "default", cfg))
					want := fmt.Sprintf("%s[1]: invalid container name %q", list, c.value)
					if err == nil || !strings.Contains(err.Error(), want) {
						t.Fatalf("Generate error = %v, want one containing %q", err, want)
					}
				})
			}
		}
	}
}
