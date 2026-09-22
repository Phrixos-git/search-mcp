package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"search-mcp/internal/indexer"
	"search-mcp/internal/storage"
	"search-mcp/internal/urlutil"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const DefaultTimeout = 18 * time.Second
const DefaultMaxBytes int64 = 5 << 20
const DefaultMaxTokens = 8192

type Resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}
type resolver struct{}

func (resolver) LookupIPAddr(c context.Context, h string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(c, h)
}

type Fetcher struct {
	store     *storage.Store
	index     *indexer.Client
	resolver  Resolver
	client    *http.Client
	timeout   time.Duration
	maxBytes  int64
	maxTokens int
}
type Result struct {
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	Content   string    `json:"content"`
	FetchedAt time.Time `json:"fetched_at"`
	FromCache bool      `json:"from_cache"`
	Truncated bool      `json:"truncated"`
}
type Input struct {
	URL string `json:"url"`
}

func (i Input) Validate() error {
	return urlutil.Validate(i.URL)
}

type FetchError struct {
	Code              string
	RetryAfterSeconds int
	Cause             error
}

func (e *FetchError) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return e.Code
}
func (e *FetchError) Unwrap() error { return e.Cause }
func NewFetcher(s *storage.Store, i *indexer.Client, timeout time.Duration, max int64) *Fetcher {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if max <= 0 {
		max = DefaultMaxBytes
	}
	f := &Fetcher{store: s, index: i, resolver: resolver{}, timeout: timeout, maxBytes: max, maxTokens: DefaultMaxTokens}
	f.client = f.newClient()
	return f
}
func (f *Fetcher) SetResolver(r Resolver) {
	if r != nil {
		f.resolver = r
		f.client = f.newClient()
	}
}
func (f *Fetcher) SetMaxTokens(n int) {
	if n > 0 {
		f.maxTokens = n
	}
}
func blocked(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		for _, c := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4"} {
			_, n, _ := net.ParseCIDR(c)
			if n.Contains(v4) {
				return true
			}
		}
	}
	return false
}
func CheckAddress(ctx context.Context, r Resolver, host string) error {
	if r == nil {
		r = resolver{}
	}
	h := strings.TrimSuffix(strings.ToLower(host), ".")
	if h == "localhost" || strings.HasSuffix(h, ".localhost") || h == "metadata" || h == "metadata.google.internal" {
		return fmt.Errorf("blocked address %q", host)
	}
	if ip := net.ParseIP(h); ip != nil {
		if blocked(ip) {
			return fmt.Errorf("blocked address %q", host)
		}
		return nil
	}
	ips, e := r.LookupIPAddr(ctx, h)
	if e != nil {
		return e
	}
	if len(ips) == 0 {
		return errors.New("host resolved to no addresses")
	}
	for _, ip := range ips {
		if blocked(ip.IP) {
			return fmt.Errorf("host %q resolves to blocked address %s", host, ip.IP)
		}
	}
	return nil
}
func (f *Fetcher) newClient() *http.Client {
	d := &net.Dialer{Timeout: f.timeout, KeepAlive: 30 * time.Second}
	tr := &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: f.timeout, IdleConnTimeout: 30 * time.Second}
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		var ips []net.IPAddr
		if ip := net.ParseIP(host); ip != nil {
			ips = []net.IPAddr{{IP: ip}}
		} else {
			ips, e = f.resolver.LookupIPAddr(ctx, host)
			if e != nil {
				return nil, e
			}
		}
		if len(ips) == 0 {
			return nil, errors.New("host resolved to no addresses")
		}
		for _, ip := range ips {
			if blocked(ip.IP) {
				return nil, fmt.Errorf("host resolves to blocked address %s", ip.IP)
			}
		}
		var last error
		for _, ip := range ips {
			c, e := d.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if e == nil {
				return c, nil
			}
			last = e
		}
		return nil, last
	}
	return &http.Client{Transport: tr, Timeout: f.timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 1 {
			return &FetchError{Code: "blocked_redirect", Cause: errors.New("redirect limit exceeded")}
		}
		if e := urlutil.Validate(req.URL.String()); e != nil {
			return &FetchError{Code: "blocked_redirect", Cause: e}
		}
		if e := CheckAddress(req.Context(), f.resolver, req.URL.Hostname()); e != nil {
			return &FetchError{Code: "blocked_redirect", Cause: e}
		}
		return nil
	}}
}
func (f *Fetcher) Fetch(ctx context.Context, raw string) (Result, error) {
	if err := (Input{URL: raw}).Validate(); err != nil {
		return Result{}, &FetchError{Code: "invalid_argument", Cause: err}
	}
	canonical, e := urlutil.Normalize(raw)
	if e != nil {
		return Result{}, &FetchError{Code: "invalid_argument", Cause: e}
	}
	u, _ := url.Parse(canonical)
	if e = CheckAddress(ctx, f.resolver, u.Hostname()); e != nil {
		return Result{}, &FetchError{Code: "blocked_address", Cause: e}
	}
	now := time.Now()
	var old storage.Document
	cached := false
	if f.store != nil {
		old, cached, e = f.store.Get(ctx, canonical)
		if e == nil && !cached {
			old, cached, e = f.store.GetByOriginalURL(ctx, raw)
		}
		if e != nil {
			return Result{}, &FetchError{Code: "internal_error", Cause: e}
		}
	}
	if cached && storage.CacheFresh(old, now, false) {
		return result(old, true), nil
	}
	requestURL := canonical
	if cached && old.CanonicalURL != "" {
		requestURL = old.CanonicalURL
	}
	requestTarget, parseErr := url.Parse(requestURL)
	if parseErr != nil {
		return Result{}, &FetchError{Code: "invalid_argument", Cause: parseErr}
	}
	if err := CheckAddress(ctx, f.resolver, requestTarget.Hostname()); err != nil {
		return Result{}, &FetchError{Code: "blocked_address", Cause: err}
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if e != nil {
		return Result{}, &FetchError{Code: "invalid_argument", Cause: e}
	}
	if cached {
		for k, v := range storage.ConditionalHeaders(old) {
			req.Header[k] = v
		}
	}
	resp, e := f.client.Do(req)
	if e != nil {
		var fe *FetchError
		if errors.As(e, &fe) {
			return Result{}, fe
		}
		if n, ok := e.(net.Error); ok && n.Timeout() {
			return Result{}, &FetchError{Code: "timeout", Cause: e}
		}
		return Result{}, &FetchError{Code: "upstream_error", Cause: e}
	}
	defer resp.Body.Close()
	if resp.StatusCode == 304 && cached {
		p := storage.EvaluateCache(resp.Header, now)
		if p.ETag == "" {
			p.ETag = old.ETag
		}
		if p.LastModified == "" {
			p.LastModified = old.LastModified
		}
		if resp.Header.Get("Cache-Control") == "" {
			p.NoCache = old.NoCache
		}
		old.FetchedAt = now
		old.ExpiresAt = p.ExpiresAt
		old.ETag = p.ETag
		old.LastModified = p.LastModified
		old.NoCache = p.NoCache
		if p.NoCache {
			old.ExpiresAt = now
		}
		if f.store != nil {
			if p.NoStore {
				if e = f.store.DeleteByCanonical(ctx, old.CanonicalURL); e != nil {
					return Result{}, &FetchError{Code: "internal_error", Cause: e}
				}
			} else if _, e = f.store.Save(ctx, old); e != nil {
				return Result{}, &FetchError{Code: "internal_error", Cause: e}
			}
		}
		return result(old, true), nil
	}
	switch resp.StatusCode {
	case 401, 403, 451:
		return Result{}, &FetchError{Code: "access_denied", Cause: fmt.Errorf("HTTP %d", resp.StatusCode)}
	case 404, 410:
		return Result{}, &FetchError{Code: "not_found", Cause: fmt.Errorf("HTTP %d", resp.StatusCode)}
	case 408:
		return Result{}, &FetchError{Code: "timeout", Cause: errors.New("HTTP 408")}
	case 429:
		n := 5
		if x, z := strconv.Atoi(resp.Header.Get("Retry-After")); z == nil && x >= 0 {
			n = x
		}
		return Result{}, &FetchError{Code: "rate_limited", RetryAfterSeconds: n, Cause: errors.New("HTTP 429")}
	}
	if resp.StatusCode >= 500 {
		return Result{}, &FetchError{Code: "upstream_error", Cause: fmt.Errorf("HTTP %d", resp.StatusCode)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, &FetchError{Code: "upstream_error", Cause: fmt.Errorf("HTTP %d", resp.StatusCode)}
	}
	if resp.ContentLength > f.maxBytes {
		return Result{}, &FetchError{Code: "response_too_large", Cause: errors.New("Content-Length exceeds limit")}
	}
	media, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if media != "text/html" && media != "text/plain" {
		return Result{}, &FetchError{Code: "unsupported_content_type", Cause: fmt.Errorf("Content-Type %q", media)}
	}
	body, e := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes+1))
	if e != nil {
		return Result{}, &FetchError{Code: "upstream_error", Cause: e}
	}
	if int64(len(body)) > f.maxBytes {
		return Result{}, &FetchError{Code: "response_too_large", Cause: errors.New("response exceeds limit")}
	}
	title, content := extract(body, media)
	content, truncated := truncate(content, f.maxTokens)
	finalURL := resp.Request.URL.String()
	final, e := urlutil.Normalize(finalURL)
	if e != nil {
		return Result{}, &FetchError{Code: "blocked_redirect", Cause: e}
	}
	p := storage.EvaluateCache(resp.Header, now)
	if p.NoCache {
		p.ExpiresAt = now
	}
	sum := sha256.Sum256([]byte(content))
	fu, _ := url.Parse(final)
	doc := storage.Document{OriginalURL: raw, CanonicalURL: final, Title: title, Content: content, ContentHash: hex.EncodeToString(sum[:]), ContentType: media, Language: "unknown", Domain: fu.Hostname(), ETag: p.ETag, LastModified: p.LastModified, HTTPStatus: resp.StatusCode, FetchedAt: now, ExpiresAt: p.ExpiresAt, Truncated: truncated, NoCache: p.NoCache}
	if f.store != nil {
		if p.NoStore {
			if cached {
				if e = f.store.DeleteByCanonical(ctx, old.CanonicalURL); e != nil {
					return Result{}, &FetchError{Code: "internal_error", Cause: e}
				}
			}
		} else {
			var id int64
			if id, e = f.store.SaveResponse(ctx, doc, p); e != nil {
				return Result{}, &FetchError{Code: "internal_error", Cause: e}
			}
			doc.ID = id
			if f.index != nil {
				go func() {
					c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					_ = f.index.SyncDocument(c, f.store, doc)
				}()
			}
		}
	}
	return Result{Title: title, URL: finalURL, Content: content, FetchedAt: now, Truncated: truncated}, nil
}
func result(d storage.Document, hit bool) Result {
	return Result{Title: d.Title, URL: d.CanonicalURL, Content: d.Content, FetchedAt: d.FetchedAt, FromCache: hit, Truncated: d.Truncated}
}
func truncate(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	if max <= 0 {
		return "", true
	}
	n := 0
	for i, r := range s {
		size := utf8.RuneLen(r)
		if n+size > max {
			return strings.TrimSpace(s[:i]), true
		}
		n += size
	}
	return s, false
}
