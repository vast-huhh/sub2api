package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

const (
	deepSeekSearchEndpoint = "https://api.deepseek.com/v1/tools/search"
	deepSeekFetchEndpoint  = "https://api.deepseek.com/v1/tools/fetch"
	deepSeekMaxQueryChars  = 128
	deepSeekMaxCount       = 50
)

// DeepSeekProvider uses the standalone Search API, independently of model calls.
type DeepSeekProvider struct {
	apiKey     string
	httpClient *http.Client
}

// NewDeepSeekProvider accepts the Manager's proxy-aware, time-limited client.
func NewDeepSeekProvider(apiKey string, httpClient *http.Client) *DeepSeekProvider {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &DeepSeekProvider{apiKey: apiKey, httpClient: httpClient}
}

func (p *DeepSeekProvider) Name() string { return ProviderTypeDeepSeek }

// Fetch uses the standalone Fetch API with the same credentials as Search.
func (p *DeepSeekProvider) Fetch(ctx context.Context, rawURL string) (*Page, error) {
	u, err := normalizePublicURL(rawURL)
	if err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(map[string]string{"url": u})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deepSeekFetchEndpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("deepseek: invalid fetch request")
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("deepseek: fetch request failed")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes+1))
	if err != nil || len(data) > maxPageBytes {
		return nil, fmt.Errorf("deepseek: fetch response unreadable or exceeds size limit")
	}
	if resp.StatusCode != http.StatusOK {
		// Do not expose response bodies or repeat unchanged 4xx requests.
		return nil, fmt.Errorf("deepseek: fetch returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if json.Unmarshal(data, &result) != nil || strings.TrimSpace(result.Content) == "" {
		return nil, fmt.Errorf("deepseek: fetch returned no readable page")
	}
	if strings.TrimSpace(result.Title) == "" {
		result.Title = u
	}
	return textPage(u, result.Title, result.Content), nil
}

func (p *DeepSeekProvider) Search(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, fmt.Errorf("deepseek: empty search query")
	}
	// Do not truncate a query: doing so can change its meaning or break site/date
	// operators. The Manager can try another configured provider on this error.
	if !utf8.ValidString(query) || utf8.RuneCountInString(query) > deepSeekMaxQueryChars {
		return nil, fmt.Errorf("deepseek: query must be valid UTF-8 and at most %d characters", deepSeekMaxQueryChars)
	}
	count := req.MaxResults
	if count <= 0 {
		count = defaultMaxResults
	}
	if count > deepSeekMaxCount {
		count = deepSeekMaxCount
	}
	payload, err := json.Marshal(struct {
		Query       string `json:"query"`
		Count       int    `json:"count"`
		NeedContent bool   `json:"need_content"`
	}{Query: query, Count: count, NeedContent: false})
	if err != nil {
		return nil, fmt.Errorf("deepseek: encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, deepSeekSearchEndpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("deepseek: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("deepseek: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("deepseek: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("deepseek: status %d: %s", resp.StatusCode, truncateBody(body))
	}
	if len(body) > maxResponseSize {
		return nil, fmt.Errorf("deepseek: response exceeds %d bytes", maxResponseSize)
	}
	var raw struct {
		Results []struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Snippet string `json:"snippet"`
			Summary string `json:"summary"`
			Date    string `json:"date"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("deepseek: decode response: %w", err)
	}
	results := make([]SearchResult, 0, len(raw.Results))
	for _, r := range raw.Results {
		// The long summary is intended for model consumption; dates may be null.
		snippet := r.Summary
		if strings.TrimSpace(snippet) == "" {
			snippet = r.Snippet
		}
		results = append(results, SearchResult{URL: r.URL, Title: r.Title, Snippet: snippet, PageAge: r.Date})
	}
	return &SearchResponse{Results: results, Query: query}, nil
}
