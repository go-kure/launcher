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

// applyBundlePatches returns the objects Flux applies for one leaf bundle whose
// Kustomization carries patches (spec.patches, which the fluxcd-patches trait
// sets). It builds them as kustomize-controller does: the objects encoded as a
// delivered artifact encodes them, a kustomization.yaml listing them with the
// patches appended in order and each target mapped field for field, built with
// krusty under kustomize-controller's options, and read back as Flux's
// ReadObjects reads a build (a list envelope still standing stands for its
// members). A patch set that does not build is an error.
func applyBundlePatches(objects []*client.Object, patches []stack.Patch) ([]client.Object, error) {
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
	built, err := kustomizeBuild(fs, patchedBundleDir)
	if err != nil {
		return nil, err
	}
	var out []client.Object
	for _, m := range built {
		u := &unstructured.Unstructured{Object: m}
		if u.IsList() {
			out = append(out, listMembers(u)...)
			continue
		}
		out = append(out, u)
	}
	return out, nil
}

// kustomizeBuild runs krusty on dir as fluxcd/pkg/kustomize Build runs it: under
// a mutex, with load restrictions off and plugins disabled, and a panic recovered
// as an error. (Build also resets kustomize's global OpenAPI schema around each
// build, which matters only to a kustomization with an openapi field; this one has
// none.)
func kustomizeBuild(fs filesys.FileSystem, dir string) (out []map[string]any, err error) {
	kustomizeBuildMutex.Lock()
	defer kustomizeBuildMutex.Unlock()
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, errors.Errorf("kustomize build panicked: %v", r)
		}
	}()
	res, err := krusty.MakeKustomizer(&krusty.Options{
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
		out = append(out, m)
	}
	return out, nil
}
