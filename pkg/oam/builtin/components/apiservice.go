package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/runtime/schema"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// APIServiceHandler handles OAM apiservice components: the kind-named
// projection of a cluster-scoped apiregistration.k8s.io/v1 APIService
// (go-kure/launcher#943).
//
// Its properties are exactly the top-level fields of
// apiregistrationv1.APIServiceSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the APIService, named after the component unless
// `objectName` names it, with no namespace, and nothing else. The API requires
// that name to be the spec's `<version>.<group>`, and the kind refuses any
// other at generation.
//
// No dimension of the environment policy reads an APIService. The kind is
// gated as every emitted object is, by the policy's object kind rules
// (oam.ObjectKindPolicy). What it does once applied is out of the build's
// reach: the API server hands the group and version to the Service it names,
// for every client of the cluster, and that Service is not resolved at build.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type APIServiceHandler struct{}

// CanHandle returns true for the apiservice component type.
func (h *APIServiceHandler) CanHandle(componentType string) bool {
	return componentType == "apiservice"
}

// PropertySchema declares every top-level apiregistrationv1.APIServiceSpec
// field by its json name.
func (h *APIServiceHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"group": {
			Type: oam.PropertyTypeString, Required: true,
			Description: "Required. APIService spec.group: the API group the Service serves. The object's name, the component's or its objectName, must be <version>.<group>.",
		},
		"version": {
			Type: oam.PropertyTypeString, Required: true,
			Description: "Required. APIService spec.version: the API version the Service serves, e.g. v1beta1.",
		},
		"service": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "APIService spec.service: the Service the API server hands the group and version to: namespace (required), name (required), port. Written as authored, and not resolved at build. Unauthored, the group and version are served by the API server itself.",
		},
		"groupPriorityMinimum": {
			Type: oam.PropertyTypeInteger, Required: true,
			Description: "Required. APIService spec.groupPriorityMinimum: the priority of the group among all groups, at least this; higher is preferred. The API server takes 1 to 20000.",
		},
		"versionPriority": {
			Type: oam.PropertyTypeInteger, Required: true,
			Description: "Required. APIService spec.versionPriority: the priority of this version within its group; higher is preferred. The API server takes 1 to 1000.",
		},
		"insecureSkipTLSVerify": {
			Type:        oam.PropertyTypeBoolean,
			Description: "APIService spec.insecureSkipTLSVerify: whether the API server skips verifying the Service's serving certificate. Not allowed beside caBundle.",
		},
		"caBundle": {
			Type:        oam.PropertyTypeString,
			Description: "APIService spec.caBundle: the PEM-encoded CA bundle, base64-encoded, that verifies the Service's serving certificate.",
		},
	}
}

// apiServiceKind is the apiservice kind: see policyFreeKind. The Go type
// always encodes groupPriorityMinimum and versionPriority, which the API
// requires, so they are on the required list.
//
// Read from ValidateAPIService, pkg/apis/apiregistration/validation of
// k8s.io/kube-aggregator v0.37.1: the object must be named
// spec.version+"."+spec.group; the version is required, and the group unless
// the version is v1; and a service names its namespace and its name. The
// kind checks those for presence, and requires the group always: the one
// APIService without one is the core API's, named "v1.", which is no DNS-1123
// subdomain and so no name a component or objectName can give. The form of each value, the two priorities'
// ranges, the port and how caBundle and insecureSkipTLSVerify combine are left
// to the API server.
var apiServiceKind = &policyFreeKind[apiregistrationv1.APIServiceSpec]{
	upstream: "apiregistration.k8s.io/v1 APIServiceSpec",
	required: map[string]string{
		"groupPriorityMinimum": "the priority of the group among all groups, at least this (1 to 20000)",
		"versionPriority":      "the priority of this version within its group (1 to 1000)",
	},
	validate: func(spec *apiregistrationv1.APIServiceSpec) error {
		switch {
		case spec.Version == "":
			return errors.New("version: required (the API version the Service serves)")
		case spec.Group == "":
			return errors.New("group: required (the API group the Service serves)")
		}
		if svc := spec.Service; svc != nil {
			switch {
			case svc.Namespace == "":
				return errors.New("service.namespace: required (the namespace of the Service that serves the group and version)")
			case svc.Name == "":
				return errors.New("service.name: required (the name of the Service that serves the group and version)")
			}
		}
		return nil
	},
	checkName: func(name string, spec *apiregistrationv1.APIServiceSpec) error {
		if want := spec.Version + "." + spec.Group; name != want {
			return errors.Errorf("the APIService is named %q, and the API requires the name %q (<version>.<group>): name the component so, or set %s", name, want, oam.ObjectNameProperty)
		}
		return nil
	},
	build: func(name, _ string, spec *apiregistrationv1.APIServiceSpec) client.Object {
		svc := kubernetes.CreateAPIService(name)
		spec.DeepCopyInto(&svc.Spec)
		return svc
	},
}

// ToApplicationConfig decodes an OAM apiservice component into its config.
// The build namespace is not used: an APIService is cluster-scoped.
func (h *APIServiceHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return apiServiceKind.config(component)
}

// ContractMetadata implements oam.ContractDescriber.
func (h *APIServiceHandler) ContractMetadata() oam.ContractMetadata {
	return contract("apiservice")
}

// ComponentObject declares the apiservice kind's APIService, which is
// cluster-scoped.
func (h *APIServiceHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: apiregistrationv1.GroupName, Kind: "APIService"}, oam.ObjectScopeCluster
}
