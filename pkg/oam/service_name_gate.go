package oam

import (
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
)

// serviceNameGate is the Kubernetes feature gate that lets a Service name start
// with a digit: off by default below the release the lifecycle table names for
// it, when the API server refuses such a name, unless the profile's spec.featureGates
// sets it (go-kure/launcher#1230, go-kure/launcher#1231).
const serviceNameGate = "RelaxedServiceNameValidation"

// CodeCheckedGates returns the lifecycle-table gates that code checks on the
// objects it generates, apart from the ones a PropertySchema names (Gate,
// GatedPaths, ValueGates). A test in a package that can see the built-in schemas
// holds the table to both: every row is carried by a schema or listed here.
func CodeCheckedGates() []string {
	return []string{serviceNameGate}
}

// serviceNameGateProblem is the refusal of a Service name that starts with a
// digit when serviceNameGate is off for target (below the release where it is on by
// default, or set off by the profile's spec.featureGates), nil otherwise. field says where the name is written. Only the first byte is
// read: the rest of the name is the DNS label rule's, which the handlers apply
// first, so a name that breaks it is refused for that reason before it gets here.
func serviceNameGateProblem(field, name string, target gateTarget) error {
	if name == "" || name[0] < '0' || name[0] > '9' {
		return nil
	}
	on, err := target.enabled(serviceNameGate)
	if err != nil {
		return err
	}
	if on {
		return nil
	}
	if target.setOff(serviceNameGate) {
		return errors.Errorf("%s %q: a Service name may start with a digit only with the Kubernetes feature gate %s, and the ClusterProfile's spec.featureGates sets it to false; start the name with a letter or set the gate to true",
			field, name, serviceNameGate)
	}
	release, _ := FeatureGateOnByDefault(serviceNameGate)
	return errors.Errorf("%s %q: a Service name may start with a digit only with the Kubernetes feature gate %s, on by default from %s, and the ClusterProfile's kubernetesVersion is %s; start the name with a letter or raise spec.kubernetesVersion",
		field, name, serviceNameGate, release, target)
}

// serviceNameBearer is a kind whose object names a Service: its own, or the one an
// operator creates or looks up from a field of the object.
type serviceNameBearer struct {
	group, kind string
	// path is the field holding the Service name; nil means metadata.name. When
	// each is set, path is read from every item of the list at each instead.
	path []string
	// each is the list whose every item holds a Service name at path.
	each []string
}

// serviceNameBearers are the kinds launcher emits that name a Service: the core
// Service by its own name, the CloudNativePG Pooler (its operator names the
// pooler's Service after it), the Prometheus operator's four workloads (the only
// kinds of its API with a spec.serviceName: Prometheus, PrometheusAgent,
// Alertmanager, ThanosRuler) by spec.serviceName, the governing Service the
// operator looks up, and the CloudNativePG Cluster, whose operator creates one
// Service for each entry of spec.managed.services.additional under the name its
// serviceTemplate states. A kind that is not here is not checked;
// TestServiceNameGate_EveryBuiltinKindIsCovered fails when a built-in component
// renders one that names a Service.
var serviceNameBearers = []serviceNameBearer{
	{group: "", kind: "Service"},
	{group: "postgresql.cnpg.io", kind: "Pooler"},
	{group: "postgresql.cnpg.io", kind: "Cluster",
		each: []string{"spec", "managed", "services", "additional"}, path: []string{"serviceTemplate", "metadata", "name"}},
	{group: "monitoring.coreos.com", kind: "Prometheus", path: []string{"spec", "serviceName"}},
	{group: "monitoring.coreos.com", kind: "PrometheusAgent", path: []string{"spec", "serviceName"}},
	{group: "monitoring.coreos.com", kind: "Alertmanager", path: []string{"spec", "serviceName"}},
	{group: "monitoring.coreos.com", kind: "ThanosRuler", path: []string{"spec", "serviceName"}},
}

// ServiceNameCheckedKinds returns the group and kind of every object the Service
// name check holds, so a test that renders the built-in components can hold the
// list to what they emit.
func ServiceNameCheckedKinds() []schema.GroupKind {
	kinds := make([]schema.GroupKind, 0, len(serviceNameBearers))
	for _, b := range serviceNameBearers {
		kinds = append(kinds, schema.GroupKind{Group: b.group, Kind: b.kind})
	}
	return kinds
}

// gate is the target the checks hold a Service name to: the wrapper's release and
// the profile's spec.featureGates. Only called with a target set.
func (o *ownedConfig) gate() gateTarget {
	return gateTarget{version: *o.target, overrides: o.gates}
}

// checkServiceNames holds obj, and every object it stands for when Flux applies
// it, to serviceNameGateProblem under the wrapper's target. A wrapper with no
// target, or with a target for which the gate is on (at or above the release where it
// is on by default, or set on by the profile's spec.featureGates), checks nothing: the rule has no say there, so an object it cannot read
// is not refused for it either.
func (o *ownedConfig) checkServiceNames(obj client.Object) error {
	if o.target == nil {
		return nil
	}
	on, err := o.gate().enabled(serviceNameGate)
	if err != nil {
		return o.serviceNameViolation(err)
	}
	if on {
		return nil
	}
	for _, applied := range kindAppliedObjects(obj) {
		if err := o.checkServiceName(applied); err != nil {
			return err
		}
	}
	return nil
}

// checkServiceName is the check on one applied object. An object whose kind
// cannot be told names no Service this check can read: the object kind policy
// refuses it when that policy is in force.
func (o *ownedConfig) checkServiceName(obj client.Object) error {
	if isNullValue(obj) {
		return nil
	}
	m, err := objectAsWritten(obj)
	gvk, ok := serviceNameObjectKind(obj, m)
	if !ok {
		return nil
	}
	for _, b := range serviceNameBearers {
		if gvk.Group != b.group || gvk.Kind != b.kind {
			continue
		}
		if err != nil {
			// Only here is the name read off the object itself: with no written form
			// there is nothing else to name it by, and it is a label, not a decision.
			return o.serviceNameViolation(errors.Errorf("%s %q: the Service name cannot be checked: %w", gvk.Kind, obj.GetName(), err))
		}
		// The name, in the refusal as in the check, is the one the writer writes.
		name := serviceNameField(m, []string{"metadata", "name"})
		object := fmt.Sprintf("%s %q", gvk.Kind, name)
		switch {
		case b.each != nil:
			for i, item := range serviceNameItemFields(m, b.each, b.path) {
				field := fmt.Sprintf("%s %s[%d].%s", object, strings.Join(b.each, "."), i, strings.Join(b.path, "."))
				if refusal := o.serviceNameViolation(serviceNameGateProblem(field, item, o.gate())); refusal != nil {
					return refusal
				}
			}
			return nil
		case b.path == nil:
			return o.serviceNameViolation(serviceNameGateProblem(gvk.Kind+" metadata.name", name, o.gate()))
		default:
			return o.serviceNameViolation(serviceNameGateProblem(object+" "+strings.Join(b.path, "."), serviceNameField(m, b.path), o.gate()))
		}
	}
	return nil
}

// serviceNameObjectKind is the group and kind the writer writes for obj. For an
// unstructured object that encodes with a string kind, that is the kind and
// apiVersion of its written form m, which a value of another Go type than string
// (a json.RawMessage, say) still states to the writer. Otherwise it is the kind
// the object kind policy tells (objectGroupVersionKind): a typed object's Go type,
// an object that does not encode, and an unstructured object whose written form
// has no string kind, which names no Service this check can read and is left to
// the object kind policy.
func serviceNameObjectKind(obj client.Object, m map[string]any) (schema.GroupVersionKind, bool) {
	if _, unstructuredObject := obj.(*unstructured.Unstructured); unstructuredObject && m != nil {
		if kind, _ := m["kind"].(string); kind != "" {
			apiVersion, _ := m["apiVersion"].(string)
			return schema.FromAPIVersionAndKind(apiVersion, kind), true
		}
	}
	return objectGroupVersionKind(obj)
}

// serviceNameViolation is the refusal of an object of the wrapper's application,
// its owning component's or, for an application the document as a whole owns,
// the one the application came from (entry). It carries no policy class: the
// rule is the library's, not the environment's.
func (o *ownedConfig) serviceNameViolation(err error) error {
	if err == nil {
		return nil
	}
	owner := o.component
	if owner == "" {
		owner = o.entry
	}
	return NewViolationError(owner, err)
}

// serviceNameField reads the string at path of m, an object as written
// (objectAsWritten); a missing, null or non-string value reads as "", which names
// no Service to hold.
func serviceNameField(m map[string]any, path []string) string {
	s, _, _ := unstructured.NestedString(m, path...)
	return s
}

// serviceNameItemFields reads path as a string from every item of the list at
// each in m, an object as written (objectAsWritten), in list order, so the index
// of a result is the item's. An item whose value is missing, null or not a string
// reads as "", which names no Service to hold; a missing or non-list value at
// each reads as no items.
func serviceNameItemFields(m map[string]any, each, path []string) []string {
	// NoCopy: reading a string needs no copy, and m is the only copy there is.
	field, _, _ := unstructured.NestedFieldNoCopy(m, each...)
	items, _ := field.([]any)
	names := make([]string, len(items))
	for i, item := range items {
		if im, isMap := item.(map[string]any); isMap {
			names[i], _, _ = unstructured.NestedString(im, path...)
		}
	}
	return names
}

// objectAsWritten is obj as the writer writes it: the JSON of its fields, read
// back as a map, for an unstructured object as for a typed one. An unstructured
// object a builder made may hold a Go-built list ([]map[string]any, not []any) or
// a Go int, which a type assertion on the raw map misses or panics on, while the
// writer serialises both as the ordinary JSON they stand for. An object that does
// not encode is an error, never an object that names no Service: a name the check
// cannot read is a name it cannot hold, and the writer fails on the object too.
func objectAsWritten(obj client.Object) (map[string]any, error) {
	var v any = obj
	if u, ok := obj.(*unstructured.Unstructured); ok {
		v = u.Object
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, errors.Errorf("the object does not encode as JSON: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, errors.Errorf("the object does not decode from its JSON: %w", err)
	}
	return m, nil
}
