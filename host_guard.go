package main

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type allowedHost struct {
	host string
	port int
}

func localHosts(addr string) []string {
	hosts := []string{"127.0.0.1", "::1", "localhost"}
	ip := net.ParseIP(addr)
	if addr != "" && (ip == nil || !ip.IsUnspecified()) {
		hosts = append(hosts, strings.ToLower(addr))
	}
	return hosts
}

func browserOrigin(origin string) string {
	u, err := url.Parse(origin)
	if err != nil {
		return origin
	}
	host, port, ok := parseAuthority(u.Host)
	if !ok {
		return origin
	}
	defaultPort := (u.Scheme == "http" && port == 80) || (u.Scheme == "https" && port == 443)
	if port != 0 && !defaultPort {
		host = net.JoinHostPort(host, strconv.Itoa(port))
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return strings.ToLower(u.Scheme) + "://" + host
}

func localCSRFOrigins(addr string, port int) []string {
	origins := make([]string, 0, 4)
	for _, host := range localHosts(addr) {
		if port == 80 {
			if strings.Contains(host, ":") {
				host = "[" + host + "]"
			}
			origins = append(origins, "http://"+host)
		} else {
			origins = append(origins, "http://"+net.JoinHostPort(host, strconv.Itoa(port)))
		}
	}
	return origins
}

// hostGuard rejects requests addressed to an authority other than the one the
// operator configured. This also protects unauthenticated loopback servers from
// a browser reaching them through a DNS-rebound hostname.
func hostGuard(addr string, port int, publicURL string, next http.Handler) (http.Handler, error) {
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid HTTP port %d", port)
	}
	ip := net.ParseIP(addr)
	if publicURL == "" && (addr == "" || ip != nil && ip.IsUnspecified()) {
		return nil, fmt.Errorf("-public-url is required when -addr has no concrete host")
	}
	allowed := []allowedHost{}
	add := func(host string, port int) {
		allowed = append(allowed, allowedHost{strings.ToLower(host), port})
	}
	if publicURL != "" {
		u, err := url.Parse(publicURL)
		if err != nil || u.User != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("invalid public URL %q", publicURL)
		}
		host, publicPort, ok := parseAuthority(u.Host)
		if !ok {
			return nil, fmt.Errorf("invalid public URL authority %q", u.Host)
		}
		if publicPort == 0 {
			if u.Scheme == "https" {
				publicPort = 443
			} else {
				publicPort = 80
			}
		}
		add(host, publicPort)
	}
	for _, host := range localHosts(addr) {
		add(host, port)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, requestPort, ok := parseAuthority(r.Host)
		if ok {
			for _, candidate := range allowed {
				hostMatches := host == candidate.host
				if !hostMatches {
					requestIP, candidateIP := net.ParseIP(host), net.ParseIP(candidate.host)
					hostMatches = requestIP != nil && candidateIP != nil && requestIP.Equal(candidateIP)
				}
				if hostMatches && (requestPort == candidate.port || (requestPort == 0 && (candidate.port == 80 || candidate.port == 443))) {
					next.ServeHTTP(w, r)
					return
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMisdirectedRequest)
		_, _ = w.Write([]byte("{\"error\":\"invalid host\"}\n"))
	}), nil
}

// parseAuthority accepts only a literal HTTP Host authority. Forwarded-Host
// and other proxy headers are deliberately irrelevant to this check.
func parseAuthority(authority string) (string, int, bool) {
	if authority == "" || strings.ContainsAny(authority, "/?#@\\ \t\r\n,%") {
		return "", 0, false
	}
	u, err := url.Parse("//" + authority)
	if err != nil || u.User != nil || u.Host != authority || u.Path != "" {
		return "", 0, false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || strings.Contains(host, ":") && net.ParseIP(host) == nil {
		return "", 0, false
	}
	port := 0
	if strings.HasSuffix(authority, ":") {
		return "", 0, false
	}
	if portString := u.Port(); portString != "" {
		port, err = strconv.Atoi(portString)
		if err != nil || port < 1 || port > 65535 {
			return "", 0, false
		}
	}
	return host, port, true
}
