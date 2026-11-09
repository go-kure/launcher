package patch

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// TOMLHeader represents a parsed TOML-style header like [kind.name.section.selector]
type TOMLHeader struct {
	Kind     string
	Name     string
	Sections []string
	Selector *Selector
}

// Selector represents different types of selectors in TOML headers
type Selector struct {
	Type      string // "index", "key-value", "bracketed"
	Index     *int
	Key       string
	Value     string
	Bracketed string
}

// VariableContext holds variables for substitution
type VariableContext struct {
	Values   map[string]any
	Features map[string]bool
}

// ParseTOMLHeader parses a TOML-style header into structured components
// Examples:
//
//	[deployment.app] → Kind: deployment, Name: app
//	[deployment.app.containers.name=main] → Kind: deployment, Name: app, Sections: [containers], Selector: {Key: name, Value: main}
//	[deployment.app.ports.0] → Kind: deployment, Name: app, Sections: [ports], Selector: {Index: 0}
//	[deployment.app.containers[image.name=main]] → Kind: deployment, Name: app, Sections: [containers], Selector: {Bracketed: image.name=main}
func ParseTOMLHeader(header string) (*TOMLHeader, error) {
	// Remove brackets and trim whitespace
	header = strings.TrimSpace(header)
	if !strings.HasPrefix(header, "[") || !strings.HasSuffix(header, "]") {
		return nil, fmt.Errorf("invalid TOML header format: %s", header)
	}

	content := strings.TrimSpace(header[1 : len(header)-1])
	if content == "" {
		return nil, fmt.Errorf("empty TOML header")
	}

	// Handle bracketed selectors first: [deployment.app.containers[image.name=main]]
	bracketedRe := regexp.MustCompile(`^(.+)\[(.+)\]$`)
	if matches := bracketedRe.FindStringSubmatch(content); len(matches) == 3 {
		path := matches[1]
		bracketed := matches[2]

		parts := strings.Split(path, ".")
		if len(parts) < 2 {
			return nil, fmt.Errorf("TOML header must have at least kind.name: %s", header)
		}

		return &TOMLHeader{
			Kind:     parts[0],
			Name:     parts[1],
			Sections: parts[2:],
			Selector: &Selector{
				Type:      "bracketed",
				Bracketed: bracketed,
			},
		}, nil
	}

	// Split by dots
	parts := strings.Split(content, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("TOML header must have at least kind.name: %s", header)
	}

	result := &TOMLHeader{
		Kind: parts[0],
		Name: parts[1],
	}

	// Process remaining parts for sections and selectors
	if len(parts) > 2 {
		sections := parts[2:]
		lastIdx := len(sections) - 1
		lastPart := sections[lastIdx]

		// Check if the last part is a selector
		selector, isSelector := parseSelector(lastPart)
		if isSelector {
			result.Selector = selector
			if lastIdx > 0 {
				result.Sections = sections[:lastIdx]
			}
		} else {
			result.Sections = sections
		}
	}

	return result, nil
}

// parseSelector determines if a part is a selector and parses it
func parseSelector(part string) (*Selector, bool) {
	// Check for key=value selector
	if strings.Contains(part, "=") && !strings.Contains(part, "[") {
		keyValue := strings.SplitN(part, "=", 2)
		return &Selector{
			Type:  "key-value",
			Key:   keyValue[0],
			Value: keyValue[1],
		}, true
	}

	// Check for numeric index
	if idx, err := strconv.Atoi(part); err == nil {
		return &Selector{
			Type:  "index",
			Index: &idx,
		}, true
	}

	return nil, false
}

// ResolveTOMLPath converts a TOML header to resource target and field path
func (h *TOMLHeader) ResolveTOMLPath() (resourceTarget, fieldPath string, err error) {
	// Resource target should include kind to distinguish resources with same name
	if h.Kind != "" {
		resourceTarget = fmt.Sprintf("%s.%s", h.Kind, h.Name)
	} else {
		resourceTarget = h.Name
	}

	// Build field path from sections and selector
	var pathParts []string

	// Map TOML sections to Kubernetes paths based on resource kind
	pathParts = h.mapSectionsToKubernetesPath()

	// Add selector to path
	if h.Selector != nil {
		switch h.Selector.Type {
		case "index":
			// Remove the last part and add it with index selector
			if len(pathParts) > 0 {
				lastPart := pathParts[len(pathParts)-1]
				pathParts = pathParts[:len(pathParts)-1]
				pathParts = append(pathParts, fmt.Sprintf("%s[%d]", lastPart, *h.Selector.Index))
			} else {
				pathParts = append(pathParts, fmt.Sprintf("[%d]", *h.Selector.Index))
			}
		case "key-value":
			// Remove the last part and add it with key-value selector
			if len(pathParts) > 0 {
				lastPart := pathParts[len(pathParts)-1]
				pathParts = pathParts[:len(pathParts)-1]
				pathParts = append(pathParts, fmt.Sprintf("%s[%s=%s]", lastPart, h.Selector.Key, h.Selector.Value))
			} else {
				pathParts = append(pathParts, fmt.Sprintf("[%s=%s]", h.Selector.Key, h.Selector.Value))
			}
		case "bracketed":
			// For bracketed selectors, parse the inner content and apply it
			if len(pathParts) > 0 {
				lastPart := pathParts[len(pathParts)-1]
				pathParts = pathParts[:len(pathParts)-1]
				pathParts = append(pathParts, fmt.Sprintf("%s[%s]", lastPart, h.Selector.Bracketed))
			} else {
				pathParts = append(pathParts, fmt.Sprintf("[%s]", h.Selector.Bracketed))
			}
		}
	}

	fieldPath = strings.Join(pathParts, ".")

	// Clean up path - remove multiple dots, leading/trailing dots
	fieldPath = regexp.MustCompile(`\.+`).ReplaceAllString(fieldPath, ".")
	fieldPath = strings.Trim(fieldPath, ".")

	return resourceTarget, fieldPath, nil
}

// mapSectionsToKubernetesPath maps TOML sections to appropriate Kubernetes field paths
// based on the resource kind and section hierarchy
func (h *TOMLHeader) mapSectionsToKubernetesPath() []string {
	if len(h.Sections) == 0 {
		return []string{}
	}

	var pathParts []string
	kind := strings.ToLower(h.Kind)

	// Process sections based on Kubernetes resource structure
	for i, section := range h.Sections {
		switch section {
		case "containers":
			if isWorkloadKind(kind) {
				pathParts = appendExpansion(pathParts, "spec", "template", "spec", "containers")
			} else {
				pathParts = appendExpansion(pathParts, "spec", "containers")
			}
		case "initContainers":
			if isWorkloadKind(kind) {
				pathParts = appendExpansion(pathParts, "spec", "template", "spec", "initContainers")
			} else {
				pathParts = appendExpansion(pathParts, "spec", "initContainers")
			}
		case "ports":
			// Context-sensitive: could be container ports or service ports
			if inContainerList(h.Sections, i) {
				pathParts = append(pathParts, "ports")
			} else {
				pathParts = appendExpansion(pathParts, "spec", "ports")
			}
		case "env":
			pathParts = append(pathParts, "env")
		case "envFrom":
			pathParts = append(pathParts, "envFrom")
		case "volumeMounts":
			pathParts = append(pathParts, "volumeMounts")
		case "volumes":
			if isWorkloadKind(kind) {
				pathParts = appendExpansion(pathParts, "spec", "template", "spec", "volumes")
			} else {
				pathParts = appendExpansion(pathParts, "spec", "volumes")
			}
		case "resources":
			pathParts = append(pathParts, "resources")
		case "securityContext":
			pathParts = append(pathParts, "securityContext")
		case "image":
			pathParts = append(pathParts, "image")
		case "command":
			pathParts = append(pathParts, "command")
		case "args":
			pathParts = append(pathParts, "args")

		// Service-specific sections
		case "selector":
			pathParts = appendExpansion(pathParts, "spec", "selector")
		case "type":
			pathParts = appendExpansion(pathParts, "spec", "type")

		// Ingress-specific sections
		case "tls":
			pathParts = appendExpansion(pathParts, "spec", "tls")
		case "rules":
			if kind == "role" || kind == "clusterrole" {
				pathParts = append(pathParts, "rules")
			} else {
				// Ingress rules
				pathParts = appendExpansion(pathParts, "spec", "rules")
			}
		case "backend":
			pathParts = append(pathParts, "backend")
		case "paths":
			pathParts = append(pathParts, "http", "paths")

		// ConfigMap/Secret sections
		case "data":
			pathParts = append(pathParts, "data")
		case "stringData":
			pathParts = append(pathParts, "stringData")
		case "binaryData":
			pathParts = append(pathParts, "binaryData")

		// RBAC sections  - handled above in the ingress case, check context
		case "subjects":
			pathParts = append(pathParts, "subjects")
		case "roleRef":
			pathParts = append(pathParts, "roleRef")

		// Generic spec and metadata sections
		case "spec":
			pathParts = append(pathParts, "spec")
		case "metadata":
			pathParts = append(pathParts, "metadata")
		case "labels":
			if len(pathParts) == 0 || pathParts[len(pathParts)-1] != "metadata" {
				pathParts = append(pathParts, "metadata", "labels")
			} else {
				pathParts = append(pathParts, "labels")
			}
		case "annotations":
			if len(pathParts) == 0 || pathParts[len(pathParts)-1] != "metadata" {
				pathParts = append(pathParts, "metadata", "annotations")
			} else {
				pathParts = append(pathParts, "annotations")
			}
		case "template":
			pathParts = append(pathParts, "template")

		// Numeric sections (likely array indices when not handled as selectors)
		default:
			if _, err := strconv.Atoi(section); err == nil {
				// This is a numeric index, treat it as array access
				if len(pathParts) > 0 {
					lastPart := pathParts[len(pathParts)-1]
					pathParts = pathParts[:len(pathParts)-1]
					pathParts = append(pathParts, fmt.Sprintf("%s[%s]", lastPart, section))
				} else {
					pathParts = append(pathParts, fmt.Sprintf("[%s]", section))
				}
			} else {
				// Unknown section, add as-is
				pathParts = append(pathParts, section)
			}
		}
	}

	return pathParts
}

// appendExpansion appends the path that a section name expands to. A section such as
// `containers` expands to `spec.template.spec.containers`. When everything the header spelled
// before it is the start of that path (`[deployment.app.spec.template.spec.containers]`), only the
// rest is appended, so the part already written is not written twice. A header that spelled
// something else first (`[deployment.app.metadata.spec.containers]`) is left as it was.
func appendExpansion(pathParts []string, expansion ...string) []string {
	if len(pathParts) <= len(expansion) && slices.Equal(pathParts, expansion[:len(pathParts)]) {
		return append(pathParts, expansion[len(pathParts):]...)
	}
	return append(pathParts, expansion...)
}

// inContainerList reports whether section i sits directly under a container list, or under one
// item of it (`containers.0.ports`). The two lists are `containers` and `initContainers`.
func inContainerList(sections []string, i int) bool {
	isList := func(s string) bool { return s == "containers" || s == "initContainers" }
	if i > 0 && isList(sections[i-1]) {
		return true
	}
	if i > 1 {
		if _, err := strconv.Atoi(sections[i-1]); err == nil {
			return isList(sections[i-2])
		}
	}
	return false
}

// isWorkloadKind returns true for Kubernetes workload kinds that have pod templates
func isWorkloadKind(kind string) bool {
	workloadKinds := []string{
		"deployment", "replicaset", "statefulset", "daemonset", "job", "cronjob",
	}

	return slices.Contains(workloadKinds, strings.ToLower(kind))
}

// SubstituteVariables replaces ${values.key} and ${features.flag} patterns with actual values
func SubstituteVariables(value string, ctx *VariableContext) (any, error) {
	if ctx == nil {
		return value, nil
	}

	strValue := fmt.Sprintf("%v", value)

	// Pattern for ${values.key} and ${features.flag}
	varPattern := regexp.MustCompile(`\$\{(values|features)\.([^}]+)\}`)

	result := varPattern.ReplaceAllStringFunc(strValue, func(match string) string {
		matches := varPattern.FindStringSubmatch(match)
		if len(matches) != 3 {
			return match // Return original if we can't parse
		}

		scope := matches[1]
		key := matches[2]

		switch scope {
		case "values":
			if val, exists := ctx.Values[key]; exists {
				return fmt.Sprintf("%v", val)
			}
		case "features":
			if val, exists := ctx.Features[key]; exists {
				return fmt.Sprintf("%v", val)
			}
		}

		return match // Return original if variable not found
	})

	// If no substitution occurred, return original value
	if result == strValue {
		return value, nil
	}

	return result, nil
}

// IsTOMLFormat detects if the content appears to be TOML-style patch format
func IsTOMLFormat(content string) bool {
	lines := strings.SplitSeq(content, "\n")

	for line := range lines {
		line = strings.TrimSpace(line)

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Look for TOML section header
		if strings.HasPrefix(line, "[") && strings.Contains(line, "]") {
			return true
		}

		// Look for TOML key = value pattern (but not in inline tables)
		if strings.Contains(line, " = ") && !strings.Contains(line, "{") {
			return true
		}

		// If we find non-comment, non-empty content that's not a TOML header
		// but looks like YAML (starts with - or has :), it's probably YAML
		if strings.HasPrefix(line, "-") || (strings.Contains(line, ":") && !strings.HasPrefix(line, "[")) {
			return false
		}
	}

	return false
}

// String returns a string representation of the TOML header
func (h *TOMLHeader) String() string {
	parts := []string{h.Kind, h.Name}
	parts = append(parts, h.Sections...)

	result := strings.Join(parts, ".")

	if h.Selector != nil {
		switch h.Selector.Type {
		case "index":
			result += fmt.Sprintf(".%d", *h.Selector.Index)
		case "key-value":
			result += fmt.Sprintf(".%s=%s", h.Selector.Key, h.Selector.Value)
		case "bracketed":
			result += fmt.Sprintf("[%s]", h.Selector.Bracketed)
		}
	}

	return fmt.Sprintf("[%s]", result)
}

// wholeReferencePattern matches a value that is exactly one ${values.x} or ${features.x} reference.
var wholeReferencePattern = regexp.MustCompile(`^\$\{(values|features)\.([^}]+)\}$`)

// referencePattern matches a ${values.x} or ${features.x} reference anywhere in a value.
var referencePattern = regexp.MustCompile(`\$\{(values|features)\.([^}]+)\}`)

// substituteValue substitutes variables in a value. When the value is exactly one defined
// reference (`${values.replicas}`), it returns the variable itself, with its own type.
// Otherwise it returns the text SubstituteVariables builds, so an embedded reference
// (`nginx:${values.tag}`) always gives a string. A reference to an undefined name is left
// as written.
func substituteValue(value string, ctx *VariableContext) (any, error) {
	if ctx == nil {
		return value, nil
	}
	if m := wholeReferencePattern.FindStringSubmatch(value); m != nil {
		switch m[1] {
		case "values":
			if v, ok := ctx.Values[m[2]]; ok {
				return v, nil
			}
		case "features":
			if v, ok := ctx.Features[m[2]]; ok {
				return v, nil
			}
		}
	}
	return SubstituteVariables(value, ctx)
}

// tomlValue types the value text of one TOML patch line. The TOML form has no types of its
// own, so the text decides:
//   - A value whose whole text is a double-quoted string (Go syntax, `strconv.Unquote`) is a
//     string. The quotes are removed, variables are substituted as text and nothing is inferred.
//     Text that is not one such string, such as `"a" and "b"` or a single-quoted value, is
//     read as unquoted text.
//   - An unquoted value with no reference is inferred by inferValueType.
//   - An unquoted whole reference takes the variable's own type, and an unquoted embedded
//     reference gives a string. Neither is inferred.
func tomlValue(key, text string, ctx *VariableContext) (any, error) {
	if len(text) >= 2 && text[0] == '"' {
		if unquoted, err := strconv.Unquote(text); err == nil {
			return SubstituteVariables(unquoted, ctx)
		}
	}
	value, err := substituteValue(text, ctx)
	if err != nil {
		return nil, err
	}
	s, isString := value.(string)
	if !isString || referencePattern.MatchString(text) {
		return value, nil
	}
	return inferValueType(key, s), nil
}

// inferValueType types the text of an unquoted TOML value: `true` or `false` in any case is a
// bool, and an integer is an int under a Kubernetes integer field. Everything else stays a
// string. Only the TOML form calls it: a YAML value already has a type.
func inferValueType(key, value string) any {
	switch strings.ToLower(value) {
	case "true":
		return true
	case "false":
		return false
	}

	if isIntegerField(key) {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}

	return value
}

// integerFields are the names of Kubernetes fields that hold an integer.
var integerFields = []string{
	"port", "targetPort", "nodePort", "containerPort",
	"replicas", "maxUnavailable", "maxSurge",
	"initialDelaySeconds", "timeoutSeconds", "periodSeconds", "successThreshold", "failureThreshold",
	"terminationGracePeriodSeconds", "activeDeadlineSeconds",
	"runAsUser", "runAsGroup", "fsGroup",
	"weight", "priority", "number",
}

// isIntegerField reports whether the last path segment of key is the name of an integer
// field, ignoring case. A trailing selector (`[0]`, `[name=http]`) is not part of the
// name, and a name that merely contains one (`support`, `phoneNumber`) does not match. Dots at
// either end of the path, and before a selector, are ignored, as path parsing ignores them, so
// `spec.replicas.` and `spec.replicas.[0]` both name `replicas`.
func isIntegerField(key string) bool {
	key = strings.Trim(key, ".")
	for strings.HasSuffix(key, "]") {
		i := strings.LastIndex(key, "[")
		if i < 0 {
			break
		}
		key = strings.Trim(key[:i], ".")
	}
	if i := strings.LastIndex(key, "."); i >= 0 {
		key = key[i+1:]
	}
	for _, field := range integerFields {
		if strings.EqualFold(key, field) {
			return true
		}
	}
	return false
}
