package schema

import (
	"encoding/json"
	"testing"
)

func TestValidateInstance(t *testing.T) {
	document := json.RawMessage(`{"type":"object","required":["id"],"properties":{"id":{"type":"integer"}},"additionalProperties":false}`)
	if err := ValidateSchema(document); err != nil {
		t.Fatalf("valid schema rejected: %v", err)
	}
	if err := ValidateInstance(document, []byte(`{"id":42}`)); err != nil {
		t.Fatalf("valid instance rejected: %v", err)
	}
	if err := ValidateInstance(document, []byte(`{"id":"bad"}`)); err == nil {
		t.Fatal("invalid instance accepted")
	}
}

func TestValidateSchemaRejectsInvalidDocument(t *testing.T) {
	if err := ValidateSchema(json.RawMessage(`{"type":"unknown-type"}`)); err == nil {
		t.Fatal("invalid schema accepted")
	}
}
