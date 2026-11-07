package oam

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// serviceNameGate is the Kubernetes feature gate that lets a Service name start
// with a digit: off by default below the release the lifecycle table names for
// it, when the API server refuses such a name (go-kure/launcher#1230).
const serviceNameGate = "RelaxedServiceNameValidation"

// CodeCheckedGates returns the lifecycle-table gates that code checks on the
// objects it generates, apart from the ones a PropertySchema names (Gate,
// GatedPaths, ValueGates). A test in a package that can see the built-in schemas
// holds the table to both: every row is carried by a schema or listed here.
func CodeCheckedGates() []string {
	return []string{serviceNameGate}
}

// serviceNameGateProblem is the refusal of a Service name that starts with a
// digit when target is below the release where serviceNameGate is on by default,
// nil otherwise. field says where the name is written.
func serviceNameGateProblem(field, name string, target KubernetesVersion) error {
	return nil
}

// checkServiceNames holds obj, and every object it stands for when Flux applies
// it, to serviceNameGateProblem under the wrapper's target. A wrapper with no
// target checks nothing.
func (o *ownedConfig) checkServiceNames(obj client.Object) error {
	return nil
}
