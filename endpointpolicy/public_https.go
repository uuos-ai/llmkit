// Package endpointpolicy contains opt-in custom endpoint validation policies.
package endpointpolicy

import (
	"context"
	"net"
	"net/url"
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
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
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

func denied(target llmkit.Target) error {
	return &llmkit.ProviderError{
		Provider: target.Provider, Model: target.Model, Kind: llmkit.ErrorPermission,
		SafeMessage: "custom endpoint is not permitted",
	}
}
