package components_test

import (
	"fmt"
	"maps"
	"reflect"
	"sort"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// aliasingPVCVolume is a volume of type pvc, which makes each kind emit a
// PersistentVolumeClaim alongside its workload. That is the second object in one
// Generate whose labels could alias the workload's, and it is the only path that
// reaches BuildPVC's clone.
//
// The access mode is ReadWriteMany because the kinds below also set replicas=3,
// and a non-RWX claim on a multi-replica workload is rejected during Generate.
var aliasingPVCVolume = []any{
	map[string]any{
		"name":        "data",
		"type":        "pvc",
		"mountPath":   "/data",
		"size":        "1Gi",
		"accessModes": []any{"ReadWriteMany"},
	},
}

// labelAliasingProps adds, per kind, whatever that kind needs to reach every
// label map it can produce: replicas >= 3 so both topology spread constraints
// are built, pod anti-affinity so an affinity selector exists, and a pvc volume
// so a second labelled object is generated. daemonset and cronjob take neither
// replicas nor affinity, so they contribute their object, template and claim
// label maps only.
var labelAliasingProps = map[string]map[string]any{
	"webservice": {"replicas": 3, "affinity": map[string]any{"enablePodAntiAffinity": true}, "volumes": aliasingPVCVolume},
	"worker":     {"replicas": 3, "affinity": map[string]any{"enablePodAntiAffinity": true}, "volumes": aliasingPVCVolume},
	"statefulset": {
		"replicas": 3,
		"affinity": map[string]any{"enablePodAntiAffinity": true},
		"volumes":  aliasingPVCVolume,
		// A claim template is a per-object metadata position only this kind
		// has, so only this kind can carry it into the collection.
		"volumeClaimTemplates": []any{
			map[string]any{"name": "state", "size": "1Gi", "mountPath": "/state"},
		},
	},
	"daemonset": {"volumes": aliasingPVCVolume},
	"cronjob":   {"volumes": aliasingPVCVolume},
}

// labelMap is one caller-reachable label map, named by where it lives so a
// failure says which two fields alias.
type labelMap struct {
	where string
	m     map[string]string
}

// collectLabelMaps returns every label map reachable from a generated object
// list: each object's own metadata labels, the pod (and job) template labels,
// the workload's own immutable spec.selector, the Service selector, a
// StatefulSet's volume claim template labels, and the MatchLabels of every
// topology spread and pod affinity/anti-affinity selector. Nil maps are skipped
// — an absent map cannot alias.
//
// Every position is prefixed with "<Kind>/<name>" because one Generate emits
// several objects and two of them can share a kind (two Services, a workload and
// its claim). Without the name, findLabelMap would match the first object of that
// kind and could compare the wrong selector.
//
// The pod affinity arm has no producer in this package today — buildAffinity in
// common.go only ever fills PodAntiAffinity and NodeAffinity. It is collected
// anyway: a scheduling selector added there later is exactly the shape of the
// defect this file guards, and a collector that does not look at it would let
// that regression through silently.
func collectLabelMaps(objects []*client.Object) []labelMap {
	var out []labelMap
	add := func(where string, m map[string]string) {
		if m != nil {
			out = append(out, labelMap{where: where, m: m})
		}
	}
	addAffinityTerms := func(prefix string, required []corev1.PodAffinityTerm, preferred []corev1.WeightedPodAffinityTerm) {
		for i, term := range required {
			if term.LabelSelector != nil {
				add(fmt.Sprintf("%s.required[%d].labelSelector", prefix, i), term.LabelSelector.MatchLabels)
			}
		}
		for i, wterm := range preferred {
			if wterm.PodAffinityTerm.LabelSelector != nil {
				add(fmt.Sprintf("%s.preferred[%d].labelSelector", prefix, i), wterm.PodAffinityTerm.LabelSelector.MatchLabels)
			}
		}
	}
	addPodSpec := func(prefix string, spec *corev1.PodSpec) {
		for i, tsc := range spec.TopologySpreadConstraints {
			if tsc.LabelSelector != nil {
				add(fmt.Sprintf("%s.topologySpreadConstraints[%d].labelSelector", prefix, i), tsc.LabelSelector.MatchLabels)
			}
		}
		if spec.Affinity == nil {
			return
		}
		if pa := spec.Affinity.PodAffinity; pa != nil {
			addAffinityTerms(prefix+".podAffinity",
				pa.RequiredDuringSchedulingIgnoredDuringExecution,
				pa.PreferredDuringSchedulingIgnoredDuringExecution)
		}
		if paa := spec.Affinity.PodAntiAffinity; paa != nil {
			addAffinityTerms(prefix+".podAntiAffinity",
				paa.RequiredDuringSchedulingIgnoredDuringExecution,
				paa.PreferredDuringSchedulingIgnoredDuringExecution)
		}
	}
	addSpecSelector := func(prefix string, sel *metav1.LabelSelector) {
		if sel != nil {
			add(prefix+".spec.selector.matchLabels", sel.MatchLabels)
		}
	}

	for _, o := range objects {
		obj := *o
		prefix := reflect.TypeOf(obj).Elem().Name() + "/" + obj.GetName()
		add(prefix+".metadata.labels", obj.GetLabels())
		switch t := obj.(type) {
		case *appsv1.Deployment:
			addSpecSelector(prefix, t.Spec.Selector)
			add(prefix+".spec.template.labels", t.Spec.Template.Labels)
			addPodSpec(prefix+".spec.template.spec", &t.Spec.Template.Spec)
		case *appsv1.StatefulSet:
			addSpecSelector(prefix, t.Spec.Selector)
			add(prefix+".spec.template.labels", t.Spec.Template.Labels)
			addPodSpec(prefix+".spec.template.spec", &t.Spec.Template.Spec)
			for i := range t.Spec.VolumeClaimTemplates {
				vct := &t.Spec.VolumeClaimTemplates[i]
				add(fmt.Sprintf("%s.spec.volumeClaimTemplates[%d].metadata.labels", prefix, i), vct.Labels)
				addSpecSelector(fmt.Sprintf("%s.spec.volumeClaimTemplates[%d]", prefix, i), vct.Spec.Selector)
			}
		case *appsv1.DaemonSet:
			addSpecSelector(prefix, t.Spec.Selector)
			add(prefix+".spec.template.labels", t.Spec.Template.Labels)
			addPodSpec(prefix+".spec.template.spec", &t.Spec.Template.Spec)
		case *batchv1.CronJob:
			add(prefix+".spec.jobTemplate.labels", t.Spec.JobTemplate.Labels)
			addSpecSelector(prefix+".spec.jobTemplate", t.Spec.JobTemplate.Spec.Selector)
			add(prefix+".spec.jobTemplate.spec.template.labels", t.Spec.JobTemplate.Spec.Template.Labels)
			addPodSpec(prefix+".spec.jobTemplate.spec.template.spec", &t.Spec.JobTemplate.Spec.Template.Spec)
		case *corev1.Service:
			add(prefix+".spec.selector", t.Spec.Selector)
		case *corev1.PersistentVolumeClaim:
			addSpecSelector(prefix, t.Spec.Selector)
		}
	}
	return out
}

// TestCollectLabelMaps_ReachesEveryGuardedPosition keeps the two tests below
// honest. Both of them assert over whatever collectLabelMaps returns, so a
// fixture that quietly stopped generating a claim, a selector or a second object
// would still pass while guarding nothing. This states the positions the fixture
// must reach, per kind, and fails loudly when one disappears.
func TestCollectLabelMaps_ReachesEveryGuardedPosition(t *testing.T) {
	// Every kind emits its workload, a pod template, a ServiceAccount and a
	// PersistentVolumeClaim from the pvc volume. Only webservice and worker
	// build topology spread constraints; only the three that take replicas
	// build an anti-affinity selector. A StatefulSet claim template carries no
	// labels today, so there is no position to require — the collector reads
	// it anyway, so labelling one later lands in these tests rather than
	// slipping past them.
	want := map[string][]string{
		"webservice": {
			"Deployment/app.metadata.labels",
			"Deployment/app.spec.selector.matchLabels",
			"Deployment/app.spec.template.labels",
			"Deployment/app.spec.template.spec.topologySpreadConstraints[0].labelSelector",
			"Deployment/app.spec.template.spec.topologySpreadConstraints[1].labelSelector",
			"Deployment/app.spec.template.spec.podAntiAffinity.preferred[0].labelSelector",
			"Service/app.metadata.labels",
			"Service/app.spec.selector",
			"ServiceAccount/app.metadata.labels",
			"PersistentVolumeClaim/app-data.metadata.labels",
		},
		"worker": {
			"Deployment/app.metadata.labels",
			"Deployment/app.spec.selector.matchLabels",
			"Deployment/app.spec.template.labels",
			"Deployment/app.spec.template.spec.topologySpreadConstraints[0].labelSelector",
			"Deployment/app.spec.template.spec.topologySpreadConstraints[1].labelSelector",
			"Deployment/app.spec.template.spec.podAntiAffinity.preferred[0].labelSelector",
			"ServiceAccount/app.metadata.labels",
			"PersistentVolumeClaim/app-data.metadata.labels",
		},
		"statefulset": {
			"StatefulSet/app.metadata.labels",
			"StatefulSet/app.spec.selector.matchLabels",
			"StatefulSet/app.spec.template.labels",
			"StatefulSet/app.spec.template.spec.podAntiAffinity.preferred[0].labelSelector",
			"Service/app.metadata.labels",
			"Service/app.spec.selector",
			"ServiceAccount/app.metadata.labels",
			"PersistentVolumeClaim/app-data.metadata.labels",
		},
		"daemonset": {
			"DaemonSet/app.metadata.labels",
			"DaemonSet/app.spec.selector.matchLabels",
			"DaemonSet/app.spec.template.labels",
			"ServiceAccount/app.metadata.labels",
			"PersistentVolumeClaim/app-data.metadata.labels",
		},
		"cronjob": {
			"CronJob/app.metadata.labels",
			"CronJob/app.spec.jobTemplate.labels",
			"CronJob/app.spec.jobTemplate.spec.template.labels",
			"ServiceAccount/app.metadata.labels",
			"PersistentVolumeClaim/app-data.metadata.labels",
		},
	}

	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			objects := generateKind(t, k.handler, k.name, withProps(k.props, labelAliasingProps[k.name]))

			have := make(map[string]bool)
			for _, lm := range collectLabelMaps(objects) {
				have[lm.where] = true
			}
			for _, w := range want[k.name] {
				if !have[w] {
					t.Errorf("collectLabelMaps did not reach %s; it reached %v", w, sortedKeys(have))
				}
			}

			// The claim template contributes no label map, so nothing above
			// can prove the fixture still produces one. Assert it directly.
			if k.name == "statefulset" {
				sts, ok := (*objects[0]).(*appsv1.StatefulSet)
				if !ok {
					t.Fatalf("first object is %T, want *appsv1.StatefulSet", *objects[0])
				}
				if len(sts.Spec.VolumeClaimTemplates) != 1 {
					t.Errorf("got %d volume claim templates, want 1 — the claim-template arm of collectLabelMaps is unreached", len(sts.Spec.VolumeClaimTemplates))
				}
			}
		})
	}
}

// sortedKeys renders a position set deterministically for a failure message.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestWorkloadKinds_GeneratedLabelMapsAreNotShared is the regression for the
// aliasing every kind carried: one map[string]string{"app": name} was built per
// create<Kind> and then assigned to the object's metadata labels, the pod
// template's labels, the topology spread constraint's labelSelector and the pod
// anti-affinity term's labelSelector, with nothing copying it in between. All
// four were one map.
//
// It is checked by map identity rather than by content, because content cannot
// distinguish "two maps that happen to be equal" from "one map read twice", and
// equal-but-separate is exactly what correct output looks like here.
func TestWorkloadKinds_GeneratedLabelMapsAreNotShared(t *testing.T) {
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			objects := generateKind(t, k.handler, k.name, withProps(k.props, labelAliasingProps[k.name]))

			found := collectLabelMaps(objects)
			if len(found) < 2 {
				t.Fatalf("collected %d label maps, want at least 2 — the fixture reaches nothing worth checking", len(found))
			}
			seen := make(map[uintptr]string, len(found))
			for _, lm := range found {
				ptr := reflect.ValueOf(lm.m).Pointer()
				if first, dup := seen[ptr]; dup {
					t.Errorf("%s and %s are the same map: editing one edits the other", first, lm.where)
					continue
				}
				seen[ptr] = lm.where
			}
		})
	}
}

// TestWorkloadKinds_StampingPodTemplateLabelsLeavesSelectorsAlone is the same
// defect in the shape a caller meets it. Adding a label to a generated pod
// template is ordinary — it is how a consumer stamps ownership, environment or
// version metadata onto the workloads it emits — and it must not reach the
// scheduling selectors. It did: a topology spread constraint's labelSelector
// defines the pod set over which skew is computed, so a version label leaking
// into it makes a rollout spread each version separately instead of spreading
// the workload.
func TestWorkloadKinds_StampingPodTemplateLabelsLeavesSelectorsAlone(t *testing.T) {
	const stamped = "example.test/version"

	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			objects := generateKind(t, k.handler, k.name, withProps(k.props, labelAliasingProps[k.name]))

			// Snapshot every selector before the caller touches anything, so
			// the assertion compares against what was generated rather than
			// against an assumed label set.
			var selectors []labelMap
			for _, lm := range collectLabelMaps(objects) {
				if isSelector(lm.where) {
					selectors = append(selectors, labelMap{where: lm.where, m: maps.Clone(lm.m)})
				}
			}
			// Every kind but cronjob owns at least its workload's
			// spec.selector; a cronjob's job template has none, so it
			// contributes label maps only.
			if k.name != "cronjob" && len(selectors) == 0 {
				t.Fatalf("%s generated no selector to check", k.name)
			}

			stampTemplateLabels(t, objects, stamped, "1.0.0")

			for _, want := range selectors {
				got := findLabelMap(t, objects, want.where)
				if !reflect.DeepEqual(got, want.m) {
					t.Errorf("%s changed when the pod template labels were stamped:\ngot  %v\nwant %v", want.where, sortedPairs(got), sortedPairs(want.m))
				}
			}
		})
	}
}

// isSelector reports whether a collected label map is a selector rather than a
// metadata label set. Only selectors must be immune to a caller's stamp; the
// pod template's own labels are precisely what the caller is editing.
//
// Matching is on the full suffix collectLabelMaps writes, so no metadata
// position can be read as a selector: every metadata map it adds ends in
// ".labels", and none of these suffixes is a suffix of that.
func isSelector(where string) bool {
	for _, suffix := range []string{".labelSelector", ".spec.selector", ".spec.selector.matchLabels"} {
		if strings.HasSuffix(where, suffix) {
			return true
		}
	}
	return false
}

// findLabelMap re-collects and returns the map at the given position, so the
// comparison reads the live object rather than a stale reference.
func findLabelMap(t *testing.T, objects []*client.Object, where string) map[string]string {
	t.Helper()
	for _, lm := range collectLabelMaps(objects) {
		if lm.where == where {
			return lm.m
		}
	}
	t.Fatalf("label map %q disappeared from the generated objects", where)
	return nil
}

// stampTemplateLabels does what a downstream consumer does: merge its own
// labels into every generated pod template, in place.
func stampTemplateLabels(t *testing.T, objects []*client.Object, key, value string) {
	t.Helper()
	stamped := 0
	merge := func(target *map[string]string) {
		if *target == nil {
			*target = map[string]string{}
		}
		(*target)[key] = value
		stamped++
	}
	for _, o := range objects {
		switch obj := (*o).(type) {
		case *appsv1.Deployment:
			merge(&obj.Spec.Template.Labels)
		case *appsv1.StatefulSet:
			merge(&obj.Spec.Template.Labels)
		case *appsv1.DaemonSet:
			merge(&obj.Spec.Template.Labels)
		case *batchv1.CronJob:
			merge(&obj.Spec.JobTemplate.Labels)
			merge(&obj.Spec.JobTemplate.Spec.Template.Labels)
		}
	}
	if stamped == 0 {
		t.Fatal("no pod template was stamped — the fixture generated no workload")
	}
}

// sortedPairs renders a label map deterministically for a failure message.
func sortedPairs(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// TestBuildTopologySpreadConstraints_SelectorsAreDistinct guards the narrower
// half of the fix inside buildTopologySpreadConstraints: the two constraints it
// returns for replicas>=3 used to share one *metav1.LabelSelector, so they could
// not be edited apart even once their MatchLabels stopped aliasing the caller's
// map.
func TestBuildTopologySpreadConstraints_SelectorsAreDistinct(t *testing.T) {
	objects := generateKind(t, workloadKinds[0].handler, workloadKinds[0].name,
		withProps(workloadKinds[0].props, labelAliasingProps[workloadKinds[0].name]))

	var tscs []corev1.TopologySpreadConstraint
	for _, o := range objects {
		if dep, ok := (*o).(*appsv1.Deployment); ok {
			tscs = dep.Spec.Template.Spec.TopologySpreadConstraints
		}
	}
	if len(tscs) != 2 {
		t.Fatalf("got %d topology spread constraints at replicas=3, want 2", len(tscs))
	}
	if tscs[0].LabelSelector == tscs[1].LabelSelector {
		t.Error("both topology spread constraints point at one *metav1.LabelSelector")
	}
	want := map[string]string{"app": "app"}
	for i, tsc := range tscs {
		if !reflect.DeepEqual(tsc.LabelSelector, &metav1.LabelSelector{MatchLabels: want}) {
			t.Errorf("constraint %d selector = %v, want %v", i, tsc.LabelSelector, want)
		}
	}
}
