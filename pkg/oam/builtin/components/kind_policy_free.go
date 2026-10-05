package components

import (
	"maps"
	"slices"
	"strings"

	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file holds what the kind components share to which no dimension of the
// environment policy applies (go-kure/launcher#790): the object runs no pod,
// holds no image, requests no storage and has no replica count. Such a kind is
// one policyFreeKind value: the upstream type its properties decode into, what
// the strict decode cannot refuse, and how the object is built. Its handler
// keeps its own exported type, CanHandle, PropertySchema and contract
// metadata; its ToApplicationConfig is one call to config.
//
// The kinds written before this file (namespace, limitrange, resourcequota)
// keep their own copies of the same steps.

// policyFreeKind describes one such kind. T is the type the properties decode
// into: the object's spec type or, for a kind with no spec type, the object
// itself (wholeObject).
type policyFreeKind[T any] struct {
	// upstream names T in the decode error ("storage.k8s.io/v1 CSIDriverSpec").
	upstream string
	// wholeObject says T is the object, not its spec: the properties are the
	// object's own top-level fields, and its kind, apiVersion and metadata,
	// which are launcher's to set, are refused (refuseObjectIdentityKeys).
	wholeObject bool
	// validate, when set, refuses what the strict decode cannot: a field the
	// API requires and the author left out or empty.
	validate func(decoded *T) error
	// required lists the fields the API requires that T encodes whether or not
	// they were authored, so that the decoded value does not show the omission
	// (refuseUnauthoredRequired). Nil for a kind with none.
	required map[string]string
	// build returns the object: the base library's identity-only constructor
	// for the name (and the namespace, unless the kind is cluster-scoped) and
	// a deep copy of decoded. name is the one the object takes: the
	// component's object name where the engine resolved another than the
	// component's, else the application's (kindObjectName). It is called once
	// per Generate, and must not hand out decoded itself.
	build func(name, namespace string, decoded *T) client.Object
}

// config decodes a component into the kind's config, under the package's null
// contract and the strict decode every spec-projecting kind uses
// (decodeKindSpec), and refuses two spellings of one field
// (refuseUncarriedSpecValues) and a required field the type would write
// unauthored (refuseUnauthoredRequired). No field of a policy-free kind may be
// one on which an authored 0 or false cannot be carried:
// TestPolicyFreeKinds_NoDefaultedZeros holds each decoded type's field
// comments to that, as far as they state a default in a form it recognises,
// and TestMonitoringKinds_NoDefaultedZeros the types that publish none, by
// the default markers of their source.
func (k *policyFreeKind[T]) config(component *oam.Component) (stack.ApplicationConfig, error) {
	if k.wholeObject {
		if err := refuseObjectIdentityKeys(component.Properties); err != nil {
			return nil, err
		}
	}
	decoded, authored, err := decodeKindSpec[T](component.Properties, k.upstream)
	if err != nil {
		return nil, err
	}
	if err := refuseUncarriedSpecValues(authored, decoded, defaultedZeroFields{}); err != nil {
		return nil, err
	}
	if err := refuseUnauthoredRequired(authored, k.required); err != nil {
		return nil, err
	}
	if k.validate != nil {
		if err := k.validate(decoded); err != nil {
			return nil, err
		}
	}
	return &policyFreeKindConfig[T]{kind: k, objectName: componentObjectName(component), decoded: decoded}, nil
}

// objectIdentityKeys are the json keys of an object that are launcher's to
// set, on every kind component: its type, and the metadata that holds its name
// and namespace.
var objectIdentityKeys = []string{"apiVersion", "kind", "metadata"}

// refuseObjectIdentityKeys refuses a property that names one of
// objectIdentityKeys, whatever its value, a null included. The match is
// case-insensitive, as the decode's is, so `Kind` cannot reach the object's
// type either. Keys are read in sorted order, so the one reported is the same
// on every build.
func refuseObjectIdentityKeys(props map[string]any) error {
	for _, key := range slices.Sorted(maps.Keys(props)) {
		if slices.ContainsFunc(objectIdentityKeys, func(id string) bool { return strings.EqualFold(id, key) }) {
			return errors.Errorf("%s: not authorable: launcher sets the object's kind, apiVersion and metadata (its name is the component's, or the one %s gives it)", key, oam.ObjectNameProperty)
		}
	}
	return nil
}

// policyFreeKindConfig implements stack.ApplicationConfig for a policyFreeKind.
// It is unexported, unlike the config of a kind written before this file: only
// config builds one, so what it holds has passed the kind's refusals and
// Generate does not repeat them.
type policyFreeKindConfig[T any] struct {
	kind *policyFreeKind[T]
	// objectName names the object (oam.Component.ObjectName). Empty for the
	// application's name.
	objectName string
	decoded    *T
}

// ApplyPolicy is a no-op, for any policy: see the top of this file.
func (c *policyFreeKindConfig[T]) ApplyPolicy(oam.Policy) error {
	return nil
}

// Generate emits the kind's one object, under the object name the config
// carries, else named after the application. The handler adds no label and no
// annotation.
func (c *policyFreeKindConfig[T]) Generate(app *stack.Application) ([]*client.Object, error) {
	obj := c.kind.build(kindObjectName(c.objectName, app.Name), app.Namespace, c.decoded)
	return []*client.Object{&obj}, nil
}
