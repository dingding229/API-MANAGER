package schema

import "testing"

func TestValidateInstanceRejectsTrailingJSON(t *testing.T) {
	document := []byte(`{"type":"object","required":["ok"],"properties":{"ok":{"const":true}},"additionalProperties":false}`)
	for _, test := range []struct {
		name  string
		body  string
		valid bool
	}{
		{"one document", `{"ok":true}`, true},
		{"whitespace", "{\"ok\":true}\n\t ", true},
		{"second document", `{"ok":true}{"admin":true}`, false},
		{"trailing garbage", `{"ok":true}garbage`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateInstance(document, []byte(test.body))
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v error=%v", test.valid, err)
			}
		})
	}
}
