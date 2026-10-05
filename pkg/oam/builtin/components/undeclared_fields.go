package components

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"io"
	"reflect"
	"slices"
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
// The parser replaces a list by its items, so what is refused or kept is each
// item, not the list (keepListItemFields). A list is what the parser reads as
// one (listDocumentOf): a list kind the scheme registers, and a kind it does
// not register that ends in List and states `items`.
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
	_, paths, err := undeclaredFieldsAs(doc, nil)
	return paths, err
}

// undeclaredFieldsAs is undeclaredFields for a document that may leave its
// apiVersion and kind out: an item of a list, which is read with what its list
// gives it. itemKind is the group, version and kind the item is read with where
// it states none, and nil for a document that states its own.
//
// readAs is the group, version and kind the strict decode read doc as, which
// is the kind whose fields it checked. keepUndeclaredFields holds it to what
// the parser made of doc.
func undeclaredFieldsAs(doc []byte, itemKind *schema.GroupVersionKind) (readAs schema.GroupVersionKind, paths []string, err error) {
	decoder, err := strictDecoder()
	if err != nil {
		return readAs, nil, err
	}
	read, err := strictDecode(decoder, doc, itemKind)
	if read != nil {
		readAs = *read
	}
	if err == nil || runtime.IsNotRegisteredError(err) {
		return readAs, nil, nil
	}
	strict, ok := runtime.AsStrictDecodingError(err)
	if !ok {
		return readAs, nil, err
	}
	recorded := strict.Errors()
	for _, e := range recorded {
		if path, ok := strings.CutPrefix(e.Error(), unknownFieldPrefix); ok {
			paths = append(paths, strings.TrimSuffix(path, `"`))
		}
	}
	if len(paths) == 0 && len(recorded) >= strictErrorLimit {
		return readAs, nil, errStrictRecordFull
	}
	return readAs, paths, nil
}

// errDecoderPanicked is the error a panic of the strict decode is turned into
// (strictDecode); the panic's value follows it.
//
// A document of a registered kind is decoded into the API type of that kind,
// and such a type may decode itself with code that does not handle what the
// document wrote. Cilium's ICMPField is one: it dereferences the `type` an
// `icmps` field left out. The document is not launcher's, so the crash would be
// a chart's or a manifest source's to cause, and it is reported instead.
//
// Kure's parser reports such a panic itself, as the parse error of that
// document (go-kure/kure#1009), and every reader here parses a document before
// it decodes it strictly. So this error is not the one a build shows for the
// known case: it is what keeps the strict decode, which launcher calls on the
// scheme's decoders itself, from being the place such a document crashes.
var errDecoderPanicked = errors.New("the decoder panicked on the document instead of refusing it (an API type that decodes itself did not handle what was written; a required field left out is the known cause)")

// strictDecode is decoder.Decode for its error and for the group, version and
// kind it read doc as (nil where it did not get that far), a panic of it
// returned as an error that is errDecoderPanicked. It does nothing but call the
// decoder: a defect in code of this package must stay a crash.
func strictDecode(decoder runtime.Decoder, doc []byte, itemKind *schema.GroupVersionKind) (read *schema.GroupVersionKind, err error) {
	defer func() {
		if r := recover(); r != nil {
			read, err = nil, errors.Errorf("%w: %v", errDecoderPanicked, r)
		}
	}()
	_, read, err = decoder.Decode(doc, itemKind, nil)
	return read, err
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
// (*appsv1.Deployment, *batchv1.Job); any other is *unstructured.Unstructured.
// An empty or comment-only document is skipped.
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
// A list (a `v1` List, a typed list such as DeploymentList, and a kind the
// scheme does not register that ends in List and states `items`) is replaced by
// its items, and the rule above is each item's: an item is refused or kept as a
// document of its own would be, the error naming its position in the list
// (keepListItemFields). An item of a list of an unregistered kind is a document
// of its own to the parser, so one of a registered kind is its Go type and is
// held to the rule as any other. The list's own fields are not read: the list
// is never emitted, and the parser refuses one whose metadata carries labels or
// annotations, which its items could not keep.
//
// An object of a kind the scheme does not register, and that does not end in
// List, is one object to the parser whatever it holds. With a top-level `items`
// array it is refused here (refuseItemsOnNoList): written out, it is a list to
// what applies the output.
//
// Every document that does not decode is an error, and the parser reports them
// together, as it always did: that error is kure's own, unchanged, and it is
// returned before any undeclared field is looked at.
//
// A document the decoder of its kind panics on is one of them: the parser
// reports the panic as that document's error, naming the object by kind,
// namespace and name (go-kure/kure#1009), beside the other documents' errors.
//
// Input that stops being YAML or JSON part-way is an error as well, reported
// with the errors of the documents ahead of that point. The parser ends at
// malformed JSON it cannot read past (go-kure/kure#1012); what follows is not
// read.
func decodeManifestDocuments(raw []byte) ([]client.Object, error) {
	docs, splitErr := splitManifestDocuments(raw)
	decoded := make([][]client.Object, len(docs))
	var err error
	for i := 0; err == nil && i < len(docs); i++ {
		decoded[i], err = kureio.ParseYAMLWithOptions(docs[i], manifestParseOptions)
	}
	if err == nil {
		err = splitErr
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
// undeclared fields of doc settled: by keepListItemFields for a list, by
// keepUndeclaredFields for any other document, which is one object.
func keepDocumentFields(doc []byte, objs []client.Object) ([]client.Object, error) {
	return keepFieldsAs(doc, objs, listIdentity{})
}

// keepFieldsAs is keepDocumentFields for a document that may leave its
// apiVersion or kind out: an item of a list of an unregistered kind, which is
// read with what its list gives it. given is zero for a document that is given
// nothing.
func keepFieldsAs(doc []byte, objs []client.Object, given listIdentity) ([]client.Object, error) {
	if list, isList := listDocumentOf(doc, given); isList {
		return keepListItemFields(list, objs)
	}
	// What is no list is one object. Any other count is the items of a list
	// listDocumentOf did not recognise, which would pass unread.
	if len(objs) != 1 {
		return nil, errors.Errorf("a document that is no list decoded to %d objects, so the fields of each cannot be checked; this is a defect of this build, not of the document", len(objs))
	}
	if given == (listIdentity{}) {
		return keepUndeclaredFields(doc, objs, nil)
	}
	// The object states what the parser read the item as, what its list gave
	// it included.
	itemKind := objs[0].GetObjectKind().GroupVersionKind()
	return keepUndeclaredFields(doc, objs, &itemKind)
}

// listIdentity is the apiVersion and the kind a document is read with where it
// leaves one out. Only a list of an unregistered kind gives its items one: its
// own apiVersion, and its kind without the List.
type listIdentity struct {
	apiVersion, kind string
}

// listDocument is a list document as kure's parser reads it: the apiVersion
// and the kind it is read with, the items it states, undecoded, and which of
// the three shapes of a list it is. A typed list (DeploymentList) is neither
// generic nor unregistered.
type listDocument struct {
	apiVersion string
	kind       string
	items      []json.RawMessage
	// generic is the `v1` List, whose items are documents of any kind.
	generic bool
	// unregistered is a list of a kind the scheme does not register. Its items
	// are documents of their own too, read with itemIdentity.
	unregistered bool
}

// itemsAreDocuments reports whether an item of the list is a document of its
// own to the parser, a list among the items opened in turn. An item of a typed
// list is one object of the kind the list holds.
func (l listDocument) itemsAreDocuments() bool { return l.generic || l.unregistered }

// itemIdentity is what an item of the list is read with where it leaves its
// apiVersion or kind out.
func (l listDocument) itemIdentity() listIdentity {
	if !l.unregistered {
		return listIdentity{}
	}
	return listIdentity{apiVersion: l.apiVersion, kind: strings.TrimSuffix(l.kind, "List")}
}

// parseItem runs kure's parser on one item of the list, as a document of its
// own.
//
// An item of a list of an unregistered kind is parsed inside a list of that
// kind which holds it alone. The parser gives such an item what it leaves out
// of apiVersion and kind, by a helper its package does not export
// (withListIdentity); a list of one item makes the parser do that itself, so
// the objects are the ones it made of the item in the document, and nothing
// here writes the item again.
func (l listDocument) parseItem(item json.RawMessage) ([]client.Object, error) {
	if !l.unregistered {
		return kureio.ParseYAMLWithOptions(item, manifestParseOptions)
	}
	head, err := json.Marshal(map[string]string{"apiVersion": l.apiVersion, "kind": l.kind})
	if err != nil {
		return nil, errors.Wrapf(err, "writing the head of %s", l.kind)
	}
	// head is a JSON object of two string members, so its last byte closes it.
	alone := slices.Concat(head[:len(head)-1], []byte(`,"items":[`), item, []byte(`]}`))
	return kureio.ParseYAMLWithOptions(alone, manifestParseOptions)
}

// listDocumentOf reports whether doc is a list document to kure's parser, which
// replaces one by its items, and returns how that parser reads it. given is
// what doc is read with where it leaves apiVersion or kind out, and zero for a
// document that is given nothing.
//
// It is the parser's own detection (kure pkg/io: registeredList, listItemKind
// and decodeUnregistered), which that package does not export, step for step.
// apiVersion, kind and items are read under exactly those keys, the last
// statement of each: the parser refuses a document that states one of them
// under another case, so there is one reading. A kind the scheme registers is
// a list when its type is one, and generic by the type of its items, not by
// its name. A kind the scheme does not register is a list when it ends in List
// and states `items`; without the suffix it is one object, whatever it holds.
// TestListDocumentOf_IsTheParsersDetection holds it to the parser. A document
// this does not hold for a list while the parser does would have its items
// pass unread; keepFieldsAs refuses what it can see of that, a document that
// is no list and decodes to more or less than one object.
func listDocumentOf(doc []byte, given listIdentity) (listDocument, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(doc, &fields); err != nil {
		return listDocument{}, false
	}
	apiVersion, okVersion := statedString(fields, "apiVersion")
	kind, okKind := statedString(fields, "kind")
	if !okVersion || !okKind {
		return listDocument{}, false
	}
	if apiVersion == "" {
		apiVersion = given.apiVersion
	}
	if kind == "" {
		kind = given.kind
	}
	if kind == "" {
		return listDocument{}, false
	}
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return listDocument{}, false
	}
	out := listDocument{apiVersion: apiVersion, kind: kind}
	stated, hasItems := fields["items"]
	if len(stated) > 0 {
		// Items that are not an array are the parser's error, reported before
		// the undeclared fields are settled and left to it by the hook check.
		_ = json.Unmarshal(stated, &out.items)
	}
	list, err := kubernetes.Scheme.New(gv.WithKind(kind))
	if err != nil {
		if !strings.HasSuffix(kind, "List") || !hasItems {
			return listDocument{}, false
		}
		out.unregistered = true
		return out, true
	}
	if _, isObject := list.(client.Object); isObject || !meta.IsListType(list) {
		return listDocument{}, false
	}
	if itemsPtr, err := meta.GetItemsPtr(list); err == nil {
		out.generic = reflect.TypeOf(itemsPtr).Elem().Elem() == reflect.TypeOf(runtime.RawExtension{})
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

// keepListItemFields is keepUndeclaredFields for each item of list; objs is
// what kure's parser made of that list document, its items in the list's order.
//
// An item of a typed list is one object, of the kind the list holds, which the
// item may leave out: it is read as that kind. An item of the generic List or
// of a list of an unregistered kind is a document of its own, so the parser is
// run on it again (parseItem) and the result settled as a document's is, a list
// among the items in turn. The parser bounds how deep lists nest, and it has
// accepted the whole document, so the descent ends.
//
// The items must account for exactly the objects the parser returned. When they
// do not, this reading of the list is not the parser's, some object would go
// unread, and the document is refused.
func keepListItemFields(list listDocument, objs []client.Object) ([]client.Object, error) {
	mismatch := func(read int) error {
		return errors.Errorf("the items of %s were read as %d objects and decoded to %d, so the fields of each cannot be checked; this is a defect of this build, not of the document", list.kind, read, len(objs))
	}
	out := make([]client.Object, 0, len(objs))
	if !list.itemsAreDocuments() {
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
		itemObjs, err := list.parseItem(item)
		if err != nil {
			return nil, errors.Wrapf(err, "item %d of %s", i, list.kind)
		}
		read += len(itemObjs)
		kept, err := keepFieldsAs(item, itemObjs, list.itemIdentity())
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
// each in the JSON form that parser decodes, empty documents dropped. Where raw
// stops being YAML, the split ends: the documents ahead of that point are
// returned beside the error.
func splitManifestDocuments(raw []byte) ([][]byte, error) {
	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(raw), 4096)
	var docs [][]byte
	for {
		var doc runtime.RawExtension
		if err := decoder.Decode(&doc); err != nil {
			if stderrors.Is(err, io.EOF) {
				return docs, nil
			}
			return docs, err
		}
		if len(bytes.TrimSpace(doc.Raw)) > 0 {
			docs = append(docs, doc.Raw)
		}
	}
}

// keepUndeclaredFields returns objs, what kure's parser made of doc, or what
// stands in for it when doc sets fields its Go type does not declare: an error
// for a workload or a claim, the document as an unstructured object for any
// other kind. itemKind is the kind of an item of a list, which doc may leave
// out, and nil for a document that states its own: the unstructured object that
// stands in for such an item is given it.
//
// An object of a kind the scheme does not register has no undeclared field. It
// is returned as the parser made it unless it carries a top-level `items`
// array (refuseItemsOnNoList).
//
// The strict decode must have read doc as the kind of the Go type the parser
// made of it: checked as another kind, the fields of the object that is emitted
// went unread, and the document is refused (refuseAnotherReading).
func keepUndeclaredFields(doc []byte, objs []client.Object, itemKind *schema.GroupVersionKind) ([]client.Object, error) {
	readAs, paths, err := undeclaredFieldsAs(doc, itemKind)
	if err != nil {
		if len(objs) == 1 {
			return nil, errors.Wrap(err, renderedObjectRef(objs[0]))
		}
		return nil, errors.Wrap(err, "reading a document for fields its kind does not declare")
	}
	for _, obj := range objs {
		if err := refuseAnotherReading(obj, readAs); err != nil {
			return nil, err
		}
	}
	if len(paths) == 0 {
		for _, obj := range objs {
			if err := refuseItemsOnNoList(obj); err != nil {
				return nil, err
			}
		}
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

// refuseAnotherReading refuses obj, a Go type the parser made of a document,
// when the strict decode read that document as another group, version or kind
// (readAs): the fields it checked are then not those of the object that is
// emitted, and an undeclared field of that object would be dropped unseen.
//
// The known case is an item of a list of an unregistered kind that states its
// apiVersion and then a null for it. The parser reads the last statement, takes
// the null for left out and gives the item its list's apiVersion; the Kubernetes
// decoder keeps the string the null follows. The parser refuses the same
// disagreement in an item of a typed list itself. An unstructured object passes:
// nothing of it is dropped, whatever the strict decode read.
func refuseAnotherReading(obj client.Object, readAs schema.GroupVersionKind) error {
	if _, untyped := obj.(*unstructured.Unstructured); untyped {
		return nil
	}
	gvk := obj.GetObjectKind().GroupVersionKind()
	if gvk == readAs {
		return nil
	}
	return errors.Errorf("%s: the object was read as %s %s, and the decode that checks its fields reads the document as %s %s, so its fields cannot be checked; a second apiVersion or kind that is null is the known cause, state each once",
		renderedObjectRef(obj), gvk.GroupVersion(), gvk.Kind, readAs.GroupVersion(), readAs.Kind)
}

// refuseItemsOnNoList refuses obj when the parser returned it untyped with a
// top-level `items` array: an object of a kind the scheme does not register
// and that does not end in List, which the parser reads as one object and
// never opens.
//
// Whatever applies the output tells a list by that array, not by the kind, and
// applies the entries in the object's place. They are entries no check here
// has read: not the policy check, not the undeclared-fields rule, not the hook
// check. So the object is refused whether a policy is given or not, as an
// object of a registered kind with an `items` field its type does not declare
// is (keepUndeclaredFields) and as passthrough refuses a list
// (rejectListEnvelope).
func refuseItemsOnNoList(obj client.Object) error {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok || !u.IsList() {
		return nil
	}
	return errors.Errorf("%s: an `items` array on an object of a kind that is no list (%s %s) is read as a list by what applies the output, which would apply its entries in the object's place; write the entries as documents of their own, or give the object a kind ending in List",
		renderedObjectRef(u), u.GetAPIVersion(), u.GetKind())
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
