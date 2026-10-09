package version

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseBuildRecompilesAllGoExecutablesWithPatchedToolchains(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	raw, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(raw)
	for _, required := range []string{"golang:1.26.9-alpine@sha256:", "golang:1.27.2-bookworm@sha256:", "GOTOOLCHAIN=local", "GOMAXPROCS=2", "golang.org/x/net@v0.60.0", "scripts/compress_assets.sh", "-tags=netgo,builtinassets"} {
		if !strings.Contains(dockerfile, required) {
			t.Fatal("missing release safeguard", required)
		}
	}
	for _, component := range []string{"loki", "alloy", "tempo", "prometheus", "alertmanager"} {
		if !strings.Contains(dockerfile, "AS patched-"+component) || !strings.Contains(dockerfile, "COPY --from=patched-"+component+" /out/"+component+" /usr/local/bin/"+component) {
			t.Fatal("component is not rebuilt from source", component)
		}
	}
	for _, obsolete := range []string{"monitoring-binaries", "api-manager:0.3.2", "FROM grafana/loki:", "FROM prom/prometheus:"} {
		if strings.Contains(dockerfile, obsolete) {
			t.Fatal("stale binary import", obsolete)
		}
	}
	if count := strings.Count(dockerfile, "| sha256sum -c -"); count != 7 {
		t.Fatal("source and UI archives must be checksummed", count)
	}
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(workflow), "Dockerfile.release") || strings.Count(string(workflow), "file: Dockerfile") != 3 {
		t.Fatal("release jobs do not use the audited build recipe")
	}
	if !strings.Contains(string(workflow), "go-version: '1.26.9'") {
		t.Fatal("CI uses an outdated app toolchain")
	}
}
