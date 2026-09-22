package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"search-mcp/internal/storage"
	"strings"
	"testing"
	"time"
)

type testResolver struct{ ips []net.IPAddr }

func (r testResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) { return r.ips, nil }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(req *http.Request, status int, headers http.Header, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: req}
}
func TestBlockedAddresses(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.1.1", "224.0.0.1", "0.0.0.0", "0.1.2.3", "100.64.0.1", "192.0.2.2"} {
		if !blocked(net.ParseIP(ip)) {
			t.Errorf("not blocked %s", ip)
		}
	}
	if blocked(net.ParseIP("8.8.8.8")) {
		t.Fatal("public address blocked")
	}
	for _, host := range []string{"localhost", "foo.localhost", "metadata.google.internal"} {
		if CheckAddress(context.Background(), testResolver{}, host) == nil {
			t.Errorf("host accepted %s", host)
		}
	}
	if CheckAddress(context.Background(), testResolver{[]net.IPAddr{{IP: net.ParseIP("10.0.0.1")}}}, "public.test") == nil {
		t.Fatal("private DNS answer accepted")
	}
}

func TestResponseBodyLimitWithoutContentLength(t *testing.T) {
	f, _ := mockFetcher(t, func(r *http.Request) (*http.Response, error) {
		resp := response(r, 200, http.Header{"Content-Type": []string{"text/plain"}}, strings.Repeat("x", 1025))
		resp.ContentLength = -1
		return resp, nil
	})
	_, err := f.Fetch(context.Background(), "https://example.test/stream")
	var fe *FetchError
	if !errors.As(err, &fe) || fe.Code != "response_too_large" {
		t.Fatalf("streaming limit result: %v", err)
	}
}
func mockFetcher(t *testing.T, handler roundTripFunc) (*Fetcher, *storage.Store) {
	t.Helper()
	s, e := storage.Open(context.Background(), ":memory:", time.Second)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close() })
	f := NewFetcher(s, nil, time.Second, 1024)
	f.SetResolver(testResolver{[]net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}})
	f.client = &http.Client{Transport: handler, CheckRedirect: f.client.CheckRedirect, Timeout: time.Second}
	return f, s
}
func TestFetch200CacheAndExtraction(t *testing.T) {
	calls := 0
	f, _ := mockFetcher(t, func(r *http.Request) (*http.Response, error) {
		calls++
		return response(r, 200, http.Header{"Content-Type": []string{"text/html"}, "Cache-Control": []string{"max-age=300"}}, "<html><head><title>Page</title></head><body><nav>nav</nav><h1>Heading</h1><p>Hello <a href='/x'>world</a></p><script>bad</script><img alt='image'></body></html>"), nil
	})
	got, e := f.Fetch(context.Background(), "https://example.test/a")
	if e != nil {
		t.Fatal(e)
	}
	if got.Title != "Page" || !strings.Contains(got.Content, "# Heading") || !strings.Contains(got.Content, "Hello world") || strings.Contains(got.Content, "bad") || !strings.Contains(got.Content, "image") {
		t.Fatalf("extraction: %#v", got)
	}
	hit, e := f.Fetch(context.Background(), "https://example.test/a")
	if e != nil || !hit.FromCache || calls != 1 {
		t.Fatalf("cache: %#v %v calls=%d", hit, e, calls)
	}
}
func TestConditional304(t *testing.T) {
	f, s := mockFetcher(t, func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("If-None-Match") != "v1" {
			t.Errorf("missing conditional header")
		}
		return response(r, 304, http.Header{"Cache-Control": []string{"max-age=30"}}, ""), nil
	})
	_, e := s.Save(context.Background(), storage.Document{OriginalURL: "https://example.test/a", Title: "kept", Content: "old", ContentHash: "h", ETag: "v1", ExpiresAt: time.Now().Add(-time.Second)})
	if e != nil {
		t.Fatal(e)
	}
	got, e := f.Fetch(context.Background(), "https://example.test/a")
	if e != nil || got.Title != "kept" || !got.FromCache {
		t.Fatalf("304 result %#v %v", got, e)
	}
}
func TestNoCacheAlwaysRevalidates(t *testing.T) {
	calls := 0
	f, s := mockFetcher(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(r, 200, http.Header{"Content-Type": []string{"text/plain"}, "Cache-Control": []string{"no-cache"}, "Etag": []string{"v2"}}, "kept"), nil
		}
		if r.Header.Get("If-None-Match") != "v2" {
			t.Errorf("no-cache revalidation omitted ETag")
		}
		return response(r, 304, http.Header{}, ""), nil
	})
	first, err := f.Fetch(context.Background(), "https://example.test/nc")
	if err != nil || first.FromCache {
		t.Fatalf("first response: %#v %v", first, err)
	}
	doc, found, err := s.Get(context.Background(), "https://example.test/nc")
	if err != nil || !found || !doc.NoCache {
		t.Fatalf("no-cache was not persisted: %#v %v", doc, err)
	}
	second, err := f.Fetch(context.Background(), "https://example.test/nc")
	if err != nil || !second.FromCache || calls != 2 {
		t.Fatalf("304 revalidation result: %#v %v calls=%d", second, err, calls)
	}
}
func TestHTTPErrorMappingAndContentLimits(t *testing.T) {
	tests := []struct {
		status  int
		code    string
		headers http.Header
		body    string
	}{
		{403, "access_denied", http.Header{"Content-Type": []string{"text/plain"}}, "no"},
		{404, "not_found", http.Header{"Content-Type": []string{"text/plain"}}, "no"},
		{429, "rate_limited", http.Header{"Content-Type": []string{"text/plain"}, "Retry-After": []string{"12"}}, "no"},
		{503, "upstream_error", http.Header{"Content-Type": []string{"text/plain"}}, "no"},
		{200, "unsupported_content_type", http.Header{"Content-Type": []string{"application/pdf"}}, "x"},
		{200, "response_too_large", http.Header{"Content-Type": []string{"text/plain"}}, strings.Repeat("x", 1025)},
	}
	for _, tt := range tests {
		f, _ := mockFetcher(t, func(r *http.Request) (*http.Response, error) { return response(r, tt.status, tt.headers, tt.body), nil })
		_, e := f.Fetch(context.Background(), "https://example.test/")
		var fe *FetchError
		if !errors.As(e, &fe) || fe.Code != tt.code {
			t.Errorf("status %d got %v", tt.status, e)
		}
		if tt.status == 429 && fe.RetryAfterSeconds != 12 {
			t.Errorf("retry-after = %d", fe.RetryAfterSeconds)
		}
	}
}
func TestFetchTimeoutAndRedirectSecurity(t *testing.T) {
	f, _ := mockFetcher(t, func(r *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "Get", URL: r.URL.String(), Err: context.DeadlineExceeded}
	})
	_, e := f.Fetch(context.Background(), "https://example.test/")
	var fe *FetchError
	if !errors.As(e, &fe) || fe.Code != "timeout" {
		t.Fatalf("timeout: %v", e)
	}
	f, _ = mockFetcher(t, func(r *http.Request) (*http.Response, error) {
		return response(r, 302, http.Header{"Location": []string{"http://127.0.0.1/private"}}, ""), nil
	})
	_, e = f.Fetch(context.Background(), "https://example.test/")
	if !errors.As(e, &fe) || fe.Code != "blocked_redirect" {
		t.Fatalf("redirect SSRF: %v", e)
	}
}

func TestOnlyOneRedirectIsFollowed(t *testing.T) {
	calls := 0
	f, _ := mockFetcher(t, func(r *http.Request) (*http.Response, error) {
		calls++
		return response(r, 302, http.Header{"Location": []string{fmt.Sprintf("https://example.test/%d", calls+1)}}, ""), nil
	})
	_, err := f.Fetch(context.Background(), "https://example.test/1")
	var fe *FetchError
	if !errors.As(err, &fe) || fe.Code != "blocked_redirect" || calls != 2 {
		t.Fatalf("redirect limit: err=%v calls=%d", err, calls)
	}
}
func TestRedirectAndTruncation(t *testing.T) {
	calls := 0
	f, _ := mockFetcher(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(r, 302, http.Header{"Location": []string{"https://example.test/final"}}, ""), nil
		}
		return response(r, 200, http.Header{"Content-Type": []string{"text/plain"}}, "one two three four"), nil
	})
	f.SetMaxTokens(8)
	got, e := f.Fetch(context.Background(), "https://example.test/start")
	if e != nil {
		t.Fatal(e)
	}
	if got.URL != "https://example.test/final" || got.Content != "one two" || !got.Truncated || calls != 2 {
		t.Fatalf("redirect/truncate %#v calls=%d", got, calls)
	}
	cached, err := f.Fetch(context.Background(), "https://example.test/final")
	if err != nil || !cached.FromCache || !cached.Truncated {
		t.Fatalf("cached truncation state %#v err=%v", cached, err)
	}
}
func TestSaveFailureDoesNotReturnPage(t *testing.T) {
	f, s := mockFetcher(t, func(r *http.Request) (*http.Response, error) {
		return response(r, 200, http.Header{"Content-Type": []string{"text/plain"}}, "content"), nil
	})
	if _, e := s.DB().Exec("CREATE TRIGGER reject_insert BEFORE INSERT ON documents BEGIN SELECT RAISE(ABORT,'no writes'); END"); e != nil {
		t.Fatal(e)
	}
	_, e := f.Fetch(context.Background(), "https://example.test/")
	var fe *FetchError
	if !errors.As(e, &fe) || fe.Code != "internal_error" {
		t.Fatalf("got %v", e)
	}
}
