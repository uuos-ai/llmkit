// Package endpointpolicy contains opt-in custom endpoint validation policies.
package endpointpolicy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/uuos-ai/llmkit"
)

type IPResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

// PublicHTTPS rejects cleartext, userinfo, localhost, and addresses that are
// not globally routable. Hosts with stricter allowlists should wrap or replace
// this policy. DNS is resolved on every validation to reduce stale decisions.
func PublicHTTPS(ctx context.Context, resolver IPResolver) func(llmkit.Target) error {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return func(target llmkit.Target) error {
		parsed, err := url.Parse(target.Endpoint)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
			return denied(target)
		}
		if parsed.Port() != "" && parsed.Port() != "443" {
			return denied(target)
		}
		decodedPath, err := url.PathUnescape(parsed.EscapedPath())
		if err != nil || strings.Contains(decodedPath, "\\") || path.Clean("/"+decodedPath) != "/"+strings.TrimPrefix(decodedPath, "/") {
			return denied(target)
		}
		host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
		if host == "localhost" || strings.HasSuffix(host, ".localhost") {
			return denied(target)
		}
		_, err = lookupPublic(ctx, resolver, host)
		if err != nil {
			return denied(target)
		}
		return nil
	}
}

// PublicHTTPClient validates the DNS answer used by each actual TCP dial,
// closing the validation-to-connect rebinding window. TLS still verifies the
// original request hostname because net/http derives ServerName from the URL.
func PublicHTTPClient(resolver IPResolver) *http.Client {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:             nil,
		ForceAttemptHTTP2: true,
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, errors.New("endpointpolicy: invalid dial address")
			}
			addresses, err := lookupPublic(ctx, resolver, strings.TrimSuffix(host, "."))
			if err != nil {
				return nil, err
			}
			var lastErr error
			for _, candidate := range addresses {
				connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
				if dialErr == nil {
					return connection, nil
				}
				lastErr = dialErr
			}
			return nil, fmt.Errorf("endpointpolicy: public endpoint dial failed: %w", lastErr)
		},
	}
	return &http.Client{Transport: transport, CheckRedirect: NoCrossHostRedirect}
}

func lookupPublic(ctx context.Context, resolver IPResolver, host string) ([]net.IPAddr, error) {
	if parsed := net.ParseIP(host); parsed != nil {
		addresses := []net.IPAddr{{IP: parsed}}
		if !isPublic(parsed) {
			return nil, errors.New("endpointpolicy: non-public address denied")
		}
		return addresses, nil
	}
	addresses, err := resolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("endpointpolicy: endpoint DNS unavailable")
	}
	for _, address := range addresses {
		if !isPublic(address.IP) {
			return nil, errors.New("endpointpolicy: non-public DNS answer denied")
		}
	}
	return addresses, nil
}

func isPublic(address net.IP) bool {
	return address.IsGlobalUnicast() && !address.IsPrivate() && !address.IsLoopback() && !address.IsLinkLocalUnicast()
}

// NoCrossHostRedirect prevents credentials from crossing an origin boundary.
// Callers must still clear sensitive headers on every manually followed redirect.
func NoCrossHostRedirect(request *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	previous := via[len(via)-1].URL
	if !strings.EqualFold(previous.Scheme, request.URL.Scheme) || !strings.EqualFold(previous.Host, request.URL.Host) {
		return errors.New("endpointpolicy: cross-origin redirect denied")
	}
	return nil
}

func denied(target llmkit.Target) error {
	return &llmkit.ProviderError{
		Provider: target.Provider, Model: target.Model, Kind: llmkit.ErrorPermission,
		SafeMessage: "custom endpoint is not permitted",
	}
}
