package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"

	"api-manager/internal/model"
	"api-manager/internal/store"
)

var ErrDatabaseWritesDisabled = errors.New("plugin database writes are disabled")
var databaseKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// DatabaseWriter is the future host-side boundary for plugin persistence.
// It accepts logical namespace/key/value data rather than SQL, so a later WASM
// host ABI can enforce tenant, size and permission limits without exposing
// arbitrary database access to a plugin.
type DatabaseWriter interface {
	Put(ctx context.Context, pluginName, namespace, key string, value []byte) error
}

type DeniedDatabaseWriter struct{}

func (DeniedDatabaseWriter) Put(context.Context, string, string, string, []byte) error {
	return ErrDatabaseWritesDisabled
}

type StoreDatabaseWriter struct {
	Store      store.PluginDataStore
	Enabled    bool
	PluginName string // Required host-bound identity; prevents cross-plugin writes.
}

func (w StoreDatabaseWriter) Put(ctx context.Context, pluginName, namespace, key string, value []byte) error {
	if !w.Enabled {
		return ErrDatabaseWritesDisabled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if w.PluginName == "" {
		return errors.New("plugin database identity is not bound by host")
	}
	if pluginName != w.PluginName {
		return errors.New("plugin database identity mismatch")
	}
	if !databaseKey.MatchString(pluginName) || !databaseKey.MatchString(namespace) || !databaseKey.MatchString(key) {
		return errors.New("invalid plugin database key")
	}
	if len(value) == 0 || !json.Valid(value) {
		return errors.New("plugin database value must be valid JSON")
	}
	if len(value) > 1<<20 {
		return errors.New("plugin database value exceeds 1 MiB")
	}
	if w.Store == nil {
		return errors.New("plugin database store is unavailable")
	}
	return w.Store.PutPluginData(model.PluginData{PluginName: pluginName, Namespace: namespace, Key: key, Value: append([]byte(nil), value...)})
}
