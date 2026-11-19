package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The conditionally required properties the built-in components declare
// (oam.RequiredWhen, go-kure/launcher#1625): a volume's csi, downwardAPI,
// ephemeral, image and projected objects when its type names them, and the
// volumeMode of a volume or a claim template when it authors a devicePath. The
// handler keeps its own refusal of each case as a second line; through Transform
// the schema's wording comes first.

func requiredWhenSentence(key, whenKey, equals string) string {
	if equals == "" {
		return `"` + key + `" is required when "` + whenKey + `" is set`
	}
	return `"` + key + `" is required when "` + whenKey + `" is "` + equals + `"`
}

func TestRequiredWhenAdopters_VolumeSourceObjects(t *testing.T) {
	site := exclusiveSite{compType: "deployment", handler: &components.DeploymentHandler{}}
	volume := func(extra map[string]any) map[string]any {
		v := map[string]any{"name": "v", "mountPath": "/v"}
		for k, val := range extra {
			v[k] = val
		}
		return map[string]any{"image": "ghcr.io/org/app:v1", "volumes": []any{v}}
	}
	for _, typ := range []string{"csi", "downwardAPI", "ephemeral", "image", "projected"} {
		t.Run(typ+" absent", func(t *testing.T) {
			wantErrContaining(t, site.transform(volume(map[string]any{"type": typ})), requiredWhenSentence(typ, "type", typ))
		})
		t.Run(typ+" null", func(t *testing.T) {
			wantErrContaining(t, site.transform(volume(map[string]any{"type": typ, typ: nil})), requiredWhenSentence(typ, "type", typ))
		})
		t.Run(typ+" set: the rule does not refuse", func(t *testing.T) {
			// The object's own content is the handler's; only the rule is asserted.
			err := site.transform(volume(map[string]any{"type": typ, typ: map[string]any{}}))
			if err != nil && strings.Contains(err.Error(), "is required when") {
				t.Errorf("a %s object is set and the rule refused: %v", typ, err)
			}
		})
	}
	for _, typ := range []string{"emptyDir", "configMap", "secret", "hostPath", "pvc"} {
		t.Run("control: "+typ+" takes no object", func(t *testing.T) {
			err := site.transform(volume(map[string]any{"type": typ}))
			if err != nil && strings.Contains(err.Error(), "is required when") {
				t.Errorf("type %s has no required object and the rule refused: %v", typ, err)
			}
		})
	}
	t.Run("another type's object does not satisfy the rule", func(t *testing.T) {
		wantErrContaining(t, site.transform(volume(map[string]any{"type": "csi", "image": map[string]any{"reference": "x"}})),
			requiredWhenSentence("csi", "type", "csi"))
	})
}

func TestRequiredWhenAdopters_VolumeMode(t *testing.T) {
	deployment := exclusiveSite{compType: "deployment", handler: &components.DeploymentHandler{}}
	statefulset := exclusiveSite{compType: "statefulset", handler: &components.StatefulsetHandler{}}
	pvc := func(extra map[string]any) map[string]any {
		v := map[string]any{"name": "disk", "type": "pvc", "claimName": "disk"}
		for k, val := range extra {
			v[k] = val
		}
		return map[string]any{"image": "ghcr.io/org/app:v1", "volumes": []any{v}}
	}
	vct := func(extra map[string]any) map[string]any {
		return map[string]any{"image": "ghcr.io/org/app:v1", "volumeClaimTemplates": []any{blockVCT(extra)}}
	}
	sites := []struct {
		name  string
		site  exclusiveSite
		props func(map[string]any) map[string]any
		mount string
	}{
		{"volume", deployment, pvc, "/data"},
		{"claim template", statefulset, vct, "/data"},
	}
	want := requiredWhenSentence("volumeMode", "devicePath", "")
	for _, s := range sites {
		t.Run(s.name+": devicePath without volumeMode", func(t *testing.T) {
			wantErrContaining(t, s.site.transform(s.props(map[string]any{"devicePath": "/dev/xvda"})), want)
		})
		t.Run(s.name+": devicePath with a null volumeMode", func(t *testing.T) {
			wantErrContaining(t, s.site.transform(s.props(map[string]any{"devicePath": "/dev/xvda", "volumeMode": nil})), want)
		})
		t.Run(s.name+": devicePath with volumeMode Block", func(t *testing.T) {
			if err := s.site.transform(s.props(map[string]any{"devicePath": "/dev/xvda", "volumeMode": "Block"})); err != nil {
				t.Errorf("a Block volume with a devicePath was refused: %v", err)
			}
		})
		t.Run(s.name+": mountPath alone needs no volumeMode", func(t *testing.T) {
			if err := s.site.transform(s.props(map[string]any{"mountPath": s.mount})); err != nil {
				t.Errorf("a mounted volume without volumeMode was refused: %v", err)
			}
		})
		t.Run(s.name+": a null devicePath beside a mountPath", func(t *testing.T) {
			err := s.site.transform(s.props(map[string]any{"mountPath": s.mount, "devicePath": nil}))
			if err != nil && strings.Contains(err.Error(), "is required when") {
				t.Errorf("a null devicePath is not set and the rule refused: %v", err)
			}
		})
	}
}

// TestRequiredWhenAdopters_PublishedRules pins the seven rows in the schema each
// component publishes, and that the shared claim spec stays free of the rule where
// it has no devicePath to name.
func TestRequiredWhenAdopters_PublishedRules(t *testing.T) {
	published := func(compType string, h oam.ComponentHandler) map[string]oam.PropertySchema {
		tr := oam.NewTransformer(map[string]oam.ComponentHandler{}, nil)
		tr.RegisterComponent(compType, h)
		return tr.HandlerSchemas().Components[compType]
	}
	rule := func(f oam.PropertySchema) string {
		if f.RequiredWhen == nil {
			return ""
		}
		if f.RequiredWhen.Equals == nil {
			return f.RequiredWhen.Key
		}
		return f.RequiredWhen.Key + "=" + f.RequiredWhen.Equals.(string)
	}

	volumes := published("deployment", &components.DeploymentHandler{})["volumes"].Items.Properties
	for key, want := range map[string]string{
		"csi": "type=csi", "downwardAPI": "type=downwardAPI", "ephemeral": "type=ephemeral",
		"image": "type=image", "projected": "type=projected", "volumeMode": "devicePath",
		// Authored flat as well, so never required as an object.
		"configMap": "", "secret": "", "emptyDir": "", "name": "", "type": "",
	} {
		if got := rule(volumes[key]); got != want {
			t.Errorf("volumes item %q: requiredWhen = %q, want %q", key, got, want)
		}
	}

	templates := published("statefulset", &components.StatefulsetHandler{})["volumeClaimTemplates"].Items.Properties
	if got := rule(templates["volumeMode"]); got != "devicePath" {
		t.Errorf("volumeClaimTemplates item volumeMode: requiredWhen = %q, want devicePath", got)
	}

	// The PersistentVolumeClaim component shares the claim spec and has no devicePath.
	pvcProps := published("persistentvolumeclaim", &components.PersistentVolumeClaimHandler{})
	if f, ok := pvcProps["volumeMode"]; !ok || rule(f) != "" {
		t.Errorf("persistentvolumeclaim volumeMode: declared = %v, requiredWhen = %q, want declared and none", ok, rule(f))
	}
}
