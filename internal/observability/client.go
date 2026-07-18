package observability

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const DefaultBodyLimit int64 = 1 << 20

type Client struct {
	HTTP      *http.Client
	BodyLimit int64
}
type Response struct {
	StatusCode int
	Status     string
	Body       []byte
}
type Error struct {
	Op         string
	URL        string
	Status     string
	StatusCode int
	Err        error
}

func (e *Error) Error() string {
	s := e.Op + " " + e.URL
	if e.Status != "" {
		s += ": " + e.Status
	}
	if e.Err != nil {
		s += ": " + redact(e.Err.Error())
	}
	return s
}
func (e *Error) Unwrap() error { return e.Err }

func ValidateURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("URL scheme must be http or https")
	}
	if u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("URL must have a host and no userinfo")
	}
	return u, nil
}
func NewClient(timeout time.Duration, limit int64) *Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if limit <= 0 {
		limit = DefaultBodyLimit
	}
	return &Client{HTTP: &http.Client{Timeout: timeout, CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		if _, err := ValidateURL(req.URL.String()); err != nil {
			return fmt.Errorf("unsafe redirect: %w", err)
		}
		return fmt.Errorf("redirect rejected")
	}}, BodyLimit: limit}
}
func (c *Client) Do(ctx context.Context, method string, base *url.URL, path string, q url.Values, headers http.Header) (Response, error) {
	if base == nil {
		return Response{}, fmt.Errorf("nil base URL")
	}
	validated, err := ValidateURL(base.String())
	if err != nil {
		return Response{}, err
	}
	u := *validated
	u.Path = strings.TrimRight(base.Path, "/") + "/" + strings.TrimLeft(path, "/")
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return Response{}, &Error{Op: method, URL: safeURL(&u), Err: err}
	}
	for k, v := range headers {
		for _, x := range v {
			req.Header.Add(k, x)
		}
	}
	h := c.HTTP
	if h == nil {
		h = NewClient(5*time.Second, c.BodyLimit).HTTP
	}
	resp, err := h.Do(req)
	if err != nil {
		return Response{}, &Error{Op: method, URL: safeURL(&u), Err: err}
	}
	defer resp.Body.Close()
	b, readErr := io.ReadAll(io.LimitReader(resp.Body, c.BodyLimit+1))
	if int64(len(b)) > c.BodyLimit {
		return Response{}, &Error{Op: method, URL: safeURL(&u), Status: resp.Status, StatusCode: resp.StatusCode, Err: fmt.Errorf("response body exceeds limit")}
	}
	if readErr != nil {
		return Response{}, &Error{Op: method, URL: safeURL(&u), Status: resp.Status, StatusCode: resp.StatusCode, Err: readErr}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Response{}, &Error{Op: method, URL: safeURL(&u), Status: resp.Status, StatusCode: resp.StatusCode, Err: fmt.Errorf("unexpected HTTP status")}
	}
	return Response{resp.StatusCode, resp.Status, b}, nil
}
func safeURL(u *url.URL) string {
	v := *u
	v.User = nil
	q := v.Query()
	for k := range q {
		if strings.Contains(strings.ToLower(k), "token") || strings.Contains(strings.ToLower(k), "key") || strings.Contains(strings.ToLower(k), "secret") || strings.Contains(strings.ToLower(k), "auth") {
			q.Set(k, "[REDACTED]")
		}
	}
	v.RawQuery = q.Encode()
	return v.String()
}
func redact(s string) string {
	return sensitiveValue.ReplaceAllString(s, `${1}=[REDACTED]`)
}

var sensitiveValue = regexp.MustCompile(`(?i)(token|api[_-]?key|secret|password|authorization)=([^&\s]+)`)
