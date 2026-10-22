package websearch

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestReaderReferenceLifecycle(t *testing.T) {
	for _, useRedis := range []bool{false, true} {
		t.Run(fmt.Sprint(useRedis), func(t *testing.T) {
			var client *redis.Client
			var server *miniredis.Miniredis
			if useRedis {
				server = miniredis.RunT(t)
				client = redis.NewClient(&redis.Options{Addr: server.Addr()})
				defer client.Close()
			}
			calls := 0
			reader := NewReader(client, func(_ context.Context, u, proxy string) (*Page, int, error) {
				calls++
				require.Equal(t, "proxy", proxy)
				if strings.HasSuffix(u, "/child") {
					return textPage(u, "Child", "Child page content"), 0, nil
				}
				return textPage(u, "Home", "# Title\nhello needle\n[next](./child)\nmore lines\nfinal line"), 1, nil
			})
			ctx := context.Background()
			require.NoError(t, reader.Remember(ctx, "key1:thread1", "search_test_0", "https://example.com/home", "Search title"))
			first, s, err := reader.Open(ctx, "key1:thread1", "search_test_0", "proxy", 0, 2)
			require.NoError(t, err)
			require.Equal(t, 1, s.Extracts)
			require.Equal(t, 1, calls)
			require.NotNil(t, first.NextLine)
			next, s, err := reader.Open(ctx, "key1:thread1", first.Ref, "proxy", *first.NextLine, 10)
			require.NoError(t, err)
			require.Equal(t, 1, s.CacheHits)
			require.Contains(t, next.Content, "[link 0]")
			found, s, err := reader.Find(ctx, "key1:thread1", first.Ref, "proxy", "needle", 10)
			require.NoError(t, err)
			require.True(t, *found.Found)
			require.Equal(t, 1, s.CacheHits)
			require.Equal(t, 1, calls)
			_, _, err = reader.Open(ctx, "key2:thread1", first.Ref, "proxy", 0, 10)
			require.ErrorContains(t, err, "unknown or expired")
			_, _, err = reader.Open(ctx, "key1:thread2", first.Ref, "proxy", 0, 10)
			require.ErrorContains(t, err, "unknown or expired")
			clicked, s, err := reader.Click(ctx, "key1:thread1", first.Ref, "proxy", 0, 10)
			require.NoError(t, err)
			require.Equal(t, "https://example.com/child", clicked.URL)
			require.Contains(t, clicked.Content, "Child page content")
			require.Equal(t, 2, calls)
			require.Equal(t, 1, s.CacheHits)
			_, _, err = reader.Click(ctx, "key1:thread1", first.Ref, "proxy", 88, 10)
			require.ErrorContains(t, err, "link ID")
			if useRedis {
				server.FastForward(pageTTL + time.Second)
				_, _, err = reader.Open(ctx, "key1:thread1", first.Ref, "proxy", 0, 10)
				require.ErrorContains(t, err, "unknown or expired")
			}
		})
	}
}
func TestReaderRedisSurvivesInstancesAndBoundsCache(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	ctx := context.Background()
	reader := NewReader(client, func(context.Context, string, string) (*Page, int, error) {
		t.Fatal("cached page must not refetch")
		return nil, 0, nil
	})
	require.NoError(t, reader.put(ctx, "scope", textPage("https://example.com/start", "start", "hello")))
	for i := 0; i < maxCachedPages+3; i++ {
		p := textPage(fmt.Sprintf("https://example.com/%d", i), "title", "hello")
		p.Ref = fmt.Sprintf("page_%04d", i)
		require.NoError(t, reader.put(ctx, "scope", p))
	}
	require.Equal(t, int64(maxCachedPages), client.ZCard(ctx, pageScope("scope")+":order").Val())
	require.Equal(t, int64(maxCachedPages), client.HLen(ctx, pageScope("scope")+":urls").Val())
	another := NewReader(client, reader.fetch)
	_, s, err := another.Open(ctx, "scope", "page_0130", "", 0, 10)
	require.NoError(t, err)
	require.Equal(t, 1, s.CacheHits)
}
func TestReaderRejectsUnsafeURLs(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "http://127.0.0.1/a", "http://[::1]", "http://169.254.169.254/latest/meta-data", "http://10.0.0.1", "http://100.64.0.1", "http://198.18.0.1", "https://user:password@example.com", "https://example.com:8080", "http://localhost", "http://service.internal"} {
		_, err := normalizePublicURL(u)
		require.Error(t, err, u)
	}
	for _, ip := range []string{"::ffff:127.0.0.1", "fe80::1", "fc00::1", "64:ff9b::7f00:1"} {
		require.False(t, publicIP(net.ParseIP(ip)))
	}
	_, _, _, err := safePageGET(context.Background(), "http://127.0.0.1/", "", "text/plain")
	require.Error(t, err)
	u, err := normalizePublicURL("https://example.com/path/?x=1#heading")
	require.NoError(t, err)
	require.Equal(t, "https://example.com/path/?x=1", u)
}
func TestReaderHTMLAndMarkdown(t *testing.T) {
	p, err := htmlPage("https://example.com/docs/index.html", []byte(`<html><head><title>Docs</title><script>secret_script</script></head><body><nav>nav_noise</nav><h1>Guide</h1><p>Read <a href="../next?q=1">next</a>.</p><a href="http://localhost/">blocked</a></body></html>`))
	require.NoError(t, err)
	require.Equal(t, "Docs", p.Title)
	require.Equal(t, []string{"https://example.com/next?q=1"}, p.Links)
	require.Contains(t, strings.Join(p.Lines, "\n"), "next [link 0]")
	require.NotContains(t, strings.Join(p.Lines, "\n"), "secret_script")
	require.NotContains(t, strings.Join(p.Lines, "\n"), "nav_noise")
	p = textPage("https://github.com/org/repo/blob/main/README.md", "Repo", "[docs](docs/guide.md)\n"+strings.Repeat("中", maxPageChars))
	require.True(t, p.Truncated)
	require.Equal(t, "https://github.com/org/repo/blob/main/docs/guide.md", p.Links[0])
	for _, line := range p.Lines {
		require.LessOrEqual(t, len([]rune(line)), 180)
	}
}

type readerRoundTrip func(*http.Request) (*http.Response, error)

func (f readerRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestTavilyExtractSuccessAndPartialFailure(t *testing.T) {
	for _, body := range []string{`{"results":[{"url":"https://example.com","raw_content":"# Hello\n[Docs](https://example.com/docs)"}],"failed_results":[]}`, `{"results":[],"failed_results":[{"url":"https://example.com","error":"failed"}]}`} {
		client := &http.Client{Transport: readerRoundTrip(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, "https://api.tavily.com/extract", r.URL.String())
			require.Equal(t, "Bearer test", r.Header.Get("Authorization"))
			raw, _ := io.ReadAll(r.Body)
			require.Contains(t, string(raw), `"format":"markdown"`)
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		page, err := NewTavilyProvider("test", client).Extract(context.Background(), "https://example.com")
		if strings.Contains(body, `"results":[]`) {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			require.Contains(t, strings.Join(page.Lines, "\n"), "[link 0]")
		}
	}
}

func TestDeepSeekFetch(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		success    bool
	}{
		{"markdown", `{"title":"Guide","content":"# Intro\n[next](/next)"}`, 200, true},
		{"empty title", `{"content":"Content"}`, 200, true},
		{"empty content", `{"title":"Guide","content":" "}`, 200, false},
		{"bad json", `not-json`, 200, false},
		{"oversize", strings.Repeat("x", maxPageBytes+1), 200, false},
		{"bad request", `secret-upstream-body`, 400, false},
		{"credentials", `secret-upstream-body`, 401, false},
		{"balance", `secret-upstream-body`, 402, false},
		{"blocked", `secret-upstream-body`, 403, false},
		{"missing", `secret-upstream-body`, 404, false},
		{"rate limit", `secret-upstream-body`, 429, false},
		{"server", `secret-upstream-body`, 500, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: readerRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, deepSeekFetchEndpoint, r.URL.String())
				require.Equal(t, "POST", r.Method)
				require.Equal(t, "Bearer test", r.Header.Get("Authorization"))
				require.Equal(t, "application/json", r.Header.Get("Content-Type"))
				raw, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.JSONEq(t, `{"url":"https://example.com/guide"}`, string(raw))
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}
			page, err := NewDeepSeekProvider("test", client).Fetch(context.Background(), "https://example.com/guide#intro")
			require.Equal(t, 1, calls)
			if tc.success {
				require.NoError(t, err)
				require.NotEmpty(t, page.Title)
				require.NotEmpty(t, page.Lines)
				if tc.name == "markdown" {
					require.Equal(t, []string{"https://example.com/next"}, page.Links)
				}
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "secret-upstream-body")
			}
			_, err = NewDeepSeekProvider("test", client).Fetch(context.Background(), "http://127.0.0.1")
			require.Error(t, err)
			require.Equal(t, 1, calls)
		})
	}
}

func TestReaderProviderFailoverQuotaAndCache(t *testing.T) {
	for _, first := range []string{ProviderTypeDeepSeek, ProviderTypeTavily} {
		t.Run(first, func(t *testing.T) {
			second := ProviderTypeTavily
			if first == second {
				second = ProviderTypeDeepSeek
			}
			server := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: server.Addr()})
			defer client.Close()
			// The weight gap guarantees the failed provider is attempted first.
			manager := NewManager([]ProviderConfig{
				{Type: first, APIKey: "test", QuotaLimit: 1000, ProxyURL: "first-proxy"},
				{Type: second, APIKey: "test", QuotaLimit: 10, ProxyURL: "second-proxy"},
			}, client)
			calls := 0
			manager.clientCache["first-proxy"] = &http.Client{Transport: readerRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			})}
			manager.clientCache["second-proxy"] = &http.Client{Transport: readerRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				body := `{"title":"Docs","content":"# Docs\n[next](/next)"}`
				if second == ProviderTypeTavily {
					body = `{"results":[{"url":"https://example.com","raw_content":"# Docs\n[next](/next)"}]}`
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			ctx := context.Background()
			page, stats, err := manager.Reader().Open(ctx, "scope", "https://example.com", "", 0, 10)
			require.NoError(t, err)
			require.Equal(t, 2, calls)
			require.Equal(t, 1, stats.Extracts)
			used, err := manager.GetUsage(ctx, first)
			require.NoError(t, err)
			require.Zero(t, used)
			used, err = manager.GetUsage(ctx, second)
			require.NoError(t, err)
			require.Equal(t, int64(1), used)
			_, stats, err = manager.Reader().Find(ctx, "scope", page.Ref, "", "Docs", 10)
			require.NoError(t, err)
			require.Equal(t, 1, stats.CacheHits)
			require.Zero(t, stats.Extracts)
			require.Equal(t, 2, calls)
		})
	}
}

func TestReaderSupplementLinks(t *testing.T) {
	page := textPage("https://example.com/docs", "Docs", "Provider text without links")
	source, err := htmlPage(page.URL, []byte(`<main><a href="/tutorial">Tutorial</a><a href="http://127.0.0.1">private</a></main>`))
	require.NoError(t, err)
	supplementPageLinks(page, source, "Links from source HTML:")
	require.Equal(t, []string{"https://example.com/tutorial"}, page.Links)
	require.Equal(t, "Provider text without links", page.Lines[0])
	require.Contains(t, strings.Join(page.Lines, "\n"), "[link 0] https://example.com/tutorial")
	supplementPageLinks(page, source, "Links from source HTML:")
	require.Len(t, page.Links, 1)
}

func TestReaderEscapedMarkdown(t *testing.T) {
	page := textPage("https://example.com", "Docs", `\# Python 3\.15\nWelcome\!`)
	require.Equal(t, `# Python 3.15\nWelcome!`, page.Lines[0])
}

func TestReaderProvidersAreIndependent(t *testing.T) {
	for _, provider := range []string{ProviderTypeDeepSeek, ProviderTypeTavily} {
		for _, both := range []bool{false, true} {
			for _, sourceAvailable := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/both=%t/source=%t", provider, both, sourceAvailable), func(t *testing.T) {
					server := miniredis.RunT(t)
					client := redis.NewClient(&redis.Options{Addr: server.Addr()})
					defer client.Close()
					other := ProviderTypeTavily
					if provider == other {
						other = ProviderTypeDeepSeek
					}
					configs := []ProviderConfig{{Type: provider, APIKey: "test", QuotaLimit: 1000, ProxyURL: "selected-proxy"}}
					if both {
						configs = append(configs, ProviderConfig{Type: other, APIKey: "test", QuotaLimit: 10, ProxyURL: "other-proxy"})
					}
					manager := NewManager(configs, client)
					calls := 0
					manager.clientCache["selected-proxy"] = &http.Client{Transport: readerRoundTrip(func(r *http.Request) (*http.Response, error) {
						calls++
						body := `{"title":"Docs","content":"Independent provider text"}`
						if provider == ProviderTypeTavily {
							body = `{"results":[{"url":"https://example.com","raw_content":"Independent provider text"}]}`
						}
						return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
					})}
					manager.clientCache["other-proxy"] = &http.Client{Transport: readerRoundTrip(func(*http.Request) (*http.Response, error) {
						t.Fatal("must not call another provider to supplement links")
						return nil, fmt.Errorf("unexpected provider")
					})}
					getCalls := 0
					ctx := context.Background()
					page, extracts, err := manager.fetchPageWithGET(ctx, "https://example.com", "", func(_ context.Context, u, proxy, accept string) ([]byte, string, string, error) {
						getCalls++
						require.Equal(t, "selected-proxy", proxy)
						require.Equal(t, "https://example.com", u)
						if !sourceAvailable {
							return nil, "", "", fmt.Errorf("source unavailable")
						}
						return []byte(`<a href="/tutorial">Tutorial</a>`), "text/html", u, nil
					})
					require.NoError(t, err)
					require.Equal(t, 1, calls)
					require.Equal(t, 1, getCalls)
					require.Equal(t, 1, extracts)
					require.Equal(t, provider+"_fetch", page.FetchMethod)
					require.Equal(t, "Independent provider text", page.Lines[0])
					if sourceAvailable {
						require.Equal(t, []string{"https://example.com/tutorial"}, page.Links)
					} else {
						require.Empty(t, page.Links)
					}
					used, err := manager.GetUsage(ctx, provider)
					require.NoError(t, err)
					require.Equal(t, int64(1), used)
					used, err = manager.GetUsage(ctx, other)
					require.NoError(t, err)
					require.Zero(t, used)
				})
			}
		}
	}
}
