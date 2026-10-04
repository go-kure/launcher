package components

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"io"
	"reflect"
	"strings"
	"sync"

	kureio "github.com/go-kure/kure/pkg/io"
	"github.com/go-kure/kure/pkg/kubernetes"
	"k8s.io/apimachinery/pkg/api/meta"
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
// The parser replaces a list of a registered kind by its items, so what is
// refused or kept is each item, not the list (keepListItemFields).
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
	return undeclaredFieldsAs(doc, nil)
}

// undeclaredFieldsAs is undeclaredFields for a document that may leave its
// apiVersion and kind out: an item of a typed list, which is of the kind the
// list holds. itemKind is that kind, and nil for a document that states its
// own.
func undeclaredFieldsAs(doc []byte, itemKind *schema.GroupVersionKind) ([]string, error) {
	decoder, err := strictDecoder()
	if err != nil {
		return nil, err
	}
	_, _, err = decoder.Decode(doc, itemKind, nil)
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
// A list of a registered kind (a `v1` List, a typed list such as
// DeploymentList) is replaced by its items, and the rule above is each item's:
// an item is refused or kept as a document of its own would be, the error
// naming its position in the list (keepListItemFields). The list's own fields
// are not read: the list is never emitted.
//
// Every document that does not decode is an error, and the parser reports them
// together, as it always did: that error is kure's own, unchanged, and it is
// returned before any undeclared field is looked at.
func decodeManifestDocuments(raw []byte) ([]client.Object, error) {
	docs, err := splitManifestDocuments(raw)
	decoded := make([][]client.Object, len(docs))
	for i := 0; err == nil && i < len(docs); i++ {
		decoded[i], err = kureio.ParseYAMLWithOptions(docs[i], manifestParseOptions)
	}
	if err != nil {
		// The parser reports every bad document of the input in one error; a
		// document alone would give only its own.
		if _, whole := kureio.ParseYAMLWithOptions(raw, manifestParseOptions); whole != nil {
			return nil, whole
		}
		return nil, err
	}

	var out []client.Object
	for i, doc := range docs {
		objs, err := keepDocumentFields(doc, decoded[i])
		if err != nil {
			return nil, err
		}
		out = append(out, objs...)
	}
	return out, nil
}

// manifestParseOptions is how decodeManifestDocuments runs kure's parser: an
// object of a kind the scheme does not register is unstructured, not an error.
var manifestParseOptions = kureio.ParseOptions{AllowUnstructured: true}

// keepDocumentFields returns objs, what kure's parser made of doc, with the
// undeclared fields of doc settled: by keepUndeclaredFields for a document of
// a single kind or a list of a kind the scheme does not register, by
// keepListItemFields for a list of a registered kind.
func keepDocumentFields(doc []byte, objs []client.Object) ([]client.Object, error) {
	list, isList := registeredListDocument(doc)
	if isList {
		return keepListItemFields(list, objs)
	}
	// What is no registered list is one object, or the untyped items of a list
	// of an unregistered kind. Several objects with a typed one among them are
	// the items of a list registeredListDocument did not recognise.
	if len(objs) > 1 {
		for _, obj := range objs {
			if _, untyped := obj.(*unstructured.Unstructured); !untyped {
				return nil, errors.Errorf("%s: a document that is no list of a registered kind decoded to %d objects, so the fields of each cannot be checked; this is a defect of this build, not of the document", renderedObjectRef(obj), len(objs))
			}
		}
	}
	return keepUndeclaredFields(doc, objs, nil)
}

// listDocument is a list document of a registered kind, as kure's parser reads
// it: its kind, the items it states, undecoded, and whether it is the generic
// `v1` List, whose items are documents of any kind.
type listDocument struct {
	kind    string
	items   []json.RawMessage
	generic bool
}

// registeredListDocument reports whether doc is a list document of a kind
// kure's scheme registers, which kure's parser replaces by its items, and
// returns how that parser reads it.
//
// It is the parser's own detection (kure pkg/io, registeredList and
// listItemKind), which that package does not export, step for step: apiVersion,
// kind and items read under exactly those keys, the last statement of each, and
// the generic list told by the type of its items, not by its name.
// TestRegisteredListDocument_IsTheParsersDetection holds it to the parser. A
// document this does not hold for a list while the parser does would have its
// items pass unread; keepDocumentFields refuses the one case of that it can
// see, a document that decodes to several objects with a typed one among them.
func registeredListDocument(doc []byte) (listDocument, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(doc, &fields); err != nil {
		return listDocument{}, false
	}
	apiVersion, okVersion := statedString(fields, "apiVersion")
	kind, okKind := statedString(fields, "kind")
	if !okVersion || !okKind || kind == "" {
		return listDocument{}, false
	}
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return listDocument{}, false
	}
	list, err := kubernetes.Scheme.New(gv.WithKind(kind))
	if err != nil {
		return listDocument{}, false
	}
	if _, isObject := list.(client.Object); isObject || !meta.IsListType(list) {
		return listDocument{}, false
	}
	out := listDocument{kind: kind}
	if itemsPtr, err := meta.GetItemsPtr(list); err == nil {
		out.generic = reflect.TypeOf(itemsPtr).Elem().Elem() == reflect.TypeOf(runtime.RawExtension{})
	}
	if stated := fields["items"]; len(stated) > 0 {
		// Items that are not an array are the parser's error, reported before
		// this is reached.
		_ = json.Unmarshal(stated, &out.items)
	}
	return out, true
}

// statedString returns the string fields states under key, "" when the key is
// absent or null, and false when its value is not a string.
func statedString(fields map[string]json.RawMessage, key string) (string, bool) {
	value, stated := fields[key]
	if !stated {
		return "", true
	}
	var s string
	if err := json.Unmarshal(value, &s); err != nil {
		return "", false
	}
	return s, true
}

// keepListItemFields is keepUndeclaredFields for each item of list, a list
// document of a registered kind; objs is what kure's parser made of that
// document, its items in the list's order.
//
// An item of a typed list is one object, of the kind the list holds, which the
// item may leave out: it is read as that kind. An item of the generic List is a
// document of its own, so the parser is run on it again and the result settled
// as a document's is, a list among the items in turn. The parser bounds how
// deep registered lists nest, and it has accepted the whole document, so the
// descent ends.
//
// The items must account for exactly the objects the parser returned. When they
// do not, this reading of the list is not the parser's, some object would go
// unread, and the document is refused.
func keepListItemFields(list listDocument, objs []client.Object) ([]client.Object, error) {
	mismatch := func(read int) error {
		return errors.Errorf("the items of %s were read as %d objects and decoded to %d, so the fields of each cannot be checked; this is a defect of this build, not of the document", list.kind, read, len(objs))
	}
	out := make([]client.Object, 0, len(objs))
	if !list.generic {
		if len(list.items) != len(objs) {
			return nil, mismatch(len(list.items))
		}
		for i, item := range list.items {
			itemKind := objs[i].GetObjectKind().GroupVersionKind()
			kept, err := keepUndeclaredFields(item, objs[i:i+1], &itemKind)
			if err != nil {
				return nil, errors.Wrapf(err, "item %d of %s", i, list.kind)
			}
			out = append(out, kept...)
		}
		return out, nil
	}
	read := 0
	for i, item := range list.items {
		itemObjs, err := kureio.ParseYAMLWithOptions(item, manifestParseOptions)
		if err != nil {
			return nil, errors.Wrapf(err, "item %d of %s", i, list.kind)
		}
		read += len(itemObjs)
		kept, err := keepDocumentFields(item, itemObjs)
		if err != nil {
			return nil, errors.Wrapf(err, "item %d of %s", i, list.kind)
		}
		out = append(out, kept...)
	}
	if read != len(objs) {
		return nil, mismatch(read)
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
// other kind. itemKind is the kind of an item of a typed list, which doc may
// leave out, and nil for a document that states its own: the unstructured
// object that stands in for such an item is given it.
func keepUndeclaredFields(doc []byte, objs []client.Object, itemKind *schema.GroupVersionKind) ([]client.Object, error) {
	paths, err := undeclaredFieldsAs(doc, itemKind)
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
	if itemKind != nil {
		u.SetGroupVersionKind(*itemKind)
	}
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
