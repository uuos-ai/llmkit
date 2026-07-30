// Package endpointpolicy contains opt-in custom endpoint validation policies.
package endpointpolicy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/uuos-ai/llmkit"
)

// PublicHTTPS rejects cleartext, userinfo, localhost, and addresses that are
// not globally routable. Hosts with stricter allowlists should wrap or replace
// this policy. DNS is resolved on every validation to reduce stale decisions.
func PublicHTTPS(ctx context.Context, resolver *net.Resolver) func(llmkit.Target) error {
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
		addresses, err := resolver.LookupIPAddr(ctx, host)
		if err != nil || len(addresses) == 0 {
			return denied(target)
		}
		for _, address := range addresses {
			if !address.IP.IsGlobalUnicast() || address.IP.IsPrivate() || address.IP.IsLoopback() || address.IP.IsLinkLocalUnicast() {
				return denied(target)
			}
		}
		return nil
	}
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
