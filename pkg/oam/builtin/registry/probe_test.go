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

// Fault B: the probe states a fill the library does not make. A statefulset fills
// no restartPolicy, so a row stating one reports a fill that does not happen.
func TestProbes_FaultB_AFillThatDoesNotHappen(t *testing.T) {
	p := probeNamed(t, "statefulset")
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
//
// The raw deployment, statefulset, daemonset, job and cronjob fill nothing
// (go-kure/launcher#1629), so the probe of the webservice stands in as the document
// with a fill to drop: the protocol its rule fills on the one port.
func TestProbes_FaultB_AnEntryNoRowLists(t *testing.T) {
	p := probeNamed(t, "webservice")
	p.Fills = slices.DeleteFunc(slices.Clone(p.Fills), func(f probe.Fill) bool { return f.Path == "ports[0].protocol" })
	got := strings.Join(probe.CheckReportedFillsAreReal(newTransformer(), []probe.Probe{p}), "\n")
	for _, want := range []string{"ports[0].protocol", "no Fill of the probe lists", "add a Fill row", "fix the report"} {
		if !strings.Contains(got, want) {
			t.Errorf("finding lacks %q: %s", want, got)
		}
	}
}

// No shipped probe lists a default as left to the cluster any more (the raw
// cronjob, which did, fills nothing: go-kure/launcher#1629), so the stale row is
// shown on the worker's `replicas`, a default the handler applies.
func TestProbes_FaultA_AStaleLeftToClusterRow(t *testing.T) {
	p := probeNamed(t, "worker")
	p.LeftToCluster = map[string]string{"replicas": "stale"}
	got := strings.Join(probe.CheckDeclaredDefaults(newTransformer(), []probe.Probe{p}), "\n")
	if !strings.Contains(got, "remove the entry from LeftToCluster") {
		t.Fatalf("want the stale-row finding, got %q", got)
	}
}

// Check A names a probe it could not check: a refusal the probe does not state is a
// finding and never a skip.
func TestProbes_FaultA_AProbeItCouldNotCheck(t *testing.T) {
	broken := probeNamed(t, "worker")
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
	p := probeNamed(t, "deployment with an ingress")
	fills := slices.Clone(p.Fills)
	i := slices.IndexFunc(fills, func(f probe.Fill) bool { return f.Trait == "ingress" && f.Path == "rules[0].paths[0].path" })
	fills[i].Path = "rules[-1].paths[0].path"
	p.Fills = fills
	got := strings.Join(probe.CheckReportedFillsAreReal(newTransformer(), []probe.Probe{p}), "\n")
	if !strings.Contains(got, "no element -1") {
		t.Fatalf("want a diagnostic naming the index, got %q", got)
	}
}

// Fault B, the third one: a row that states a value and no source never has its
// value compared, so it is a finding and not a silent pass.
func TestProbes_FaultB_AValueWithNoSource(t *testing.T) {
	p := probeNamed(t, "worker")
	fills := slices.Clone(p.Fills)
	i := slices.IndexFunc(fills, func(f probe.Fill) bool { return f.Path == "resources.limits.cpu" })
	fills[i].ValueWithoutPolicy = "999m"
	p.Fills = fills
	got := strings.Join(probe.CheckReportedFillsAreReal(newTransformer(), []probe.Probe{p}), "\n")
	if !strings.Contains(got, "states a value and no source") {
		t.Fatalf("want the value-without-source finding, got %q", got)
	}
}

// An explicit null on the way to a fill reads as omitted when the fill is authored,
// as the pipeline reads it: the probe still renders and the fill is still compared.
func TestProbes_FaultB_ANullOnTheWayToAFill(t *testing.T) {
	p := probeNamed(t, "worker")
	p.Components[0].Properties["resources"] = map[string]any{"requests": nil}
	for _, f := range probe.CheckReportedFillsAreReal(newTransformer(), []probe.Probe{p}) {
		if strings.Contains(f, "not under an object") {
			t.Errorf("a null on the way read as an object: %s", f)
		}
	}
}

// The lowered types speak at the place of a member. A row naming the wrong member is
// found in both directions: the report's entry is unlisted and the row's own place
// holds none.
func TestProbes_FaultB_AWrongMember(t *testing.T) {
	p := probeNamed(t, "worker")
	fills := slices.Clone(p.Fills)
	i := slices.IndexFunc(fills, func(f probe.Fill) bool { return f.Path == "replicas" })
	fills[i].Member = "statefulset"
	p.Fills = fills
	got := strings.Join(probe.CheckReportedFillsAreReal(newTransformer(), []probe.Probe{p}), "\n")
	for _, want := range []string{"app/statefulset replicas", "app/role-deployment replicas"} {
		if !strings.Contains(got, want) {
			t.Errorf("finding lacks %q: %s", want, got)
		}
	}
}

// Check A reads a declared default of a lowered type at the places its key map
// names, and a gap the probe lists must still be a gap.
func TestProbes_FaultA_LoweredTypes(t *testing.T) {
	p := probeNamed(t, "worker")
	p.Unreported = nil
	if got := strings.Join(probe.CheckDeclaredDefaults(newTransformer(), []probe.Probe{p}), "\n"); !strings.Contains(got, "topologySpread") {
		t.Errorf("an unlisted gap: want a finding naming topologySpread, got %q", got)
	}
	// The raw statefulset declares no default at all now (go-kure/launcher#1629),
	// so the stale row sits on the worker's `replicas`, which the report holds.
	q := probeNamed(t, "worker")
	q.Unreported = map[string]string{"replicas": "stale"}
	if got := strings.Join(probe.CheckDeclaredDefaults(newTransformer(), []probe.Probe{q}), "\n"); !strings.Contains(got, "remove the entry from Unreported") {
		t.Errorf("a stale Unreported row: want the finding, got %q", got)
	}
}

// Provenance records leaves, so a default that lands under an object is reported at
// a path below the place the key map names, and check A must find it there. The worker
// schema declares defaults under affinity, so the probe reaches them and every finding
// must be empty. It catches a check that reads the place as an exact path; it does not
// catch one that skips the defaults, which TestProbes_FaultA_LoweredTypes' gap rows cover.
func TestProbes_FaultA_ADefaultReportedBelowItsPlace(t *testing.T) {
	p := probeNamed(t, "worker")
	p.Components[0].Properties["affinity"] = map[string]any{"enablePodAntiAffinity": true}
	if got := probe.CheckDeclaredDefaults(newTransformer(), []probe.Probe{p}); len(got) != 0 {
		t.Errorf("want no finding, got %v", got)
	}
}

// A row of LeftToCluster or Unreported that names no declared default is a finding, so
// a misspelled path or a default removed from the schema cannot stay behind.
func TestProbes_FaultA_AnEntryNamingNoDefault(t *testing.T) {
	p := probeNamed(t, "postgresql")
	p.Unreported = map[string]string{"provder": "misspelled"}
	got := strings.Join(probe.CheckDeclaredDefaults(newTransformer(), []probe.Probe{p}), "\n")
	if !strings.Contains(got, `Unreported lists "provder"`) {
		t.Errorf("want the finding naming provder, got %q", got)
	}
	q := probeNamed(t, "worker")
	q.LeftToCluster = map[string]string{"concurrencyPolcy": "misspelled"}
	if got := strings.Join(probe.CheckDeclaredDefaults(newTransformer(), []probe.Probe{q}), "\n"); !strings.Contains(got, `LeftToCluster lists "concurrencyPolcy"`) {
		t.Errorf("want the finding naming concurrencyPolcy, got %q", got)
	}
}

// A trait's fills are named by the trait: an entry no row lists is found, and a row
// that names the wrong trait is found in both directions.
func TestProbes_FaultB_ATraitEntryNoRowLists(t *testing.T) {
	p := probeNamed(t, "deployment with a certificate")
	p.Fills = slices.DeleteFunc(slices.Clone(p.Fills), func(f probe.Fill) bool { return f.Trait == "certificate" && f.Path == "duration" })
	got := strings.Join(probe.CheckReportedFillsAreReal(newTransformer(), []probe.Probe{p}), "\n")
	if !strings.Contains(got, "app trait certificate[0] duration") || !strings.Contains(got, "no Fill of the probe lists") {
		t.Errorf("want the unlisted trait entry named, got %q", got)
	}
}

func TestProbes_FaultB_AWrongTrait(t *testing.T) {
	p := probeNamed(t, "deployment with a certificate")
	fills := slices.Clone(p.Fills)
	i := slices.IndexFunc(fills, func(f probe.Fill) bool { return f.Trait == "certificate" && f.Path == "duration" })
	fills[i].Trait = "external-secret"
	p.Fills = fills
	got := strings.Join(probe.CheckReportedFillsAreReal(newTransformer(), []probe.Probe{p}), "\n")
	for _, want := range []string{"app trait external-secret[0] duration", "app trait certificate[0] duration"} {
		if !strings.Contains(got, want) {
			t.Errorf("finding lacks %q: %s", want, got)
		}
	}
}

// Two traits of one type are two keys: a fill of the second with no row of its own
// is found and is never covered by the first trait's row.
func TestProbes_FaultB_ASecondTraitOfOneType(t *testing.T) {
	p := probeNamed(t, "deployment with an ingress")
	second := slices.Clone(p.Components[0].Traits)
	second[0].Properties = map[string]any{
		"servicePort": 8080,
		"name":        "second",
		"rules":       []any{map[string]any{"host": "b.example.com", "paths": []any{map[string]any{}}}},
	}
	p.Components[0].Traits = append(p.Components[0].Traits, second...)
	got := strings.Join(probe.CheckReportedFillsAreReal(newTransformer(), []probe.Probe{p}), "\n")
	if !strings.Contains(got, "trait ingress[1] rules[0].paths[0].path") || !strings.Contains(got, "no Fill of the probe lists") {
		t.Errorf("want the second trait's entry named as unlisted, got %q", got)
	}
}

// Authoring reaches the trait's properties: an Other value equal to the default changes
// no object, which is the finding that shows the row proves nothing.
func TestProbes_FaultB_ATraitOtherThatChangesNothing(t *testing.T) {
	p := probeNamed(t, "deployment with a claim")
	fills := slices.Clone(p.Fills)
	i := slices.IndexFunc(fills, func(f probe.Fill) bool { return f.Trait == "pvc" && f.Path == "accessModes" })
	fills[i].Other = fills[i].ValueWithPolicy
	p.Fills = fills
	got := strings.Join(probe.CheckReportedFillsAreReal(newTransformer(), []probe.Probe{p}), "\n")
	if !strings.Contains(got, "leaves the generated objects unchanged") {
		t.Errorf("want the unchanged-objects finding, got %q", got)
	}
}

// workerWithAScaler is the document that exercises NotAuthorable. The raw
// deployment, statefulset and daemonset declare no replicas default
// (go-kure/launcher#1629), so the shipped scaler probe, which is a deployment,
// has no refusal to list. The reshaped worker still declares one, and a scaler
// on it refuses an authored replicas with the same text.
func workerWithAScaler(t *testing.T) probe.Probe {
	t.Helper()
	p := probeNamed(t, "worker")
	p.Name = "worker with a scaler"
	p.Components[0].Traits = []oam.Trait{{Type: "scaler", Properties: map[string]any{"minReplicas": 2, "maxReplicas": 5}}}
	// The scaler owns the replica count: the worker reports none, and the
	// trait reports its own fills, on the member. Its minimum of two makes the
	// topology-spread trait write constraints with no policy too, so the gap the
	// shipped worker probe lists is none here.
	p.Unreported = nil
	p.Fills = slices.DeleteFunc(slices.Clone(p.Fills), func(f probe.Fill) bool {
		return f.Trait == "topology-spread" || (f.Trait == "" && f.Path == "replicas")
	})
	p.Fills = append(p.Fills,
		probe.Fill{Component: "app", Member: "role-deployment", Trait: "topology-spread", Path: "topologySpreadConstraints",
			WithPolicy: oam.SourceBuiltin, WithoutPolicy: oam.SourceBuiltin},
		probe.Fill{Component: "app", Member: "role-deployment", Trait: "scaler", Path: "cpuUtilization", WithPolicy: oam.SourceBuiltin, WithoutPolicy: oam.SourceBuiltin,
			ValueWithPolicy: 80, ValueWithoutPolicy: 80, Other: 70},
		probe.Fill{Component: "app", Member: "role-deployment", Trait: "scaler", Path: "hpaName", WithPolicy: oam.SourceBuiltin, WithoutPolicy: oam.SourceBuiltin})
	p.NotAuthorable = map[string]string{"replicas": "owns the replica count"}
	return p
}

// A default the document's own rule refuses to have written is listed in NotAuthorable,
// and the entry must stay true in both directions.
func TestProbes_FaultA_NotAuthorable(t *testing.T) {
	if got := probe.CheckDeclaredDefaults(newTransformer(), []probe.Probe{workerWithAScaler(t)}); len(got) != 0 {
		t.Fatalf("test bug: the control probe is not clean: %v", got)
	}
	p := workerWithAScaler(t)
	p.NotAuthorable = nil
	if got := strings.Join(probe.CheckDeclaredDefaults(newTransformer(), []probe.Probe{p}), "\n"); !strings.Contains(got, "NotAuthorable") {
		t.Errorf("an unlisted refusal: want a finding naming NotAuthorable, got %q", got)
	}
	e := workerWithAScaler(t)
	e.NotAuthorable = map[string]string{"replicas": ""}
	if got := strings.Join(probe.CheckDeclaredDefaults(newTransformer(), []probe.Probe{e}), "\n"); !strings.Contains(got, "no text the refusal must contain") {
		t.Errorf("an empty text: want the finding, got %q", got)
	}
	w := workerWithAScaler(t)
	w.NotAuthorable = map[string]string{"replicas": "some other reason"}
	if got := strings.Join(probe.CheckDeclaredDefaults(newTransformer(), []probe.Probe{w}), "\n"); !strings.Contains(got, "does not contain the stated") {
		t.Errorf("a refusal for another reason: want the finding, got %q", got)
	}
	q := probeNamed(t, "worker")
	q.NotAuthorable = map[string]string{"replicas": "stale"}
	if got := strings.Join(probe.CheckDeclaredDefaults(newTransformer(), []probe.Probe{q}), "\n"); !strings.Contains(got, "remove the entry from NotAuthorable") {
		t.Errorf("a stale row: want the finding, got %q", got)
	}
}
