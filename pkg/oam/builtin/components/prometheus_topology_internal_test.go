package components

import (
	"go/ast"
	"maps"
	"os"
	"strings"
	"testing"
)

// TestPrometheusTopologyNodeZone_MatchVendoredSource holds the versions the
// prometheus kind documents for the node zone of topology sharding
// (go-kure/launcher#1019) to the operator's source: the minimum each monitor
// kind passes mergeAttachMetadataForTopology, and that function forcing node
// metadata at that minimum, only with topology sharding active and no pod
// topology labels. That the generator then writes attach_metadata only from
// that minimum on is a row of prometheusGateRows (generateK8SSDConfig).
func TestPrometheusTopologyNodeZone_MatchVendoredSource(t *testing.T) {
	src := loadVendoredPromOp(t)
	const (
		cfg = "pkg/prometheus/promcfg.go"
		cg  = "(*ConfigGenerator)."
	)

	want := map[string]string{
		cg + "generatePodMonitorConfig":     prometheusTopologyNodeZonePodMonitor,
		cg + "generateServiceMonitorConfig": prometheusTopologyNodeZoneServiceMonitor,
	}
	got := map[string]string{}
	for _, c := range src.callStringArgs(t, cfg, "mergeAttachMetadataForTopology", 1) {
		if prev, ok := got[c.fn]; ok {
			t.Errorf("%s calls mergeAttachMetadataForTopology twice, with %s and %s", c.fn, prev, c.arg)
		}
		got[c.fn] = c.arg
	}
	if !maps.Equal(got, want) {
		t.Errorf("mergeAttachMetadataForTopology is called with %v; the kind documents %v", got, want)
	}

	body := ""
	for _, decl := range src.files[cfg].Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && funcName(fd) == cg+"mergeAttachMetadataForTopology" {
			body = src.text(fd.Body)
		}
	}
	if body == "" {
		t.Fatalf("%s declares no %smergeAttachMetadataForTopology", cfg, cg)
	}
	for _, s := range []string{
		"if !cg.isTopologyShardingActive() { return amc }",
		"if cg.podTopologyLabelsSupported { return amc }",
		"if amc != nil && amc.node() { return amc }",
		"MinimumVersion: minimumVersion",
		"Node: new(true)",
	} {
		if !strings.Contains(body, s) {
			t.Errorf("mergeAttachMetadataForTopology no longer reads %q: re-read the fallback the kind documents (go-kure/launcher#1019)", s)
		}
	}

	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	doc := strings.Join(strings.Fields(string(readme)), " ")
	if s := "every PodMonitor from Prometheus " + prometheusTopologyNodeZonePodMonitor + " and every ServiceMonitor from " + prometheusTopologyNodeZoneServiceMonitor; !strings.Contains(doc, s) {
		t.Errorf("README.md does not state %q", s)
	}
	if s := "runs Prometheus " + prometheusTopologyNodeZoneServiceMonitor + " or later"; !strings.Contains(doc, s) {
		t.Errorf("README.md does not state %q", s)
	}
}
