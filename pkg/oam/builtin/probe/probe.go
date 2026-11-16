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
	// Component is the component the entry belongs to. The probes hold handler-built
	// types, whose entries carry no member.
	Component string
	// Path is the property path the author would have written.
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
}

// Run transforms comps as one application under p and returns the provenance
// report and the generated objects, marshalled. It is the pipeline a consumer
// runs: validate the authored properties, transform with provenance, generate.
func Run(tr *oam.Transformer, comps []oam.Component, p oam.Policy) (*oam.Provenance, []byte, error) {
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
	cluster, _, prov, err := tr.TransformWithProvenance(validated, oam.TransformContext{Namespace: "default", Policy: p})
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
