package components

import (
	"bytes"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	autov1 "github.com/fluxcd/image-automation-controller/api/v1"
	imagev1 "github.com/fluxcd/image-reflector-controller/api/v1"
	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"
	notificationv1 "github.com/fluxcd/notification-controller/api/v1"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	swv1beta1 "github.com/fluxcd/source-watcher/api/v2/v1beta1"
)

// vendoredFluxRepo is one Flux controller's repository whose CRDs are vendored
// under testdata/upstream/<name>, by scripts/vendor-flux-crds.sh: the API
// modules of the Flux controllers that go.mod links ship no CRD, so each CRD
// of a kind launcher ships is copied from the repository at the tag
// of the linked API module, with the repository's LICENSE. Its SOURCE file
// names the tag, the tag object and the git blob id of each file as committed
// (go-kure/launcher#883). The one addition is an allow-term pragma comment
// line above an upstream line that names a term the repository's
// forbidden-terms guard lists; for a file that carries one, a line
// "# upstream <path> <blob>" names upstream's blob id, that of the file
// without them.
type vendoredFluxRepo struct {
	// name is the repository's name under github.com/fluxcd, and the
	// directory's under testdata/upstream.
	name string
	// module is the API module go.mod links; the tag is api/<its version>.
	module string
}

// vendoredFluxRepos are the Flux controllers' repositories whose CRDs are
// vendored, as scripts/vendor-flux-crds.sh lists them.
var vendoredFluxRepos = []vendoredFluxRepo{
	{"helm-controller", "github.com/fluxcd/helm-controller/api"},
	{"image-automation-controller", "github.com/fluxcd/image-automation-controller/api"},
	{"image-reflector-controller", "github.com/fluxcd/image-reflector-controller/api"},
	{"kustomize-controller", "github.com/fluxcd/kustomize-controller/api"},
	{"notification-controller", "github.com/fluxcd/notification-controller/api"},
	{"source-controller", "github.com/fluxcd/source-controller/api"},
	{"source-watcher", "github.com/fluxcd/source-watcher/api/v2"},
}

// fluxVendoredCRDs are the vendored CRDs of the Flux controllers' kinds, by
// component: the repository, the CRD's path in it, and the version of it the
// kind emits.
var fluxVendoredCRDs = map[string]struct{ repo, file, version string }{
	"helmrelease":           {"helm-controller", "config/crd/bases/helm.toolkit.fluxcd.io_helmreleases.yaml", helmv2.GroupVersion.Version},
	"imageupdateautomation": {"image-automation-controller", "config/crd/bases/image.toolkit.fluxcd.io_imageupdateautomations.yaml", autov1.GroupVersion.Version},
	"imagepolicy":           {"image-reflector-controller", "config/crd/bases/image.toolkit.fluxcd.io_imagepolicies.yaml", imagev1.GroupVersion.Version},
	"imagerepository":       {"image-reflector-controller", "config/crd/bases/image.toolkit.fluxcd.io_imagerepositories.yaml", imagev1.GroupVersion.Version},
	"fluxcd-kustomization":  {"kustomize-controller", "config/crd/bases/kustomize.toolkit.fluxcd.io_kustomizations.yaml", kustv1.GroupVersion.Version},
	"fluxcd-alert":          {"notification-controller", "config/crd/bases/notification.toolkit.fluxcd.io_alerts.yaml", notificationv1.GroupVersion.Version},
	"fluxcd-provider":       {"notification-controller", "config/crd/bases/notification.toolkit.fluxcd.io_providers.yaml", notificationv1.GroupVersion.Version},
	"fluxcd-receiver":       {"notification-controller", "config/crd/bases/notification.toolkit.fluxcd.io_receivers.yaml", notificationv1.GroupVersion.Version},
	"bucket":                {"source-controller", "config/crd/bases/source.toolkit.fluxcd.io_buckets.yaml", sourcev1.GroupVersion.Version},
	"gitrepository":         {"source-controller", "config/crd/bases/source.toolkit.fluxcd.io_gitrepositories.yaml", sourcev1.GroupVersion.Version},
	"helmchart":             {"source-controller", "config/crd/bases/source.toolkit.fluxcd.io_helmcharts.yaml", sourcev1.GroupVersion.Version},
	"helmrepository":        {"source-controller", "config/crd/bases/source.toolkit.fluxcd.io_helmrepositories.yaml", sourcev1.GroupVersion.Version},
	"ocirepository":         {"source-controller", "config/crd/bases/source.toolkit.fluxcd.io_ocirepositories.yaml", sourcev1.GroupVersion.Version},
	"artifactgenerator":     {"source-watcher", "config/crd/bases/source.extensions.fluxcd.io_artifactgenerators.yaml", swv1beta1.GroupVersion.Version},
}

// allowTermPragma is a comment line scripts/vendor-flux-crds.sh writes above an
// upstream line that names a term the repository's forbidden-terms guard
// lists (a receiver type), the guard's escape hatch: one or more
// allow-term:<word> after a "#", indented as the line below it.
var allowTermPragma = regexp.MustCompile(`^ *# allow-term:[a-z]+( allow-term:[a-z]+)*$`)

// stripAllowTermPragmas returns body without its allow-term pragma lines, the
// bytes upstream wrote, and how many it stripped, failing on a pragma whose
// next line does not name each word it allows, so that no pragma outlives the
// line it was written for.
func stripAllowTermPragmas(t *testing.T, name string, body []byte) ([]byte, int) {
	t.Helper()
	lines := strings.SplitAfter(string(body), "\n")
	var out strings.Builder
	pragmas := 0
	for i, line := range lines {
		if !allowTermPragma.MatchString(strings.TrimSuffix(line, "\n")) {
			out.WriteString(line)
			continue
		}
		pragmas++
		next := ""
		if i+1 < len(lines) {
			next = strings.ToLower(lines[i+1])
		}
		for _, w := range strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "#")) {
			word := strings.TrimPrefix(w, "allow-term:")
			if !regexp.MustCompile(`\b` + regexp.QuoteMeta(word) + `\b`).MatchString(next) {
				t.Fatalf("%s:%d: the pragma %q stands above a line that does not name %q", name, i+1, strings.TrimSpace(line), word)
			}
		}
	}
	return []byte(out.String()), pragmas
}

// goModVersion is the version go.mod links the module at.
func goModVersion(t *testing.T, module string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	for line := range strings.Lines(string(data)) {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == module {
			return f[1]
		}
	}
	t.Fatalf("go.mod does not link %s", module)
	return ""
}

// loadVendoredFluxCRDs reads the SOURCE of the vendored repository and every
// CRD it names, by the path of each in the repository, failing on a file
// whose blob id differs from its pin, a file whose allow-term pragma lines
// and "# upstream" line disagree, a file that is no CustomResourceDefinition, a file in
// the directory that SOURCE does not name, a directory without the licence's
// full text, and a tag other than that of the API module go.mod links.
func loadVendoredFluxCRDs(t *testing.T, repo vendoredFluxRepo) map[string][]byte {
	t.Helper()
	dir := filepath.Join("testdata", "upstream", repo.name)
	data, err := os.ReadFile(filepath.Join(dir, "SOURCE"))
	if err != nil {
		t.Fatalf("read the SOURCE of %s: %v", repo.name, err)
	}
	crds := map[string][]byte{}
	listed := map[string]bool{"SOURCE": true}
	read := func(rel, blob string) []byte {
		listed[rel] = true
		body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("the SOURCE of %s names %s: %v", repo.name, rel, err)
		}
		if got := vendoredBlobID(body); got != blob {
			t.Fatalf("%s/%s has blob id %s, SOURCE says %s: each pin is the committed file's; re-run scripts/vendor-flux-crds.sh", repo.name, rel, got, blob)
		}
		return body
	}
	// upstream is upstream's blob id of each file that carries allow-term
	// pragma lines, by path, from the SOURCE lines "# upstream <path> <blob>".
	upstream := map[string]string{}
	tag, tagObject, licence := "", false, false
	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		switch {
		case len(fields) == 4 && fields[0] == "#" && fields[1] == "upstream":
			upstream[fields[2]] = fields[3]
		case len(fields) == 0 || strings.HasPrefix(line, "#"):
		case fields[0] == "tag" && len(fields) == 2:
			tag = fields[1]
		case fields[0] == "tag-object" && len(fields) == 2 && len(fields[1]) == 40:
			// Provenance for whoever refreshes the CRDs; offline, the test holds
			// the tag to go.mod and each file to its blob id.
			tagObject = true
		case fields[0] == "licence" && len(fields) == 3:
			body := read(fields[1], fields[2])
			if !bytes.Contains(body, []byte("Apache License")) || !bytes.Contains(body, []byte("Version 2.0, January 2004")) {
				t.Fatalf("%s/%s is not the Apache License, Version 2.0", repo.name, fields[1])
			}
			licence = true
		case fields[0] == "file" && len(fields) == 3:
			body := read(fields[1], fields[2])
			if !bytes.Contains(body, []byte("kind: CustomResourceDefinition")) {
				t.Fatalf("%s/%s is not a CustomResourceDefinition", repo.name, fields[1])
			}
			crds[fields[1]] = body
		default:
			t.Fatalf("the SOURCE of %s: unreadable line %q", repo.name, line)
		}
	}
	// A file is upstream's bytes: either as committed, with no pragma line, or,
	// when SOURCE names upstream's blob id of it, with at least one pragma line
	// and upstream's blob id once they are stripped.
	for rel, blob := range upstream {
		if _, ok := crds[rel]; !ok {
			t.Fatalf("the SOURCE of %s names upstream's blob id of %s, which no file line pins", repo.name, rel)
		}
		stripped, pragmas := stripAllowTermPragmas(t, repo.name+"/"+rel, crds[rel])
		if pragmas == 0 {
			t.Fatalf("%s/%s carries no allow-term pragma line, and SOURCE names upstream's blob id of it: re-run scripts/vendor-flux-crds.sh", repo.name, rel)
		}
		if got := vendoredBlobID(stripped); got != blob {
			t.Fatalf("%s/%s has blob id %s without its allow-term pragma lines, and upstream's is %s: the CRDs are copied unmodified but for those lines; re-run scripts/vendor-flux-crds.sh", repo.name, rel, got, blob)
		}
	}
	for rel, body := range crds {
		if _, ok := upstream[rel]; ok {
			continue
		}
		if _, pragmas := stripAllowTermPragmas(t, repo.name+"/"+rel, body); pragmas != 0 {
			t.Fatalf("%s/%s carries %d allow-term pragma lines, and SOURCE names no upstream blob id of it: re-run scripts/vendor-flux-crds.sh", repo.name, rel, pragmas)
		}
	}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if !listed[filepath.ToSlash(rel)] {
			t.Errorf("%s/%s is vendored and not named in SOURCE", repo.name, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	if !licence || !tagObject || len(crds) == 0 {
		t.Fatalf("the SOURCE of %s names licence %v, a tag object %v and %d CRDs: each vendored repository carries the Apache License, Version 2.0, its tag object and its CRDs", repo.name, licence, tagObject, len(crds))
	}
	if want := "api/" + goModVersion(t, repo.module); tag != want {
		t.Fatalf("the CRDs of %s are of %s, and go.mod links %s at the tag %s: re-run scripts/vendor-flux-crds.sh", repo.name, tag, repo.module, want)
	}
	return crds
}

// fluxVendoredCRD returns the vendored CRD of a Flux controller's kind, nil for
// another component.
func fluxVendoredCRD(t *testing.T, component string) []byte {
	t.Helper()
	at, ok := fluxVendoredCRDs[component]
	if !ok {
		return nil
	}
	for _, repo := range vendoredFluxRepos {
		if repo.name != at.repo {
			continue
		}
		data := loadVendoredFluxCRDs(t, repo)[at.file]
		if data == nil {
			t.Fatalf("the vendored %s holds no %s, the CRD of %s; re-run scripts/vendor-flux-crds.sh", at.repo, at.file, component)
		}
		return data
	}
	t.Fatalf("%s is no vendored repository", at.repo)
	return nil
}

// TestVendoredCRDs_MatchSource holds every vendored CRD to its SOURCE: the
// Flux controllers' repositories and the Prometheus operator's excerpt, each
// file to its git blob id, each directory to the tag of the module go.mod
// links, and each CRD a kind's harness rows read to a file one of them names
// (go-kure/launcher#883). Every vendored repository holds the CRD of a kind.
func TestVendoredCRDs_MatchSource(t *testing.T) {
	named := map[string]bool{}
	for _, repo := range vendoredFluxRepos {
		for rel := range loadVendoredFluxCRDs(t, repo) {
			named[repo.name+"/"+rel] = true
		}
	}
	used := map[string]bool{}
	for _, component := range slices.Sorted(maps.Keys(fluxVendoredCRDs)) {
		at := fluxVendoredCRDs[component]
		if !named[at.repo+"/"+at.file] {
			t.Errorf("%s reads %s/%s, which no SOURCE names", component, at.repo, at.file)
		}
		used[at.repo+"/"+at.file] = true
	}
	for _, path := range slices.Sorted(maps.Keys(named)) {
		if !used[path] {
			t.Errorf("%s is vendored, and no kind reads it; drop it from scripts/vendor-flux-crds.sh", path)
		}
	}
	promOp := loadVendoredPromOp(t)
	for _, component := range slices.Sorted(maps.Keys(promOpVendoredCRDs)) {
		if promOp.crds[promOpVendoredCRDs[component]] == nil {
			t.Errorf("%s reads %s, which the Prometheus operator's SOURCE does not name", component, promOpVendoredCRDs[component])
		}
	}
	if len(promOp.crds) != len(promOpVendoredCRDs) {
		t.Errorf("the Prometheus operator's excerpt holds %d CRDs, and %d kinds read one", len(promOp.crds), len(promOpVendoredCRDs))
	}
	t.Logf("%d Flux CRDs in %d repositories, %d of the Prometheus operator's", len(named), len(vendoredFluxRepos), len(promOp.crds))
}

// promOpVendoredCRDs are the vendored CRDs of the Prometheus operator's kinds,
// by component: the path of each in the operator's repository, and so in the
// excerpt (loadVendoredPromOp).
var promOpVendoredCRDs = map[string]string{
	"alertmanager":     promOpCRDs + "alertmanagers.yaml",
	"podmonitor":       promOpCRDs + "podmonitors.yaml",
	"prometheus":       promOpCRDs + "prometheuses.yaml",
	"prometheus-probe": promOpCRDs + "probes.yaml",
	"prometheusrule":   promOpCRDs + "prometheusrules.yaml",
	"servicemonitor":   promOpCRDs + "servicemonitors.yaml",
	"thanosruler":      promOpCRDs + "thanosrulers.yaml",
}

// promOpCRDs is the directory and file prefix of the Prometheus operator's
// CRDs in its repository.
const promOpCRDs = "example/prometheus-operator-crd/monitoring.coreos.com_"

// promOpVendoredCRD returns the vendored CRD of a Prometheus operator's kind,
// nil for another component.
func promOpVendoredCRD(t *testing.T, component string) []byte {
	t.Helper()
	rel, ok := promOpVendoredCRDs[component]
	if !ok {
		return nil
	}
	data := loadVendoredPromOp(t).crds[rel]
	if data == nil {
		t.Fatalf("the vendored excerpt holds no %s, the CRD of %s; re-run scripts/vendor-prometheus-operator-gates.sh", rel, component)
	}
	return data
}
