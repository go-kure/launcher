package components

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// This file is the upstream parity test of the hand-parsed kinds
// (go-kure/launcher#790). Such a kind reads its properties key by key, so
// nothing but a test holds it to the Kubernetes type it projects: an upstream
// field its parser does not know is not a decode error, it is a key the parser
// never looks at.

// parityKind is one hand-parsed kind: its component type, its property schema
// and the Kubernetes object it generates.
type parityKind struct {
	componentType string
	schema        func() map[string]oam.PropertySchema
	object        reflect.Type
}

var parityKinds = []parityKind{
	{"deployment", (&DeploymentHandler{}).PropertySchema, reflect.TypeFor[appsv1.Deployment]()},
	{"statefulset", (&StatefulsetHandler{}).PropertySchema, reflect.TypeFor[appsv1.StatefulSet]()},
	{"daemonset", (&DaemonsetHandler{}).PropertySchema, reflect.TypeFor[appsv1.DaemonSet]()},
	{"job", (&JobHandler{}).PropertySchema, reflect.TypeFor[batchv1.Job]()},
	{"cronjob", (&CronjobHandler{}).PropertySchema, reflect.TypeFor[batchv1.CronJob]()},
	{"service", (&ServiceHandler{}).PropertySchema, reflect.TypeFor[corev1.Service]()},
	{"persistentvolumeclaim", (&PersistentVolumeClaimHandler{}).PropertySchema, reflect.TypeFor[corev1.PersistentVolumeClaim]()},
	{"configmap", (&ConfigMapHandler{}).PropertySchema, reflect.TypeFor[corev1.ConfigMap]()},
	{"serviceaccount", (&ServiceAccountHandler{}).PropertySchema, reflect.TypeFor[corev1.ServiceAccount]()},
}

// The classes an upstream field of a hand-parsed kind falls in. Two need no
// rule below, since the kind's schema and its refusal maps decide them: a field
// the schema declares under its upstream name is read, and a field one of the
// kind's refusal maps names is refused with that map's reason.
const (
	// parityEnvelope: a field of the object around the spec. A component
	// authors none of them as a property of the kind's own schema; the rule's
	// comment says what stands for it.
	parityEnvelope = "envelope"
	// parityLevel: a field that only holds the next level down, whose own
	// fields are the component's properties. Its name is no property and has no
	// refusal entry: it is the envelope of that level.
	parityLevel = "level"
	// parityOtherShape: read, but not under its upstream name, which is free on
	// this kind. The name is an entry of a refusal map whose reason names the
	// properties of `as`, so neither path drops it in silence.
	parityOtherShape = "other shape"
	// parityRenamed: read under the property of `as`, because the upstream name
	// is a property of this kind already, read as another upstream field.
	parityRenamed = "renamed"
	// parityNameTaken: not read, and its name is taken on this kind by another
	// upstream field of the same name, which is read as that property: `as`
	// names that field. No refusal can sit on the name, so the README is where
	// an author learns it: a container's own restartPolicy under the pod's on
	// `job` and `cronjob`, and the job template's suspend under the CronJob's
	// on `cronjob`.
	parityNameTaken = "name taken on this kind by another upstream field of the same name"
	// parityReadInPart: read under its name, and the fields of its type the
	// property does not declare are keys of `nested`, the refusal map of the
	// property's parser. The walk goes this one level further down for the
	// fields that have such a map and for no other.
	parityReadInPart = "read, a field of its type refused by the property's parser"
)

// parityRule places one upstream field, named "<Go type>.<JSON name>", in a
// class its kind's schema and refusal maps do not decide.
type parityRule struct {
	class string
	// as are the properties that author the field (parityOtherShape,
	// parityRenamed), each of which must be a property of the kind; for
	// parityNameTaken it is the one upstream field that has the name.
	as []string
	// only limits the rule to these component types; empty means every kind
	// that has the field.
	only []string
	// descend says the field's type is walked too: its fields are properties
	// of the component.
	descend bool
	// nested is the refusal map of the parser that reads the property
	// (parityReadInPart): its keys are fields of the upstream
	// field's type, refused one level down.
	nested map[string]string
}

var parityRules = map[string][]parityRule{
	// The object's own fields. A kind's component type is its kind and API
	// version. Its metadata is the component's name and the three properties
	// the engine adds to every kind's schema, `objectName`, `labels` and
	// `annotations` (TestObjectMetadata_EveryKindComponentTakesIt); they are no
	// keys of the handler's own schema, which is what this walk reads. Status
	// is the cluster's.
	"apiVersion": {{class: parityEnvelope}},
	"kind":       {{class: parityEnvelope}},
	"metadata":   {{class: parityEnvelope}},
	"status":     {{class: parityEnvelope}},
	"spec":       {{class: parityLevel, descend: true}},

	// The pod template and the job template: each is refused as a whole with
	// the reason, and its fields are the component's own properties.
	"DeploymentSpec.template":  {{class: parityOtherShape, descend: true}},
	"StatefulSetSpec.template": {{class: parityOtherShape, descend: true}},
	"DaemonSetSpec.template":   {{class: parityOtherShape, descend: true}},
	"JobSpec.template":         {{class: parityOtherShape, descend: true}},
	"CronJobSpec.jobTemplate":  {{class: parityOtherShape, descend: true}},
	// A template's metadata is the builder's: the `app` label, and nothing an
	// author writes (TestHandParsedKinds_TemplateMetadataAndContainerName).
	"PodTemplateSpec.metadata": {{class: parityLevel}},
	"PodTemplateSpec.spec":     {{class: parityLevel, descend: true}},
	"JobTemplateSpec.metadata": {{class: parityLevel}},
	"JobTemplateSpec.spec":     {{class: parityLevel, descend: true}},

	// The main container is the component's own container properties.
	"PodSpec.containers":       {{class: parityOtherShape, descend: true}},
	"Container.name":           {{class: parityOtherShape}},
	"Container.livenessProbe":  {{class: parityOtherShape, as: []string{"probes"}}},
	"Container.readinessProbe": {{class: parityOtherShape, as: []string{"probes"}}},
	"Container.startupProbe":   {{class: parityOtherShape, as: []string{"probes"}}},
	"Container.volumeMounts":   {{class: parityOtherShape, as: []string{"volumes"}}},
	"Container.volumeDevices":  {{class: parityOtherShape, as: []string{"volumes"}}},
	"Container.restartPolicy":  {{class: parityNameTaken, as: []string{"PodSpec.restartPolicy"}, only: []string{"job", "cronjob"}}},
	// The cronjob's `suspend` is the CronJob's; the job's own stays with `job`
	// (JobSpecConfig, TestCronjobHandler_SuspendWritesCronJobSpecOnly).
	"JobSpec.suspend": {{class: parityNameTaken, as: []string{"CronJobSpec.suspend"}, only: []string{"cronjob"}}},

	// Pod fields whose name is the container's property, or the job's.
	"PodSpec.securityContext":       {{class: parityRenamed, as: []string{"podSecurityContext"}}},
	"PodSpec.resources":             {{class: parityRenamed, as: []string{"podResources"}}},
	"PodSpec.activeDeadlineSeconds": {{class: parityRenamed, as: []string{"podActiveDeadlineSeconds"}, only: []string{"job", "cronjob"}}},

	// A container's `resources` are read without `claims` (parseResources).
	"Container.resources": {{class: parityReadInPart, nested: resourcesRejectedKeys}},

	"PersistentVolumeClaimSpec.resources": {{class: parityOtherShape, as: []string{"size"}}},
}

// parityRefusedBeyondUpstream are the keys of a kind's refusal maps that are no
// field of the type the kind projects, each with why the kind refuses it
// nevertheless.
var parityRefusedBeyondUpstream = map[string]map[string]string{
	"job": {
		// jobCronOnlyRejectedKeys: the CronJobSpec fields a document retyped
		// from `cronjob` to `job` leaves behind.
		"schedule":                   "a CronJobSpec field",
		"timeZone":                   "a CronJobSpec field",
		"concurrencyPolicy":          "a CronJobSpec field",
		"startingDeadlineSeconds":    "a CronJobSpec field",
		"successfulJobsHistoryLimit": "a CronJobSpec field",
		"failedJobsHistoryLimit":     "a CronJobSpec field",
	},
}

// parityFields returns the JSON fields of a Kubernetes API struct by name, an
// inlined struct's among them.
func parityFields(typ reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for i := range typ.NumField() {
		f := typ.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.Anonymous && name == "" {
			maps.Copy(out, parityFields(f.Type))
			continue
		}
		if !f.IsExported() || name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		out[name] = f.Type
	}
	return out
}

// parityRuleFor returns the rule of parityRules for a field of typ on a kind.
// The object's own fields are keyed by their bare name: every kind has them.
func parityRuleFor(k parityKind, typ reflect.Type, name string) (string, parityRule, bool) {
	id := typ.Name() + "." + name
	if typ == k.object {
		if _, ok := parityRules[name]; ok {
			id = name
		}
	}
	for _, rule := range parityRules[id] {
		if len(rule.only) == 0 || slices.Contains(rule.only, k.componentType) {
			return id, rule, true
		}
	}
	return id, parityRule{}, false
}

// TestHandParsedKinds_CoverEveryUpstreamField walks the Kubernetes type each
// hand-parsed kind projects, from the object down every level whose fields are
// the component's own properties, and holds every field to exactly one answer:
//
//   - read: the kind's schema declares a property of the field's name;
//   - refused: one of the kind's refusal maps (refusedKeys) names it, and
//     TestRefusedKeys_BothPathsGiveTheReason holds the parser and the document
//     check to that reason;
//   - one of the classes of parityRules.
//
// The walk stops at the component's properties. What a property holds inside is
// held to the upstream type only for a property whose parser has a refusal map
// of its own (parityReadInPart); the fields inside every other property are not
// walked.
//
// A field with no answer fails the test, so a field a later k8s.io/api adds to
// one of these types cannot be dropped in silence: it is read, or it gets its
// refusal and reason. Two upstream fields may not be read as one property, a
// refusal key that is no upstream field needs its row in
// parityRefusedBeyondUpstream, and a rule nothing uses fails as well.
//
// What "read" rests on: that a declared property reaches the object is the
// business of each parser's schema/parser parity test and of the kind's own
// tests, not of this walk.
func TestHandParsedKinds_CoverEveryUpstreamField(t *testing.T) {
	usedRules := map[string]bool{}
	for _, k := range parityKinds {
		t.Run(k.componentType, func(t *testing.T) {
			schema := k.schema()
			readAs := map[string]string{}    // property -> the upstream field it reads
			refused := map[string]bool{}     // refusal keys an upstream field met
			tally := map[string]int{}        // answer -> number of upstream fields
			nameTaken := map[string]string{} // property -> the field a parityNameTaken rule says has it
			declared := func(props ...string) bool {
				for _, p := range props {
					if _, ok := schema[p]; !ok {
						return false
					}
				}
				return true
			}
			claim := func(prop, id string) {
				if prev, ok := readAs[prop]; ok {
					t.Errorf("%s and %s are both read as the property %q: one of them needs a rule in parityRules", prev, id, prop)
				}
				readAs[prop] = id
			}

			var walk func(typ reflect.Type)
			walk = func(typ reflect.Type) {
				fields := parityFields(typ)
				for _, name := range slices.Sorted(maps.Keys(fields)) {
					id, rule, hasRule := parityRuleFor(k, typ, name)
					hint := refusedKeyHint(k.componentType, name)
					class := rule.class
					switch {
					case hasRule && (class == parityEnvelope || class == parityLevel):
						usedRules[id] = true
						if declared(name) || hint != "" {
							t.Errorf("%s is %s, but the kind declares or refuses a property %q", id, class, name)
						}
					case hasRule && class == parityOtherShape:
						usedRules[id] = true
						refused[name] = true
						if declared(name) {
							t.Errorf("%s is read in another shape, but the kind declares a property %q", id, name)
						}
						if hint == "" {
							t.Errorf("%s is read in another shape, and the kind has no refusal entry for %q naming it", id, name)
						}
						for _, prop := range rule.as {
							if !declared(prop) {
								t.Errorf("%s is authored as %q, which the kind does not declare", id, prop)
							}
							if !strings.Contains(hint, prop) {
								t.Errorf("%s is authored as %q, and its refusal does not say so: %q", id, prop, hint)
							}
						}
					case hasRule && class == parityRenamed:
						usedRules[id] = true
						if !declared(name) {
							t.Errorf("%s is renamed because %q is taken, but the kind declares no property %q", id, name, name)
						}
						for _, prop := range rule.as {
							if !declared(prop) {
								t.Errorf("%s is read as %q, which the kind does not declare", id, prop)
							}
							claim(prop, id)
						}
					case hasRule && class == parityNameTaken:
						usedRules[id] = true
						if len(rule.as) != 1 {
							t.Fatalf("%s: a parityNameTaken rule names exactly one field in `as`, got %v", id, rule.as)
						}
						nameTaken[name] = rule.as[0]
						if !declared(name) || hint != "" {
							t.Errorf("%s: its name is said to be the property of %s, but %q is not a declared, unrefused property", id, rule.as[0], name)
						}
					case hasRule && class == parityReadInPart:
						usedRules[id] = true
						if !declared(name) || hint != "" {
							t.Errorf("%s: %q is not a declared, unrefused property of the kind", id, name)
						}
						claim(name, id)
						inner := fields[name]
						for inner.Kind() == reflect.Pointer {
							inner = inner.Elem()
						}
						innerFields := parityFields(inner)
						for _, field := range slices.Sorted(maps.Keys(innerFields)) {
							_, isDeclared := schema[name].Properties[field]
							reason, isRefused := rule.nested[field]
							switch {
							case isDeclared && isRefused:
								t.Errorf("%s.%s: the property both declares and refuses it", id, field)
							case !isDeclared && !isRefused:
								t.Errorf("%s.%s (%s) is neither read nor refused by the property %q of %q: declare it, or give it an entry with its reason in the parser's refusal map", id, field, innerFields[field], name, k.componentType)
							case isRefused && !strings.HasPrefix(reason, name+"."+field+": "):
								t.Errorf("%s.%s: the refusal does not begin with the path %q: %q", id, field, name+"."+field, reason)
							}
						}
						for field := range rule.nested {
							if _, ok := innerFields[field]; !ok {
								t.Errorf("%s: the refusal key %q is no field of %s", id, field, inner)
							}
						}
					case hint != "":
						class = "refused"
						refused[name] = true
						if declared(name) {
							t.Errorf("%s: the kind both declares and refuses %q", id, name)
						}
					case declared(name):
						class = "read"
						claim(name, id)
					default:
						class = "no answer"
						t.Errorf("%s (%s) is neither read nor refused by %q: declare it, give it a refusal entry with its reason, or a rule in parityRules", id, fields[name], k.componentType)
					}
					tally[class]++
					if hasRule && rule.descend {
						next := fields[name]
						for next.Kind() == reflect.Pointer || next.Kind() == reflect.Slice {
							next = next.Elem()
						}
						walk(next)
					}
				}
			}
			walk(k.object)
			if tally["read"] == 0 {
				t.Errorf("the walk found no field %q reads: it did not reach the kind's properties", k.componentType)
			}
			t.Logf("upstream fields by answer: %v", tally)

			for name, holder := range nameTaken {
				if readAs[name] != holder {
					t.Errorf("the property %q is read as %q, want %s", name, readAs[name], holder)
				}
			}
			for _, refusals := range refusedKeys[k.componentType] {
				for key := range refusals {
					if _, beyond := parityRefusedBeyondUpstream[k.componentType][key]; !refused[key] && !beyond {
						t.Errorf("the refusal key %q is no field of the type %q projects: remove it, or say why in parityRefusedBeyondUpstream", key, k.componentType)
					} else if refused[key] && beyond {
						t.Errorf("the refusal key %q is an upstream field; drop its row in parityRefusedBeyondUpstream", key)
					}
				}
			}
			for key := range parityRefusedBeyondUpstream[k.componentType] {
				if refusedKeyHint(k.componentType, key) == "" {
					t.Errorf("parityRefusedBeyondUpstream names %q, which %q does not refuse", key, k.componentType)
				}
			}
		})
	}
	for id := range parityRules {
		if !usedRules[id] {
			t.Errorf("the rule for %s in parityRules matched no field of any kind", id)
		}
	}
}
