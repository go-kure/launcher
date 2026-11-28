package traits_test

import (
	"crypto/sha1" //nolint:gosec // a git blob id, not a security digest
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests hold the objects the monitoring trait writes to the
// CustomResourceDefinitions of VictoriaMetrics operator release v0.76.0
// (testdata/upstream/victoria-metrics-operator): every field the trait writes
// is one the CRD declares, with the type it declares, and every field the CRD
// requires is written. The trait writes unstructured objects, so this is what
// a typed API would have checked at compile time (go-kure/launcher#1736). It
// cannot say that an `expr` parses: the operator checks that.

const vmCRDDir = "testdata/upstream/victoria-metrics-operator/"

func readVMFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(vmCRDDir + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

// TestMonitoringCRD_FixtureMatchesSource: the vendored files are the ones the
// SOURCE file names.
func TestMonitoringCRD_FixtureMatchesSource(t *testing.T) {
	source := "\n" + string(readVMFile(t, "SOURCE"))
	for _, want := range []struct{ prefix, got string }{
		{"file crds.yaml ", gitBlobID(readVMFile(t, "crds.yaml"))},
		{"licence LICENSE ", gitBlobID(readVMFile(t, "LICENSE"))},
	} {
		if !strings.Contains(source, "\n"+want.prefix+want.got+"\n") {
			t.Errorf("SOURCE has no line %q: the file's own git blob id is %s", want.prefix+"…", want.got)
		}
	}
	if !strings.Contains(source, "\ntag v0.76.0 ") {
		t.Errorf("SOURCE does not name tag v0.76.0")
	}
}

func gitBlobID(data []byte) string {
	h := sha1.New() //nolint:gosec // a git blob id
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// vmCRD is one CustomResourceDefinition of the fixture: the kind, and the
// OpenAPI schema of the version the trait writes.
type vmCRD struct {
	group, version string
	spec           map[string]any
}

func loadVMCRDs(t *testing.T) map[string]vmCRD {
	t.Helper()
	out := map[string]vmCRD{}
	for _, doc := range strings.Split(string(readVMFile(t, "crds.yaml")), "\n---\n") {
		if strings.TrimSpace(doc) == "" {
			continue
		}
		var crd map[string]any
		if err := yaml.Unmarshal([]byte(doc), &crd); err != nil {
			t.Fatalf("parse a CRD of the fixture: %v", err)
		}
		spec, _ := crd["spec"].(map[string]any)
		names, _ := spec["names"].(map[string]any)
		kind, _ := names["kind"].(string)
		versions, _ := spec["versions"].([]any)
		for _, v := range versions {
			ver, _ := v.(map[string]any)
			if ver["name"] != vmVersion {
				continue
			}
			schema, _ := ver["schema"].(map[string]any)
			root, _ := schema["openAPIV3Schema"].(map[string]any)
			props, _ := root["properties"].(map[string]any)
			specSchema, _ := props["spec"].(map[string]any)
			out[kind] = vmCRD{group: spec["group"].(string), version: vmVersion, spec: specSchema}
		}
	}
	for _, kind := range []string{"VMServiceScrape", "VMPodScrape", "VMRule"} {
		if out[kind].spec == nil {
			t.Fatalf("the fixture has no %s %s schema", kind, vmVersion)
		}
	}
	return out
}

// checkAgainstSchema appends to problems every place v departs from the
// OpenAPI schema s: a field s does not declare (and does not leave open), a
// required field v lacks, a value of the wrong type.
func checkAgainstSchema(path string, v any, s map[string]any, problems *[]string) {
	if s["x-kubernetes-int-or-string"] == true {
		switch v.(type) {
		case string, int64, int, int32, float64:
		default:
			*problems = append(*problems, fmt.Sprintf("%s: %T is neither an integer nor a string", path, v))
		}
		return
	}
	switch s["type"] {
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			*problems = append(*problems, fmt.Sprintf("%s: %T is not an object", path, v))
			return
		}
		props, _ := s["properties"].(map[string]any)
		additional := s["additionalProperties"]
		for key, val := range m {
			switch {
			case props[key] != nil:
				child, _ := props[key].(map[string]any)
				checkAgainstSchema(path+"."+key, val, child, problems)
			case additional != nil && additional != false:
				if child, ok := additional.(map[string]any); ok {
					checkAgainstSchema(path+"."+key, val, child, problems)
				}
			case s["x-kubernetes-preserve-unknown-fields"] == true:
			default:
				*problems = append(*problems, fmt.Sprintf("%s.%s: the CRD declares no such field", path, key))
			}
		}
		if req, _ := s["required"].([]any); req != nil {
			for _, r := range req {
				if _, has := m[r.(string)]; !has {
					*problems = append(*problems, fmt.Sprintf("%s.%s: the CRD requires it", path, r))
				}
			}
		}
	case "array":
		list, ok := v.([]any)
		if !ok {
			*problems = append(*problems, fmt.Sprintf("%s: %T is not a list", path, v))
			return
		}
		items, _ := s["items"].(map[string]any)
		for i, item := range list {
			checkAgainstSchema(fmt.Sprintf("%s[%d]", path, i), item, items, problems)
		}
	case "string":
		if _, ok := v.(string); !ok {
			*problems = append(*problems, fmt.Sprintf("%s: %T is not a string", path, v))
		}
	case "integer":
		switch v.(type) {
		case int64, int, int32:
		default:
			*problems = append(*problems, fmt.Sprintf("%s: %T is not an integer", path, v))
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			*problems = append(*problems, fmt.Sprintf("%s: %T is not a boolean", path, v))
		}
	}
}

// vmProblems is every departure of obj from the CRD of its kind.
func vmProblems(t *testing.T, crds map[string]vmCRD, obj *unstructured.Unstructured) []string {
	t.Helper()
	crd, ok := crds[obj.GetKind()]
	if !ok {
		return []string{obj.GetKind() + ": no CRD in the fixture"}
	}
	var problems []string
	if got, want := obj.GetAPIVersion(), crd.group+"/"+crd.version; got != want {
		problems = append(problems, fmt.Sprintf("apiVersion %s, the CRD serves %s", got, want))
	}
	checkAgainstSchema("spec", obj.Object["spec"], crd.spec, &problems)
	sort.Strings(problems)
	return problems
}

// fullMonitoringObjects is the objects of components whose traits set every
// property between them: a webservice scraped by Service port name with rules, a
// webservice scraped by pod port number, and a worker scraped by container port
// name and by number.
func fullMonitoringObjects(t *testing.T) []*unstructured.Unstructured {
	t.Helper()
	rules := []any{map[string]any{"name": "availability", "interval": "30s", "rules": []any{
		map[string]any{"alert": "Down", "expr": "up == 0", "for": "5m",
			"labels": map[string]any{"severity": "page"}, "annotations": map[string]any{"summary": "down"}},
		map[string]any{"record": "job:up:avg", "expr": "avg(up)", "labels": map[string]any{"team": "a"}},
	}}}
	var all []*unstructured.Unstructured
	for _, comp := range []oam.Component{
		disruptionComponent("named", "webservice", monitoringTrait(map[string]any{
			"port": "http", "path": "/stats", "interval": "30s", "rules": rules, "scrapeName": "named-scrape", "ruleName": "named-rule"})),
		disruptionComponent("numbered", "webservice", monitoringTrait(map[string]any{"port": 9090, "path": "/m", "interval": "1m"})),
		disruptionComponent("plain", "webservice", monitoringTrait(map[string]any{})),
		disruptionComponent("podname", "worker", monitoringTrait(map[string]any{"port": "metrics", "path": "/m", "interval": "15s", "rules": rules})),
		disruptionComponent("podnum", "worker", monitoringTrait(map[string]any{"port": 9100})),
	} {
		objs, err := monitoringObjects(t, nil, comp)
		if err != nil {
			t.Fatalf("transform %s: %v", comp.Name, err)
		}
		all = append(all, objs...)
	}
	return all
}

// TestMonitoringCRD_EveryWrittenFieldIsDeclared holds each object the trait can
// write to the CRD of its kind.
func TestMonitoringCRD_EveryWrittenFieldIsDeclared(t *testing.T) {
	crds := loadVMCRDs(t)
	kinds := map[string]int{}
	for _, obj := range fullMonitoringObjects(t) {
		kinds[obj.GetKind()]++
		if problems := vmProblems(t, crds, obj); len(problems) > 0 {
			t.Errorf("%s %q departs from the v0.76.0 CRD:\n  %s", obj.GetKind(), obj.GetName(), strings.Join(problems, "\n  "))
		}
	}
	// A kind the fixtures never wrote is a kind this test never held.
	for _, kind := range []string{"VMServiceScrape", "VMPodScrape", "VMRule"} {
		if kinds[kind] == 0 {
			t.Errorf("no %s among the fixtures' objects (wrote %v)", kind, kinds)
		}
	}
}

// TestMonitoringCRD_CheckerFindsADefect is the positive control: the check
// above reports an invented field, a missing required one and a wrong type.
func TestMonitoringCRD_CheckerFindsADefect(t *testing.T) {
	crds := loadVMCRDs(t)
	objs, err := monitoringObjects(t, nil, disruptionComponent("web", "webservice", monitoringTrait(map[string]any{
		"rules": []any{map[string]any{"name": "g", "rules": []any{map[string]any{"alert": "A", "expr": "up"}}}}})))
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(o *unstructured.Unstructured)
		want   string
	}{
		{"invented field", func(o *unstructured.Unstructured) {
			if o.GetKind() == "VMServiceScrape" {
				ep := o.Object["spec"].(map[string]any)["endpoints"].([]any)[0].(map[string]any)
				ep["prot"] = "http"
			}
		}, "spec.endpoints[0].prot: the CRD declares no such field"},
		{"missing required field", func(o *unstructured.Unstructured) {
			if o.GetKind() == "VMServiceScrape" {
				delete(o.Object["spec"].(map[string]any), "endpoints")
			}
		}, "spec.endpoints: the CRD requires it"},
		{"wrong type", func(o *unstructured.Unstructured) {
			if o.GetKind() == "VMRule" {
				o.Object["spec"].(map[string]any)["groups"].([]any)[0].(map[string]any)["name"] = 7
			}
		}, "spec.groups[0].name: int is not a string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			found := false
			for _, obj := range objs {
				c := obj.DeepCopy()
				tc.mutate(c)
				if p := vmProblems(t, crds, c); strings.Contains(strings.Join(p, "\n"), tc.want) {
					found = true
				}
			}
			if !found {
				t.Errorf("no problem reported containing %q", tc.want)
			}
		})
	}
}
