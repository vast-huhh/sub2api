package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
)

// ErrAccountProxyBindingUnavailable means an account is bound to a proxy but
// the binding could not be turned into a usable proxy URL. Callers must not
// fall back to a direct connection: only an absent binding (ProxyID == nil,
// including a persisted "direct" expiry fallback, which clears proxy_id) means
// direct access.
var ErrAccountProxyBindingUnavailable = errors.New("configured account proxy is unavailable")

// resolveConfiguredAccountProxyURL returns the proxy URL an account must use.
//
//   - no binding                → ("", nil): direct access is intended
//   - binding with loaded proxy → validated URL
//   - binding without loaded proxy → reload through repo when available,
//     otherwise fail closed
//
// A lookup failure never degrades to "": that would send credentials from the
// host's own IP. Errors wrap ErrAccountProxyBindingUnavailable and never contain
// the proxy URL (it may carry credentials).
func resolveConfiguredAccountProxyURL(ctx context.Context, repo ProxyRepository, account *Account) (string, error) {
	if account == nil {
		return "", nil
	}
	return resolveConfiguredProxyURL(ctx, repo, account.ProxyID, account.Proxy)
}

// resolveConfiguredProxyURL is the binding-level form of
// resolveConfiguredAccountProxyURL. Pass loaded=nil to force a repository read.
func resolveConfiguredProxyURL(ctx context.Context, repo ProxyRepository, proxyID *int64, loaded *Proxy) (string, error) {
	if proxyID == nil {
		return "", nil
	}
	id := *proxyID
	proxy := loaded
	if proxy == nil {
		if repo == nil {
			return "", fmt.Errorf("%w: proxy %d is not loaded", ErrAccountProxyBindingUnavailable, id)
		}
		fetched, err := repo.GetByID(ctx, id)
		if err != nil {
			return "", fmt.Errorf("%w: proxy %d lookup failed: %v", ErrAccountProxyBindingUnavailable, id, err)
		}
		proxy = fetched
	}
	if proxy == nil {
		return "", fmt.Errorf("%w: proxy %d was not found", ErrAccountProxyBindingUnavailable, id)
	}
	if proxy.ID != 0 && proxy.ID != id {
		return "", fmt.Errorf("%w: loaded proxy %d does not match binding %d", ErrAccountProxyBindingUnavailable, proxy.ID, id)
	}
	if strings.TrimSpace(proxy.Host) == "" {
		return "", fmt.Errorf("%w: proxy %d has no host", ErrAccountProxyBindingUnavailable, id)
	}
	raw := proxy.URL()
	if resolved, _, err := proxyurl.Parse(raw); err != nil || resolved == "" {
		return "", fmt.Errorf("%w: proxy %d has an invalid URL", ErrAccountProxyBindingUnavailable, id)
	}
	// Keep the original URL: transport construction owns normalization such as
	// socks5 -> socks5h, and plugins receive the same value as before.
	return raw, nil
}

// boundAccountProxyURL is resolveConfiguredAccountProxyURL for hot paths that
// only have the scheduler-hydrated account (no repository round trip).
func boundAccountProxyURL(account *Account) (string, error) {
	return resolveConfiguredAccountProxyURL(context.Background(), nil, account)
}

// requireAccountProxyBinding is the last-mile guard for senders whose caller
// already resolved proxyURL. A bound account must never leave with an empty
// proxy URL, regardless of how the caller computed it.
func requireAccountProxyBinding(account *Account, proxyURL string) error {
	if account != nil && account.ProxyID != nil && strings.TrimSpace(proxyURL) == "" {
		return fmt.Errorf("%w: proxy %d resolved to an empty URL", ErrAccountProxyBindingUnavailable, *account.ProxyID)
	}
	return nil
}
