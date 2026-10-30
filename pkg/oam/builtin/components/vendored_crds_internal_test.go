package components

import (
	"bytes"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	autov1 "github.com/fluxcd/image-automation-controller/api/v1"
	imagev1 "github.com/fluxcd/image-reflector-controller/api/v1"
	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"
	notificationv1 "github.com/fluxcd/notification-controller/api/v1"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	swv1beta1 "github.com/fluxcd/source-watcher/api/v2/v1beta1"
)

// vendoredCRDRepo is one repository whose CRDs are vendored under
// testdata/upstream/<name>, because the API module go.mod links ship none:
//
//   - a Flux controller's, by scripts/vendor-flux-crds.sh, each CRD of a kind
//     launcher ships copied from the repository at the tag of the linked API
//     module, api/<its version> (go-kure/launcher#883). The one addition is an
//     allow-term pragma comment line above an upstream line that names a term
//     the repository's forbidden-terms guard lists; for a file that carries
//     one, a line "# upstream <path> <blob>" names upstream's blob id, that of
//     the file without them;
//   - the External Secrets Operator's, by scripts/vendor-external-secrets-crds.sh,
//     unmodified (go-kure/launcher#1108). go.mod links its API module at a
//     pseudo-version of a commit, not at a tag of its own; the commit is the
//     operator's release tag, a lightweight one, whose ref names the commit
//     with no tag object. SOURCE's tag-object is then the object the tag ref
//     names, the commit, and its first 12 hex digits must be the
//     pseudo-version's.
//
// Each comes with the repository's LICENSE, and its SOURCE file names the tag,
// the tag object and the git blob id of each file as committed.
type vendoredCRDRepo struct {
	// name is the directory's name under testdata/upstream: the repository's
	// under github.com/fluxcd, or external-secrets.
	name string
	// module is the API module go.mod links: the tag is api/<its version>, or,
	// where pseudo is set, the tag object is the commit of its pseudo-version.
	module string
	// script is the script that vendors the repository.
	script string
	// pseudo says go.mod links module at a pseudo-version of the tag's commit.
	pseudo bool
}

// The scripts that vendor the repositories.
const (
	vendorFluxScript            = "scripts/vendor-flux-crds.sh"
	vendorExternalSecretsScript = "scripts/vendor-external-secrets-crds.sh"
)

// vendoredCRDRepos are the repositories whose CRDs are vendored, as
// scripts/vendor-flux-crds.sh and scripts/vendor-external-secrets-crds.sh list
// them.
var vendoredCRDRepos = []vendoredCRDRepo{
	{"helm-controller", "github.com/fluxcd/helm-controller/api", vendorFluxScript, false},
	{"image-automation-controller", "github.com/fluxcd/image-automation-controller/api", vendorFluxScript, false},
	{"image-reflector-controller", "github.com/fluxcd/image-reflector-controller/api", vendorFluxScript, false},
	{"kustomize-controller", "github.com/fluxcd/kustomize-controller/api", vendorFluxScript, false},
	{"notification-controller", "github.com/fluxcd/notification-controller/api", vendorFluxScript, false},
	{"source-controller", "github.com/fluxcd/source-controller/api", vendorFluxScript, false},
	{"source-watcher", "github.com/fluxcd/source-watcher/api/v2", vendorFluxScript, false},
	{"external-secrets", externalSecretsModulePath, vendorExternalSecretsScript, true},
}

// externalSecretsCRDs is the directory and file prefix of the External Secrets
// Operator's CRDs in its repository, and so in testdata/upstream/external-secrets.
const externalSecretsCRDs = "config/crds/bases/external-secrets.io_"

// vendoredCRDs are the vendored CRDs of the Flux controllers' and the External
// Secrets Operator's kinds, by component: the repository, the CRD's path in
// it, and the version of it the kind emits.
var vendoredCRDs = map[string]struct{ repo, file, version string }{
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
	"clusterexternalsecret": {"external-secrets", externalSecretsCRDs + "clusterexternalsecrets.yaml", esv1.SchemeGroupVersion.Version},
	"clustersecretstore":    {"external-secrets", externalSecretsCRDs + "clustersecretstores.yaml", esv1.SchemeGroupVersion.Version},
	"externalsecret":        {"external-secrets", externalSecretsCRDs + "externalsecrets.yaml", esv1.SchemeGroupVersion.Version},
	"secretstore":           {"external-secrets", externalSecretsCRDs + "secretstores.yaml", esv1.SchemeGroupVersion.Version},
}

// pseudoVersionRev matches a pseudo-version, its last field the first 12 hex
// digits of the commit it names.
var pseudoVersionRev = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+-(?:.*[.-])?[0-9]{14}-([0-9a-f]{12})$`)

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

// loadVendoredCRDs reads the SOURCE of the vendored repository and every
// CRD it names, by the path of each in the repository, failing on a file
// whose blob id differs from its pin, a file whose allow-term pragma lines
// and "# upstream" line disagree, a file that is no CustomResourceDefinition, a file in
// the directory that SOURCE does not name, a directory without the licence's
// full text, and a tag other than that of the API module go.mod links: for a
// module linked at a pseudo-version (vendoredCRDRepo.pseudo), a release tag
// whose tag object, the commit a lightweight tag names, is not the
// pseudo-version's.
func loadVendoredCRDs(t *testing.T, repo vendoredCRDRepo) map[string][]byte {
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
			t.Fatalf("%s/%s has blob id %s, SOURCE says %s: each pin is the committed file's; re-run %s", repo.name, rel, got, blob, repo.script)
		}
		return body
	}
	// upstream is upstream's blob id of each file that carries allow-term
	// pragma lines, by path, from the SOURCE lines "# upstream <path> <blob>".
	upstream := map[string]string{}
	tag, tagObject, licence := "", "", false
	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		switch {
		case len(fields) == 4 && fields[0] == "#" && fields[1] == "upstream":
			upstream[fields[2]] = fields[3]
		case len(fields) == 0 || strings.HasPrefix(line, "#"):
		case fields[0] == "tag" && len(fields) == 2:
			tag = fields[1]
		case fields[0] == "tag-object" && len(fields) == 2 && regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(fields[1]):
			// Provenance for whoever refreshes the CRDs; offline, the test holds
			// the tag to go.mod, or this object to a pseudo-version's commit, and
			// each file to its blob id.
			tagObject = fields[1]
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
			t.Fatalf("%s/%s carries no allow-term pragma line, and SOURCE names upstream's blob id of it: re-run %s", repo.name, rel, repo.script)
		}
		if got := vendoredBlobID(stripped); got != blob {
			t.Fatalf("%s/%s has blob id %s without its allow-term pragma lines, and upstream's is %s: the CRDs are copied unmodified but for those lines; re-run %s", repo.name, rel, got, blob, repo.script)
		}
	}
	for rel, body := range crds {
		if _, ok := upstream[rel]; ok {
			continue
		}
		if _, pragmas := stripAllowTermPragmas(t, repo.name+"/"+rel, body); pragmas != 0 {
			t.Fatalf("%s/%s carries %d allow-term pragma lines, and SOURCE names no upstream blob id of it: re-run %s", repo.name, rel, pragmas, repo.script)
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
	if !licence || tagObject == "" || len(crds) == 0 {
		t.Fatalf("the SOURCE of %s names licence %v, a tag object %q and %d CRDs: each vendored repository carries the Apache License, Version 2.0, its tag object and its CRDs", repo.name, licence, tagObject, len(crds))
	}
	if mismatch := vendoredTagMismatch(repo, goModVersion(t, repo.module), tag, tagObject); mismatch != "" {
		t.Fatal(mismatch)
	}
	return crds
}

// vendoredTagMismatch holds the tag and the tag object a vendored repository's
// SOURCE names to the version go.mod links its module at, and says how they
// differ, "" where they agree. A module linked at its own tag is held by the
// tag: api/<version>. A module linked at a pseudo-version
// (vendoredCRDRepo.pseudo) has no tag of its own; its commit is a release tag
// of the repository, a lightweight one, whose ref names the commit with no tag
// object, so SOURCE's tag-object is that commit and its first 12 hex digits
// must be the pseudo-version's.
func vendoredTagMismatch(repo vendoredCRDRepo, linked, tag, tagObject string) string {
	if !repo.pseudo {
		if want := "api/" + linked; tag != want {
			return fmt.Sprintf("the CRDs of %s are of %s, and go.mod links %s at the tag %s: re-run %s", repo.name, tag, repo.module, want, repo.script)
		}
		return ""
	}
	m := pseudoVersionRev.FindStringSubmatch(linked)
	switch {
	case m == nil:
		return fmt.Sprintf("go.mod links %s at %s, not a pseudo-version, and the CRDs of %s are held to one: re-run %s", repo.module, linked, repo.name, repo.script)
	case !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(tag):
		return fmt.Sprintf("the CRDs of %s are of %s, not a release tag: re-run %s", repo.name, tag, repo.script)
	case !strings.HasPrefix(tagObject, m[1]):
		return fmt.Sprintf("the CRDs of %s are of %s at %s, and go.mod links %s at %s, commit %s: re-run %s with the release tag of that commit", repo.name, tag, tagObject, repo.module, linked, m[1], repo.script)
	}
	return ""
}

// vendoredCRD returns the vendored CRD of a Flux controller's or the External
// Secrets Operator's kind, nil for another component.
func vendoredCRD(t *testing.T, component string) []byte {
	t.Helper()
	at, ok := vendoredCRDs[component]
	if !ok {
		return nil
	}
	for _, repo := range vendoredCRDRepos {
		if repo.name != at.repo {
			continue
		}
		data := loadVendoredCRDs(t, repo)[at.file]
		if data == nil {
			t.Fatalf("the vendored %s holds no %s, the CRD of %s; re-run %s", at.repo, at.file, component, repo.script)
		}
		return data
	}
	t.Fatalf("%s is no vendored repository", at.repo)
	return nil
}

// TestVendoredCRDs_MatchSource holds every vendored CRD to its SOURCE: the
// Flux controllers' repositories, the External Secrets Operator's and the
// Prometheus operator's excerpt, each file to its git blob id, each directory
// to the tag of the module go.mod links, or, for the External Secrets
// Operator's API module, linked at a pseudo-version, its tag object to that
// pseudo-version's commit (checkVendoredTag), and each CRD a kind's harness
// rows read to a file one of them names (go-kure/launcher#883,
// go-kure/launcher#1108). Every vendored repository holds the CRD of a kind.
func TestVendoredCRDs_MatchSource(t *testing.T) {
	named := map[string]bool{}
	scripts := map[string]string{}
	for _, repo := range vendoredCRDRepos {
		for rel := range loadVendoredCRDs(t, repo) {
			named[repo.name+"/"+rel] = true
		}
		scripts[repo.name] = repo.script
	}
	used := map[string]bool{}
	for _, component := range slices.Sorted(maps.Keys(vendoredCRDs)) {
		at := vendoredCRDs[component]
		if !named[at.repo+"/"+at.file] {
			t.Errorf("%s reads %s/%s, which no SOURCE names", component, at.repo, at.file)
		}
		used[at.repo+"/"+at.file] = true
	}
	for _, path := range slices.Sorted(maps.Keys(named)) {
		if !used[path] {
			repo, _, _ := strings.Cut(path, "/")
			t.Errorf("%s is vendored, and no kind reads it; drop it from %s", path, scripts[repo])
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
	t.Logf("%d Flux and External Secrets CRDs in %d repositories, %d of the Prometheus operator's", len(named), len(vendoredCRDRepos), len(promOp.crds))
}

// TestVendoredCRDs_TagRuleBites: vendoredTagMismatch refuses, for a module
// linked at a pseudo-version, a tag object whose first 12 hex digits are not
// the pseudo-version's, a go.mod link that is no pseudo-version and a tag that
// is no release; and accepts the pseudo-version's commit.
func TestVendoredCRDs_TagRuleBites(t *testing.T) {
	const (
		linked = "v0.0.0-20261023150316-e932f3a3f6f2"
		commit = "e932f3a3f6f274e8e269c01aa1f5fb3d1322e3c9"
	)
	repo := vendoredCRDRepo{"external-secrets", externalSecretsModulePath, vendorExternalSecretsScript, true}
	for _, c := range []struct {
		name, linked, tag, tagObject string
		refused                      bool
	}{
		{"the pseudo-version's commit", linked, "v2.14.0", commit, false},
		{"another commit", linked, "v2.14.0", "e932f3a3f6f3" + commit[12:], true},
		{"no pseudo-version", "v2.14.0", "v2.14.0", commit, true},
		{"no release tag", linked, "main", commit, true},
	} {
		mismatch := vendoredTagMismatch(repo, c.linked, c.tag, c.tagObject)
		if refused := mismatch != ""; refused != c.refused {
			t.Errorf("%s: vendoredTagMismatch(%s, %s, %s) = %q, want refused %v", c.name, c.linked, c.tag, c.tagObject, mismatch, c.refused)
		}
	}
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
