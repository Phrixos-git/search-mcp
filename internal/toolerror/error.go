// Package toolerror defines the stable error contract returned by search tools.
package toolerror

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	InvalidArgument = "invalid_argument"
	NotFound        = "not_found"
	AccessDenied    = "access_denied"
	RateLimited     = "rate_limited"
	Timeout         = "timeout"
	Upstream        = "upstream_error"
	Network         = "network_error"
	Internal        = "internal_error"
	MaxRetries      = 3
	BaseRetryDelay  = 5 * time.Second
)

// Error is the public, serializable tool error schema.
type Error struct {
	Code              string `json:"code"`
	Message           string `json:"message"`
	Retryable         bool   `json:"retryable"`
	HTTPStatus        int    `json:"http_status,omitempty"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
	ErrorID           string `json:"error_id,omitempty"`
}

func (e *Error) Error() string { return e.Message }

func Invalid(message string) *Error { return &Error{Code: InvalidArgument, Message: message} }

// FromHTTP maps upstream status codes and Retry-After into the common schema.
func FromHTTP(status int, retryAfter string) *Error {
	e := &Error{HTTPStatus: status}
	switch status {
	case http.StatusNotFound:
		e.Code, e.Message = NotFound, "upstream resource was not found"
	case http.StatusUnauthorized, http.StatusForbidden:
		e.Code, e.Message = AccessDenied, "upstream access was denied"
	case http.StatusTooManyRequests:
		e.Code, e.Message, e.Retryable = RateLimited, "upstream rate limit exceeded", true
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		e.Code, e.Message, e.Retryable = Timeout, "upstream request timed out", true
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
		e.Code, e.Message, e.Retryable = Upstream, "upstream service failed", true
	default:
		e.Code, e.Message = Upstream, "upstream request failed"
		e.Retryable = status >= 500
	}
	if e.Code == RateLimited {
		e.RetryAfterSeconds = ParseRetryAfter(retryAfter, time.Now())
	}
	return e
}

func ParseRetryAfter(value string, now time.Time) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return seconds
	}
	if when, err := http.ParseTime(value); err == nil && when.After(now) {
		return int(when.Sub(now).Seconds())
	}
	return 0
}

// FromNetwork maps timeout and transport errors without exposing internal details.
func FromNetwork(err error) *Error {
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return &Error{Code: Network, Message: "upstream DNS lookup failed", Retryable: dns.IsTemporary || dns.IsTimeout}
	}
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var record tls.RecordHeaderError
	if errors.As(err, &unknownAuthority) || errors.As(err, &hostname) || errors.As(err, &invalid) || errors.As(err, &record) {
		return &Error{Code: Network, Message: "upstream TLS validation failed", Retryable: false}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() || errors.Is(err, context.DeadlineExceeded) {
		return &Error{Code: Timeout, Message: "upstream request timed out", Retryable: true}
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		message := strings.ToLower(opErr.Err.Error())
		if strings.Contains(message, "certificate") || strings.Contains(message, "tls") {
			return &Error{Code: Network, Message: "upstream TLS validation failed", Retryable: false}
		}
	}
	return &Error{Code: Network, Message: "upstream network request failed", Retryable: true}
}

// FromCode maps errors produced by a tool implementation into the common public schema.
func FromCode(code string, retryAfter int) *Error {
	e := &Error{Code: code, Message: "upstream request failed"}
	switch code {
	case InvalidArgument:
		e.Message = "invalid tool arguments"
	case NotFound:
		e.Message = "requested resource was not found"
	case AccessDenied:
		e.Message = "upstream access was denied"
	case RateLimited:
		e.Message, e.Retryable, e.RetryAfterSeconds = "upstream rate limit exceeded", true, retryAfter
	case Timeout:
		e.Message, e.Retryable = "upstream request timed out", true
	case Upstream:
		e.Message, e.Retryable = "upstream service failed", true
	case Network:
		e.Message, e.Retryable = "upstream network request failed", true
	case Internal:
		e.Message = "internal error"
	default:
		e.Code, e.Message = Internal, "internal error"
	}
	return e
}

// Do retries retryable operations at a fixed base interval, at most three times.
func Do(ctx context.Context, op func(context.Context) error, classify func(error) *Error) *Error {
	return DoWithDelay(ctx, op, classify, BaseRetryDelay)
}

// DoWithDelay is Do with an injectable base delay for deterministic tests.
func DoWithDelay(ctx context.Context, op func(context.Context) error, classify func(error) *Error, baseDelay time.Duration) *Error {
	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			return &Error{Code: Timeout, Message: "request canceled", Retryable: false}
		}
		err := op(ctx)
		if err == nil {
			return nil
		}
		mapped := classify(err)
		if mapped == nil {
			mapped = &Error{Code: Internal, Message: "internal error"}
		}
		if !mapped.Retryable || attempt >= MaxRetries {
			return mapped
		}
		delay := baseDelay
		if mapped.RetryAfterSeconds > 0 {
			delay = time.Duration(mapped.RetryAfterSeconds) * time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			if mapped.Code == RateLimited {
				return mapped
			}
			return &Error{Code: Timeout, Message: "request canceled", Retryable: false}
		case <-timer.C:
		}
	}
}
