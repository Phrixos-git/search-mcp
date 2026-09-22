package storage

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

const FallbackTTL = 24 * time.Hour

type CachePolicy struct {
	ExpiresAt    time.Time
	NoCache      bool
	NoStore      bool
	ETag         string
	LastModified string
}

func EvaluateCache(h http.Header, now time.Time) CachePolicy {
	p := CachePolicy{ExpiresAt: now.Add(FallbackTTL), ETag: h.Get("ETag"), LastModified: h.Get("Last-Modified")}
	cc := h.Get("Cache-Control")
	hasMaxAge := false
	for _, part := range strings.Split(cc, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		name := strings.ToLower(kv[0])
		switch name {
		case "no-cache":
			p.NoCache = true
		case "no-store":
			p.NoStore = true
		case "max-age":
			if len(kv) == 2 {
				v := strings.Trim(strings.TrimSpace(kv[1]), "\"")
				if n, e := strconv.ParseInt(v, 10, 64); e == nil {
					if n < 0 {
						n = 0
					}
					p.ExpiresAt = now.Add(time.Duration(n) * time.Second)
					hasMaxAge = true
				}
			}
		}
	}
	if !hasMaxAge {
		if exp, e := http.ParseTime(h.Get("Expires")); e == nil {
			p.ExpiresAt = exp
		}
	}
	return p
}

func ConditionalHeaders(d Document) http.Header {
	h := make(http.Header)
	if d.ETag != "" {
		h.Set("If-None-Match", d.ETag)
	} else if d.LastModified != "" {
		h.Set("If-Modified-Since", d.LastModified)
	}
	return h
}
