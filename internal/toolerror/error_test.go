package toolerror

import (
	"context"
	"crypto/x509"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestHTTPMapping(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
		retry  bool
	}{{400, Upstream, false}, {404, NotFound, false}, {403, AccessDenied, false}, {429, RateLimited, true}, {500, Upstream, true}, {503, Upstream, true}} {
		e := FromHTTP(tc.status, "12")
		if e.Code != tc.code || e.Retryable != tc.retry || e.HTTPStatus != tc.status {
			t.Fatalf("status %d: %#v", tc.status, e)
		}
	}
	if n := ParseRetryAfter("9", timeNow()); n != 9 {
		t.Fatalf("retry after: %d", n)
	}
}

func TestRetriesBoundedAndCancellable(t *testing.T) {
	count := 0
	e := DoWithDelay(context.Background(), func(context.Context) error { count++; return errors.New("temporary") }, func(error) *Error { return &Error{Code: Upstream, Retryable: true} }, time.Nanosecond)
	if count != MaxRetries+1 || e == nil {
		t.Fatalf("count=%d err=%v", count, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e = Do(ctx, func(context.Context) error { return errors.New("temporary") }, func(error) *Error { return &Error{Code: Upstream, Retryable: true} })
	if e == nil || e.Code != Timeout {
		t.Fatalf("cancel mapping: %#v", e)
	}
	if FromHTTP(http.StatusTooManyRequests, "2").RetryAfterSeconds != 2 {
		t.Fatal("retry-after missing")
	}
	if e := FromNetwork(x509.UnknownAuthorityError{}); e.Retryable || e.Code != Network {
		t.Fatalf("TLS mapping: %+v", e)
	}
}

func timeNow() time.Time { return time.Now() }
