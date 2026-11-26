package probe

import (
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// Fill is one value a probe document leaves out and the library supplies, as the
// provenance report shows it. Its two sources are the ones observed with the
// probe policy on and off; an empty one means the report holds no entry at the
// path in that state.
type Fill struct {
	// Component is the component the entry belongs to. Member is the member of a
	// type that lowers to one: the report speaks at the member's place, with the
	// member's component type (empty for a handler-built type's own entries).
	Component, Member string
	// Trait is the type of a trait of the component, for a fill the report holds
	// under that trait: Path is then a path of the trait's properties, written at
	// the trait at TraitIndex among the component's traits. Empty for an entry of
	// the component itself.
	Trait      string
	TraitIndex int
	// Path is the property path of the entry. Checks A and B write a value at the
	// same path on the authored component, so a row whose key the lowering renames
	// carries no value.
	Path string
	// WithPolicy and WithoutPolicy are the source kinds observed at Path with the
	// probe policy and with none.
	WithPolicy, WithoutPolicy oam.SourceKind
	// ValueWithPolicy and ValueWithoutPolicy are the value the fill supplies in
	// each state, written as an author would write it. Authoring it at Path must
	// give the same objects as leaving it out (check B). Nil leaves the state out
	// of B: the value is not one an author can write, or no generated object
	// shows it, so comparing objects could not tell a right value from a wrong one.
	ValueWithPolicy, ValueWithoutPolicy any
	// Other is a second value an author can write at Path. Writing it must change
	// the generated objects: equality of the objects for the value above proves
	// nothing where the library never puts the field in an object, and a changed
	// object for Other shows the path reaches one. A Fill with a value and no
	// Other fails check B.
	Other any
}

// Probe is one document and what was observed when it ran.
type Probe struct {
	Name       string
	Components []oam.Component
	// Capabilities are the capability bindings the transform runs with, by
	// capability name: the platform's rendering a trait reads (a certificate's
	// issuer, a route's gateway).
	Capabilities map[string]oam.CapabilityBinding
	// Fills lists every entry of source policy or builtin the report holds for
	// the document, in either state. An entry the report holds that no Fill lists
	// fails check B.
	Fills []Fill
	// RefusedWithPolicy and RefusedWithoutPolicy, when set, say the document is
	// refused in that state, with this text in the error. A refused state has no
	// Fills to compare.
	RefusedWithPolicy, RefusedWithoutPolicy string
	// LeftToCluster names, by path, a default the schema declares and the handler
	// leaves unset for the cluster to apply, with the reason. Check A expects
	// writing such a default to change the objects, and fails when it does not.
	LeftToCluster map[string]string
	// Unreported names, by path, a declared default the handler applies and the
	// report holds no entry for, with the reason: a known gap in what the report
	// says. Check A fails when an entry here is no longer a gap.
	Unreported map[string]string
	// NotAuthorable names, by path, a declared default the document's own rule
	// refuses to have written (a scaler trait owns the replica count), with the text
	// the refusal must contain. Check A cannot compare objects for it, so it checks
	// nothing there; the entry fails when writing the default is accepted, or is
	// refused with other text.
	NotAuthorable map[string]string
}

// Run transforms comps as one application under p and returns the provenance
// report and the generated objects, marshalled. It is the pipeline a consumer
// runs: validate the authored properties, transform with provenance, generate.
func Run(tr *oam.Transformer, comps []oam.Component, p oam.Policy) (*oam.Provenance, []byte, error) {
	return RunWith(tr, comps, p, nil)
}

// RunWith is Run with the capability bindings the transform runs with.
func RunWith(tr *oam.Transformer, comps []oam.Component, p oam.Policy, caps map[string]oam.CapabilityBinding) (*oam.Provenance, []byte, error) {
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "probe", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: comps},
	}
	validated, err := tr.ValidateAuthoredProperties(app)
	if err != nil {
		return nil, nil, errors.Wrap(err, "validate authored properties")
	}
	cluster, _, prov, err := tr.TransformWithProvenance(validated, oam.TransformContext{Namespace: "default", Policy: p, Capabilities: caps})
	if err != nil {
		return nil, nil, errors.Wrap(err, "transform")
	}
	// The cluster is not compared: the engine wraps its application configs in a
	// type whose fields are unexported, so marshalling the cluster would hide the
	// values a policy default fills.
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		return nil, nil, errors.Wrap(err, "generate")
	}
	out, err := yaml.Marshal(apps)
	if err != nil {
		return nil, nil, errors.Wrap(err, "marshal the generated objects")
	}
	return prov, out, nil
}
