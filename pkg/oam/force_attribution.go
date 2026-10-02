package oam

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
)

// forceOriginAnnotation tags each object of a bundle's attribution build with its
// index among the objects the bundle generates. It is launcher's own key, so
// removing it touches no annotation a user or a patch sets, and only the
// attribution build carries it: the build Flux applies and the generated output
// never do.
const forceOriginAnnotation = "launcher.gokure.dev/force-warning-origin"

// attributionBuild builds the tagged copy of a bundle. It is
// applyBundlePatches; a test replaces it to pin that the verdict never depends
// on it.
var attributionBuild = applyBundlePatches

// patchedVolume is one volume of a bundle's patch build. id and selected are the
// verdict, read from the build Flux applies; producer, generatedSelected and
// order only name it.
type patchedVolume struct {
	id                          objectIdentity
	producer                    string
	selected, generatedSelected bool
	built, order                int
}

// volumeOrigin is one object a bundle generates, as Flux applies it: the
// application that generates it and whether Flux's force selector matches it.
type volumeOrigin struct {
	app      GeneratedApplication
	selected bool
}

// attributePatched names the volumes of one leaf bundle's patch build by the
// generated objects they come from, where an attribution build proves it. It
// builds the bundle a second time, every object Flux would apply as generated
// tagged with its origin (forceOriginAnnotation), and uses the tags only when
// that build, with the tag removed, is exactly the build Flux applies: the same
// objects, serialized to the same bytes, in the same order. A volume carrying the
// tag of exactly one built object is then named by that origin's application, its
// force key as generated if the origin carried it, and the volumes follow
// generation order, untagged ones last in build order. A build error or any
// difference leaves the volumes as given; so does an untagged volume (a patch
// dropped its annotations, or added it) and one whose tag another object also
// carries (a patch copied annotations). It changes neither a volume's identity nor
// whether it is forced.
func attributePatched(volumes []patchedVolume, built []*unstructured.Unstructured, apps []GeneratedApplication) {
	objects, origins, err := taggedObjects(apps)
	if err != nil {
		return
	}
	shadow, err := attributionBuild(objects, apps[0].Patches)
	if err != nil || len(shadow) != len(built) {
		return
	}
	traced := make([]int, len(shadow))
	holders := make([]int, len(origins))
	for k, obj := range shadow {
		stripped, tag, tagged := withoutOriginTag(obj)
		if !sameObject(stripped, built[k]) {
			return
		}
		traced[k] = -1
		if i, err := strconv.Atoi(tag); tagged && err == nil && i >= 0 && i < len(origins) {
			traced[k] = i
			holders[i]++
		}
	}
	for j := range volumes {
		v := &volumes[j]
		v.order = len(origins) + v.built
		i := traced[v.built]
		if i < 0 || holders[i] != 1 {
			continue
		}
		v.producer = origins[i].app.String()
		v.generatedSelected = origins[i].selected
		v.order = i
	}
	slices.SortStableFunc(volumes, func(a, b patchedVolume) int { return a.order - b.order })
}

// taggedObjects returns copies of apps' objects, as their manifests encode them,
// with every object Flux would apply (appliedObjects) tagged with its index among
// the returned origins.
func taggedObjects(apps []GeneratedApplication) ([]*client.Object, []volumeOrigin, error) {
	var objects []*client.Object
	var origins []volumeOrigin
	for _, app := range apps {
		for _, p := range generatedObjects(app) {
			content, err := objectContent(*p)
			if err != nil {
				return nil, nil, err
			}
			var obj client.Object = &unstructured.Unstructured{Object: content}
			// appliedObjects shares content's maps, so each tag lands in obj.
			for _, applied := range appliedObjects(obj) {
				origins = append(origins, volumeOrigin{app: app, selected: forceSelected(applied)})
				annotations := applied.GetAnnotations()
				if annotations == nil {
					annotations = map[string]string{}
				}
				annotations[forceOriginAnnotation] = strconv.Itoa(len(origins) - 1)
				applied.SetAnnotations(annotations)
			}
			objects = append(objects, &obj)
		}
	}
	return objects, origins, nil
}

// objectContent returns a copy of obj's content as its manifest encodes it: its
// JSON encoding, decoded.
func objectContent(obj client.Object) (map[string]any, error) {
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, errors.Wrap(err, "encoding a generated object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var content map[string]any
	if err := decoder.Decode(&content); err != nil {
		return nil, errors.Wrap(err, "decoding a generated object")
	}
	return content, nil
}

// withoutOriginTag returns a copy of obj without the origin tag, and the tag. An
// annotations map the tag alone made is removed with it.
func withoutOriginTag(obj *unstructured.Unstructured) (*unstructured.Unstructured, string, bool) {
	out := obj.DeepCopy()
	metadata, _ := out.Object["metadata"].(map[string]any)
	annotations, _ := metadata["annotations"].(map[string]any)
	tag, found := annotations[forceOriginAnnotation]
	if !found {
		return out, "", false
	}
	delete(annotations, forceOriginAnnotation)
	if len(annotations) == 0 {
		delete(metadata, "annotations")
	}
	s, ok := tag.(string)
	return out, s, ok
}

// sameObject reports whether a and b serialize to the same bytes.
func sameObject(a, b *unstructured.Unstructured) bool {
	x, err := json.Marshal(a.Object)
	if err != nil {
		return false
	}
	y, err := json.Marshal(b.Object)
	return err == nil && bytes.Equal(x, y)
}

// forceRelevant reports whether any of volumes is force-applied, its bundle
// forcing it or Flux's force selector matching it: only then is there a warning
// for an attribution build to name.
func forceRelevant(volumes []patchedVolume, forced bool) bool {
	return (forced && len(volumes) > 0) || slices.ContainsFunc(volumes, func(v patchedVolume) bool { return v.selected })
}
