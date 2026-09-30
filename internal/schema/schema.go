// Package schema provides JSON Schema compilation and instance validation for API contracts.
package schema

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

var cacheMu sync.Mutex
var compiledSchemas = make(map[string]compiledResult)

type compiledResult struct {
	schema *jsonschema.Schema
	err    error
}

func IsEmpty(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte("{}"))
}

func Compile(raw json.RawMessage) (*jsonschema.Schema, error) {
	trimmed := bytes.TrimSpace(raw)
	if IsEmpty(trimmed) {
		return nil, nil
	}
	key := fmt.Sprintf("%x", sha256.Sum256(trimmed))
	cacheMu.Lock()
	if cached, ok := compiledSchemas[key]; ok {
		cacheMu.Unlock()
		return cached.schema, cached.err
	}
	cacheMu.Unlock()
	result := compile(trimmed)
	cacheMu.Lock()
	if len(compiledSchemas) >= 512 {
		compiledSchemas = make(map[string]compiledResult)
	}
	if cached, ok := compiledSchemas[key]; ok {
		result = cached
	} else {
		compiledSchemas[key] = result
	}
	cacheMu.Unlock()
	return result.schema, result.err
}

func compile(raw []byte) compiledResult {
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return compiledResult{err: fmt.Errorf("schema must be valid JSON: %w", err)}
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("memory://api-schema.json", document); err != nil {
		return compiledResult{err: fmt.Errorf("register schema: %w", err)}
	}
	compiled, err := compiler.Compile("memory://api-schema.json")
	if err != nil {
		return compiledResult{err: fmt.Errorf("compile schema: %w", err)}
	}
	return compiledResult{schema: compiled}
}

func ValidateSchema(raw json.RawMessage) error {
	_, err := Compile(raw)
	return err
}

func ValidateInstance(raw json.RawMessage, body []byte) error {
	compiled, err := Compile(raw)
	if err != nil {
		return err
	}
	if compiled == nil {
		return nil
	}
	var instance any
	if len(bytes.TrimSpace(body)) == 0 {
		instance = nil
	} else if err := json.Unmarshal(body, &instance); err != nil {
		return fmt.Errorf("request body must be valid JSON: %w", err)
	}
	if err := compiled.Validate(instance); err != nil {
		return fmt.Errorf("JSON Schema validation failed: %s", compactError(err))
	}
	return nil
}

func compactError(err error) string {
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "instance does not match schema"
	}
	if len(message) > 512 {
		return message[:512] + "..."
	}
	return message
}
