# OAM Fill Probes

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/builtin/probe.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/probe)

Package `probe` holds the library's provenance report to what the library does, as
observed in a fixed set of probe documents. A built-in handler fills values the author
left out, and the report names each one with its source (`policy` or `builtin`, see
`pkg/oam`). The probes run documents through a transformer and compare the report with
the generated objects. They show what these documents trigger. A fill no probe document
triggers is not seen, so a clean run says nothing about it.

A consumer passes its own `*oam.Transformer`. The probes bring their own policies:
`ProbePolicy()` (every `Default*` accessor of `oam.Policy` set to an arbitrary value) and
`NoPolicy()`. The arbitrary values never appear in what a consumer publishes.

## Surface

| Name | Use |
|------|-----|
| `Probes()` | The probe documents, each with the fills observed with and without the policy. |
| `Probe`, `Fill` | One document, and one fill it shows: path, source in each state, and the value an author can write for it. |
| `ProbePolicy()`, `NoPolicy()` | The two policy states. |
| `Run(tr, components, policy)` | Validate, transform with provenance, generate: the report and the marshalled objects. |
| `CheckDeclaredDefaults` | Check A. |
| `CheckReportedFillsAreReal` | Check B. |
| `CheckProbePolicyCoversEveryDefault` | Check E. |

Each check returns findings as strings and never calls `testing`, so a consumer runs it
from its own test and prints what it returns.

## The three checks

- **A, declared defaults.** For every default a handler schema declares for a property a
  probe document leaves out, writing the default gives the same objects as leaving it out
  and the report holds an entry at that path. A default the handler leaves to the
  cluster is listed in the probe's `LeftToCluster` with the reason. The check reads schema
  declarations only: a fill no schema declares, such as the resource requests, is not
  seen here.
- **B, reported fills are real.** In both policy states the report holds the sources each
  probe states, and no entry of source `policy` or `builtin` that the probe does not list.
  For each fill with an authored value, writing that value gives the same objects and no
  entry at that path. An entry no probe lists fails with an instruction to add a row or
  fix the report.
- **E, the probe policy sets every `Default*` accessor.** An accessor added to
  `oam.Policy` and not to the probe policy is named.

One exception is named and kept: the cronjob schema declares `concurrencyPolicy` with the
default `Allow`, and the handler leaves the field unset for the cluster to apply. Writing
`Allow` changes the generated manifest text and not the cluster's behaviour. The cronjob
probe lists it in `LeftToCluster`. It is the one place check A disagrees with a schema.

The probes cover the handler-built workload types: deployment, statefulset, daemonset,
job and cronjob, with their init containers, sidecars, claim templates and pod failure
policy. Types that lower to other kinds are not probed yet.
