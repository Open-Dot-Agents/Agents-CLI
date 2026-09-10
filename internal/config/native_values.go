package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

var nativeNumberSyntax = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// Normalize decimal numbers without expanding their exponent. This preserves
// large integers and bounds memory use by the length of the input number.
func nativeDecimal(number string) (canonical string, integral, positive bool) {
	if !nativeNumberSyntax.MatchString(number) {
		return "", false, false
	}
	negative := strings.HasPrefix(number, "-")
	number = strings.TrimPrefix(number, "-")
	coefficient, exponent := number, "0"
	if i := strings.IndexAny(number, "eE"); i >= 0 {
		coefficient, exponent = number[:i], number[i+1:]
	}
	power, ok := new(big.Int).SetString(exponent, 10)
	if !ok {
		return "", false, false
	}
	fraction := 0
	if i := strings.IndexByte(coefficient, '.'); i >= 0 {
		fraction = len(coefficient) - i - 1
		coefficient = coefficient[:i] + coefficient[i+1:]
	}
	coefficient = strings.TrimLeft(coefficient, "0")
	if coefficient == "" {
		return "0", true, false
	}
	stripped := strings.TrimRight(coefficient, "0")
	power.Add(power, big.NewInt(int64(len(coefficient)-len(stripped)-fraction)))
	sign := ""
	if negative {
		sign = "-"
	}
	return sign + stripped + "e" + power.String(), power.Sign() >= 0, !negative
}
func nativeHash(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		// TOML has values JSON cannot encode, including non-finite floats. Such
		// values cannot be mapped, but external edits must not share one empty hash.
		data, err = toml.Marshal(map[string]any{"value": value})
		if err != nil {
			return "invalid-value"
		}
		sum := sha256.Sum256(data)
		return "toml-v2:" + hex.EncodeToString(sum[:])
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var tree any
	if decoder.Decode(&tree) != nil {
		return "invalid-value"
	}
	var normalize func(any) any
	normalize = func(v any) any {
		switch x := v.(type) {
		case json.Number:
			n, _, _ := nativeDecimal(x.String())
			return json.Number(n)
		case map[string]any:
			for k, v := range x {
				x[k] = normalize(v)
			}
		case []any:
			for i, v := range x {
				x[i] = normalize(v)
			}
		}
		return v
	}
	data, err = json.Marshal(normalize(tree))
	if err != nil {
		return "invalid-value"
	}
	sum := sha256.Sum256(data)
	return "json-v2:" + hex.EncodeToString(sum[:])
}
func nativeHashMatches(value any, hash string) bool {
	if strings.Contains(hash, ":") {
		return hash == nativeHash(value)
	}
	// Preserve ownership written by the first draft.2 implementation. A changed
	// value still needs the exact legacy hash; new records use the tagged format.
	data, err := json.Marshal(value)
	if err != nil {
		return false
	}
	sum := sha256.Sum256(data)
	return hash == hex.EncodeToString(sum[:])
}
func nativeValueType(kind string, value any) bool {
	for _, alternative := range nativeTypeAlternatives(kind) {
		alternative = strings.TrimSpace(alternative)
		if alternative == "null" && value == nil {
			return true
		}
		if alternative == "string" || strings.HasPrefix(alternative, "string (") {
			if _, ok := value.(string); ok {
				return true
			}
			continue
		}
		if alternative == "boolean" || alternative == "bool" {
			if _, ok := value.(bool); ok {
				return true
			}
			continue
		}
		if alternative == "number" || alternative == "integer" || alternative == "integer (positive)" {
			var raw string
			switch n := value.(type) {
			case int:
				raw = strconv.Itoa(n)
			case int64:
				raw = strconv.FormatInt(n, 10)
			case float64:
				if math.IsInf(n, 0) || math.IsNaN(n) {
					continue
				}
				raw = strconv.FormatFloat(n, 'g', -1, 64)
			case json.Number:
				raw = n.String()
			default:
				continue
			}
			normalized, integral, positive := nativeDecimal(raw)
			if normalized == "" {
				continue
			}
			// Native scalar number types are finite; do not accept overflow as a grant.
			if f, err := strconv.ParseFloat(raw, 64); err != nil || math.IsInf(f, 0) {
				continue
			}
			if alternative == "number" || integral && (alternative != "integer (positive)" || positive) {
				return true
			}
			continue
		}
		if alternative == "true" && value == true || alternative == "false" && value == false {
			return true
		}
		// A type name is not a literal enum member. Only documented alternatives
		// with a separator or quoted enum strings can match a native string.
		if text, ok := value.(string); ok && (strings.Contains(kind, "|") || strings.HasPrefix(alternative, "\"")) && !strings.ContainsAny(alternative, "<>{}[]") && alternative != "boolean" && alternative != "string" && text == strings.Trim(alternative, "\"") {
			return true
		}
	}
	return false
}
func nativeTypeAlternatives(kind string) []string {
	// A pipe inside a generic type is not a top-level union separator.
	var result []string
	start, depth := 0, 0
	for i, c := range kind {
		switch c {
		case '<', '{', '[':
			depth++
		case '>', '}', ']':
			depth--
		case '|':
			if depth == 0 {
				result = append(result, strings.TrimSpace(kind[start:i]))
				start = i + 1
			}
		}
	}
	return append(result, strings.TrimSpace(kind[start:]))
}
func nativePatternParts(pattern string) []string {
	pattern = strings.ReplaceAll(pattern, "[]", ".[].")
	parts := strings.Split(pattern, ".")
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
func nativeMatchPattern(pattern string, path []string) (bool, int) {
	parts := nativePatternParts(pattern)
	if len(parts) != len(path) {
		return false, 0
	}
	specificity := 0
	for i, part := range parts {
		if part == path[i] {
			specificity++
			continue
		}
		if strings.HasPrefix(part, "<") && strings.HasSuffix(part, ">") {
			if part == "<index>" {
				if _, err := strconv.Atoi(path[i]); err != nil {
					return false, 0
				}
			}
			continue
		}
		if part == "[]" {
			if _, err := strconv.Atoi(path[i]); err == nil {
				continue
			}
		}
		return false, 0
	}
	return true, specificity
}
func nativeTypeAt(vendor string, path []string) string {
	// Sort equally specific patterns so map iteration cannot change validation.
	patterns := make([]string, 0, len(nativeSettingTypes[vendor]))
	for pattern := range nativeSettingTypes[vendor] {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	best, kind := -1, ""
	for _, pattern := range patterns {
		if ok, score := nativeMatchPattern(pattern, path); ok && score > best {
			best, kind = score, nativeSettingTypes[vendor][pattern]
		}
	}
	if best < 0 && len(path) > 0 && (nativeSettingRegistry[vendor]["user"][path[0]] || nativeSettingRegistry[vendor]["project"][path[0]]) {
		// Documentation often lists only object leaves (for example
		// ide.autoConnect). A known descendant establishes its object parent,
		// but does not make any additional child fields valid.
		for _, pattern := range patterns {
			parts := nativePatternParts(pattern)
			if len(parts) > len(path) {
				if matches, _ := nativeMatchPattern(strings.Join(parts[:len(path)], "."), path); matches {
					return "object"
				}
			}
		}
	}
	return kind
}
func nativeMappedValue(vendor, scope, key string, value any) bool {
	if nativePluginRoot(vendor, key) {
		_, inactive := nativeSelectPlugins(vendor, scope, map[string]any{key: value})
		return nativeSettingRegistry[vendor][scope][key] && len(inactive) == 0
	}
	if vendor == "codex" && key == "hooks" {
		_, inactive, err := nativeSelectCodexHooks(map[string]any{"hooks": value}, true)
		return nativeSettingRegistry[vendor][scope][key] && err == nil && len(inactive) == 0
	}
	if vendor == "copilot" && key == "hooks" {
		_, inactive, err := nativeSelectCopilotHooks(map[string]any{"hooks": value}, true)
		return nativeSettingRegistry[vendor][scope][key] && err == nil && len(inactive) == 0
	}
	if vendor == "codex" {
		if key == "skills" || key == "otel" {
			_, inactive := nativeSelectConfig(vendor, scope, map[string]any{key: value})
			if len(inactive) != 0 {
				return false
			}
		}
		return nativeSettingRegistry[vendor][scope][key] && nativeCodexValue(key, value) && !nativeHasPermissionControl(vendor, []string{key}, value)
	}
	return nativeMappedAt(vendor, scope, []string{key}, value)
}
func nativeMappedAt(vendor, scope string, path []string, value any) bool {
	if len(path) == 0 || !nativeSettingRegistry[vendor][scope][path[0]] {
		return false
	}
	if vendor == "copilot" && nativeCopilotConfigConstraint(path, value) != "" {
		return false
	}
	kind := nativeTypeAt(vendor, path)
	switch value.(type) {
	case map[string]any, []any:
	default:
		return nativeValueType(kind, value)
	}
	for _, alternative := range nativeTypeAlternatives(kind) {
		if nativeMappedAlternative(vendor, scope, path, alternative, value) {
			return true
		}
	}
	return false
}
func nativeMappedAlternative(vendor, scope string, path []string, kind string, value any) bool {
	switch v := value.(type) {
	case map[string]any:
		if len(v) == 0 {
			return kind == "table" || strings.HasPrefix(kind, "table<") || strings.HasPrefix(kind, "map<string,") || strings.HasPrefix(kind, "Record<string,") || kind == "object"
		}
		element := ""
		for _, prefix := range []string{"table<", "map<string,", "Record<string,"} {
			if strings.HasPrefix(kind, prefix) && strings.HasSuffix(kind, ">") {
				element = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(kind, prefix), ">"))
				break
			}
		}
		for key, child := range v {
			// Uniform maps contain caller-defined names. A structured object's actual
			// fields still pass the independent authority and permission checks.
			if element != "" {
				if !nativeValueType(element, child) {
					return false
				}
				continue
			}
			next := append(append([]string(nil), path...), key)
			if nativeConcreteField(vendor, next) && (nativeForbidden(key) || nativeSecurityKey(key)) {
				return false
			}
			if !nativeMappedAt(vendor, scope, next, child) {
				return false
			}
		}
		return true
	case []any:
		element := ""
		if strings.HasPrefix(kind, "array<") && strings.HasSuffix(kind, ">") {
			element = strings.TrimSuffix(strings.TrimPrefix(kind, "array<"), ">")
		} else if strings.HasSuffix(kind, "[]") {
			element = strings.TrimSuffix(kind, "[]")
		}
		if element == "" {
			return false
		}
		for i, child := range v {
			if vendor == "copilot" && nativeCopilotConfigConstraint(append(append([]string(nil), path...), strconv.Itoa(i)), child) != "" {
				return false
			}
			if element == "object" || element == "table" {
				if !nativeMappedAt(vendor, scope, append(append([]string(nil), path...), strconv.Itoa(i)), child) {
					return false
				}
			} else if !nativeValueType(element, child) {
				return false
			}
		}
		return true
	default:
		return nativeValueType(kind, value)
	}
}
func nativeConcreteField(vendor string, path []string) bool {
	// A dynamic resource identifier is not a configuration field. Descendants
	// of that resource retain their own field checks.
	for pattern := range nativeSettingTypes[vendor] {
		parts := nativePatternParts(pattern)
		if len(parts) < len(path) {
			continue
		}
		prefix := strings.Join(parts[:len(path)], ".")
		if matched, _ := nativeMatchPattern(prefix, path); matched {
			last := parts[len(path)-1]
			if strings.HasPrefix(last, "<") && strings.HasSuffix(last, ">") {
				return false
			}
		}
	}
	return true
}
