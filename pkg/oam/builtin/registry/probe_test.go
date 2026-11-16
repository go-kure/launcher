package registry_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/probe"
)

// The probe package holds the report to what the library does, as observed in
// its probe documents (go-kure/launcher#1580). Each check runs clean here, and
// each has a test that shows the fault it exists to catch.

func TestProbes_ReportedFillsAreReal(t *testing.T) {
	for _, f := range probe.CheckReportedFillsAreReal(newTransformer(), probe.Probes()) {
		t.Error(f)
	}
}

func TestProbes_DeclaredDefaultsAreReported(t *testing.T) {
	for _, f := range probe.CheckDeclaredDefaults(newTransformer(), probe.Probes()) {
		t.Error(f)
	}
}

func TestProbes_ProbePolicySetsEveryDefault(t *testing.T) {
	for _, f := range probe.CheckProbePolicyCoversEveryDefault(probe.ProbePolicy()) {
		t.Error(f)
	}
}

// zeroed is the probe policy with one accessor returning nothing, which is what
// a Default* accessor added to oam.Policy and not to the probe policy looks like.
type zeroed struct{ oam.Policy }

func (zeroed) DefaultStorageSize() string { return "" }

func TestProbes_FaultE_AnAccessorTheProbePolicyDoesNotSet(t *testing.T) {
	got := probe.CheckProbePolicyCoversEveryDefault(zeroed{probe.ProbePolicy()})
	if len(got) != 1 || !strings.Contains(got[0], "DefaultStorageSize") {
		t.Fatalf("want one finding naming DefaultStorageSize, got %v", got)
	}
}

// probeNamed returns a copy of the probe called name from the shipped list.
func probeNamed(t *testing.T, name string) probe.Probe {
	t.Helper()
	i := slices.IndexFunc(probe.Probes(), func(p probe.Probe) bool { return p.Name == name })
	if i < 0 {
		t.Fatalf("no probe %q", name)
	}
	return probe.Probes()[i]
}

// Fault B: the probe states a fill the library does not make. A job that authors
// its restartPolicy is reported nothing, so a row stating one reports a fill that
// does not happen.
func TestProbes_FaultB_AFillThatDoesNotHappen(t *testing.T) {
	p := probeNamed(t, "job with a pod failure policy")
	p.Fills = append(slices.Clone(p.Fills), probe.Fill{
		Component: "app", Path: "restartPolicy",
		WithPolicy: oam.SourceBuiltin, WithoutPolicy: oam.SourceBuiltin, ValueWithPolicy: "OnFailure", ValueWithoutPolicy: "OnFailure",
	})
	got := probe.CheckReportedFillsAreReal(newTransformer(), []probe.Probe{p})
	if len(got) == 0 || !strings.Contains(strings.Join(got, "\n"), "restartPolicy") {
		t.Fatalf("want a finding naming restartPolicy, got %v", got)
	}
}

// Fault B, the other direction: an entry no probe row lists is named, with what to
// do about it.
func TestProbes_FaultB_AnEntryNoRowLists(t *testing.T) {
	p := probeNamed(t, "deployment")
	p.Fills = slices.DeleteFunc(slices.Clone(p.Fills), func(f probe.Fill) bool { return f.Path == "replicas" })
	got := strings.Join(probe.CheckReportedFillsAreReal(newTransformer(), []probe.Probe{p}), "\n")
	for _, want := range []string{"replicas", "no Fill of the probe lists", "add a Fill row", "fix the report"} {
		if !strings.Contains(got, want) {
			t.Errorf("finding lacks %q: %s", want, got)
		}
	}
}

// Fault A: a declared default the library applies and does not report. The
// stand-in is a probe whose component is a handler of the same name as the
// registry's, but the check is pointed at a transformer whose report is silent:
// here, the cronjob row naming concurrencyPolicy as left to the cluster is
// removed, so the declared default shows as applied differently.
func TestProbes_FaultA_ADeclaredDefaultTheHandlerDoesNotApply(t *testing.T) {
	p := probeNamed(t, "cronjob")
	p.LeftToCluster = nil
	got := strings.Join(probe.CheckDeclaredDefaults(newTransformer(), []probe.Probe{p}), "\n")
	if !strings.Contains(got, "concurrencyPolicy") {
		t.Fatalf("want a finding naming concurrencyPolicy, got %q", got)
	}
}

func TestProbes_FaultA_AStaleLeftToClusterRow(t *testing.T) {
	p := probeNamed(t, "job")
	p.LeftToCluster = map[string]string{"restartPolicy": "stale"}
	got := strings.Join(probe.CheckDeclaredDefaults(newTransformer(), []probe.Probe{p}), "\n")
	if !strings.Contains(got, "remove the entry from LeftToCluster") {
		t.Fatalf("want the stale-row finding, got %q", got)
	}
}

// Check A names a probe it could not check: a refusal the probe does not state is a
// finding and never a skip.
func TestProbes_FaultA_AProbeItCouldNotCheck(t *testing.T) {
	broken := probeNamed(t, "deployment")
	delete(broken.Components[0].Properties, "image")
	if got := strings.Join(probe.CheckDeclaredDefaults(newTransformer(), []probe.Probe{broken}), "\n"); !strings.Contains(got, "the probe does not say so") {
		t.Errorf("a refused probe: want a finding, got %q", got)
	}
}

// Fault B, the second one: a fill the library reports and never puts in an object.
// Authoring the value the library would have used gives equal objects whether or
// not it is applied, so a probe row also carries an Other value that must change
// them. A negative path index in a row is a diagnostic, not a panic.
func TestProbes_FaultB_ANegativeIndexIsADiagnostic(t *testing.T) {
	p := probeNamed(t, "deployment with an init container, a sidecar and ports")
	fills := slices.Clone(p.Fills)
	i := slices.IndexFunc(fills, func(f probe.Fill) bool { return f.Path == "ports[0].protocol" })
	fills[i].Path = "ports[-1].protocol"
	p.Fills = fills
	got := strings.Join(probe.CheckReportedFillsAreReal(newTransformer(), []probe.Probe{p}), "\n")
	if !strings.Contains(got, "no element -1") {
		t.Fatalf("want a diagnostic naming the index, got %q", got)
	}
}

// Fault B, the third one: a row that states a value and no source never has its
// value compared, so it is a finding and not a silent pass.
func TestProbes_FaultB_AValueWithNoSource(t *testing.T) {
	p := probeNamed(t, "deployment")
	fills := slices.Clone(p.Fills)
	i := slices.IndexFunc(fills, func(f probe.Fill) bool { return f.Path == "resources.limits.cpu" })
	fills[i].ValueWithoutPolicy = "999m"
	p.Fills = fills
	got := strings.Join(probe.CheckReportedFillsAreReal(newTransformer(), []probe.Probe{p}), "\n")
	if !strings.Contains(got, "states a value and no source") {
		t.Fatalf("want the value-without-source finding, got %q", got)
	}
}

// Check A reads an explicit null as omitted, as the pipeline does.
func TestProbes_FaultA_ANullReadsAsOmitted(t *testing.T) {
	p := probeNamed(t, "cronjob")
	p.LeftToCluster = nil
	p.Components[0].Properties["concurrencyPolicy"] = nil
	got := strings.Join(probe.CheckDeclaredDefaults(newTransformer(), []probe.Probe{p}), "\n")
	if !strings.Contains(got, "concurrencyPolicy") {
		t.Fatalf("want a finding naming concurrencyPolicy, got %q", got)
	}
}
