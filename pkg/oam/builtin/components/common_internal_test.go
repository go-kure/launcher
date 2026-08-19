package components

import (
	"math"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/go-kure/launcher/pkg/oam"
)

func TestToInt64(t *testing.T) {
	cases := []struct {
		name   string
		in     any
		want   int64
		wantOk bool
	}{
		{"float64 whole", float64(42), 42, true},
		{"float64 fractional", 1.5, 0, false},
		{"float64 NaN", math.NaN(), 0, false},
		{"float64 large but valid", float64(1e15), 1e15, true},
		{"float64 overflow", float64(1e20), 0, false},
		{"float64 at MaxInt64 (rounds to 2^63, out of int64 range)", float64(math.MaxInt64), 0, false},
		{"int", int(7), 7, true},
		{"int32", int32(7), 7, true},
		{"int64", int64(7), 7, true},
		{"string rejected", "7", 0, false},
		{"nil rejected", nil, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := toInt64(tc.in)
			if ok != tc.wantOk {
				t.Fatalf("toInt64(%v) ok = %v, want %v", tc.in, ok, tc.wantOk)
			}
			if ok && got != tc.want {
				t.Errorf("toInt64(%v) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseEnv_FieldRef(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "POD_NAME",
				"valueFrom": map[string]any{
					"fieldRef": map[string]any{"fieldPath": "metadata.name"},
				},
			},
		},
	}
	vars, err := parseEnv(props)
	if err != nil {
		t.Fatalf("parseEnv: %v", err)
	}
	if len(vars) != 1 || vars[0].ValueFrom == nil || vars[0].ValueFrom.FieldRef == nil {
		t.Fatalf("unexpected result: %+v", vars)
	}
	if vars[0].ValueFrom.FieldRef.FieldPath != "metadata.name" {
		t.Errorf("fieldPath = %q, want metadata.name", vars[0].ValueFrom.FieldRef.FieldPath)
	}
}

func TestParseEnv_FieldRef_ExplicitV1_Accepted(t *testing.T) {
	// corev1.ObjectFieldSelector.APIVersion "defaults to v1" per its field doc
	// comment; an explicit "v1" must round-trip identically to the field being
	// left unset.
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "POD_NAME",
				"valueFrom": map[string]any{
					"fieldRef": map[string]any{"fieldPath": "metadata.name", "apiVersion": "v1"},
				},
			},
		},
	}
	vars, err := parseEnv(props)
	if err != nil {
		t.Fatalf("parseEnv: %v", err)
	}
	if vars[0].ValueFrom.FieldRef.APIVersion != "v1" {
		t.Errorf("apiVersion = %q, want v1", vars[0].ValueFrom.FieldRef.APIVersion)
	}
}

func TestParseEnv_FieldRef_InvalidAPIVersion_Error(t *testing.T) {
	// v1 is the only field-label conversion Kubernetes has ever shipped for the
	// downward API; any other apiVersion builds a Pod admission rejects.
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "POD_NAME",
				"valueFrom": map[string]any{
					"fieldRef": map[string]any{"fieldPath": "metadata.name", "apiVersion": "v2"},
				},
			},
		},
	}
	if _, err := parseEnv(props); err == nil {
		t.Fatal("expected error for fieldRef.apiVersion other than v1")
	}
}

func TestParseEnv_ResourceFieldRef(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "CPU_LIMIT",
				"valueFrom": map[string]any{
					"resourceFieldRef": map[string]any{
						"resource": "limits.cpu",
						"divisor":  "1m",
					},
				},
			},
		},
	}
	vars, err := parseEnv(props)
	if err != nil {
		t.Fatalf("parseEnv: %v", err)
	}
	if len(vars) != 1 || vars[0].ValueFrom == nil || vars[0].ValueFrom.ResourceFieldRef == nil {
		t.Fatalf("unexpected result: %+v", vars)
	}
	if vars[0].ValueFrom.ResourceFieldRef.Resource != "limits.cpu" {
		t.Errorf("resource = %q, want limits.cpu", vars[0].ValueFrom.ResourceFieldRef.Resource)
	}
}

func TestParseEnv_ResourceFieldRef_UnprefixedSelector_Error(t *testing.T) {
	// The downward API only understands "requests.<name>"/"limits.<name>"
	// selectors; a bare resource name like "cpu" is not valid and builds a Pod
	// admission rejects.
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "BAD",
				"valueFrom": map[string]any{
					"resourceFieldRef": map[string]any{"resource": "cpu"},
				},
			},
		},
	}
	if _, err := parseEnv(props); err == nil {
		t.Fatal("expected error for an unprefixed resourceFieldRef.resource selector")
	}
}

func TestParseEnv_ResourceFieldRef_MissingResource(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "BAD",
				"valueFrom": map[string]any{
					"resourceFieldRef": map[string]any{"divisor": "1m"},
				},
			},
		},
	}
	if _, err := parseEnv(props); err == nil {
		t.Fatal("expected error for missing resourceFieldRef.resource")
	}
}

func TestParseEnv_ValueFrom_MutuallyExclusive(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "BAD",
				"valueFrom": map[string]any{
					"fieldRef":     map[string]any{"fieldPath": "metadata.name"},
					"secretKeyRef": map[string]any{"name": "s", "key": "k"},
				},
			},
		},
	}
	if _, err := parseEnv(props); err == nil {
		t.Fatal("expected error for multiple valueFrom sources")
	}
}

func TestParseEnv_ValueFrom_Empty(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name":      "BAD",
				"valueFrom": map[string]any{},
			},
		},
	}
	if _, err := parseEnv(props); err == nil {
		t.Fatal("expected error for empty valueFrom")
	}
}

func TestParseEnv_FileKeyRef(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "FROM_FILE",
				"valueFrom": map[string]any{
					"fileKeyRef": map[string]any{
						"volumeName": "envfiles",
						"path":       "app.env",
						"key":        "SOME_KEY",
						"optional":   true,
					},
				},
			},
		},
	}
	vars, err := parseEnv(props)
	if err != nil {
		t.Fatalf("parseEnv: %v", err)
	}
	if len(vars) != 1 || vars[0].ValueFrom == nil || vars[0].ValueFrom.FileKeyRef == nil {
		t.Fatalf("unexpected result: %+v", vars)
	}
	fkr := vars[0].ValueFrom.FileKeyRef
	if fkr.VolumeName != "envfiles" || fkr.Path != "app.env" || fkr.Key != "SOME_KEY" {
		t.Errorf("unexpected fileKeyRef: %+v", fkr)
	}
	if fkr.Optional == nil || !*fkr.Optional {
		t.Errorf("expected Optional=true, got %+v", fkr)
	}
}

func TestParseEnv_FileKeyRef_AbsolutePath_Error(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "BAD",
				"valueFrom": map[string]any{
					"fileKeyRef": map[string]any{"volumeName": "envfiles", "path": "/etc/passwd", "key": "K"},
				},
			},
		},
	}
	if _, err := parseEnv(props); err == nil {
		t.Fatal("expected error for an absolute fileKeyRef.path")
	}
}

func TestParseEnv_FileKeyRef_EscapingPath_Error(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "BAD",
				"valueFrom": map[string]any{
					"fileKeyRef": map[string]any{"volumeName": "envfiles", "path": "../../etc/passwd", "key": "K"},
				},
			},
		},
	}
	if _, err := parseEnv(props); err == nil {
		t.Fatal("expected error for a fileKeyRef.path containing '..'")
	}
}

func TestParseEnv_FileKeyRef_MissingRequiredField_Error(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "BAD",
				"valueFrom": map[string]any{
					"fileKeyRef": map[string]any{"volumeName": "envfiles", "path": "app.env"},
				},
			},
		},
	}
	if _, err := parseEnv(props); err == nil {
		t.Fatal("expected error for fileKeyRef missing key")
	}
}

func TestParseEnv_FileKeyRef_MutuallyExclusiveWithSecretKeyRef(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "BAD",
				"valueFrom": map[string]any{
					"fileKeyRef":   map[string]any{"volumeName": "envfiles", "path": "app.env", "key": "K"},
					"secretKeyRef": map[string]any{"name": "s", "key": "k"},
				},
			},
		},
	}
	if _, err := parseEnv(props); err == nil {
		t.Fatal("expected error for fileKeyRef + secretKeyRef both set")
	}
}

// TestParseEnv_FileKeyRef_InvalidVolumeName_Error regression-tests a review
// finding (launcher#284): fileKeyRef.volumeName was copied into the Pod with
// no shape validation, so a value like "bad/name" could never resolve to any
// legal Pod volume (real admission's validateFileKeySelector requires it be a
// valid DNS-1123 label, matching Volume.Name's own constraint).
func TestParseEnv_FileKeyRef_InvalidVolumeName_Error(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "BAD",
				"valueFrom": map[string]any{
					"fileKeyRef": map[string]any{"volumeName": "bad/name", "path": "app.env", "key": "K"},
				},
			},
		},
	}
	if _, err := parseEnv(props); err == nil {
		t.Fatal("expected error for an invalid fileKeyRef.volumeName")
	}
}

func TestParseEnv_FileKeyRef_InvalidKey_Error(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "BAD",
				"valueFrom": map[string]any{
					"fileKeyRef": map[string]any{"volumeName": "envfiles", "path": "app.env", "key": "BAD=KEY"},
				},
			},
		},
	}
	if _, err := parseEnv(props); err == nil {
		t.Fatal("expected error for a fileKeyRef.key containing '='")
	}
}

func TestParseEnv_FileKeyRef_NonBoolOptional_Error(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "BAD",
				"valueFrom": map[string]any{
					"fileKeyRef": map[string]any{"volumeName": "envfiles", "path": "app.env", "key": "K", "optional": "true"},
				},
			},
		},
	}
	if _, err := parseEnv(props); err == nil {
		t.Fatal("expected error for a non-boolean fileKeyRef.optional")
	}
}

// TestParseEnv_SecretKeyRef_NonBoolOptional_Error and its configMapKeyRef
// sibling regression-test a review finding (launcher#284): a mistyped
// `optional: "true"` fell through the failed type assertion silently, so
// Optional stayed nil (defaults to required) — turning a requested optional
// dependency into a required one, the opposite of authored intent.
func TestParseEnv_SecretKeyRef_NonBoolOptional_Error(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "BAD",
				"valueFrom": map[string]any{
					"secretKeyRef": map[string]any{"name": "s", "key": "k", "optional": "true"},
				},
			},
		},
	}
	if _, err := parseEnv(props); err == nil {
		t.Fatal("expected error for a non-boolean secretKeyRef.optional")
	}
}

func TestParseEnv_ConfigMapKeyRef_NonBoolOptional_Error(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "BAD",
				"valueFrom": map[string]any{
					"configMapKeyRef": map[string]any{"name": "c", "key": "k", "optional": "true"},
				},
			},
		},
	}
	if _, err := parseEnv(props); err == nil {
		t.Fatal("expected error for a non-boolean configMapKeyRef.optional")
	}
}

func TestParseEnvFrom_ConfigMapAndSecret(t *testing.T) {
	props := map[string]any{
		"envFrom": []any{
			map[string]any{
				"configMapRef": map[string]any{"name": "cfg"},
				"prefix":       "CFG_",
			},
			map[string]any{
				"secretRef": map[string]any{"name": "sec", "optional": true},
			},
		},
	}
	out, err := parseEnvFrom(props)
	if err != nil {
		t.Fatalf("parseEnvFrom: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(out))
	}
	if out[0].ConfigMapRef == nil || out[0].ConfigMapRef.Name != "cfg" || out[0].Prefix != "CFG_" {
		t.Errorf("unexpected entry 0: %+v", out[0])
	}
	if out[1].SecretRef == nil || out[1].SecretRef.Name != "sec" || out[1].SecretRef.Optional == nil || !*out[1].SecretRef.Optional {
		t.Errorf("unexpected entry 1: %+v", out[1])
	}
}

func TestParseEnvFrom_Absent(t *testing.T) {
	out, err := parseEnvFrom(map[string]any{})
	if err != nil || out != nil {
		t.Fatalf("expected nil,nil for absent envFrom, got %v, %v", out, err)
	}
}

func TestParseEnvFrom_NonArray_Error(t *testing.T) {
	props := map[string]any{
		"envFrom": map[string]any{"secretRef": map[string]any{"name": "credentials"}},
	}
	if _, err := parseEnvFrom(props); err == nil {
		t.Fatal("expected error for a non-array envFrom value")
	}
}

func TestParseEnvFrom_BothRefs_Error(t *testing.T) {
	props := map[string]any{
		"envFrom": []any{
			map[string]any{
				"configMapRef": map[string]any{"name": "a"},
				"secretRef":    map[string]any{"name": "b"},
			},
		},
	}
	if _, err := parseEnvFrom(props); err == nil {
		t.Fatal("expected error when both configMapRef and secretRef are set")
	}
}

func TestParseEnvFrom_NeitherRef_Error(t *testing.T) {
	props := map[string]any{
		"envFrom": []any{
			map[string]any{"prefix": "X_"},
		},
	}
	if _, err := parseEnvFrom(props); err == nil {
		t.Fatal("expected error when neither configMapRef nor secretRef is set")
	}
}

func TestParseEnvFrom_MissingName_Error(t *testing.T) {
	props := map[string]any{
		"envFrom": []any{
			map[string]any{"configMapRef": map[string]any{}},
		},
	}
	if _, err := parseEnvFrom(props); err == nil {
		t.Fatal("expected error for missing configMapRef.name")
	}
}

func TestParseEnvFrom_InvalidPrefix_Error(t *testing.T) {
	// corev1.EnvFromSource.Prefix's field doc comment: "May consist of any
	// printable ASCII characters except '='" — a literal '=' would produce a
	// nonsensical env var definition (a key containing the KEY=VALUE
	// separator) that Kubernetes rejects at admission.
	props := map[string]any{
		"envFrom": []any{
			map[string]any{"configMapRef": map[string]any{"name": "cfg"}, "prefix": "BAD=PREFIX_"},
		},
	}
	if _, err := parseEnvFrom(props); err == nil {
		t.Fatal("expected error for a prefix containing '='")
	}
}

func TestParseEnvFrom_NonStringPrefix_Error(t *testing.T) {
	props := map[string]any{
		"envFrom": []any{
			map[string]any{"configMapRef": map[string]any{"name": "cfg"}, "prefix": 123},
		},
	}
	if _, err := parseEnvFrom(props); err == nil {
		t.Fatal("expected error for a non-string prefix")
	}
}

func TestParseEnvFrom_HyphenatedPrefix_Accepted(t *testing.T) {
	// Only '=' and non-printable-ASCII are excluded — a prefix like
	// "APP-CONFIG_" is valid printable ASCII and must not be rejected as if it
	// had to be a C identifier itself (only the final prefix+key concatenation
	// needs to be one).
	props := map[string]any{
		"envFrom": []any{
			map[string]any{"configMapRef": map[string]any{"name": "cfg"}, "prefix": "APP-CONFIG_"},
		},
	}
	out, err := parseEnvFrom(props)
	if err != nil {
		t.Fatalf("parseEnvFrom: %v", err)
	}
	if out[0].Prefix != "APP-CONFIG_" {
		t.Errorf("prefix = %q, want APP-CONFIG_", out[0].Prefix)
	}
}

func TestParseResources_ExtraNamedResources(t *testing.T) {
	resources := map[string]any{
		"requests": map[string]any{
			"cpu":               "100m",
			"nvidia.com/gpu":    "1",
			"ephemeral-storage": "1Gi",
		},
		"limits": map[string]any{
			"nvidia.com/gpu": "1",
		},
	}
	rr, err := parseResources(resources)
	if err != nil {
		t.Fatalf("parseResources: unexpected error: %v", err)
	}
	if got := quantityString(rr.Requests, corev1.ResourceCPU); got != "100m" {
		t.Errorf("Requests[cpu] = %q, want 100m", got)
	}
	gpuReq := rr.Requests[corev1.ResourceName("nvidia.com/gpu")]
	storageReq := rr.Requests[corev1.ResourceName("ephemeral-storage")]
	if gpuReq.String() != "1" || storageReq.String() != "1Gi" {
		t.Errorf("unexpected Requests: %+v", rr.Requests)
	}
	gpuLimit := rr.Limits[corev1.ResourceName("nvidia.com/gpu")]
	if gpuLimit.String() != "1" {
		t.Errorf("unexpected Limits: %+v", rr.Limits)
	}
}

func TestParseResources_NoExtra_NilMaps(t *testing.T) {
	rr, err := parseResources(map[string]any{
		"requests": map[string]any{"cpu": "100m"},
	})
	if err != nil {
		t.Fatalf("parseResources: unexpected error: %v", err)
	}
	if rr.Limits != nil {
		t.Errorf("expected nil Limits when limits was never authored, got %+v", rr.Limits)
	}
	if len(rr.Requests) != 1 {
		t.Errorf("expected exactly the authored cpu request, got %+v", rr.Requests)
	}
}

func TestParseResources_InvalidQuantity_Error(t *testing.T) {
	if _, err := parseResources(map[string]any{
		"requests": map[string]any{"cpu": "not-a-quantity"},
	}); err == nil {
		t.Fatal("expected error for invalid cpu quantity")
	}
}

func TestParseResources_NumericValue_Accepted(t *testing.T) {
	// A bare YAML/JSON number (e.g. `nvidia.com/gpu: 1`, decoded as float64) is
	// valid Kubernetes Quantity input, equivalent to the quoted string form —
	// corev1.Quantity.UnmarshalJSON parses the raw numeric literal directly
	// when it isn't wrapped in quotes. Silently rejecting it would make this
	// schema stricter than real Kubernetes and break a documented, commonly
	// authored form (the README documents nvidia.com/gpu as supported).
	req, err := parseResources(map[string]any{
		"requests": map[string]any{"nvidia.com/gpu": float64(1), "cpu": float64(0.5)},
		"limits":   map[string]any{"nvidia.com/gpu": float64(1)},
	})
	if err != nil {
		t.Fatalf("parseResources: %v", err)
	}
	gpu, ok := req.Requests["nvidia.com/gpu"]
	if !ok || gpu.Cmp(resource.MustParse("1")) != 0 {
		t.Errorf("nvidia.com/gpu = %v, ok=%v, want 1", gpu, ok)
	}
	cpu, ok := req.Requests[corev1.ResourceCPU]
	if !ok || cpu.Cmp(resource.MustParse("500m")) != 0 {
		t.Errorf("cpu = %v, ok=%v, want 500m", cpu, ok)
	}
}

func TestParseResources_ExtendedResource_Fractional_Error(t *testing.T) {
	// Unlike cpu/memory/storage/ephemeral-storage, extended resources (e.g.
	// nvidia.com/gpu) can only be requested in whole-number amounts.
	_, err := parseResources(map[string]any{
		"requests": map[string]any{"nvidia.com/gpu": "0.5"},
	})
	if err == nil {
		t.Fatal("expected error for a fractional extended-resource quantity")
	}
}

func TestParseResources_ExtendedResource_WholeNumber_Accepted(t *testing.T) {
	req, err := parseResources(map[string]any{
		"requests": map[string]any{"nvidia.com/gpu": "2"},
		"limits":   map[string]any{"nvidia.com/gpu": "2"},
	})
	if err != nil {
		t.Fatalf("parseResources: %v", err)
	}
	if q, ok := req.Requests["nvidia.com/gpu"]; !ok || q.Cmp(resource.MustParse("2")) != 0 {
		t.Errorf("nvidia.com/gpu = %v, ok=%v, want 2", q, ok)
	}
}

func TestParseResources_NegativeQuantity_Error(t *testing.T) {
	_, err := parseResources(map[string]any{
		"requests": map[string]any{"cpu": "-100m"},
	})
	if err == nil {
		t.Fatal("expected error for a negative resource quantity")
	}
}

func TestParseResources_InvalidResourceName_Error(t *testing.T) {
	// A malformed resource name key must be rejected at parse time — casting
	// it straight to corev1.ResourceName without validation would build
	// successfully and only fail Kubernetes admission at apply time.
	_, err := parseResources(map[string]any{
		"requests": map[string]any{"nvidia.com/gpu!": "1"},
	})
	if err == nil {
		t.Fatal("expected error for an invalid resource name")
	}
	if !strings.Contains(err.Error(), "nvidia.com/gpu!") {
		t.Errorf("expected error to name the offending key, got: %v", err)
	}
}

func TestParseLifecycle_Absent(t *testing.T) {
	lc, err := parseLifecycle(map[string]any{})
	if err != nil || lc != nil {
		t.Fatalf("expected nil,nil for absent lifecycle, got %v, %v", lc, err)
	}
}

func TestParseLifecycle_PostStartAndPreStop(t *testing.T) {
	props := map[string]any{
		"lifecycle": map[string]any{
			"postStart": map[string]any{
				"exec": map[string]any{"command": []any{"/bin/sh", "-c", "echo start"}},
			},
			"preStop": map[string]any{
				"sleep": map[string]any{"seconds": 5},
			},
		},
	}
	lc, err := parseLifecycle(props)
	if err != nil {
		t.Fatalf("parseLifecycle: %v", err)
	}
	if lc.PostStart == nil || lc.PostStart.Exec == nil {
		t.Error("expected postStart.exec")
	}
	if lc.PreStop == nil || lc.PreStop.Sleep == nil || lc.PreStop.Sleep.Seconds != 5 {
		t.Error("expected preStop.sleep.seconds=5")
	}
}

func TestParseLifecycleHandler_HTTPGet(t *testing.T) {
	h, err := parseLifecycleHandler(map[string]any{
		"httpGet": map[string]any{"path": "/started", "port": 8080, "host": "127.0.0.1"},
	})
	if err != nil {
		t.Fatalf("parseLifecycleHandler: %v", err)
	}
	if h.HTTPGet == nil || h.HTTPGet.Host != "127.0.0.1" {
		t.Errorf("unexpected handler: %+v", h.HTTPGet)
	}
}

func TestParseLifecycleHandler_HTTPGet_FractionalPort_Error(t *testing.T) {
	// A fractional YAML/JSON number (decoded as float64) must be rejected, not
	// silently truncated — Kubernetes container ports are integers, and silent
	// truncation (8080.5 -> 8080) would mask an authoring mistake.
	_, err := parseLifecycleHandler(map[string]any{
		"httpGet": map[string]any{"path": "/x", "port": 8080.5},
	})
	if err == nil {
		t.Fatal("expected error for a fractional httpGet port")
	}
}

func TestParseLifecycleHandler_TCPSocket_Unsupported(t *testing.T) {
	h, err := parseLifecycleHandler(map[string]any{
		"tcpSocket": map[string]any{"port": 8080},
	})
	if err == nil {
		t.Fatalf("expected error for unsupported tcpSocket handler, got handler=%+v", h)
	}
}

func TestParseLifecycleHandler_MultipleHandlers_Error(t *testing.T) {
	_, err := parseLifecycleHandler(map[string]any{
		"httpGet": map[string]any{"path": "/x", "port": 80},
		"exec":    map[string]any{"command": []any{"/bin/true"}},
	})
	if err == nil {
		t.Fatal("expected error for multiple handlers")
	}
}

func TestParseLifecycleHandler_Sleep_MissingSeconds_Error(t *testing.T) {
	_, err := parseLifecycleHandler(map[string]any{
		"sleep": map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error for sleep handler missing seconds")
	}
}

func TestParseLifecycleHandler_Sleep_NegativeSeconds_Error(t *testing.T) {
	_, err := parseLifecycleHandler(map[string]any{
		"sleep": map[string]any{"seconds": -1},
	})
	if err == nil {
		t.Fatal("expected error for sleep handler with negative seconds")
	}
}

func TestParseLifecycleHandler_Exec_NonStringElement_Error(t *testing.T) {
	// A mixed-type command such as ["sleep", 5] must be rejected, not silently
	// filtered down to ["sleep"] — that changes the authored hook and can make
	// it hang or fail at runtime.
	_, err := parseLifecycleHandler(map[string]any{
		"exec": map[string]any{"command": []any{"sleep", float64(5)}},
	})
	if err == nil {
		t.Fatal("expected error for a non-string exec command element")
	}
}

func TestParseLifecycleHandler_Exec_EmptyCommand_Error(t *testing.T) {
	_, err := parseLifecycleHandler(map[string]any{
		"exec": map[string]any{"command": []any{}},
	})
	if err == nil {
		t.Fatal("expected error for exec handler with empty command")
	}
}

func TestParseLifecycleHandler_NoHandler_Error(t *testing.T) {
	_, err := parseLifecycleHandler(map[string]any{})
	if err == nil {
		t.Fatal("expected error when no handler is specified")
	}
}

func TestParseSecurityContext_Absent(t *testing.T) {
	sc, err := parseSecurityContext(map[string]any{})
	if err != nil || sc != nil {
		t.Fatalf("expected nil,nil for absent securityContext, got %v, %v", sc, err)
	}
}

func TestParseSecurityContext_NonObject_Error(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  any
	}{
		{"scalar", "true"},
		{"array", []any{"a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseSecurityContext(map[string]any{"securityContext": tc.val})
			if err == nil {
				t.Fatal("expected error for non-object securityContext, got nil")
			}
		})
	}
}

func TestParseSecurityContext_FullFidelity(t *testing.T) {
	props := map[string]any{
		"securityContext": map[string]any{
			"runAsUser":                int64(1000),
			"runAsGroup":               int64(2000),
			"runAsNonRoot":             true,
			"readOnlyRootFilesystem":   true,
			"allowPrivilegeEscalation": false,
			"privileged":               false,
			"capabilities": map[string]any{
				"add":  []any{"NET_BIND_SERVICE"},
				"drop": []any{"ALL"},
			},
			"seccompProfile": map[string]any{"type": "RuntimeDefault"},
		},
	}
	sc, err := parseSecurityContext(props)
	if err != nil {
		t.Fatalf("parseSecurityContext: %v", err)
	}
	if sc.RunAsUser == nil || *sc.RunAsUser != 1000 {
		t.Errorf("RunAsUser = %v, want 1000", sc.RunAsUser)
	}
	if sc.RunAsGroup == nil || *sc.RunAsGroup != 2000 {
		t.Errorf("RunAsGroup = %v, want 2000", sc.RunAsGroup)
	}
	if len(sc.Capabilities.Add) != 1 || len(sc.Capabilities.Drop) != 1 {
		t.Errorf("unexpected capabilities: %+v", sc.Capabilities)
	}
	if sc.SeccompProfile == nil || string(sc.SeccompProfile.Type) != "RuntimeDefault" {
		t.Errorf("unexpected seccompProfile: %+v", sc.SeccompProfile)
	}
}

func TestParseSecurityContext_RunAsUser_Negative_Error(t *testing.T) {
	_, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{"runAsUser": int64(-1)},
	})
	if err == nil {
		t.Fatal("expected error for negative runAsUser")
	}
}

func TestParseSecurityContext_RunAsGroup_Negative_Error(t *testing.T) {
	_, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{"runAsGroup": int64(-1)},
	})
	if err == nil {
		t.Fatal("expected error for negative runAsGroup")
	}
}

func TestParseSecurityContext_SeccompLocalhost_RequiresProfile(t *testing.T) {
	_, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"seccompProfile": map[string]any{"type": "Localhost"},
		},
	})
	if err == nil {
		t.Fatal("expected error for Localhost seccompProfile missing localhostProfile")
	}
}

func TestParseSecurityContext_SeccompLocalhost_WithProfile(t *testing.T) {
	sc, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"seccompProfile": map[string]any{
				"type":             "Localhost",
				"localhostProfile": "profiles/my-profile.json",
			},
		},
	})
	if err != nil {
		t.Fatalf("parseSecurityContext: %v", err)
	}
	if sc.SeccompProfile == nil || sc.SeccompProfile.LocalhostProfile == nil || *sc.SeccompProfile.LocalhostProfile != "profiles/my-profile.json" {
		t.Errorf("unexpected seccompProfile: %+v", sc.SeccompProfile)
	}
}

func TestParseSecurityContext_SeccompRuntimeDefault_WithLocalhostProfile_Error(t *testing.T) {
	// localhostProfile is only meaningful (and only documented as valid) when
	// type is Localhost; authoring it alongside RuntimeDefault/Unconfined is a
	// contradictory input that must be rejected, not silently dropped.
	_, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"seccompProfile": map[string]any{
				"type":             "RuntimeDefault",
				"localhostProfile": "profiles/my-profile.json",
			},
		},
	})
	if err == nil {
		t.Fatal("expected error for RuntimeDefault seccompProfile with a localhostProfile set")
	}
}

func TestParseSecurityContext_SeccompUnconfined_WithLocalhostProfile_Error(t *testing.T) {
	_, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"seccompProfile": map[string]any{
				"type":             "Unconfined",
				"localhostProfile": "profiles/my-profile.json",
			},
		},
	})
	if err == nil {
		t.Fatal("expected error for Unconfined seccompProfile with a localhostProfile set")
	}
}

func TestParseSecurityContext_SeccompInvalidType_Error(t *testing.T) {
	_, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"seccompProfile": map[string]any{"type": "Bogus"},
		},
	})
	if err == nil {
		t.Fatal("expected error for invalid seccompProfile type")
	}
}

func TestParseSecurityContext_AppArmorRuntimeDefault_WithLocalhostProfile_Error(t *testing.T) {
	// Same contradictory-input rule as seccompProfile above: localhostProfile
	// is only meaningful when type is Localhost.
	_, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"appArmorProfile": map[string]any{
				"type":             "RuntimeDefault",
				"localhostProfile": "my-profile",
			},
		},
	})
	if err == nil {
		t.Fatal("expected error for RuntimeDefault appArmorProfile with a localhostProfile set")
	}
}

func TestParseSecurityContext_AppArmorUnconfined_WithLocalhostProfile_Error(t *testing.T) {
	_, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"appArmorProfile": map[string]any{
				"type":             "Unconfined",
				"localhostProfile": "my-profile",
			},
		},
	})
	if err == nil {
		t.Fatal("expected error for Unconfined appArmorProfile with a localhostProfile set")
	}
}

func TestParseSecurityContext_EmptyObject_ReturnsNil(t *testing.T) {
	// An authored but empty securityContext{} sets no recognized field, so `set`
	// stays false and the function must return nil rather than an all-zero-value
	// non-nil SecurityContext (which would make the container opt out of the
	// security-context trait's nil-only backfill for no reason).
	sc, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{},
	})
	if err != nil {
		t.Fatalf("parseSecurityContext: %v", err)
	}
	if sc != nil {
		t.Errorf("expected nil for empty securityContext object, got %+v", sc)
	}
}

func TestParseProbe_HTTPGetHost(t *testing.T) {
	probe, err := parseProbe(map[string]any{
		"httpGet": map[string]any{
			"path": "/healthz",
			"port": 8080,
			"host": "10.0.0.1",
		},
	}, "liveness")
	if err != nil {
		t.Fatalf("parseProbe: %v", err)
	}
	if probe.HTTPGet.Host != "10.0.0.1" {
		t.Errorf("Host = %q, want 10.0.0.1", probe.HTTPGet.Host)
	}
}

func TestParseProbe_TerminationGracePeriodSeconds(t *testing.T) {
	probe, err := parseProbe(map[string]any{
		"exec":                          map[string]any{"command": []any{"/bin/true"}},
		"terminationGracePeriodSeconds": 30,
	}, "liveness")
	if err != nil {
		t.Fatalf("parseProbe: %v", err)
	}
	if probe.TerminationGracePeriodSeconds == nil || *probe.TerminationGracePeriodSeconds != 30 {
		t.Errorf("TerminationGracePeriodSeconds = %v, want 30", probe.TerminationGracePeriodSeconds)
	}
}

func TestParseProbe_TerminationGracePeriodSeconds_Negative_Error(t *testing.T) {
	_, err := parseProbe(map[string]any{
		"exec":                          map[string]any{"command": []any{"/bin/true"}},
		"terminationGracePeriodSeconds": -5,
	}, "liveness")
	if err == nil {
		t.Fatal("expected error for negative terminationGracePeriodSeconds")
	}
}

func TestParseProbe_TerminationGracePeriodSeconds_Zero_Error(t *testing.T) {
	_, err := parseProbe(map[string]any{
		"exec":                          map[string]any{"command": []any{"/bin/true"}},
		"terminationGracePeriodSeconds": 0,
	}, "startup")
	if err == nil {
		t.Fatal("expected error for zero terminationGracePeriodSeconds (minimum is 1)")
	}
}

func TestParseProbe_TerminationGracePeriodSeconds_NotPermittedOnReadiness_Error(t *testing.T) {
	_, err := parseProbe(map[string]any{
		"exec":                          map[string]any{"command": []any{"/bin/true"}},
		"terminationGracePeriodSeconds": 30,
	}, "readiness")
	if err == nil {
		t.Fatal("expected error for terminationGracePeriodSeconds on a readiness probe")
	}
	if !strings.Contains(err.Error(), "readiness") {
		t.Errorf("expected error to mention readiness, got: %v", err)
	}
}

// --- round-1 codex-review regression tests (launcher#278) ------------------

func TestParseEnv_ValueAndValueFrom_MutuallyExclusive(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name":  "BAD",
				"value": "literal",
				"valueFrom": map[string]any{
					"fieldRef": map[string]any{"fieldPath": "metadata.name"},
				},
			},
		},
	}
	if _, err := parseEnv(props); err == nil {
		t.Fatal("expected error when both value and valueFrom are set")
	}
}

func TestParseEnv_EmptyValueWithValueFrom_Allowed(t *testing.T) {
	// Mirrors corev1.EnvVar.ValueFrom's own doc comment ("Cannot be used if value
	// is not empty"): an explicitly-authored empty value alongside valueFrom is
	// NOT rejected, matching upstream Kubernetes API validation exactly.
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name":  "OK",
				"value": "",
				"valueFrom": map[string]any{
					"fieldRef": map[string]any{"fieldPath": "metadata.name"},
				},
			},
		},
	}
	vars, err := parseEnv(props)
	if err != nil {
		t.Fatalf("parseEnv: %v", err)
	}
	if len(vars) != 1 || vars[0].ValueFrom == nil {
		t.Fatalf("unexpected result: %+v", vars)
	}
}

func TestParseEnv_SecretKeyRef_Optional(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "SECRET_VAL",
				"valueFrom": map[string]any{
					"secretKeyRef": map[string]any{"name": "s", "key": "k", "optional": true},
				},
			},
		},
	}
	vars, err := parseEnv(props)
	if err != nil {
		t.Fatalf("parseEnv: %v", err)
	}
	if len(vars) != 1 || vars[0].ValueFrom == nil || vars[0].ValueFrom.SecretKeyRef == nil {
		t.Fatalf("unexpected result: %+v", vars)
	}
	if vars[0].ValueFrom.SecretKeyRef.Optional == nil || !*vars[0].ValueFrom.SecretKeyRef.Optional {
		t.Errorf("expected SecretKeyRef.Optional=true, got %+v", vars[0].ValueFrom.SecretKeyRef)
	}
}

func TestParseEnv_ConfigMapKeyRef_Optional(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "CONFIG_VAL",
				"valueFrom": map[string]any{
					"configMapKeyRef": map[string]any{"name": "c", "key": "k", "optional": false},
				},
			},
		},
	}
	vars, err := parseEnv(props)
	if err != nil {
		t.Fatalf("parseEnv: %v", err)
	}
	if len(vars) != 1 || vars[0].ValueFrom == nil || vars[0].ValueFrom.ConfigMapKeyRef == nil {
		t.Fatalf("unexpected result: %+v", vars)
	}
	if vars[0].ValueFrom.ConfigMapKeyRef.Optional == nil || *vars[0].ValueFrom.ConfigMapKeyRef.Optional {
		t.Errorf("expected ConfigMapKeyRef.Optional=false (explicitly set), got %+v", vars[0].ValueFrom.ConfigMapKeyRef)
	}
}

func TestParseLifecycleHandler_HTTPGet_Headers(t *testing.T) {
	h, err := parseLifecycleHandler(map[string]any{
		"httpGet": map[string]any{
			"path": "/started",
			"port": 8080,
			"httpHeaders": []any{
				map[string]any{"name": "X-Auth", "value": "secret"},
				map[string]any{"name": "X-Empty"},
			},
		},
	})
	if err != nil {
		t.Fatalf("parseLifecycleHandler: %v", err)
	}
	if h.HTTPGet == nil || len(h.HTTPGet.HTTPHeaders) != 2 {
		t.Fatalf("unexpected handler: %+v", h.HTTPGet)
	}
	if h.HTTPGet.HTTPHeaders[0].Name != "X-Auth" || h.HTTPGet.HTTPHeaders[0].Value != "secret" {
		t.Errorf("unexpected header 0: %+v", h.HTTPGet.HTTPHeaders[0])
	}
	if h.HTTPGet.HTTPHeaders[1].Name != "X-Empty" || h.HTTPGet.HTTPHeaders[1].Value != "" {
		t.Errorf("unexpected header 1: %+v", h.HTTPGet.HTTPHeaders[1])
	}
}

func TestParseSecurityContext_SELinuxOptions(t *testing.T) {
	sc, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"seLinuxOptions": map[string]any{
				"user":  "u",
				"role":  "r",
				"type":  "t",
				"level": "l",
			},
		},
	})
	if err != nil {
		t.Fatalf("parseSecurityContext: %v", err)
	}
	if sc == nil || sc.SELinuxOptions == nil {
		t.Fatalf("expected SELinuxOptions to be set, got %+v", sc)
	}
	if sc.SELinuxOptions.User != "u" || sc.SELinuxOptions.Role != "r" || sc.SELinuxOptions.Type != "t" || sc.SELinuxOptions.Level != "l" {
		t.Errorf("unexpected seLinuxOptions: %+v", sc.SELinuxOptions)
	}
}

func TestParseSecurityContext_SELinuxOptions_EmptyObject_NoOp(t *testing.T) {
	sc, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"seLinuxOptions": map[string]any{},
		},
	})
	if err != nil {
		t.Fatalf("parseSecurityContext: %v", err)
	}
	if sc != nil {
		t.Errorf("expected nil securityContext for an all-empty seLinuxOptions object, got %+v", sc)
	}
}

func TestParseSecurityContext_AppArmorProfile_RuntimeDefault(t *testing.T) {
	sc, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"appArmorProfile": map[string]any{"type": "RuntimeDefault"},
		},
	})
	if err != nil {
		t.Fatalf("parseSecurityContext: %v", err)
	}
	if sc == nil || sc.AppArmorProfile == nil || string(sc.AppArmorProfile.Type) != "RuntimeDefault" {
		t.Errorf("unexpected appArmorProfile: %+v", sc)
	}
}

func TestParseSecurityContext_AppArmorProfile_Localhost_RequiresProfile(t *testing.T) {
	_, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"appArmorProfile": map[string]any{"type": "Localhost"},
		},
	})
	if err == nil {
		t.Fatal("expected error for Localhost appArmorProfile missing localhostProfile")
	}
}

func TestParseSecurityContext_AppArmorProfile_Localhost_WithProfile(t *testing.T) {
	sc, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"appArmorProfile": map[string]any{
				"type":             "Localhost",
				"localhostProfile": "profiles/my-profile.json",
			},
		},
	})
	if err != nil {
		t.Fatalf("parseSecurityContext: %v", err)
	}
	if sc == nil || sc.AppArmorProfile == nil || sc.AppArmorProfile.LocalhostProfile == nil || *sc.AppArmorProfile.LocalhostProfile != "profiles/my-profile.json" {
		t.Errorf("unexpected appArmorProfile: %+v", sc)
	}
}

func TestParseSecurityContext_AppArmorProfile_InvalidType_Error(t *testing.T) {
	_, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"appArmorProfile": map[string]any{"type": "Bogus"},
		},
	})
	if err == nil {
		t.Fatal("expected error for invalid appArmorProfile type")
	}
}

func TestParseSecurityContext_ProcMount_Default(t *testing.T) {
	sc, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{"procMount": "Default"},
	})
	if err != nil {
		t.Fatalf("parseSecurityContext: %v", err)
	}
	if sc == nil || sc.ProcMount == nil || *sc.ProcMount != corev1.DefaultProcMount {
		t.Errorf("unexpected procMount: %+v", sc)
	}
}

func TestParseSecurityContext_ProcMount_Unmasked(t *testing.T) {
	sc, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{"procMount": "Unmasked"},
	})
	if err != nil {
		t.Fatalf("parseSecurityContext: %v", err)
	}
	if sc == nil || sc.ProcMount == nil || *sc.ProcMount != corev1.UnmaskedProcMount {
		t.Errorf("unexpected procMount: %+v", sc)
	}
}

func TestParseSecurityContext_ProcMount_InvalidValue_Error(t *testing.T) {
	_, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{"procMount": "Bogus"},
	})
	if err == nil {
		t.Fatal("expected error for invalid procMount value")
	}
}

func TestSchemaFragments_ReservedParam(t *testing.T) {
	cases := []struct {
		name   string
		schema oam.PropertySchema
	}{
		{"schemaEnv", schemaEnv(true)},
		{"schemaEnvFrom", schemaEnvFrom(true)},
		{"schemaResources", schemaResources(true)},
		{"schemaLifecycle", schemaLifecycle(true)},
		{"schemaWorkingDir", schemaWorkingDir(true)},
		{"schemaProbes", schemaProbes(true)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.schema.PlatformReserved {
				t.Errorf("%s(true) did not set PlatformReserved", tc.name)
			}
		})
	}
	if schemaEnv(false).PlatformReserved {
		t.Error("schemaEnv(false) unexpectedly set PlatformReserved")
	}
}

func TestSchemaResources_QuantityFieldsHaveNoDeclaredType(t *testing.T) {
	// cpu/memory must stay untyped so a bare numeric value validates the same
	// way any other (additional) resource name already does — a declared
	// Type: PropertyTypeString would reject the numeric form parseResourceList
	// accepts and the README documents.
	quantity := schemaResources(false).Properties["requests"].Properties
	for _, name := range []string{"cpu", "memory"} {
		if got := quantity[name].Type; got != "" {
			t.Errorf("requests.%s: Type = %q, want unset", name, got)
		}
	}
}

func TestParseEnv_FieldRef_UnsupportedPath_Error(t *testing.T) {
	// status.phase is a real corev1 field but is not in the downward API's
	// env-var field path allow-list (it IS accepted for the downwardAPI
	// *volume* form, but not for an env var fieldRef) — admission rejects it.
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "BAD",
				"valueFrom": map[string]any{
					"fieldRef": map[string]any{"fieldPath": "status.phase"},
				},
			},
		},
	}
	if _, err := parseEnv(props); err == nil {
		t.Fatal("expected error for unsupported fieldRef.fieldPath")
	}
}

func TestParseEnv_FieldRef_SupportedPaths_Accepted(t *testing.T) {
	for _, path := range []string{
		"metadata.name", "metadata.namespace", "metadata.uid",
		"spec.nodeName", "spec.serviceAccountName",
		"status.hostIP", "status.hostIPs", "status.podIP", "status.podIPs",
	} {
		t.Run(path, func(t *testing.T) {
			props := map[string]any{
				"env": []any{
					map[string]any{
						"name":      "V",
						"valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": path}},
					},
				},
			}
			if _, err := parseEnv(props); err != nil {
				t.Errorf("parseEnv: unexpected error for fieldPath %q: %v", path, err)
			}
		})
	}
}

func TestParseEnv_FieldRef_LabelSubscript_Accepted(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "TIER",
				"valueFrom": map[string]any{
					"fieldRef": map[string]any{"fieldPath": "metadata.labels['app.kubernetes.io/component']"},
				},
			},
		},
	}
	vars, err := parseEnv(props)
	if err != nil {
		t.Fatalf("parseEnv: %v", err)
	}
	if vars[0].ValueFrom.FieldRef.FieldPath != "metadata.labels['app.kubernetes.io/component']" {
		t.Errorf("fieldPath = %q", vars[0].ValueFrom.FieldRef.FieldPath)
	}
}

func TestParseEnv_FieldRef_AnnotationSubscript_Accepted(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "NOTE",
				"valueFrom": map[string]any{
					"fieldRef": map[string]any{"fieldPath": "metadata.annotations['example.com/note']"},
				},
			},
		},
	}
	if _, err := parseEnv(props); err != nil {
		t.Fatalf("parseEnv: %v", err)
	}
}

func TestParseEnv_FieldRef_InvalidLabelKey_Error(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "BAD",
				"valueFrom": map[string]any{
					"fieldRef": map[string]any{"fieldPath": "metadata.labels['not a valid key!']"},
				},
			},
		},
	}
	if _, err := parseEnv(props); err == nil {
		t.Fatal("expected error for an invalid label key in a fieldRef subscript")
	}
}

func TestParseEnv_FieldRef_UnsupportedSubscriptBase_Error(t *testing.T) {
	// Only metadata.labels/metadata.annotations support the subscript form;
	// admission rejects a subscript on any other base field.
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "BAD",
				"valueFrom": map[string]any{
					"fieldRef": map[string]any{"fieldPath": "spec.nodeName['x']"},
				},
			},
		},
	}
	if _, err := parseEnv(props); err == nil {
		t.Fatal("expected error for a subscript on a field that does not support one")
	}
}

func TestParseEnv_ResourceFieldRef_ExtendedResource_Error(t *testing.T) {
	// Unlike a plain `resources` map, the downward API's resourceFieldRef
	// cannot project an arbitrary extended resource — only cpu/memory/
	// ephemeral-storage/hugepages-* are ever valid selectors.
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "BAD",
				"valueFrom": map[string]any{
					"resourceFieldRef": map[string]any{"resource": "limits.nvidia.com/gpu"},
				},
			},
		},
	}
	if _, err := parseEnv(props); err == nil {
		t.Fatal("expected error for an extended-resource resourceFieldRef.resource selector")
	}
}

func TestParseEnv_ResourceFieldRef_HugePages_Accepted(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "BAD",
				"valueFrom": map[string]any{
					"resourceFieldRef": map[string]any{"resource": "requests.hugepages-2Mi", "divisor": "1Mi"},
				},
			},
		},
	}
	if _, err := parseEnv(props); err != nil {
		t.Fatalf("parseEnv: unexpected error for a hugepages resourceFieldRef selector: %v", err)
	}
}

func TestParseEnv_ResourceFieldRef_NonCanonicalDivisor_Error(t *testing.T) {
	// Real admission restricts a standard resource's divisor to a small
	// canonical set of unit strings per resource family; "500m" is not one of
	// the memory family's accepted values even though it parses fine as a
	// quantity.
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "BAD",
				"valueFrom": map[string]any{
					"resourceFieldRef": map[string]any{"resource": "limits.memory", "divisor": "500m"},
				},
			},
		},
	}
	if _, err := parseEnv(props); err == nil {
		t.Fatal("expected error for a non-canonical resourceFieldRef.divisor")
	}
}

func TestParseEnv_ResourceFieldRef_NonStringDivisor_Error(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "BAD",
				"valueFrom": map[string]any{
					"resourceFieldRef": map[string]any{"resource": "limits.cpu", "divisor": 1000},
				},
			},
		},
	}
	if _, err := parseEnv(props); err == nil {
		t.Fatal("expected error for a non-string resourceFieldRef.divisor")
	}
}

func TestParseEnv_ResourceFieldRef_ZeroDivisor_Accepted(t *testing.T) {
	// A zero-valued divisor ("0") is numerically equal to the unset Quantity
	// zero value; real admission's own Cmp-to-zero check treats it as absent
	// rather than rejecting it — this is NOT the same as validating "0" as a
	// canonical unit string, which it is not.
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "BAD",
				"valueFrom": map[string]any{
					"resourceFieldRef": map[string]any{"resource": "limits.cpu", "divisor": "0"},
				},
			},
		},
	}
	if _, err := parseEnv(props); err != nil {
		t.Fatalf("parseEnv: unexpected error for a zero resourceFieldRef.divisor: %v", err)
	}
}

func TestParseEnvFrom_InvalidConfigMapName_Error(t *testing.T) {
	props := map[string]any{
		"envFrom": []any{
			map[string]any{"configMapRef": map[string]any{"name": "Not_A_Valid-Name"}},
		},
	}
	if _, err := parseEnvFrom(props); err == nil {
		t.Fatal("expected error for an invalid envFrom.configMapRef.name")
	}
}

func TestParseEnvFrom_InvalidSecretName_Error(t *testing.T) {
	props := map[string]any{
		"envFrom": []any{
			map[string]any{"secretRef": map[string]any{"name": "UPPERCASE"}},
		},
	}
	if _, err := parseEnvFrom(props); err == nil {
		t.Fatal("expected error for an invalid envFrom.secretRef.name")
	}
}

// TestParseEnvFrom_NonBoolOptional_Error regression-tests a review finding
// (launcher#284): the same mistyped-optional-silently-dropped bug fixed for
// secretKeyRef/configMapKeyRef/fileKeyRef above also applied to envFrom's
// configMapRef and secretRef.
func TestParseEnvFrom_NonBoolOptional_Error(t *testing.T) {
	for _, refKey := range []string{"configMapRef", "secretRef"} {
		t.Run(refKey, func(t *testing.T) {
			props := map[string]any{
				"envFrom": []any{
					map[string]any{refKey: map[string]any{"name": "n", "optional": "true"}},
				},
			}
			if _, err := parseEnvFrom(props); err == nil {
				t.Fatalf("expected error for a non-boolean envFrom.%s.optional", refKey)
			}
		})
	}
}

func TestParseSecurityContext_SeccompLocalhost_AbsolutePath_Error(t *testing.T) {
	_, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"seccompProfile": map[string]any{
				"type":             "Localhost",
				"localhostProfile": "/etc/profiles/my-profile.json",
			},
		},
	})
	if err == nil {
		t.Fatal("expected error for an absolute seccompProfile.localhostProfile")
	}
}

func TestParseSecurityContext_SeccompLocalhost_EscapingPath_Error(t *testing.T) {
	_, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"seccompProfile": map[string]any{
				"type":             "Localhost",
				"localhostProfile": "../../etc/passwd",
			},
		},
	})
	if err == nil {
		t.Fatal("expected error for a seccompProfile.localhostProfile containing \"..\"")
	}
}

func TestParseSecurityContext_RunAsUserZero_RunAsNonRootTrue_Error(t *testing.T) {
	// Builds and is admitted by the API server, but the kubelet's
	// verifyRunAsNonRoot check deterministically fails this combination at
	// container-start time — reject it here instead of shipping a workload
	// guaranteed never to start.
	_, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"runAsUser":    int64(0),
			"runAsNonRoot": true,
		},
	})
	if err == nil {
		t.Fatal("expected error for runAsUser=0 combined with runAsNonRoot=true")
	}
}

func TestParseSecurityContext_RunAsUserZero_RunAsNonRootFalse_Accepted(t *testing.T) {
	sc, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"runAsUser":    int64(0),
			"runAsNonRoot": false,
		},
	})
	if err != nil {
		t.Fatalf("parseSecurityContext: %v", err)
	}
	if sc.RunAsUser == nil || *sc.RunAsUser != 0 {
		t.Errorf("runAsUser = %v, want 0", sc.RunAsUser)
	}
}

func TestParseSecurityContext_RunAsUserNonZero_RunAsNonRootTrue_Accepted(t *testing.T) {
	sc, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"runAsUser":    int64(1000),
			"runAsNonRoot": true,
		},
	})
	if err != nil {
		t.Fatalf("parseSecurityContext: %v", err)
	}
	if sc.RunAsUser == nil || *sc.RunAsUser != 1000 {
		t.Errorf("runAsUser = %v, want 1000", sc.RunAsUser)
	}
}

func TestParseResources_UnqualifiedNonStandardName_Error(t *testing.T) {
	// An unqualified name passes validation.IsQualifiedName (it's a syntactically
	// valid token) but is not one Kubernetes reserves for container use unless
	// it's cpu/memory/ephemeral-storage or a hugepages-<size> name.
	_, err := parseResources(map[string]any{
		"requests": map[string]any{"foo": "1"},
	})
	if err == nil {
		t.Fatal("expected error for an unqualified non-standard resource name")
	}
}

func TestParseResources_QuotaAliasExtendedResource_Error(t *testing.T) {
	// A qualified name prefixed "requests." (without a "kubernetes.io/"
	// segment) collides with the ResourceQuota "requests.<name>" alias form
	// and is rejected on a container, mirroring IsExtendedResourceName.
	_, err := parseResources(map[string]any{
		"requests": map[string]any{"requests.example.com/foo": "1"},
	})
	if err == nil {
		t.Fatal("expected error for a requests.-prefixed extended resource name")
	}
}

func TestParseResources_HugePages_NonMultiple_Error(t *testing.T) {
	_, err := parseResources(map[string]any{
		"limits": map[string]any{"hugepages-2Mi": "3Mi"},
	})
	if err == nil {
		t.Fatal("expected error for a hugepages quantity not a multiple of the page size")
	}
}

func TestParseResources_HugePages_Multiple_Accepted(t *testing.T) {
	req, err := parseResources(map[string]any{
		"limits": map[string]any{"hugepages-2Mi": "4Mi"},
	})
	if err != nil {
		t.Fatalf("parseResources: %v", err)
	}
	if q, ok := req.Limits["hugepages-2Mi"]; !ok || q.Cmp(resource.MustParse("4Mi")) != 0 {
		t.Errorf("hugepages-2Mi = %v, ok=%v, want 4Mi", q, ok)
	}
}

func TestParseSecurityContext_SeccompProfile_MissingType_Error(t *testing.T) {
	_, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"seccompProfile": map[string]any{"localhostProfile": "profiles/my-profile.json"},
		},
	})
	if err == nil {
		t.Fatal("expected error for a seccompProfile object with no type")
	}
}

func TestParseSecurityContext_AppArmorProfile_MissingType_Error(t *testing.T) {
	_, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"appArmorProfile": map[string]any{"localhostProfile": "my-profile"},
		},
	})
	if err == nil {
		t.Fatal("expected error for an appArmorProfile object with no type")
	}
}

func TestParsePort_InvalidName_Error(t *testing.T) {
	cases := []string{"8080", "UPPER", "-leading-hyphen", "trailing-hyphen-", "a--b", ""}
	for _, name := range cases {
		if _, err := parsePort(name); err == nil {
			t.Errorf("parsePort(%q): expected error for an invalid port name", name)
		}
	}
}

func TestParsePort_ValidName_Accepted(t *testing.T) {
	port, err := parsePort("http")
	if err != nil {
		t.Fatalf("parsePort: %v", err)
	}
	if port.StrVal != "http" {
		t.Errorf("StrVal = %q, want http", port.StrVal)
	}
}

func TestParseProbe_HTTPGet_NamedPort_Invalid_Error(t *testing.T) {
	_, err := parseProbe(map[string]any{
		"httpGet": map[string]any{"path": "/healthz", "port": "8080"},
	}, "liveness")
	if err == nil {
		t.Fatal("expected error for an invalid httpGet named port")
	}
}

func TestParseLifecycleHandler_HTTPGet_NamedPort_Invalid_Error(t *testing.T) {
	_, err := parseLifecycleHandler(map[string]any{
		"httpGet": map[string]any{"path": "/started", "port": "bad_name"},
	})
	if err == nil {
		t.Fatal("expected error for an invalid lifecycle httpGet named port")
	}
}

func TestParseHTTPHeaders_NonObjectEntry_Error(t *testing.T) {
	_, err := parseHTTPHeaders(map[string]any{"httpHeaders": []any{"not-an-object"}}, "httpHeaders")
	if err == nil {
		t.Fatal("expected error for a non-object httpHeaders entry")
	}
}

func TestParseHTTPHeaders_MissingName_Error(t *testing.T) {
	_, err := parseHTTPHeaders(map[string]any{"httpHeaders": []any{map[string]any{"value": "x"}}}, "httpHeaders")
	if err == nil {
		t.Fatal("expected error for an httpHeaders entry with no name")
	}
}

func TestParseHTTPHeaders_InvalidName_Error(t *testing.T) {
	// A space is not a valid Go http header field name.
	_, err := parseHTTPHeaders(map[string]any{"httpHeaders": []any{map[string]any{"name": "X Auth", "value": "x"}}}, "httpHeaders")
	if err == nil {
		t.Fatal("expected error for an httpHeaders entry with an invalid name")
	}
}

func TestParseHTTPHeaders_NonStringValue_Error(t *testing.T) {
	_, err := parseHTTPHeaders(map[string]any{"httpHeaders": []any{map[string]any{"name": "Authorization", "value": float64(123)}}}, "httpHeaders")
	if err == nil {
		t.Fatal("expected error for an httpHeaders entry with a non-string value")
	}
}

func TestParseHTTPHeaders_MissingValue_DefaultsEmpty(t *testing.T) {
	headers, err := parseHTTPHeaders(map[string]any{"httpHeaders": []any{map[string]any{"name": "X-Flag"}}}, "httpHeaders")
	if err != nil {
		t.Fatalf("parseHTTPHeaders: %v", err)
	}
	if len(headers) != 1 || headers[0].Name != "X-Flag" || headers[0].Value != "" {
		t.Errorf("unexpected headers: %+v", headers)
	}
}

func TestParseHTTPHeaders_NonArray_Error(t *testing.T) {
	_, err := parseHTTPHeaders(map[string]any{"httpHeaders": map[string]any{"name": "X-Flag", "value": "x"}}, "httpHeaders")
	if err == nil {
		t.Fatal("expected error for a non-array httpHeaders value")
	}
}

func TestParseHTTPHeaders_Absent_NoError(t *testing.T) {
	headers, err := parseHTTPHeaders(map[string]any{}, "httpHeaders")
	if err != nil {
		t.Fatalf("parseHTTPHeaders: %v", err)
	}
	if headers != nil {
		t.Errorf("expected nil headers, got %+v", headers)
	}
}

func TestParseEnv_ResourceFieldRef_InvalidContainerName_Error(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "CPU_LIMIT",
				"valueFrom": map[string]any{
					"resourceFieldRef": map[string]any{"resource": "limits.cpu", "containerName": "bad/name"},
				},
			},
		},
	}
	if _, err := parseEnv(props); err == nil {
		t.Fatal("expected error for an invalid resourceFieldRef.containerName")
	}
}

func TestParseEnv_ResourceFieldRef_ValidContainerName_Accepted(t *testing.T) {
	props := map[string]any{
		"env": []any{
			map[string]any{
				"name": "CPU_LIMIT",
				"valueFrom": map[string]any{
					"resourceFieldRef": map[string]any{"resource": "limits.cpu", "containerName": "sidecar"},
				},
			},
		},
	}
	env, err := parseEnv(props)
	if err != nil {
		t.Fatalf("parseEnv: %v", err)
	}
	if env[0].ValueFrom.ResourceFieldRef.ContainerName != "sidecar" {
		t.Errorf("containerName = %q, want sidecar", env[0].ValueFrom.ResourceFieldRef.ContainerName)
	}
}

func TestParseResources_ExtendedResource_MismatchedRequestLimit_Error(t *testing.T) {
	_, err := parseResources(map[string]any{
		"requests": map[string]any{"nvidia.com/gpu": "1"},
		"limits":   map[string]any{"nvidia.com/gpu": "2"},
	})
	if err == nil {
		t.Fatal("expected error for a mismatched extended-resource request/limit")
	}
}

func TestParseResources_ExtendedResource_RequestWithoutLimit_Error(t *testing.T) {
	_, err := parseResources(map[string]any{
		"requests": map[string]any{"nvidia.com/gpu": "1"},
	})
	if err == nil {
		t.Fatal("expected error for an extended-resource request with no matching limit")
	}
}

func TestParseResources_HugePages_MismatchedRequestLimit_Error(t *testing.T) {
	_, err := parseResources(map[string]any{
		"requests": map[string]any{"hugepages-2Mi": "2Mi"},
		"limits":   map[string]any{"hugepages-2Mi": "4Mi"},
	})
	if err == nil {
		t.Fatal("expected error for a mismatched hugepages request/limit")
	}
}

func TestParseResources_HugePages_RequestWithoutLimit_Error(t *testing.T) {
	_, err := parseResources(map[string]any{
		"requests": map[string]any{"hugepages-2Mi": "2Mi"},
	})
	if err == nil {
		t.Fatal("expected error for a hugepages request with no matching limit")
	}
}

func TestParseResources_StandardResource_MismatchedRequestLimit_Accepted(t *testing.T) {
	// cpu/memory/ephemeral-storage remain overcommittable: a request lower
	// than the limit (or a request with no limit at all) is fine.
	req, err := parseResources(map[string]any{
		"requests": map[string]any{"cpu": "100m"},
		"limits":   map[string]any{"cpu": "500m"},
	})
	if err != nil {
		t.Fatalf("parseResources: %v", err)
	}
	if q, ok := req.Requests[corev1.ResourceCPU]; !ok || q.Cmp(resource.MustParse("100m")) != 0 {
		t.Errorf("cpu request = %v, ok=%v, want 100m", q, ok)
	}
}

// TestParseSecurityContext_NonBoolHardeningField_Error regression-tests a
// review finding (launcher#284): a quoted `"false"` for
// allowPrivilegeEscalation (or any of its three sibling hardening flags)
// used to fail the `.(bool)` type assertion silently, leaving the field unset
// so the container fell back to Kubernetes's permissive default while
// looking like the authored hardening request was honored.
func TestParseSecurityContext_NonBoolHardeningField_Error(t *testing.T) {
	for _, key := range []string{"runAsNonRoot", "readOnlyRootFilesystem", "allowPrivilegeEscalation", "privileged"} {
		t.Run(key, func(t *testing.T) {
			_, err := parseSecurityContext(map[string]any{
				"securityContext": map[string]any{
					key: "false",
				},
			})
			if err == nil {
				t.Fatalf("expected error for non-bool %s", key)
			}
		})
	}
}

// TestParseResources_StandardResource_RequestExceedsLimit_Error regression-
// tests a review finding (launcher#284): standardContainerResourceNames
// (cpu/memory/ephemeral-storage) skipped all request-vs-limit comparison, so
// a request greater than its limit (e.g. ephemeral-storage request 2Gi,
// limit 1Gi) was accepted even though real admission's
// validateResourceRequirements rejects request > limit for every resource,
// not just the non-overcommitable set.
func TestParseResources_StandardResource_RequestExceedsLimit_Error(t *testing.T) {
	_, err := parseResources(map[string]any{
		"requests": map[string]any{"ephemeral-storage": "2Gi"},
		"limits":   map[string]any{"ephemeral-storage": "1Gi"},
	})
	if err == nil {
		t.Fatal("expected error when a standard resource's request exceeds its limit")
	}
}

func TestParseResources_StandardResource_RequestBelowLimit_Accepted(t *testing.T) {
	_, err := parseResources(map[string]any{
		"requests": map[string]any{"ephemeral-storage": "1Gi"},
		"limits":   map[string]any{"ephemeral-storage": "2Gi"},
	})
	if err != nil {
		t.Fatalf("parseResources: %v", err)
	}
}

// TestParseProbe_HTTPGet_MissingPath_Accepted and its siblings below
// regression-test two rounds of the same review finding (launcher#284).
// Round 5's finding claimed a path must begin with "/" — false against the
// real validateHTTPGetAction, which has no leading-slash check — but
// investigating it surfaced that validateHTTPGetAction unconditionally
// rejects an empty path (field.Required), so a "missing path" requirement
// was added. Round 6's finding then disputed that requirement itself:
// k8s.io/kubernetes/pkg/apis/core/v1/defaults.go's SetDefaults_HTTPGetAction
// defaults an empty Path to "/" during the apiserver's decode/conversion
// step, strictly before validation runs — wired for both probe and
// lifecycle httpGet (zz_generated.defaults.go) — so a real cluster never
// sees the field.Required rejection for an omitted or empty path. Requiring
// it here was stricter than upstream, so it was reverted; only a
// present-but-wrong-type value (never a valid corev1.HTTPGetAction.Path
// value under any circumstance) is still an error.
func TestParseProbe_HTTPGet_MissingPath_Accepted(t *testing.T) {
	probe, err := parseProbe(map[string]any{
		"httpGet": map[string]any{"port": 8080},
	}, "liveness")
	if err != nil {
		t.Fatalf("parseProbe: %v", err)
	}
	if probe.HTTPGet.Path != "" {
		t.Errorf("Path = %q, want empty (left for upstream SetDefaults_HTTPGetAction to default to /)", probe.HTTPGet.Path)
	}
}

func TestParseProbe_HTTPGet_EmptyPath_Accepted(t *testing.T) {
	probe, err := parseProbe(map[string]any{
		"httpGet": map[string]any{"path": "", "port": 8080},
	}, "liveness")
	if err != nil {
		t.Fatalf("parseProbe: %v", err)
	}
	if probe.HTTPGet.Path != "" {
		t.Errorf("Path = %q, want empty", probe.HTTPGet.Path)
	}
}

func TestParseProbe_HTTPGet_NonStringPath_Error(t *testing.T) {
	_, err := parseProbe(map[string]any{
		"httpGet": map[string]any{"path": 123, "port": 8080},
	}, "liveness")
	if err == nil {
		t.Fatal("expected error for a non-string httpGet path")
	}
}

func TestParseProbe_HTTPGet_PathWithoutLeadingSlash_Accepted(t *testing.T) {
	// Real admission has no leading-slash requirement for httpGet.path — only
	// non-empty. "healthz" (no leading "/") must not be rejected.
	probe, err := parseProbe(map[string]any{
		"httpGet": map[string]any{"path": "healthz", "port": 8080},
	}, "liveness")
	if err != nil {
		t.Fatalf("parseProbe: %v", err)
	}
	if probe.HTTPGet.Path != "healthz" {
		t.Errorf("Path = %q, want healthz", probe.HTTPGet.Path)
	}
}

func TestParseLifecycleHandler_HTTPGet_MissingPath_Accepted(t *testing.T) {
	handler, err := parseLifecycleHandler(map[string]any{
		"httpGet": map[string]any{"port": 8080},
	})
	if err != nil {
		t.Fatalf("parseLifecycleHandler: %v", err)
	}
	if handler.HTTPGet.Path != "" {
		t.Errorf("Path = %q, want empty", handler.HTTPGet.Path)
	}
}

func TestParseLifecycleHandler_HTTPGet_NonStringPath_Error(t *testing.T) {
	_, err := parseLifecycleHandler(map[string]any{
		"httpGet": map[string]any{"path": 123, "port": 8080},
	})
	if err == nil {
		t.Fatal("expected error for a non-string lifecycle httpGet path")
	}
}

// TestParseLifecycleHandler_TCPSocket_WithOtherHandler_Error regression-tests
// a review finding (launcher#284): parseLifecycleHandler's handler-count loop
// only tracked httpGet/exec/sleep, so a hook pairing tcpSocket with a valid
// exec counted as "single handler" and silently built the exec handler
// alone, dropping the disallowed tcpSocket instead of rejecting the hook.
func TestParseLifecycleHandler_TCPSocket_WithOtherHandler_Error(t *testing.T) {
	_, err := parseLifecycleHandler(map[string]any{
		"tcpSocket": map[string]any{"port": 8080},
		"exec":      map[string]any{"command": []any{"true"}},
	})
	if err == nil {
		t.Fatal("expected error for tcpSocket paired with another lifecycle handler")
	}
}

func TestParseLifecycleHandler_TCPSocket_Alone_Error(t *testing.T) {
	_, err := parseLifecycleHandler(map[string]any{
		"tcpSocket": map[string]any{"port": 8080},
	})
	if err == nil {
		t.Fatal("expected error for a lifecycle handler containing only tcpSocket")
	}
}

// TestParseLifecycle_NonObjectPostStart_Error and its preStop sibling
// regression-test a review finding (launcher#284): `raw["postStart"].(map[string]any)`
// silently no-ops when the key is present with a non-object value (e.g. a
// string), so the build succeeded while silently dropping the authored hook.
func TestParseLifecycle_NonObjectPostStart_Error(t *testing.T) {
	_, err := parseLifecycle(map[string]any{
		"lifecycle": map[string]any{"postStart": "flush"},
	})
	if err == nil {
		t.Fatal("expected error for a non-object lifecycle.postStart")
	}
}

func TestParseLifecycle_NonObjectPreStop_Error(t *testing.T) {
	_, err := parseLifecycle(map[string]any{
		"lifecycle": map[string]any{"preStop": "flush"},
	})
	if err == nil {
		t.Fatal("expected error for a non-object lifecycle.preStop")
	}
}

func TestParseSecurityContext_BoolHardeningFields_Accepted(t *testing.T) {
	sc, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"runAsNonRoot":             true,
			"readOnlyRootFilesystem":   true,
			"allowPrivilegeEscalation": false,
			"privileged":               false,
		},
	})
	if err != nil {
		t.Fatalf("parseSecurityContext: %v", err)
	}
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Error("expected runAsNonRoot=true")
	}
	if sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
		t.Error("expected readOnlyRootFilesystem=true")
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Error("expected allowPrivilegeEscalation=false")
	}
	if sc.Privileged == nil || *sc.Privileged {
		t.Error("expected privileged=false")
	}
}

// TestParseSecurityContext_NonIntUIDGIDField_Error regression-tests a review
// finding (launcher#284, P1): a mistyped runAsUser (e.g. the quoted string
// "1000") previously fell through toInt64's ok=false silently, leaving the
// container to fall back to the image's own default user, which may be
// root — the opposite of the hardening the author asked for. runAsGroup gets
// the identical fix as a same-function sibling.
func TestParseSecurityContext_NonIntUIDGIDField_Error(t *testing.T) {
	for _, key := range []string{"runAsUser", "runAsGroup"} {
		t.Run(key, func(t *testing.T) {
			_, err := parseSecurityContext(map[string]any{
				"securityContext": map[string]any{key: "1000"},
			})
			if err == nil {
				t.Fatalf("expected error for non-integer securityContext.%s", key)
			}
		})
	}
}

func TestParseSecurityContext_UIDGIDFields_Accepted(t *testing.T) {
	sc, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{"runAsUser": 1000, "runAsGroup": 2000},
	})
	if err != nil {
		t.Fatalf("parseSecurityContext: %v", err)
	}
	if sc.RunAsUser == nil || *sc.RunAsUser != 1000 {
		t.Errorf("RunAsUser = %v, want 1000", sc.RunAsUser)
	}
	if sc.RunAsGroup == nil || *sc.RunAsGroup != 2000 {
		t.Errorf("RunAsGroup = %v, want 2000", sc.RunAsGroup)
	}
}

// TestParseSecurityContext_NonStringSELinuxField_Error regression-tests a
// review finding (launcher#284): a non-string seLinuxOptions.type (e.g. the
// number 123) previously fell through the failed type assertion silently; if
// it was the only field authored, the entire SELinux context was discarded.
// user/role/level get the identical fix as same-block siblings.
func TestParseSecurityContext_NonStringSELinuxField_Error(t *testing.T) {
	for _, key := range []string{"user", "role", "type", "level"} {
		t.Run(key, func(t *testing.T) {
			_, err := parseSecurityContext(map[string]any{
				"securityContext": map[string]any{
					"seLinuxOptions": map[string]any{key: 123},
				},
			})
			if err == nil {
				t.Fatalf("expected error for non-string seLinuxOptions.%s", key)
			}
		})
	}
}

func TestParseSecurityContext_SELinuxOptions_Accepted(t *testing.T) {
	sc, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"seLinuxOptions": map[string]any{
				"user": "u", "role": "r", "type": "t", "level": "l",
			},
		},
	})
	if err != nil {
		t.Fatalf("parseSecurityContext: %v", err)
	}
	if sc.SELinuxOptions == nil || sc.SELinuxOptions.User != "u" || sc.SELinuxOptions.Role != "r" ||
		sc.SELinuxOptions.Type != "t" || sc.SELinuxOptions.Level != "l" {
		t.Errorf("SELinuxOptions = %+v, want {u r t l}", sc.SELinuxOptions)
	}
}

// TestParseSecurityContext_NonArrayCapabilityField_Error regression-tests a
// review finding (launcher#284): authoring capabilities.add/drop as a scalar
// (e.g. `drop: ALL`) instead of an array previously fell through the failed
// type assertion silently, discarding the requested hardening entirely.
func TestParseSecurityContext_NonArrayCapabilityField_Error(t *testing.T) {
	for _, key := range []string{"add", "drop"} {
		t.Run(key, func(t *testing.T) {
			_, err := parseSecurityContext(map[string]any{
				"securityContext": map[string]any{
					"capabilities": map[string]any{key: "ALL"},
				},
			})
			if err == nil {
				t.Fatalf("expected error for non-array capabilities.%s", key)
			}
		})
	}
}

func TestParseSecurityContext_NonStringCapabilityElement_Error(t *testing.T) {
	_, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"capabilities": map[string]any{"add": []any{123}},
		},
	})
	if err == nil {
		t.Fatal("expected error for a non-string capabilities.add element")
	}
}

func TestParseSecurityContext_NonObjectCapabilities_Error(t *testing.T) {
	_, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"capabilities": "ALL",
		},
	})
	if err == nil {
		t.Fatal("expected error for a non-object capabilities value")
	}
}

func TestParseSecurityContext_NonObjectNestedProfile_Error(t *testing.T) {
	for _, field := range []string{"seccompProfile", "seLinuxOptions", "appArmorProfile"} {
		t.Run(field, func(t *testing.T) {
			_, err := parseSecurityContext(map[string]any{
				"securityContext": map[string]any{
					field: "RuntimeDefault",
				},
			})
			if err == nil {
				t.Fatalf("expected error for a non-object %s value", field)
			}
		})
	}
}

func TestParseSecurityContext_EmptyCapabilityElement_Skipped(t *testing.T) {
	sc, err := parseSecurityContext(map[string]any{
		"securityContext": map[string]any{
			"capabilities": map[string]any{"add": []any{"", "NET_BIND_SERVICE"}},
		},
	})
	if err != nil {
		t.Fatalf("parseSecurityContext: %v", err)
	}
	if len(sc.Capabilities.Add) != 1 || sc.Capabilities.Add[0] != "NET_BIND_SERVICE" {
		t.Errorf("Capabilities.Add = %+v, want [NET_BIND_SERVICE]", sc.Capabilities.Add)
	}
}

// TestParseProbe_NonIntNumericField_Error regression-tests a review finding
// (launcher#284, terminationGracePeriodSeconds specifically): every optional
// numeric probe field shared the same toInt64/toInt32 silent-skip idiom, so a
// mistyped value was treated as though the field were absent — e.g. a
// mistyped terminationGracePeriodSeconds silently fell back to the pod-level
// grace period instead of the explicitly requested probe override. All six
// fields share the identical helper and are tested together as same-function
// siblings.
func TestParseProbe_NonIntNumericField_Error(t *testing.T) {
	for _, key := range []string{
		"initialDelaySeconds", "periodSeconds", "timeoutSeconds",
		"successThreshold", "failureThreshold", "terminationGracePeriodSeconds",
	} {
		t.Run(key, func(t *testing.T) {
			_, err := parseProbe(map[string]any{
				"tcpSocket": map[string]any{"port": 8080},
				key:         "30",
			}, "liveness")
			if err == nil {
				t.Fatalf("expected error for non-integer probe.%s", key)
			}
		})
	}
}
