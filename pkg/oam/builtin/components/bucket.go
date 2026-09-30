package components

import (
	"strings"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/go-kure/kure/pkg/kubernetes/fluxcd"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// BucketHandler handles the kind-named `bucket` component: a 1:1 projection of
// Flux's BucketSpec. It emits one Bucket named after the component. See
// fluxsource.go for what the source components share.
type BucketHandler struct{}

// CanHandle returns true for the bucket component type.
func (h *BucketHandler) CanHandle(componentType string) bool {
	return componentType == "bucket"
}

// PropertySchema declares every top-level key of sourcev1.BucketSpec. A test
// ties this key set to the struct.
func (h *BucketHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"provider":           fluxSourceString("Bucket spec.provider: generic (S3-compatible, Flux's default), aws, gcp or azure. Flux's gcp provider ignores endpoint and fetches from storage.googleapis.com, which is then the host the policy's allowed registries must list."),
		"bucketName":         fluxSourceRequiredString("Bucket spec.bucketName: the object storage bucket."),
		"endpoint":           fluxSourceRequiredString("Bucket spec.endpoint: the object storage address, host[:port] or a URL as the provider expects. Except under provider gcp, its host must be in the policy's allowed registries when that list is non-empty; such a list also refuses an Amazon S3 endpoint (any containing amazonaws) unless the provider is azure or gcp, since Flux's S3 client picks the host it contacts from region at runtime."),
		"sts":                fluxSourceObject("Bucket spec.sts: a Security Token Service for temporary credentials (aws and generic providers). Its endpoint is not checked against the allowed registries."),
		"insecure":           fluxSourceBool("Bucket spec.insecure: allow a non-TLS endpoint."),
		"region":             fluxSourceString("Bucket spec.region of the endpoint. With an Amazon S3 endpoint it selects the host Flux fetches from (see endpoint)."),
		"prefix":             fluxSourceString("Bucket spec.prefix for server-side filtering of objects."),
		"secretRef":          fluxSourceObject("Bucket spec.secretRef: the Secret holding the credentials, in the namespace the Bucket lands in."),
		"serviceAccountName": fluxSourceString("Bucket spec.serviceAccountName for workload identity (gcp and aws providers)."),
		"certSecretRef":      fluxSourceObject("Bucket spec.certSecretRef: the Secret holding a client certificate and/or CA certificate (generic provider)."),
		"proxySecretRef":     fluxSourceObject("Bucket spec.proxySecretRef: the Secret holding the proxy configuration."),
		"interval":           fluxSourceString("Bucket spec.interval as a duration (e.g. 10m); defaults to 60m when unset or zero."),
		"timeout":            fluxSourceString("Bucket spec.timeout for fetch operations, as a duration."),
		"ignore":             fluxSourceString("Bucket spec.ignore: exclusion patterns in .sourceignore format."),
		"suspend":            fluxSourceBool("Bucket spec.suspend: stop reconciling the source. Also skips the auto health check."),
	}
}

// ToApplicationConfig decodes the component's properties strictly into a
// sourcev1.BucketSpec: any key BucketSpec does not declare, at any depth, and
// any wrongly typed value is an error. Checks: bucketName and endpoint are set.
func (h *BucketHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	spec, _, err := builtin.DecodeStrictJSON[sourcev1.BucketSpec](component.Properties)
	if err != nil {
		return nil, errors.Errorf("bucket: properties do not decode as a BucketSpec: %w", err)
	}
	cfg := &BucketConfig{Name: component.Name, Namespace: namespace, Spec: *spec}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// BucketConfig implements stack.ApplicationConfig for bucket components.
type BucketConfig struct {
	// Name is the component name, and the Bucket's name.
	Name string
	// Namespace is the application namespace. The Bucket lands here unless a
	// Flux namespace is set (SetFluxNamespace).
	Namespace string
	// Spec is the Bucket spec as authored. Generate copies it and applies the
	// interval default to the copy.
	Spec sourcev1.BucketSpec

	// fluxNS overrides the Bucket's namespace. Set by postProcessFluxNamespace
	// via TransformContext.FluxNamespace.
	fluxNS string
}

// validate holds the checks shared by the parse path and Generate, which
// repeats them for a config built directly by a library caller. The CRD has no
// pattern for endpoint, so only its presence is checked.
func (c *BucketConfig) validate() error {
	if c.Spec.BucketName == "" {
		return errors.New("bucket: bucketName is required")
	}
	if c.Spec.Endpoint == "" {
		return errors.New("bucket: endpoint is required")
	}
	return nil
}

// gcsHost is the host Flux's gcp Bucket provider fetches from: its client is
// Google Cloud Storage's own, built without an endpoint option, so it reaches
// the storage service's default host and never reads spec.endpoint.
const gcsHost = "storage.googleapis.com"

// ApplyPolicy rejects a Bucket whose fetch host is not in the policy's allowed
// registries: the endpoint host for the generic, aws and azure providers (and
// none, which Flux treats as generic), and gcsHost for gcp, whose endpoint Flux
// ignores. Under a non-empty allowlist it also refuses an Amazon S3 endpoint on
// the S3 client's path (every provider but gcp and azure), whose contacted host
// cannot be checked (namesAmazonS3).
func (c *BucketConfig) ApplyPolicy(p oam.Policy) error {
	if c.Spec.Provider == sourcev1.BucketProviderGoogle {
		return enforceFluxSourceHost("bucket", "provider gcp (Flux ignores endpoint)", gcsHost, p)
	}
	if c.Spec.Provider != sourcev1.BucketProviderAzure && p != nil && len(p.AllowedRegistries()) > 0 &&
		namesAmazonS3(c.Spec.Endpoint) {
		return errors.Errorf("bucket: endpoint: %q is treated as an Amazon S3 host (it contains \"amazonaws\"): "+
			"Flux's S3 client fetches such a bucket not from endpoint but from the S3 host of spec.region, or of the bucket's "+
			"location when region is unset, chosen at runtime, so the host cannot be checked against the allowed registries %v",
			c.Spec.Endpoint, p.AllowedRegistries())
	}
	return enforceFluxSourceHost("bucket", "endpoint", c.Spec.Endpoint, p)
}

// namesAmazonS3 reports whether a Bucket endpoint may be one the S3 client
// behind Flux's generic and aws providers (minio-go) treats as Amazon S3. For
// such an endpoint that client replaces the host with the regional S3 host of
// the bucket's region — spec.region, which may name any AWS region or
// partition, else the location it discovers — so the endpoint an allowlist
// admitted is not the host fetched from. It errs broad, since the refusal it
// drives fails closed: the client's Amazon host patterns leave their dots
// unescaped, so look-alikes such as s3.x-amazonaws.com match them too, and any
// endpoint containing "amazonaws", in any case, counts. The whole value is
// tested: an endpoint is host[:port], and the client refuses one with a path.
func namesAmazonS3(endpoint string) bool {
	return strings.Contains(strings.ToLower(endpoint), "amazonaws")
}

// SetFluxNamespace moves the Bucket to ns. Satisfies
// pkg/oam.fluxNamespaceSettable.
func (c *BucketConfig) SetFluxNamespace(ns string) { c.fluxNS = ns }

// EmitsAutoHealthCheck vetoes the auto health check for `suspend: true`, where
// the document tells source-controller not to reconcile the source. Satisfies
// pkg/oam.autoHealthCheckEmitter.
func (c *BucketConfig) EmitsAutoHealthCheck() bool { return !c.Spec.Suspend }

// Generate emits the Bucket.
func (c *BucketConfig) Generate(_ *stack.Application) ([]*client.Object, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	b := fluxcd.CreateBucket(c.Name, fluxSourceNamespace(c.Namespace, c.fluxNS))
	// A deep copy, so no render shares a pointer or slice with the config.
	b.Spec = *c.Spec.DeepCopy()
	defaultFluxSourceInterval(&b.Spec.Interval)
	obj := client.Object(b)
	return []*client.Object{&obj}, nil
}
