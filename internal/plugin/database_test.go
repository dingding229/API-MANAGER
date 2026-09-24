package plugin

import (
	"context"
	"errors"
	"testing"

	"api-manager/internal/store"
)

func TestPluginDatabaseWriterFailClosedAndNamespaced(t *testing.T) {
	memory := store.NewMemory()
	writer := StoreDatabaseWriter{Store: memory}
	if err := writer.Put(context.Background(), "plugin", "data", "key", []byte(`{"ok":true}`)); !errors.Is(err, ErrDatabaseWritesDisabled) {
		t.Fatalf("not disabled: %v", err)
	}
	writer.Enabled = true
	if err := writer.Put(context.Background(), "plugin", "data", "key", []byte(`{"ok":true}`)); err == nil {
		t.Fatal("accepted write without host-bound plugin identity")
	}
	writer.PluginName = "plugin"
	if err := writer.Put(context.Background(), "plugin", "data", "key", []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	found, err := memory.GetPluginData("plugin", "data", "key")
	if err != nil || string(found.Value) != `{"ok":true}` {
		t.Fatalf("bad data: %+v %v", found, err)
	}
	if _, err := memory.GetPluginData("other", "data", "key"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("namespace isolation failed")
	}
	if err := writer.Put(context.Background(), "other", "data", "key", []byte(`{"ok":true}`)); err == nil {
		t.Fatal("accepted cross-plugin write")
	}
	if err := writer.Put(context.Background(), "../other", "data", "key", nil); err == nil {
		t.Fatal("accepted invalid namespace")
	}
}

func TestPluginDatabaseWriterRejectsInvalidJSONAndCanceledContext(t *testing.T) {
	writer := StoreDatabaseWriter{Store: store.NewMemory(), Enabled: true, PluginName: "plugin"}
	for _, value := range [][]byte{nil, []byte("not json"), []byte("{"), make([]byte, (1<<20)+1)} {
		if err := writer.Put(context.Background(), "plugin", "state", "key", value); err == nil {
			t.Fatalf("accepted invalid value of length %d", len(value))
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := writer.Put(ctx, "plugin", "state", "key", []byte(`{"ok":true}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write: %v", err)
	}
}
