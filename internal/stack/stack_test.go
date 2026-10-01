package stack

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestStackStartsExactlyFiveStandaloneComponents(t *testing.T) {
	binaries := t.TempDir()
	data := t.TempDir()
	names := []string{"loki", "tempo", "prometheus", "alertmanager", "alloy"}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(binaries, name), []byte("#!/bin/sh\nexec sleep 60\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := Start(ctx, Options{Directory: data, APIPort: "8080", BinaryDir: binaries})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if len(s.processes) != len(names) {
		t.Fatalf("started %d processes, want %d", len(s.processes), len(names))
	}
	for _, name := range names {
		if info, err := os.Stat(filepath.Join(data, name)); err != nil || !info.IsDir() {
			t.Fatalf("missing data directory for %s", name)
		}
	}
	dirs, err := os.ReadDir(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != len(names) {
		t.Fatalf("unexpected component data directories: %d", len(dirs))
	}
}
