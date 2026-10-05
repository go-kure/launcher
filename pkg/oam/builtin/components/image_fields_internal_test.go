package components

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// imageFieldPolicy lists the registries images may come from and is the no-op
// policy otherwise.
type imageFieldPolicy struct {
	oam.NoopPolicy
	allowed []string
}

func (p *imageFieldPolicy) AllowedRegistries() []string { return p.allowed }

// imageFieldType is a type whose document can name an image, and how each of
// its fields that could is accounted for.
type imageFieldType struct {
	name string
	typ  reflect.Type
	// held is, by json path, each field whose image is held to the allowed
	// registries: the check the build runs on a value of the type whose field
	// at that path names reference. For a field that is an image volume source
	// the path is the source's and the image is its reference.
	held map[string]func(reference string, p oam.Policy) error
	// notHeld is, by json path, each field with "image" in its name that is not
	// held, and why.
	notHeld map[string]string
}

// imageFieldTypes are the types the registry rule is derived over.
var imageFieldTypes = []imageFieldType{
	{
		// The one check behind every raw pod spec: the pod kind, the pod template
		// of the replicaset, replicationcontroller and podtemplate kinds and of
		// cnpg-pooler, and the pod spec of an object template delivery,
		// passthrough or a manifests source carries.
		name: "pod spec",
		typ:  reflect.TypeFor[corev1.PodSpec](),
		held: map[string]func(string, oam.Policy) error{
			"containers[].image": func(reference string, p oam.Policy) error {
				return enforcePodTemplatePolicy("", &corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: reference}}}, p)
			},
			"initContainers[].image": func(reference string, p oam.Policy) error {
				return enforcePodTemplatePolicy("", &corev1.PodSpec{InitContainers: []corev1.Container{{Name: "init", Image: reference}}}, p)
			},
			"volumes[].image": func(reference string, p oam.Policy) error {
				return enforcePodTemplatePolicy("", &corev1.PodSpec{Volumes: []corev1.Volume{{
					Name:         "ext",
					VolumeSource: corev1.VolumeSource{Image: &corev1.ImageVolumeSource{Reference: reference}},
				}}}, p)
			},
		},
		notHeld: map[string]string{
			"ephemeralContainers[].image":           "every caller of the shared check refuses a pod spec that lists ephemeral containers (podSpecRejectedKeys)",
			"containers[].imagePullPolicy":          "says when the image is pulled, not which image",
			"initContainers[].imagePullPolicy":      "says when the image is pulled, not which image",
			"ephemeralContainers[].imagePullPolicy": "says when the image is pulled, not which image",
			"imagePullSecrets":                      "names the Secrets holding registry credentials, not an image",
			"volumes[].rbd.image":                   "the name of a Ceph RBD block image in a pool, not an OCI image reference",
		},
	},
	{
		// The cnpg-cluster kind. Instances is set because the kind's policy step
		// refuses a Cluster without one before it reads an image.
		name: "cnpg-cluster spec",
		typ:  reflect.TypeFor[cnpgv1.ClusterSpec](),
		held: map[string]func(string, oam.Policy) error{
			"imageName": func(reference string, p oam.Policy) error {
				return (&CnpgClusterConfig{Name: "db", Spec: cnpgv1.ClusterSpec{Instances: 1, ImageName: reference}}).ApplyPolicy(p)
			},
			"postgresql.extensions[].image": func(reference string, p oam.Policy) error {
				spec := cnpgv1.ClusterSpec{Instances: 1}
				spec.PostgresConfiguration.Extensions = []cnpgv1.ExtensionConfiguration{{
					Name:              "ext",
					ImageVolumeSource: corev1.ImageVolumeSource{Reference: reference},
				}}
				return (&CnpgClusterConfig{Name: "db", Spec: spec}).ApplyPolicy(p)
			},
		},
		notHeld: map[string]string{
			"imageCatalogRef":  "names an image catalog object and a major version, not an image; the catalog's images are the catalog's to hold",
			"imagePullPolicy":  "says when the image is pulled, not which image",
			"imagePullSecrets": "names the Secrets holding registry credentials, not an image",
		},
	},
	{
		// The cnpg-pooler kind: its own image, and the pod template through the
		// shared check under template.spec.
		name: "cnpg-pooler spec",
		typ:  reflect.TypeFor[cnpgv1.PoolerSpec](),
		held: map[string]func(string, oam.Policy) error{
			"pgbouncer.image": func(reference string, p oam.Policy) error {
				return (&CnpgPoolerConfig{Name: "pool", Spec: cnpgv1.PoolerSpec{PgBouncer: &cnpgv1.PgBouncerSpec{Image: reference}}}).ApplyPolicy(p)
			},
			"template.spec.containers[].image": func(reference string, p oam.Policy) error {
				return poolerTemplatePolicy(corev1.PodSpec{Containers: []corev1.Container{{Name: "pgbouncer", Image: reference}}}, p)
			},
			"template.spec.initContainers[].image": func(reference string, p oam.Policy) error {
				return poolerTemplatePolicy(corev1.PodSpec{InitContainers: []corev1.Container{{Name: "init", Image: reference}}}, p)
			},
			"template.spec.volumes[].image": func(reference string, p oam.Policy) error {
				return poolerTemplatePolicy(corev1.PodSpec{Volumes: []corev1.Volume{{
					Name:         "ext",
					VolumeSource: corev1.VolumeSource{Image: &corev1.ImageVolumeSource{Reference: reference}},
				}}}, p)
			},
		},
		notHeld: map[string]string{
			"pgbouncer.imageCatalogRef":                           "names an image catalog object and a key in it, not an image; the catalog's images are the catalog's to hold",
			"template.spec.ephemeralContainers[].image":           "the kind refuses a template that lists ephemeral containers, at parse and at generation",
			"template.spec.containers[].imagePullPolicy":          "says when the image is pulled, not which image",
			"template.spec.initContainers[].imagePullPolicy":      "says when the image is pulled, not which image",
			"template.spec.ephemeralContainers[].imagePullPolicy": "says when the image is pulled, not which image",
			"template.spec.imagePullSecrets":                      "names the Secrets holding registry credentials, not an image",
			"template.spec.volumes[].rbd.image":                   "the name of a Ceph RBD block image in a pool, not an OCI image reference",
		},
	},
}

// poolerTemplatePolicy is the cnpg-pooler kind's policy step on a Pooler whose
// pod template has the given spec.
func poolerTemplatePolicy(ps corev1.PodSpec, p oam.Policy) error {
	return (&CnpgPoolerConfig{Name: "pool", Spec: cnpgv1.PoolerSpec{
		PgBouncer: &cnpgv1.PgBouncerSpec{},
		Template:  &cnpgv1.PodTemplateSpec{Spec: ps},
	}}).ApplyPolicy(p)
}

// isImageField says whether f could name an image: its json name holds
// "image", or it is an image volume source under any name, behind any pointer,
// slice, array or map. That is the whole of what is recognised: a field that
// names an image under another name and another type is not.
func isImageField(f kindField) bool {
	name, _, _ := strings.Cut(f.field.Tag.Get("json"), ",")
	if name == "" {
		name = f.field.Name
	}
	typ := f.field.Type
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array || typ.Kind() == reflect.Map {
		typ = typ.Elem()
	}
	return strings.Contains(strings.ToLower(name), "image") || typ == reflect.TypeFor[corev1.ImageVolumeSource]()
}

// TestImageFields_WhatIsRecognised pins isImageField: a field is a candidate by
// its json name or by the image volume source type, whatever wraps the type,
// and by nothing else.
func TestImageFields_WhatIsRecognised(t *testing.T) {
	type sample struct {
		ByName      string                              `json:"sidecarImage"`
		Leading     string                              `json:"imageName"`
		Inside      string                              `json:"sidecarImageReference"`
		PlainImage  string                              // untagged: the Go name is the json name
		Image       int                                 `json:"renamed"`
		Direct      corev1.ImageVolumeSource            `json:"a"`
		Pointer     *corev1.ImageVolumeSource           `json:"b"`
		Slice       []corev1.ImageVolumeSource          `json:"c"`
		Array       [1]corev1.ImageVolumeSource         `json:"d"`
		Map         map[string]corev1.ImageVolumeSource `json:"e"`
		Nested      []*[2]corev1.ImageVolumeSource      `json:"f"`
		OtherSource corev1.SecretVolumeSource           `json:"g"`
		Reference   string                              `json:"artifactReference"`
	}
	want := map[string]bool{
		"ByName": true, "Leading": true, "Inside": true, "PlainImage": true, "Image": false,
		"Direct": true, "Pointer": true, "Slice": true, "Array": true, "Map": true, "Nested": true,
		"OtherSource": false, "Reference": false,
	}
	typ := reflect.TypeFor[sample]()
	if typ.NumField() != len(want) {
		t.Fatalf("the sample has %d fields and %d are expected", typ.NumField(), len(want))
	}
	for i := range typ.NumField() {
		field := typ.Field(i)
		if got := isImageField(kindField{field: field}); got != want[field.Name] {
			t.Errorf("%s: recognised = %v, want %v", field.Name, got, want[field.Name])
		}
	}
}

// TestImageFields_HeldOrListed derives, from each type, every field that could
// name an image (isImageField), and fails on one that is neither held to the
// allowed registries nor listed with the reason it is not. A dependency bump
// that adds a field named for an image, or of the image volume source type,
// fails here, naming it, until the field is held or listed. A
// held field is proven held: the check refuses a reference outside the list
// with the registry class, and passes one inside it.
func TestImageFields_HeldOrListed(t *testing.T) {
	const listed, other = "registry.example/team/thing:1.0.0", "other.example/team/thing:1.0.0"
	policy := &imageFieldPolicy{allowed: []string{"registry.example"}}
	for _, typ := range imageFieldTypes {
		t.Run(typ.name, func(t *testing.T) {
			var candidates []string
			for path, f := range ciliumBGPTypeFields(typ.typ) {
				if isImageField(f) {
					candidates = append(candidates, path)
				}
			}
			slices.Sort(candidates)
			if len(candidates) == 0 {
				t.Fatal("the type has no field that could name an image; the walk is broken")
			}
			for _, path := range candidates {
				check, held := typ.held[path]
				reason, isListed := typ.notHeld[path]
				switch {
				case held && isListed:
					t.Errorf("%s is both held and listed as not held", path)
				case held:
					err := check(other, policy)
					var refusal *oam.PolicyRefusal
					if !errors.As(err, &refusal) || refusal.Class != oam.RefusalRegistry || !strings.Contains(err.Error(), other) {
						t.Errorf("%s: a reference outside the allowed registries gave %v, want a refusal of class %q naming it", path, err, oam.RefusalRegistry)
					}
					if err := check(listed, policy); err != nil {
						t.Errorf("%s: a reference inside the allowed registries is refused: %v", path, err)
					}
				case isListed:
					if reason == "" {
						t.Errorf("%s is listed as not held without a reason", path)
					}
				default:
					t.Errorf("%s could name an image and is neither held to the allowed registries nor listed with the reason it is not", path)
				}
			}
			for path := range typ.held {
				if !slices.Contains(candidates, path) {
					t.Errorf("%s is held and is not a field of the type that could name an image", path)
				}
			}
			for path := range typ.notHeld {
				if !slices.Contains(candidates, path) {
					t.Errorf("%s is listed as not held and is not a field of the type that could name an image", path)
				}
			}
		})
	}
}

// TestImageFields_WorkloadKindsTakeNoImageVolume: the components with a volume
// schema of their own (webservice, worker, deployment, statefulset, daemonset,
// job, cronjob) take their volumes through one parser, which has no image
// volume: the only image volume a build can produce comes through a raw pod
// spec.
func TestImageFields_WorkloadKindsTakeNoImageVolume(t *testing.T) {
	_, err := parseVolumes(map[string]any{"volumes": []any{map[string]any{
		"name": "ext", "type": "image", "mountPath": "/ext", "reference": "registry.example/team/ext:1.0.0",
	}}})
	if want := `volume "ext": unrecognized type "image"`; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}
