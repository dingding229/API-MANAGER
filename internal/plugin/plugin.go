package plugin

import (
	"context"
	"fmt"
	"github.com/tetratelabs/wazero"
	"net/http"
	"sort"
	"sync"

	"api-manager/internal/model"
)

type Handler interface {
	Name() string
	Handle(context.Context, http.ResponseWriter, *http.Request, model.API) error
}

type Registry struct {
	services *pluginRuntimeServices
	settings map[string]settingSnapshot
	mu       sync.RWMutex
	handlers map[string]Handler
	entries  map[string]*handlerLease
	cache    wazero.CompilationCache
}

func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]Handler), entries: make(map[string]*handlerLease), cache: wazero.NewCompilationCache()}
}

type handlerLease struct {
	mu      sync.Mutex
	handler Handler
	refs    int
	retired bool
	closed  bool
}

func (e *handlerLease) retire() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.retired = true
	closeNow := e.refs == 0 && !e.closed
	if closeNow {
		e.closed = true
	}
	e.mu.Unlock()
	if closeNow {
		closeHandler(e.handler)
	}
}
func (e *handlerLease) release() {
	e.mu.Lock()
	e.refs--
	closeNow := e.refs == 0 && e.retired && !e.closed
	if closeNow {
		e.closed = true
	}
	e.mu.Unlock()
	if closeNow {
		closeHandler(e.handler)
	}
}
func (r *Registry) Register(handler Handler) {
	r.mu.Lock()
	if r.entries == nil {
		r.entries = map[string]*handlerLease{}
	}
	previous := r.entries[handler.Name()]
	r.handlers[handler.Name()] = handler
	r.entries[handler.Name()] = &handlerLease{handler: handler}
	r.mu.Unlock()
	previous.retire()
}
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	previous := r.entries[name]
	delete(r.handlers, name)
	delete(r.entries, name)
	r.mu.Unlock()
	previous.retire()
}
func closeHandler(handler Handler) {
	wasm, ok := handler.(*wasmHandler)
	if !ok || wasm == nil {
		return
	}
	ctx := context.Background()
	_ = wasm.compiled.Close(ctx)
	_ = wasm.runtime.Close(ctx)
}

// An invocation owns only its entry lease, never the registry mutex. Replacements
// can proceed while old requests complete, without recursive RWMutex deadlocks.
func (r *Registry) Acquire(name string) (Handler, func(), bool) {
	r.mu.RLock()
	entry, ok := r.entries[name]
	if !ok {
		r.mu.RUnlock()
		return nil, func() {}, false
	}
	entry.mu.Lock()
	entry.refs++
	entry.mu.Unlock()
	r.mu.RUnlock()
	var once sync.Once
	return entry.handler, func() { once.Do(entry.release) }, true
}

func (r *Registry) Get(name string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	handler, ok := r.handlers[name]
	return handler, ok
}

func (r *Registry) Close(ctx context.Context) error {
	r.mu.Lock()
	entries := make([]*handlerLease, 0, len(r.entries))
	for _, entry := range r.entries {
		entries = append(entries, entry)
	}
	r.handlers = map[string]Handler{}
	r.entries = map[string]*handlerLease{}
	r.mu.Unlock()
	for _, entry := range entries {
		entry.retire()
	}
	return nil
}

func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.handlers))
	for name := range r.handlers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func StaticResponse(w http.ResponseWriter, api model.API) error {
	w.Header().Set("Content-Type", "application/json")
	body := api.ResponseBody
	if body == "" {
		body = `{"message":"ok"}`
	}
	_, err := w.Write([]byte(body))
	return err
}

func Unknown(name string) error {
	return fmt.Errorf("plugin %q is not registered", name)
}
