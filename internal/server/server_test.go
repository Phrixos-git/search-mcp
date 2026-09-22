package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakePinger struct {
	err   error
	calls int
}

func (f *fakePinger) PingContext(context.Context) error { f.calls++; return f.err }

type fakeProbe struct {
	err   error
	calls int
}

func (f *fakeProbe) Check(context.Context) error { f.calls++; return f.err }
func request(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHealth(t *testing.T) {
	db := &fakePinger{}
	w := request(t, NewHandler(nil, db), http.MethodGet, "/health")
	if w.Code != 200 || w.Body.String() != "ok\n" || db.calls != 1 {
		t.Fatalf("status=%d body=%q pings=%d", w.Code, w.Body.String(), db.calls)
	}
	db = &fakePinger{err: errors.New("database down secret DSN")}
	w = request(t, NewHandler(nil, db), http.MethodGet, "/health")
	if w.Code != 503 || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "DSN") {
		t.Fatalf("health leaked detail: %d %q", w.Code, w.Body.String())
	}
}

func TestHealthMethodRestrictions(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodHead, http.MethodOptions} {
		db := &fakePinger{}
		w := request(t, NewHandler(nil, db), method, "/health")
		if w.Code != 405 || db.calls != 0 || w.Header().Get("Allow") != "GET" {
			t.Errorf("%s: code=%d calls=%d allow=%q", method, w.Code, db.calls, w.Header().Get("Allow"))
		}
	}
}

func TestReadyChecksDatabaseAndDependencies(t *testing.T) {
	db, one, two := &fakePinger{}, &fakeProbe{}, &fakeProbe{}
	h := NewHandler(nil, db, one, two)
	w := request(t, h, http.MethodGet, "/ready")
	if w.Code != 200 || db.calls != 1 || one.calls != 1 || two.calls != 1 {
		t.Fatalf("ready=%d calls db=%d probes=%d,%d", w.Code, db.calls, one.calls, two.calls)
	}
	if w = request(t, NewHandler(nil, &fakePinger{}), http.MethodGet, "/ready"); w.Code != 200 {
		t.Fatalf("no-dependency ready=%d", w.Code)
	}
	db = &fakePinger{err: errors.New("db key=private")}
	one = &fakeProbe{}
	w = request(t, NewHandler(nil, db, one), http.MethodGet, "/ready")
	if w.Code != 503 || one.calls != 0 || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("db failure response=%d %q probe calls=%d", w.Code, w.Body.String(), one.calls)
	}
	bad := &fakeProbe{err: errors.New("meili key=private")}
	w = request(t, NewHandler(nil, &fakePinger{}, bad), http.MethodGet, "/ready")
	if w.Code != 503 || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("probe failure response=%d %q", w.Code, w.Body.String())
	}
}

func TestReadyMethodAndShutdownGate(t *testing.T) {
	w := request(t, NewHandler(nil, &fakePinger{}), http.MethodPost, "/ready")
	if w.Code != 405 {
		t.Fatalf("POST /ready = %d", w.Code)
	}
	h := NewHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), &fakePinger{})
	h.SetShuttingDown(true)
	if w = request(t, h, http.MethodGet, "/ready"); w.Code != 503 {
		t.Fatalf("shutting /ready = %d", w.Code)
	}
	if w = request(t, h, http.MethodPost, "/mcp"); w.Code != 503 {
		t.Fatalf("shutting /mcp = %d", w.Code)
	}
}

func TestMCPMountedAndUnknownRoute(t *testing.T) {
	mcp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			t.Errorf("path=%q", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	h := NewHandler(mcp, &fakePinger{})
	if w := request(t, h, http.MethodPost, "/mcp"); w.Code != 204 {
		t.Fatalf("mcp=%d", w.Code)
	}
	if w := request(t, h, http.MethodGet, "/missing"); w.Code != 404 {
		t.Fatalf("missing=%d", w.Code)
	}
}
