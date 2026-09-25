package plugin

import (
	"context"
	"fmt"
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
	mu       sync.RWMutex
	handlers map[string]Handler
}

func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]Handler)}
}

func (r *Registry) Register(handler Handler) {
	r.mu.Lock()
	previous := r.handlers[handler.Name()]
	r.handlers[handler.Name()] = handler
	r.mu.Unlock()
	closeHandler(previous)
}

func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	previous := r.handlers[name]
	delete(r.handlers, name)
	r.mu.Unlock()
	closeHandler(previous)
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

// Acquire holds a read lease for an in-flight call; replacement waits before closing its runtime.
func (r *Registry) Acquire(name string) (Handler, func(), bool) {
	r.mu.RLock()
	handler, ok := r.handlers[name]
	if !ok {
		r.mu.RUnlock()
		return nil, func() {}, false
	}
	return handler, r.mu.RUnlock, true
}

func (r *Registry) Get(name string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	handler, ok := r.handlers[name]
	return handler, ok
}

func (r *Registry) Close(ctx context.Context) error {
	r.mu.Lock()
	handlers := make([]Handler, 0, len(r.handlers))
	for _, handler := range r.handlers {
		handlers = append(handlers, handler)
	}
	r.mu.Unlock()
	var firstErr error
	for _, handler := range handlers {
		if wasm, ok := handler.(*wasmHandler); ok {
			if err := wasm.compiled.Close(ctx); err != nil && firstErr == nil {
				firstErr = err
			}
			if err := wasm.runtime.Close(ctx); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
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
