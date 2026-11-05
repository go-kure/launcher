package components

import (
	"bytes"
	"encoding"
	"encoding/base64"
	"encoding/json"
	"math"
	"math/big"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	kureio "github.com/go-kure/kure/pkg/io"
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/manifest"
	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utiljson "k8s.io/apimachinery/pkg/util/json"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// PassthroughHandler handles the generic "passthrough" component type, which emits
// an arbitrary Kubernetes object (CRD or non-standard type) declared inline. The
// component properties separate control (clusterScoped) from the emitted body (object):
//
//	type: passthrough
//	properties:
//	  clusterScoped: false   # optional; unset, the object's kind decides
//	  object:                # the Kubernetes object, emitted verbatim
//	    apiVersion: ...
//	    kind: ...
//	    metadata: { ... }    # optional; name defaults to the component name
//	    spec: { ... }        # any top-level fields pass through
//
// "Object" is singular and is enforced as such: a list-shaped body (an `items`
// sequence, by apimachinery's own IsList) is rejected in ToApplicationConfig, because
// Generate emits ONE resource and a list would smuggle N past every per-object rule
// downstream. Declare one passthrough component per object.
type PassthroughHandler struct{}

// CanHandle returns true for the passthrough component type.
func (h *PassthroughHandler) CanHandle(componentType string) bool {
	return componentType == "passthrough"
}

// PropertySchema declares the passthrough component's properties. `object` is the
// escape-hatch body emitted verbatim, so it is an open object (additionalProperties).
// `clusterScoped` declares no default: left unset it is not read as false, the
// object's kind decides (PassthroughConfig.resolveClusterScoped).
func (h *PassthroughHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"object":        {Type: oam.PropertyTypeObject, Required: true, AdditionalProperties: true, Description: "The single Kubernetes object emitted verbatim (apiVersion, kind, metadata, and any body fields); a list is rejected."},
		"clusterScoped": {Type: oam.PropertyTypeBoolean, Description: "Whether the emitted object is cluster-scoped, so that it gets no namespace. Unset, the object's kind decides where its scope is known, and the object is treated as namespaced otherwise. Refused when it contradicts a kind whose scope the Kubernetes API defines."},
	}
}

// ToApplicationConfig validates the passthrough properties and returns a PassthroughConfig.
func (h *PassthroughHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	props := component.Properties

	for key := range props {
		if key != "object" && key != "clusterScoped" {
			return nil, errors.Errorf("passthrough component %q: unsupported property %q (only 'object' and 'clusterScoped' are allowed)", component.Name, key)
		}
	}

	clusterScoped := false
	raw, scopeAuthored := authoredValue(props, "clusterScoped")
	if scopeAuthored {
		b, isBool := raw.(bool)
		if !isBool {
			return nil, errors.Errorf("passthrough component %q: 'clusterScoped' must be a bool", component.Name)
		}
		clusterScoped = b
	}

	rawObj, ok := props["object"]
	if !ok {
		return nil, errors.Errorf("passthrough component %q: required property 'object' missing", component.Name)
	}
	object, ok := rawObj.(map[string]any)
	if !ok {
		return nil, errors.Errorf("passthrough component %q: 'object' must be a map", component.Name)
	}

	if err := validateEmittableObject(component.Name, object); err != nil {
		return nil, err
	}

	if rawMeta, ok := object["metadata"]; ok {
		if _, ok := rawMeta.(map[string]any); !ok {
			return nil, errors.Errorf("passthrough component %q: object.metadata must be a map", component.Name)
		}
	}

	cfg := &PassthroughConfig{
		componentName: component.Name,
		Namespace:     namespace,
		ClusterScoped: clusterScoped,
		scopeAuthored: scopeAuthored,
		// Frozen here, not aliased, so a caller mutating the map it passed in
		// cannot change what was validated. Generate still copies again per call —
		// it stamps metadata and may run more than once, and that copy protects
		// the source properties, not this invariant.
		//
		// The copy alone does NOT make the checks above binding, and the comment
		// here used to claim it did. Object is an exported field: a caller holding
		// the config can assign a fresh map to it, and a struct literal or a JSON
		// decode never runs this function at all. Generate re-runs the arms on the
		// map it is about to emit; that, not the copy, is what closes those routes.
		Object: deepCopyMap(object),
	}
	// Build the emitted object once here, so that what emitted refuses on its
	// own account (a clusterScoped that contradicts the object's kind, a
	// namespace on a cluster-scoped object, a workload that sets a field its
	// API type does not declare) fails the component now and not at generation.
	if _, err := cfg.validated(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validateEmittableObject holds everything that must be true of the map passthrough
// is about to emit, so that ToApplicationConfig and Generate cannot disagree about
// what a valid body is. Splitting it — the constructor checking identity, Generate
// checking only list shape — is what let `&PassthroughConfig{Object: map[string]any{}}`
// through: non-nil, so it cleared the nil guard, and carrying no items, so it cleared
// both list arms, leaving Generate to emit a document consisting of nothing but the
// metadata it had just stamped on.
func validateEmittableObject(componentName string, object map[string]any) error {
	if apiVersion, ok := object["apiVersion"].(string); !ok || apiVersion == "" {
		return errors.Errorf("passthrough component %q: object.apiVersion is required and must be a non-empty string", componentName)
	}
	kind, ok := object["kind"].(string)
	if !ok || kind == "" {
		return errors.Errorf("passthrough component %q: object.kind is required and must be a non-empty string", componentName)
	}
	return rejectListEnvelope(componentName, kind, object)
}

// rejectListEnvelope refuses the two list-shaped bodies passthrough cannot honestly
// emit as one resource. It is reached from ToApplicationConfig, and again from
// Generate on the map that is actually about to be emitted — see the note on
// PassthroughConfig.Object for why the second call is not redundant.
//
// A List is N objects and this handler's contract is one — the type comment on
// PassthroughHandler and the schema description both say "the Kubernetes object
// emitted verbatim", singular, so a list was never inside the contract. It matters
// here rather than downstream because Generate emits the map as a SINGLE
// unstructured and stamps a name and a namespace onto it: a list would arrive as
// one named envelope whose items never see per-object label mutation, namespace
// stamping or ownership checks, while Flux unwraps it at apply time into N objects
// that do reach the cluster. One envelope bypasses every per-object rule at once,
// which is why no single downstream check can catch it.
//
// Two stages expand lists on the way to the cluster, with different predicates, and
// each arm below matches one of them. Kustomize's build (kustomize/api
// resource/factory.go, inlineAnyEmbeddedLists) consults `items` only on a kind
// ending in "List". Flux's kustomize-controller then decodes the build output with
// fluxcd/pkg/ssa utils.ReadObjects, which expands every object for which
// Unstructured.IsList holds, kind unchecked, and applies the members instead of the
// object. The first arm matches the Flux stage, the second the Kustomize stage.
func rejectListEnvelope(componentName, kind string, object map[string]any) error {
	// Keyed on apimachinery's own predicate, not on the kind name. Unstructured.IsList
	// is "items is present AND is a []interface{}" (k8s.io/apimachinery v0.36.3), and a
	// kind check is wrong in BOTH directions: a typed ConfigMapList carries items and
	// would slip past `kind == "List"`, while a CRD whose kind merely ENDS in "List"
	// with no items is not a list at all and must keep compiling. Both directions are
	// pinned in TestPassthrough_ListShapedObjectIsRejected.
	//
	// Deliberately stricter than Kustomize for a kind NOT ending in "List" that carries
	// a top-level items array (go-kure/launcher#486). Kustomize keeps `{kind: Widget,
	// items: [<ConfigMap>]}` as one resource, but Flux's ReadObjects keys on this same
	// IsList predicate with no kind check, so the Widget is never applied and the
	// ConfigMap is, in whatever namespace it names. Narrowing this arm to the kind
	// suffix reopens exactly the smuggle it exists to stop; with scalar members,
	// ReadObjects instead fails the whole apply. Both pinned as rejected.
	//
	// Residual, scoped rather than merely disclosed: IsList requires exactly
	// []interface{}, so a Go-assembled []map[string]any under `items` would slip past.
	// NO IN-REPO PRODUCER CAN CONSTRUCT THAT TODAY — the authored path decodes through
	// yaml.v3 into any, which yields []interface{}, and the one production
	// ComponentLoweringRule, WorkerRule (worker.go), emits only `deployment`
	// components, never a passthrough; every other LowerComponent implementation in
	// this module is in a _test.go. Two events make it live, and the second is the
	// smaller edit: a production ComponentLoweringRule that emits a passthrough
	// component, OR any trait rule populating LoweringResult.Components, which
	// PositionTrait already permits (lowering.go loweringPositionRules) and which the
	// one production trait rule today, traits.ExposeRule, does not do. Whoever does
	// either is the trigger. Same class as go-kure/launcher#428.
	if (&unstructured.Unstructured{Object: object}).IsList() {
		return errors.Errorf(
			"passthrough component %q: 'object' is a list (kind %q with an 'items' array), but passthrough emits a single object verbatim — declare one passthrough component per object",
			componentName, kind)
	}
	// The second arm covers what IsList structurally cannot see, and it is the
	// silent-drop end of the same bug. IsList wants exactly []interface{}; an
	// authored `items: null` decodes to an untyped nil, so the envelope passed
	// every check here and Kustomize expanded it to ZERO objects with no error
	// (sigs.k8s.io/kustomize/api resource/factory.go's explicit-null branch). Worse
	// than the N-objects case it sits next to: with pruning enabled an empty
	// desired result also removes whatever the previous inventory held.
	//
	// Narrowed to a NULL items rather than a PRESENT one deliberately. A CRD may
	// legitimately carry an object-valued `items` field, and a bare presence
	// predicate rejects it — pinned as accepted/items_is_an_object,_not_a_sequence
	// and confirmed by mutation, where a presence predicate fails exactly that
	// subtest and nothing else.
	//
	// The two arms are ORDER-COUPLED and the coupling is not visible from the code:
	// a first-arm predicate widened to also match a null items would intercept these
	// cases and emit the list diagnostic instead. TestPassthrough_ListShapedObjectIsRejected
	// asserts on the diagnostic TEXT, not merely on error presence, so that masking
	// fails the suite rather than passing it.
	//
	// Gated on the kind suffix, unlike the first arm — deliberately, and for the
	// opposite reason. The first arm is keyed on IsList (apimachinery's own list
	// predicate), not the kind name — a typed ConfigMapList object is a list
	// regardless of what its kind happens to be called, so that arm intentionally
	// never consults the kind suffix at all. Kustomize's own expansion (kustomize/api
	// resource/factory.go, inlineAnyEmbeddedLists) instead checks
	// `strings.HasSuffix(kind, "List")` FIRST and never even looks at `items`
	// otherwise — so an ordinary object of a non-List kind that
	// happens to carry a field named `items` set to null is never treated as a list
	// envelope by Kustomize, and rejecting it here would be stricter than the tool
	// this arm exists to match. Flux's stage agrees: a null items is not IsList, so
	// ReadObjects applies the object as one. A CRD's `spec.items` colliding with this top-level
	// key is the concrete case: without the guard, `{apiVersion: example.com/v1, kind:
	// Widget, items: null}` fails to compile despite being one ordinary resource.
	if strings.HasSuffix(kind, "List") {
		if rawItems, hasItems := object["items"]; hasItems && isExplicitNull(rawItems) {
			return errors.Errorf(
				"passthrough component %q: object (kind %q) sets 'items' to null, which is an empty list envelope that expands to zero objects at apply time — passthrough emits a single object verbatim, so declare the object itself",
				componentName, kind)
		}
	}
	return nil
}

// PassthroughConfig implements stack.ApplicationConfig for passthrough components.
// It emits the declared object verbatim, defaulting metadata.name to the component
// name and (for namespaced objects) metadata.namespace to the build namespace.
//
// ClusterScoped is the authored clusterScoped property. It is one input to the
// object's scope, not the scope itself (resolveClusterScoped): false means the
// property was not authored unless scopeAuthored says an explicit false was,
// which only ToApplicationConfig can record. A config built as a struct literal
// therefore states true or nothing.
type PassthroughConfig struct {
	componentName string
	Namespace     string
	ClusterScoped bool
	scopeAuthored bool
	Object        map[string]any

	// policy is the environment policy ApplyPolicy was given, kept so that
	// Generate holds the object it emits to it again. Nil until then.
	policy oam.Policy
}

// ComponentName returns the OAM component this sub-app belongs to, for resource
// provenance attribution.
func (c *PassthroughConfig) ComponentName() string { return c.componentName }

// ApplyPolicy holds the object this component emits to the environment policy
// an authored workload is held to (enforceRenderedObjectPolicy, the check
// template delivery runs on the objects a chart renders): the image, pod
// security, resource, storage and replica rules, on every kind that check
// reads. A core Secret is refused under a policy that forbids explicit secrets
// (enforceExplicitSecretObject). An object of any other kind passes, a custom
// resource included, whatever it holds: the pods its controller creates are
// not covered. A nil policy checks nothing.
//
// The object is authored as a map, and the check reads Go types, so an object
// whose group, version and kind kure's scheme registers is decoded as that kind
// for the check alone (policyObject); what is emitted is the authored
// object's document, in the types of a decoded JSON one (Generate). One that
// cannot be read is refused, not passed: a registered kind that does not
// decode, and a workload kind in an API version the scheme does not register.
//
// The policy is kept, and Generate checks the object it is about to emit
// against it again: Object is an exported field, so the map checked here need
// not be the one emitted.
func (c *PassthroughConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}
	c.policy = p
	u, err := c.validated()
	if err != nil {
		return err
	}
	return enforcePassthroughPolicy(u, p)
}

// enforcePassthroughPolicy checks the object passthrough emits against p and
// names the object in what it refuses; each caller adds the component.
func enforcePassthroughPolicy(u *unstructured.Unstructured, p oam.Policy) error {
	err := enforceExplicitSecretObject(u, p)
	if err == nil {
		var obj client.Object
		if obj, err = policyObject(u); err == nil {
			err = enforceRenderedObjectPolicy(obj, p)
		}
	}
	if err != nil {
		return errors.Wrapf(err, "passthrough: object %s", renderedObjectRef(u))
	}
	return nil
}

// policyObject returns u in the form enforceRenderedObjectPolicy reads, by way
// of its JSON form, the form it is emitted in. An object whose group, version
// and kind kure's scheme registers comes back as the Go type of its kind,
// decoded as the manifests component and template delivery decode a document
// (kure's parser). That decode is the lenient one, so a field the vendored API
// type does not declare is not read: validated has already refused a workload or
// a claim that sets one, and no other kind is read here beyond what it
// declares. Any other object comes back unstructured,
// with the value types a decoded document has: the authored map holds what the
// YAML decoder or a Go caller put there (an int, a map[string]string), which
// the unstructured readers do not take.
//
// An object that does not come out so is an error, since nothing in it can be
// read: one that does not serialize, and one of a registered kind that does not
// decode as a single object of that kind (a field whose value has the wrong
// type, for one; an object the decoder of its kind panics on, for another, which
// kure's parser reports as an error: go-kure/kure#1009).
func policyObject(u *unstructured.Unstructured) (client.Object, error) {
	// unreadable is the refusal, with what kept the object from being read after
	// it when there is one: the text errors.Wrap gave, and the cause still in the
	// chain.
	unreadable := func(cause error) error {
		refusal := oam.NewPolicyRefusal(oam.RefusalUnreadableObject, "the object cannot be read, so it cannot be checked against environment policy")
		if cause == nil {
			return refusal
		}
		return errors.Errorf("%w: %w", refusal, cause)
	}
	raw, err := json.Marshal(u.Object)
	if err != nil {
		return nil, unreadable(err)
	}
	if err := kubernetes.RegisterSchemes(); err != nil {
		return nil, errors.Wrap(err, "registering the kinds this build can read")
	}
	if !kubernetes.Scheme.Recognizes(u.GroupVersionKind()) {
		var object map[string]any
		if err := utiljson.Unmarshal(raw, &object); err != nil {
			return nil, unreadable(err)
		}
		return &unstructured.Unstructured{Object: object}, nil
	}
	objs, err := kureio.ParseYAMLWithOptions(raw, kureio.ParseOptions{})
	if err != nil {
		return nil, unreadable(err)
	}
	if len(objs) != 1 {
		return nil, unreadable(nil)
	}
	return objs[0], nil
}

// Generate emits the declared object as an unstructured resource. It deep-copies
// the object first so the metadata fixup — and any later in-place mutation of
// labels/annotations by the delivery layer (kure stack.Bundle.Generate) — never
// touches the source component properties.
//
// It re-runs the list rejection on that copy. ToApplicationConfig is not the only
// way to reach here: Object is exported, so a caller holding the config can assign
// a list-shaped map to it, and a struct literal or a JSON decode of PassthroughConfig
// never calls the constructor at all. Checking the map that is about to be emitted,
// rather than trusting a check that ran on some earlier map, is what makes the
// rejection binding instead of advisory.
//
// For the same reason it holds that copy to the environment policy again, once
// ApplyPolicy has supplied one: the object checked there and the object emitted
// here are then the same document, a repeated key having been refused. A
// refusal here is the component's oam.ViolationError, as the transform reports
// one from ApplyPolicy.
func (c *PassthroughConfig) Generate(_ *stack.Application) ([]*client.Object, error) {
	u, err := c.validated()
	if err != nil {
		return nil, err
	}
	if c.policy != nil {
		if err := enforcePassthroughPolicy(u, c.policy); err != nil {
			return nil, oam.NewViolationError(c.componentName, err)
		}
	}
	// Last, after every check above. The emitted object is an
	// Unstructured, whose DeepCopyObject takes only the types of a decoded JSON document
	// and panics on any other, so it goes out in those (go-kure/launcher#1162). c.Object
	// stays as authored.
	jsonShape(u.Object)
	// A value that does not serialize stays as authored in the shaped copy, and
	// DeepCopyObject panics on it, so it is refused here, naming the field
	// (go-kure/launcher#1166).
	if field, ok := unshapedField(u.Object, ""); ok {
		return nil, errors.Errorf("passthrough component %q: object %s: %s holds a value that cannot be written as JSON; give it a string, a number, a boolean, a list or a map", c.componentName, renderedObjectRef(u), field)
	}
	out := client.Object(u)
	return []*client.Object{&out}, nil
}

// validated builds the object Generate emits, before Generate gives it the JSON
// types: a validated copy of Object, as authored, with the metadata defaults
// stamped on. ApplyPolicy checks the same construction, and ToApplicationConfig
// builds it once to fail early.
func (c *PassthroughConfig) validated() (*unstructured.Unstructured, error) {
	// Only a config that never went through ToApplicationConfig can be here with no
	// object — the constructor requires a non-empty apiVersion and kind. Emitting a
	// body consisting solely of the metadata stamped below would be a resource nobody
	// declared, so say what is wrong instead.
	if c.Object == nil {
		return nil, errors.Errorf("passthrough component %q: config has no 'object' — build it with PassthroughHandler.ToApplicationConfig", c.componentName)
	}
	obj := deepCopyMap(c.Object)
	if err := validateEmittableObject(c.componentName, obj); err != nil {
		return nil, err
	}

	u := &unstructured.Unstructured{Object: obj}
	clusterScoped, because, err := c.resolveClusterScoped(u)
	if err != nil {
		return nil, err
	}

	meta, _ := obj["metadata"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
		obj["metadata"] = meta
	}
	if name, ok := meta["name"].(string); !ok || name == "" {
		meta["name"] = c.componentName
	}
	ns, authoredNamespace := meta["namespace"].(string)
	authoredNamespace = authoredNamespace && ns != ""
	switch {
	case clusterScoped && authoredNamespace:
		return nil, errors.Errorf("passthrough component %q: object.metadata.namespace must not be set (%q): %s; the Kubernetes API rejects a namespace on a cluster-scoped object", c.componentName, ns, because)
	case !clusterScoped && !authoredNamespace:
		meta["namespace"] = c.Namespace
	}

	// A workload or a claim that sets a field its API type does not declare is
	// refused, policy or none: the policy check reads that type, so it cannot
	// see the field, and the manifests component and template delivery refuse
	// the same object (undeclared_fields.go).
	if err := refuseUndeclaredWorkloadFields(u); err != nil {
		return nil, errors.Wrapf(err, "passthrough component %q: object %s", c.componentName, renderedObjectRef(u))
	}

	// A repeated key is refused, since the readers of one document disagree on it.
	if err := refuseRepeatedKeys(u.Object); err != nil {
		return nil, errors.Wrapf(err, "passthrough component %q: object %s", c.componentName, renderedObjectRef(u))
	}

	// A container name used twice in a workload's pod is refused, policy or none
	// (go-kure/launcher#1253): the API server refuses such a pod. It runs first,
	// in the order the typed pod reader applies the checks
	// (validateAuthoredPodSpec), so every delivery path refuses an object with
	// the same first error.
	if err := refuseWorkloadContainerNames(u); err != nil {
		return nil, errors.Wrapf(err, "passthrough component %q: object %s", c.componentName, renderedObjectRef(u))
	}

	// The volumeMounts of a workload are held to the rules of an app.yaml mount,
	// and its pod spec to the lifecycle sleep and regular init container rules,
	// policy or none, as those of the kind components are (go-kure/launcher#1212,
	// go-kure/launcher#1223, go-kure/launcher#1236, go-kure/launcher#1237,
	// go-kure/launcher#1238).
	if err := refuseWorkloadPodRules(u); err != nil {
		return nil, errors.Wrapf(err, "passthrough component %q: object %s", c.componentName, renderedObjectRef(u))
	}

	return u, nil
}

// refuseWorkloadContainerNames holds the pod spec of u, when u is a workload
// kure's scheme registers, to checkPodSpecContainerNames. u is read typed, and a
// workload that cannot be read passes, exactly as for refuseWorkloadPodRules.
func refuseWorkloadContainerNames(u *unstructured.Unstructured) error {
	if !isWorkloadGVK(u.GroupVersionKind()) {
		return nil
	}
	obj, err := policyObject(u)
	if err == nil {
		return validateObjectContainerNames(obj)
	}
	return nil
}

// refuseWorkloadPodRules holds the pod spec of u, when u is a workload kure's
// scheme registers, to validateObjectMounts and then validateObjectPodRules. u
// is authored as a map, so the rules read it typed by way of policyObject, as
// the policy check does; validated has already refused a workload that sets a
// field its type does not declare, so that view loses nothing the rules read. A
// kind that is no workload is not read. A workload that cannot be read (an API
// version the scheme does not register, or a document that does not decode as
// its kind) passes here, and is the policy check's to refuse as unreadable,
// which every transform runs. go-kure/launcher#1223, go-kure/launcher#1238.
func refuseWorkloadPodRules(u *unstructured.Unstructured) error {
	if !isWorkloadGVK(u.GroupVersionKind()) {
		return nil
	}
	// policyObject's error is the policy check's refusal to make, not these
	// rules': an object it cannot read has no pod spec here to hold.
	obj, err := policyObject(u)
	if err != nil {
		return nil
	}
	if err := validateObjectMounts(obj); err != nil {
		return err
	}
	return validateObjectPodRules(obj)
}

// refuseRepeatedKeys refuses an object that holds a key twice in one JSON object, at
// any depth, and names the field. An authored map cannot: the repeat comes from a value
// that serializes itself, such as a json.RawMessage a Go caller built. The readers of
// such a document disagree. The typed decode the policy reads merges a repeated
// object member (a selector given twice keeps the matchLabels of the first), while the
// generic decode that gives the emitted object its JSON types takes the last one (an
// empty selector), so the object checked is not the object emitted
// (go-kure/launcher#1162). Refusing is strict mode: no key is picked silently.
//
// An object that does not serialize is passed on here: policyObject refuses it under a
// policy, and Generate refuses it, naming the field, once it is shaped
// (go-kure/launcher#1166).
func refuseRepeatedKeys(object map[string]any) error {
	raw, err := json.Marshal(object)
	if err == nil {
		// UseNumber: the walk reads a number as its text, so an integer beyond
		// float64 range is not an unreadable document (go-kure/launcher#1176).
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var field string
		if field, err = repeatedKey(dec, ""); err != nil {
			return errors.Wrap(err, "the object cannot be read")
		}
		if field != "" {
			return errors.Errorf("%s is given more than once in the same object; give each key once", field)
		}
	}
	return nil
}

// repeatedKey walks the next JSON value of dec and returns the path of the first key
// that appears twice in one object, or "" if none does.
func repeatedKey(dec *json.Decoder, path string) (string, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return "", nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return "", err
			}
			key, _ := keyTok.(string)
			field := fieldPath(path, key)
			if seen[key] {
				return field, nil
			}
			seen[key] = true
			if found, err := repeatedKey(dec, field); err != nil || found != "" {
				return found, err
			}
		}
	case '[':
		for i := 0; dec.More(); i++ {
			if found, err := repeatedKey(dec, path+"["+strconv.Itoa(i)+"]"); err != nil || found != "" {
				return found, err
			}
		}
	}
	_, err = dec.Token()
	return "", err
}

// resolveClusterScoped reports whether the object passthrough emits is
// cluster-scoped, and the reason in words a refusal can quote. It follows the
// precedence the manifests component gives a scopeOverrides entry
// (resolveObjectScope), with clusterScoped in the entry's place:
//
//   - A kind whose scope the Kubernetes API itself governs (isAPIGovernedScope:
//     a built-in kind, a CustomResourceDefinition) has the scope kure's table
//     gives it. A clusterScoped that says otherwise is refused. manifests
//     ignores such an override, since its list may name kinds the source does
//     not hold; here the property is a statement about the one object, so a
//     wrong one is an error in the document and not a spare entry.
//   - For any other kind an authored clusterScoped decides, true or false: the
//     author knows the CustomResourceDefinition the cluster serves, and the
//     table only what the module pinned at build time declared.
//   - Not authored, the table decides for a kind kure registers. A kind it does
//     not know is treated as namespaced, the default this component always had:
//     failing closed, as manifests does on an unknown scope, would refuse every
//     custom resource that relies on it.
//
// No CustomResourceDefinition is consulted (the crdScopes of manifest.Scope):
// the component holds one object.
func (c *PassthroughConfig) resolveClusterScoped(u *unstructured.Unstructured) (clusterScoped bool, because string, err error) {
	kind := u.GetAPIVersion() + " " + u.GetKind()
	table := manifest.Scope(u, nil)
	if isAPIGovernedScope(u) {
		cluster := table == manifest.ScopeCluster
		switch {
		case c.ClusterScoped && !cluster:
			return false, "", errors.Errorf("passthrough component %q: clusterScoped is true, but the Kubernetes API defines %s as namespaced; remove clusterScoped", c.componentName, kind)
		case c.scopeAuthored && !c.ClusterScoped && cluster:
			return false, "", errors.Errorf("passthrough component %q: clusterScoped is false, but the Kubernetes API defines %s as cluster-scoped; remove clusterScoped", c.componentName, kind)
		}
		return cluster, "the Kubernetes API defines " + kind + " as cluster-scoped", nil
	}
	if c.ClusterScoped || c.scopeAuthored {
		return c.ClusterScoped, "clusterScoped is true", nil
	}
	return table == manifest.ScopeCluster, kind + " is registered as cluster-scoped (set clusterScoped: false if the cluster serves it namespaced)", nil
}

// deepCopyMap returns a deep copy of a decoded YAML/JSON map: nested maps and
// slices are cloned, scalars (immutable) are copied by value. Unlike
// runtime.DeepCopyJSON it does not assume JSON-typed scalars, so it is safe for
// whatever scalar types the OAM decoder produces.
func deepCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

// deepCopyValue detaches one value. The two fast paths are the shapes the authored
// path actually produces (yaml.v3 into any yields map[string]any and []any); the
// reflection arm exists for the shapes a Go-assembled body can hold and the fast
// paths cannot see — map[string]string, []string, []int32, named map/slice types,
// arrays. Those aliased through the previous `default: return v`, which made the
// freeze partial in exactly the way that is invisible from the authored path.
//
// Two deliberate limits, stated rather than left to be rediscovered:
//
//   - A typed nil (map[string]any(nil), []string(nil), a nil pointer) is returned
//     as it stands. Copying it through the container arms would turn a null into an
//     empty {} or [], which changes the emitted document — isExplicitNull is the same
//     predicate the null-items arm keys on, so both agree on what a null is.
//   - A value that merely CONTAINS a pointer (a pointer field, resource.Quantity's
//     *inf.Dec, time.Time's *Location) is copied by value and its pointee still
//     aliases. Chasing pointees generically is where a reflective copy starts
//     inventing semantics — an unexported field or a self-referential graph — and no
//     shape reaching this function today has one. Copying the containers is what the
//     freeze needs; copying the world is not.
func deepCopyValue(v any) any {
	// Checked before the fast paths below, not after: a typed-nil []any matches
	// `case []any:` in a type switch (it's nil of that dynamic type), and
	// `make([]any, len(nil))` silently produces a non-nil empty slice — turning a
	// null into `[]`, the exact collapse this function exists to prevent. The
	// map fast path (deepCopyMap) already special-cases m == nil on its own, so
	// this move changes only the slice path's behavior, not the map path's.
	if isExplicitNull(v) {
		return v
	}
	switch t := v.(type) {
	case map[string]any:
		return deepCopyMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = deepCopyValue(e)
		}
		return out
	}
	rv := reflect.ValueOf(v)
	elem := func(i int) reflect.Value { return copiedValue(rv.Index(i).Interface(), rv.Type().Elem()) }
	switch rv.Kind() {
	case reflect.Map:
		out := reflect.MakeMapWithSize(rv.Type(), rv.Len())
		valueType := rv.Type().Elem()
		iter := rv.MapRange()
		for iter.Next() {
			// Keys are left as they stand: a map key must be comparable, so it can
			// hold no slice or map to detach, and copying a pointer key would change
			// which entry it is.
			out.SetMapIndex(iter.Key(), copiedValue(iter.Value().Interface(), valueType))
		}
		return out.Interface()
	case reflect.Slice:
		out := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out.Index(i).Set(elem(i))
		}
		return out.Interface()
	case reflect.Array:
		out := reflect.New(rv.Type()).Elem()
		for i := 0; i < rv.Len(); i++ {
			out.Index(i).Set(elem(i))
		}
		return out.Interface()
	default:
		return v
	}
}

// copiedValue is deepCopyValue with a reflect.Value result assignable to t.
//
// The nil branch is not defensive. A nil interface has no reflect.Value of its own:
// reflect.ValueOf(nil) is the ZERO Value, and the two uses above fail differently on
// it — Set panics, while SetMapIndex takes a zero Value as "delete this key" and
// silently drops the entry. An element of an `any`-valued collection is nil whenever
// a Go-assembled body writes an explicit null into one, which is exactly the shape
// this package spends two rejection arms reasoning about.
func copiedValue(v any, t reflect.Type) reflect.Value {
	c := deepCopyValue(v)
	if c == nil {
		return reflect.Zero(t)
	}
	return reflect.ValueOf(c)
}

// jsonShape rewrites m, a copy no one else holds, into the types of a decoded JSON
// document: map[string]any, []any, string, bool, int64, float64, nil and json.Number (a
// number that an int64 or a float64 would not hold exactly, with its own digits). An
// Unstructured's DeepCopyObject (runtime.DeepCopyJSON) takes nothing else and panics on
// the rest, and the unstructured readers (NestedString, NestedSlice, ...) read the same
// types. The authored map holds whatever the YAML decoder or a Go caller put there, so
// the shape is given to what is emitted and the authored object is left as it is.
func jsonShape(m map[string]any) {
	for k, v := range m {
		m[k] = jsonValue(v)
	}
}

// jsonValue is the JSON-typed form of v, with the document it serializes to unchanged:
// an integer is an int64 (a uint64 beyond int64 a json.Number of its digits), a
// json.Number the type its text has (see numbered), a float32
// the float64 of its shortest decimal form, a named scalar its underlying one, a typed
// map with string keys a map[string]any, a slice or array an []any, a []byte the base64
// string encoding/json writes, and a typed nil a null. A value with a form of its own
// (it marshals itself, or is a struct, a pointer or a map with other keys) is decoded
// from the JSON it serializes to. A map[string]any and an []any are rewritten in place.
// A value that does not serialize is returned as it is, for Generate to refuse
// (unshapedField).
func jsonValue(v any) any {
	switch t := v.(type) {
	case nil, string, bool, int64, float64:
		return v
	case json.Number:
		return numbered(t) // the type its text has, as in a decoded document (go-kure/launcher#1167)
	case map[string]any:
		jsonShape(t)
		return t
	case []any:
		for i, e := range t {
			t[i] = jsonValue(e)
		}
		return t
	}
	switch v.(type) {
	case json.Marshaler, encoding.TextMarshaler:
		return viaJSON(v)
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Bool:
		return rv.Bool()
	case reflect.String:
		return rv.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		u := rv.Uint()
		if u > math.MaxInt64 {
			return json.Number(strconv.FormatUint(u, 10)) // its own digits, as authored
		}
		return int64(u)
	case reflect.Float32:
		f, _ := strconv.ParseFloat(strconv.FormatFloat(rv.Float(), 'g', -1, 32), 64)
		return f
	case reflect.Float64:
		return rv.Float()
	case reflect.Map:
		if rv.IsNil() {
			return nil
		}
		if rv.Type().Key().Kind() != reflect.String {
			return viaJSON(v) // an integer or text-marshaling key is written as a string
		}
		out := make(map[string]any, rv.Len())
		for iter := rv.MapRange(); iter.Next(); {
			out[iter.Key().String()] = jsonValue(iter.Value().Interface())
		}
		return out
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice {
			if rv.IsNil() {
				return nil
			}
			et := rv.Type().Elem()
			if selfMarshaling(reflect.PointerTo(et)) || et.Kind() == reflect.Struct || et.Kind() == reflect.Array {
				// The elements of a slice are addressable, and so is whatever they hold, at
				// any depth, so encoding/json also takes a marshaler on a pointer there; the
				// element copies below are not addressable.
				return viaJSON(v)
			}
			if et.Kind() == reflect.Uint8 {
				return base64.StdEncoding.EncodeToString(rv.Bytes())
			}
		}
		out := make([]any, rv.Len())
		for i := range out {
			out[i] = jsonValue(rv.Index(i).Interface())
		}
		return out
	case reflect.Struct, reflect.Pointer:
		return viaJSON(v)
	case reflect.Invalid, reflect.Complex64, reflect.Complex128, reflect.Chan, reflect.Func,
		reflect.Interface, reflect.UnsafePointer:
		// None of these serializes: left as it is, for Generate to refuse
		// (go-kure/launcher#1166).
	}
	return v
}

// selfMarshaling reports whether type t has a marshaler encoding/json writes it through
// (on a pointer type t, one of its element's too).
func selfMarshaling(t reflect.Type) bool {
	return t.Implements(reflect.TypeFor[json.Marshaler]()) || t.Implements(reflect.TypeFor[encoding.TextMarshaler]())
}

// viaJSON is v as the JSON document it serializes to, decoded into the JSON types, for a
// value that has a form of its own (a json.RawMessage, a time, a quantity, a struct). A
// value that does not serialize is returned as it is, for Generate to refuse.
func viaJSON(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return v
	}
	return numbered(out)
}

// numbered gives the numbers of a document decoded with UseNumber the types of one
// decoded as the API machinery does: an int64 where the integer fits, a float64 for a
// fraction or an exponent that a float64 holds exactly, and a json.Number, with its own
// digits, for any other number (which the API machinery would round to a float64).
func numbered(v any) any {
	switch t := v.(type) {
	case json.Number:
		// encoding/json writes the zero value of json.Number as 0.
		if t == "" {
			return int64(0)
		}
		// Text that is not a JSON number ("+12", "012") is kept, for Generate to
		// refuse: Int64 would read it as a valid integer.
		if !jsonNumberGrammar.MatchString(t.String()) {
			return t
		}
		if i, err := t.Int64(); err == nil {
			return i
		}
		if !strings.ContainsAny(t.String(), ".eE") {
			return t
		}
		if f, ok := exactFloat(t); ok {
			return f
		}
		return t
	case map[string]any:
		for k, e := range t {
			t[k] = numbered(e)
		}
	case []any:
		for i, e := range t {
			t[i] = numbered(e)
		}
	}
	return v
}

// unshapedField returns the path of the first value under v (keys in sorted order) that
// is not one jsonShape gives: nil, a string, a bool, an int64, a finite float64 or a
// json.Number in the JSON number grammar. It returns "" and false when there is none.
// path is the path of v, "" for the object itself.
func unshapedField(v any, path string) (string, bool) {
	switch t := v.(type) {
	case nil, string, bool, int64:
		return "", false
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return path, true
		}
		return "", false
	case json.Number:
		if !jsonNumberGrammar.MatchString(t.String()) {
			return path, true
		}
		return "", false
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if field, ok := unshapedField(t[k], fieldPath(path, k)); ok {
				return field, true
			}
		}
		return "", false
	case []any:
		for i, e := range t {
			if field, ok := unshapedField(e, path+"["+strconv.Itoa(i)+"]"); ok {
				return field, true
			}
		}
		return "", false
	}
	return path, true
}

// fieldPath is the path of key under path: "path.key", or "path[\"key\"]" when the key is
// empty or holds a dot, a bracket or a quote, so that two different fields never share a
// path.
func fieldPath(path, key string) string {
	if key == "" || strings.ContainsAny(key, `.[]"`) {
		return path + "[" + strconv.Quote(key) + "]"
	}
	if path == "" {
		return key
	}
	return path + "." + key
}

// Bounds on a number's text before it is compared exactly: the comparison builds a
// rational whose size follows the exponent, so authored input must not set that size
// (the text of "1e-9999999" is short, and its float64 is 0).
const (
	maxExactNumberLen      = 1000
	maxExactNumberExponent = 400
)

// jsonNumberGrammar is the JSON number grammar. A json.Number is only text, so
// "true", "[]" and "0x10" are not numbers however a decoder would read them.
var jsonNumberGrammar = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// exactFloat is n as a float64 where that is the same number: its shortest decimal form
// denotes the value of n exactly. It reports false for a number that is not in the JSON
// number grammar, is too long or has too large an exponent to compare, is beyond the
// float64 range, or that a float64 would round, so that the caller keeps n with its own
// digits.
func exactFloat(n json.Number) (float64, bool) {
	s := n.String()
	if len(s) > maxExactNumberLen || !jsonNumberGrammar.MatchString(s) {
		return 0, false
	}
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		exp, err := strconv.Atoi(s[i+1:])
		if err != nil || exp > maxExactNumberExponent || exp < -maxExactNumberExponent {
			return 0, false
		}
	}
	f, err := n.Float64()
	if err != nil {
		return 0, false
	}
	want, ok := new(big.Rat).SetString(s)
	if !ok {
		return 0, false
	}
	got, ok := new(big.Rat).SetString(strconv.FormatFloat(f, 'g', -1, 64))
	if !ok || want.Cmp(got) != 0 {
		return 0, false
	}
	return f, true
}
