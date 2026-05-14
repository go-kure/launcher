# Design: Package Composition — Optional Sections, Multi-Instance, Split Files

*Status: **Final — deferred to Phase 2 (issue #39)** | Issue #36*

| Version | Date | Summary |
|---|---|---|
| 1.1 | 2026-05-14 | Record decision (Phase 2 deferral); remove mechanism sections; keep background and open questions for #39 |
| 1.0 | 2026-05-14 | Initial draft — compared kurel.yaml optional list (Option A) and inline include-if annotation (Option B) |

**Decision:** No optional sections, multi-instance, or split-file support in Phase 1.
Package authors publish always-on packages. Users who need deployment variants instantiate
separate packages. Composition mechanism is designed in Phase 2 (issue #39).

**Scope:** package-level composition decisions made before the Application is resolved.
Runtime conditionality (e.g. "include this component only if another trait is present") is
a Phase 2 concern and is tracked in issue #39.

---

## What is deferred

The following questions are explicitly out of scope for Phase 1 and belong to issue #39:

1. **Optional traits** — a package may include a `certificate` trait or an `external-secret`
   trait that only applies when the user wants TLS or external secret injection. The user
   must be able to leave these out without forking the package.

2. **Optional components** — a package may include a worker component, a migration job, or a
   Redis sidecar component that is not always needed.

3. **Multi-instance** — a package may define a logical component pattern (e.g. a "worker")
   that can be instantiated multiple times with different names and values.

4. **Split files** — splitting `app.yaml` into per-component files for large packages.

---

## Open questions for issue #39

**Q1 — Where does optionality belong?**
- Package metadata (`kurel.yaml` optional list) — keeps optionality in the public API surface
- Inline in `app.yaml` (`include-if` annotation) — co-located with the section it controls,
  but introduces non-Application syntax into app.yaml and conflicts with strict parsing
- Both (annotations as mechanism; `kurel.yaml` as discovery index)

**Q2 — Per-section required parameters**
If optional components or traits have required parameters, how are they validated?
- Required-if: parameter is only required when the section is enabled
- Explicit group: parameters belong to a section; the group is validated or skipped together
- User responsibility: no framework validation

**Q3 — Multi-instance scope**
- Not Phase 2: users instantiate the same package multiple times with different names
- Phase 2: package declares a component pattern; values provide a list of instances

**Q4 — Split files**
Independent of optionality. Affects developer experience for large packages but not
correctness. May be addressed independently of Q1–Q3.
