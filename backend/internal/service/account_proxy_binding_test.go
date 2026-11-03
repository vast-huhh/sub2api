package service

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

type proxyBindingRepoStub struct {
	ProxyRepository
	proxy *Proxy
	err   error
	calls int
}

func (r *proxyBindingRepoStub) GetByID(ctx context.Context, id int64) (*Proxy, error) {
	r.calls++
	return r.proxy, r.err
}

func proxyBindingTestProxy(id int64) *Proxy {
	return &Proxy{ID: id, Protocol: "http", Host: "127.0.0.1", Port: 44004}
}

func TestResolveConfiguredAccountProxyURL(t *testing.T) {
	ctx := context.Background()
	proxyID := int64(64)

	t.Run("no binding means direct", func(t *testing.T) {
		url, err := resolveConfiguredAccountProxyURL(ctx, nil, &Account{ID: 1})
		require.NoError(t, err)
		require.Empty(t, url)
	})

	t.Run("loaded binding returns its URL without a repository read", func(t *testing.T) {
		repo := &proxyBindingRepoStub{}
		url, err := resolveConfiguredAccountProxyURL(ctx, repo, &Account{ID: 1, ProxyID: &proxyID, Proxy: proxyBindingTestProxy(64)})
		require.NoError(t, err)
		require.Equal(t, "http://127.0.0.1:44004", url)
		require.Zero(t, repo.calls)
	})

	t.Run("unloaded binding without repository fails closed", func(t *testing.T) {
		url, err := resolveConfiguredAccountProxyURL(ctx, nil, &Account{ID: 1, ProxyID: &proxyID})
		require.ErrorIs(t, err, ErrAccountProxyBindingUnavailable)
		require.Empty(t, url)
	})

	t.Run("unloaded binding reloads through repository", func(t *testing.T) {
		repo := &proxyBindingRepoStub{proxy: proxyBindingTestProxy(64)}
		url, err := resolveConfiguredAccountProxyURL(ctx, repo, &Account{ID: 1, ProxyID: &proxyID})
		require.NoError(t, err)
		require.Equal(t, "http://127.0.0.1:44004", url)
		require.Equal(t, 1, repo.calls)
	})

	t.Run("repository error fails closed", func(t *testing.T) {
		repo := &proxyBindingRepoStub{err: errors.New("db timeout")}
		url, err := resolveConfiguredAccountProxyURL(ctx, repo, &Account{ID: 1, ProxyID: &proxyID})
		require.ErrorIs(t, err, ErrAccountProxyBindingUnavailable)
		require.Empty(t, url)
	})

	t.Run("missing proxy row fails closed", func(t *testing.T) {
		repo := &proxyBindingRepoStub{}
		_, err := resolveConfiguredAccountProxyURL(ctx, repo, &Account{ID: 1, ProxyID: &proxyID})
		require.ErrorIs(t, err, ErrAccountProxyBindingUnavailable)
	})

	t.Run("mismatched loaded proxy fails closed", func(t *testing.T) {
		_, err := resolveConfiguredAccountProxyURL(ctx, nil, &Account{ID: 1, ProxyID: &proxyID, Proxy: proxyBindingTestProxy(72)})
		require.ErrorIs(t, err, ErrAccountProxyBindingUnavailable)
	})

	t.Run("empty host fails closed", func(t *testing.T) {
		_, err := resolveConfiguredAccountProxyURL(ctx, nil, &Account{ID: 1, ProxyID: &proxyID, Proxy: &Proxy{ID: 64, Protocol: "http", Port: 1}})
		require.ErrorIs(t, err, ErrAccountProxyBindingUnavailable)
	})

	t.Run("unsupported scheme fails closed and hides credentials", func(t *testing.T) {
		proxy := &Proxy{ID: 64, Protocol: "ftp", Host: "proxy.example", Port: 21, Username: "user", Password: "secret"}
		_, err := resolveConfiguredAccountProxyURL(ctx, nil, &Account{ID: 1, ProxyID: &proxyID, Proxy: proxy})
		require.ErrorIs(t, err, ErrAccountProxyBindingUnavailable)
		require.NotContains(t, err.Error(), "secret")
	})
}

func TestRequireAccountProxyBinding(t *testing.T) {
	proxyID := int64(64)
	require.NoError(t, requireAccountProxyBinding(nil, ""))
	require.NoError(t, requireAccountProxyBinding(&Account{ID: 1}, ""))
	require.NoError(t, requireAccountProxyBinding(&Account{ID: 1, ProxyID: &proxyID}, "http://127.0.0.1:44004"))
	require.ErrorIs(t, requireAccountProxyBinding(&Account{ID: 1, ProxyID: &proxyID}, "  "), ErrAccountProxyBindingUnavailable)
}

func TestProxyBindingErrorIsPersistentTransportError(t *testing.T) {
	proxyID := int64(64)
	err := requireAccountProxyBinding(&Account{ID: 1, ProxyID: &proxyID}, "")
	require.True(t, classifyUpstreamTransportError(err).Persistent)
}

func TestDoOpenAIUpstreamRefusesDirectSendForBoundAccount(t *testing.T) {
	proxyID := int64(64)
	upstream := &recordingHTTPUpstream{}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	req, err := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", nil)
	require.NoError(t, err)

	resp, err := svc.doOpenAIUpstream(req, "", &Account{ID: 1, Platform: PlatformOpenAI, ProxyID: &proxyID})
	require.Nil(t, resp)
	require.ErrorIs(t, err, ErrAccountProxyBindingUnavailable)
	require.Zero(t, upstream.calls, "bound account must never reach the direct transport")

	_, _ = svc.doOpenAIUpstream(req, "", &Account{ID: 2, Platform: PlatformOpenAI})
	require.Equal(t, 1, upstream.calls, "unbound account keeps direct access")
}

func TestOpenAIWSPoolDialRefusesDirectSendForBoundAccount(t *testing.T) {
	proxyID := int64(64)
	pool := &openAIWSConnPool{clientDialer: &recordingWSDialer{}}
	_, err := pool.dialConn(context.Background(), openAIWSAcquireRequest{
		Account: &Account{ID: 1, Platform: PlatformOpenAI, ProxyID: &proxyID},
		WSURL:   "wss://chatgpt.com/backend-api/codex/responses",
	})
	require.ErrorIs(t, err, ErrAccountProxyBindingUnavailable)
	require.Zero(t, pool.clientDialer.(*recordingWSDialer).calls)
}

type recordingWSDialer struct{ calls int }

func (d *recordingWSDialer) Dial(ctx context.Context, wsURL string, headers http.Header, proxyURL string) (openAIWSClientConn, int, http.Header, error) {
	d.calls++
	return nil, 0, nil, errors.New("dial not expected")
}
