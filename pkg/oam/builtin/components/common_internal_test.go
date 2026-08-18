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
