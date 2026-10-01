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

// checkCollisions refuses an output in which one object identity has two
// owners, since every object reconciliation applies must be managed by
// exactly one Flux Kustomization:
//
//   - an artifact object with the identity of a delivery object: reconciling
//     that artifact would apply it over the OCIRepository or Kustomization
//     that reconciles it (or another unit's), redirecting its source or
//     fighting over its spec. The delivery objects are named after
//     reconciliation units by kure's naming and are not renamed here, so the
//     build is refused instead;
//   - an object in two artifacts (e.g. two components in different units with
//     a configmap trait of one name): each unit's Kustomization would apply it,
//     so they would overwrite each other's version and each would prune it
//     when it leaves that unit;
//   - an object twice in one artifact: kustomize refuses to build it ("may not
//     add resource with an already registered id", Append in
//     sigs.k8s.io/kustomize/api/resmap/reswrangler.go), so the Kustomization
//     would never become Ready. Refusing it here names the object at build
//     time, and also covers one object at two API versions, which kustomize
//     would accept and apply twice.
//
// The check covers every artifact object alike, whatever component or trait
// rendered it. Delivery objects never collide with each other: kure names
// each unit's Kustomization after the unit's first bundle, and
// layout.IndexOrigins refuses two bundles with one name, while
// GenerateFromLayout emits a source shared by two units once and refuses two
// different definitions of it.
//
// Namespaces are compared exactly. An artifact object with no namespace is
// not read as the delivery objects' namespace: the generated Kustomizations
// set no spec.targetNamespace and the artifact's kustomization.yaml no
// namespace, and kustomize-controller's server-side apply refuses a namespaced
// object without one ("namespace not specified"), so it is never applied as
// the delivery object of that name. Two artifact objects with no namespace and
// one kind and name are one object (a cluster-scoped one), or two that are
// both refused at apply, so they are refused here either way.
//
// Only what reconciliation applies owns an identity (appliedObjects): a list
// envelope it expands owns nothing, and each member it applies is compared,
// whatever fields that member has. An object kustomize-controller's decoder
// drops (fluxStage) owns nothing either, so one carried twice in an artifact
// is left to kustomize's own refusal.
func (d *deliveryOutput) checkCollisions() error {
	delivery := make(map[objectIdentity]bool, len(d.flux))
	for _, o := range d.flux {
		delivery[identityOf(*o)] = true
	}
	owner := map[objectIdentity]ownership{}
	for _, a := range d.artifacts {
		for _, o := range a.objects {
			top := identityOf(*o)
			applied, err := appliedObjects(*o)
			if err != nil {
				return errors.Wrapf(err, "artifact %q: %s", a.name, top)
			}
			for _, m := range applied {
				id := identityOf(m.object)
				object := id.String()
				if m.member {
					object += " (a member of list " + top.String() + ")"
				}
				if delivery[id] {
					return collisionError(a.name, object)
				}
				if prev, ok := owner[id]; ok {
					return duplicateError(prev, ownership{a.name, object})
				}
				owner[id] = ownership{a.name, object}
			}
		}
	}
	return nil
}

func collisionError(artifact, object string) error {
	return errors.Errorf("artifact %q carries %s, which is also a Flux delivery object this build generates: rename the component that renders it, or the application, so that no artifact object has a delivery object's kind, namespace and name",
		artifact, object)
}

// ownership is the artifact that carries an object identity, and the object
// as an error names it.
type ownership struct{ artifact, object string }

// duplicateError refuses one object identity carried twice: by two artifacts,
// or twice by one.
func duplicateError(first, second ownership) error {
	also := ""
	if first.object != second.object {
		also = " (also as " + first.object + ")"
	}
	if first.artifact == second.artifact {
		return errors.Errorf("artifact %q carries %s twice%s: kustomize refuses to build an artifact with one object in it twice; rename one of the components or traits that render it",
			second.artifact, second.object, also)
	}
	return errors.Errorf("artifacts %q and %q both carry %s%s: the Flux Kustomization of each would apply and prune that one object; rename one of the components or traits that render it, so that each object belongs to one bundle",
		first.artifact, second.artifact, second.object, also)
}

// appliedObject is one object reconciling an artifact applies; member is
// false when it is the artifact object itself, true when a list envelope
// carried it.
type appliedObject struct {
	object client.Object
	member bool
}

// appliedObjects returns what reconciling an artifact applies of its object o.
// Reconciliation is two stages, and appliedObjects is exactly their
// composition: kustomizeStage on o, then fluxStage on each object that yields.
//
//  1. kustomize builds the artifact (kustomizeStage);
//  2. kustomize-controller decodes kustomize's output with ReadObjects and
//     applies what that returns (fluxStage).
//
// A list envelope either stage expands is never applied and owns nothing;
// every object the second stage returns is applied as is, and owns its
// identity even when it has an items field of its own.
func appliedObjects(o client.Object) ([]appliedObject, error) {
	content, err := objectContent(o)
	if err != nil {
		return nil, errors.Wrap(err, "reading the object")
	}
	built, err := kustomizeStage(content)
	if err != nil {
		return nil, err
	}
	var applied []appliedObject
	for _, b := range built {
		objs, err := fluxStage(b)
		if err != nil {
			return nil, err
		}
		for _, m := range objs {
			if m.object == nil {
				// o itself, neither expanded nor dropped.
				m.object = o
			}
			applied = append(applied, m)
		}
	}
	return applied, nil
}

// builtObject is one object kustomizeStage yields: content, and whether a
// *List envelope carried it (false for the artifact object itself).
type builtObject struct {
	content map[string]any
	member  bool
}

// kustomizeStage is what kustomize's build makes of one artifact object
// (Factory.inlineAnyEmbeddedLists, sigs.k8s.io/kustomize/api v0.21.1
// resource/factory.go:187-227, applied to each item by
// convertObjectSliceToNodeSlice and dropBadNodes, factory.go:230-268): an
// object whose kind ends in "List" and that has an items field is replaced by
// its items, recursively. Without an items field it is kept as is; with items
// null it yields nothing; with items of any other type kustomize refuses the
// build, and so does this. An empty object item is dropped (dropBadNodes). An
// item that is not an object is refused here: kustomize refuses a null or
// non-empty scalar one and drops an empty or zero one, and applies nothing of
// it either way. Any other object is kept as is, whatever items field it has.
//
// When an artifact's only object is a List or ResourceList, kustomize's
// reader unwraps it before that (ByteReader, sigs.k8s.io/kustomize/kyaml
// v0.21.1 kio/byteio_reader.go:247-271): the same members, but it reads a
// malformed one, or one with a functionConfig field and no items, as nothing
// rather than refusing or keeping it. Where the two differ this refuses or
// keeps the List, which can only refuse an artifact, never let an applied
// object through unchecked.
func kustomizeStage(content map[string]any) ([]builtObject, error) {
	return kustomizeExpand(content, false)
}

func kustomizeExpand(content map[string]any, member bool) ([]builtObject, error) {
	kind, _ := content["kind"].(string)
	items, hasItems := content["items"]
	if !strings.HasSuffix(kind, "List") || !hasItems {
		return []builtObject{{content: content, member: member}}, nil
	}
	if items == nil {
		return nil, nil
	}
	slice, ok := items.([]any)
	if !ok {
		return nil, errors.Errorf("%s has items of type %T, not an array: kustomize refuses to build it", kind, items)
	}
	var built []builtObject
	for _, item := range slice {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, errors.Errorf("%s has an item of type %T, not an object: kustomize refuses to build it", kind, item)
		}
		if len(m) == 0 {
			continue
		}
		inner, err := kustomizeExpand(m, true)
		if err != nil {
			return nil, err
		}
		built = append(built, inner...)
	}
	return built, nil
}

// fluxStage is what kustomize-controller applies of one object kustomize
// built: ReadObjects (github.com/fluxcd/pkg/ssa v0.76.2 utils/object.go:44-77)
// replaces an object whose items field is an array, whatever its kind, by its
// immediate members, appended as is without expanding them further, and
// refuses a member that is not an object. It drops any other object that is
// not a Kubernetes object (no apiVersion, kind or name) or is a kustomize
// config Kustomization (utils/is.go:71-82). A returned appliedObject with a
// nil object stands for b itself when b is the artifact object.
func fluxStage(b builtObject) ([]appliedObject, error) {
	if items, isArray := b.content["items"].([]any); isArray {
		applied := make([]appliedObject, 0, len(items))
		for _, item := range items {
			m, ok := item.(map[string]any)
			if !ok {
				kind, _ := b.content["kind"].(string)
				return nil, errors.Errorf("%s has an item of type %T, not an object: kustomize-controller refuses to apply it", kind, item)
			}
			applied = append(applied, appliedObject{object: &unstructured.Unstructured{Object: m}, member: true})
		}
		return applied, nil
	}
	u := &unstructured.Unstructured{Object: b.content}
	if u.GetName() == "" || u.GetKind() == "" || u.GetAPIVersion() == "" ||
		(strings.ToLower(u.GetKind()) == "kustomization" && strings.HasPrefix(u.GetAPIVersion(), "kustomize.config.k8s.io/")) {
		return nil, nil
	}
	if !b.member {
		return []appliedObject{{}}, nil
	}
	return []appliedObject{{object: u, member: true}}, nil
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

// maxFileNameBytes is the longest file or directory name common filesystems
// accept (NAME_MAX on ext4, XFS, Btrfs and tmpfs); a longer one fails with
// ENAMETOOLONG.
const maxFileNameBytes = 255

// checkFileNames refuses, before anything is written, an output whose file
// or directory names the filesystem would refuse partway through the write:
// <appName>.flux.yaml, since a DNS-1123 subdomain may be 253 characters, and
// each artifact directory, named by its unit (which generateDelivery's
// per-bundle url check already bounds, as the unit name is part of the url's
// path). The remaining names, manifests.yaml and kustomization.yaml, are
// fixed.
func (d *deliveryOutput) checkFileNames(appName string) error {
	if n := len(fluxFileName(appName)); n > maxFileNameBytes {
		return errors.Errorf("the Flux objects file name %q is %d bytes, more than the %d-byte file name limit: shorten the application name to at most %d characters",
			fluxFileName(appName), n, maxFileNameBytes, maxFileNameBytes-len(fluxFileName("")))
	}
	for _, a := range d.artifacts {
		if n := len(a.name); n > maxFileNameBytes {
			return errors.Errorf("the artifact directory name %q is %d bytes, more than the %d-byte file name limit: shorten the application or component name",
				a.name, n, maxFileNameBytes)
		}
	}
	return nil
}

// fluxFileName is the name of the file the delivery objects are written to.
func fluxFileName(appName string) string { return appName + ".flux.yaml" }

// write writes the delivery output into dir. Unit and application names are
// DNS-1123 subdomains (oam validation), so they are safe path segments, and
// checkFileNames bounds their length before the first write.
func (d *deliveryOutput) write(dir, appName string) error {
	if err := d.checkFileNames(appName); err != nil {
		return err
	}
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
	fluxPath := filepath.Join(dir, fluxFileName(appName))
	//nolint:gosec // G703: appName is Metadata.Name, which parsing already required to
	// be a DNS-1123 subdomain (no '/' or '..'), and dir is the operator's own
	// --output flag — the same reasoning as writeOutputDir in build.go.
	if err := os.WriteFile(fluxPath, data, 0644); err != nil {
		return errors.Wrapf(err, "writing %q", fluxPath)
	}
	return nil
}
