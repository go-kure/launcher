package components

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// refusedKeyBase is, per type of refusedKeys, a document of that type that
// both paths accept: the control every refusal below is measured against.
var refusedKeyBase = map[string]map[string]any{
	"deployment":            {"image": "ghcr.io/org/app:v1"},
	"webservice":            {"image": "ghcr.io/org/app:v1"},
	"worker":                {"image": "ghcr.io/org/app:v1"},
	"statefulset":           {"image": "ghcr.io/org/app:v1"},
	"daemonset":             {"image": "ghcr.io/org/app:v1"},
	"job":                   {"image": "ghcr.io/org/app:v1"},
	"cronjob":               {"image": "ghcr.io/org/app:v1", "schedule": "*/5 * * * *"},
	"service":               {"ports": []any{map[string]any{"port": 80}}},
	"persistentvolumeclaim": {"size": "1Gi"},
	"serviceaccount":        {},
}

// refusedKeyTransformer registers every type of refusedKeys at the position the
// built-in registry gives it: a terminal handler, or for webservice and worker
// their lowering rule.
func refusedKeyTransformer() *oam.Transformer {
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{
		"deployment":            &DeploymentHandler{},
		"statefulset":           &StatefulsetHandler{},
		"daemonset":             &DaemonsetHandler{},
		"job":                   &JobHandler{},
		"cronjob":               &CronjobHandler{},
		"service":               &ServiceHandler{},
		"persistentvolumeclaim": &PersistentVolumeClaimHandler{},
		"serviceaccount":        &ServiceAccountHandler{},
		"configmap":             &ConfigMapHandler{},
	}, nil)
	tr.RegisterComponentLowering(WebserviceRule{})
	tr.RegisterComponentLowering(WorkerRule{})
	return tr
}

// refusedKeyPaths runs one component down the two paths an authored key takes:
// the type's own parser, as a caller that drives Transform without the document
// check reaches it, and the document check.
func refusedKeyPaths(t *testing.T, tr *oam.Transformer, componentType string, props map[string]any) (direct, document error) {
	t.Helper()
	comp := func() *oam.Component {
		return &oam.Component{Name: "c", Type: componentType, Properties: maps.Clone(props)}
	}
	switch componentType {
	case "webservice":
		_, direct = parseWebservice(comp())
	case "worker":
		_, direct = parseWorker(comp().Properties)
	default:
		handlers := map[string]oam.ComponentHandler{
			"deployment":            &DeploymentHandler{},
			"statefulset":           &StatefulsetHandler{},
			"daemonset":             &DaemonsetHandler{},
			"job":                   &JobHandler{},
			"cronjob":               &CronjobHandler{},
			"service":               &ServiceHandler{},
			"persistentvolumeclaim": &PersistentVolumeClaimHandler{},
			"serviceaccount":        &ServiceAccountHandler{},
			"configmap":             &ConfigMapHandler{},
		}
		h, ok := handlers[componentType]
		if !ok {
			t.Fatalf("no handler for type %q: add it here and to refusedKeyTransformer", componentType)
		}
		_, direct = h.ToApplicationConfig(comp(), "ns")
	}
	document = tr.ValidateAuthoredProperties(&oam.Application{Spec: oam.ApplicationSpec{Components: []oam.Component{*comp()}}})
	return direct, document
}

// nullIsUnauthoredToParser reports the refusals a type's parser does not make
// for an explicit null, which it reads as "not authored": the service's and
// the claim's (authoredValue), and on a deployment every key below its spec
// map, since the handler strips nulls before the pod and container parsers run
// (withoutExplicitNulls). The document check refuses the null key on all of
// them; that difference is older than refusedKeys and is pinned here as it is.
func nullIsUnauthoredToParser(componentType, key string) bool {
	switch componentType {
	case "service", "persistentvolumeclaim":
		return true
	case "deployment":
		_, specKey := deploymentSpecRejectedKeys[key]
		return !specKey
	}
	return false
}

// TestRefusedKeys_BothPathsGiveTheReason holds every entry of every refusal map
// of refusedKeys to what the file's comment says (go-kure/launcher#790): the
// type's parser refuses the key with the map's text, and the document check
// refuses it with its generic text followed by "; " and that same text. So a
// caller that skips the document check and one that runs it read the same
// reason, and neither has the key dropped in silence.
//
// The document check refuses the key whatever its value. So does the parser,
// except where nullIsUnauthoredToParser says it reads a null as unauthored.
func TestRefusedKeys_BothPathsGiveTheReason(t *testing.T) {
	tr := refusedKeyTransformer()
	for _, componentType := range slices.Sorted(maps.Keys(refusedKeys)) {
		base, ok := refusedKeyBase[componentType]
		if !ok {
			t.Errorf("type %q has no base document in refusedKeyBase", componentType)
			continue
		}
		t.Run(componentType+"/control", func(t *testing.T) {
			direct, document := refusedKeyPaths(t, tr, componentType, base)
			if direct != nil || document != nil {
				t.Fatalf("the base document is refused: parser %v, document check %v", direct, document)
			}
		})

		seen := map[string]bool{}
		for _, refusals := range refusedKeys[componentType] {
			for _, key := range slices.Sorted(maps.Keys(refusals)) {
				reason := refusals[key]
				if seen[key] {
					t.Errorf("type %q: key %q is in two of its refusal maps; one reason per key", componentType, key)
					continue
				}
				seen[key] = true
				if !strings.HasPrefix(reason, key+": ") {
					t.Errorf("type %q: the reason for %q does not begin with the key: %q", componentType, key, reason)
				}
				if got := refusedKeyHint(componentType, key); got != reason {
					t.Errorf("refusedKeyHint(%q, %q) = %q, want %q", componentType, key, got, reason)
				}
				for _, value := range []struct {
					name string
					v    any
				}{{"a value", "x"}, {"null", nil}} {
					t.Run(componentType+"/"+key+"/"+value.name, func(t *testing.T) {
						props := maps.Clone(base)
						props[key] = value.v
						direct, document := refusedKeyPaths(t, tr, componentType, props)
						switch {
						case value.v == nil && nullIsUnauthoredToParser(componentType, key):
							if direct != nil {
								t.Errorf("parser: error = %v, want a null %s read as unauthored", direct, key)
							}
						case direct == nil || direct.Error() != reason:
							t.Errorf("parser: error = %v, want exactly %q", direct, reason)
						}
						generic := `component "c" (type "` + componentType + `"): properties: unsupported field "` + key + `" (allowed: `
						if document == nil || !strings.HasPrefix(document.Error(), generic) || !strings.HasSuffix(document.Error(), "); "+reason) {
							t.Errorf("document check: error = %v, want %q…, then \"; \" and %q", document, generic, reason)
						}
					})
				}
			}
		}
	}
}

// TestRefusedKeys_NamedRefusals names the refusals go-kure/launcher#790 added,
// so that a row dropped from a map or from the table is a failure here and not
// only one case fewer in the table test above. Before, the parser dropped each
// of these in silence: the cronjob's three JobSpec keys, the whole `template`
// on a statefulset and a daemonset, and the upstream fields no hand-parsed type
// reads.
func TestRefusedKeys_NamedRefusals(t *testing.T) {
	for _, tc := range []struct{ componentType, key string }{
		{"cronjob", "selector"},
		{"cronjob", "manualSelector"},
		{"cronjob", "template"},
		{"cronjob", "scheduling"},
		{"job", "scheduling"},
		{"statefulset", "template"},
		{"daemonset", "template"},
		{"deployment", "restartPolicy"},
		{"statefulset", "restartPolicy"},
		{"daemonset", "restartPolicy"},
		{"webservice", "restartPolicy"},
		{"worker", "restartPolicy"},
		{"deployment", "activeDeadlineSeconds"},
		{"statefulset", "activeDeadlineSeconds"},
		{"daemonset", "activeDeadlineSeconds"},
		{"deployment", "evictionResponders"},
		{"job", "evictionResponders"},
		{"deployment", "restartPolicyRules"},
		{"cronjob", "restartPolicyRules"},
		{"serviceaccount", "secrets"},
		// Upstream names read in another shape.
		{"deployment", "containers"},
		{"job", "containers"},
		{"webservice", "livenessProbe"},
		{"statefulset", "readinessProbe"},
		{"cronjob", "startupProbe"},
		{"daemonset", "volumeMounts"},
		{"statefulset", "volumeDevices"},
		{"worker", "name"},
		{"job", "name"},
		{"cronjob", "jobTemplate"},
		{"persistentvolumeclaim", "resources"},
	} {
		if refusedKeyHint(tc.componentType, tc.key) == "" {
			t.Errorf("type %q does not refuse %q", tc.componentType, tc.key)
		}
	}
	// A key the type has no refusal for, whether it reads the key or leaves it
	// undeclared, gets no hint, whatever another type makes of it.
	for _, tc := range []struct{ componentType, key string }{
		{"job", "restartPolicy"},
		{"cronjob", "restartPolicy"},
		{"job", "activeDeadlineSeconds"},
		{"cronjob", "schedule"},
		{"job", "jobTemplate"},
		{"service", "name"},
		{"serviceaccount", "name"},
	} {
		if got := refusedKeyHint(tc.componentType, tc.key); got != "" {
			t.Errorf("type %q has no refusal for %q, but refusedKeyHint gives %q", tc.componentType, tc.key, got)
		}
	}
}

// TestRefusedKeys_OneLevelDown holds the two refusals that sit inside a
// property the type declares (go-kure/launcher#790): an upstream field of
// corev1.Affinity under the `affinity` shorthand of webservice and worker, and `claims` under a container's `resources`. The parser of the
// property refuses each with its reason, whatever the value; before, a caller
// that drives Transform without the document check had them dropped. The
// document check refuses the same key with its generic text, followed by the
// same reason (the type's NestedUnsupportedFieldHint); a key that is no field
// of the upstream type gets the generic text alone.
func TestRefusedKeys_OneLevelDown(t *testing.T) {
	tr := refusedKeyTransformer()
	with := func(componentType, key string, value any) map[string]any {
		props := maps.Clone(refusedKeyBase[componentType])
		props[key] = value
		return props
	}
	check := func(t *testing.T, componentType string, props map[string]any, reason, documentPath, nested string, hinted bool) {
		t.Helper()
		direct, document := refusedKeyPaths(t, tr, componentType, props)
		if direct == nil || !strings.HasSuffix(direct.Error(), reason) {
			t.Errorf("parser: error = %v, want it to end with %q", direct, reason)
		}
		generic := `component "c" (type "` + componentType + `"): ` + documentPath + `: unsupported field "` + nested + `" (allowed: `
		if document == nil || !strings.HasPrefix(document.Error(), generic) {
			t.Fatalf("document check: error = %v, want it to begin with %q", document, generic)
		}
		if hinted && !strings.HasSuffix(document.Error(), "); "+reason) {
			t.Errorf("document check: error = %v, want the allowed list followed by the parser's reason %q", document, reason)
		}
		if !hinted && !strings.HasSuffix(document.Error(), ")") {
			t.Errorf("document check: error = %v, want it to end with the allowed list and nothing after it", document)
		}
	}
	values := []struct {
		name string
		v    any
	}{{"a value", map[string]any{}}, {"null", nil}}

	for _, componentType := range []string{"webservice", "worker"} {
		t.Run(componentType+"/affinity/control", func(t *testing.T) {
			direct, document := refusedKeyPaths(t, tr, componentType, with(componentType, "affinity", map[string]any{
				"enablePodAntiAffinity": true, "topologyKey": "topology.kubernetes.io/zone",
				"podAntiAffinityType": "required", "nodeSelector": map[string]any{"disk": "ssd"},
			}))
			if direct != nil || document != nil {
				t.Fatalf("the four keys of the shorthand are refused: parser %v, document check %v", direct, document)
			}
		})
		for _, key := range slices.Sorted(maps.Keys(affinityShorthandRejectedKeys)) {
			reason := affinityShorthandRejectedKeys[key]
			if !strings.HasPrefix(reason, "affinity."+key+": ") {
				t.Errorf("the reason for affinity.%s does not begin with its path: %q", key, reason)
			}
			for _, value := range values {
				t.Run(componentType+"/affinity/"+key+"/"+value.name, func(t *testing.T) {
					check(t, componentType, with(componentType, "affinity", map[string]any{key: value.v}), reason, "properties.affinity", key, true)
				})
			}
		}
		for _, value := range values {
			t.Run(componentType+"/affinity/no upstream field/"+value.name, func(t *testing.T) {
				check(t, componentType, with(componentType, "affinity", map[string]any{"noSuchKey": value.v}),
					`affinity: unrecognized key "noSuchKey"`, "properties.affinity", "noSuchKey", false)
			})
		}
	}

	reason := resourcesRejectedKeys["claims"]
	if !strings.HasPrefix(reason, "resources.claims: ") {
		t.Errorf("the reason for resources.claims does not begin with its path: %q", reason)
	}
	entry := func(resources map[string]any) []any {
		return []any{map[string]any{"name": "extra", "image": "busybox:1", "resources": resources}}
	}
	for _, componentType := range []string{"deployment", "webservice", "worker", "statefulset", "daemonset", "job", "cronjob"} {
		// A job's pod runs to completion, so `job` and `cronjob` have no
		// `sidecars` property.
		lists := []string{"initContainers", "sidecars"}
		if componentType == "job" || componentType == "cronjob" {
			lists = []string{"initContainers"}
		}
		t.Run(componentType+"/resources/control", func(t *testing.T) {
			requests := map[string]any{"requests": map[string]any{"cpu": "10m"}}
			documents := []map[string]any{with(componentType, "resources", requests)}
			for _, list := range lists {
				documents = append(documents, with(componentType, list, entry(requests)))
			}
			for _, props := range documents {
				if direct, document := refusedKeyPaths(t, tr, componentType, props); direct != nil || document != nil {
					t.Fatalf("resources with requests alone are refused: parser %v, document check %v", direct, document)
				}
			}
		})
		for _, value := range []struct {
			name string
			v    any
		}{{"a value", []any{map[string]any{"name": "gpu"}}}, {"null", nil}} {
			claims := map[string]any{"claims": value.v}
			t.Run(componentType+"/resources/claims/"+value.name, func(t *testing.T) {
				check(t, componentType, with(componentType, "resources", claims), reason, "properties.resources", "claims", true)
			})
			for _, list := range lists {
				t.Run(componentType+"/"+list+"/resources/claims/"+value.name, func(t *testing.T) {
					check(t, componentType, with(componentType, list, entry(claims)), reason, "properties."+list+"[0].resources", "claims", true)
				})
			}
		}
	}
}

// TestRefusedKeys_InAnEntry holds the refusals inside a list entry the type
// declares (go-kure/launcher#790): `probes` and `lifecycle` on an init
// container entry, on every workload type, and the claim-spec fields a
// statefulset's volumeClaimTemplates entry refuses. The document check refuses
// each with its generic text followed by the reason the entry's parser gives.
// A key of an entry that a type refuses only at its top level (the main
// container's `livenessProbe`) gets the generic text alone: the type's
// top-level hint is not asked about a nested key.
func TestRefusedKeys_InAnEntry(t *testing.T) {
	tr := refusedKeyTransformer()
	check := func(t *testing.T, componentType string, props map[string]any, documentPath, key, hint string) {
		t.Helper()
		direct, document := refusedKeyPaths(t, tr, componentType, props)
		generic := `component "c" (type "` + componentType + `"): ` + documentPath + `: unsupported field "` + key + `" (allowed: `
		if document == nil || !strings.HasPrefix(document.Error(), generic) {
			t.Fatalf("document check: error = %v, want it to begin with %q", document, generic)
		}
		if hint == "" {
			if !strings.HasSuffix(document.Error(), ")") {
				t.Errorf("document check: error = %v, want it to end with the allowed list and nothing after it", document)
			}
			return
		}
		if direct == nil || !strings.HasSuffix(direct.Error(), hint) {
			t.Errorf("parser: error = %v, want it to end with %q", direct, hint)
		}
		if !strings.HasSuffix(document.Error(), "); "+hint) {
			t.Errorf("document check: error = %v, want the allowed list followed by the parser's reason %q", document, hint)
		}
	}
	initEntry := func(key string) []any {
		return []any{map[string]any{"name": "init", "image": "busybox:1", key: map[string]any{}}}
	}
	for _, componentType := range []string{"deployment", "webservice", "worker", "statefulset", "daemonset", "job", "cronjob"} {
		if refusedKeyHint(componentType, "livenessProbe") == "" {
			t.Fatalf("type %q has no top-level refusal of livenessProbe; pick another key for the control below", componentType)
		}
		for _, key := range slices.Sorted(maps.Keys(initContainerRejectedKeys)) {
			t.Run(componentType+"/initContainers/"+key, func(t *testing.T) {
				props := maps.Clone(refusedKeyBase[componentType])
				props["initContainers"] = initEntry(key)
				check(t, componentType, props, "properties.initContainers[0]", key,
					key+": not supported on an init container — "+initContainerRejectedKeys[key])
			})
		}
		t.Run(componentType+"/initContainers/livenessProbe", func(t *testing.T) {
			props := maps.Clone(refusedKeyBase[componentType])
			props["initContainers"] = initEntry("livenessProbe")
			check(t, componentType, props, "properties.initContainers[0]", "livenessProbe", "")
		})
	}
	for _, key := range slices.Sorted(maps.Keys(volumeClaimTemplateRejectedKeys)) {
		t.Run("statefulset/volumeClaimTemplates/"+key, func(t *testing.T) {
			props := maps.Clone(refusedKeyBase["statefulset"])
			props["volumeClaimTemplates"] = []any{map[string]any{"name": "data", "size": "1Gi", "mountPath": "/data", key: "x"}}
			check(t, "statefulset", props, "properties.volumeClaimTemplates[0]", key,
				key+": not authorable — "+volumeClaimTemplateRejectedKeys[key])
		})
	}
}

// TestRefusedKeys_OtherKeysReadAsBefore is the control for the hint: a key that
// is no field of the upstream type keeps the document check's generic refusal
// on a type of refusedKeys, and a type with no row there (configmap) is refused
// as it was, with nothing appended.
func TestRefusedKeys_OtherKeysReadAsBefore(t *testing.T) {
	tr := refusedKeyTransformer()
	if _, listed := refusedKeys["configmap"]; listed {
		t.Fatal("configmap has a row in refusedKeys; pick another type with none as this test's control")
	}
	for _, tc := range []struct {
		componentType string
		base          map[string]any
	}{
		{"deployment", refusedKeyBase["deployment"]},
		{"webservice", refusedKeyBase["webservice"]},
		{"service", refusedKeyBase["service"]},
		{"configmap", map[string]any{"data": map[string]any{"k": "v"}}},
	} {
		t.Run(tc.componentType, func(t *testing.T) {
			props := maps.Clone(tc.base)
			props["noSuchField"] = "x"
			if got := refusedKeyHint(tc.componentType, "noSuchField"); got != "" {
				t.Errorf("refusedKeyHint = %q, want none", got)
			}
			_, document := refusedKeyPaths(t, tr, tc.componentType, props)
			generic := `component "c" (type "` + tc.componentType + `"): properties: unsupported field "noSuchField" (allowed: `
			if document == nil || !strings.HasPrefix(document.Error(), generic) || !strings.HasSuffix(document.Error(), ")") {
				t.Errorf("document check: error = %v, want %q… ending with the allowed list and nothing after it", document, generic)
			}
		})
	}
}
