package oam

import (
	"path"
	"sync"

	kio "github.com/go-kure/kure/pkg/io"
	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/api/krusty"
	kustypes "sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/errors"
)

// kustomizeBuildMutex serializes kustomize builds, as kustomize-controller
// serializes them: krusty is not safe for concurrent use
// (kubernetes-sigs/kustomize#3659).
var kustomizeBuildMutex sync.Mutex

const (
	patchedBundleDir       = "/bundle"
	patchedBundleManifests = "manifests.yaml"
)

// applyBundlePatches returns the resources kustomize builds for one leaf bundle
// whose Kustomization carries patches (spec.patches, which the fluxcd-patches
// trait sets), as kustomize-controller builds them: the objects encoded as a
// delivered artifact encodes them, a kustomization.yaml listing them with the
// patches appended in order and each target mapped field for field, built with
// krusty under kustomize-controller's options. The resources keep their input
// order (a patch never reorders them), list envelopes not yet expanded the way
// Flux's ReadObjects expands them. A patch set that does not build is an error.
func applyBundlePatches(objects []*client.Object, patches []stack.Patch) ([]*unstructured.Unstructured, error) {
	manifests, err := kio.EncodeObjectsToYAML(objects)
	if err != nil {
		return nil, errors.Wrap(err, "encoding the bundle's objects")
	}
	kust := kustypes.Kustomization{
		TypeMeta:  kustypes.TypeMeta{APIVersion: kustypes.KustomizationVersion, Kind: kustypes.KustomizationKind},
		Resources: []string{patchedBundleManifests},
	}
	for _, p := range patches {
		patch := kustypes.Patch{Patch: p.Patch}
		if s := p.Target; s != nil {
			patch.Target = &kustypes.Selector{LabelSelector: s.LabelSelector, AnnotationSelector: s.AnnotationSelector}
			patch.Target.Group = s.Group
			patch.Target.Version = s.Version
			patch.Target.Kind = s.Kind
			patch.Target.Name = s.Name
			patch.Target.Namespace = s.Namespace
		}
		kust.Patches = append(kust.Patches, patch)
	}
	kustYAML, err := yaml.Marshal(kust)
	if err != nil {
		return nil, errors.Wrap(err, "encoding the bundle's kustomization")
	}
	fs := filesys.MakeFsInMemory()
	if err := fs.WriteFile(path.Join(patchedBundleDir, patchedBundleManifests), manifests); err != nil {
		return nil, errors.Wrap(err, "writing the bundle's objects")
	}
	if err := fs.WriteFile(path.Join(patchedBundleDir, "kustomization.yaml"), kustYAML); err != nil {
		return nil, errors.Wrap(err, "writing the bundle's kustomization")
	}
	return kustomizeBuild(fs, patchedBundleDir)
}

// kustomizeBuild runs krusty on dir as fluxcd/pkg/kustomize Build runs it: under
// a mutex, with load restrictions off and plugins disabled, and a panic recovered
// as an error. It keeps the input order, as Flux's zero-value Reorder option does
// (it is neither legacy nor unspecified, so nothing is sorted), named here so a
// change of default cannot reorder it: each resource is traced to the object it
// was built from by position. (Build also resets kustomize's global OpenAPI schema
// around each build, which matters only to a kustomization with an openapi
// field; this one has none.)
func kustomizeBuild(fs filesys.FileSystem, dir string) (out []*unstructured.Unstructured, err error) {
	kustomizeBuildMutex.Lock()
	defer kustomizeBuildMutex.Unlock()
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, errors.Errorf("kustomize build panicked: %v", r)
		}
	}()
	res, err := krusty.MakeKustomizer(&krusty.Options{
		Reorder:          krusty.ReorderOptionNone,
		LoadRestrictions: kustypes.LoadRestrictionsNone,
		PluginConfig:     kustypes.DisabledPluginConfig(),
	}).Run(fs, dir)
	if err != nil {
		return nil, errors.Wrap(err, "building the bundle's patches")
	}
	for _, r := range res.Resources() {
		m, err := r.Map()
		if err != nil {
			return nil, errors.Wrap(err, "reading the patched objects")
		}
		out = append(out, &unstructured.Unstructured{Object: m})
	}
	return out, nil
}
