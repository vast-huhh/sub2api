//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/websearch"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type alphaEmulationAccountRepo struct{ codexModelsFailoverAccountRepo }

func (r alphaEmulationAccountRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, _ int64, p string) ([]service.Account, error) {
	return r.ListSchedulableByPlatform(ctx, p)
}

type alphaCapabilityRejection struct {
	service.HTTPUpstream
	calls int
}

func (u *alphaCapabilityRejection) Do(*http.Request, string, int64, int) (*http.Response, error) {
	u.calls++
	return &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"no_matching_rule"}}`))}, nil
}

func newAlphaEmulationHandler(t *testing.T, account service.Account, enabled bool, providers []websearch.ProviderConfig) (*OpenAIGatewayHandler, *config.Config, <-chan *service.UsageLog, *alphaCapabilityRejection) {
	t.Helper()
	settings := service.NewSettingService(&contentModerationHandlerSettingRepo{values: map[string]string{}}, nil)
	oldConfig, err := settings.GetWebSearchEmulationConfig(context.Background())
	require.NoError(t, err)
	require.NoError(t, settings.SaveWebSearchEmulationConfig(context.Background(), &service.WebSearchEmulationConfig{Enabled: enabled, Providers: []service.WebSearchProviderConfig{{Type: "tavily", APIKey: "test"}}}))
	service.SetWebSearchManager(websearch.NewManager(providers, nil))
	t.Cleanup(func() {
		require.NoError(t, settings.SaveWebSearchEmulationConfig(context.Background(), oldConfig))
		service.SetWebSearchManager(nil)
	})
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	account.Status = service.StatusActive
	account.Schedulable = true
	account.Concurrency = 0
	account.GroupIDs = []int64{21}
	repo := alphaEmulationAccountRepo{codexModelsFailoverAccountRepo{accounts: []service.Account{account}}}
	usageRepo := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 2)}
	billingCache := service.NewBillingCacheService(newHandlerInflightCache(0), nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	upstream := &alphaCapabilityRejection{}
	gw := service.NewOpenAIGatewayService(repo, usageRepo, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billingCache, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, settings, nil)
	h := NewOpenAIGatewayHandler(gw, service.NewConcurrencyService(nil), billingCache, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	return h, cfg, usageRepo.created, upstream
}

func alphaHandlerContext(body string, platform, groupPlatform string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/alpha/search", bytes.NewBufferString(body))
	c.Request = c.Request.WithContext(service.WithResolvedTargetPlatform(c.Request.Context(), platform))
	groupID := int64(21)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 77, UserID: 88, User: &service.User{ID: 88}, GroupID: &groupID, Group: &service.Group{ID: groupID, Platform: groupPlatform, RateMultiplier: 1}})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 88, Concurrency: 0})
	return c, rec
}

func TestAlphaSearchHandlerEmulationGates(t *testing.T) {
	for _, scenario := range []string{"composite", "direct", "disabled", "account_disabled", "balance", "auth", "model"} {
		t.Run(scenario, func(t *testing.T) {
			account := service.Account{ID: 190, Platform: service.PlatformDeepseek, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.6-luna": "cn:deepseek-v4.1-flash"}}}
			if scenario == "account_disabled" {
				account.Extra = map[string]any{"web_search_emulation": "disabled"}
			}
			h, cfg, _, upstream := newAlphaEmulationHandler(t, account, scenario != "disabled", nil)
			groupPlatform := service.PlatformComposite
			if scenario == "direct" {
				groupPlatform = service.PlatformDeepseek
			}
			model := "gpt-5.6-luna"
			if scenario == "model" {
				model = "unknown-not-permitted"
			}
			body, _ := json.Marshal(map[string]any{"model": model, "commands": map[string]any{"search_query": []any{map[string]string{"q": "news"}}}})
			c, rec := alphaHandlerContext(string(body), service.PlatformDeepseek, groupPlatform)
			if scenario == "auth" {
				c.Set(string(middleware2.ContextKeyAPIKey), nil)
			}
			if scenario == "balance" {
				cfg.RunMode = "standard"
			}
			h.AlphaSearch(c)
			require.Zero(t, upstream.calls, "DeepSeek alpha search must not call the model endpoint")
			switch scenario {
			case "composite", "direct":
				require.Equal(t, 502, rec.Code, rec.Body.String())
				require.Contains(t, rec.Body.String(), "web_search_unavailable")
			case "disabled", "model":
				require.Equal(t, 404, rec.Code, rec.Body.String())
			case "account_disabled":
				require.Equal(t, 403, rec.Code, rec.Body.String())
			case "balance":
				require.Contains(t, strings.ToLower(rec.Body.String()), "balance")
				require.NotEqual(t, 200, rec.Code)
			case "auth":
				require.Equal(t, 401, rec.Code, rec.Body.String())
			}
		})
	}
}

// Opt-in: full handler + scheduler + usage recording, with real search services
// and an in-memory account/group repository. Does not change the running server.
func TestAlphaSearchHandlerEmulationLive(t *testing.T) {
	path := os.Getenv("SUB2API_SEARCH_LIVE_CONFIG")
	if path == "" {
		t.Skip("live credentials not supplied")
	}
	var live struct {
		Account   service.Account `json:"account"`
		Providers []struct {
			websearch.ProviderConfig
			Proxy string `json:"proxy_url"`
		} `json:"providers"`
	}
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &live))
	for _, p := range live.Providers {
		provider := p.ProviderConfig
		provider.ProxyURL = p.Proxy
		for _, nativeRejected := range []bool{false, true} {
			name := provider.Type + "/deepseek"
			if nativeRejected {
				name = provider.Type + "/native_rejected"
			}
			t.Run(name, func(t *testing.T) {
				account := live.Account
				if nativeRejected {
					account.Platform = service.PlatformOpenAI
					account.Credentials = map[string]any{"api_key": "test", "base_url": "https://example.com", "model_mapping": map[string]any{"gpt-5.6-luna": "gpt-5.6-luna"}}
				}
				h, _, usage, upstream := newAlphaEmulationHandler(t, account, true, []websearch.ProviderConfig{provider})
				c, rec := alphaHandlerContext(`{"model":"gpt-5.6-luna","commands":{"search_query":[{"q":"site.anthropic.com latest Claude model October 2026"}],"response_length":"short"}}`, account.Platform, service.PlatformComposite)
				start := time.Now()
				h.AlphaSearch(c)
				require.Equal(t, 200, rec.Code, "live alpha adapter failed")
				require.Contains(t, gjson.Get(rec.Body.String(), "output").String(), "https://")
				require.NotEmpty(t, gjson.Get(rec.Body.String(), "results.0.url").String())
				if nativeRejected {
					require.Equal(t, 1, upstream.calls)
				} else {
					require.Zero(t, upstream.calls)
				}
				select {
				case log := <-usage:
					require.InDelta(t, 0.01, log.ActualCost, 1e-9)
					require.NotNil(t, log.InboundEndpoint)
					require.Equal(t, "/v1/alpha/search", *log.InboundEndpoint)
				case <-time.After(5 * time.Second):
					t.Fatal("search usage was not recorded")
				}
				t.Logf("provider=%s native_rejected=%t results=%d duration=%s", provider.Type, nativeRejected, len(gjson.Get(rec.Body.String(), "results").Array()), time.Since(start))
			})
		}
	}
}
