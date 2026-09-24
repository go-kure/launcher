package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// A required string field authored with the wrong type used to be read with a
// discarding type assertion (`s, _ := m[k].(string)`), so `fieldPath: 123`
// became "" and was reported as "fieldPath is required" — the author wrote the
// field and was sent looking for a missing key instead of a wrong type
// (go-kure/launcher#453). Three sites also OR'd several fields into one
// message, so a wrong type on one read as any of them possibly being absent.
//
// Each site is checked both ways: a wrongly typed value must name the field
// and its type and must not say "required", and an absent value must still
// say "required" for that one field. The pair matters — before the fix both
// shapes produced the same message, so a test pinning only the type error
// would also pass if absence regressed.

// absent is the sentinel a requiredFieldSite's props builder receives to leave
// the field out entirely.
type absentField struct{}

var absent = absentField{}

// setOrOmit writes v under key, or leaves key out when v is the absent
// sentinel.
func setOrOmit(m map[string]any, key string, v any) map[string]any {
	if _, ok := v.(absentField); !ok {
		m[key] = v
	}
	return m
}

type requiredFieldSite struct {
	name  string
	props func(v any) map[string]any
	run   func(props map[string]any) error
	// typeErr is the exact error text a wrongly typed (int) value produces.
	typeErr string
	// requiredErr is the exact error text an absent value produces.
	requiredErr string
	// good is a well-typed value with which the document converts.
	good any
}

func runWebservice(props map[string]any) error {
	_, err := (&components.WebserviceHandler{}).ToApplicationConfig(
		&oam.Component{Name: "app", Type: "webservice", Properties: props}, "default")
	return err
}

func runHelmchart(props map[string]any) error {
	_, err := (&components.HelmchartHandler{}).ToApplicationConfig(
		&oam.Component{Name: "metrics", Type: "helmchart", Properties: props}, "monitoring")
	return err
}

func runManifests(props map[string]any) error {
	_, err := (&components.ManifestsHandler{}).ToApplicationConfig(
		&oam.Component{Name: "m", Type: "manifests", Properties: props}, "default")
	return err
}

func runOCI(props map[string]any) error {
	_, err := (&components.OCIHandler{}).ToApplicationConfig(
		&oam.Component{Name: "checkout", Type: "oci", Properties: props}, "checkout")
	return err
}

// envValueFrom builds a webservice whose single env entry reads valueFrom.<kind>.
func envValueFrom(kind string, ref map[string]any) map[string]any {
	return map[string]any{
		"image": "ghcr.io/org/app:v1",
		"env": []any{map[string]any{
			"name":      "X",
			"valueFrom": map[string]any{kind: ref},
		}},
	}
}

func fileKeyRef(field string, v any) map[string]any {
	ref := map[string]any{"volumeName": "envfiles", "path": "api.env", "key": "API_KEY"}
	delete(ref, field)
	props := envValueFrom("fileKeyRef", setOrOmit(ref, field, v))
	props["volumes"] = []any{map[string]any{"name": "envfiles", "type": "emptyDir", "mountPath": "/etc/envfiles"}}
	return props
}

func envFromRef(kind string, v any) map[string]any {
	return map[string]any{
		"image":   "ghcr.io/org/app:v1",
		"envFrom": []any{map[string]any{kind: setOrOmit(map[string]any{}, "name", v)}},
	}
}

func extraContainer(block, field string, v any) map[string]any {
	c := map[string]any{"name": "helper", "image": "ghcr.io/org/helper:v1"}
	delete(c, field)
	return map[string]any{
		"image": "ghcr.io/org/app:v1",
		block:   []any{setOrOmit(c, field, v)},
	}
}

func extraContainerMount(block, field string, v any) map[string]any {
	mount := map[string]any{"name": "data", "mountPath": "/data"}
	delete(mount, field)
	return map[string]any{
		"image":   "ghcr.io/org/app:v1",
		"volumes": []any{map[string]any{"name": "data", "type": "emptyDir", "mountPath": "/srv"}},
		block: []any{map[string]any{
			"name":         "helper",
			"image":        "ghcr.io/org/helper:v1",
			"volumeMounts": []any{setOrOmit(mount, field, v)},
		}},
	}
}

func scopeOverride(field string, v any) map[string]any {
	o := map[string]any{"apiVersion": "fixtures.example.com/v1", "kind": "ClusterWidget", "scope": "Cluster"}
	delete(o, field)
	return map[string]any{
		"inline":         "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n",
		"scopeOverrides": []any{setOrOmit(o, field, v)},
	}
}

func requiredFieldSites() []requiredFieldSite {
	fileKeyRefGood := map[string]any{"volumeName": "envfiles", "path": "api.env", "key": "API_KEY"}
	var sites []requiredFieldSite
	for _, f := range []string{"volumeName", "path", "key"} {
		sites = append(sites, requiredFieldSite{
			name:        "fileKeyRef." + f,
			props:       func(v any) map[string]any { return fileKeyRef(f, v) },
			run:         runWebservice,
			typeErr:     "fileKeyRef." + f + ": must be a string, got int",
			requiredErr: "fileKeyRef: " + f + " is required",
			good:        fileKeyRefGood[f],
		})
	}
	sites = append(sites,
		requiredFieldSite{
			name: "fieldRef.fieldPath",
			props: func(v any) map[string]any {
				return envValueFrom("fieldRef", setOrOmit(map[string]any{}, "fieldPath", v))
			},
			run:         runWebservice,
			typeErr:     "fieldRef.fieldPath: must be a string, got int",
			requiredErr: "fieldRef: fieldPath is required",
			good:        "metadata.name",
		},
		requiredFieldSite{
			name: "resourceFieldRef.resource",
			props: func(v any) map[string]any {
				return envValueFrom("resourceFieldRef", setOrOmit(map[string]any{}, "resource", v))
			},
			run:         runWebservice,
			typeErr:     "resourceFieldRef.resource: must be a string, got int",
			requiredErr: "resourceFieldRef: resource is required",
			good:        "limits.cpu",
		},
		requiredFieldSite{
			name:        "envFrom.configMapRef.name",
			props:       func(v any) map[string]any { return envFromRef("configMapRef", v) },
			run:         runWebservice,
			typeErr:     "envFrom[0].configMapRef.name: must be a string, got int",
			requiredErr: "envFrom[0].configMapRef: name is required",
			good:        "cfg",
		},
		requiredFieldSite{
			name:        "envFrom.secretRef.name",
			props:       func(v any) map[string]any { return envFromRef("secretRef", v) },
			run:         runWebservice,
			typeErr:     "envFrom[0].secretRef.name: must be a string, got int",
			requiredErr: "envFrom[0].secretRef: name is required",
			good:        "cfg",
		},
	)
	for _, block := range []string{"initContainers", "sidecars"} {
		sites = append(sites,
			requiredFieldSite{
				name:        block + ".name",
				props:       func(v any) map[string]any { return extraContainer(block, "name", v) },
				run:         runWebservice,
				typeErr:     block + "[0].name: must be a string, got int",
				requiredErr: block + "[0]: name is required",
				good:        "helper",
			},
			requiredFieldSite{
				name:        block + ".image",
				props:       func(v any) map[string]any { return extraContainer(block, "image", v) },
				run:         runWebservice,
				typeErr:     block + `[0] "helper": image: must be a string, got int`,
				requiredErr: block + `[0] "helper": image is required`,
				good:        "ghcr.io/org/helper:v1",
			},
		)
		for _, f := range []string{"name", "mountPath"} {
			sites = append(sites, requiredFieldSite{
				name:        block + ".volumeMounts." + f,
				props:       func(v any) map[string]any { return extraContainerMount(block, f, v) },
				run:         runWebservice,
				typeErr:     block + `[0] "helper": volumeMounts[0].` + f + ": must be a string, got int",
				requiredErr: block + `[0] "helper": volumeMounts[0]: ` + f + " is required",
				good:        map[string]any{"name": "data", "mountPath": "/data"}[f],
			})
		}
	}
	sites = append(sites, requiredFieldSite{
		name: "helmchart.valuesFrom.name",
		props: func(v any) map[string]any {
			return map[string]any{
				"chart":      "kube-prometheus-stack",
				"source":     map[string]any{"url": "https://prometheus-community.github.io/helm-charts"},
				"valuesFrom": []any{setOrOmit(map[string]any{"kind": "ConfigMap"}, "name", v)},
			}
		},
		run:         runHelmchart,
		typeErr:     "valuesFrom[0].name: must be a string, got int",
		requiredErr: "valuesFrom[0]: name is required",
		good:        "cfg",
	})
	for _, f := range []string{"apiVersion", "kind"} {
		sites = append(sites, requiredFieldSite{
			name:        "manifests.scopeOverrides." + f,
			props:       func(v any) map[string]any { return scopeOverride(f, v) },
			run:         runManifests,
			typeErr:     "scopeOverrides[0]." + f + ": must be a string, got int",
			requiredErr: "scopeOverrides[0]: " + f + " is required",
			good:        map[string]any{"apiVersion": "fixtures.example.com/v1", "kind": "ClusterWidget"}[f],
		})
	}
	sites = append(sites,
		requiredFieldSite{
			name: "oci.source.url",
			props: func(v any) map[string]any {
				return map[string]any{"source": setOrOmit(map[string]any{}, "url", v), "version": "0.3.0"}
			},
			run:         runOCI,
			typeErr:     "oci: source.url: must be a string, got int",
			requiredErr: "oci: source.url is required",
			good:        "oci://registry.example.com/charts/checkout",
		},
		requiredFieldSite{
			name: "oci.version",
			props: func(v any) map[string]any {
				return setOrOmit(map[string]any{"source": map[string]any{"url": "oci://registry.example.com/charts/checkout"}}, "version", v)
			},
			run:         runOCI,
			typeErr:     "oci: version: must be a string, got int",
			requiredErr: "oci: version is required",
			good:        "0.3.0",
		},
	)
	return sites
}

func TestRequiredField_WrongTypeNamesTheType(t *testing.T) {
	for _, s := range requiredFieldSites() {
		t.Run(s.name, func(t *testing.T) {
			err := s.run(s.props(123))
			if err == nil {
				t.Fatal("a wrongly typed required field converted cleanly")
			}
			if !strings.Contains(err.Error(), s.typeErr) {
				t.Errorf("error = %q, want it to contain %q", err, s.typeErr)
			}
			if strings.Contains(err.Error(), "required") {
				t.Errorf("error = %q reports a present, wrongly typed field as missing", err)
			}
		})
	}
}

func TestRequiredField_AbsentStillRequired(t *testing.T) {
	for _, s := range requiredFieldSites() {
		for label, v := range map[string]any{"absent": absent, "empty": "", "null": nil} {
			t.Run(s.name+"/"+label, func(t *testing.T) {
				err := s.run(s.props(v))
				if err == nil {
					t.Fatalf("a %s required field converted cleanly", label)
				}
				if !strings.Contains(err.Error(), s.requiredErr) {
					t.Errorf("error = %q, want it to contain %q", err, s.requiredErr)
				}
			})
		}
	}
}

// The builders must produce a document that converts when the field is
// present and well typed; otherwise the two tests above could be passing on an
// unrelated error.
func TestRequiredField_ControlBuilds(t *testing.T) {
	for _, s := range requiredFieldSites() {
		t.Run(s.name, func(t *testing.T) {
			if err := s.run(s.props(s.good)); err != nil {
				t.Errorf("well-typed control failed: %v", err)
			}
		})
	}
}
