package kurel

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// These tests pin the workload model that launcher ships in the binary
// (go-kure/launcher#1737): an application whose directory holds app.yaml only builds a
// component of type workload@v1 in each mode, and each optional part is emitted only when its
// input is set.

// workloadProfile is a ClusterProfile with the expose capability, which the exposure part needs.
const workloadProfile = "testdata/webservice-expose-ingress/cluster.yaml"

const workloadAppHead = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
`

// workloadComponent is one component of the shipped workload type; props is the indented
// properties block, each line written as it is in app.yaml under "properties:".
func workloadComponent(name, props string) string {
	return "    - name: " + name + "\n      type: workload@v1\n      properties:\n" + props
}

// buildWorkload builds an application of the given components with no models/ directory and
// returns its objects.
func buildWorkload(t *testing.T, components ...string) ([]map[string]any, error) {
	t.Helper()
	dir := modelPackage(t, workloadAppHead+strings.Join(components, ""), nil)
	out, err := runKurel(t, "build", dir, "--profile", workloadProfile)
	if err != nil {
		return nil, err
	}
	return decodeObjects(t, out), nil
}

// decodeObjects decodes the YAML documents a build printed. Any decode error other than the end
// of the output fails the test.
func decodeObjects(t *testing.T, out string) []map[string]any {
	t.Helper()
	dec := yaml.NewDecoder(bytes.NewReader([]byte(out)))
	var docs []map[string]any
	for {
		var d map[string]any
		if err := dec.Decode(&d); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode build output: %v", err)
		}
		if d != nil {
			docs = append(docs, d)
		}
	}
	return docs
}

func mustBuildWorkload(t *testing.T, components ...string) []map[string]any {
	t.Helper()
	docs, err := buildWorkload(t, components...)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return docs
}

// kindsOf lists the kinds of docs, sorted, one entry per object.
func kindsOf(docs []map[string]any) []string {
	var kinds []string
	for _, d := range docs {
		kinds = append(kinds, d["kind"].(string))
	}
	sort.Strings(kinds)
	return kinds
}

func objectsOf(docs []map[string]any, kind string) []map[string]any {
	var out []map[string]any
	for _, d := range docs {
		if d["kind"] == kind {
			out = append(out, d)
		}
	}
	return out
}

func requireKinds(t *testing.T, docs []map[string]any, present, absent []string) {
	t.Helper()
	for _, k := range present {
		if len(objectsOf(docs, k)) == 0 {
			t.Errorf("no %s in %v", k, kindsOf(docs))
		}
	}
	for _, k := range absent {
		if n := len(objectsOf(docs, k)); n != 0 {
			t.Errorf("%d %s in %v, want none", n, k, kindsOf(docs))
		}
	}
}

func asText(t *testing.T, v any) string {
	t.Helper()
	b, err := yaml.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// allWorkloadKinds is every kind the model can write; a build that does not ask for one lists it as absent.
var allWorkloadKinds = []string{
	"Deployment", "Service", "ServiceAccount", "Job", "PersistentVolumeClaim", "Cluster", "ObjectStore",
	"ScheduledBackup", "Ingress", "ReplicationSource", "VMServiceScrape", "VMPodScrape", "VMRule",
}

func kindsWithout(all []string, drop ...string) []string {
	var out []string
	for _, k := range all {
		keep := true
		for _, d := range drop {
			keep = keep && k != d
		}
		if keep {
			out = append(out, k)
		}
	}
	return out
}

// TestShippedWorkloadModes: each mode lowers to its own workload, and an application that sets
// no optional input gets nothing else.
func TestShippedWorkloadModes(t *testing.T) {
	tests := []struct {
		name, props string
		present     []string
	}{
		{"service by default", "        image: reg/orders:1\n", []string{"Deployment", "Service", "ServiceAccount"}},
		{"service", "        mode: service\n        image: reg/orders:1\n", []string{"Deployment", "Service", "ServiceAccount"}},
		{"worker", "        mode: worker\n        image: reg/orders:1\n", []string{"Deployment", "ServiceAccount"}},
		{"job", "        mode: job\n        image: reg/orders:1\n", []string{"Job"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			docs := mustBuildWorkload(t, workloadComponent("orders", tt.props))
			requireKinds(t, docs, tt.present, kindsWithout(allWorkloadKinds, tt.present...))
		})
	}
}

// TestShippedWorkloadModeIsRefused: the mode input is one of three words.
func TestShippedWorkloadModeIsRefused(t *testing.T) {
	_, err := buildWorkload(t, workloadComponent("orders", "        mode: batch\n        image: reg/orders:1\n"))
	requireRefused(t, err, "mode", "batch")
}

// TestShippedWorkloadParts: each optional input switches its part on, in every mode it applies to.
func TestShippedWorkloadParts(t *testing.T) {
	const volume = "        volume: {size: 1Gi, mountPath: /data}\n"
	const database = "        database: {storageSize: 1Gi}\n"
	tests := []struct {
		name, props string
		present     []string
		text        []string // text the whole output must hold
	}{
		{"volume in service mode", "        image: reg/orders:1\n" + volume,
			[]string{"Deployment", "Service", "ServiceAccount", "PersistentVolumeClaim"}, []string{"claimName: orders-data", "mountPath: /data"}},
		{"volume in worker mode", "        mode: worker\n        image: reg/orders:1\n" + volume,
			[]string{"Deployment", "ServiceAccount", "PersistentVolumeClaim"}, []string{"claimName: orders-data", "mountPath: /data"}},
		{"volume in job mode", "        mode: job\n        image: reg/orders:1\n" + volume,
			[]string{"Job", "PersistentVolumeClaim"}, []string{"claimName: orders-data", "mountPath: /data"}},
		{"database", "        image: reg/orders:1\n" + database,
			[]string{"Deployment", "Service", "ServiceAccount", "Cluster"}, []string{"name: DATABASE_URI", "name: orders-db-app", "key: uri"}},
		{"database in job mode", "        mode: job\n        image: reg/orders:1\n" + database,
			[]string{"Job", "Cluster"}, []string{"name: DATABASE_URI", "name: orders-db-app"}},
		{"exposure", "        image: reg/orders:1\n        exposure: {hostnames: [orders.example.org]}\n",
			[]string{"Deployment", "Service", "ServiceAccount", "Ingress"}, []string{"host: orders.example.org", "name: orders-app"}},
		{"volume backup", "        image: reg/orders:1\n        volume: {size: 1Gi, mountPath: /data, backup: {schedule: \"0 3 * * *\", repository: restic-secret}}\n",
			[]string{"Deployment", "Service", "ServiceAccount", "PersistentVolumeClaim", "ReplicationSource"}, []string{"sourcePVC: orders-data", "repository: restic-secret"}},
		{"database backup", "        image: reg/orders:1\n" + `        database:
          storageSize: 1Gi
          backup:
            objectStore:
              destinationPath: s3://bkt/orders
              s3Credentials:
                accessKeyId: {name: s3, key: id}
                secretAccessKey: {name: s3, key: secret}
            schedules:
              - {name: daily, schedule: "0 0 3 * * *"}
`, []string{"Deployment", "Service", "ServiceAccount", "Cluster", "ObjectStore", "ScheduledBackup"}, []string{"name: orders-db-daily", "method: plugin"}},
		{"monitoring in service mode", "        image: reg/orders:1\n        monitoring: {path: /metrics}\n",
			[]string{"Deployment", "Service", "ServiceAccount", "VMServiceScrape"}, nil},
		// A worker and a job write no container port, so only a number is a usable endpoint: the
		// model writes it as portNumber.
		{"monitoring in worker mode", "        mode: worker\n        image: reg/orders:1\n        monitoring: {port: 9090}\n",
			[]string{"Deployment", "ServiceAccount", "VMPodScrape"}, []string{"portNumber: 9090"}},
		{"monitoring in job mode", "        mode: job\n        image: reg/orders:1\n        monitoring: {port: 9090}\n",
			[]string{"Job", "VMPodScrape"}, []string{"portNumber: 9090"}},
		{"monitoring rules", "        image: reg/orders:1\n        monitoring:\n          rules:\n            - name: availability\n              rules:\n                - {alert: Down, expr: \"up == 0\", for: 5m}\n",
			[]string{"Deployment", "Service", "ServiceAccount", "VMServiceScrape", "VMRule"}, []string{"alert: Down"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			docs := mustBuildWorkload(t, workloadComponent("orders", tt.props))
			absent := kindsWithout(allWorkloadKinds, tt.present...)
			// A database is a Cluster, and a database backup adds its object store and schedule; a
			// monitoring input without rules writes no rule.
			requireKinds(t, docs, tt.present, absent)
			all := asText(t, docs)
			for _, want := range tt.text {
				if !strings.Contains(all, want) {
					t.Errorf("the output does not hold %q:\n%s", want, all)
				}
			}
		})
	}
}

// TestShippedWorkloadMonitoringPortInServiceMode: the monitoring port reaches the scrape object
// itself, as a name or as a number. The Service already writes the workload port, so the
// scrape's endpoint is read, not the whole output.
func TestShippedWorkloadMonitoringPortInServiceMode(t *testing.T) {
	tests := []struct {
		name, port string
		want       map[string]any
	}{
		{"default", "", map[string]any{"port": "http"}},
		{"name", ", port: metrics", map[string]any{"port": "metrics"}},
		// A number is a pod port, and the model declares only the workload's own `port` on the pod.
		{"number", ", port: 8080", map[string]any{"targetPort": 8080}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			docs := mustBuildWorkload(t, workloadComponent("orders", "        image: reg/orders:1\n        port: 8080\n        monitoring: {path: /m"+tt.port+"}\n"))
			scrapes := objectsOf(docs, "VMServiceScrape")
			if len(scrapes) != 1 {
				t.Fatalf("%d VMServiceScrape, want 1", len(scrapes))
			}
			endpoints, _ := scrapes[0]["spec"].(map[string]any)["endpoints"].([]any)
			if len(endpoints) != 1 {
				t.Fatalf("endpoints %v, want one", endpoints)
			}
			got := endpoints[0].(map[string]any)
			for k, v := range tt.want {
				if !reflect.DeepEqual(got[k], v) {
					t.Errorf("endpoint %s = %v, want %v (endpoint %v)", k, got[k], v, got)
				}
			}
			for _, k := range []string{"port", "targetPort"} {
				if _, want := tt.want[k]; !want && got[k] != nil {
					t.Errorf("endpoint holds %s = %v, want it absent (endpoint %v)", k, got[k], got)
				}
			}
		})
	}
}

// TestShippedWorkloadMonitoringNeedsAPortOutsideServiceMode pins the refusal: the schema cannot
// say that monitoring.port is required in worker and job mode, so the model reads it and the
// build names the site (go-kure/launcher#1737).
func TestShippedWorkloadMonitoringNeedsAPortOutsideServiceMode(t *testing.T) {
	for _, mode := range []string{"worker", "job"} {
		t.Run(mode, func(t *testing.T) {
			_, err := buildWorkload(t, workloadComponent("orders", "        mode: "+mode+"\n        image: reg/orders:1\n        monitoring: {path: /metrics}\n"))
			// The part index moves when a part is added before the scrape: the test then names the
			// new index, which is the point of pinning the whole text.
			requireRefused(t, err, "model: evaluate spec.parts[8].properties.podMetricsEndpoints[0]: no such key: port")
		})
	}
}

// TestShippedWorkloadMonitoringMatchesTheTrait guards the copy of the monitoring trait's property
// shape: with path and interval set, the model's scrape has the spec the trait writes on a
// webservice. A second row pins what the model writes when they are unset, which differs from
// the trait (it omits both and VictoriaMetrics' defaults apply).
func TestShippedWorkloadMonitoringMatchesTheTrait(t *testing.T) {
	trait := workloadAppHead + `    - name: orders-app
      type: webservice
      properties:
        image: reg/orders:1
        port: 8080
      traits:
        - type: monitoring
          properties: {path: /metrics, interval: 30s}
`
	traitOut, err := runKurel(t, "build", modelPackage(t, trait, nil), "--profile", workloadProfile)
	if err != nil {
		t.Fatalf("the trait build: %v", err)
	}
	traitScrapes := objectsOf(decodeObjects(t, traitOut), "VMServiceScrape")
	if len(traitScrapes) != 1 {
		t.Fatalf("the trait built %d VMServiceScrape, want 1", len(traitScrapes))
	}
	want := traitScrapes[0]["spec"].(map[string]any)

	t.Run("set", func(t *testing.T) {
		docs := mustBuildWorkload(t, workloadComponent("orders", "        image: reg/orders:1\n        monitoring: {path: /metrics, interval: 30s}\n"))
		got := objectsOf(docs, "VMServiceScrape")
		if len(got) != 1 {
			t.Fatalf("%d VMServiceScrape, want 1", len(got))
		}
		if !reflect.DeepEqual(got[0]["spec"], want) {
			t.Errorf("the model's scrape differs from the trait's:\nmodel: %s\ntrait: %s", asText(t, got[0]["spec"]), asText(t, want))
		}
	})
	t.Run("unset", func(t *testing.T) {
		docs := mustBuildWorkload(t, workloadComponent("orders", "        image: reg/orders:1\n        monitoring: {}\n"))
		got := objectsOf(docs, "VMServiceScrape")
		if len(got) != 1 {
			t.Fatalf("%d VMServiceScrape, want 1", len(got))
		}
		if !reflect.DeepEqual(got[0]["spec"], want) {
			t.Errorf("the model's defaults are /metrics and 30s, the values set above:\nmodel: %s\ntrait: %s", asText(t, got[0]["spec"]), asText(t, want))
		}
	})
}

// TestShippedWorkloadExample builds examples/workload-model, which has no models/ directory.
func TestShippedWorkloadExample(t *testing.T) {
	out, err := runKurel(t, "build", "../../../examples/workload-model", "--profile", "../../../examples/cluster-profiles/minimal.yaml")
	requireBuilt(t, out, err, "kind: Deployment", "kind: Job", "kind: Ingress", "kind: Cluster", "kind: ScheduledBackup",
		"kind: ReplicationSource", "kind: VMServiceScrape", "kind: VMPodScrape", "kind: VMRule", "kind: PersistentVolumeClaim")
}

// TestShippedWorkloadIsNotReplaced: an application's model of the shipped identity is refused,
// naming the shipped file.
func TestShippedWorkloadIsNotReplaced(t *testing.T) {
	model := `apiVersion: launcher.gokure.dev/v1alpha1
kind: Model
metadata:
  name: workload.launcher.gokure.dev
  version: v1
spec:
  celEnv: "1"
  outputs: {}
  parts:
    - id: cm
      type: configmap@v1
      properties:
        data: {k: v}
`
	dir := modelPackage(t, workloadAppHead+workloadComponent("orders", "        image: reg/orders:1\n"), map[string]string{"models/workload.yaml": model})
	_, err := runKurel(t, "build", dir, "--profile", workloadProfile)
	requireRefused(t, err, "workload.launcher.gokure.dev@v1.0.0", "shipped", "model workload.yaml")
}
