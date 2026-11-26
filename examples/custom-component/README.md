# Custom component type without Go code (spike)

Spike for go-kure/launcher#102. Not a supported feature.

```bash
kurel build examples/custom-component --profile examples/custom-component/cluster.yaml
```

`definitions/` declares two types:

| File | What it declares |
|---|---|
| `sparkapplication.yaml` | A `ComponentDefinition`: the type `spark-application`, one SparkApplication, validated offline against the pinned CRD. |
| `sparkbatch.yaml` | A `Model` over that type, with four inputs. |

The CRD under `definitions/crds/` is copied unchanged from
[kubeflow/spark-operator](https://github.com/kubeflow/spark-operator) (Apache-2.0), tag `v2.1.0`,
file `charts/spark-operator-chart/crds/sparkoperator.k8s.io_sparkapplications.yaml`,
sha256 `0f6a7d761ab9b53ddb8eebbf6b2b906b0e3fb9ffd63559cb90d18d6357e535be`.

## Not handled

- CEL rules (`x-kubernetes-validations`); this CRD has none.
- API-server defaults.
- Policy on the pod templates the CR embeds. `driver` and `executor` can set a security context,
  host networking and volumes, and no launcher policy reads them. This blocks production use.
- Traits, published outputs, and a nested property schema.
