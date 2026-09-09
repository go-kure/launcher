package components

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// The six parsers below used to read their property with a bare comma-ok type
// assertion and, on failure, return the zero value with a nil error — so a
// property authored with the wrong container type was indistinguishable from an
// absent one and was silently discarded (go-kure/launcher#423).
//
// Every case is paired: the mismatch must be rejected AND the absent key must
// still parse as absent. The pairing is the point. Before the fix the two
// inputs produced the identical result, so a test covering only absence passes
// against the defect as readily as against the fix and proves nothing.

func TestParseInitContainers_WrongContainerType(t *testing.T) {
	// A mapping where the schema wants a list. Authoring `initContainers:` with
	// nested keys instead of `- name: …` items is the plausible mistake.
	_, err := parseInitContainers(map[string]any{
		"initContainers": map[string]any{"name": "setup", "image": "busybox:1"},
	})
	if err == nil {
		t.Fatal("expected an error for initContainers authored as a mapping, got nil")
	}
	if !strings.Contains(err.Error(), "initContainers") {
		t.Errorf("error does not name the property: %v", err)
	}
}

func TestParseInitContainers_AbsentIsAbsent(t *testing.T) {
	out, err := parseInitContainers(map[string]any{"image": "busybox:1"})
	if err != nil {
		t.Fatalf("parseInitContainers: %v", err)
	}
	if out != nil {
		t.Errorf("absent initContainers parsed as %+v, want nil", out)
	}
}

func TestParseSidecars_WrongContainerType(t *testing.T) {
	_, err := parseSidecars(map[string]any{
		"sidecars": map[string]any{"name": "proxy", "image": "envoy:1"},
	})
	if err == nil {
		t.Fatal("expected an error for sidecars authored as a mapping, got nil")
	}
	if !strings.Contains(err.Error(), "sidecars") {
		t.Errorf("error does not name the property: %v", err)
	}
}

func TestParseSidecars_AbsentIsAbsent(t *testing.T) {
	out, err := parseSidecars(map[string]any{"image": "envoy:1"})
	if err != nil {
		t.Fatalf("parseSidecars: %v", err)
	}
	if out != nil {
		t.Errorf("absent sidecars parsed as %+v, want nil", out)
	}
}

func TestParseAffinity_WrongContainerType(t *testing.T) {
	// A list where the schema wants an object. The sibling parsers on the
	// adjacent call-site lines (parseRawAffinity, parseTopologySpreadConstraints)
	// already rejected this shape, so the inconsistency was visible between two
	// properties sitting next to each other in the same document.
	_, err := parseAffinity(map[string]any{
		"affinity": []any{map[string]any{"topologyKey": "kubernetes.io/hostname"}},
	})
	if err == nil {
		t.Fatal("expected an error for affinity authored as a list, got nil")
	}
	if !strings.Contains(err.Error(), "affinity") {
		t.Errorf("error does not name the property: %v", err)
	}
}

func TestParseAffinity_AbsentIsAbsent(t *testing.T) {
	cfg, err := parseAffinity(map[string]any{"replicas": 3})
	if err != nil {
		t.Fatalf("parseAffinity: %v", err)
	}
	// Absent must stay the zero AffinityConfig, not the defaulted one the
	// present path builds (TopologyKey "kubernetes.io/hostname",
	// PodAntiAffinityType "preferred").
	if !reflect.DeepEqual(cfg, AffinityConfig{}) {
		t.Errorf("absent affinity parsed as %+v, want the zero value", cfg)
	}
}

func TestParseEnv_WrongContainerType(t *testing.T) {
	// `env:` as a name->value mapping is the most common authoring mistake here,
	// because that is how several other tools spell it.
	_, err := parseEnv(map[string]any{
		"env": map[string]any{"LOG_LEVEL": "debug"},
	})
	if err == nil {
		t.Fatal("expected an error for env authored as a mapping, got nil")
	}
	if !strings.Contains(err.Error(), "env") {
		t.Errorf("error does not name the property: %v", err)
	}
}

func TestParseEnv_AbsentIsAbsent(t *testing.T) {
	vars, err := parseEnv(map[string]any{"image": "busybox:1"})
	if err != nil {
		t.Fatalf("parseEnv: %v", err)
	}
	if vars != nil {
		t.Errorf("absent env parsed as %+v, want nil", vars)
	}
}

func TestParseCommand_WrongContainerType(t *testing.T) {
	_, err := parseCommand(map[string]any{"command": "/bin/sh -c true"})
	if err == nil {
		t.Fatal("expected an error for command authored as a string, got nil")
	}
	if !strings.Contains(err.Error(), "command") {
		t.Errorf("error does not name the property: %v", err)
	}
}

func TestParseCommand_AbsentIsAbsent(t *testing.T) {
	cmd, err := parseCommand(map[string]any{"args": []any{"--verbose"}})
	if err != nil {
		t.Fatalf("parseCommand: %v", err)
	}
	if cmd != nil {
		t.Errorf("absent command parsed as %+v, want nil", cmd)
	}
}

func TestParseArgs_WrongContainerType(t *testing.T) {
	_, err := parseArgs(map[string]any{"args": map[string]any{"verbose": true}})
	if err == nil {
		t.Fatal("expected an error for args authored as a mapping, got nil")
	}
	if !strings.Contains(err.Error(), "args") {
		t.Errorf("error does not name the property: %v", err)
	}
}

func TestParseArgs_AbsentIsAbsent(t *testing.T) {
	args, err := parseArgs(map[string]any{"command": []any{"/bin/sh"}})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if args != nil {
		t.Errorf("absent args parsed as %+v, want nil", args)
	}
}

// TestParseCommandArgs_NonStringElement covers the second silent drop these two
// parsers carried: their inner loops appended only the elements that asserted to
// string, so `command: [ls, 3]` emitted ["ls"] and said nothing about the 3.
// Routing through optionalStringList makes the element an error too. This is a
// behaviour change for a document that currently relies on the element being
// ignored — but such a document is already not getting what it authored.
func TestParseCommandArgs_NonStringElement(t *testing.T) {
	if _, err := parseCommand(map[string]any{"command": []any{"ls", 3}}); err == nil {
		t.Error("expected an error for a non-string command element, got nil")
	}
	if _, err := parseArgs(map[string]any{"args": []any{"--n", true}}); err == nil {
		t.Error("expected an error for a non-string args element, got nil")
	}
}

// TestParseInitContainersSidecars_NonObjectElement is the matching element-level
// control for the two object lists. Both already rejected a non-object element
// before this change, via their own inner assertion; the conversion moved that
// check into optionalObjectList, so this asserts the behaviour survived the move
// rather than that it is new.
//
// It asserts the message, not merely that an error came back. A mutation that
// drops the element check still errors here, because a non-object element
// decodes to a nil map and then trips the "name is required" guard a few lines
// later — so `err != nil` alone passes against the mutation and pins nothing.
func TestParseInitContainersSidecars_NonObjectElement(t *testing.T) {
	_, err := parseInitContainers(map[string]any{"initContainers": []any{"busybox:1"}})
	if err == nil {
		t.Fatal("expected an error for a non-object initContainers element, got nil")
	}
	if !strings.Contains(err.Error(), "must be an object") {
		t.Errorf("initContainers element rejected for the wrong reason: %v", err)
	}
	_, err = parseSidecars(map[string]any{"sidecars": []any{"envoy:1"}})
	if err == nil {
		t.Fatal("expected an error for a non-object sidecars element, got nil")
	}
	if !strings.Contains(err.Error(), "must be an object") {
		t.Errorf("sidecars element rejected for the wrong reason: %v", err)
	}
}

// TestParseCommandArgs_ReachTheHandler proves the fix is reachable from the
// production entry point, not just from a direct parser call: a mistyped
// `command` on a webservice component must fail the whole build rather than be
// dropped on the way to the container.
func TestParseCommandArgs_ReachTheHandler(t *testing.T) {
	h := &WebserviceHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image":   "ghcr.io/org/app:v1",
			"command": "/bin/sh -c true",
		},
	}, "default")
	if err == nil {
		t.Fatal("expected the webservice handler to reject a string command, got nil")
	}
	if !strings.Contains(err.Error(), "command") {
		t.Errorf("error does not name the property: %v", err)
	}
}
