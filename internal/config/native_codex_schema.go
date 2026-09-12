package config

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed native_schemas/codex-0.154.0.json
var nativeCodexSchema []byte

var codexSchemaOnce sync.Once
var codexSchemaValidator *jsonschema.Schema
var codexSchemaError error

type nativeSchemaLoader struct{}

func (nativeSchemaLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("external schema resource is not permitted")
}

func loadNativeCodexSchema() (*jsonschema.Schema, error) {
	codexSchemaOnce.Do(func() {
		decoder := json.NewDecoder(bytes.NewReader(nativeCodexSchema))
		decoder.UseNumber()
		var document any
		if codexSchemaError = decoder.Decode(&document); codexSchemaError != nil {
			return
		}
		// Schemars records Rust numeric widths as format annotations. Apply the
		// corresponding bounds explicitly: generic JSON Schema validators do not
		// give an int64 format annotation its Rust deserialization semantics.
		addNativeIntegerBounds(document)
		compiler := jsonschema.NewCompiler()
		compiler.UseLoader(nativeSchemaLoader{})
		const resource = "urn:open-dot-agents:native:codex:0.154.0"
		if codexSchemaError = compiler.AddResource(resource, document); codexSchemaError != nil {
			return
		}
		codexSchemaValidator, codexSchemaError = compiler.Compile(resource)
	})
	return codexSchemaValidator, codexSchemaError
}
func addNativeIntegerBounds(value any) {
	switch v := value.(type) {
	case map[string]any:
		widths := map[string][2]string{"int64": {"-9223372036854775808", "9223372036854775807"}, "uint64": {"0", "18446744073709551615"}, "uint": {"0", "18446744073709551615"}, "int32": {"-2147483648", "2147483647"}, "uint32": {"0", "4294967295"}, "uint16": {"0", "65535"}, "uint8": {"0", "255"}}
		if format, ok := v["format"].(string); ok {
			if bounds, ok := widths[format]; ok {
				if _, set := v["minimum"]; !set {
					v["minimum"] = json.Number(bounds[0])
				}
				if _, set := v["maximum"]; !set {
					v["maximum"] = json.Number(bounds[1])
				}
			}
		}
		for _, child := range v {
			addNativeIntegerBounds(child)
		}
	case []any:
		for _, child := range v {
			addNativeIntegerBounds(child)
		}
	}
}
func nativeFiniteJSON(value any) bool {
	switch v := value.(type) {
	case json.Number:
		_, err := strconv.ParseFloat(v.String(), 64)
		return err == nil
	case map[string]any:
		for _, child := range v {
			if !nativeFiniteJSON(child) {
				return false
			}
		}
	case []any:
		for _, child := range v {
			if !nativeFiniteJSON(child) {
				return false
			}
		}
	}
	return true
}
func nativeCodexValue(key string, value any) bool {
	validator, err := loadNativeCodexSchema()
	if err != nil {
		return false
	}
	data, err := json.Marshal(map[string]any{key: value})
	if err != nil {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var document any
	if decoder.Decode(&document) != nil || !nativeFiniteJSON(document) {
		return false
	}
	if !nativeCodexKeymapDocument(key, document.(map[string]any)[key]) {
		return false
	}
	if nativeCodexAliases(document.(map[string]any), true) != nil {
		return false
	}
	return validator.Validate(document) == nil
}

// Serde accepts these aliases in the pinned source, but Schemars omits them.
// Normalize only the validation copy. Keep the original spelling in artifacts
// and ownership records. A duplicate alias is an error even with equal values.
var nativeCodexAliasFields = map[string]map[string]string{
	"agents":   {"max_threads": "max_concurrent_threads_per_session"},
	"memories": {"no_memories_if_mcp_or_web_search": "disable_on_external_context"},
}

func nativeCodexAliases(document map[string]any, normalize bool) error {
	for section, aliases := range nativeCodexAliasFields {
		fields, ok := document[section].(map[string]any)
		if !ok {
			continue
		}
		for alias, canonical := range aliases {
			value, exists := fields[alias]
			if !exists {
				continue
			}
			if _, duplicate := fields[canonical]; duplicate {
				return fmt.Errorf("duplicate native alias assignment: %s.%s and %s.%s", section, alias, section, canonical)
			}
			if normalize {
				fields[canonical] = value
				delete(fields, alias)
			}
		}
	}
	return nil
}
func nativeHasPermissionControl(vendor string, path []string, value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			next := append(append([]string(nil), path...), key)
			if nativePolicyField(vendor, next) && nativeSecurityKey(key) || nativeHasPermissionControl(vendor, next, child) {
				return true
			}
		}
	case []any:
		for i, child := range v {
			if nativeHasPermissionControl(vendor, append(append([]string(nil), path...), fmt.Sprint(i)), child) {
				return true
			}
		}
	}
	return false
}
