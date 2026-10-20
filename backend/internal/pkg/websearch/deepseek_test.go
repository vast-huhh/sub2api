package websearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type searchRoundTripFunc func(*http.Request) (*http.Response, error)

func (f searchRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func searchTestResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestDeepSeekProvider_Search(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		count int
		want  int
	}{
		{"defaults", "  中文搜索 site:example.com  ", 0, 5},
		{"unicode boundary", strings.Repeat("搜", 128), 3, 3},
		{"maximum count", "query", 100, 50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: searchRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				require.Equal(t, "https://api.deepseek.com/v1/tools/search", r.URL.String())
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
				require.Equal(t, "application/json", r.Header.Get("Content-Type"))
				var payload map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
				require.Equal(t, strings.TrimSpace(tc.query), payload["query"])
				require.Equal(t, float64(tc.want), payload["count"])
				require.Equal(t, false, payload["need_content"])
				require.NotContains(t, payload, "api_key")
				return searchTestResponse(200, `{"results":[
					{"url":"https://example.com/a","title":"A","snippet":"short","summary":"long summary","date":"2026-10-20T00:00:00+00:00"},
					{"url":"https://example.com/b","title":"B","snippet":"fallback","summary":"  ","date":null}
				]}`), nil
			})}
			provider := NewDeepSeekProvider("test-key", client)
			require.Equal(t, ProviderTypeDeepSeek, provider.Name())
			resp, err := provider.Search(context.Background(), SearchRequest{Query: tc.query, MaxResults: tc.count})
			require.NoError(t, err)
			require.Equal(t, strings.TrimSpace(tc.query), resp.Query)
			require.Equal(t, []SearchResult{
				{URL: "https://example.com/a", Title: "A", Snippet: "long summary", PageAge: "2026-10-20T00:00:00+00:00"},
				{URL: "https://example.com/b", Title: "B", Snippet: "fallback"},
			}, resp.Results)
		})
	}
}

func TestDeepSeekProvider_InvalidQueryDoesNotSend(t *testing.T) {
	client := &http.Client{Transport: searchRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("invalid queries must not reach the provider")
		return nil, nil
	})}
	for _, query := range []string{"", " \t\n", strings.Repeat("搜", 129), string([]byte{0xff})} {
		_, err := NewDeepSeekProvider("test-key", client).Search(context.Background(), SearchRequest{Query: query})
		require.Error(t, err)
	}
}

func TestDeepSeekProvider_ResponseErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"rate limited", 429, `{"error":{"message":"rate limited"}}`, "deepseek: status 429"},
		{"invalid key", 401, `{"error":{"message":"invalid key"}}`, "deepseek: status 401"},
		{"insufficient balance", 402, `{"error":{"message":"insufficient balance"}}`, "deepseek: status 402"},
		{"invalid json", 200, "{", "deepseek: decode response"},
		{"oversized response", 200, strings.Repeat(" ", maxResponseSize+1), "deepseek: response exceeds"},
		{"empty results", 200, `{"results":[]}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: searchRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				return searchTestResponse(tc.status, tc.body), nil
			})}
			resp, err := NewDeepSeekProvider("test-key", client).Search(context.Background(), SearchRequest{Query: "query"})
			if tc.want != "" {
				require.ErrorContains(t, err, tc.want)
				require.Nil(t, resp)
			} else {
				require.NoError(t, err)
				require.Empty(t, resp.Results)
			}
		})
	}
}

func TestManager_DeepSeekQuotaAndFailover(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	m := NewManager([]ProviderConfig{
		{Type: ProviderTypeDeepSeek, APIKey: "test-key", QuotaLimit: 2, ProxyURL: "http://search-proxy:8080"},
		{Type: ProviderTypeBrave, APIKey: "fallback-key"},
	}, rdb)
	status := http.StatusOK
	var hosts []string
	client := &http.Client{Transport: searchRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		hosts = append(hosts, r.URL.Host)
		if r.URL.Host == "api.deepseek.com" {
			return searchTestResponse(status, `{"results":[{"url":"https://example.com","title":"Result","summary":"DeepSeek summary"}]}`), nil
		}
		require.Equal(t, "api.search.brave.com", r.URL.Host)
		return searchTestResponse(200, `{"web":{"results":[{"url":"https://example.com","title":"Result","description":"Brave summary"}]}}`), nil
	})}
	m.clientCache["http://search-proxy:8080"] = client
	m.clientCache[""] = client
	ctx := context.Background()
	resp, provider, err := m.SearchWithBestProvider(ctx, SearchRequest{Query: "query"})
	require.NoError(t, err)
	require.Equal(t, ProviderTypeDeepSeek, provider)
	require.Equal(t, "DeepSeek summary", resp.Results[0].Snippet)
	used, err := m.GetUsage(ctx, ProviderTypeDeepSeek)
	require.NoError(t, err)
	require.Equal(t, int64(1), used)

	status = http.StatusTooManyRequests
	resp, provider, err = m.SearchWithBestProvider(ctx, SearchRequest{Query: "query"})
	require.NoError(t, err)
	require.Equal(t, ProviderTypeBrave, provider)
	require.Equal(t, "Brave summary", resp.Results[0].Snippet)
	used, err = m.GetUsage(ctx, ProviderTypeDeepSeek)
	require.NoError(t, err)
	require.Equal(t, int64(1), used, "failed search must roll back the reserved quota")
	require.Equal(t, []string{"api.deepseek.com", "api.deepseek.com", "api.search.brave.com"}, hosts)

	// A long query must reach the fallback intact, without an invalid DeepSeek
	// request or a leaked quota reservation.
	_, provider, err = m.SearchWithBestProvider(ctx, SearchRequest{Query: strings.Repeat("搜", 129)})
	require.NoError(t, err)
	require.Equal(t, ProviderTypeBrave, provider)
	used, err = m.GetUsage(ctx, ProviderTypeDeepSeek)
	require.NoError(t, err)
	require.Equal(t, int64(1), used)
	require.Equal(t, []string{"api.deepseek.com", "api.deepseek.com", "api.search.brave.com", "api.search.brave.com"}, hosts)
}
