package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// End to end through Forward: the outbound body carries the proxy's timezone and
// search location, and a failover attempt re-resolves for the next account.
func TestOpenAIForwardAppliesOutboundLocalePerAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	proxy := &Proxy{ID: 64, Protocol: "http", Host: "127.0.0.1", Port: 44004}
	repo := newMemoryProxyExitLocationRepo()
	now := time.Now()
	repo.rows[64] = ProxyExitLocation{
		ProxyID: 64, ConfigFingerprint: ProxyConnectionFingerprint(proxy), ExitIP: "54.178.66.215",
		CountryCode: "JP", Region: "Tokyo", City: "Tokyo", Timezone: "Asia/Tokyo",
		SucceededAt: &now, NextProbeAt: now.Add(24 * time.Hour),
	}
	locale := NewOpenAIOutboundLocaleService(repo, &scriptedExitProber{}, &outboundLocaleProxyRepo{proxies: map[int64]Proxy{64: *proxy}})

	body, err := json.Marshal(map[string]any{
		"model":  "gpt-5.5",
		"stream": false,
		"input": []any{
			textMessage("user", capturedCodexEnvironmentContext),
			textMessage("user", "hi"),
		},
		"tools": []any{map[string]any{"type": "web_search", "external_web_access": false}},
	})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"rate_limit_error","message":"rate limited"}}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_1","status":"completed","model":"gpt-5.5","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)),
		},
	}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	svc.SetOutboundLocaleService(locale)

	following := openAIFailoverCachedBodyTestAccount(1, "follow", nil)
	proxyID := proxy.ID
	following.ProxyID = &proxyID
	following.Proxy = proxy
	following.Extra = map[string]any{OpenAIOutboundTimezoneModeExtraKey: OpenAIOutboundTimezoneModeFollowProxy}

	_, err = svc.Forward(context.Background(), c, following, body)
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.Len(t, upstream.bodies, 1)
	first := upstream.bodies[0]
	require.Contains(t, string(first), "<timezone>Asia/Tokyo</timezone>")
	require.NotContains(t, string(first), "Asia/Shanghai")
	require.Contains(t, string(first), "<current_date>2026-11-03</current_date>")
	require.Equal(t, "Tokyo", gjson.GetBytes(first, "tools.0.user_location.city").String())
	require.Equal(t, "127.0.0.1:44004", strings.TrimPrefix(upstream.lastProxyURL, "http://"))

	plain := openAIFailoverCachedBodyTestAccount(2, "plain", nil)
	_, err = svc.Forward(context.Background(), c, plain, body)
	require.NoError(t, err)
	require.Len(t, upstream.bodies, 2)
	second := upstream.bodies[1]
	require.Contains(t, string(second), "Asia/Shanghai", "an account without the setting keeps the client's timezone")
	require.False(t, gjson.GetBytes(second, "tools.0.user_location").Exists())
}
