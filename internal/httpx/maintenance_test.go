package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMaintenanceDrainsRequestsRejectsWorkAndRecoversOnFailure(t *testing.T) {
	m := &Maintenance{}
	started, finish := make(chan struct{}), make(chan struct{})
	handler := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-finish; w.WriteHeader(200) }))
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/foo", nil))
		close(done)
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*5)
	defer cancel()
	if _, e := m.Begin(ctx); e == nil {
		t.Fatal("did not wait for in-flight request")
	}
	close(finish)
	<-done
	end, e := m.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	m.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("new work entered maintenance") })).ServeHTTP(w, httptest.NewRequest("GET", "/api/foo", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	end()
	w = httptest.NewRecorder()
	m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })).ServeHTTP(w, httptest.NewRequest("GET", "/account/v1/me", nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}

func TestMaintenanceAlsoDrainsBackgroundJobs(t *testing.T) {
	m := &Maintenance{}
	release, ok := m.Work()
	if !ok {
		t.Fatal("job was not admitted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if _, e := m.Begin(ctx); e == nil {
		t.Fatal("background job wasn't drained")
	}
	release()
	end, e := m.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if _, ok = m.Work(); ok {
		t.Fatal("background job entered maintenance")
	}
	end()
}
