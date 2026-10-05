package components

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestServiceSpecSchemaMatchesParser pins schemaServiceSpec's key set to
// serviceSpecPropertyKeys, at every level, and the `ports` item schema to
// servicePortKeys: every key the parser reads is published, nothing it ignores
// is, and no rejected key is advertised.
func TestServiceSpecSchemaMatchesParser(t *testing.T) {
	got := slices.Sorted(maps.Keys(schemaServiceSpec()))
	want := slices.Sorted(slices.Values(serviceSpecPropertyKeys))
	if !slices.Equal(got, want) {
		t.Fatalf("schemaServiceSpec() keys = %v\nwant %v", got, want)
	}
	full := (&ServiceHandler{}).PropertySchema()
	for k := range serviceRejectedKeys {
		if _, ok := full[k]; ok {
			t.Errorf("the service schema publishes rejected key %q", k)
		}
	}

	root := oam.PropertySchema{Type: oam.PropertyTypeObject, Properties: full}
	assertSchemaKeysAt(t, root, "sessionAffinityConfig", serviceSessionAffinityConfigKeys)
	assertSchemaKeysAt(t, root, "sessionAffinityConfig.clientIP", serviceClientIPConfigKeys)
	assertSchemaKeysAt(t, root, "ports.[]", servicePortKeys)

	// The whole schema is the four keys service.go reads itself plus the fragment.
	wantFull := slices.Sorted(slices.Values(append([]string{"type", "clusterIP", "selector", "ports"}, serviceSpecPropertyKeys...)))
	if gotFull := slices.Sorted(maps.Keys(full)); !slices.Equal(gotFull, wantFull) {
		t.Errorf("PropertySchema() keys = %v\nwant %v", gotFull, wantFull)
	}
}

// TestServiceSpecSchema_EveryKeyDescribed walks the service schema recursively:
// every property, nested property and array item carries a Description.
func TestServiceSpecSchema_EveryKeyDescribed(t *testing.T) {
	var walk func(path string, s oam.PropertySchema)
	walk = func(path string, s oam.PropertySchema) {
		if strings.TrimSpace(s.Description) == "" {
			t.Errorf("%s: missing Description", path)
		}
		for k, sub := range s.Properties {
			walk(path+"."+k, sub)
		}
		if s.Items != nil {
			walk(path+"[]", *s.Items)
		}
	}
	for k, s := range (&ServiceHandler{}).PropertySchema() {
		walk(k, s)
	}
}

// TestParseServiceSpec_EveryKeyIsRead authors each published key once, on a
// document where it is legal, and checks that it changed ServiceSpecFields: a
// key published but dropped by the parser would leave the fields at their zero
// value. The table's key set is pinned to serviceSpecPropertyKeys.
func TestParseServiceSpec_EveryKeyIsRead(t *testing.T) {
	lb := func(extra map[string]any) map[string]any {
		props := map[string]any{"type": "LoadBalancer", "ports": []any{map[string]any{"port": 80}}}
		for k, v := range extra {
			props[k] = v
		}
		return props
	}
	samples := map[string]map[string]any{
		"externalName":             {"type": "ExternalName", "externalName": "db.example.com"},
		"externalTrafficPolicy":    lb(map[string]any{"externalTrafficPolicy": "Local"}),
		"internalTrafficPolicy":    lb(map[string]any{"internalTrafficPolicy": "Local"}),
		"trafficDistribution":      lb(map[string]any{"trafficDistribution": "PreferSameZone"}),
		"sessionAffinity":          lb(map[string]any{"sessionAffinity": "ClientIP"}),
		"publishNotReadyAddresses": lb(map[string]any{"publishNotReadyAddresses": true}),
		"ipFamilies":               lb(map[string]any{"ipFamilies": []any{"IPv6"}}),
		"ipFamilyPolicy":           lb(map[string]any{"ipFamilyPolicy": "PreferDualStack"}),
		"loadBalancerClass":        lb(map[string]any{"loadBalancerClass": "example.com/internal"}),
		"loadBalancerSourceRanges": lb(map[string]any{"loadBalancerSourceRanges": []any{"10.0.0.0/8"}}),
		"loadBalancerIP":           lb(map[string]any{"loadBalancerIP": "192.0.2.10"}),
		"healthCheckNodePort":      lb(map[string]any{"externalTrafficPolicy": "Local", "healthCheckNodePort": 32000}),
		"sessionAffinityConfig": lb(map[string]any{
			"sessionAffinity":       "ClientIP",
			"sessionAffinityConfig": map[string]any{"clientIP": map[string]any{"timeoutSeconds": 600}},
		}),
		"allocateLoadBalancerNodePorts": lb(map[string]any{"allocateLoadBalancerNodePorts": false}),
	}
	if got, want := slices.Sorted(maps.Keys(samples)), slices.Sorted(slices.Values(serviceSpecPropertyKeys)); !slices.Equal(got, want) {
		t.Fatalf("sample keys = %v\nwant serviceSpecPropertyKeys %v", got, want)
	}
	for key, props := range samples {
		t.Run(key, func(t *testing.T) {
			with, err := parseService(&oam.Component{Name: "api", Type: "service", Properties: props})
			if err != nil {
				t.Fatalf("parseService: %v", err)
			}
			without := map[string]any{}
			for k, v := range props {
				if k != key {
					without[k] = v
				}
			}
			// externalName is required on its type, so its baseline is another name.
			if key == "externalName" {
				without[key] = "other.example.com"
			}
			base, err := parseService(&oam.Component{Name: "api", Type: "service", Properties: without})
			if err != nil {
				t.Fatalf("parseService without %s: %v", key, err)
			}
			if reflect.DeepEqual(with.Spec, base.Spec) {
				t.Errorf("authoring %s left ServiceSpecFields unchanged: %+v", key, with.Spec)
			}
		})
	}
}

// Nothing is defaulted: a document that authors none of the keys leaves every
// field at its zero value, so Generate writes none of them.
func TestParseServiceSpec_UnauthoredIsZero(t *testing.T) {
	c, err := parseService(&oam.Component{Name: "api", Type: "service", Properties: map[string]any{
		"ports": []any{map[string]any{"port": 80}},
		// Explicit nulls, false and an empty list all read as "not authored".
		"sessionAffinity":          nil,
		"sessionAffinityConfig":    nil,
		"ipFamilies":               []any{},
		"loadBalancerSourceRanges": []any{},
		"publishNotReadyAddresses": false,
	}})
	if err != nil {
		t.Fatalf("parseService: %v", err)
	}
	if !reflect.DeepEqual(c.Spec, ServiceSpecFields{}) {
		t.Errorf("Spec = %+v, want the zero value", c.Spec)
	}
}

// apply hands out copies: an edit to one rendered ServiceSpec must not reach
// the config, or a second render.
func TestServiceSpecFields_ApplyCopies(t *testing.T) {
	c, err := parseService(&oam.Component{Name: "api", Type: "service", Properties: map[string]any{
		"type":                          "LoadBalancer",
		"ports":                         []any{map[string]any{"port": 80}},
		"internalTrafficPolicy":         "Local",
		"trafficDistribution":           "PreferSameNode",
		"sessionAffinity":               "ClientIP",
		"sessionAffinityConfig":         map[string]any{"clientIP": map[string]any{"timeoutSeconds": 600}},
		"ipFamilies":                    []any{"IPv4", "IPv6"},
		"ipFamilyPolicy":                "RequireDualStack",
		"loadBalancerClass":             "example.com/internal",
		"loadBalancerSourceRanges":      []any{"10.0.0.0/8"},
		"allocateLoadBalancerNodePorts": false,
	}})
	if err != nil {
		t.Fatalf("parseService: %v", err)
	}
	var first corev1.ServiceSpec
	c.Spec.apply(&first)
	*first.InternalTrafficPolicy = corev1.ServiceInternalTrafficPolicyCluster
	*first.TrafficDistribution = "changed"
	*first.SessionAffinityConfig.ClientIP.TimeoutSeconds = 1
	first.IPFamilies[0] = corev1.IPv6Protocol
	*first.IPFamilyPolicy = corev1.IPFamilyPolicySingleStack
	*first.LoadBalancerClass = "changed"
	first.LoadBalancerSourceRanges[0] = "192.0.2.0/24"
	*first.AllocateLoadBalancerNodePorts = true

	var second corev1.ServiceSpec
	c.Spec.apply(&second)
	want := corev1.ServiceSpec{
		InternalTrafficPolicy:         new(corev1.ServiceInternalTrafficPolicyLocal),
		TrafficDistribution:           new("PreferSameNode"),
		SessionAffinity:               corev1.ServiceAffinityClientIP,
		SessionAffinityConfig:         &corev1.SessionAffinityConfig{ClientIP: &corev1.ClientIPConfig{TimeoutSeconds: new(int32(600))}},
		IPFamilies:                    []corev1.IPFamily{corev1.IPv4Protocol, corev1.IPv6Protocol},
		IPFamilyPolicy:                new(corev1.IPFamilyPolicyRequireDualStack),
		LoadBalancerClass:             new("example.com/internal"),
		LoadBalancerSourceRanges:      []string{"10.0.0.0/8"},
		AllocateLoadBalancerNodePorts: new(false),
	}
	if !reflect.DeepEqual(second, want) {
		t.Errorf("second apply = %+v\nwant %+v", second, want)
	}
}
