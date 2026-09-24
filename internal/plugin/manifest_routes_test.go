package plugin

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestManifestDeclaredRoutesRoundTrip(t *testing.T) {
	raw := []byte(`name: inventory-demo
version: 1.0.0
runtime: wasm
entrypoint: plugin.wasm
routes:
  - name: Item details
    method: GET
    path: /api/inventory/v1/items/{id}
    auth_mode: api_key
    example_path_params:
      id: sample-001
    example_query:
      region: HK
`)
	manifest, err := ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Manifest
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Routes) != 1 || decoded.Routes[0].ExamplePathParams["id"] != "sample-001" || decoded.Routes[0].ExampleQuery["region"] != "HK" {
		t.Fatalf("lost route metadata: %+v", decoded.Routes)
	}
}

func TestManifestRejectsUnsafeOrDuplicateRoutes(t *testing.T) {
	base := "name: inventory-demo\nversion: 1.0.0\nruntime: wasm\nentrypoint: plugin.wasm\nroutes:\n  - method: GET\n    path: /api/inventory/{id}\n    auth_mode: api_key\n"
	tests := []struct{ name, change string }{
		{"missing prefix", "    path: inventory/{id}\n"},
		{"double slash", "    path: /api//inventory/{id}\n"},
		{"unescaped query", "    path: /api/inventory?token=secret\n"},
		{"invalid method", "    method: TRACE\n"},
		{"unknown auth", "    auth_mode: bogus\n"},
		{"bad placeholder", "    path: /api/inventory/{invalid-name}\n"},
		{"unknown example param", "    example_path_params:\n      other: demo\n"},
		{"unsafe example param", "    example_path_params:\n      id: ../bad\n"},
		{"duplicate", "  - method: GET\n    path: /api/inventory/{id}\n    auth_mode: api_key\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := base
			switch tc.name {
			case "missing prefix", "double slash", "unescaped query", "bad placeholder":
				raw = strings.Replace(raw, "    path: /api/inventory/{id}\n", tc.change, 1)
			case "invalid method":
				raw = strings.Replace(raw, "  - method: GET\n", "  - method: TRACE\n", 1)
			case "unknown auth":
				raw = strings.Replace(raw, "    auth_mode: api_key\n", tc.change, 1)
			default:
				raw += tc.change
			}
			if _, err := ParseManifest([]byte(raw)); err == nil {
				t.Fatalf("accepted invalid route: %s", raw)
			}
		})
	}
}

func TestLegacyManifestWithoutRoutesRemainsValid(t *testing.T) {
	manifest, err := ParseManifest([]byte("name: legacy\nversion: 1.0.0\nruntime: wasm\nentrypoint: plugin.wasm\n"))
	if err != nil || len(manifest.Routes) != 0 {
		t.Fatalf("legacy manifest: %+v, %v", manifest, err)
	}
}

func TestManifestDefaultsToBoundedResources(t *testing.T) {
	raw := []byte("name: safe-plugin\nversion: 1.0.0\nruntime: wasm\nentrypoint: plugin.wasm\n")
	manifest, err := ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Limits.MemoryMB != 64 || manifest.Limits.TimeoutMS != 3000 {
		t.Fatalf("unbounded defaults: %+v", manifest.Limits)
	}
	for _, extra := range []string{"limits:\n  memory_mb: 257\n", "limits:\n  timeout_ms: 30001\n"} {
		if _, err := ParseManifest(append(append([]byte{}, raw...), []byte(extra)...)); err == nil {
			t.Fatalf("accepted unbounded limits: %s", extra)
		}
	}
}
