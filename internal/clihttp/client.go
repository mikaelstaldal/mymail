// Package clihttp provides the common transport rules for MyMail CLI clients.
package clihttp

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const DefaultURL = "http://127.0.0.1:8080"

// URLDefault returns the configured server origin, or the local default.
func URLDefault() string {
	if value := os.Getenv("MYMAIL_URL"); value != "" {
		return value
	}
	return DefaultURL
}

// ParseOrigin rejects URL components that could redirect credentials or alter routes.
func ParseOrigin(raw string) (*url.URL, error) {
	if strings.ContainsRune(raw, '\\') || strings.IndexFunc(raw, unicode.IsSpace) >= 0 || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return nil, errors.New("-url must not contain whitespace, controls, or backslashes")
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("-url must be a server origin without credentials, path, query, or fragment")
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !isLiteralLoopback(u.Hostname())) {
		return nil, errors.New("-url requires HTTPS except for literal loopback addresses")
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, errors.New("-url has an invalid port")
		}
	}
	return u, nil
}

func isLiteralLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// NewClient bypasses environment proxies and never forwards credentials on redirects.
func NewClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
