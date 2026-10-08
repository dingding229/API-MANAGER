package plugin

import (
	"api-manager/internal/model"
	"context"
	"net/http"
	"sync"
	"testing"
	"time"
)

type leaseFixture struct{ name string }

func (f *leaseFixture) Name() string { return f.name }
func (f *leaseFixture) Handle(context.Context, http.ResponseWriter, *http.Request, model.API) error {
	return nil
}
func TestRegistryLeaseDoesNotHoldGlobalLockOrBlockReplacement(t *testing.T) {
	r := NewRegistry()
	first := &leaseFixture{name: "fixture"}
	second := &leaseFixture{name: "fixture"}
	r.Register(first)
	handler, release, ok := r.Acquire("fixture")
	if !ok || handler != first {
		t.Fatal("lease absent")
	}
	done := make(chan struct{})
	go func() { r.Register(second); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("replacement blocked by active invocation")
	}
	got, ok := r.Get("fixture")
	if !ok || got != second {
		t.Fatal("replacement not visible")
	}
	release()
	release()
	r.Unregister("fixture")
}
func TestRegistryConcurrentAcquireReplaceAndMetadata(t *testing.T) {
	r := NewRegistry()
	r.Register(&leaseFixture{name: "fixture"})
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, release, ok := r.Acquire("fixture")
				if ok {
					_, _ = r.Get("fixture")
					_ = r.List()
					release()
				}
			}
		}()
	}
	for i := 0; i < 100; i++ {
		r.Register(&leaseFixture{name: "fixture"})
	}
	wg.Wait()
	_ = r.Close(context.Background())
}
