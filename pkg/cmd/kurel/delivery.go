package kurel

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"
	"github.com/spf13/cobra"
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
// --oci-repository that is not an oci:// URL, and --oci-repository without
// --output: the delivery output is a directory tree, which stdout cannot carry.
func validateDeliveryFlags(opts *buildOptions, repositorySet, tagSet bool) error {
	if !repositorySet {
		if tagSet {
			return errors.Errorf("--%s requires --%s", ociTagFlag, ociRepositoryFlag)
		}
		return nil
	}
	rest, ok := strings.CutPrefix(opts.delivery.repository, ociScheme)
	if !ok || strings.Trim(rest, "/") == "" {
		return errors.Errorf("--%s %q must be an %s URL naming a registry and optional path prefix, e.g. %sregistry.example.com/apps",
			ociRepositoryFlag, opts.delivery.repository, ociScheme, ociScheme)
	}
	if opts.outputDir == "" {
		return errors.Errorf("--%s requires --output: the Flux delivery output is a directory tree (one artifact directory per bundle plus a Flux objects file) and cannot be written to stdout", ociRepositoryFlag)
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
		b.SourceRef = &stack.SourceRef{
			Kind: "OCIRepository",
			Name: unit,
			URL:  base + "/" + unit,
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
	return out, nil
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
