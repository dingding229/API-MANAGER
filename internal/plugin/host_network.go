package plugin

import (
	"api-manager/internal/upstream/security"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"golang.org/x/net/http/httpguts"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type hostHTTPRequest struct {
	URL     string              `json:"url"`
	Method  string              `json:"method"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body_base64"`
}

func (s *pluginRuntimeServices) httpRequest(ctx context.Context, name string, q hostHTTPRequest) (wasmResponse, error) {
	snapshot := s.snapshot(name)
	if identity, ok := ctx.Value(hostIdentityKey{}).(string); ok && identity != snapshot.ID {
		return wasmResponse{}, errors.New("plugin runtime identity changed")
	}
	p := snapshot.Policy
	origin, e := url.Parse(q.URL)
	if !p.NetworkEnabled || snapshot.ID == "" {
		return wasmResponse{}, errors.New("external network access disabled")
	}
	if e != nil || origin.Scheme != "https" || origin.User != nil || origin.Hostname() == "" || origin.Fragment != "" || len(q.URL) > 8192 {
		return wasmResponse{}, errors.New("invalid external HTTPS URL")
	}
	approved := false
	for _, value := range p.AllowedOrigins {
		if security.SafeOrigin(q.URL) == value {
			approved = true
			break
		}
	}
	if !approved {
		return wasmResponse{}, errors.New("external origin not allowed")
	}
	if !routeMethod[q.Method] {
		return wasmResponse{}, errors.New("HTTP method not allowed")
	}
	body, e := base64.StdEncoding.DecodeString(q.Body)
	if e != nil || len(body) > 256<<10 {
		return wasmResponse{}, errors.New("invalid or oversized HTTP body")
	}
	if len(q.Headers) > 32 {
		return wasmResponse{}, errors.New("too many request headers")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.TimeoutMS)*time.Millisecond)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, q.Method, q.URL, bytes.NewReader(body))
	if e != nil {
		return wasmResponse{}, errors.New("invalid external request")
	}
	headerSize := 0
	for key, values := range q.Headers {
		canonical := http.CanonicalHeaderKey(key)
		if !httpguts.ValidHeaderFieldName(key) || len(values) > 8 || canonical == "Host" || canonical == "Connection" || canonical == "Proxy-Connection" || canonical == "Keep-Alive" || canonical == "Te" || canonical == "Proxy-Authorization" || canonical == "Transfer-Encoding" || canonical == "Content-Length" || canonical == "Upgrade" || canonical == "Trailer" || strings.HasPrefix(canonical, "Sec-") || strings.HasPrefix(canonical, "X-Forwarded-") || canonical == "Forwarded" || canonical == "X-Passkey-Confirmation" {
			return wasmResponse{}, errors.New("request header not allowed")
		}
		for _, value := range values {
			if !httpguts.ValidHeaderFieldValue(value) {
				return wasmResponse{}, errors.New("invalid request header")
			}
			headerSize += len(key) + len(value)
			req.Header.Add(key, value)
		}
	}
	if headerSize > 16<<10 {
		return wasmResponse{}, errors.New("request headers exceed limit")
	}
	// No environment proxies, no automatic cookies, no redirect credential
	// forwarding. Numeric-address dialing pins every DNS result to a public IP.
	transport := &http.Transport{Proxy: nil, DialContext: security.DialContext(nil, "", origin), DisableKeepAlives: true, MaxResponseHeaderBytes: 16 << 10, ResponseHeaderTimeout: time.Duration(p.TimeoutMS) * time.Millisecond, TLSHandshakeTimeout: time.Duration(p.TimeoutMS) * time.Millisecond}
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, Timeout: time.Duration(p.TimeoutMS) * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, e := client.Do(req)
	if e != nil {
		return wasmResponse{}, errors.New("external request failed or destination was blocked")
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, int64(p.ResponseBytes)+1))
	if e != nil || len(raw) > p.ResponseBytes {
		return wasmResponse{}, errors.New("external response exceeds limit or cannot be read")
	}
	return wasmResponse{Status: response.StatusCode, Headers: response.Header, Body: base64.StdEncoding.EncodeToString(raw)}, nil
}
