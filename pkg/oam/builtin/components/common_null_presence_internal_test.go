package components

import (
	"reflect"
	"strings"
	"testing"
)

// go-kure/launcher#394 names six parsers in its own scope list and states two
// acceptance criteria over them:
//
//	A document authoring any of the affected keys as an explicit null builds
//	exactly as one omitting the key.
//	A present-but-wrong-type value still errors, with the same message it has now.
//	One test per affected parser pins both, so removing the carve-out or the type
//	check each turns the suite red.
//
// Those six read their property with a bare comma-ok lookup rather than through
// one of the typed field helpers, so folding authoredValue into the helpers did
// not reach them: `envFrom:` with nothing after it was still rejected as
// "must be an array, got <nil>" while `replicas:` had become absence. This file
// is the missing per-parser coverage, and it is deliberately two-sided — a test
// that only pinned the null case would pass just as well if the type check were
// deleted outright, which is the failure the second half exists to catch.
//
// nullValues covers every shape isExplicitNull recognises, not just the untyped
// nil a hand-written document produces: a lowering rule assembled in Go emits a
// TYPED nil for an unset optional map or slice, and that is a non-nil interface
// holding a nil value. A test using only `nil` would pass against a `v == nil`
// implementation and prove nothing about the case that actually occurs.
//
// One subtest in this table does NOT discriminate, and saying so is the point of
// this paragraph. Reverting parseVolumes to a bare `props["volumes"]` was run as
// a mutation probe with the outcome written down first; four of its five null
// subtests went red as predicted, and `typed nil list` stayed GREEN — because
// `[]any(nil)` inside an `any` satisfies a `.([]any)` assertion with ok=true and
// a nil slice, so the parser reads the null as an authored empty list and
// happens to produce the same result. That is the silent value-substitution the
// PR body names, not a passing case; the subtest is kept because on the
// map-shaped and scalar parsers it does discriminate, and a per-helper table
// with a hole in it is more honest than one trimmed to only its red cells.
// Do not read a green here as evidence for the list-shaped parsers.
func nullValues() []struct {
	name string
	val  any
} {
	var (
		nilMap   map[string]any
		nilList  []any
		nilSlice []string
		nilPtr   *int32
	)
	return []struct {
		name string
		val  any
	}{
		{"untyped nil", nil},
		{"typed nil map", nilMap},
		{"typed nil list", nilList},
		{"typed nil slice", nilSlice},
		{"typed nil pointer", nilPtr},
	}
}

func TestParseEnvFrom_NullIsOmission(t *testing.T) {
	for _, tc := range nullValues() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseEnvFrom(map[string]any{"envFrom": tc.val})
			if err != nil {
				t.Fatalf("parseEnvFrom(envFrom: null) = error %v, want nil", err)
			}
			if got != nil {
				t.Errorf("parseEnvFrom(envFrom: null) = %v, want nil", got)
			}
		})
	}

	t.Run("wrong type still errors", func(t *testing.T) {
		_, err := parseEnvFrom(map[string]any{"envFrom": map[string]any{"name": "cm"}})
		if err == nil {
			t.Fatal("parseEnvFrom(envFrom: <object>) = nil error, want a type error")
		}
		if !strings.Contains(err.Error(), "must be an array") {
			t.Errorf("error = %q, want it to name the expected type", err)
		}
	})
}

func TestParseProbes_NullIsOmission(t *testing.T) {
	for _, tc := range nullValues() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseProbes(map[string]any{"probes": tc.val}, true, "web")
			if err != nil {
				t.Fatalf("parseProbes(probes: null) = error %v, want nil", err)
			}
			if got.Readiness != nil || got.Liveness != nil || got.Startup != nil {
				t.Errorf("parseProbes(probes: null) = %+v, want the zero ProbeConfig", got)
			}
		})
	}

	// The individual probe keys are named separately in #394's scope list, so
	// they are pinned separately: `probes: {readiness: null}` is an authored
	// probes block with one probe unset, which is a different document from
	// `probes: null` and reaches a different comma-ok read.
	t.Run("individual probe null is omission", func(t *testing.T) {
		got, err := parseProbes(map[string]any{"probes": map[string]any{"readiness": nil}}, true, "web")
		if err != nil {
			t.Fatalf("parseProbes(probes.readiness: null) = error %v, want nil", err)
		}
		if got.Readiness != nil {
			t.Errorf("readiness = %+v, want nil", got.Readiness)
		}
	})

	t.Run("wrong type still errors", func(t *testing.T) {
		_, err := parseProbes(map[string]any{"probes": true}, true, "web")
		if err == nil {
			t.Fatal("parseProbes(probes: true) = nil error, want a type error")
		}
		if !strings.Contains(err.Error(), "must be an object") {
			t.Errorf("error = %q, want it to name the expected type", err)
		}
	})

	t.Run("wrong type on an individual probe still errors", func(t *testing.T) {
		_, err := parseProbes(map[string]any{"probes": map[string]any{"liveness": true}}, true, "web")
		if err == nil {
			t.Fatal("parseProbes(probes.liveness: true) = nil error, want a type error")
		}
	})
}

func TestParseLifecycle_NullIsOmission(t *testing.T) {
	for _, tc := range nullValues() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseLifecycle(map[string]any{"lifecycle": tc.val}, true, "web")
			if err != nil {
				t.Fatalf("parseLifecycle(lifecycle: null) = error %v, want nil", err)
			}
			if got != nil {
				t.Errorf("parseLifecycle(lifecycle: null) = %+v, want nil", got)
			}
		})
	}

	t.Run("individual hook null is omission", func(t *testing.T) {
		got, err := parseLifecycle(map[string]any{"lifecycle": map[string]any{"postStart": nil}}, true, "web")
		if err != nil {
			t.Fatalf("parseLifecycle(lifecycle.postStart: null) = error %v, want nil", err)
		}
		if got != nil && got.PostStart != nil {
			t.Errorf("postStart = %+v, want nil", got.PostStart)
		}
	})

	// The carve-out control. parseLifecycleHandler's httpGet/exec/sleep loop and
	// its tcpSocket check were deliberately NOT routed through authoredValue,
	// and this subtest is what stops a later sweep "finishing the job" and
	// reversing that. There the rule being enforced is presence itself: an
	// authored-but-empty tcpSocket read as absence would vanish and let a valid
	// sibling handler build silently, which is the same silent drop the rest of
	// this parser exists to prevent. Absence and authored-null are different
	// documents for these four keys, and only for these four.
	t.Run("handler key authored as null is still refused", func(t *testing.T) {
		_, err := parseLifecycle(map[string]any{
			"lifecycle": map[string]any{"preStop": map[string]any{"tcpSocket": nil}},
		}, true, "web")
		if err == nil {
			t.Fatal("parseLifecycle(preStop.tcpSocket: null) = nil error, want the tcpSocket refusal")
		}
	})

	t.Run("wrong type still errors", func(t *testing.T) {
		_, err := parseLifecycle(map[string]any{"lifecycle": []any{"postStart"}}, true, "web")
		if err == nil {
			t.Fatal("parseLifecycle(lifecycle: <array>) = nil error, want a type error")
		}
		if !strings.Contains(err.Error(), "must be an object") {
			t.Errorf("error = %q, want it to name the expected type", err)
		}
	})
}

func TestParseSecurityContext_NullIsOmission(t *testing.T) {
	for _, tc := range nullValues() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSecurityContext(map[string]any{"securityContext": tc.val})
			if err != nil {
				t.Fatalf("parseSecurityContext(securityContext: null) = error %v, want nil", err)
			}
			if got != nil {
				t.Errorf("parseSecurityContext(securityContext: null) = %+v, want nil", got)
			}
		})
	}

	// capabilities is named in #394's scope list in its own right.
	t.Run("nested capabilities null is omission", func(t *testing.T) {
		got, err := parseSecurityContext(map[string]any{
			"securityContext": map[string]any{"runAsNonRoot": true, "capabilities": nil},
		})
		if err != nil {
			t.Fatalf("parseSecurityContext(capabilities: null) = error %v, want nil", err)
		}
		if got == nil {
			t.Fatal("securityContext = nil, want the runAsNonRoot value to survive")
		}
		if got.Capabilities != nil {
			t.Errorf("capabilities = %+v, want nil", got.Capabilities)
		}
	})

	t.Run("wrong type still errors", func(t *testing.T) {
		_, err := parseSecurityContext(map[string]any{"securityContext": "privileged"})
		if err == nil {
			t.Fatal("parseSecurityContext(securityContext: <string>) = nil error, want a type error")
		}
		if !strings.Contains(err.Error(), "must be an object") {
			t.Errorf("error = %q, want it to name the expected type", err)
		}
	})
}

func TestParseVolumes_NullIsOmission(t *testing.T) {
	for _, tc := range nullValues() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseVolumes(map[string]any{"volumes": tc.val})
			if err != nil {
				t.Fatalf("parseVolumes(volumes: null) = error %v, want nil", err)
			}
			if len(got.Volumes) != 0 || len(got.Mounts) != 0 {
				t.Errorf("parseVolumes(volumes: null) = %+v, want no volumes and no mounts", got)
			}
		})
	}

	// #394 gives this exact pair as the distinction the fix must preserve:
	// `volumes:` is absence, `volumes: {name: data}` is a wrong type.
	t.Run("wrong type still errors", func(t *testing.T) {
		_, err := parseVolumes(map[string]any{"volumes": map[string]any{"name": "data"}})
		if err == nil {
			t.Fatal("parseVolumes(volumes: <object>) = nil error, want a type error")
		}
		if !strings.Contains(err.Error(), "must be an array") {
			t.Errorf("error = %q, want it to name the expected type", err)
		}
	})
}

func TestParseAccessModes_NullIsOmission(t *testing.T) {
	for _, tc := range nullValues() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAccessModes(map[string]any{"accessModes": tc.val})
			if err != nil {
				t.Fatalf("parseAccessModes(accessModes: null) = error %v, want nil", err)
			}
			// Absence here is not the empty slice: this parser defaults, so
			// "builds exactly as one omitting the key" means the default.
			if len(got) != 1 || got[0] != "ReadWriteOnce" {
				t.Errorf("parseAccessModes(accessModes: null) = %v, want the ReadWriteOnce default", got)
			}
		})
	}

	t.Run("wrong type still errors", func(t *testing.T) {
		_, err := parseAccessModes(map[string]any{"accessModes": "ReadWriteOnce"})
		if err == nil {
			t.Fatal("parseAccessModes(accessModes: <string>) = nil error, want a type error")
		}
	})
}

// The six parsers below are a DIFFERENT six from the ones above, and they are
// here because of how they arrived rather than because #394 named them.
//
// While this branch was open, main landed its own null fix for env, command,
// args, initContainers, sidecars and affinity, shaped as five optional*
// wrappers ("parse*Field with an explicit null read as omission"). This branch
// had put the same null handling INSIDE parseObjectList/parseStringList/
// parseObjectField instead, which makes each wrapper an exact no-op forward — a
// guard that can no longer fire, because its delegate already returns absence
// for a null. Rebasing dropped the wrappers (they live in a region this branch
// rewrote) while keeping main's six call sites, so the merged tree referenced
// five functions that no longer existed. The call sites were re-pointed at the
// delegates rather than the wrappers being restored.
//
// That substitution needed proof, and the inherited suite could not give it: of
// the six keys, only `env` had a null case at all. A green run therefore said
// nothing about the other five. These tests are that missing half — with the
// substitution wrong, five of them go red.
//
// Each asserts #394's acceptance criterion directly rather than against a
// hand-written zero value: parsing `key: null` must produce exactly what parsing
// a document WITHOUT the key produces. The absent-key result is computed here,
// so it cannot drift from the parser.
//
// The typed-nil-list caveat in this file's opening comment applies here too: on
// the list-shaped parsers that subtest does not discriminate, because []any(nil)
// satisfies a .([]any) assertion and yields an empty slice.
func TestInheritedParsers_NullIsOmission(t *testing.T) {
	cases := []struct {
		key   string
		parse func(map[string]any) (any, error)
	}{
		{"env", func(p map[string]any) (any, error) { return parseEnv(p) }},
		{"command", func(p map[string]any) (any, error) { return parseCommand(p) }},
		{"args", func(p map[string]any) (any, error) { return parseArgs(p) }},
		{"initContainers", func(p map[string]any) (any, error) { return parseInitContainers(p) }},
		{"sidecars", func(p map[string]any) (any, error) { return parseSidecars(p) }},
		{"affinity", func(p map[string]any) (any, error) { return parseAffinity(p) }},
	}

	for _, tc := range cases {
		absent, err := tc.parse(map[string]any{})
		if err != nil {
			t.Fatalf("parse(%s absent) = error %v, want nil", tc.key, err)
		}
		for _, nv := range nullValues() {
			t.Run(tc.key+"/"+nv.name, func(t *testing.T) {
				got, err := tc.parse(map[string]any{tc.key: nv.val})
				if err != nil {
					t.Fatalf("parse(%s: null) = error %v, want nil — a null must read as absence, not as a wrong type", tc.key, err)
				}
				if !reflect.DeepEqual(got, absent) {
					t.Errorf("parse(%s: null) = %#v, want the absent-key result %#v", tc.key, got, absent)
				}
			})
		}
	}
}

// TestParseHTTPHeaders_NullIsOmission closes a gap the sweep to authoredValue
// missed: parseHTTPHeaders (shared by the probe and lifecycle httpGet
// handlers) still reads both its own presence and its per-entry "value" via a
// bare comma-ok lookup, so `httpHeaders: null` was rejected as "must be an
// array, got <nil>" instead of reading as absence, and a per-entry
// `value: null` was rejected instead of defaulting to "".
func TestParseHTTPHeaders_NullIsOmission(t *testing.T) {
	for _, tc := range nullValues() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseHTTPHeaders(map[string]any{"httpHeaders": tc.val}, "httpHeaders")
			if err != nil {
				t.Fatalf("parseHTTPHeaders(httpHeaders: null) = error %v, want nil", err)
			}
			if got != nil {
				t.Errorf("parseHTTPHeaders(httpHeaders: null) = %v, want nil", got)
			}
		})
	}

	t.Run("entry value null is omission", func(t *testing.T) {
		got, err := parseHTTPHeaders(map[string]any{"httpHeaders": []any{
			map[string]any{"name": "X-Test", "value": nil},
		}}, "httpHeaders")
		if err != nil {
			t.Fatalf("parseHTTPHeaders(value: null) = error %v, want nil", err)
		}
		if len(got) != 1 || got[0].Value != "" {
			t.Errorf("parseHTTPHeaders(value: null) = %+v, want one header with Value \"\"", got)
		}
	})

	t.Run("wrong type still errors", func(t *testing.T) {
		_, err := parseHTTPHeaders(map[string]any{"httpHeaders": "x"}, "httpHeaders")
		if err == nil {
			t.Fatal("parseHTTPHeaders(httpHeaders: <string>) = nil error, want a type error")
		}
	})
}

// TestParsePodSecurityContext_NullIsOmission closes a gap the sweep missed at
// the pod level: unlike their container-level parseSecurityContext siblings,
// supplementalGroups, seccompProfile, seLinuxOptions and appArmorProfile
// still read their presence via a bare comma-ok lookup, so e.g.
// `podSecurityContext: {seccompProfile: null}` errored instead of reading as
// absent, even though the container-level `securityContext.seccompProfile`
// already handled null correctly.
func TestParsePodSecurityContext_NullIsOmission(t *testing.T) {
	for _, tc := range nullValues() {
		t.Run("supplementalGroups/"+tc.name, func(t *testing.T) {
			got, err := parsePodSecurityContext(map[string]any{"supplementalGroups": tc.val}, "podSecurityContext")
			if err != nil {
				t.Fatalf("parsePodSecurityContext(supplementalGroups: null) = error %v, want nil", err)
			}
			if got != nil && got.SupplementalGroups != nil {
				t.Errorf("SupplementalGroups = %v, want nil", got.SupplementalGroups)
			}
		})
		t.Run("seccompProfile/"+tc.name, func(t *testing.T) {
			got, err := parsePodSecurityContext(map[string]any{"seccompProfile": tc.val}, "podSecurityContext")
			if err != nil {
				t.Fatalf("parsePodSecurityContext(seccompProfile: null) = error %v, want nil", err)
			}
			if got != nil && got.SeccompProfile != nil {
				t.Errorf("SeccompProfile = %+v, want nil", got.SeccompProfile)
			}
		})
		t.Run("seLinuxOptions/"+tc.name, func(t *testing.T) {
			got, err := parsePodSecurityContext(map[string]any{"seLinuxOptions": tc.val}, "podSecurityContext")
			if err != nil {
				t.Fatalf("parsePodSecurityContext(seLinuxOptions: null) = error %v, want nil", err)
			}
			if got != nil && got.SELinuxOptions != nil {
				t.Errorf("SELinuxOptions = %+v, want nil", got.SELinuxOptions)
			}
		})
		t.Run("appArmorProfile/"+tc.name, func(t *testing.T) {
			got, err := parsePodSecurityContext(map[string]any{"appArmorProfile": tc.val}, "podSecurityContext")
			if err != nil {
				t.Fatalf("parsePodSecurityContext(appArmorProfile: null) = error %v, want nil", err)
			}
			if got != nil && got.AppArmorProfile != nil {
				t.Errorf("AppArmorProfile = %+v, want nil", got.AppArmorProfile)
			}
		})
	}

	t.Run("wrong type still errors", func(t *testing.T) {
		for _, key := range []string{"supplementalGroups", "seccompProfile", "seLinuxOptions", "appArmorProfile"} {
			t.Run(key, func(t *testing.T) {
				if _, err := parsePodSecurityContext(map[string]any{key: "x"}, "podSecurityContext"); err == nil {
					t.Fatalf("parsePodSecurityContext(%s: <string>) = nil error, want a type error", key)
				}
			})
		}
	})
}

// TestParsePodSpec_PodResources_NullIsOmission closes a gap in the
// podResources.requests/limits type guard, which still reads each key via a
// bare comma-ok lookup ahead of parseResources: `podResources: {requests:
// null}` errored ("must be an object, got <nil>") instead of reading as an
// absent requests block.
func TestParsePodSpec_PodResources_NullIsOmission(t *testing.T) {
	for _, key := range []string{"requests", "limits"} {
		for _, tc := range nullValues() {
			t.Run(key+"/"+tc.name, func(t *testing.T) {
				cfg, err := parsePodSpec(map[string]any{
					"podResources": map[string]any{key: tc.val},
				}, false)
				if err != nil {
					t.Fatalf("parsePodSpec(podResources.%s: null) = error %v, want nil", key, err)
				}
				if cfg.Resources != nil {
					t.Errorf("Resources = %+v, want nil", cfg.Resources)
				}
			})
		}
	}

	t.Run("wrong type still errors", func(t *testing.T) {
		_, err := parsePodSpec(map[string]any{
			"podResources": map[string]any{"requests": "x"},
		}, false)
		if err == nil {
			t.Fatal("parsePodSpec(podResources.requests: <string>) = nil error, want a type error")
		}
	})
}

// TestParsePodDNSConfig_OptionValue_NullIsOmission closes a gap in the DNS
// option value lookup, which still reads via a bare comma-ok lookup:
// `options: [{name: ndots, value: null}]` errored instead of leaving Value
// nil, the same result an omitted value key already produces.
func TestParsePodDNSConfig_OptionValue_NullIsOmission(t *testing.T) {
	for _, tc := range nullValues() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePodDNSConfig(map[string]any{
				"options": []any{map[string]any{"name": "ndots", "value": tc.val}},
			}, "dnsConfig")
			if err != nil {
				t.Fatalf("parsePodDNSConfig(options[0].value: null) = error %v, want nil", err)
			}
			if len(got.Options) != 1 || got.Options[0].Value != nil {
				t.Errorf("Options = %+v, want one option with a nil Value", got.Options)
			}
		})
	}

	t.Run("wrong type still errors", func(t *testing.T) {
		_, err := parsePodDNSConfig(map[string]any{
			"options": []any{map[string]any{"name": "ndots", "value": 1}},
		}, "dnsConfig")
		if err == nil {
			t.Fatal("parsePodDNSConfig(options[0].value: <int>) = nil error, want a type error")
		}
	})
}

// TestParseStorageClassField_NullIsOmission closes a gap in the PVC
// storageClass lookup, which still reads via a bare comma-ok lookup:
// `storageClass: null` errored ("must be a string, got <nil>") instead of
// producing the same (value="", explicitEmpty=false) result an omitted key
// produces. explicitEmpty distinguishes an authored "" (request no
// StorageClass) from absence (use the cluster default), and a null must land
// on the absence side of that distinction, not the authored side.
func TestParseStorageClassField_NullIsOmission(t *testing.T) {
	absentValue, absentExplicit, err := parseStorageClassField(map[string]any{}, "volumes[0]")
	if err != nil {
		t.Fatalf("parseStorageClassField(absent) = error %v, want nil", err)
	}
	for _, tc := range nullValues() {
		t.Run(tc.name, func(t *testing.T) {
			got, explicitEmpty, err := parseStorageClassField(map[string]any{"storageClass": tc.val}, "volumes[0]")
			if err != nil {
				t.Fatalf("parseStorageClassField(storageClass: null) = error %v, want nil", err)
			}
			if got != absentValue || explicitEmpty != absentExplicit {
				t.Errorf("parseStorageClassField(storageClass: null) = (%q, %v), want the absent-key result (%q, %v)", got, explicitEmpty, absentValue, absentExplicit)
			}
		})
	}

	t.Run("explicit empty string is still distinguished", func(t *testing.T) {
		got, explicitEmpty, err := parseStorageClassField(map[string]any{"storageClass": ""}, "volumes[0]")
		if err != nil {
			t.Fatalf("parseStorageClassField(storageClass: \"\") = error %v, want nil", err)
		}
		if got != "" || !explicitEmpty {
			t.Errorf("parseStorageClassField(storageClass: \"\") = (%q, %v), want (\"\", true)", got, explicitEmpty)
		}
	})

	t.Run("wrong type still errors", func(t *testing.T) {
		_, _, err := parseStorageClassField(map[string]any{"storageClass": 1}, "volumes[0]")
		if err == nil {
			t.Fatal("parseStorageClassField(storageClass: <int>) = nil error, want a type error")
		}
	})
}

// TestParseEnv_ValueFrom_TypedNilMap closes a gap in the valueFrom presence
// check, which uses a bare type-assertion comma-ok (`envMap["valueFrom"].
// (map[string]any)`) instead of authoredValue/isExplicitNull. A TYPED nil
// map[string]any — exactly what a Go-constructed lowering rule produces for
// an unset optional map, per this file's isExplicitNull doc comment — still
// satisfies that assertion with ok=true, so it was misread as an authored
// valueFrom and tripped the mutually-exclusive-fields error even though
// `value` was also set.
func TestParseEnv_ValueFrom_TypedNilMap(t *testing.T) {
	var nilMap map[string]any
	got, err := parseEnv(map[string]any{"env": []any{
		map[string]any{"name": "X", "value": "ok", "valueFrom": nilMap},
	}})
	if err != nil {
		t.Fatalf("parseEnv(valueFrom: typed nil map) = error %v, want nil", err)
	}
	if len(got) != 1 || got[0].Value != "ok" || got[0].ValueFrom != nil {
		t.Errorf("parseEnv(valueFrom: typed nil map) = %+v, want one var with Value \"ok\" and nil ValueFrom", got)
	}
}

// The other half of #394's acceptance: a present-but-wrong-type value must still
// error. Without this, deleting the type check outright would leave the table
// above just as green.
func TestInheritedParsers_WrongTypeStillErrors(t *testing.T) {
	cases := []struct {
		key   string
		bad   any
		parse func(map[string]any) (any, error)
	}{
		{"env", map[string]any{"name": "X"}, func(p map[string]any) (any, error) { return parseEnv(p) }},
		{"command", "sh", func(p map[string]any) (any, error) { return parseCommand(p) }},
		{"args", "-c", func(p map[string]any) (any, error) { return parseArgs(p) }},
		{"initContainers", map[string]any{"name": "c"}, func(p map[string]any) (any, error) { return parseInitContainers(p) }},
		{"sidecars", map[string]any{"name": "c"}, func(p map[string]any) (any, error) { return parseSidecars(p) }},
		{"affinity", []any{"nodeAffinity"}, func(p map[string]any) (any, error) { return parseAffinity(p) }},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			if _, err := tc.parse(map[string]any{tc.key: tc.bad}); err == nil {
				t.Fatalf("parse(%s: %T) = nil error, want a type error", tc.key, tc.bad)
			}
		})
	}
}
