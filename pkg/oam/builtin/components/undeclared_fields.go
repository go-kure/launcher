package components

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"io"
	"strings"
	"sync"

	kureio "github.com/go-kure/kure/pkg/io"
	"github.com/go-kure/kure/pkg/kubernetes"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utiljson "k8s.io/apimachinery/pkg/util/json"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
)

// A document of a kind kure's scheme registers is decoded into the Go type of
// that kind, and kure's parser decodes leniently: a field the type does not
// declare is dropped without a word. This file is what the three readers of
// such documents (template delivery, the manifests and crd components, and the
// passthrough policy check) do about it:
//
//   - a workload or a claim (isWorkloadGVK, the kinds enforceRenderedObjectPolicy
//     reads as Go types, a PersistentVolume among them) is refused. The policy check cannot see a field its
//     type does not declare, so a pod spec field newer than the vendored API
//     would pass unchecked, and template delivery and manifests would then emit
//     the object without it.
//   - any other registered kind is emitted as the document was written, as an
//     unstructured object, so the field is kept. Nothing reads such an object
//     as its Go type: the policy check passes it, and scope, namespace and
//     metadata are read through interfaces an unstructured object satisfies.
//
// The strict decode is the one over kure's own scheme, so it reports exactly
// what kure's lenient decode drops, with one known limit: a type that
// unmarshals itself (a CustomResourceDefinition's schema `items`, for one)
// decodes its own keys, and an undeclared key inside it is not reported.

// unknownFieldPrefix starts the message of a strict decoding error that names
// a field the Go type does not declare (sigs.k8s.io/json); the field's path
// follows, quoted. The same strict decode reports a duplicated key with another
// message, which is not a lost field and is left to the lenient decode.
const unknownFieldPrefix = `unknown field "`

// strictErrorLimit is the number of strict decoding errors the decoder records
// for one document (sigs.k8s.io/json); it drops the rest. A JSON document that
// writes that many keys twice fills the record with those, and an undeclared
// field after them is not reported. TestUndeclaredFields_StrictErrorLimit holds
// the number to the decoder's.
const strictErrorLimit = 100

// errStrictRecordFull is undeclaredFields' refusal of a document whose record
// of strict errors is full and names no undeclared field.
var errStrictRecordFull = errors.Errorf("the document writes %d or more keys twice, more than the decoder reports, so whether it sets a field its type does not declare cannot be told; write each key once", strictErrorLimit)

// strictDecoder is kure's decoder with strict decoding on: the same scheme and
// the same serializers, so it differs from kure's parser only in reporting what
// that parser drops.
var strictDecoder = sync.OnceValues(func() (runtime.Decoder, error) {
	if err := kubernetes.RegisterSchemes(); err != nil {
		return nil, errors.Wrap(err, "registering the kinds this build can read")
	}
	return serializer.NewCodecFactory(kubernetes.Scheme, serializer.EnableStrict).UniversalDeserializer(), nil
})

// undeclaredFields returns the paths of the fields in doc, one JSON document,
// that the Go type of its kind does not declare, in the order of the document's
// keys. It returns
// none for a document that decodes without loss and for one of a kind kure's
// scheme does not register, which is not decoded into a Go type at all. A
// document that does not decode is an error, and so is one that writes so many
// keys twice that the decoder's record of strict errors is full and names no
// undeclared field: whether it sets one cannot be told.
func undeclaredFields(doc []byte) ([]string, error) {
	decoder, err := strictDecoder()
	if err != nil {
		return nil, err
	}
	_, _, err = decoder.Decode(doc, nil, nil)
	if err == nil || runtime.IsNotRegisteredError(err) {
		return nil, nil
	}
	strict, ok := runtime.AsStrictDecodingError(err)
	if !ok {
		return nil, err
	}
	recorded := strict.Errors()
	var paths []string
	for _, e := range recorded {
		if path, ok := strings.CutPrefix(e.Error(), unknownFieldPrefix); ok {
			paths = append(paths, strings.TrimSuffix(path, `"`))
		}
	}
	if len(paths) == 0 && len(recorded) >= strictErrorLimit {
		return nil, errStrictRecordFull
	}
	return paths, nil
}

// undeclaredFieldsError is the refusal of a workload or claim that sets fields
// its Go type does not declare. The caller names the object.
func undeclaredFieldsError(gvk schema.GroupVersionKind, paths []string) error {
	noun, pronoun := "field", "it"
	if len(paths) > 1 {
		noun, pronoun = "fields", "them"
	}
	return errors.Errorf("undeclared %s %s: the %s %s type this build reads the object with does not declare %s, so the object cannot be checked against environment policy",
		noun, strings.Join(paths, ", "), gvk.GroupVersion(), gvk.Kind, pronoun)
}

// decodeManifestDocuments decodes multi-document YAML or JSON into Kubernetes
// objects with kure's parser, unstructured objects allowed, and settles what
// that parser drops (see the note at the top of this file). An object whose
// group, version and kind kure's scheme registers is its Go type
// (*appsv1.Deployment, *batchv1.Job); any other is *unstructured.Unstructured,
// and an unregistered list kind is replaced by its items. An empty or
// comment-only document is skipped.
//
// A document of a registered kind that sets a field its Go type does not
// declare is refused when it is a workload or a claim, the error naming the
// object and each field's path. Of any other kind it comes back unstructured,
// as written, the field kept — unless the field is a top-level `items` array:
// written out, such an object is a list to whatever applies it, which would
// apply the items in its place, so it is refused. So is a document of any
// registered kind whose repeated keys fill the strict decode's record of
// errors (strictErrorLimit of them) before it names an undeclared field: the
// decode cannot say whether the document sets one.
//
// Every document that does not decode is an error, and the parser reports them
// together, as it always did: that error is kure's own, unchanged, and it is
// returned before any undeclared field is looked at.
func decodeManifestDocuments(raw []byte) ([]client.Object, error) {
	opts := kureio.ParseOptions{AllowUnstructured: true}
	docs, err := splitManifestDocuments(raw)
	decoded := make([][]client.Object, len(docs))
	for i := 0; err == nil && i < len(docs); i++ {
		decoded[i], err = kureio.ParseYAMLWithOptions(docs[i], opts)
	}
	if err != nil {
		// The parser reports every bad document of the input in one error; a
		// document alone would give only its own.
		if _, whole := kureio.ParseYAMLWithOptions(raw, opts); whole != nil {
			return nil, whole
		}
		return nil, err
	}

	var out []client.Object
	for i, doc := range docs {
		objs, err := keepUndeclaredFields(doc, decoded[i])
		if err != nil {
			return nil, err
		}
		out = append(out, objs...)
	}
	return out, nil
}

// splitManifestDocuments splits raw into its documents as kure's parser does,
// each in the JSON form that parser decodes, empty documents dropped.
func splitManifestDocuments(raw []byte) ([][]byte, error) {
	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(raw), 4096)
	var docs [][]byte
	for {
		var doc runtime.RawExtension
		if err := decoder.Decode(&doc); err != nil {
			if stderrors.Is(err, io.EOF) {
				return docs, nil
			}
			return nil, err
		}
		if len(bytes.TrimSpace(doc.Raw)) > 0 {
			docs = append(docs, doc.Raw)
		}
	}
}

// keepUndeclaredFields returns objs, what kure's parser made of doc, or what
// stands in for it when doc sets fields its Go type does not declare: an error
// for a workload or a claim, the document as an unstructured object for any
// other kind.
func keepUndeclaredFields(doc []byte, objs []client.Object) ([]client.Object, error) {
	paths, err := undeclaredFields(doc)
	if err != nil {
		if len(objs) == 1 {
			return nil, errors.Wrap(err, renderedObjectRef(objs[0]))
		}
		return nil, errors.Wrap(err, "reading a document for fields its kind does not declare")
	}
	if len(paths) == 0 {
		return objs, nil
	}
	// Only a registered kind has undeclared fields, and the parser makes one
	// object of such a document.
	if len(objs) != 1 {
		return nil, errors.Errorf("a document with undeclared fields decoded to %d objects, want 1", len(objs))
	}
	obj := objs[0]
	gvk := obj.GetObjectKind().GroupVersionKind()
	if isWorkloadGVK(gvk) {
		return nil, errors.Wrap(undeclaredFieldsError(gvk, paths), renderedObjectRef(obj))
	}
	var object map[string]any
	if err := utiljson.Unmarshal(doc, &object); err != nil {
		return nil, errors.Wrapf(err, "%s: reading the document as written", renderedObjectRef(obj))
	}
	u := &unstructured.Unstructured{Object: object}
	if u.IsList() {
		return nil, errors.Errorf("%s: the %s %s type declares no `items` field, and written out with one the object is a list whose items would be applied in its place; remove the field", renderedObjectRef(obj), gvk.GroupVersion(), gvk.Kind)
	}
	return []client.Object{u}, nil
}

// refuseUndeclaredWorkloadFields refuses u when it is a workload or a claim of
// a registered kind that sets fields the Go type of that kind does not declare:
// the fields the policy check, which reads that type, cannot see. A workload or
// a claim whose record of strict errors is full is refused too, since such a
// field cannot be ruled out. An object of any other kind passes whatever the
// strict decode says of it, a full record included: passthrough emits the
// authored map, so nothing the object sets is dropped, and no check reads it
// for a field its type does not declare. So does one that does not decode as
// its kind, which the policy check refuses on its own account.
func refuseUndeclaredWorkloadFields(u *unstructured.Unstructured) error {
	gvk := u.GroupVersionKind()
	if !isWorkloadGVK(gvk) {
		return nil
	}
	paths, full := undeclaredFieldsOfObject(u)
	switch {
	case full:
		return errStrictRecordFull
	case len(paths) == 0:
		return nil
	}
	return undeclaredFieldsError(gvk, paths)
}

// undeclaredFieldsOfObject is undeclaredFields for an object held as a map. It
// returns none for an object that does not serialize or does not decode as its
// kind: that is not an undeclared field, and what is done about it is the
// policy check's to say, not this one's. full reports errStrictRecordFull: a
// map holds no key twice, but a value in it that serializes itself (a
// json.RawMessage a Go caller put there) can.
func undeclaredFieldsOfObject(u *unstructured.Unstructured) (paths []string, full bool) {
	raw, err := json.Marshal(u.Object)
	if err != nil {
		return nil, false
	}
	paths, err = undeclaredFields(raw)
	if err != nil {
		return nil, stderrors.Is(err, errStrictRecordFull)
	}
	return paths, false
}
