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
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/pkg/websearch"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type searchTestUpstream func(*http.Request) (*http.Response, error)

func (f searchTestUpstream) Do(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return f(r)
}
func (f searchTestUpstream) DoWithTLS(r *http.Request, p string, a int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return f.Do(r, p, a, n)
}
func searchTestResponse(message any, finish string) *http.Response {
	body, _ := json.Marshal(map[string]any{"id": "chatcmpl_search", "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120}})
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(body))}
}
func searchTestPlan(t *testing.T, extra string) (*responsesSearchPlan, []byte) {
	t.Helper()
	body := []byte(`{"model":"gpt-5.6-luna","input":"search latest release","tools":[{"type":"web_search"},{"type":"function","name":"client_tool","parameters":{"type":"object"}}]` + extra + `}`)
	var req apicompat.ResponsesRequest
	require.NoError(t, json.Unmarshal(body, &req))
	effective, e := apicompat.EffectiveResponsesTools(&req)
	require.NoError(t, e)
	plan, e := prepareResponsesSearch(&req, effective, body)
	require.NoError(t, e)
	chat, e := apicompat.ResponsesToChatCompletionsRequestWithOptions(&req, nil)
	require.NoError(t, e)
	out, e := json.Marshal(chat)
	require.NoError(t, e)
	return plan, out
}
func TestResponsesSearchRoundTrip(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			plan, body := searchTestPlan(t, `,"tool_choice":{"type":"web_search"}`)
			require.Equal(t, plan.name, gjson.GetBytes(body, "tool_choice.function.name").String())
			account := rawChatCompletionsTestAccount()
			calls := 0
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
			svc.httpUpstream = searchTestUpstream(func(r *http.Request) (*http.Response, error) {
				raw, _ := io.ReadAll(r.Body)
				calls++
				require.False(t, gjson.GetBytes(raw, "stream").Bool())
				if calls == 1 {
					return searchTestResponse(map[string]any{"role": "assistant", "reasoning_content": "need current facts", "tool_calls": []any{map[string]any{"id": "call_search", "type": "function", "function": map[string]string{"name": plan.name, "arguments": `{"query":"latest release"}`}}}}, "tool_calls"), nil
				}
				require.Equal(t, "auto", gjson.GetBytes(raw, "tool_choice").String())
				require.Contains(t, string(raw), "need current facts")
				require.Equal(t, "call_search", gjson.GetBytes(raw, "messages.2.tool_call_id").String())
				require.Contains(t, gjson.GetBytes(raw, "messages.2.content").String(), "https://example.org/release")
				return searchTestResponse(map[string]any{"role": "assistant", "content": "Latest release: https://example.org/release", "reasoning_content": "use the source"}, "stop"), nil
			})
			searchCalls := 0
			search := func(ctx context.Context, a *Account, q string) (*websearch.SearchResponse, string, error) {
				searchCalls++
				require.Equal(t, "latest release", q)
				return &websearch.SearchResponse{Results: []websearch.SearchResult{{URL: "https://example.org/release", Title: "Release", Snippet: "Version 123"}}}, "test", nil
			}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(body))
			result, e := svc.forwardResponsesWithSearch(context.Background(), c, account, body, plan, "gpt-5.6-luna", "deepseek", "deepseek", nil, nil, stream, time.Now(), search)
			require.NoError(t, e)
			require.Equal(t, 2, calls)
			require.Equal(t, 1, searchCalls)
			require.Equal(t, 200, result.Usage.InputTokens)
			require.Equal(t, 40, result.Usage.OutputTokens)
			require.Equal(t, 1, result.EmulatedWebSearchCalls)
			var response gjson.Result
			if !stream {
				response = gjson.Parse(rec.Body.String())
			} else {
				sequence := 0
				terminals := 0
				indexes := map[string]int{}
				for _, line := range strings.Split(rec.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					ev := gjson.Parse(strings.TrimPrefix(line, "data: "))
					require.Equal(t, sequence, int(ev.Get("sequence_number").Int()))
					sequence++
					if ev.Get("type").String() == "response.output_item.added" {
						indexes[ev.Get("item.id").String()] = int(ev.Get("output_index").Int())
					}
					if strings.HasPrefix(ev.Get("type").String(), "response.web_search_call.") {
						require.True(t, ev.Get("output_index").Exists())
					}
					if ev.Get("type").String() == "response.completed" {
						terminals++
						response = ev.Get("response")
					}
				}
				require.Equal(t, 1, terminals)
				for i, item := range response.Get("output").Array() {
					require.Equal(t, i, indexes[item.Get("id").String()])
				}
			}
			require.Equal(t, "web_search_call", response.Get("output.0.type").String())
			require.Equal(t, "https://example.org/release", response.Get("output.0.action.sources.0.url").String())
			require.Equal(t, int64(240), response.Get("usage.total_tokens").Int())
			require.NotContains(t, rec.Body.String(), plan.name)
		})
	}
}

func TestResponsesSearchFailureLimitAndMixedTools(t *testing.T) {
	for _, scenario := range []string{"provider_failure", "limit", "mixed", "invalid_arguments", "upstream_failure"} {
		t.Run(scenario, func(t *testing.T) {
			plan, body := searchTestPlan(t, `,"max_tool_calls":1`)
			calls := 0
			searchCalls := 0
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
			svc.httpUpstream = searchTestUpstream(func(r *http.Request) (*http.Response, error) {
				raw, _ := io.ReadAll(r.Body)
				calls++
				if calls == 1 {
					args := `{"query":"news"}`
					if scenario == "invalid_arguments" {
						args = `{"query":`
					}
					tools := []any{map[string]any{"id": "search1", "type": "function", "function": map[string]string{"name": plan.name, "arguments": args}}}
					if scenario == "mixed" {
						tools = append(tools, map[string]any{"id": "client1", "type": "function", "function": map[string]string{"name": "client_tool", "arguments": "{}"}})
					}
					return searchTestResponse(map[string]any{"role": "assistant", "tool_calls": tools}, "tool_calls"), nil
				}
				require.NotContains(t, gjson.GetBytes(raw, "tools").Raw, plan.name)
				if scenario == "upstream_failure" {
					return nil, fmt.Errorf("upstream unavailable")
				}
				if scenario == "provider_failure" {
					require.Contains(t, string(raw), "Web search failed")
					require.NotContains(t, string(raw), "SECRET")
				}
				return searchTestResponse(map[string]any{"role": "assistant", "content": "finished"}, "stop"), nil
			})
			search := func(context.Context, *Account, string) (*websearch.SearchResponse, string, error) {
				searchCalls++
				if scenario == "provider_failure" {
					return nil, "", fmt.Errorf("SECRET provider failure")
				}
				return &websearch.SearchResponse{Results: []websearch.SearchResult{{URL: "https://example.org", Snippet: "result"}}}, "test", nil
			}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/responses", bytes.NewReader(body))
			result, e := svc.forwardResponsesWithSearch(context.Background(), c, rawChatCompletionsTestAccount(), body, plan, "luna", "deepseek", "deepseek", nil, nil, true, time.Now(), search)
			if scenario == "invalid_arguments" || scenario == "upstream_failure" {
				require.Error(t, e)
				require.Contains(t, rec.Body.String(), "response.failed")
				require.NotContains(t, rec.Body.String(), "response.completed")
				if scenario == "invalid_arguments" {
					require.Zero(t, searchCalls)
				} else {
					require.Equal(t, 1, result.EmulatedWebSearchCalls)
					require.Equal(t, 100, result.Usage.InputTokens)
				}
			} else {
				require.NoError(t, e)
				if scenario == "mixed" {
					require.Equal(t, 1, calls)
					require.Contains(t, rec.Body.String(), "client_tool")
					require.Contains(t, rec.Body.String(), "https://example.org")
				}
				if scenario == "provider_failure" {
					require.Zero(t, result.EmulatedWebSearchCalls)
					require.Contains(t, rec.Body.String(), `"status":"failed"`)
				}
			}
			require.NotContains(t, rec.Body.String(), plan.name)
			require.NotContains(t, rec.Body.String(), "SECRET")
		})
	}
}

func TestPrepareResponsesSearchOptions(t *testing.T) {
	for _, kind := range []string{"web_search", "web_search_preview", "web_search_preview_2025_03_11"} {
		raw := []byte(fmt.Sprintf(`{"model":"test","tools":[{"type":%q}],"tool_choice":"none"}`, kind))
		var req apicompat.ResponsesRequest
		require.NoError(t, json.Unmarshal(raw, &req))
		plan, e := prepareResponsesSearch(&req, req.Tools, raw)
		require.NoError(t, e)
		require.Nil(t, plan)
	}
	for _, option := range []string{`"filters":{"allowed_domains":["example.org"]}`, `"external_web_access":false`, `"user_location":{"type":"approximate","country":"US"}`} {
		raw := []byte(`{"model":"test","tools":[{"type":"web_search",` + option + `}]}`)
		var req apicompat.ResponsesRequest
		require.NoError(t, json.Unmarshal(raw, &req))
		_, e := prepareResponsesSearch(&req, req.Tools, raw)
		require.Error(t, e)
	}
}

func TestResponsesSearchOutputBudget(t *testing.T) {
	for _, budget := range []int{20, 30} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			plan, body := searchTestPlan(t, fmt.Sprintf(`,"max_output_tokens":%d`, budget))
			calls := 0
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
			svc.httpUpstream = searchTestUpstream(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return searchTestResponse(map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": "search1", "type": "function", "function": map[string]string{"name": plan.name, "arguments": `{"query":"news"}`}}}}, "tool_calls"), nil
				}
				raw, _ := io.ReadAll(r.Body)
				require.Equal(t, int64(10), gjson.GetBytes(raw, "max_tokens").Int())
				return searchTestResponse(map[string]any{"role": "assistant", "content": "answer"}, "stop"), nil
			})
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/responses", bytes.NewReader(body))
			_, err := svc.forwardResponsesWithSearch(context.Background(), c, rawChatCompletionsTestAccount(), body, plan, "luna", "deepseek", "deepseek", nil, nil, false, time.Now(), func(context.Context, *Account, string) (*websearch.SearchResponse, string, error) {
				return &websearch.SearchResponse{}, "test", nil
			})
			require.NoError(t, err)
			if budget == 20 {
				require.Equal(t, 1, calls)
				require.Equal(t, "incomplete", gjson.Get(rec.Body.String(), "status").String())
			} else {
				require.Equal(t, 2, calls)
			}
			require.NotContains(t, rec.Body.String(), plan.name)
		})
	}
}

func TestResponsesSearchPreservesCancellation(t *testing.T) {
	plan, body := searchTestPlan(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
	svc.httpUpstream = searchTestUpstream(func(r *http.Request) (*http.Response, error) {
		_, hasDeadline := r.Context().Deadline()
		require.True(t, hasDeadline)
		cancel()
		require.ErrorIs(t, r.Context().Err(), context.Canceled)
		return nil, r.Context().Err()
	})
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/responses", bytes.NewReader(body))
	_, err := svc.forwardResponsesWithSearch(ctx, c, rawChatCompletionsTestAccount(), body, plan, "luna", "deepseek", "deepseek", nil, nil, false, time.Now(), func(context.Context, *Account, string) (*websearch.SearchResponse, string, error) {
		t.Fatal("canceled request must not search")
		return nil, "", nil
	})
	require.Error(t, err)
}

func TestResponsesSearchEnabled(t *testing.T) {
	SetWebSearchManager(websearch.NewManager(nil, nil))
	defer SetWebSearchManager(nil)
	setGlobalWebSearchConfig(&WebSearchEmulationConfig{Enabled: true, Providers: []WebSearchProviderConfig{{Type: "tavily", APIKey: "test"}}})
	defer clearGlobalWebSearchConfig()
	svc := &OpenAIGatewayService{settingService: &SettingService{settingRepo: &settingRepoStub{}}}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/responses", nil)
	account := rawChatCompletionsTestAccount()
	account.Platform = "deepseek"
	require.True(t, svc.responsesSearchEnabled(context.Background(), c, account))
	account.Extra = map[string]any{featureKeyWebSearchEmulation: "disabled"}
	require.False(t, svc.responsesSearchEnabled(context.Background(), c, account))
}

func TestResponsesSearchBillingAddsTokens(t *testing.T) {
	price := 0.02
	svc := &OpenAIGatewayService{billingService: newTestBillingService()}
	cost, e := svc.calculateOpenAIRecordUsageCost(context.Background(), &OpenAIForwardResult{EmulatedWebSearchCalls: 2}, &APIKey{Group: &Group{WebSearchPricePerCall: &price}}, []string{"claude-sonnet-4"}, 1, 1, 1, 1, UsageTokens{InputTokens: 1000, OutputTokens: 500}, "", boolPtr(false), false, time.Time{})
	require.NoError(t, e)
	require.InDelta(t, 0.0505, cost.ActualCost, 1e-9)
}

// Opt-in integration probe; credentials are supplied in a private file, never
// committed or logged. Exercises the real bridge and real configured search API.
func TestResponsesSearchLive(t *testing.T) {
	path := os.Getenv("SUB2API_SEARCH_LIVE_CONFIG")
	if path == "" {
		t.Skip("live credentials not supplied")
	}
	var live struct {
		Account   Account `json:"account"`
		Providers []struct {
			websearch.ProviderConfig
			Proxy string `json:"proxy_url"`
		} `json:"providers"`
	}
	raw, e := os.ReadFile(path)
	require.NoError(t, e)
	require.NoError(t, json.Unmarshal(raw, &live))
	for _, configured := range live.Providers {
		provider := configured.ProviderConfig
		provider.ProxyURL = configured.Proxy
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", provider.Type, stream), func(t *testing.T) {
				SetWebSearchManager(websearch.NewManager([]websearch.ProviderConfig{provider}, nil))
				defer SetWebSearchManager(nil)
				setGlobalWebSearchConfig(&WebSearchEmulationConfig{Enabled: true, Providers: []WebSearchProviderConfig{{Type: provider.Type, APIKey: "configured"}}})
				defer clearGlobalWebSearchConfig()
				client := &http.Client{Timeout: 120 * time.Second}
				if live.Account.Proxy != nil {
					u, e := url.Parse(live.Account.Proxy.URL())
					require.NoError(t, e)
					client.Transport = &http.Transport{Proxy: http.ProxyURL(u)}
				}
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), settingService: &SettingService{}, httpUpstream: searchTestUpstream(client.Do)}
				body := []byte(fmt.Sprintf(`{"model":"gpt-5.6-luna","input":"今天是2026年10月20日。请使用搜索工具查询 Anthropic 最新发布的 Claude 模型，简短回答模型名称、发布日期及官方链接。一次搜索即可。","tools":[{"type":"web_search"}],"tool_choice":{"type":"web_search"},"max_tool_calls":1,"max_output_tokens":2000,"reasoning":{"effort":"low"},"stream":%t}`, stream))
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(body))
				result, e := svc.forwardResponsesViaRawChatCompletions(context.Background(), c, &live.Account, body, time.Now())
				// Only generic assertions are printed on error; upstream credentials remain private.
				require.True(t, e == nil, "live bridge failed; check sanitized response")
				require.Equal(t, 200, rec.Code)
				require.Equal(t, 1, result.EmulatedWebSearchCalls)
				require.Greater(t, result.Usage.InputTokens, 0)
				require.Contains(t, rec.Body.String(), "web_search_call")
				require.Contains(t, rec.Body.String(), "https://")
				require.NotContains(t, rec.Body.String(), responsesSearchToolName)
				t.Logf("provider=%s stream=%t searches=%d input=%d output=%d duration=%s", provider.Type, stream, result.EmulatedWebSearchCalls, result.Usage.InputTokens, result.Usage.OutputTokens, result.Duration)
				if dest := os.Getenv("SUB2API_SEARCH_LIVE_OUTPUT"); dest != "" {
					require.NoError(t, os.WriteFile(fmt.Sprintf("%s/%s-%t.json", dest, provider.Type, stream), rec.Body.Bytes(), 0600))
				}
			})
		}
	}
}

func TestPrepareResponsesSearchImplicitChoice(t *testing.T) {
	for _, tc := range []struct {
		choice string
		want   bool
	}{
		{``, true}, {`,"tool_choice":"auto"`, true}, {`,"tool_choice":"none"`, false},
		{`,"tool_choice":"required"`, false}, {`,"tool_choice":{"type":"function","name":"client"}`, false},
	} {
		t.Run(tc.choice, func(t *testing.T) {
			body := []byte(`{"model":"luna","input":"搜索热门自建邮箱服务","tools":[{"type":"function","name":"client","parameters":{"type":"object"}}]` + tc.choice + `}`)
			var req apicompat.ResponsesRequest
			require.NoError(t, json.Unmarshal(body, &req))
			plan, err := prepareResponsesSearch(&req, req.Tools, body)
			require.NoError(t, err)
			if !tc.want {
				require.Nil(t, plan)
				require.Len(t, req.Tools, 1)
				return
			}
			require.NotNil(t, plan)
			require.Len(t, req.Tools, 3)
			chat, err := apicompat.ResponsesToChatCompletionsRequest(&req)
			require.NoError(t, err)
			require.Len(t, chat.Tools, 3)
			require.Equal(t, "client", chat.Tools[0].Function.Name)
			require.Equal(t, plan.name, chat.Tools[1].Function.Name)
		})
	}
}

func TestResponsesSearchImplicitGateway(t *testing.T) {
	SetWebSearchManager(websearch.NewManager(nil, nil))
	defer SetWebSearchManager(nil)
	setGlobalWebSearchConfig(&WebSearchEmulationConfig{Enabled: true, Providers: []WebSearchProviderConfig{{Type: "tavily", APIKey: "test"}}})
	defer clearGlobalWebSearchConfig()
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), settingService: &SettingService{settingRepo: &settingRepoStub{}}}
			svc.httpUpstream = searchTestUpstream(func(r *http.Request) (*http.Response, error) {
				raw, _ := io.ReadAll(r.Body)
				require.Len(t, gjson.GetBytes(raw, "tools").Array(), 2)
				require.Contains(t, gjson.GetBytes(raw, "tools.0.function.name").String(), responsesSearchToolName)
				require.NotEqual(t, "required", gjson.GetBytes(raw, "tool_choice").String())
				return searchTestResponse(map[string]any{"role": "assistant", "content": "你好"}, "stop"), nil
			})
			body := []byte(fmt.Sprintf(`{"model":"luna","input":"你好","stream":%t}`, stream))
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(body))
			result, err := svc.forwardResponsesViaRawChatCompletions(context.Background(), c, rawChatCompletionsTestAccount(), body, time.Now())
			require.NoError(t, err)
			require.Equal(t, 200, rec.Code)
			require.Zero(t, result.EmulatedWebSearchCalls)
			require.NotContains(t, rec.Body.String(), "web_search_call")
			require.Contains(t, rec.Body.String(), "你好")
		})
	}
}
