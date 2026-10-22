//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/websearch"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAlphaSearchEmulationErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
		want   bool
	}{
		{404, "", true}, {405, "", true}, {501, "", true},
		{401, "no_matching_rule", true}, {403, "no_matching_rule", true},
		{400, "unsupported_tool", true}, {400, "unsupported_endpoint", true},
		{401, "invalid_api_key", false}, {401, "", false}, {403, "permission_denied", false},
		{404, "model_not_found", false}, {429, "insufficient_quota", false},
		{402, "", false}, {500, "", false}, {400, "invalid_request_error", false},
	} {
		t.Run(fmt.Sprintf("%d/%s", tc.status, tc.code), func(t *testing.T) {
			require.Equal(t, tc.want, isAlphaSearchEmulationFallbackError(tc.status, []byte(fmt.Sprintf(`{"error":{"code":%q}}`, tc.code))))
		})
	}
}

func TestAlphaSearchEmulationValidatesWholeBatch(t *testing.T) {
	for _, commands := range []string{
		`{}`, `{"open":[{"ref_id":""}]}`,
		`{"search_query":[{"q":"valid"},{"q":""}]}`,
		`{"search_query":[{"q":"valid","domains":["example.com"]}]}`,
		`{"search_query":[{"q":"valid","recency":1}]}`,
		`{"search_query":[{"q":"1"},{"q":"2"},{"q":"3"},{"q":"4"},{"q":"5"}]}`,
		`{"search_query":[{"q":"valid"}],"find":[{"ref_id":"x","pattern":""}]}`,
	} {
		body := []byte(`{"model":"luna","commands":` + commands + `}`)
		c, rec := alphaEmulationContext(body)
		result, err := (&OpenAIGatewayService{}).forwardAlphaSearchEmulated(context.Background(), c, rawChatCompletionsTestAccount(), body, func(context.Context, *Account, string) (*websearch.SearchResponse, string, error) {
			t.Fatal("invalid batch must not execute even its valid first query")
			return nil, "", nil
		})
		require.NoError(t, err)
		require.Nil(t, result)
		require.Equal(t, 400, rec.Code)
	}
	for _, option := range []string{`"external_web_access":false`, `"filters":{"allowed_domains":["example.com"]}`, `"user_location":{"country":"US"}`} {
		_, err := parseAlphaSearchEmulationQueries([]byte(`{"commands":{"search_query":[{"q":"news"}]},"settings":{` + option + `}}`))
		require.Error(t, err)
	}
	queries, err := parseAlphaSearchEmulationQueries([]byte(`{"commands":{"search_query":[{"q":" news ","domains":null,"recency":null}],"open":[],"response_length":"short"}}`))
	require.NoError(t, err)
	require.Equal(t, []string{"news"}, queries)
}

func alphaEmulationContext(body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/alpha/search", bytes.NewReader(body))
	return c, rec
}

func TestAlphaSearchEmulationOutputAndBilling(t *testing.T) {
	for _, failAt := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.6-luna","commands":{"search_query":[{"q":"one"},{"q":"two"}]}}`)
			c, rec := alphaEmulationContext(body)
			calls := 0
			result, err := (&OpenAIGatewayService{}).forwardAlphaSearchEmulated(context.Background(), c, rawChatCompletionsTestAccount(), body, func(ctx context.Context, _ *Account, q string) (*websearch.SearchResponse, string, error) {
				_, ok := ctx.Deadline()
				require.True(t, ok)
				calls++
				if calls == failAt {
					return nil, "", fmt.Errorf("SECRET provider error")
				}
				return &websearch.SearchResponse{Results: []websearch.SearchResult{{URL: "https://example.com/" + q, Title: q, Snippet: "real snippet", PageAge: "2026-10-20"}, {URL: "javascript:alert(1)"}}}, "test", nil
			})
			require.NoError(t, err)
			require.NotContains(t, rec.Body.String(), "SECRET")
			require.NotContains(t, rec.Body.String(), "javascript:")

			require.Equal(t, 200, rec.Code)
			expected := 2
			if failAt > 0 {
				expected = 1
				require.Contains(t, gjson.Get(rec.Body.String(), "output").String(), "Search failed")
			}
			require.Equal(t, expected, result.WebSearchCalls)
			require.Equal(t, expected, AlphaSearchReservationCalls([]byte(`{"commands":{"search_query":[{"q":"one"}`+strings.Repeat(`,{"q":"two"}`, expected-1)+`]}}`)))
			require.Zero(t, result.Usage.InputTokens)
			require.Len(t, gjson.Get(rec.Body.String(), "results").Array(), expected)
			require.Contains(t, gjson.Get(rec.Body.String(), "output").String(), gjson.Get(rec.Body.String(), "results.0.ref_id").String())
			if failAt != 1 {
				require.Contains(t, gjson.Get(rec.Body.String(), "output").String(), "https://example.com/one")
			}
			require.False(t, gjson.Get(rec.Body.String(), "encrypted_output").Exists())
		})
	}
}

func TestAlphaSearchEmulationReservationMatchesBilling(t *testing.T) {
	svc := newInflightEstimateGateway(t, nil)
	groupID := int64(21)
	price, unrelatedSearchPrice := 0.25, 999.0
	key := &APIKey{User: &User{ID: 1}, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformComposite, RateMultiplier: 2, WebSearchPricePerCall: &price, SearchPricePer1k: &unrelatedSearchPrice}}
	estimate, priced := svc.EstimateInflightReservation(context.Background(), key, InflightEstimateRequest{Model: "unknown-alias", Kind: InflightEstimateAlphaSearch, Units: 2})
	require.True(t, priced)
	require.InDelta(t, 1.0, estimate, 1e-9)
	price = 0
	estimate, priced = svc.EstimateInflightReservation(context.Background(), key, InflightEstimateRequest{Model: "unknown-alias", Kind: InflightEstimateAlphaSearch, Units: 2})
	require.True(t, priced)
	require.Zero(t, estimate)
}

func TestAlphaSearchEmulationChannelDisable(t *testing.T) {
	SetWebSearchManager(websearch.NewManager(nil, nil))
	setGlobalWebSearchConfig(&WebSearchEmulationConfig{Enabled: true, Providers: []WebSearchProviderConfig{{Type: "tavily", APIKey: "test"}}})
	t.Cleanup(func() { SetWebSearchManager(nil); clearGlobalWebSearchConfig() })
	groupID := int64(21)
	svc := &OpenAIGatewayService{settingService: &SettingService{}, channelService: newChannelServiceWithCache(groupID, &Channel{ID: 1, Status: StatusActive, FeaturesConfig: map[string]any{featureKeyWebSearchEmulation: map[string]any{PlatformDeepseek: false}}})}
	body := []byte(`{"model":"luna","commands":{"search_query":[{"q":"news"}]}}`)
	c, rec := alphaEmulationContext(body)
	c.Set("api_key", &APIKey{GroupID: &groupID})
	result, err := svc.ForwardAlphaSearch(context.Background(), c, &Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey}, body)
	require.NoError(t, err)
	require.Nil(t, result)
	require.Equal(t, 403, rec.Code)
}

func TestForwardAlphaSearchNativeCapabilityFallback(t *testing.T) {
	SetWebSearchManager(websearch.NewManager(nil, nil))
	t.Cleanup(func() { SetWebSearchManager(nil); clearGlobalWebSearchConfig() })
	setGlobalWebSearchConfig(&WebSearchEmulationConfig{Enabled: true, Providers: []WebSearchProviderConfig{{Type: "tavily", APIKey: "test"}}})
	for _, status := range []int{404, 401, 403, 200} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.6-luna","commands":{"search_query":[{"q":"news"}]}}`)
			c, rec := alphaEmulationContext(body)
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), settingService: &SettingService{}}
			svc.httpUpstream = searchTestUpstream(func(*http.Request) (*http.Response, error) {
				wire := `{"error":{"code":"no_matching_rule"}}`
				if status == 200 {
					wire = `{"output":"native success"}`
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(wire))}, nil
			})
			result, err := svc.ForwardAlphaSearch(context.Background(), c, rawChatCompletionsTestAccount(), body)
			require.NoError(t, err)
			if status == 200 {
				require.Equal(t, 1, result.WebSearchCalls)
				require.Equal(t, "native success", gjson.Get(rec.Body.String(), "output").String())
			} else {
				// Empty manager guarantees no external traffic and a sanitized provider
				// failure. Getting here proves the rejection reached the adapter.
				require.Nil(t, result)
				require.Equal(t, 502, rec.Code)
				require.Contains(t, rec.Body.String(), "web_search_unavailable")
			}
		})
	}
}
