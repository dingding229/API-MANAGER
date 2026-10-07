package plugin

import (
	"api-manager/internal/schema"
	"encoding/json"
	"strings"
	"testing"
)

func TestPluginSettingsRejectUnsafeSchemaAndRedactSecrets(t *testing.T) {
	for _, unsafe := range []string{`{"__proto__":{}}`, `{"constructor":{}}`} {
		var value any
		json.Unmarshal([]byte(unsafe), &value)
		if safeSettings(value, 0) {
			t.Fatal("prototype poison accepted")
		}
	}
	var fields map[string]any
	json.Unmarshal([]byte(`{"type":"object","additionalProperties":false,"properties":{"token":{"type":"string","writeOnly":true},"nested":{"type":"object","properties":{"password":{"writeOnly":true},"name":{"type":"string"}}},"items":{"type":"array","items":{"type":"object","properties":{"secret":{"writeOnly":true}}}}}}`), &fields)
	data := map[string]any{"token": "private-one", "nested": map[string]any{"password": "private-two", "name": "safe"}, "items": []any{map[string]any{"secret": "private-three"}}}
	raw, _ := json.Marshal(redactSettings(data, fields))
	if strings.Contains(string(raw), "private-") {
		t.Fatal("settings leaked", string(raw))
	}
	if !strings.Contains(string(raw), "safe") {
		t.Fatal("public setting lost")
	}
	if schema.ValidateSchema(settingsSchema(Manifest{SettingsSchema: fields})) != nil {
		t.Fatal("schema failed")
	}
	r := NewRegistry()
	r.setSettings("one", "id", data, 1)
	if r.getSettings("two").Data != nil {
		t.Fatal("cross-plugin settings read")
	}
	r.setSettings("one", "id", data, 2)
	if r.getSettings("one").Version != 2 {
		t.Fatal("revision did not change")
	}
}
