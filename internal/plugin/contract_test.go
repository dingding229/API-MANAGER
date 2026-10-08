package plugin

import (
	"encoding/json"
	"testing"
)

func contractFixture() Manifest {
	sections := map[string]any{}
	for _, name := range []string{"path", "query", "header"} {
		sections[name] = map[string]any{"type": "object", "description": name + " fields", "properties": map[string]any{}, "additionalProperties": name == "header"}
	}
	return Manifest{Name: "fixture", Version: "1.0.0", Runtime: "wasm", Entrypoint: "plugin.wasm", Routes: []Route{{Name: "fixture", Method: "GET", Path: "/api/fixture", AuthMode: "api_key", ParametersSchema: map[string]any{"type": "object", "additionalProperties": false, "properties": sections}, RequestSchema: map[string]any{"type": "null", "description": "no request body"}}}}
}
func TestPluginRequestDeclarationRejectsMissingTypesDescriptionsAndIndirectReferences(t *testing.T) {
	if e := ValidateRequestContracts(contractFixture()); e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"no-routes", "missing-parameters", "missing-body", "missing-description", "missing-type", "missing-path", "external-reference"} {
		t.Run(kind, func(t *testing.T) {
			m := contractFixture()
			switch kind {
			case "no-routes":
				m.Routes = nil
			case "missing-parameters":
				m.Routes[0].ParametersSchema = nil
			case "missing-body":
				m.Routes[0].RequestSchema = nil
			case "missing-path":
				m.Routes[0].Path = "/api/fixture/{id}"
			default:
				query := m.Routes[0].ParametersSchema["properties"].(map[string]any)["query"].(map[string]any)
				field := map[string]any{"type": "string", "description": "query value"}
				query["properties"].(map[string]any)["q"] = field
				if kind == "missing-description" {
					delete(field, "description")
				}
				if kind == "missing-type" {
					delete(field, "type")
				}
				if kind == "external-reference" {
					field["allOf"] = []any{map[string]any{"$ref": "http://127.0.0.1/private"}}
				}
			}
			if ValidateRequestContracts(m) == nil {
				t.Fatal("invalid declaration accepted", kind)
			}
		})
	}
}
func TestContractMultiMethodRequiresSameDeclaration(t *testing.T) {
	m := contractFixture()
	r := m.Routes[0]
	r.Method = "POST"
	m.Routes = append(m.Routes, r)
	if _, e := contractForManifest(m, "/api/fixture", []string{"GET", "POST"}); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(r.ParametersSchema)
	var different map[string]any
	json.Unmarshal(raw, &different)
	different["description"] = "different"
	m.Routes[1].ParametersSchema = different
	if _, e := contractForManifest(m, "/api/fixture", []string{"GET", "POST"}); e == nil {
		t.Fatal("conflicting method contract accepted")
	}
}
