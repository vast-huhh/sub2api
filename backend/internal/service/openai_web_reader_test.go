//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/websearch"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func fixtureWebReader(t *testing.T) *websearch.Reader {
	return websearch.NewReader(nil, func(_ context.Context, u, _ string) (*websearch.Page, int, error) {
		if strings.Contains(u, "failure") {
			return nil, 0, fmt.Errorf("page unavailable")
		}
		if strings.HasSuffix(u, "/child") {
			return &websearch.Page{URL: u, Title: "Child", Lines: []string{"Child content"}}, 0, nil
		}
		return &websearch.Page{URL: u, Title: "Fixture", Lines: []string{"Page headline", "A needle in text", "Child [link 0]", "Last line"}, Links: []string{"https://example.com/child"}}, 1, nil
	})
}
func TestAlphaWebReaderLifecycleAndIsolation(t *testing.T) {
	reader := fixtureWebReader(t)
	svc := &OpenAIGatewayService{}
	account := rawChatCompletionsTestAccount()
	search := func(context.Context, *Account, string) (*websearch.SearchResponse, string, error) {
		return &websearch.SearchResponse{Results: []websearch.SearchResult{{URL: "https://example.com/page", Title: "Fixture", Snippet: "Search snippet"}}}, "mock", nil
	}
	run := func(key int64, session, commands string) (gjson.Result, *OpenAIForwardResult) {
		body := []byte(`{"model":"luna","id":"` + session + `","commands":` + commands + `}`)
		c, rec := alphaEmulationContext(body)
		c.Set("api_key", &APIKey{ID: key})
		result, err := svc.forwardAlphaWebEmulated(context.Background(), c, account, body, search, reader)
		require.NoError(t, err)
		require.Equal(t, 200, rec.Code)
		return gjson.Parse(rec.Body.String()), result
	}
	first, result := run(1, "thread1", `{"search_query":[{"q":"fixture"}],"response_length":"short"}`)
	require.Equal(t, 1, result.WebSearchCalls)
	ref := first.Get("results.0.ref_id").String()
	require.NotEmpty(t, ref)
	opened, result := run(1, "thread1", fmt.Sprintf(`{"open":[{"ref_id":%q}]}`, ref))
	require.Zero(t, result.WebSearchCalls)
	require.Equal(t, 1, result.WebReadCalls)
	require.Equal(t, 1, result.WebExtractCalls)
	require.Contains(t, opened.Get("output").String(), "L1: A needle in text")
	found, result := run(1, "thread1", fmt.Sprintf(`{"find":[{"ref_id":%q,"pattern":"needle"}]}`, ref))
	require.Equal(t, 1, result.WebReadCacheHits)
	require.Contains(t, found.Get("output").String(), "Pattern found: true")
	clicked, result := run(1, "thread1", fmt.Sprintf(`{"click":[{"ref_id":%q,"id":0}]}`, ref))
	require.Contains(t, clicked.Get("output").String(), "Child content")
	require.Equal(t, 1, result.WebReadCalls)
	for _, identity := range []struct {
		key     int64
		session string
	}{{2, "thread1"}, {1, "thread2"}} {
		failed, _ := run(identity.key, identity.session, fmt.Sprintf(`{"open":[{"ref_id":%q}]}`, ref))
		require.Contains(t, failed.Get("output").String(), "unknown or expired")
		require.Empty(t, failed.Get("results").Array())
	}
	partial, _ := run(1, "thread1", `{"open":[{"ref_id":"https://example.com/failure"},{"ref_id":"https://example.com/page"}]}`)
	require.Contains(t, partial.Get("output").String(), "open failed")
	require.Contains(t, partial.Get("output").String(), "Page headline")
}
func TestAlphaWebCommandsValidation(t *testing.T) {
	for _, commands := range []string{`{"open":[{"ref_id":"x","lineno":-1}]}`, `{"open":[{"ref_id":"x","lineno":1.5}]}`, `{"click":[{"ref_id":"x"}]}`, `{"find":[{"ref_id":"x","pattern":""}]}`, `{"search_query":[{"q":"valid"}],"click":[{"ref_id":"x","id":false}]}`, `{"open":"https://example.com"}`, `{"open":[{"ref_id":"x"}],"response_length":"huge"}`} {
		_, err := parseAlphaWebCommands([]byte(`{"commands":` + commands + `}`))
		require.Error(t, err, commands)
	}
}
func TestWebReaderBillingDoesNotChargeSearch(t *testing.T) {
	svc := &OpenAIGatewayService{billingService: newTestBillingService()}
	price := 0.02
	for _, searches := range []int{0, 2} {
		cost, err := svc.calculateOpenAIRecordUsageCost(context.Background(), &OpenAIForwardResult{EmulatedWebOperations: true, WebSearchCalls: searches, WebReadCalls: 4, WebExtractCalls: 1, WebReadCacheHits: 5}, &APIKey{Group: &Group{WebSearchPricePerCall: &price}}, []string{"claude-sonnet-4"}, 1, 1, 1, 1, UsageTokens{}, "", boolPtr(false), false, time.Time{})
		require.NoError(t, err)
		require.InDelta(t, float64(searches)*price, cost.ActualCost, 1e-9)
	}
	estimate, priced := newInflightEstimateGateway(t, nil).EstimateInflightReservation(context.Background(), &APIKey{User: &User{ID: 1}}, InflightEstimateRequest{Model: "luna", Kind: InflightEstimateWebRead})
	require.True(t, priced)
	require.Zero(t, estimate)
}
func TestResponsesSearchReadFindClickRoundTrip(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			plan, body := searchTestPlan(t, "")
			plan.reader = fixtureWebReader(t)
			plan.scope = "test:thread"
			calls := 0
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
			svc.httpUpstream = searchTestUpstream(func(r *http.Request) (*http.Response, error) {
				raw, _ := io.ReadAll(r.Body)
				calls++
				name, args := plan.name, `{"query":"fixture"}`
				messages := gjson.GetBytes(raw, "messages").Array()
				last := messages[len(messages)-1].Get("content").String()
				if calls > 1 {
					name = plan.readName
				}
				switch calls {
				case 2:
					ref := gjson.Get(last, "results.0.ref_id").String()
					require.NotEmpty(t, ref)
					args = fmt.Sprintf(`{"operation":"open","ref_id":%q}`, ref)
				case 3:
					require.Contains(t, last, "needle")
					args = fmt.Sprintf(`{"operation":"find","ref_id":%q,"pattern":"needle"}`, gjson.Get(last, "ref_id").String())
				case 4:
					require.True(t, gjson.Get(last, "found").Bool())
					args = fmt.Sprintf(`{"operation":"click","ref_id":%q,"id":0}`, gjson.Get(last, "ref_id").String())
				case 5:
					require.Contains(t, last, "Child content")
					return searchTestResponse(map[string]any{"role": "assistant", "content": "Verified https://example.com/child"}, "stop"), nil
				}
				return searchTestResponse(map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": fmt.Sprintf("call_%d", calls), "type": "function", "function": map[string]string{"name": name, "arguments": args}}}}, "tool_calls"), nil
			})
			search := func(context.Context, *Account, string) (*websearch.SearchResponse, string, error) {
				return &websearch.SearchResponse{Results: []websearch.SearchResult{{URL: "https://example.com/page", Title: "Fixture", Snippet: "Snippet"}}}, "mock", nil
			}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/responses", bytes.NewReader(body))
			result, err := svc.forwardResponsesWithSearch(context.Background(), c, rawChatCompletionsTestAccount(), body, plan, "luna", "deepseek", "deepseek", nil, nil, stream, time.Now(), search)
			require.NoError(t, err)
			require.Equal(t, 5, calls)
			require.Equal(t, 1, result.EmulatedWebSearchCalls)
			require.Equal(t, 2, result.WebReadCalls)
			require.Equal(t, 1, result.WebExtractCalls)
			require.Equal(t, 2, result.WebReadCacheHits)
			require.Contains(t, rec.Body.String(), "find_in_page")
			require.Contains(t, rec.Body.String(), "open_page")
			require.NotContains(t, rec.Body.String(), plan.readName)
			require.NotContains(t, rec.Body.String(), plan.name)
			if !stream {
				var output map[string]any
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &output))
				require.Len(t, output["output"], 5)
			}
		})
	}
}

func TestResponsesWebReadRepairsMissingClickID(t *testing.T) {
	plan, body := searchTestPlan(t, "")
	plan.reader = fixtureWebReader(t)
	plan.scope = "repair:thread"
	calls := 0
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
	svc.httpUpstream = searchTestUpstream(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		calls++
		args := `{"operation":"click","ref_id":"https://example.com/page"}`
		if calls == 2 {
			require.Contains(t, string(raw), "click requires a link id")
			args = `{"operation":"click","ref_id":"https://example.com/page","id":0}`
		}
		if calls == 3 {
			require.Contains(t, string(raw), "Child content")
			return searchTestResponse(map[string]any{"role": "assistant", "content": "Read the child page successfully."}, "stop"), nil
		}
		return searchTestResponse(map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": fmt.Sprintf("repair_%d", calls), "type": "function", "function": map[string]string{"name": plan.readName, "arguments": args}}}}, "tool_calls"), nil
	})
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/responses", bytes.NewReader(body))
	result, err := svc.forwardResponsesWithSearch(context.Background(), c, rawChatCompletionsTestAccount(), body, plan, "luna", "deepseek", "deepseek", nil, nil, true, time.Now(), nil)
	require.NoError(t, err)
	require.Equal(t, 3, calls)
	require.Zero(t, result.EmulatedWebSearchCalls)
	require.Equal(t, 2, result.WebReadCalls)
	require.Contains(t, rec.Body.String(), "response.completed")
	require.NotContains(t, rec.Body.String(), "response.failed")
	require.Contains(t, rec.Body.String(), "Read the child page successfully.")
}
