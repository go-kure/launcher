# Component definitions (spike)

Spike for go-kure/launcher#102. Not a supported API.

A `ComponentDefinition` names one custom resource kind and pins its CustomResourceDefinition
file by SHA-256. `Load` reads both, `Register` makes the type authorable on a Transformer and
declares its identity, `<kind>.<group>@<version>`, so a model can name it as a part. A
component's properties are the resource's `spec`, validated offline against the CRD's
structural schema, unknown fields refused. The object is emitted by the `passthrough` handler.

No policy reads the pod templates a resource may embed. See
[the example](../../../examples/custom-component/README.md) for what else is left out.
