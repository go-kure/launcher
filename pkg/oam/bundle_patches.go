package oam

import (
	"bytes"
	"io"
	"path"
	"strings"
	"sync"

	kio "github.com/go-kure/kure/pkg/io"
	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
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
// krusty under kustomize-controller's options, then read back as Flux reads them
// (fluxReadObjects). The objects come in kustomize's build order (input order,
// except that a List's members follow the other objects), list envelopes expanded
// to their members. A patch set that does not build, or whose result does not
// serialize or read, is an error.
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
// as an error. It does not sort, as Flux's zero-value Reorder option does not (it
// is neither legacy nor unspecified), named here so a change of default cannot
// reorder it. (Build also resets kustomize's global OpenAPI schema
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
	// kustomize-controller serializes the build and reads the objects back from
	// the YAML (fluxReadObjects), so they are read here the same way: a result that
	// does not serialize or read fails the build, and a value the YAML changes (a
	// date becomes a string) is read as Flux reads it.
	manifests, err := res.AsYaml()
	if err != nil {
		return nil, errors.Wrap(err, "serializing the patched objects")
	}
	out, err = fluxReadObjects(bytes.NewReader(manifests))
	if err != nil {
		return nil, errors.Wrap(err, "reading the patched objects")
	}
	return out, nil
}

// fluxReadObjects mirrors fluxcd/pkg/ssa utils.ReadObjects (ssa/v0.77.0), which
// kustomize-controller reads a build's YAML with: a list (apimachinery's IsList:
// items is an array) stands for its members, one level only and unfiltered, and
// a member that is not an object is an error; any other document is kept only if
// it is a Kubernetes object (isKubernetesObject) and not a kustomize config
// (isKustomization).
func fluxReadObjects(r io.Reader) ([]*unstructured.Unstructured, error) {
	reader := utilyaml.NewYAMLOrJSONDecoder(r, 2048)
	objects := make([]*unstructured.Unstructured, 0)

	for {
		obj := &unstructured.Unstructured{}
		err := reader.Decode(obj)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return objects, err
		}

		if obj.IsList() {
			err = obj.EachListItem(func(item runtime.Object) error {
				obj := item.(*unstructured.Unstructured)
				objects = append(objects, obj)
				return nil
			})
			if err != nil {
				return objects, err
			}
			continue
		}

		if isKubernetesObject(obj) && !isKustomization(obj) {
			objects = append(objects, obj)
		}
	}

	return objects, nil
}

// isKubernetesObject mirrors fluxcd/pkg/ssa utils.IsKubernetesObject (ssa/v0.77.0):
// the object has a name, a kind and an apiVersion.
func isKubernetesObject(object *unstructured.Unstructured) bool {
	if object.GetName() == "" || object.GetKind() == "" || object.GetAPIVersion() == "" {
		return false
	}
	return true
}

// isKustomization mirrors fluxcd/pkg/ssa utils.IsKustomization (ssa/v0.77.0): the
// object is a kustomize config.
func isKustomization(object *unstructured.Unstructured) bool {
	return strings.ToLower(object.GetKind()) == "kustomization" &&
		strings.HasPrefix(object.GetAPIVersion(), "kustomize.config.k8s.io/")
}
