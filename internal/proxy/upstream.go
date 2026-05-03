package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	claude2CodexPrefix = "/claude2codex/"
)

type targetRoute struct {
	AnthropicMessagesURL string
	Endpoint             string
}

func parseClaude2CodexRoute(path string) (targetRoute, error) {
	rest, ok := strings.CutPrefix(path, claude2CodexPrefix)
	if !ok {
		return targetRoute{}, fmt.Errorf("path must start with %s", claude2CodexPrefix)
	}
	if rest == "" {
		return targetRoute{}, errors.New("missing upstream URL")
	}

	for _, suffix := range []string{"/v1/responses/compact", "/responses/compact", "/v1/responses", "/responses"} {
		if strings.HasSuffix(rest, suffix) {
			upstream := strings.TrimSuffix(rest, suffix)
			if upstream == "" {
				return targetRoute{}, errors.New("missing upstream URL")
			}
			endpoint, _ := normalizeResponsesEndpoint(suffix)
			u, err := normalizeAnthropicMessagesURL(upstream)
			if err != nil {
				return targetRoute{}, err
			}
			return targetRoute{AnthropicMessagesURL: u.String(), Endpoint: endpoint}, nil
		}
	}
	return targetRoute{}, errors.New("path must contain an upstream Anthropic base URL followed by /responses")
}

func normalizeResponsesEndpoint(endpoint string) (string, error) {
	switch endpoint {
	case "/v1/responses", "/responses":
		return "/responses", nil
	case "/v1/responses/compact", "/responses/compact":
		return "/responses/compact", nil
	default:
		return "", fmt.Errorf("unsupported endpoint %q", endpoint)
	}
}

func normalizeAnthropicMessagesURL(upstream string) (*url.URL, error) {
	u, err := validateUpstreamURL(upstream)
	if err != nil {
		return nil, err
	}
	u.RawQuery = ""
	u.Fragment = ""

	path := strings.TrimRight(u.EscapedPath(), "/")
	switch {
	case strings.HasSuffix(path, "/messages"):
		u.Path = strings.TrimRight(u.Path, "/")
	case strings.HasSuffix(path, "/v1"):
		u.Path = strings.TrimRight(u.Path, "/") + "/messages"
	default:
		u.Path = strings.TrimRight(u.Path, "/") + "/v1/messages"
	}
	return u, nil
}

func validateUpstreamURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("upstream URL is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream URL: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, errors.New("upstream URL must use http or https")
	}
	if u.User != nil {
		return nil, errors.New("upstream URL must not include credentials")
	}
	if u.Hostname() == "" {
		return nil, errors.New("upstream URL must include a host")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "metadata" || host == "metadata.google.internal" {
		return nil, errors.New("upstream URL host is blocked")
	}
	if ip := net.ParseIP(host); ip != nil && isBlockedIP(ip) {
		return nil, errors.New("upstream URL host is blocked")
	}
	if port := u.Port(); port != "" && port != "80" && port != "443" {
		return nil, errors.New("upstream URL port must be 80 or 443")
	}
	return u, nil
}

func newSecureHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if isBlockedIP(ip.IP) {
					return nil, fmt.Errorf("upstream host resolves to blocked IP %s", ip.IP.String())
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   0,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		// 169.254.169.254 and broader link-local metadata range are covered by
		// IsLinkLocalUnicast, but keep this explicit for readability.
		if ip4[0] == 169 && ip4[1] == 254 {
			return true
		}
	}
	return false
}
