package websearch

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Explicit opt-in only; credentials are supplied privately, never logged.
func TestWebReaderLive(t *testing.T) {
	file := os.Getenv("SUB2API_SEARCH_LIVE_CONFIG")
	if file == "" {
		t.Skip("live credentials not supplied")
	}
	var live struct {
		Providers []struct {
			ProviderConfig
			Proxy string `json:"proxy_url"`
		} `json:"providers"`
	}
	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &live))
	var configs []ProviderConfig
	for _, p := range live.Providers {
		cfg := p.ProviderConfig
		cfg.ProxyURL = p.Proxy
		configs = append(configs, cfg)
	}
	reader := NewManager(configs, nil).Reader()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, u := range []string{"https://github.com/shuishuipingan/qoder2api-hub", "https://docs.python.org/3/"} {
		start := time.Now()
		p, stats, err := reader.resolve(ctx, "live", u, "")
		require.NoError(t, err)
		require.NotEmpty(t, p.Lines)
		if strings.Contains(u, "github.com") {
			require.Contains(t, strings.Join(p.Lines, "\n"), "Qoder")
		}
		if strings.Contains(u, "python.org") {
			for _, cfg := range configs {
				if cfg.Type != ProviderTypeTavily && cfg.Type != ProviderTypeDeepSeek {
					continue
				}
				client, e := newHTTPClient(cfg.ProxyURL)
				require.NoError(t, e)
				extracted, e := NewManager(nil, nil).buildProvider(cfg, client).(PageProvider).Fetch(ctx, u)
				require.NoError(t, e, "direct provider Fetch probe: "+cfg.Type)
				require.NotEmpty(t, extracted.Lines)
				t.Logf("direct %s Fetch verified: lines=%d", cfg.Type, len(extracted.Lines))
			}
		}
		found, s, err := reader.Find(ctx, "live", p.Ref, "", p.Lines[0], 10)
		require.NoError(t, err)
		require.True(t, *found.Found)
		require.Equal(t, 1, s.CacheHits)
		t.Logf("source=%s method=%s lines=%d links=%d extracts=%d duration=%s", u, p.FetchMethod, len(p.Lines), len(p.Links), stats.Extracts, time.Since(start))
	}
}
