package kurel

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kio "github.com/go-kure/kure/pkg/io"
	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/fluxcd"
	"github.com/go-kure/kure/pkg/stack/layout"

	"github.com/go-kure/launcher/pkg/errors"
)

// Flux delivery output (docs/design.md §11, "Launcher Layout"): with
// --oci-repository, `kurel build -o <dir>` also writes one OCI artifact
// directory per bundle and one Flux OCIRepository + Kustomization per bundle
// that reconciles it.
const (
	ociRepositoryFlag = "oci-repository"
	ociTagFlag        = "oci-tag"

	// ociScheme is the URL scheme Flux's OCIRepository requires.
	ociScheme = "oci://"

	artifactManifestsFile     = "manifests.yaml"
	artifactKustomizationFile = "kustomization.yaml"

	// artifactKustomizationPath is every generated Kustomization's spec.path:
	// each OCIRepository serves one bundle's artifact directory, so the
	// Kustomization builds that artifact's root.
	artifactKustomizationPath = "./"
)

// artifactKustomization is the kustomization.yaml of an artifact that carries
// manifests; emptyArtifactKustomization is the one of an artifact that carries
// none (a tier umbrella), which kustomize builds to an empty set.
const (
	artifactKustomization = `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
- ` + artifactManifestsFile + `
`
	emptyArtifactKustomization = `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources: []
`
)

// deliveryOptions are the Flux delivery flags of `kurel build`.
type deliveryOptions struct {
	// repository is the oci:// URL prefix every bundle's artifact is published
	// under, as <repository>/<bundle name>. Empty disables delivery output.
	repository string
	// tag is the OCIRepository spec.ref.tag; empty leaves spec.ref unset, so
	// Flux pulls the artifact's latest tag.
	tag string
}

// registerDeliveryFlags adds --oci-repository and --oci-tag to the build
// command and checks them before the build runs (PreRunE), so a refused flag
// combination fails before anything is read or written.
func registerDeliveryFlags(cmd *cobra.Command, opts *buildOptions) {
	cmd.Flags().StringVar(&opts.delivery.repository, ociRepositoryFlag, "",
		"also write per-bundle OCI artifact directories and Flux OCIRepository/Kustomization objects, with artifacts at <oci://registry/prefix>/<bundle> (requires --output)")
	cmd.Flags().StringVar(&opts.delivery.tag, ociTagFlag, "",
		"OCI artifact tag the generated OCIRepositories pull (default: none, Flux pulls latest; requires --oci-repository)")
	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return validateDeliveryFlags(opts,
			cmd.Flags().Changed(ociRepositoryFlag), cmd.Flags().Changed(ociTagFlag))
	}
}

// validateDeliveryFlags refuses --oci-tag without --oci-repository, an
// --oci-repository that is not a valid oci:// repository URL (checkOCIRepository),
// an --oci-tag that is not a valid OCI tag, and --oci-repository without
// --output: the delivery output is a directory tree, which stdout cannot carry.
func validateDeliveryFlags(opts *buildOptions, repositorySet, tagSet bool) error {
	if !repositorySet {
		if tagSet {
			return errors.Errorf("--%s requires --%s", ociTagFlag, ociRepositoryFlag)
		}
		return nil
	}
	if err := checkOCIRepository(strings.TrimRight(opts.delivery.repository, "/"), false); err != nil {
		return errors.Wrapf(err, "--%s %q must be an %s URL naming a registry and optional path prefix, e.g. %sregistry.example.com/apps",
			ociRepositoryFlag, opts.delivery.repository, ociScheme, ociScheme)
	}
	if tagSet && !ociTagPattern.MatchString(opts.delivery.tag) {
		return errors.Errorf("--%s %q is not a valid OCI tag: it must match %s (up to 128 characters, starting with a letter, digit or '_')",
			ociTagFlag, opts.delivery.tag, ociTagPattern)
	}
	if opts.outputDir == "" {
		return errors.Errorf("--%s requires --output: the Flux delivery output is a directory tree (one artifact directory per bundle plus a Flux objects file) and cannot be written to stdout", ociRepositoryFlag)
	}
	return nil
}

// ociPathComponent is one '/'-separated component of an OCI
// distribution-spec repository name: lowercase alphanumerics, joined by '.',
// '_', '__' or a run of '-'.
var ociPathComponent = regexp.MustCompile(`^[a-z0-9]+((\.|_|__|-+)[a-z0-9]+)*$`)

// ociRegistryPattern is the distribution reference grammar's registry
// (domainAndPort in github.com/distribution/reference): a DNS name or IPv4
// address, or a bracketed IPv6 address, then an optional numeric port. It
// requires a hostname, which an RFC 3986 authority does not: name.NewRegistry
// accepts ":5000" and "registry.example.com:", neither of which a registry
// can be reached at.
var ociRegistryPattern = regexp.MustCompile(`^(?:(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9])(?:\.(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9]))*|\[[a-fA-F0-9:]+\])(?::[0-9]+)?$`)

// ociTagPattern is the OCI distribution-spec tag grammar.
var ociTagPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)

// ociRepositoryPathMax is the longest repository path (the part after the
// registry) go-containerregistry's parser accepts.
const ociRepositoryPathMax = 255

// checkOCIRepository checks that ref, an oci:// URL without a trailing '/',
// names an OCI repository (or, without requirePath, a repository prefix) that
// Flux's source-controller resolves where the operator meant:
// oci://<registry>[/<path>]. The registry is a host[:port] that names itself
// explicitly — localhost, or containing '.' or ':' — because go-containerregistry,
// which Flux parses the url with, resolves any other first segment against
// Docker Hub, and that has a hostname (ociRegistryPattern), so neither a
// bare port nor an empty one names it. The path is '/'-separated distribution-spec components of at most
// ociRepositoryPathMax characters, so no query, fragment, whitespace, empty
// segment or uppercase letter reaches a generated url.
func checkOCIRepository(ref string, requirePath bool) error {
	rest, ok := strings.CutPrefix(ref, ociScheme)
	if !ok {
		return errors.Errorf("missing the %s scheme", ociScheme)
	}
	host, path, hasPath := strings.Cut(rest, "/")
	if host == "" {
		return errors.New("no registry")
	}
	if host != "localhost" && !strings.ContainsAny(host, ".:") {
		return errors.Errorf("registry %q is not explicit (localhost, or a host containing '.' or ':'), so Flux would resolve it against Docker Hub", host)
	}
	if !ociRegistryPattern.MatchString(host) {
		return errors.Errorf("registry %q must be a hostname, IPv4 address or bracketed IPv6 address, with an optional numeric port", host)
	}
	if _, err := name.NewRegistry(host, name.StrictValidation); err != nil {
		return errors.Wrapf(err, "registry %q", host)
	}
	if !hasPath {
		if requirePath {
			return errors.New("no repository path")
		}
		return nil
	}
	if len(path) > ociRepositoryPathMax {
		return errors.Errorf("repository path is %d characters, more than %d", len(path), ociRepositoryPathMax)
	}
	for c := range strings.SplitSeq(path, "/") {
		if !ociPathComponent.MatchString(c) {
			return errors.Errorf("repository path component %q must match %s", c, ociPathComponent)
		}
	}
	return nil
}

// deliveryArtifact is one bundle's OCI artifact: the directory name (the
// bundle's reconciliation unit name) and exactly the objects that bundle
// renders.
type deliveryArtifact struct {
	name    string
	objects []*client.Object
}

// deliveryOutput is the in-memory delivery output of one build.
type deliveryOutput struct {
	artifacts []deliveryArtifact
	// flux holds the generated OCIRepositories and Kustomizations, in kure's
	// order (layout pre-order, each unit's Kustomization before its source).
	flux []*client.Object
}

// writeDelivery generates the Flux delivery output for cluster and writes it
// into dir: one flat artifact directory per bundle and <appName>.flux.yaml. It
// is a no-op without --oci-repository. Everything is generated before the
// first write, so a refused cluster writes nothing.
func writeDelivery(dir, appName string, cluster *stack.Cluster, opts deliveryOptions) error {
	if opts.repository == "" {
		return nil
	}
	out, err := generateDelivery(cluster, opts)
	if err != nil {
		return errors.Wrap(err, "generating Flux delivery objects")
	}
	return out.write(dir, appName)
}

// generateDelivery builds the delivery output from kure's own Flux generation
// (fluxcd.ResourceGenerator.GenerateFromLayout on the default-rules layout),
// so names, dependsOn, health checks and reconcile settings are kure's. Only
// two things are launcher's: every bundle's SourceRef points at an
// OCIRepository serving that bundle's artifact, and every Kustomization's
// spec.path is the artifact root ("./"), since the artifact IS the bundle's
// directory.
//
// The artifacts are not written with kure's layout writer, which nests an
// umbrella child's directory inside its parent's: each unit gets a flat
// directory holding exactly the objects OriginBundleObjects attributes to its
// bundles, so no object lands in two artifacts.
func generateDelivery(cluster *stack.Cluster, opts deliveryOptions) (*deliveryOutput, error) {
	ml, err := layout.WalkCluster(cluster, layout.DefaultLayoutRules())
	if err != nil {
		return nil, errors.Wrap(err, "walking the cluster layout")
	}
	ix, err := layout.IndexOrigins(ml, cluster)
	if err != nil {
		return nil, errors.Wrap(err, "indexing the cluster layout")
	}

	base := strings.TrimRight(opts.repository, "/")
	for _, b := range ix.Bundles() {
		// The unit name, not the bundle's own: bundles a layout rule merges
		// into one directory share one artifact, so they must share one
		// source. Under the default rules every bundle is its own unit.
		unit := ix.UnitName(b)
		url := base + "/" + unit
		// The flags checked the prefix; the unit name appended to it can
		// still push the path past its length limit.
		if err := checkOCIRepository(url, true); err != nil {
			return nil, errors.Wrapf(err, "bundle %q: artifact url %q", unit, url)
		}
		b.SourceRef = &stack.SourceRef{
			Kind: "OCIRepository",
			Name: unit,
			URL:  url,
			Tag:  opts.tag,
		}
	}

	objs, err := fluxcd.NewResourceGenerator().GenerateFromLayout(ml, cluster)
	if err != nil {
		return nil, err
	}
	out := &deliveryOutput{}
	for i := range objs {
		if k, ok := objs[i].(*kustv1.Kustomization); ok {
			k.Spec.Path = artifactKustomizationPath
		}
		out.flux = append(out.flux, &objs[i])
	}

	for _, l := range ix.Units() {
		bundles := l.OriginBundles()
		a := deliveryArtifact{name: ix.UnitName(bundles[0])}
		for _, b := range bundles {
			bo := l.OriginBundleObjects(b)
			for i := range bo {
				a.objects = append(a.objects, &bo[i])
			}
		}
		out.artifacts = append(out.artifacts, a)
	}
	if err := out.checkCollisions(); err != nil {
		return nil, err
	}
	return out, nil
}

// objectIdentity is what the API server tells two objects apart by: API
// group, kind, namespace and name. The version is left out, since one object
// served at two versions is still one object.
type objectIdentity struct {
	group, kind, namespace, name string
}

func identityOf(o client.Object) objectIdentity {
	gvk := o.GetObjectKind().GroupVersionKind()
	return objectIdentity{group: gvk.Group, kind: gvk.Kind, namespace: o.GetNamespace(), name: o.GetName()}
}

func (id objectIdentity) String() string {
	kind := id.kind
	if id.group != "" {
		kind += "." + id.group
	}
	return kind + " " + id.namespace + "/" + id.name
}

// checkCollisions refuses an output in which an artifact carries an object
// with the identity of a delivery object: reconciling that artifact would
// apply it over the OCIRepository or Kustomization that reconciles it (or
// another unit's), redirecting its source or fighting over its spec. The
// delivery objects are named after reconciliation units by kure's naming and
// are not renamed here, so the build is refused instead; the check covers
// every artifact object alike, whatever component or trait rendered it.
//
// Delivery objects never collide with each other: kure names each unit's
// Kustomization after the unit's first bundle, and layout.IndexOrigins
// refuses two bundles with one name, while GenerateFromLayout emits a source
// shared by two units once and refuses two different definitions of it.
//
// Namespaces are compared exactly. An artifact object with no namespace is
// not read as the delivery objects' namespace: the generated Kustomizations
// set no spec.targetNamespace and the artifact's kustomization.yaml no
// namespace, and kustomize-controller's server-side apply refuses a namespaced
// object without one ("namespace not specified"), so it is never applied as
// the delivery object of that name.
//
// An artifact object that is a list envelope is applied as its members, so
// every member is compared too (listMembers).
func (d *deliveryOutput) checkCollisions() error {
	delivery := make(map[objectIdentity]bool, len(d.flux))
	for _, o := range d.flux {
		delivery[identityOf(*o)] = true
	}
	for _, a := range d.artifacts {
		for _, o := range a.objects {
			id := identityOf(*o)
			if delivery[id] {
				return collisionError(a.name, id.String())
			}
			members, err := listMembers(*o)
			if err != nil {
				return errors.Wrapf(err, "artifact %q: reading the list members of %s", a.name, id)
			}
			for _, m := range members {
				if mid := identityOf(m); delivery[mid] {
					return collisionError(a.name, mid.String()+" (a member of list "+id.String()+")")
				}
			}
		}
	}
	return nil
}

func collisionError(artifact, object string) error {
	return errors.Errorf("artifact %q carries %s, which is also a Flux delivery object this build generates: rename the component that renders it, or the application, so that no artifact object has a delivery object's kind, namespace and name",
		artifact, object)
}

// listMembers returns the objects o carries as a list envelope, at every
// depth: each object in its items array and, recursively, theirs. Nothing
// applies a list envelope itself; reconciling an artifact applies its
// members:
//
//   - kustomize replaces an object whose kind ends in "List" and that has an
//     items field by its items, recursively (Factory.inlineAnyEmbeddedLists in
//     sigs.k8s.io/kustomize/api/resource/factory.go);
//   - kustomize-controller then decodes kustomize's output with
//     ReadObjects (github.com/fluxcd/pkg/ssa/utils/object.go), which
//     replaces any object whose items field is an array, whatever its kind,
//     by its items.
//
// Expanding every items array at every depth covers both, and can only
// compare more objects than are applied, never fewer. An item that is not an
// object is skipped: kustomize and ReadObjects both refuse it, so it is never
// applied.
func listMembers(o client.Object) ([]client.Object, error) {
	content, err := objectContent(o)
	if err != nil {
		return nil, err
	}
	items, ok := content["items"].([]any)
	if !ok {
		return nil, nil
	}
	var members []client.Object
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		member := &unstructured.Unstructured{Object: m}
		nested, err := listMembers(member)
		if err != nil {
			return nil, err
		}
		members = append(members, member)
		members = append(members, nested...)
	}
	return members, nil
}

// objectContent is o as its manifest encodes it: an unstructured object's own
// content, otherwise o's JSON encoding decoded, which is how
// kio.EncodeObjectsToYAML serializes a typed object.
func objectContent(o client.Object) (map[string]any, error) {
	if u, ok := o.(runtime.Unstructured); ok {
		return u.UnstructuredContent(), nil
	}
	data, err := json.Marshal(o)
	if err != nil {
		return nil, errors.Wrap(err, "encoding the object")
	}
	var content map[string]any
	if err := json.Unmarshal(data, &content); err != nil {
		return nil, errors.Wrap(err, "decoding the object")
	}
	return content, nil
}

// write writes the delivery output into dir. Unit and application names are
// DNS-1123 subdomains (oam validation), so they are safe path segments.
func (d *deliveryOutput) write(dir, appName string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return errors.Wrapf(err, "creating output directory %q", dir)
	}
	for _, a := range d.artifacts {
		adir := filepath.Join(dir, a.name)
		if err := os.MkdirAll(adir, 0755); err != nil {
			return errors.Wrapf(err, "creating artifact directory %q", adir)
		}
		manifestsPath := filepath.Join(adir, artifactManifestsFile)
		kustomization := emptyArtifactKustomization
		if len(a.objects) > 0 {
			data, err := kio.EncodeObjectsToYAML(a.objects)
			if err != nil {
				return errors.Wrapf(err, "encoding artifact %q", a.name)
			}
			if err := os.WriteFile(manifestsPath, data, 0644); err != nil {
				return errors.Wrapf(err, "writing %q", manifestsPath)
			}
			kustomization = artifactKustomization
		} else if err := os.Remove(manifestsPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			// An empty artifact must not ship a manifests.yaml an earlier
			// build left behind.
			return errors.Wrapf(err, "removing stale %q", manifestsPath)
		}
		kustPath := filepath.Join(adir, artifactKustomizationFile)
		if err := os.WriteFile(kustPath, []byte(kustomization), 0644); err != nil {
			return errors.Wrapf(err, "writing %q", kustPath)
		}
	}
	data, err := kio.EncodeObjectsToYAML(d.flux)
	if err != nil {
		return errors.Wrap(err, "encoding Flux delivery objects")
	}
	fluxPath := filepath.Join(dir, appName+".flux.yaml")
	//nolint:gosec // G703: appName is Metadata.Name, which parsing already required to
	// be a DNS-1123 subdomain (no '/' or '..'), and dir is the operator's own
	// --output flag — the same reasoning as writeOutputDir in build.go.
	if err := os.WriteFile(fluxPath, data, 0644); err != nil {
		return errors.Wrapf(err, "writing %q", fluxPath)
	}
	return nil
}
