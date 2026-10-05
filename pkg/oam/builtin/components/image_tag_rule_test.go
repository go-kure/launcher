package components_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// Every image a document names is held to ValidateImageRef, with or without an
// environment policy: no untagged image and no :latest. These tests hold the
// fields that are no container's image to it (go-kure/launcher#790): an image
// volume's reference on every path that produces a pod spec, the images of a
// cnpg-pooler, a cnpg-cluster and the two cnpg image catalogs, and the image a
// postgresql component composes. TestImageFields_HeldOrListed derives the same over the types; these
// run each field through its handler and pin the text of the refusal.

const trDigest = "@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

const (
	trNoTag  = "no tag or digest specified; use an explicit version tag or digest"
	trLatest = ":latest tag not allowed; use an explicit version tag or digest"
)

// trRefused are the references the rule refuses, each with the reason its
// refusal ends on. The last is one the reference parser refuses, whose reason
// is the parser's.
var trRefused = []struct{ name, reference, reason string }{
	{"no tag", "registry.example/team/ext", trNoTag},
	{"latest", "registry.example/team/ext:latest", trLatest},
	{"latest with a digest", "registry.example/team/ext:latest" + trDigest, trLatest},
	{"not a reference", "registry.example/Team/ext:1.0.0", ""},
}

// trAccepted are references the rule accepts.
var trAccepted = []struct{ name, reference string }{
	{"a tag", "registry.example/team/ext:1.0.0"},
	{"a digest without a tag", "registry.example/team/ext" + trDigest},
	{"a tag and a digest", "registry.example/team/ext:1.0.0" + trDigest},
}

// trRejected is the refusal of reference as ValidateImageRef words it.
func trRejected(reference, reason string) string {
	return `image "` + reference + `" rejected: ` + reason
}

// trWant fails the test unless err holds every fragment and is no refusal by
// the policy: the tag rule is the library's, not the environment's.
func trWant(t *testing.T, err error, fragments ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("built, want the image refused")
	}
	for _, f := range fragments {
		if !strings.Contains(err.Error(), f) {
			t.Errorf("err = %v, want one holding %q", err, f)
		}
	}
	var refusal *oam.PolicyRefusal
	if errors.As(err, &refusal) {
		t.Errorf("err = %v carries a policy refusal of class %q, want none", err, refusal.Class)
	}
}

// trPolicies are the two policies every pod path is run under: one that lists
// the registry of every reference here, and none passed.
var trPolicies = []struct {
	name   string
	policy func() *stubPolicy
}{
	{"under a policy", ptStrictPolicy},
	{"with no policy passed", func() *stubPolicy { return nil }},
}

// TestImageTagRule_PodImageVolume: a pod's image volume is held to the rule on
// each of the four paths that produce a Pod, under a policy and with none
// passed. The kind names the volume as the property it is, the three other
// paths under the object's spec.
func TestImageTagRule_PodImageVolume(t *testing.T) {
	for _, path := range podPolicyPaths {
		for _, pol := range trPolicies {
			for _, tc := range trRefused {
				t.Run(path.name+"/"+pol.name+"/refused: "+tc.name, func(t *testing.T) {
					_, err := path.build(t, htPlainPod+ivVolume(`"`+tc.reference+`"`), pol.policy())
					field := `volumes[0] "ext" image.reference: ` + trRejected(tc.reference, tc.reason)
					if path.rendered != "" {
						trWant(t, err, path.rendered, "spec."+field)
						return
					}
					trWant(t, err, field)
				})
			}
			for _, tc := range trAccepted {
				t.Run(path.name+"/"+pol.name+"/accepted: "+tc.name, func(t *testing.T) {
					if objs, err := path.build(t, htPlainPod+ivVolume(`"`+tc.reference+`"`), pol.policy()); err != nil || len(objs) != 1 {
						t.Errorf("objects %v, err %v; want the one Pod", objs, err)
					}
				})
			}
			t.Run(path.name+"/"+pol.name+"/accepted: no reference", func(t *testing.T) {
				if objs, err := path.build(t, htPlainPod+"volumes:\n  - name: ext\n    image: {}\n", pol.policy()); err != nil || len(objs) != 1 {
					t.Errorf("objects %v, err %v; want the one Pod", objs, err)
				}
			})
		}
	}
}

// TestImageTagRule_PodTemplateKindsImageVolume: the same on the pod template of
// each pod template kind, on each of its four paths. The kind names the volume
// by its path in the properties, the three other paths by its path in the
// object.
func TestImageTagRule_PodTemplateKindsImageVolume(t *testing.T) {
	for _, k := range podTemplatePolicyKinds {
		for _, path := range podTemplatePolicyPaths {
			for _, pol := range trPolicies {
				for _, tc := range trRefused {
					t.Run(k.typ+"/"+path.name+"/"+pol.name+"/refused: "+tc.name, func(t *testing.T) {
						_, err := path.build(t, k, htPlainPod+ivVolume(`"`+tc.reference+`"`), pol.policy())
						field := `.volumes[0] "ext" image.reference: ` + trRejected(tc.reference, tc.reason)
						if path.rendered != "" {
							trWant(t, err, path.rendered+k.kind, k.objectPath+field)
							return
						}
						trWant(t, err, k.propsPath+field)
					})
				}
				t.Run(k.typ+"/"+path.name+"/"+pol.name+"/accepted: a digest without a tag", func(t *testing.T) {
					spec := htPlainPod + ivVolume(`"registry.example/team/ext`+trDigest+`"`)
					if objs, err := path.build(t, k, spec, pol.policy()); err != nil || len(objs) != 1 {
						t.Errorf("objects %v, err %v; want the one %s", objs, err, k.kind)
					}
				})
			}
		}
	}
}

// TestImageTagRule_ImageVolumeOnEveryWorkloadKind: an object written elsewhere,
// of each kind whose pod spec the shared check reads, is refused when an image
// volume of its pod is untagged, with the volume named under the pod spec's
// path and no policy class; the same object builds with the reference pinned
// by digest alone.
func TestImageTagRule_ImageVolumeOnEveryWorkloadKind(t *testing.T) {
	for _, w := range ivWorkloads {
		t.Run(w.kind, func(t *testing.T) {
			_, err := mfTransform("manifests", mfInline(ptWorkload(w.kind, w.apiVersion, w.path, w.podSpec+ivVolume("registry.example/team/ext"))), rcStrict())
			rcWantClass(t, err, oam.RefusalUnclassified)
			trWant(t, err, w.path+`.volumes[0] "ext" image.reference: `+trRejected("registry.example/team/ext", trNoTag))

			objs, err := mfTransform("manifests", mfInline(ptWorkload(w.kind, w.apiVersion, w.path, w.podSpec+ivVolume(`"registry.example/team/ext`+trDigest+`"`))), rcStrict())
			if err != nil || len(objs) != 1 {
				t.Errorf("an image volume by digest alone: objects %v, err %v; want the one %s", objs, err, w.kind)
			}
		})
	}
}

// TestImageTagRule_ImageVolumeOnEveryObjectPath: the refusal is the component's
// violation, with no policy class, on each of the five ways a build meets an
// object written elsewhere.
func TestImageTagRule_ImageVolumeOnEveryObjectPath(t *testing.T) {
	doc := ptDeployment(htPlainPod + ivVolume("registry.example/team/ext:latest"))
	for _, path := range rcObjectPaths {
		t.Run(path.name, func(t *testing.T) {
			err := path.build(t, doc, rcStrict)
			rcWantClass(t, err, oam.RefusalUnclassified)
			trWant(t, err, `spec.template.spec.volumes[0] "ext" image.reference: `+trRejected("registry.example/team/ext:latest", trLatest))
		})
	}
}

// TestImageTagRule_CnpgPooler: a cnpg-pooler's pgbouncer.image and, in its
// template, each container's image and each image volume's reference are held
// to the rule when the component is read, with no policy applied. A template
// container that names no image is the ordinary form here — the operator
// supplies the PgBouncer image — and is not checked.
func TestImageTagRule_CnpgPooler(t *testing.T) {
	tmpl := func(spec map[string]any) map[string]any {
		p := minimalPooler()
		p["template"] = map[string]any{"spec": spec}
		return p
	}
	fields := []struct {
		name, at string
		props    func(reference string) map[string]any
	}{
		{"pgbouncer.image", "pgbouncer.image: ", func(reference string) map[string]any {
			return map[string]any{"cluster": map[string]any{"name": "db"}, "pgbouncer": map[string]any{"image": reference}}
		}},
		{"template container", `template.spec.containers[0] "pgbouncer": `, func(reference string) map[string]any {
			return tmpl(map[string]any{"containers": []any{map[string]any{"name": "pgbouncer", "image": reference}}})
		}},
		{"template init container", `template.spec.initContainers[0] "init": `, func(reference string) map[string]any {
			return tmpl(map[string]any{"containers": []any{}, "initContainers": []any{map[string]any{"name": "init", "image": reference}}})
		}},
		{"template image volume", `template.spec.volumes[0] "ext" image.reference: `, func(reference string) map[string]any {
			return tmpl(map[string]any{"containers": []any{}, "volumes": []any{map[string]any{"name": "ext", "image": map[string]any{"reference": reference}}}})
		}},
	}
	for _, f := range fields {
		for _, tc := range trRefused {
			t.Run(f.name+"/refused: "+tc.name, func(t *testing.T) {
				trWant(t, cnpgPoolerErr(t, f.props(tc.reference)), f.at+trRejected(tc.reference, tc.reason))
			})
		}
		for _, tc := range trAccepted {
			t.Run(f.name+"/accepted: "+tc.name, func(t *testing.T) {
				if err := cnpgPoolerErr(t, f.props(tc.reference)); err != nil {
					t.Errorf("refused: %v", err)
				}
			})
		}
	}

	t.Run("a field that names no image is not checked", func(t *testing.T) {
		for name, props := range map[string]map[string]any{
			"no pgbouncer.image":                  minimalPooler(),
			"a template container with no image":  tmpl(map[string]any{"containers": []any{map[string]any{"name": "pgbouncer"}}}),
			"a template volume with no reference": tmpl(map[string]any{"containers": []any{}, "volumes": []any{map[string]any{"name": "ext", "image": map[string]any{}}}}),
		} {
			if err := cnpgPoolerErr(t, props); err != nil {
				t.Errorf("%s: refused: %v", name, err)
			}
		}
	})

	t.Run("generation repeats the rule", func(t *testing.T) {
		c := newCnpgPooler(t, minimalPooler())
		c.Spec.PgBouncer.Image = "registry.example/team/pgbouncer"
		_, err := c.Generate(stack.NewApplication("db-pooler", "data", c))
		trWant(t, err, "pgbouncer.image: "+trRejected("registry.example/team/pgbouncer", trNoTag))
	})
}

// TestImageTagRule_CnpgCluster: a cnpg-cluster's imageName and the reference
// of each extension's image volume are held to the rule when the component is
// read, with no policy applied. A digest without a tag passes on imageName as
// it does everywhere: CloudNativePG's webhook refuses one there, and that rule
// is the operator's.
func TestImageTagRule_CnpgCluster(t *testing.T) {
	fields := []struct {
		name, at string
		props    func(reference string) map[string]any
	}{
		{"imageName", "imageName: ", func(reference string) map[string]any { return map[string]any{"imageName": reference} }},
		{"extension", "postgresql.extensions[0].image.reference: ", func(reference string) map[string]any { return cnpgExtensions(reference) }},
		{"the second extension", "postgresql.extensions[1].image.reference: ", func(reference string) map[string]any {
			return cnpgExtensions("registry.example/team/first:1.0.0", reference)
		}},
	}
	for _, f := range fields {
		for _, tc := range trRefused {
			t.Run(f.name+"/refused: "+tc.name, func(t *testing.T) {
				trWant(t, cnpgClusterErr(t, f.props(tc.reference)), f.at+trRejected(tc.reference, tc.reason))
			})
		}
		for _, tc := range trAccepted {
			t.Run(f.name+"/accepted: "+tc.name, func(t *testing.T) {
				if err := cnpgClusterErr(t, f.props(tc.reference)); err != nil {
					t.Errorf("refused: %v", err)
				}
			})
		}
	}

	t.Run("a field that names no image is not checked", func(t *testing.T) {
		// The operator then takes the image from a catalog or runs its own
		// default: neither is an image the document names.
		for name, props := range map[string]map[string]any{
			"no imageName":                   {},
			"an extension with no reference": cnpgExtensions(""),
		} {
			if err := cnpgClusterErr(t, props); err != nil {
				t.Errorf("%s: refused: %v", name, err)
			}
		}
	})

	t.Run("generation repeats the rule", func(t *testing.T) {
		c := newCnpgCluster(t, map[string]any{})
		c.Spec.ImageName = "registry.example/team/postgresql:latest"
		_, err := c.Generate(stack.NewApplication("db", "data", c))
		trWant(t, err, "imageName: "+trRejected("registry.example/team/postgresql:latest", trLatest))
	})
}

// TestImageTagRule_CnpgImageCatalogs: the three fields of a catalog that name
// an image are held to the rule on both catalog kinds, under a policy that
// lists the registry and with none passed: a Cluster that takes its image from
// the catalog runs what the entry names. An extension that names no reference
// names no image and is not checked; an image and a component image are
// required, and an empty one is no reference.
func TestImageTagRule_CnpgImageCatalogs(t *testing.T) {
	extension := func(reference string) map[string]any {
		return map[string]any{"name": "pgvector", "image": map[string]any{"reference": reference}}
	}
	componentImages := func(images ...string) map[string]any {
		list := make([]any, len(images))
		for i, image := range images {
			list[i] = map[string]any{"key": "component-" + strconv.Itoa(i), "image": image}
		}
		return cnpgWith(cnpgImages(cnpgImage(17)), "componentImages", list)
	}
	fields := []struct {
		name, at string
		props    func(reference string) map[string]any
	}{
		{"image", "images[0].image: ", func(reference string) map[string]any {
			return cnpgImages(cnpgWith(cnpgImage(17), "image", reference))
		}},
		{"the second image", "images[1].image: ", func(reference string) map[string]any {
			return cnpgImages(cnpgImage(17), cnpgWith(cnpgImage(16), "image", reference))
		}},
		{"extension", "images[0].extensions[0].image.reference: ", func(reference string) map[string]any {
			return cnpgImages(cnpgImage(17, extension(reference)))
		}},
		{"the second extension", "images[0].extensions[1].image.reference: ", func(reference string) map[string]any {
			return cnpgImages(cnpgImage(17, extension("registry.example/team/first:1.0.0"), extension(reference)))
		}},
		{"component image", "componentImages[0].image: ", func(reference string) map[string]any { return componentImages(reference) }},
		{"the second component image", "componentImages[1].image: ", func(reference string) map[string]any {
			return componentImages("registry.example/team/first:1.0.0", reference)
		}},
	}
	policies := map[string]oam.Policy{"under a policy": rcStrict(), "with no policy passed": nil}
	for _, component := range []string{"cnpg-imagecatalog", "cnpg-clusterimagecatalog"} {
		kind := cnpgFurtherKind(t, component)
		for name, policy := range policies {
			for _, f := range fields {
				for _, tc := range trRefused {
					t.Run(component+"/"+name+"/"+f.name+"/refused: "+tc.name, func(t *testing.T) {
						_, err := pvTransform(component, kind.handler, f.props(tc.reference), policy)
						trWant(t, err, f.at+trRejected(tc.reference, tc.reason))
					})
				}
				for _, tc := range trAccepted {
					t.Run(component+"/"+name+"/"+f.name+"/accepted: "+tc.name, func(t *testing.T) {
						if _, err := pvTransform(component, kind.handler, f.props(tc.reference), policy); err != nil {
							t.Errorf("refused: %v", err)
						}
					})
				}
			}

			t.Run(component+"/"+name+"/an extension with no reference is not checked", func(t *testing.T) {
				props := cnpgImages(cnpgImage(17, map[string]any{"name": "pgvector"}, extension("")))
				if _, err := pvTransform(component, kind.handler, props, policy); err != nil {
					t.Errorf("refused: %v", err)
				}
			})

			// The required-field refusal reads whether the field was authored,
			// not what it holds: an authored empty image reaches the rule.
			for at, props := range map[string]map[string]any{
				"images[0].image: ":          cnpgImages(cnpgWith(cnpgImage(17), "image", "")),
				"componentImages[0].image: ": componentImages(""),
			} {
				t.Run(component+"/"+name+"/an empty "+at, func(t *testing.T) {
					_, err := pvTransform(component, kind.handler, props, policy)
					trWant(t, err, at+trRejected("", ""))
				})
			}
		}
	}
}

// TestImageTagRule_Postgresql: the image a postgresql component runs is held to
// the rule where the property that produced it is known. An image composed
// from version is refused under version, with the image it made; an authored
// imageName under imageName. version is read for the default image only, so it
// is not checked beside an authored imageName.
func TestImageTagRule_Postgresql(t *testing.T) {
	lower := func(props map[string]any) (oam.LoweringResult, error) {
		return components.PostgresqlRule{}.LowerComponent(
			&oam.Component{Name: "db", Type: "postgresql", Properties: props},
			oam.LoweringContext{Namer: oam.NewNameAllocator()})
	}
	refused := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"version latest", map[string]any{"version": "latest"},
			`version: "latest" is refused as the tag of the default image: ` + trRejected("ghcr.io/cloudnative-pg/postgresql:latest", trLatest)},
		{"a version that makes no tag", map[string]any{"version": "16 beta"},
			`version: "16 beta" is refused as the tag of the default image: ` + trRejected("ghcr.io/cloudnative-pg/postgresql:16 beta", "")},
		{"imageName with no tag", map[string]any{"imageName": "registry.example/team/postgresql"},
			"imageName: " + trRejected("registry.example/team/postgresql", trNoTag)},
		{"imageName at latest", map[string]any{"imageName": "registry.example/team/postgresql:latest"},
			"imageName: " + trRejected("registry.example/team/postgresql:latest", trLatest)},
		{"imageName at latest beside a version", map[string]any{"version": "17", "imageName": "registry.example/team/postgresql:latest"},
			"imageName: " + trRejected("registry.example/team/postgresql:latest", trLatest)},
	}
	for _, tc := range refused {
		t.Run("refused: "+tc.name, func(t *testing.T) {
			_, err := lower(tc.props)
			trWant(t, err)
			if err != nil && !strings.HasPrefix(err.Error(), tc.want) {
				t.Errorf("err = %v, want one starting %q", err, tc.want)
			}
		})
	}

	accepted := []struct {
		name  string
		props map[string]any
	}{
		{"the default image", map[string]any{}},
		{"a version", map[string]any{"version": "17.2"}},
		{"imageName with a tag", map[string]any{"imageName": "registry.example/team/postgresql:16.4"}},
		{"imageName by digest alone", map[string]any{"imageName": "registry.example/team/postgresql" + trDigest}},
		{"version latest beside an imageName", map[string]any{"version": "latest", "imageName": "registry.example/team/postgresql:16.4"}},
	}
	for _, tc := range accepted {
		t.Run("accepted: "+tc.name, func(t *testing.T) {
			if _, err := lower(tc.props); err != nil {
				t.Errorf("refused: %v", err)
			}
		})
	}
}
