// Package urlutil implements shared URL validation and canonicalization.
package urlutil

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

var tracking = map[string]bool{"fbclid": true, "gclid": true, "dclid": true, "msclkid": true, "yclid": true, "mc_cid": true, "mc_eid": true, "igshid": true, "ref_src": true}

func Validate(raw string) error {
	_, err := Normalize(raw)
	return err
}

func Normalize(raw string) (string, error) {
	if len(raw) > 2048 {
		return "", errors.New("URL exceeds 2048 bytes")
	}
	if strings.TrimSpace(raw) == "" || raw != strings.TrimSpace(raw) {
		return "", errors.New("URL must be non-empty and have no surrounding whitespace")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("URL scheme must be http or https")
	}
	if u.Opaque != "" || u.Host == "" || u.User != nil {
		return "", errors.New("URL must have a valid host and no user information")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", errors.New("URL host is required")
	}
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host = net.JoinHostPort(strings.Trim(host, "[]"), port)
	}
	u.Scheme = scheme
	u.Host = host
	u.Fragment = ""
	u.RawFragment = ""
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	v, e := url.PathUnescape(p)
	if e == nil {
		p = v
	}
	trailing := strings.HasSuffix(p, "/")
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "." {
			continue
		}
		if part == ".." {
			if len(out) > 1 {
				out = out[:len(out)-1]
			}
			continue
		}
		out = append(out, part)
	}
	p = strings.Join(out, "/")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if trailing && !strings.HasSuffix(p, "/") {
		p += "/"
	}
	u.Path = p
	u.RawPath = ""
	q := u.Query()
	for k := range q {
		lk := strings.ToLower(k)
		if tracking[lk] || strings.HasPrefix(lk, "utm_") {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
