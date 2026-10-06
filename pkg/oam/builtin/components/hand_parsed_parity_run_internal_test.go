package components

import (
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// This file runs what TestHandParsedKinds_CoverEveryUpstreamField only
// classifies (go-kure/launcher#790): the parts of a hand-parsed kind's object
// no property authors, a Service port's fields, and a match expression at every
// label selector these kinds parse by hand.

var parityHandlers = map[string]oam.ComponentHandler{
	"deployment":            &DeploymentHandler{},
	"statefulset":           &StatefulsetHandler{},
	"daemonset":             &DaemonsetHandler{},
	"job":                   &JobHandler{},
	"cronjob":               &CronjobHandler{},
	"service":               &ServiceHandler{},
	"persistentvolumeclaim": &PersistentVolumeClaimHandler{},
}

// parityGenerate builds the component "app" of componentType from base and
// extra, as a caller that drives the handler directly, and returns its objects.
func parityGenerate(t *testing.T, componentType string, extra map[string]any) ([]*client.Object, error) {
	t.Helper()
	props := maps.Clone(refusedKeyBase[componentType])
	maps.Copy(props, extra)
	cfg, err := parityHandlers[componentType].ToApplicationConfig(&oam.Component{Name: "app", Type: componentType, Properties: props}, "ns")
	if err != nil {
		return nil, err
	}
	objects, err := cfg.Generate(stack.NewApplication("app", "ns", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return objects, nil
}

// parityObject returns the one object of type T among objects.
func parityObject[T client.Object](t *testing.T, objects []*client.Object) T {
	t.Helper()
	var found []T
	for _, obj := range objects {
		if typed, ok := (*obj).(T); ok {
			found = append(found, typed)
		}
	}
	if len(found) != 1 {
		t.Fatalf("got %d objects of type %T, want one", len(found), *new(T))
	}
	return found[0]
}

// parityPodTemplate returns the pod template of the workload object among
// objects, and for a CronJob its job template's metadata too.
func parityPodTemplate(t *testing.T, objects []*client.Object) (pod corev1.PodTemplateSpec, jobTemplate *metav1.ObjectMeta) {
	t.Helper()
	for _, obj := range objects {
		switch o := (*obj).(type) {
		case *appsv1.Deployment:
			return o.Spec.Template, nil
		case *appsv1.StatefulSet:
			return o.Spec.Template, nil
		case *appsv1.DaemonSet:
			return o.Spec.Template, nil
		case *batchv1.Job:
			return o.Spec.Template, nil
		case *batchv1.CronJob:
			return o.Spec.JobTemplate.Spec.Template, &o.Spec.JobTemplate.ObjectMeta
		}
	}
	t.Fatal("no workload object in the output")
	return corev1.PodTemplateSpec{}, nil
}

// TestHandParsedKinds_TemplateMetadataAndContainerName runs the three places of
// a workload kind's object that the parity walk classes as no property's: the
// pod template's metadata and the job template's (parityLevel), and the main
// container's name (parityOtherShape, refused with its reason). The builder
// writes each: the `app` label and nothing else on a template, and the
// component's name on the container.
func TestHandParsedKinds_TemplateMetadataAndContainerName(t *testing.T) {
	want := metav1.ObjectMeta{Labels: map[string]string{"app": "app"}}
	for _, componentType := range []string{"deployment", "statefulset", "daemonset", "job", "cronjob"} {
		t.Run(componentType, func(t *testing.T) {
			objects, err := parityGenerate(t, componentType, nil)
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			pod, jobTemplate := parityPodTemplate(t, objects)
			if !reflect.DeepEqual(pod.ObjectMeta, want) {
				t.Errorf("pod template metadata = %+v, want only the app label", pod.ObjectMeta)
			}
			if (jobTemplate != nil) != (componentType == "cronjob") {
				t.Fatalf("job template present = %v on a %s", jobTemplate != nil, componentType)
			}
			if jobTemplate != nil && !reflect.DeepEqual(*jobTemplate, want) {
				t.Errorf("job template metadata = %+v, want only the app label", *jobTemplate)
			}
			if len(pod.Spec.Containers) != 1 || pod.Spec.Containers[0].Name != "app" {
				t.Errorf("containers = %+v, want one, named after the component", pod.Spec.Containers)
			}
		})
	}
}

// TestHandParsedKinds_ServicePortFieldsReachTheObject holds a `ports` entry of
// the service kind to corev1.ServicePort: the entry's keys are that type's
// fields, name for name, and an entry that authors all of them generates a port
// that carries each value. The walk above sees `ports` as one property; this is
// the level below it.
func TestHandParsedKinds_ServicePortFieldsReachTheObject(t *testing.T) {
	entry := (&ServiceHandler{}).PropertySchema()["ports"].Items
	if entry == nil {
		t.Fatal("the service kind's ports property declares no entry schema")
	}
	upstream := slices.Sorted(maps.Keys(parityFields(reflect.TypeFor[corev1.ServicePort]())))
	if declared := slices.Sorted(maps.Keys(entry.Properties)); !slices.Equal(declared, upstream) {
		t.Fatalf("a ports entry declares %v, and corev1.ServicePort has %v", declared, upstream)
	}

	authored := map[string]any{
		"name":        "web",
		"port":        8080,
		"targetPort":  "http",
		"protocol":    "UDP",
		"nodePort":    30080,
		"appProtocol": "kubernetes.io/h2c",
	}
	if got := slices.Sorted(maps.Keys(authored)); !slices.Equal(got, upstream) {
		t.Fatalf("this test authors %v, and corev1.ServicePort has %v: author the new field and assert it below", got, upstream)
	}
	objects, err := parityGenerate(t, "service", map[string]any{"type": "NodePort", "ports": []any{authored}})
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	appProtocol := "kubernetes.io/h2c"
	want := []corev1.ServicePort{{
		Name:        "web",
		Port:        8080,
		TargetPort:  intstr.FromString("http"),
		Protocol:    corev1.ProtocolUDP,
		NodePort:    30080,
		AppProtocol: &appProtocol,
	}}
	if got := parityObject[*corev1.Service](t, objects).Spec.Ports; !reflect.DeepEqual(got, want) {
		t.Errorf("ports = %+v, want %+v", got, want)
	}
}

// paritySelectorSite is one label selector a hand-parsed kind parses: where an
// author writes it, and where it lands on the generated object.
type paritySelectorSite struct {
	componentType string
	path          string
	// author returns the property that holds the selector sel at this site.
	author func(sel map[string]any) (key string, value any)
	// read returns the selector the generated objects carry at this site.
	read func(t *testing.T, objects []*client.Object) *metav1.LabelSelector
}

// paritySelectorSites lists every label selector the nine hand-parsed kinds
// parse: 39.
//
//   - deployment, daemonset, job and cronjob read the raw `affinity`: two arms
//     (podAffinity, podAntiAffinity), each with required and preferred terms,
//     each term with a labelSelector and a namespaceSelector — eight selectors
//     — and the labelSelector of a `topologySpreadConstraints` entry: nine a
//     kind, 36;
//   - statefulset reads no raw affinity (its `affinity` is launcher's own
//     anti-affinity switch), so it has the topology spread selector and the
//     `selector` of a `volumeClaimTemplates` entry: two;
//   - persistentvolumeclaim has its `selector`: one. The `pvc` trait reads a
//     claim through the same parser (ParseClaimProperties).
//
// service's `selector` is a label map, not a LabelSelector, and the workload
// kinds' own `selector` is refused; configmap and serviceaccount have none.
func paritySelectorSites() []paritySelectorSite {
	const required, preferred = "requiredDuringSchedulingIgnoredDuringExecution", "preferredDuringSchedulingIgnoredDuringExecution"
	var sites []paritySelectorSite
	podSpec := func(t *testing.T, objects []*client.Object) corev1.PodSpec {
		t.Helper()
		pod, _ := parityPodTemplate(t, objects)
		return pod.Spec
	}
	topologySpread := func(componentType string) paritySelectorSite {
		return paritySelectorSite{
			componentType: componentType,
			path:          "topologySpreadConstraints[0].labelSelector",
			author: func(sel map[string]any) (string, any) {
				return "topologySpreadConstraints", []any{map[string]any{
					"maxSkew": 1, "topologyKey": "kubernetes.io/hostname", "whenUnsatisfiable": "DoNotSchedule", "labelSelector": sel,
				}}
			},
			read: func(t *testing.T, objects []*client.Object) *metav1.LabelSelector {
				t.Helper()
				constraints := podSpec(t, objects).TopologySpreadConstraints
				if len(constraints) != 1 {
					t.Fatalf("topologySpreadConstraints = %+v, want one", constraints)
				}
				return constraints[0].LabelSelector
			},
		}
	}
	for _, componentType := range []string{"deployment", "daemonset", "job", "cronjob"} {
		for _, arm := range []string{"podAffinity", "podAntiAffinity"} {
			for _, mode := range []string{required, preferred} {
				for _, field := range []string{"labelSelector", "namespaceSelector"} {
					sites = append(sites, paritySelectorSite{
						componentType: componentType,
						path:          "affinity." + arm + "." + mode + "[0]." + field,
						author: func(sel map[string]any) (string, any) {
							// A term needs a labelSelector; the selector under
							// test replaces it when it is the one.
							term := map[string]any{
								"topologyKey":   "kubernetes.io/hostname",
								"labelSelector": map[string]any{"matchLabels": map[string]any{"tier": "web"}},
							}
							term[field] = sel
							item := any(term)
							if mode == preferred {
								item = map[string]any{"weight": 1, "podAffinityTerm": term}
							}
							return "affinity", map[string]any{arm: map[string]any{mode: []any{item}}}
						},
						read: func(t *testing.T, objects []*client.Object) *metav1.LabelSelector {
							t.Helper()
							affinity := podSpec(t, objects).Affinity
							if affinity == nil {
								t.Fatal("the pod has no affinity")
							}
							var terms []corev1.PodAffinityTerm
							var weighted []corev1.WeightedPodAffinityTerm
							switch {
							case arm == "podAffinity" && affinity.PodAffinity != nil:
								terms, weighted = affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution, affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution
							case arm == "podAntiAffinity" && affinity.PodAntiAffinity != nil:
								terms, weighted = affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution, affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution
							}
							if mode == preferred {
								terms = nil
								for _, w := range weighted {
									terms = append(terms, w.PodAffinityTerm)
								}
							}
							if len(terms) != 1 {
								t.Fatalf("%s.%s = %+v, want one term", arm, mode, terms)
							}
							if field == "namespaceSelector" {
								return terms[0].NamespaceSelector
							}
							return terms[0].LabelSelector
						},
					})
				}
			}
		}
		sites = append(sites, topologySpread(componentType))
	}
	sites = append(sites, topologySpread("statefulset"), paritySelectorSite{
		componentType: "statefulset",
		path:          "volumeClaimTemplates[0].selector",
		author: func(sel map[string]any) (string, any) {
			return "volumeClaimTemplates", []any{map[string]any{"name": "data", "mountPath": "/data", "size": "1Gi", "selector": sel}}
		},
		read: func(t *testing.T, objects []*client.Object) *metav1.LabelSelector {
			t.Helper()
			templates := parityObject[*appsv1.StatefulSet](t, objects).Spec.VolumeClaimTemplates
			if len(templates) != 1 {
				t.Fatalf("volumeClaimTemplates = %+v, want one", templates)
			}
			return templates[0].Spec.Selector
		},
	}, paritySelectorSite{
		componentType: "persistentvolumeclaim",
		path:          "selector",
		author:        func(sel map[string]any) (string, any) { return "selector", sel },
		read: func(t *testing.T, objects []*client.Object) *metav1.LabelSelector {
			t.Helper()
			return parityObject[*corev1.PersistentVolumeClaim](t, objects).Spec.Selector
		},
	})
	return sites
}

// TestHandParsedKinds_MatchExpressionsAtEverySelector runs six match
// expressions at every label selector a hand-parsed kind parses
// (paritySelectorSites): one whole expression, which must reach the object as
// authored, and five the API server refuses (ValidateLabelSelectorRequirement),
// each of which the kind must refuse at build time. A selector with a refused
// expression that built would reach the cluster as a selector the author did
// not write, or as an object the API server turns away.
func TestHandParsedKinds_MatchExpressionsAtEverySelector(t *testing.T) {
	sites := paritySelectorSites()
	if len(sites) != 39 {
		t.Fatalf("paritySelectorSites lists %d sites, want 39: a site was added or lost, so recount its comment", len(sites))
	}
	whole := map[string]any{"key": "tier", "operator": "In", "values": []any{"web"}}
	wantWhole := &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
		{Key: "tier", Operator: metav1.LabelSelectorOpIn, Values: []string{"web"}},
	}}
	refused := []struct {
		name string
		expr map[string]any
	}{
		{"no key", map[string]any{"operator": "In", "values": []any{"web"}}},
		{"no operator", map[string]any{"key": "tier", "values": []any{"web"}}},
		{"an operator that is none of the four", map[string]any{"key": "tier", "operator": "Equals", "values": []any{"web"}}},
		{"In without values", map[string]any{"key": "tier", "operator": "In"}},
		{"Exists with values", map[string]any{"key": "tier", "operator": "Exists", "values": []any{"web"}}},
	}
	selector := func(expr map[string]any) map[string]any {
		return map[string]any{"matchExpressions": []any{expr}}
	}
	seen := map[string]bool{}
	for _, site := range sites {
		name := site.componentType + "/" + site.path
		if seen[name] {
			t.Errorf("the site %s is listed twice", name)
		}
		seen[name] = true
		t.Run(name, func(t *testing.T) {
			key, value := site.author(selector(whole))
			objects, err := parityGenerate(t, site.componentType, map[string]any{key: value})
			if err != nil {
				t.Fatalf("a whole expression is refused: %v", err)
			}
			if got := site.read(t, objects); !reflect.DeepEqual(got, wantWhole) {
				t.Errorf("selector on the object = %+v, want %+v", got, wantWhole)
			}
			for _, tc := range refused {
				key, value := site.author(selector(tc.expr))
				if _, err := parityGenerate(t, site.componentType, map[string]any{key: value}); err == nil {
					t.Errorf("%s: the expression %v builds, want it refused", tc.name, tc.expr)
				}
			}
		})
	}
}
