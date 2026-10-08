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
	// validateName, when set, refuses what the object's name and decoded
	// together make invalid in the objects another controller names after it.
	// name is the one the object takes (oam.Component.ObjectName), and
	// componentName the component's, to say where the name came from.
	validateName func(name, componentName string, decoded *T) error
	// checkName, when set, refuses an object name the API requires to follow
	// from what was decoded (an APIService is named <version>.<group>). It is
	// called by Generate, with the name build is given, since an object named
	// after the application has no name before then.
	checkName func(name string, decoded *T) error
	// required lists the fields the API requires that T encodes whether or not
	// they were authored, so that the decoded value does not show the omission
	// (refuseUnauthoredRequired). Nil for a kind with none.
	required map[string]string
	// defaultedZeros lists the fields of T on which an authored 0, false or ""
	// cannot be carried (refuseUncarriedSpecValues). The zero value lists
	// none: it is the value of every kind whose type has no such field, which
	// the tests named at config hold.
	defaultedZeros defaultedZeroFields
	// defaultedZerosFor, when set, is the list for decoded, in place of
	// defaultedZeros: for a type on which the API server defaults a field
	// only under another field's value, as it does a container port's
	// hostPort under hostNetwork (podSpecDefaultedZeros).
	defaultedZerosFor func(decoded *T) defaultedZeroFields
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
// unauthored (refuseUnauthoredRequired). A field on which an authored 0, false
// or "" cannot be carried must be in the kind's defaultedZeros, or in the list
// defaultedZerosFor gives for the decoded value, which refuses that value:
// TestPolicyFreeKinds_NoDefaultedZeros holds each decoded type's
// field comments to having no number or boolean of the kind, as far as they
// state a default in a form it recognises, and
// TestMonitoringKinds_DefaultedZeros and TestExternalSecretsKinds_DefaultedZeros
// hold the list of each kind whose types publish no comment to the default
// markers of their source, in both directions.
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
	zeros := k.defaultedZeros
	if k.defaultedZerosFor != nil {
		zeros = k.defaultedZerosFor(decoded)
	}
	if err := refuseUncarriedSpecValues(authored, decoded, zeros); err != nil {
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
	if k.validateName != nil {
		if err := k.validateName(component.ObjectName(), component.Name, decoded); err != nil {
			return nil, err
		}
	}
	return &policyFreeKindConfig[T]{kind: k, objectName: componentObjectName(component), metadata: component.ObjectMetadata(), decoded: decoded}, nil
}

// objectIdentityKeys are the json keys of an object that are launcher's to
// set, on every kind component: its type, and the metadata that holds its name
// and namespace. The labels and annotations of that metadata are authored as
// the `labels` and `annotations` properties, which the engine reads.
var objectIdentityKeys = []string{"apiVersion", "kind", "metadata"}

// refuseObjectIdentityKeys refuses a property that names one of
// objectIdentityKeys, whatever its value, a null included. The match is
// case-insensitive, as the decode's is, so `Kind` cannot reach the object's
// type either. Keys are read in sorted order, so the one reported is the same
// on every build.
func refuseObjectIdentityKeys(props map[string]any) error {
	for _, key := range slices.Sorted(maps.Keys(props)) {
		if slices.ContainsFunc(objectIdentityKeys, func(id string) bool { return strings.EqualFold(id, key) }) {
			return errors.Errorf("%s: not authorable: launcher sets the object's kind, apiVersion and metadata (its name is the component's, or the one %s gives it; its labels and annotations are the %s and %s properties)",
				key, oam.ObjectNameProperty, oam.ObjectLabelsProperty, oam.ObjectAnnotationsProperty)
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
	// metadata is the labels and annotations authored for the object
	// (oam.Component.ObjectMetadata).
	metadata oam.ObjectMetadata
	decoded  *T
}

// ApplyPolicy is a no-op, for any policy: see the top of this file.
func (c *policyFreeKindConfig[T]) ApplyPolicy(oam.Policy) error {
	return nil
}

// Generate emits the kind's one object, under the object name the config
// carries, else named after the application, once the kind's checkName, if
// any, accepts that name. Its labels and annotations are the authored ones:
// the handler adds none of its own.
func (c *policyFreeKindConfig[T]) Generate(app *stack.Application) ([]*client.Object, error) {
	name := kindObjectName(c.objectName, app.Name)
	if c.kind.checkName != nil {
		if err := c.kind.checkName(name, c.decoded); err != nil {
			return nil, err
		}
	}
	return kindObject(c.kind.build(name, app.Namespace, c.decoded), c.metadata)
}
