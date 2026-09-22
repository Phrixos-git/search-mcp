package search

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func startTestServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &httptest.Server{Config: &http.Server{Handler: h}, Listener: l}
	s.Start()
	t.Cleanup(s.Close)
	return s
}

func TestInputValidation(t *testing.T) {
	if in, e := (Input{Query: "  hello  "}).Validate(); e != nil || in.Query != "hello" || in.MaxResults != 5 {
		t.Fatalf("input=%+v err=%v", in, e)
	}
	for _, in := range []Input{{Query: " "}, {Query: strings.Repeat("x", 257)}, {Query: "ok", MaxResults: 11}} {
		if _, e := in.Validate(); e == nil {
			t.Fatalf("accepted invalid input: %+v", in)
		}
	}
}

func TestWebSuccessDeduplicatesAndClipsInRankOrder(t *testing.T) {
	long := strings.Repeat("あ", 257)
	server := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("request: %s %s %s", r.Method, r.URL, r.Header.Get("Content-Type"))
		}
		if err := r.ParseForm(); err != nil || r.Form.Get("format") != "json" || r.Form.Get("q") != "term" {
			t.Errorf("form: %#v err=%v", r.Form, err)
		}
		fmt.Fprintf(w, `{"results":[{"title":"first","url":"https://example.test/a?utm_source=x","content":%q},{"title":"duplicate","url":"https://example.test/a","content":"other"},{"title":"second","url":"https://example.test/b","content":"ok"}],"unresponsive_engines":[]}`, long)
	}))
	c, _ := NewSearXNG(server.URL, server.Client(), time.Second)
	got, partial, e := c.Search(context.Background(), Input{Query: "term", MaxResults: 5})
	if e != nil || partial || len(got) != 2 || got[0].Title != "first" || len([]rune(got[0].Snippet)) != 256 || got[1].Title != "second" {
		t.Fatalf("got=%+v partial=%v err=%v", got, partial, e)
	}
}

func TestWebEmptyPartialAllFailedAndTimeout(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int
		partial    bool
		fail       bool
	}{{"empty", `{"results":[],"unresponsive_engines":[]}`, 0, false, false}, {"partial", `{"results":[{"title":"ok","url":"https://example.test/"}],"unresponsive_engines":[["broken","timeout"]]}`, 1, true, false}, {"failed", `{"results":[],"unresponsive_engines":[["broken","timeout"]]}`, 0, false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			s := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) }))
			c, _ := NewSearXNG(s.URL, s.Client(), time.Second)
			got, part, e := c.Search(context.Background(), Input{Query: "x"})
			if len(got) != tc.want || part != tc.partial || (e != nil) != tc.fail {
				t.Fatalf("got=%v partial=%v err=%v", got, part, e)
			}
		})
	}
	s := startTestServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { time.Sleep(80 * time.Millisecond) }))
	c, _ := NewSearXNG(s.URL, &http.Client{Timeout: 10 * time.Millisecond}, time.Second)
	_, _, e := c.Search(context.Background(), Input{Query: "x"})
	if e == nil || e.Code != "timeout" {
		t.Fatalf("timeout error=%v", e)
	}
}
