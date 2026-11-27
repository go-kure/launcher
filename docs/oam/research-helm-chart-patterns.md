# Helm chart pattern survey: which component models to ship

Research for go-kure/launcher#1637. It asks which configuration patterns public Helm charts share, which of them a [component model](https://github.com/go-kure/launcher/blob/main/pkg/oam/model/README.md) can express, and which need a Go lowering rule. It proposes models. It builds none, and nothing here is scheduled.

Read against launcher `43d483e9`, with 120 built-in component types (115 handlers and 5 lowering rules: `webservice`, `worker`, `helm`, `postgresql`, `oci`) and 18 traits (17 handlers and the `expose` lowering rule). At that commit `kurel` imports no model package, so `kurel build` loads no models (go-kure/launcher#1570), and go-kure/launcher#1706 on expression allocation limits is open.

## Result

Four patterns need Go, each in part:

- **The hash behind a restart on config change.** 13 of 27 application charts hash something in a template (template count); 13 render a checksum annotation on a pod template (render count). A model cannot compute a hash. It can often get the restart without one, by writing the text itself into the pod annotations (see below).
- **Order between the parts of one model, inside one tier, and hook phases.** 9 of 27 charts render a hook that is not a `helm test` hook, and 13 declare one in a template (14 together). A model can already order its parts across the three deployment tiers; it cannot order two parts of one tier, and a tier has no hook phase.
- **A config file serialised from structured settings.** At least 10 of 27 (template count).
- **A grant into each watched namespace.** 5 of 27 charts (a template loops over namespaces and a render holds a RoleBinding). A model part has no namespace field, so a model grants in one namespace only (see below).

What to build, if anything, is in the [Conclusion](#conclusion): the models to consider, triaged by how generic they are, and a separate list of custom resource kinds that kure and launcher could support. The one candidate that stands out is a generic monitored web app or service with a volume and a database, each part optional (see [Candidate models](#candidate-models)). The [use cases](#use-cases) a user deploys, from stateless web apps to multi-service applications, come first and the models are triaged from them. Two sections read evidence beyond the 30 charts: a [many-service application](#a-many-service-application-owncloud-and-opencloud) (ownCloud Infinite Scale and OpenCloud) and [monitoring and backup](#monitoring-and-backup-generically).

## Method

- 30 charts at pinned versions: 3 library charts and 27 application charts.
- Each application chart was rendered offline with `helm template` (Helm v4.3.2): its baseline and 2 to 3 value sets, 83 renders in total. No cluster was read, so `lookup` returns nothing.
- One render fails by design: chart 6 refuses to render without an image. Its baseline is the `minimal` set.
- A script produced every count. It reads a chart's own templates and each helper they call, followed through library charts. Templates of application subcharts are not read; their rendered objects are counted.
- Where a helper is picked by a computed name (charts 10, 12, 13 and 22), the script follows every helper named in a string.
- A text detector shows that a chart contains a construct, not how much it matters. Each detector has a positive control on a chart read by hand, and a sample of its matches was read.
- Library charts cannot be installed, so their code is read whole and counted apart. Charts 4 and 10 render libraries 2 and 1; a minimal consumer written for the survey renders library 3.
- Four more charts (one from ownCloud, three from OpenCloud) and one compose set sit outside the sample, as a worked example. They are rendered and counted apart (see [A many-service application](#a-many-service-application-owncloud-and-opencloud)), and their counts are never added to the sample's.

**Stop rule.** The sample stays at 30 unless charts 26 to 30 add a pattern to the catalogue. They add none: every pattern first appears in an application chart numbered 23 or lower. The catalogue grew during the analysis (rows marked *added* below), so this was also checked mechanically: charts 26 to 30 use one template function no earlier chart uses (`toPrettyJson`, chart 30), a serialiser, and no new `helm.sh/` annotation. That check sees functions and annotations only.

## Sample

| # | Chart | Version | Group | Render command (common flags below) |
|---|---|---|---|---|
| 1 | bitnami-common | 2.41.0 | Library | not installable; `https://charts.bitnami.com/bitnami` |
| 2 | bjw-s-common | 5.3.0 | Library | not installable; `https://bjw-s-labs.github.io/helm-charts` |
| 3 | helmet | 0.15.0 | Library | not installable; `https://charts.companyinfo.dev` |
| 4 | app-template | 5.3.0 | Generic app | `helm template demo app-template --repo https://bjw-s-labs.github.io/helm-charts --version 5.3.0` |
| 5 | podinfo | 6.17.0 | Generic app | `helm template demo podinfo --repo https://stefanprodan.github.io/podinfo --version 6.17.0` |
| 6 | stakater-application | 9.3.2 | Generic app | `helm template demo application --repo https://stakater.github.io/stakater-charts --version 9.3.2` |
| 7 | nextcloud | 9.4.6 | Web app with database | `helm template demo nextcloud --repo https://nextcloud.github.io/helm/ --version 9.4.6` |
| 8 | gitea | 12.7.0 | Web app with database | `helm template demo gitea --repo https://dl.gitea.com/charts --version 12.7.0` |
| 9 | authentik | 2026.8.7 | Web app with database | `helm template demo authentik --repo https://charts.goauthentik.io/ --version 2026.8.7` |
| 10 | wordpress | 34.2.12 | Web app with database | `helm template demo wordpress --repo https://charts.bitnami.com/bitnami --version 34.2.12` |
| 11 | mattermost-team-edition | 6.6.112 | Web app with database | `helm template demo mattermost-team-edition --repo https://helm.mattermost.com --version 6.6.112` |
| 12 | postgresql | 18.12.28 | Data store | `helm template demo postgresql --repo https://charts.bitnami.com/bitnami --version 18.12.28` |
| 13 | redis | 28.3.16 | Data store | `helm template demo redis --repo https://charts.bitnami.com/bitnami --version 28.3.16` |
| 14 | cnpg-cluster | 0.9.0 | Data store | `helm template demo cluster --repo https://cloudnative-pg.io/charts/ --version 0.9.0` |
| 15 | minio | 5.4.0 | Data store | `helm template demo minio --repo https://charts.min.io --version 5.4.0` |
| 16 | nats | 2.15.3 | Data store | `helm template demo nats --repo https://nats-io.github.io/k8s/helm/charts/ --version 2.15.3` |
| 17 | cert-manager | v1.21.3 | Operator, kinds supported | `helm template demo cert-manager --repo https://charts.jetstack.io --version v1.21.3` |
| 18 | external-secrets | 2.15.0 | Operator, kinds supported | `helm template demo external-secrets --repo https://charts.external-secrets.io/ --version 2.15.0` |
| 19 | kube-prometheus-stack | 94.1.0 | Operator, kinds supported | `helm template demo kube-prometheus-stack --repo https://prometheus-community.github.io/helm-charts --version 94.1.0` |
| 20 | cilium | 1.20.5 | Operator, kinds supported | `helm template demo cilium --repo https://helm.cilium.io/ --version 1.20.5` |
| 21 | flux2 | 2.20.0 | Operator, kinds supported | `helm template demo flux2 --repo https://fluxcd-community.github.io/helm-charts --version 2.20.0` |
| 22 | victoria-metrics-k8s-stack | 0.99.0 | Operator, kinds likely needed | `helm template demo victoria-metrics-k8s-stack --repo https://victoriametrics.github.io/helm-charts/ --version 0.99.0` |
| 23 | strimzi | 1.3.1 | Operator, kinds likely needed | `helm template demo strimzi-kafka-operator --repo https://strimzi.io/charts --version 1.3.1` |
| 24 | rabbitmq-cluster-operator | 4.4.34 | Operator, kinds likely needed | `helm template demo rabbitmq-cluster-operator --repo https://charts.bitnami.com/bitnami --version 4.4.34` |
| 25 | spark-operator | 2.6.0 | Operator, kinds likely needed | `helm template demo spark-operator --repo https://kubeflow.github.io/spark-operator --version 2.6.0` |
| 26 | emqx-operator | 3.0.0 | Operator, kinds likely needed | `helm template demo emqx-operator --repo https://repos.emqx.io/charts --version 3.0.0` |
| 27 | node-feature-discovery | 0.20.1 | Operator, kinds likely needed | `helm template demo node-feature-discovery --repo https://kubernetes-sigs.github.io/node-feature-discovery/charts --version 0.20.1` |
| 28 | ingress-nginx | 4.15.1 | Ingress, gateway, jobs | `helm template demo ingress-nginx --repo https://kubernetes.github.io/ingress-nginx --version 4.15.1` |
| 29 | envoy-gateway | 1.10.0 | Ingress, gateway, jobs | `helm template demo oci://docker.io/envoyproxy/gateway-helm --version 1.10.0` |
| 30 | velero | 12.2.5 | Ingress, gateway, jobs | `helm template demo velero --repo https://vmware-tanzu.github.io/helm-charts/ --version 12.2.5` |

Common flags on every render:

```bash
--namespace demo --include-crds \
  --api-versions monitoring.coreos.com/v1,cert-manager.io/v1,gateway.networking.k8s.io/v1,autoscaling/v2,policy/v1,networking.k8s.io/v1,velero.io/v1,monitoring.coreos.com/v1/ServiceMonitor,monitoring.coreos.com/v1/PodMonitor,monitoring.coreos.com/v1/PrometheusRule,cert-manager.io/v1/Issuer,cert-manager.io/v1/Certificate
```

The value sets are in [Value sets](#value-sets). The Keycloak operator of the first candidate list was replaced by the EMQX operator: an Artifact Hub search showed only third-party Keycloak operator charts.

## Pattern catalogue

Counts are charts that show the pattern: out of 27 application charts, and out of 3 libraries. *plan* marks a pattern the survey set out to find, *added* one found during the analysis.

The *Counted from* column says what a count reads: `render` is the objects of the value sets, `template` is the text of the chart's own templates and its library helpers, so a construct behind a switch no value set turned on counts only there.

| Pattern | Apps | Libs | Counted from | Source | Verdict | Why |
|---|---|---|---|---|---|---|
| Loops over lists in values | 27 | 3 | template | plan | configuration model | A CEL comprehension ranges over a list only. A chart that loops over a map needs a list input here. |
| One object per list entry | 17 | 2 | template | added | configuration model | `parts[].forEach`; one render gives at most 50 components. |
| Objects switched on and off by values | 26 | 2 | template | plan | configuration model | `parts[].when`. |
| A mode value picks the workload shape | 6 | 0 | template | added | configuration model | `parts[].when` on alternative parts. |
| Values copied verbatim into object fields | 27 | 3 | template | added | configuration model | A property that is exactly one `${ }` expression keeps its type, so an object input passes through. |
| One object's name written into another | 26 | 0 | render | plan | covered / configuration model | Covered inside `webservice` and by traits such as `expose`, `rbac` and `scaler`. Between model parts, `parts.<id>.name` reads a declared name. |
| A Service name inside env or a config value | 12 | 0 | render | added | configuration model | A string template around `parts.<id>.name`. The expression scope has no namespace, so only the short name. |
| Generated names and labels | 24 | 3 | template | plan | covered | launcher names objects and stamps labels itself. |
| Use an existing object, or create one | 14 | 3 | template | added | configuration model | An optional input plus `when`. |
| Bundled dependency or an external one | 3 | 0 | template | added | configuration model | `when` on the bundled parts; reads of a conditional part must be guarded. |
| Application subcharts | 9 | 0 | Chart.yaml | plan | configuration model | A model can use an earlier model as a part, at most 4 deep. A chart itself stays usable through `helm`. |
| Image reference assembled from registry, repository, tag | 20 | 2 | template | added | configuration model | A string template. |
| Rejecting bad value combinations | 19 | 2 | template, values schema | added | configuration model (partly) | Input schemas hold `required`, `enum`, `requiredWhen` and exclusive groups. A model has no field for a free assertion between two inputs. |
| Scrape and alert objects | 22 | 0 | render | added | covered | `servicemonitor`, `podmonitor`, `prometheusrule`. The VictoriaMetrics counterparts have no type. |
| Dashboards shipped as labelled ConfigMaps | 2 | 0 | render | added | covered | `configmap`. |
| Webhook that needs a serving certificate | 8 | 0 | render | added | configuration model | With cert-manager: a `certificate` part beside the webhook configuration kinds. The chart's own certificate-writing job is not copied. |
| Escape hatch for arbitrary extra objects | 16 | 1 | template | added | covered | `manifests`, `passthrough`. |
| CRD installation | 12 | 0 | render | plan | covered | `crd`. |
| Setup, upgrade and maintenance jobs | 12 | 0 | render | added | covered | `job` and `cronjob` build the object. When it runs is the ordering row below. |
| Hook in a rendered object (not `helm test`) | 9 | 0 | render | plan | covered for charts; in a model, **needs Go** inside a tier and for hook phases | `helm` keeps a chart's hook phases. A model orders its parts only through the three tiers (see below). |
| Hook declared in a template (not `helm test`) | 13 | 0 | template | added | same as the row above | Reads the template text, so it also counts hooks behind a switch no value set turned on. |
| Config file in a ConfigMap or Secret | 13 | 0 | render | plan | covered for text | `configmap` takes the file as text. Counted in renders, so it includes files the template holds as text. |
| Config file serialised from structured settings | 10 | 0 | template, 3 read by hand | added | **needs Go** | The model's expression environment has no function that writes YAML, TOML, INI or JSON. At least this many charts: see below. |
| Hash in a template (config checksum) | 13 | 3 | template | plan | **needs Go** for the hash | The value is a hash of rendered content. A model can still roll pods on a text change, within limits (see below). |
| Grant written into each watched namespace | 5 | 0 | template and render | added | **needs Go** | A model part has no namespace field, and `role` and `rolebinding` write into the application's namespace (see below). |
| Generated passwords | 5 | 2 | template | plan | out of scope | A value drawn at render time changes on each build unless the cluster is read back. Use a controller: the Secret CloudNativePG generates (`postgresql` publishes its name), or `external-secret`. |
| Certificates generated by the template | 5 | 1 | template | added | out of scope | Same reason. Use `certificate` with cert-manager. |
| Reading live cluster state (`lookup`) | 6 | 2 | template | plan | out of scope | A launcher build reads no cluster. |
| Branching on cluster version or API list | 17 | 3 | template | added | out of scope | launcher states what a cluster offers in its cluster profile. |
| User values evaluated as templates (`tpl`) | 22 | 3 | template | added | out of scope | Helm-only behaviour. |
| User merge or patch over generated objects | 2 | 0 | template | added | out of scope | The model format has no `patches`. |

<details><summary>Which charts show each pattern (numbers from the sample table)</summary>

| Pattern | Application charts | Libraries |
|---|---|---|
| Loops over lists in values | 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30 | 1, 2, 3 |
| One object per list entry | 4, 5, 6, 9, 10, 13, 14, 19, 20, 22, 23, 24, 25, 26, 28, 29, 30 | 2, 3 |
| Objects switched on and off by values | 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30 | 2, 3 |
| A mode value picks the workload shape | 10, 12, 13, 14, 15, 28 | — |
| Values copied verbatim into object fields | 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30 | 1, 2, 3 |
| One object's name written into another | 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30 | — |
| A Service name inside env or a config value | 5, 7, 8, 10, 11, 12, 13, 15, 16, 19, 20, 22 | — |
| Generated names and labels | 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 22, 24, 25, 26, 27, 28, 29, 30 | 1, 2, 3 |
| Use an existing object, or create one | 4, 6, 7, 8, 9, 10, 11, 12, 13, 15, 19, 20, 24, 30 | 1, 2, 3 |
| Bundled dependency or an external one | 7, 10, 11 | — |
| Application subcharts | 7, 8, 9, 10, 11, 18, 19, 22, 29 | — |
| Image reference assembled from registry, repository, tag | 4, 7, 8, 10, 12, 13, 14, 15, 17, 18, 19, 20, 21, 22, 23, 24, 25, 27, 28, 29 | 2, 3 |
| Rejecting bad value combinations | 4, 5, 6, 8, 9, 12, 13, 14, 16, 17, 18, 19, 20, 22, 25, 27, 28, 29, 30 | 2, 3 |
| Scrape and alert objects | 4, 5, 6, 7, 8, 9, 10, 12, 13, 14, 16, 17, 18, 19, 20, 21, 22, 24, 25, 27, 28, 30 | — |
| Dashboards shipped as labelled ConfigMaps | 19, 23 | — |
| Webhook that needs a serving certificate | 17, 18, 19, 22, 24, 25, 28, 29 | — |
| Escape hatch for arbitrary extra objects | 6, 7, 8, 9, 10, 11, 12, 13, 16, 17, 18, 19, 21, 22, 24, 30 | 3 |
| CRD installation | 17, 18, 19, 21, 22, 23, 24, 25, 26, 27, 29, 30 | — |
| Setup, upgrade and maintenance jobs | 4, 12, 14, 15, 17, 19, 21, 22, 27, 28, 29, 30 | — |
| Hook in a rendered object (not `helm test`) | 15, 17, 19, 21, 22, 27, 28, 29, 30 | — |
| Hook declared in a template (not `helm test`) | 5, 12, 14, 15, 19, 20, 21, 22, 25, 27, 28, 29, 30 | — |
| Config file in a ConfigMap or Secret | 5, 7, 8, 9, 10, 13, 15, 16, 19, 20, 22, 23, 29 | — |
| Config file serialised from structured settings | 8, 12, 16, 17, 18, 19, 20, 22, 27, 29 | — |
| Hash in a template (config checksum) | 4, 5, 7, 8, 9, 10, 11, 12, 13, 15, 20, 27, 30 | 1, 2, 3 |
| Grant written into each watched namespace | 23, 24, 25, 26, 29 | — |
| Generated passwords | 7, 10, 12, 13, 15 | 1, 3 |
| Certificates generated by the template | 10, 12, 13, 20, 24 | 3 |
| Reading live cluster state (`lookup`) | 10, 12, 13, 15, 20, 24 | 1, 3 |
| Branching on cluster version or API list | 4, 6, 7, 8, 10, 11, 12, 13, 15, 18, 19, 20, 23, 24, 25, 28, 30 | 1, 2, 3 |
| User values evaluated as templates (`tpl`) | 4, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 28, 30 | 1, 2, 3 |
| User merge or patch over generated objects | 16, 28 | — |

</details>

## Why each "needs Go" verdict

### Restart on config change

A chart annotates the pod template with a SHA-256 of a rendered ConfigMap or Secret, so a change restarts the pods. A model cannot compute the hash:

- A part reads only the *name* of an earlier part's records, never rendered content.
- The expression environment is the CEL standard library plus the strings, lists and bindings extensions (`pkg/oam/model/celenv.go`). Encoders are refused and no function hashes.

**The behaviour often needs no hash.** For a ConfigMap the model builds from its own inputs, the model holds the text as a value and can write that text, or a value derived from the inputs, into the pod-template annotations. `webservice` and `worker` take `podMetadata.annotations` (`pkg/oam/builtin/components/pod_metadata.go`), and the raw workload kinds write the template whole. A text change then changes the template, so the pods roll. Where this falls short:

- A Secret: the route would copy secret data into an annotation.
- Content over the 256 KiB annotation limit (the same file checks it).
- Content the model does not build: a ConfigMap another part or another document owns.

What still needs Go is the hash itself, and so any restart in those three cases. A Go rule can do it: the `helm` rule already names the values ConfigMap and Secret it generates after a SHA-256 of their content, so a change makes a new name (`pkg/oam/builtin/components/helm.go`, `helm_secret_values.go`). A search of `pkg/` for the standard hash packages found no code that does this, or annotates a pod template, for a workload's own configuration.

### Ordering between the parts of one model

A model part holds an id, a name, a type, `when`, `forEach`, properties, traits and annotations (`pkg/oam/model/types.go`). It cannot emit a `dependency` policy.

**A model can already order its parts across three tiers.** A part's `annotations` become the component's annotations. The component annotation `<domain>/tier` takes `infra`, `services` or `apps` (`pkg/oam/classify.go`), and the tiers deploy in that order, each after the one before (`TierOrder` in `pkg/oam/pipeline.go`). It is read after lowering, and no code in `pkg/oam/lowering*.go` overrides a lowered component's tier. This was read in the code and not built. Its limits:

- The tiers are application-wide and there are three.
- The key holds the domain of the transform (`TransformContext.Domain`), which a model has to hardcode.

What still needs Go: an order between two parts of one tier, and hook phases (the charts use `pre-delete`, `post-delete` and `pre-rollback`, which a tier cannot express). A Go rule can emit a policy: `postgresql` lowers into `dependency` and `placement` policies. A chart delivered through `helm` keeps its own hooks. Not tested: whether a document-level `dependency` policy can name the components a model generates.

### A config file serialised from structured settings

Where a chart turns a map of settings into a YAML, TOML, INI or JSON file, a model has no serialiser: the environment above has no encoder, and a comprehension cannot range over a map. A model can join strings, so a short line-based file with fixed keys is possible, and a file the user writes as text needs no Go. Not tested: whether the text `format` prints for a map would parse as YAML; it is not a documented serialiser.

The count is a lower bound. A detector matches charts 12, 17, 18, 19, 20, 22, 27, where a file key is filled by a `toYaml` or JSON function. Charts 8, 16, 29 were read by hand and do the same through helpers: Gitea builds the sections of `app.ini` in a helper (`gitea.inline_configuration`, only its definition was read), NATS formats `nats.conf` with its own helper, and Envoy Gateway merges two maps and writes them as YAML. Other charts were not read for this.

### A grant into each watched namespace

A model part has no namespace field (`pkg/oam/model/types.go`), and `role` and `rolebinding` write into the application's namespace (`pkg/oam/builtin/components/role.go`, `rolebinding.go`). A `forEach` over the namespaces would therefore make 5 grants in one namespace and none in the others.

### A model limit that is not a chart pattern

A trait on a component of a model type is refused. A model that users must extend with `expose` or `scaler` has to take those settings as inputs, or be a Go rule.

## A many-service application: ownCloud and OpenCloud

ownCloud Infinite Scale (oCIS) and its fork OpenCloud are file-sharing platforms built from many services. They are a worked example of an application that a user must be able to build, and **not a candidate for a base model**: launcher would not ship one as a default. The evidence is outside the 30-chart sample, so its counts are not added to the sample's.

| Source | Pinned at | State on 2026-11-27 |
|---|---|---|
| ownCloud chart `ocis` 0.8.0, app 8.2.0: https://github.com/owncloud/ocis-charts | `36a4490a94c033c610fcda0de7a62d25206bff02` (2026-10-12) | repository not archived |
| OpenCloud community charts `opencloud` 0.2.3, `opencloud-dev` 0.1.0, `opencloud-microservices` 0.3.10: https://github.com/opencloud-eu/helm | `72de94b25a89bfb3cf467cdf312e4166c55ded55` (2025-11-26) | archived; its README points to a paid offer for production charts |
| OpenCloud compose files: https://github.com/opencloud-eu/opencloud-compose | `6de23f8e55d12c9cbff4b816fd86478d93614e8b` (2026-11-25) | active |

Each chart was rendered offline with `helm template demo <chart directory> --namespace demo --include-crds` (Helm v4.3.2), once, with its default values. `ocis` also needs `--set externalDomain=files.example.org`, because its values schema refuses a render without it. Counts of rendered objects:

| Chart | Deployment | Service | Secret | ConfigMap | Other |
|---|---|---|---|---|---|
| `ocis` | 32 | 32 | 13 | 8 | none |
| `opencloud` | 7 | 7 | 6 | 6 | 5 PersistentVolumeClaim, 1 Ingress |
| `opencloud-microservices` | 38 | 38 | 14 | 10 | 3 HTTPRoute, 1 PersistentVolumeClaim |
| `opencloud-dev` | 1 | 1 | 0 | 0 | 1 Job, 2 PersistentVolumeClaim |

**What the services and shared pieces are** (read from the `ocis` templates and values, and from the compose files):

- **Services.** The `ocis` values hold 39 `services.<name>` blocks; the default render has a Deployment and a Service for 32 of them (no Deployment for 7: `antivirus`, `authapp`, `collaboration`, `notifications`, `ocm`, `policies`, `storageusersvault`). They cover the proxy, the gateway, several storage services, the search, thumbnail, notification, sharing, user and group services, and the web client. `opencloud` runs one binary in one Deployment beside collaboration, keycloak, minio, onlyoffice, postgres and tika Deployments.
- **Identity.** The `idp` and `idm` services are a built-in OIDC provider and directory. The compose overlays swap them for Keycloak with an LDAP server, or for an external identity provider.
- **Messaging.** A `nats` service by default; `messagingSystem.external` points to an external NATS.
- **Storage.** 10 templates hold a PersistentVolumeClaim (for example `storageusers`, `search`, `idm`, `nats`); the 32 default Deployments hold 32 `emptyDir` volumes. The compose set has a `storage/decomposeds3.yml` overlay for S3.
- **Search, content extraction, office, extras.** `search` plus the compose overlays for OpenSearch and Tika; the `collaboration` service plus Collabora or `euro-office` and `yjs` overlays for web office; overlays for ClamAV, `imagor` thumbnails and a Radicale calendar.
- **Entry.** Two templates write an Ingress, `proxy` and `collaboration`, and the default render holds none; the compose set puts Traefik in front, or an external proxy.
- **Configuration.** A generated ConfigMap or Secret per service, replaceable by an existing one through `configRefs` and `secretRefs`. The compose set builds the application from a `COMPOSE_FILE` list of base file and overlays: the same optional-parts pattern as the catalogue, in another form.
- **Patterns from the catalogue** (template counts in `ocis`): a SHA-256 in 1 template, a generated password or certificate function in 7, a CronJob in 3 (storage and thumbnail clean-up, a daily notification summary), a `ServiceMonitor` in 1, no `helm.sh/hook` annotation.

**What a model could express today** (judgement; nothing was built):

| Piece | Today | Missing kind | Needs Go |
|---|---|---|---|
| 32 to 39 services from one list of names | `forEach` over a list of service names, one `worker` or `webservice` part each. The services block in the values is a map, so the model takes a list input. One render gives at most 50 components (`MaxParts`, `pkg/oam/model/rule.go:12-14`, counted after `when` and `forEach`), and all renders of one build give at most 500 (`MaxBuildParts`, `pkg/oam/model/budget.go:15`). With one part per workload and one per ConfigMap or Secret, `ocis` needs 53 parts (32, 13 and 8) and `opencloud-microservices` 62 (38, 14 and 10), both over the 50 of one render. So the configuration has to be passed in from outside, or the application split into nested models (at most 4 deep, `MaxNestingDepth`, `pkg/oam/model/rule.go:15-17`), each with its own 50. Not tested | none | none |
| A ConfigMap or Secret per service | `configmap` and `secret` parts; a config file the user writes as text | none | a file written from structured settings |
| A claim per service, only where it keeps data | `pvc` parts with `when` | none | none |
| One entry through the proxy | the `expose` trait on one part | none | none |
| NATS or identity provider bundled, or an external one | `when` on the bundled parts and inputs for the external one | none | none |
| Office suite, antivirus, search engine as options | parts with `when`; Collabora, OnlyOffice, ClamAV, OpenSearch and Tika run as ordinary workloads | none | none |
| Restart on a config change | the text in the pod annotations, for a ConfigMap the model builds | none | the hash, and a Secret |
| Generated passwords and certificates | out of scope (see the catalogue) | none | none |

Every kind in these renders has a built-in type, so the example adds nothing to the list of kinds without one. What it asks of the model mechanism is size (tens of parts), one shared set of inputs reaching all of them, and optional parts. Judgement: a user who runs this application writes a model for it; launcher ships the mechanism and the generic parts.

## Use cases

Judgement, not measurement: what a user of launcher deploys, written from the charts read and the ownCloud example. Counts are charts out of the 27 application charts (the shape table and the catalogue); an overlap is not an addition. The only evidence of real use is the production tree on go-kure/launcher#1637, kept apart.

| # | Use case | What the user deploys | Evidence |
|---|---|---|---|
| U1 | Web app or service without state | one workload, a Service, perhaps a route, scaling and a disruption budget | exposed service 14, autoscaled or disruption bounded 19, monitored 21; 12 of the 14 exposed are monitored |
| U2 | Web app with state | U1 plus a volume and a database | 8 with a volume, 5 with a database; 6 of the 8 with a volume are monitored |
| U3 | Background worker | a workload with no Service, such as a queue consumer | not counted by this survey |
| U4 | Scheduled job | a CronJob or a setup job | 12 charts render a setup, upgrade or maintenance job; `ocis` has 3 CronJob templates |
| U5 | Database | one store, replicated or not, with its credentials | 8 replicated stateful stores and 5 web apps with a database; 4 charts (7 to 10) are in both, because a chart bundles its own store; PostgreSQL has a type, MariaDB and MySQL have none |
| U6 | Cache and queue | Redis, NATS, MinIO, or an operator-run broker | Redis (13), NATS (16) and MinIO (15) are store charts; Kafka (23), RabbitMQ (24) and EMQX (26) charts install operators only, so none of their resource kinds is counted |
| U7 | Multi-service application | tens of workloads that share inputs, a message bus, an identity provider and one entry | the ownCloud and OpenCloud example: 32 to 39 services |
| U8 | Monitoring attached to any of the above | a scrape, a rule, a dashboard | 22 charts render scrape or alert objects |
| U9 | Backup attached to any of the above | a scheduled backup of a volume or a database | 5 charts show a workload with a scheduled backup; 4 of the 5 are also monitored and 2 have a volume |
| U10 | Operator or controller install | CRDs, RBAC, a webhook with a certificate | 12 charts; 7 of the 8 webhook-server charts and 5 of 5 per-namespace-grant charts are inside it |

The overlaps were computed from the shape table, with the table's own lists as the control (a list against itself gives its full count). The last row matters most: two earlier candidates, a webhook server with a certificate and role grants per watched namespace, are parts of one use case and not two models.

## Candidate models

A chart counts for a candidate when one of its renders holds all the object kinds of that shape. The count says the shape occurs, not that the chart is that kind of application. *Restart* is how many of the counted charts also hash a config in a template (the *hash in a template* count).

Ranked by what a model would save, then by count. The order is a judgement; the counts are not. Rank 1 merges two earlier candidates, a monitored web service and a web app with a volume and a database: they overlap in 6 charts and are one shape with different parts present. More specific shapes can be built on top of the merged one, but a customer with a narrower pattern would probably write their own, so none is proposed as a default. The candidates are triaged for inclusion in the [Conclusion](#conclusion); none of the existing lowering rules they name is taken as settled.

| Rank | Candidate | Charts | Restart | Parts | Missing kinds | Category | Why this rank |
|---|---|---|---|---|---|---|---|
| 1 | Monitored web app or service with volume and database, each part optional | 23 show at least one part: 21 monitored (6 with alerts), 8 with a volume, 5 with a database; 4 show all three | 13 of the 23 | `webservice`; optional `servicemonitor` or `podmonitor` and `prometheusrule`; optional `pvc`; optional `expose`; optional `postgresql` with `external-secret` | VictoriaMetrics scrape and rule kinds; no MariaDB or MySQL type | configuration model; restart on a config change by a pod annotation, Go for the hash | The most frequent combination no single type gives today, and the one that joins the most parts by hand. It replaces two earlier candidates, a monitored web service and a web app with a volume and a database: each is this model with parts switched off. |
| 2 | Webhook server with certificate | 8 | 0 | `deployment`, `service`, `certificate`, a webhook configuration kind | none | configuration model | Mostly operator installs, which `helm` already delivers. |
| 3 | Replicated stateful store | 8 | 7 | `statefulset`, two `service` parts, `disruption` | none | configuration model; restart on a config change by a pod annotation, Go for the hash | An operator type such as `cnpg-cluster` is the better route where one exists. |
| 4 | Workload with scheduled backup | 5 | 3 | `cnpg-scheduledbackup` or the `volsync` trait | Velero `Backup`, `Schedule` | covered, except Velero | A model adds little over the existing parts. Which backup families to support by default is open (see [Monitoring and backup](#monitoring-and-backup-generically)). |
| 5 | Role grants per watched namespace | 5 | 0 | `serviceaccount`, `role` and `rolebinding` | none | **needs Go** | A chart writes a grant into each watched namespace. `role` and `rolebinding` always write into the application's namespace and a model part has no namespace field, so a `forEach` would grant in one namespace only. |

Frequent shapes that need no new model, because existing types already give them:

| Shape | Charts | Existing route |
|---|---|---|
| exposed service | 14 | `webservice` with `expose` |
| autoscaled or disruption bounded | 19 | the `scaler` and `disruption` traits |
| operator install | 12 | `helm`, or `crd`, `deployment` and the RBAC kinds |
| network isolated workload | 10 | the `networkpolicy` trait and synthesized policies |
| node agent | 6 | `daemonset`, `serviceaccount` and the RBAC kinds |

<details><summary>Which charts count for each shape</summary>

| Shape | Charts |
|---|---|
| admission webhook server | 17, 18, 19, 22, 24, 25, 28, 29 |
| autoscaled or disruption bounded | 5, 6, 7, 8, 9, 10, 12, 13, 15, 16, 17, 18, 19, 23, 24, 25, 27, 28, 29 |
| exposed service | 4, 5, 6, 7, 8, 9, 10, 11, 15, 16, 19, 20, 21, 22 |
| exposed service with volume | 4, 6, 7, 8, 9, 10, 11, 15 |
| monitored and alerting workload | 6, 12, 13, 19, 28, 30 |
| monitored workload | 4, 5, 6, 7, 8, 9, 10, 12, 13, 16, 17, 18, 19, 20, 21, 22, 24, 25, 27, 28, 30 |
| network isolated workload | 6, 7, 8, 9, 10, 12, 13, 17, 21, 24 |
| node agent | 19, 20, 22, 27, 28, 30 |
| operator install | 17, 18, 19, 21, 22, 23, 24, 25, 26, 27, 29, 30 |
| per namespace rbac | 23, 24, 25, 26, 29 |
| replicated stateful store | 7, 8, 9, 10, 12, 13, 15, 16 |
| web app with database | 7, 8, 9, 10, 11 |
| workload with scheduled backup | 4, 6, 12, 14, 30 |

</details>

## Kinds without a built-in type

The renders hold 56 distinct group and kind pairs: 41 have a built-in type, 15 do not.

| Kind | Charts |
|---|---|
| `operator.victoriametrics.com/VLAgent` | 22 |
| `operator.victoriametrics.com/VLSingle` | 22 |
| `operator.victoriametrics.com/VMAgent` | 22 |
| `operator.victoriametrics.com/VMAlert` | 22 |
| `operator.victoriametrics.com/VMAlertmanager` | 22 |
| `operator.victoriametrics.com/VMAuth` | 22 |
| `operator.victoriametrics.com/VMCluster` | 22 |
| `operator.victoriametrics.com/VMNodeScrape` | 22 |
| `operator.victoriametrics.com/VMServiceScrape` | 22 |
| `operator.victoriametrics.com/VMSingle` | 22 |
| `operator.victoriametrics.com/VTSingle` | 22 |
| `velero.io/Backup` | 6 |
| `velero.io/BackupStorageLocation` | 30 |
| `velero.io/Schedule` | 30 |
| `velero.io/VolumeSnapshotLocation` | 30 |

Templates also declare kinds that no value set switched on. 17 have no built-in type: the 15 below, which may get one, and 2 OpenShift kinds that never will (next table). Only a literal `apiVersion` is read, so the list can be short. Removed and generic kinds (`Endpoints`, `List`, `OAuthRedirectReference`, `policy/PodSecurityPolicy`) are left out.

| Kind | Charts |
|---|---|
| `autoscaling.k8s.io/VerticalPodAutoscaler` | 6, 19 |
| `grafana.integreatly.org/GrafanaDashboard` | 6, 19 |
| `grafana.integreatly.org/GrafanaDatasource` | 19, 22 |
| `secrets-store.csi.x-k8s.io/SecretProviderClass` | 6, 19 |
| `bitnami.com/SealedSecret` | 6 |
| `endpointmonitor.stakater.com/EndpointMonitor` | 6 |
| `forecastle.stakater.com/ForecastleApp` | 6 |
| `gateway.nginx.org/ClientSettingsPolicy` | 8 |
| `monitoring.coreos.com/AlertmanagerConfig` | 6 |
| `monitoring.coreos.com/PrometheusAgent` | 19 |
| `operator.victoriametrics.com/VLCluster` | 22 |
| `operator.victoriametrics.com/VLDistributed` | 22 |
| `operator.victoriametrics.com/VMAlertmanagerConfig` | 22 |
| `operator.victoriametrics.com/VMDistributed` | 22 |
| `operator.victoriametrics.com/VTCluster` | 22 |

**Never supported:** OpenShift-only kinds. They exist on one distribution of Kubernetes, not on the platform launcher targets, so they stay out of every candidate list on this page.

| Kind | Charts | Status |
|---|---|---|
| `route.openshift.io/Route` | 6, 8, 11 | never supported |
| `security.openshift.io/SecurityContextConstraints` | 15 | never supported |

Of the Prometheus Operator kinds, 7 have a built-in type (`alertmanager`, `podmonitor`, `prometheus`, `prometheus-probe`, `prometheusrule`, `servicemonitor`, `thanosruler`; `pkg/oam/builtin/components/README.md`). `AlertmanagerConfig` (chart 6) and `PrometheusAgent` (chart 19) have none, and nor has `ScrapeConfig`, which neither list on this page names.

## Monitoring and backup, generically

**What charts ship.** 22 of 27 application charts render scrape or alert objects (catalogue, render count). Chart 22 renders VictoriaMetrics kinds, and it is the one sampled chart that does; the others use the Prometheus Operator kinds. 2 charts ship dashboards as labelled ConfigMaps. The production tree on go-kure/launcher#1637 is the opposite case, 66 VictoriaMetrics objects in 2 units; it is separate evidence. Both families matter.

**How far the two families are compatible.** From the VictoriaMetrics operator's own page on Prometheus ([source](https://docs.victoriametrics.com/operator/integrations/prometheus/), read 2026-11-27): the operator supports conversion from the Prometheus Operator kinds `ServiceMonitor`, `PodMonitor`, `PrometheusRule` and `Probe` (`monitoring.coreos.com/v1`) and `AlertmanagerConfig` (`v1alpha1`), with `VMServiceScrape`, `VMPodScrape`, `VMRule`, `VMProbe` and `VMAlertmanagerConfig` as the counterparts. Its default behaviour converts `ServiceMonitor`, `PodMonitor`, `PrometheusRule`, `Probe` and `ScrapeConfig` (counterpart `VMScrapeConfig`) and keeps them in sync; the page's two lists differ on `AlertmanagerConfig` and `ScrapeConfig`. The operator does not ship the Prometheus Operator CRDs, so they must be installed. `VMNodeScrape` and `VMStaticScrape` are never created by conversion. The page names no conversion for the instance kinds (`Prometheus`, `Alertmanager`, `ThanosRuler`, `PrometheusAgent`), whose counterparts are `VMAgent`, `VMAlert`, `VMAlertmanager`, `VMSingle` and `VMCluster`. Judgement: the compatibility is one way and partial. A chart whose only monitoring objects are kinds the page says are converted can run on a VictoriaMetrics cluster, if the conversion is on and the CRDs are installed. A chart that also emits an instance kind needs the Prometheus Operator for it, or those objects replaced by the VictoriaMetrics kinds. A chart that emits VictoriaMetrics kinds cannot run on a Prometheus cluster.

**Proposal** (judgement; nothing is built):

- Support both families as kinds: a type or a route for each of the kinds in the [Conclusion](#conclusion) list, so a user can write either by hand.
- Add one generic monitoring part: a scrape (selector, port, path, interval), a rule (an alert or recording expression) and a dashboard. It lowers to the Prometheus Operator kinds or to the VictoriaMetrics kinds, chosen by one input or by the cluster profile. A trait on a component of a model type is refused (see above), so the part would sit inside a model as well as be a trait on a plain workload.
- A dashboard part has two routes, and the generic part names which it takes: the Grafana operator kinds (`GrafanaDashboard` and `GrafanaDatasource`, declared by charts 6, 19 and 22), or a ConfigMap that carries the label a Grafana sidecar is set to watch (2 charts; `configmap` has a type today).

**Backup families seen.** Velero (`Backup`, `Schedule`, `BackupStorageLocation`, `VolumeSnapshotLocation`; charts 6 and 30; no type), CloudNativePG (`cnpg-scheduledbackup` and `cnpg-objectstore`, built in; the chart 14 backup values) and VolSync (the `volsync` trait and the `replicationsource` and `replicationdestination` types, built in). Velero and VolSync back up volumes; CloudNativePG backs up one database. A generic backup part would say what to back up (a claim or a database), how often, how long to keep it and where to, and lower to the family that fits. It is not written here, because the first question is not mine to answer.

**Open question for Serge:** which backup families should launcher support by default: Velero, CloudNativePG, VolSync, or a subset? The page does not decide it. The candidate "workload with scheduled backup" and the Velero kinds in the CRD list wait on the answer.

## What this does not show

- How often users set an option. A chart shows what its authors made configurable.
- Anything about one real deployment. The running list on go-kure/launcher#1637 counts objects in a production GitOps tree. Those counts and these chart counts are separate evidence and are not added up.
- Complete pattern counts. A text detector over Go templates can miss a construct written another way. Known cases: chart 8 configures an external database under its own config keys, which the *external one* detector does not match, and generators inside application subcharts are not counted.
- Anything about what users deploy beyond the charts read. The use cases are judgement tied to chart counts and one worked example. The ownCloud and OpenCloud counts are counts of rendered objects and template files of four charts, and they are not added to the sample's.
- Whether any proposed model works. None was built or rendered, and the many-service example was counted against the 50-part limit of one render, not tested against it.
- Behaviour at install time. Hooks are counted from rendered objects and, separately, from template text; `lookup` only from template text. Nothing was installed. No Helm 3 render was compared.

## Value sets

Add these flags to the chart's row command and the common flags.

<details><summary>All value sets</summary>

**4 app-template**

```bash
# web
--set controllers.main.containers.main.image.repository=nginx \
  --set controllers.main.containers.main.image.tag=1.29 \
  --set service.main.controller=main \
  --set service.main.ports.http.port=80 \
  --set ingress.main.enabled=true \
  --set 'ingress.main.hosts[0].host=a.example.org' \
  --set 'ingress.main.hosts[0].paths[0].path=/' \
  --set 'ingress.main.hosts[0].paths[0].service.identifier=main' \
  --set 'ingress.main.hosts[0].paths[0].service.port=http' \
  --set persistence.data.type=persistentVolumeClaim \
  --set persistence.data.accessMode=ReadWriteOnce \
  --set persistence.data.size=1Gi
# workers-config
--set controllers.main.containers.main.image.repository=nginx \
  --set controllers.main.containers.main.image.tag=1.29 \
  --set controllers.job.type=cronjob \
  --set 'controllers.job.cronjob.schedule=0 * * * *' \
  --set controllers.job.containers.main.image.repository=busybox \
  --set controllers.job.containers.main.image.tag=1 \
  --set configMaps.app.data.config=x \
  --set secrets.app.stringData.k=v \
  --set serviceMonitor.main.serviceName=demo \
  --set 'serviceMonitor.main.endpoints[0].port=http'
```

**5 podinfo**

```bash
# ingress-ha
--set ingress.enabled=true \
  --set replicaCount=3 \
  --set hpa.enabled=true \
  --set podDisruptionBudget.maxUnavailable=1
# metrics-redis
--set serviceMonitor.enabled=true \
  --set redis.enabled=true \
  --set httpRoute.enabled=true
```

**6 stakater-application**

```bash
# expose-persist
--set deployment.image.repository=nginx \
  --set deployment.image.tag=1.29 \
  --set applicationName=demo \
  --set ingress.enabled=true \
  --set 'ingress.hosts[0].host=a.example.org' \
  --set 'ingress.hosts[0].paths[0].path=/' \
  --set persistence.enabled=true
# ha-metrics
--set deployment.image.repository=nginx \
  --set deployment.image.tag=1.29 \
  --set applicationName=demo \
  --set autoscaling.enabled=true \
  --set pdb.enabled=true \
  --set serviceMonitor.enabled=true \
  --set prometheusRule.enabled=true \
  --set backup.enabled=true \
  --set networkPolicy.enabled=true
# minimal
--set deployment.image.repository=nginx \
  --set deployment.image.tag=1.29 \
  --set applicationName=demo
```

**7 nextcloud**

```bash
# bundled-db
--set internalDatabase.enabled=false \
  --set postgresql.enabled=true \
  --set externalDatabase.enabled=true \
  --set externalDatabase.type=postgresql \
  --set redis.enabled=true
# extdb-metrics
--set internalDatabase.enabled=false \
  --set externalDatabase.enabled=true \
  --set externalDatabase.host=db.example.org \
  --set metrics.enabled=true \
  --set prometheus.serviceMonitor.enabled=true \
  --set hpa.enabled=true
# persist-ingress
--set persistence.enabled=true \
  --set ingress.enabled=true \
  --set cronjob.enabled=true
```

**8 gitea**

```bash
# extdb-metrics
--set postgresql-ha.enabled=false \
  --set valkey-cluster.enabled=false \
  --set gitea.config.database.DB_TYPE=postgres \
  --set gitea.config.database.HOST=db.example.org \
  --set gitea.metrics.enabled=true \
  --set gitea.metrics.serviceMonitor.enabled=true
# ingress-pg
--set ingress.enabled=true \
  --set postgresql.enabled=true \
  --set postgresql-ha.enabled=false \
  --set valkey-cluster.enabled=false \
  --set valkey.enabled=true
```

**9 authentik**

```bash
# ha-metrics
--set authentik.postgresql.host=db.example.org \
  --set server.replicas=3 \
  --set worker.replicas=2 \
  --set server.pdb.enabled=true \
  --set server.autoscaling.enabled=true \
  --set server.metrics.enabled=true \
  --set server.metrics.serviceMonitor.enabled=true
# ingress-db
--set server.ingress.enabled=true \
  --set postgresql.enabled=true \
  --set authentik.secret_key=x
```

**10 wordpress**

```bash
# extdb-metrics
--set mariadb.enabled=false \
  --set externalDatabase.host=db.example.org \
  --set externalDatabase.password=x \
  --set metrics.enabled=true \
  --set metrics.serviceMonitor.enabled=true \
  --set memcached.enabled=true
# ingress-ha
--set ingress.enabled=true \
  --set replicaCount=3 \
  --set autoscaling.enabled=true
```

**11 mattermost-team-edition**

```bash
# extdb
--set mysql.enabled=false \
  --set externalDB.enabled=true \
  --set externalDB.externalConnectionString=u:p@db.example.org:3306/m
# ingress
--set ingress.enabled=true
```

**12 postgresql**

```bash
# backup
--set backup.enabled=true
# replication-metrics
--set architecture=replication \
  --set metrics.enabled=true \
  --set metrics.serviceMonitor.enabled=true \
  --set metrics.prometheusRule.enabled=true \
  --set-json 'metrics.prometheusRule.rules=[{"alert":"Down","expr":"up == 0"}]'
```

**13 redis**

```bash
# sentinel
--set sentinel.enabled=true
# standalone-metrics
--set architecture=standalone \
  --set metrics.enabled=true \
  --set metrics.serviceMonitor.enabled=true \
  --set metrics.prometheusRule.enabled=true
```

**14 cnpg-cluster**

```bash
# backup-s3
--set backups.enabled=true \
  --set backups.provider=s3 \
  --set backups.s3.bucket=b \
  --set backups.s3.region=r \
  --set backups.secret.create=true \
  --set backups.s3.accessKey=a \
  --set backups.s3.secretKey=s
# pooler-db
--set 'poolers[0].name=rw' \
  --set 'poolers[0].type=rw' \
  --set 'poolers[0].instances=2' \
  --set 'databases[0].name=app' \
  --set 'databases[0].owner=app'
# replica-monitoring
--set cluster.instances=3 \
  --set cluster.monitoring.enabled=true
```

**15 minio**

```bash
# buckets-users
--set 'buckets[0].name=b' \
  --set 'policies[0].name=p' \
  --set 'policies[0].statements[0].resources[0]=arn:aws:s3:::b' \
  --set 'policies[0].statements[0].actions[0]=s3:GetObject' \
  --set 'users[0].accessKey=u' \
  --set 'users[0].secretKey=s' \
  --set 'users[0].policy=p' \
  --set metrics.serviceMonitor.enabled=true \
  --set podDisruptionBudget.enabled=true
# standalone-ingress
--set mode=standalone \
  --set ingress.enabled=true \
  --set persistence.enabled=true
```

**16 nats**

```bash
# cluster-jetstream
--set config.cluster.enabled=true \
  --set config.jetstream.enabled=true
# websocket-metrics
--set config.websocket.enabled=true \
  --set config.websocket.ingress.enabled=true \
  --set 'config.websocket.ingress.hosts[0]=a.example.org' \
  --set promExporter.enabled=true \
  --set promExporter.podMonitor.enabled=true
```

**17 cert-manager**

```bash
# crds-ha
--set crds.enabled=true \
  --set replicaCount=2 \
  --set podDisruptionBudget.enabled=true \
  --set webhook.replicaCount=2 \
  --set webhook.podDisruptionBudget.enabled=true
# monitoring-netpol
--set prometheus.servicemonitor.enabled=true \
  --set webhook.networkPolicy.enabled=true
```

**18 external-secrets**

```bash
# ha-metrics
--set replicaCount=2 \
  --set leaderElect=true \
  --set podDisruptionBudget.enabled=true \
  --set serviceMonitor.enabled=true
# no-crds-certmanager
--set installCRDs=false \
  --set webhook.certManager.enabled=true \
  --set webhook.certManager.cert.issuerRef.name=ca \
  --set certController.create=false
```

**19 kube-prometheus-stack**

```bash
# certmanager-thanos
--set prometheusOperator.admissionWebhooks.certManager.enabled=true \
  --set thanosRuler.enabled=true
# ingress-ha
--set prometheus.ingress.enabled=true \
  --set alertmanager.ingress.enabled=true \
  --set grafana.ingress.enabled=true \
  --set prometheus.prometheusSpec.replicas=2 \
  --set alertmanager.alertmanagerSpec.replicas=3 \
  --set prometheus.podDisruptionBudget.enabled=true
```

**20 cilium**

```bash
# hubble
--set hubble.relay.enabled=true \
  --set hubble.ui.enabled=true \
  --set hubble.ui.ingress.enabled=true \
  --set 'hubble.metrics.enabled={dns,drop}' \
  --set hubble.metrics.serviceMonitor.enabled=true \
  --set prometheus.enabled=true \
  --set prometheus.serviceMonitor.enabled=true
# ingress-gateway
--set ingressController.enabled=true \
  --set gatewayAPI.enabled=true \
  --set kubeProxyReplacement=true \
  --set k8sServiceHost=api.example.org \
  --set k8sServicePort=6443 \
  --set encryption.enabled=true \
  --set encryption.type=wireguard
```

**21 flux2**

```bash
# minimal-nocrds
--set installCRDs=false \
  --set imageAutomationController.create=false \
  --set imageReflectionController.create=false \
  --set multitenancy.enabled=true
# receiver-metrics
--set notificationController.webhookReceiver.ingress.create=true \
  --set prometheus.podMonitor.create=true
```

**22 victoria-metrics-k8s-stack**

```bash
# cluster-ingress
--set vmsingle.enabled=false \
  --set vmcluster.enabled=true \
  --set vmcluster.ingress.select.enabled=true \
  --set grafana.ingress.enabled=true
# logs-traces
--set vlsingle.enabled=true \
  --set vlagent.enabled=true \
  --set vtsingle.enabled=true \
  --set alertmanager.ingress.enabled=true
```

**23 strimzi**

```bash
# any-namespace-dashboards
--set watchAnyNamespace=true \
  --set dashboards.enabled=true \
  --set generateNetworkPolicy=false
# namespaces
--set 'watchNamespaces={team-a,team-b}' \
  --set podDisruptionBudget.enabled=true \
  --set replicas=2
```

**24 rabbitmq-cluster-operator**

```bash
# certmanager-ha
--set useCertManager=true \
  --set clusterOperator.replicaCount=2 \
  --set msgTopologyOperator.replicaCount=2
# metrics
--set clusterOperator.metrics.service.enabled=true \
  --set clusterOperator.metrics.serviceMonitor.enabled=true \
  --set msgTopologyOperator.metrics.service.enabled=true \
  --set msgTopologyOperator.metrics.serviceMonitor.enabled=true
```

**25 spark-operator**

```bash
# ha-monitoring
--set controller.replicas=2 \
  --set controller.leaderElection.enable=true \
  --set controller.podDisruptionBudget.enable=true \
  --set prometheus.podMonitor.create=true
# namespaces-certmanager
--set 'spark.jobNamespaces={team-a,team-b}' \
  --set certManager.enable=true \
  --set controller.podDisruptionBudget.enable=false
```

**26 emqx-operator**

```bash
# ha
--set replicaCount=3 \
  --set skipCRDs=true
# watch-namespaces
--set 'watchNamespaces={team-a,team-b}' \
  --set development=false
```

**27 node-feature-discovery**

```bash
# certmanager-ha
--set tls.enable=true \
  --set tls.certManager=true \
  --set master.replicaCount=2 \
  --set master.podDisruptionBudget.enable=true
# topology-prom
--set topologyUpdater.enable=true \
  --set prometheus.enable=true
```

**28 ingress-nginx**

```bash
# config-certmanager
--set controller.config.use-gzip=true \
  --set controller.admissionWebhooks.certManager.enabled=true \
  --set controller.kind=DaemonSet \
  --set tcp.9000=ns/svc:9000
# ha-metrics
--set controller.replicaCount=3 \
  --set controller.autoscaling.enabled=true \
  --set controller.metrics.enabled=true \
  --set controller.metrics.serviceMonitor.enabled=true \
  --set controller.metrics.prometheusRule.enabled=true \
  --set-json 'controller.metrics.prometheusRule.rules=[{"alert":"Down","expr":"up == 0"}]' \
  --set defaultBackend.enabled=true
```

**29 envoy-gateway**

```bash
# ha
--set deployment.replicas=2 \
  --set hpa.enabled=true \
  --set hpa.minReplicas=2 \
  --set hpa.maxReplicas=4 \
  --set podDisruptionBudget.minAvailable=1
# nocrds-certmanager
--set crds.enabled=false \
  --set certgen.disableTopologyInjector=true \
  --set topologyInjector.enabled=false
```

**30 velero**

```bash
# nodeagent-bsl
--set deployNodeAgent=true \
  --set 'configuration.backupStorageLocation[0].name=default' \
  --set 'configuration.backupStorageLocation[0].provider=aws' \
  --set 'configuration.backupStorageLocation[0].bucket=b' \
  --set upgradeCRDs=false
# schedules-metrics
--set 'schedules.daily.schedule=0 1 * * *' \
  --set schedules.daily.template.ttl=240h \
  --set metrics.serviceMonitor.enabled=true \
  --set metrics.prometheusRule.enabled=true \
  --set-json 'metrics.prometheusRule.spec=[{"alert":"Down","expr":"up == 0"}]'
```

</details>

## Conclusion

Everything here is judgement. The counts are the page's own, and nothing is built or scheduled. No existing lowering rule (`webservice`, `worker`, `helm`, `postgresql`, `oci`) and no earlier proposal is taken as accepted as designed: where a model below relies on one, the text names the part of it and says its design is not settled.

### Use cases and what serves them

| Use case | Served by | Still lacks |
|---|---|---|
| U1 web app or service without state | M1 with the optional parts off | nothing for the workload; the monitoring part needs M2 |
| U2 web app with state | M1 | a MariaDB or MySQL type; backup (M3) |
| U3 background worker | M1 without the exposure part | nothing counted |
| U4 scheduled job | the `cronjob` type; M1 only if it takes a schedule mode | nothing |
| U5 database | M4, or an operator type where one exists (`postgresql`, `cnpg-*`) | MariaDB and MySQL |
| U6 cache and queue | M4; for an operator-run broker, a CRD route | Kafka, RabbitMQ and EMQX operator kinds |
| U7 multi-service application | no base model: M1 repeated by `forEach`, with the configuration passed in or the application split into nested models, because the flat part count is over the 50 of one render (see the ownCloud example) | a list input for the services, a config file written from structured settings (Go) |
| U8 monitoring | M2 | the VictoriaMetrics kinds, `AlertmanagerConfig`, `ScrapeConfig`, `PrometheusAgent`, the Grafana operator kinds |
| U9 backup | M3, which waits on the open question | the Velero kinds |
| U10 operator or controller install | `helm`, or M5 | a namespace field on a model part, for the per-namespace grant (Go) |

### Models to consider

Triaged from the use cases, most generic first.

1. **M1: a workload with optional parts.** A workload part that is a service or a worker, with optional parts: a volume (`pvc`), exposure (`expose`), a database (`postgresql` with `external-secret`), monitoring (M2) and backup (M3). Serves U1 to U3, and U7 as a repeated part. Evidence: 23 charts show at least one part. It relies on the `webservice` and `worker` rules, the `expose` rule and the `postgresql` rule; none of them is settled. A trait on a component of a model type is refused, so exposure has to be an input of the model. Whether the workload is one part that a mode value turns into a service, a worker or a job (6 charts pick the shape by a mode) is open.
2. **M2: a generic monitoring part.** A scrape, a rule and a dashboard that lower to the Prometheus Operator kinds or the VictoriaMetrics kinds (see [Monitoring and backup](#monitoring-and-backup-generically)). 22 charts. It relies on `servicemonitor`, `podmonitor` and `prometheusrule`. Whether it is a trait, a model part or both is a design question for go-kure/launcher#102.
3. **M4: a data store.** `statefulset`, two `service` parts and `disruption`; 8 charts, 7 of which also hash a config in a template. Where an operator type exists it is the better route. It relies on the raw `statefulset` handling, which is not settled.
4. **M3: a generic backup part.** Not proposed until the open question on backup families is answered.
5. **M5: an operator install.** A workload, CRDs, RBAC and, for a webhook, a `certificate` part; 12 charts. `helm` delivers most of it today, so the value is low. The per-namespace grant needs Go.

Not proposed as defaults: a monitored web service or a web app with a volume and a database (each is M1 with parts off, and a user with a narrower pattern writes their own), and the many-service application (an example of what the mechanism must build, never a base model).

### Custom resource kinds to consider for kure and launcher

Chart numbers name charts of the 27 that declare the kind. "Tree" counts are objects in the production tree on go-kure/launcher#1637 and are not added to them. Routes: **generic** is a `ComponentDefinition` over a pinned CRD (go-kure/launcher#1712). **Pods** means the operator runs pods from settings in the resource, so the generic route waits on go-kure/launcher#1705, and a Go type is the other way.

| Kind | Family | Why | Route |
|---|---|---|---|
| `VMServiceScrape`, `VMPodScrape`, `VMRule`, `VMProbe`, `VMNodeScrape`, `VMStaticScrape` | VictoriaMetrics | chart 22 renders `VMServiceScrape` and `VMNodeScrape`; tree: 18, 11, 18, 2, 6 and 4; the operator converts the Prometheus equivalents of the first four, and `VMNodeScrape` and `VMStaticScrape` are never created by conversion | generic |
| `VMScrapeConfig`, `VMAlertmanagerConfig` | VictoriaMetrics | `VMAlertmanagerConfig` in a chart 22 template; both named by the operator's conversion page | generic |
| `AlertmanagerConfig`, `ScrapeConfig` | Prometheus Operator | `AlertmanagerConfig` in chart 6; `ScrapeConfig` in no chart, listed because the monitoring part (M2) needs the family complete | generic |
| `Backup`, `Schedule`, `BackupStorageLocation`, `VolumeSnapshotLocation` | Velero | charts 6 and 30 | generic; waits on the open backup question |
| `GrafanaDashboard`, `GrafanaDatasource` | Grafana operator | charts 6, 19 and 22, in templates only | generic |
| `PersesDashboard`, `PersesGlobalDatasource` | Perses | tree: 34 and 2 | generic |
| `ForecastleApp`, `EndpointMonitor` | Forecastle, Stakater | `ForecastleApp` in chart 6 and tree: 29, where a trait may fit better (go-kure/launcher#1641); `EndpointMonitor` in chart 6 | generic |
| `VerticalPodAutoscaler`, `SecretProviderClass`, `SealedSecret`, `ClientSettingsPolicy`, `NodeFeatureRule` | several | charts 6 and 19, 6 and 19, 6, and 8; `NodeFeatureRule` only in the tree (2) | generic, low priority |
| `VMAgent`, `VMCluster`, `VMSingle`, `VMAlert`, `VMAlertmanager`, `VMAuth`, `VLSingle`, `VLAgent`, `VTSingle`, and in templates `VLCluster`, `VLDistributed`, `VMDistributed`, `VTCluster` | VictoriaMetrics | chart 22; tree: one or two each of `VMAgent`, `VMCluster`, `VMAlert`, `VMAlertmanager`, `VLCluster` and `VLAgent` | pods |
| `PrometheusAgent` | Prometheus Operator | chart 19; the built-in `prometheus` type already holds the operator's pod settings in Go | pods, or a Go type beside `prometheus` |
| `Perses`, `Mattermost`, `EMQX` | Perses, Mattermost, EMQX operators | tree only: 2, 1 and 1 | pods |

Never supported: `route.openshift.io/Route` and `security.openshift.io/SecurityContextConstraints`.

**Changes to the split posted on go-kure/launcher#1637 on 2026-11-27:** the two OpenShift kinds are removed; `AlertmanagerConfig` and `ScrapeConfig` are named as gaps in the Prometheus Operator family, and `PrometheusAgent` joins the kinds that run pods; the monitored-web-service model is no longer a candidate of its own (it is M1 with parts off); ownCloud and OpenCloud add no kind. A MariaDB and MySQL type is a type, not a kind.
