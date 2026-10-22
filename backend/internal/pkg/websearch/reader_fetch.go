package websearch

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const maxPageBytes = 2 << 20

var markdownLink = regexp.MustCompile(`\[([^\]\n]{1,300})\]\(([^\s)]+)(?:\s+"[^"\n]*")?\)`)

// CommonMark punctuation escapes are presentation syntax, not page text.
var markdownEscape = regexp.MustCompile("\\\\([[:punct:]])")

func publicIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	for _, cidr := range []string{"100.64.0.0/10", "198.18.0.0/15", "192.0.0.0/24", "192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96", "2002::/16"} {
		_, block, _ := net.ParseCIDR(cidr)
		if block.Contains(ip) {
			return false
		}
	}
	return true
}
func publicHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if ip := net.ParseIP(h); ip != nil {
		return publicIP(ip)
	}
	return strings.Contains(h, ".") && h != "localhost" && !strings.HasSuffix(h, ".localhost") && !strings.HasSuffix(h, ".local") && !strings.HasSuffix(h, ".internal")
}

// Resolve and pin the destination for each hop, including proxy CONNECT. Host
// and TLS SNI retain the original hostname; a DNS rebinding cannot redirect the
// actual connection to a private address after validation.
func safePageGET(ctx context.Context, raw, proxy, accept string) ([]byte, string, string, error) {
	for hop := 0; hop < 5; hop++ {
		normalized, e := normalizePublicURL(raw)
		if e != nil {
			return nil, "", "", e
		}
		u, _ := url.Parse(normalized)
		ips, e := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
		if e != nil || len(ips) == 0 {
			return nil, "", "", fmt.Errorf("page DNS lookup failed")
		}
		for _, a := range ips {
			if !publicIP(a.IP) {
				return nil, "", "", fmt.Errorf("page resolves to a non-public address")
			}
		}
		client, e := newHTTPClient(proxy)
		if e != nil {
			return nil, "", "", fmt.Errorf("page proxy unavailable")
		}
		client.Timeout = 25 * time.Second
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		transport := client.Transport.(*http.Transport)
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: u.Hostname()}
		pinned := *u
		port := u.Port()
		if port == "" {
			if u.Scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
		pinned.Host = net.JoinHostPort(ips[0].IP.String(), port)
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, pinned.String(), nil)
		if e != nil {
			return nil, "", "", e
		}
		req.Host = u.Host
		req.Header.Set("User-Agent", "Sub2API-WebReader/1.0")
		req.Header.Set("Accept", accept)
		resp, e := client.Do(req)
		if e != nil {
			transport.CloseIdleConnections()
			return nil, "", "", fmt.Errorf("page request failed")
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			loc, e := resp.Location()
			resp.Body.Close()
			transport.CloseIdleConnections()
			if e != nil {
				return nil, "", "", fmt.Errorf("invalid page redirect")
			}
			if loc.Host == pinned.Host {
				loc.Host = u.Host
			}
			raw = loc.String()
			continue
		}
		data, e := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes+1))
		resp.Body.Close()
		transport.CloseIdleConnections()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, "", "", fmt.Errorf("page returned HTTP %d", resp.StatusCode)
		}
		if e != nil || len(data) > maxPageBytes {
			return nil, "", "", fmt.Errorf("page exceeds size limit or could not be read")
		}
		return data, resp.Header.Get("Content-Type"), normalized, nil
	}
	return nil, "", "", fmt.Errorf("too many page redirects")
}

type pageGET func(context.Context, string, string, string) ([]byte, string, string, error)

func (m *Manager) fetchPage(ctx context.Context, raw, accountProxy string) (*Page, int, error) {
	return m.fetchPageWithGET(ctx, raw, accountProxy, safePageGET)
}

func (m *Manager) fetchPageWithGET(ctx context.Context, raw, accountProxy string, get pageGET) (*Page, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 55*time.Second)
	defer cancel()
	normalized, err := normalizePublicURL(raw)
	if err != nil {
		return nil, 0, err
	}
	raw = normalized
	u, _ := url.Parse(raw)
	proxy := accountProxy
	// Use the first configured reader's network route for direct HTTP fallback,
	// even when that provider is exhausted or its proxy is unavailable.
	if proxy == "" {
		for _, cfg := range m.configs {
			if _, ok := m.buildProvider(cfg, nil).(PageProvider); ok {
				proxy = cfg.ProxyURL
				break
			}
		}
	}
	// Only providers implementing page extraction participate. Search and page
	// extraction share the configured quota; failed reservations are rolled back.
	for _, cfg := range m.selectByQuotaWeight(ctx, m.filterAvailableProviders(ctx, accountProxy)) {
		if _, ok := m.buildProvider(cfg, nil).(PageProvider); !ok {
			continue
		}
		allowed, reserved := m.tryReserveQuota(ctx, cfg)
		if !allowed {
			continue
		}
		pxy := cfg.ProxyURL
		if accountProxy != "" {
			pxy = accountProxy
		}
		client, e := m.getOrCreateHTTPClient(pxy)
		var p *Page
		if e == nil {
			// Leave time for provider failover and the generic HTTP fallback.
			attempt, done := context.WithTimeout(ctx, 20*time.Second)
			p, e = m.buildProvider(cfg, client).(PageProvider).Fetch(attempt, raw)
			done()
		}
		if e == nil {
			p.FetchMethod = cfg.Type + "_fetch"
			// Missing links are not a provider failure. Never call another paid
			// provider or combine extraction results to complete a successful page.
			if len(p.Links) == 0 {
				linkCtx, done := context.WithTimeout(ctx, 8*time.Second)
				if data, kind, final, err := get(linkCtx, raw, pxy, "text/html"); err == nil && strings.Contains(kind, "html") {
					if source, err := htmlPage(final, data); err == nil {
						supplementPageLinks(p, source, "Links from source HTML:")
					}
				}
				done()
			}
			return p, 1, nil
		}
		if reserved {
			m.rollbackQuota(ctx, cfg)
		}
	}
	data, kind, final, e := get(ctx, raw, proxy, "text/html,text/plain,application/json;q=0.9,*/*;q=0.1")
	if e != nil {
		return nil, 0, e
	}
	if strings.Contains(kind, "html") {
		p, e := htmlPage(final, data)
		if p != nil {
			p.FetchMethod = "http"
		}
		return p, 0, e
	}
	if strings.HasPrefix(kind, "text/") || strings.Contains(kind, "json") || strings.HasSuffix(u.Path, ".md") {
		p := textPage(final, final, string(data))
		p.FetchMethod = "http"
		return p, 0, nil
	}
	return nil, 0, fmt.Errorf("unsupported page content type; HTML, Markdown and text are supported")
}

func supplementPageLinks(page, source *Page, label string) {
	if len(page.Links) != 0 || len(source.Links) == 0 {
		return
	}
	// Keep a bounded, explicitly labelled link index separate from extracted text.
	page.Links = append([]string(nil), source.Links[:min(len(source.Links), 80)]...)
	page.Lines = append(page.Lines, label)
	for id, link := range page.Links {
		display := []rune(link)
		if len(display) > 300 {
			display = append(display[:300], '…')
		}
		page.Lines = append(page.Lines, pageLines(fmt.Sprintf("[link %d] %s", id, string(display)))...)
	}
}

// PageProvider is an optional capability independent of search support.
type PageProvider interface {
	Fetch(context.Context, string) (*Page, error)
}

func (t *TavilyProvider) Fetch(ctx context.Context, u string) (*Page, error) {
	return t.Extract(ctx, u)
}

func (t *TavilyProvider) Extract(ctx context.Context, u string) (*Page, error) {
	var err error
	u, err = normalizePublicURL(u)
	if err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(map[string]any{"urls": []string{u}, "format": "markdown", "extract_depth": "basic", "timeout": 20})
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.tavily.com/extract", bytes.NewReader(payload))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, e := t.httpClient.Do(req)
	if e != nil {
		return nil, fmt.Errorf("extract request failed")
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes+1))
	if e != nil || len(data) > maxPageBytes {
		return nil, fmt.Errorf("extract response could not be read or exceeds size limit")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("extract returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		Results []struct {
			URL     string `json:"url"`
			Content string `json:"raw_content"`
		} `json:"results"`
	}
	if json.Unmarshal(data, &result) != nil {
		return nil, fmt.Errorf("invalid extract response")
	}
	for _, r := range result.Results {
		if strings.TrimSpace(r.Content) != "" {
			base := u
			if v, e := normalizePublicURL(r.URL); e == nil {
				base = v
			}
			return textPage(base, u, r.Content), nil
		}
	}
	return nil, fmt.Errorf("extract returned no readable page")
}
func addPageLink(p *Page, base, href, label string) string {
	u, e := url.Parse(base)
	if e != nil {
		return label
	}
	link, e := url.Parse(strings.TrimSpace(href))
	if e != nil {
		return label
	}
	resolved := u.ResolveReference(link)
	// Resolve relative links against the source page before reusing the fetch pipeline.
	absolute, e := normalizePublicURL(resolved.String())
	if e != nil {
		return label
	}
	id := -1
	for i, u := range p.Links {
		if u == absolute {
			id = i
			break
		}
	}
	if id < 0 {
		if len(p.Links) >= 300 {
			return label
		}
		id = len(p.Links)
		p.Links = append(p.Links, absolute)
	}
	return label + " [link " + strconv.Itoa(id) + "]"
}
func textPage(base, title, text string) *Page {
	p := &Page{URL: base, Title: title}
	runes := []rune(text)
	if len(runes) > maxPageChars {
		text = string(runes[:maxPageChars])
		p.Truncated = true
	}
	text = markdownLink.ReplaceAllStringFunc(text, func(match string) string {
		v := markdownLink.FindStringSubmatch(match)
		return addPageLink(p, base, v[2], v[1])
	})
	p.Lines = pageLines(markdownEscape.ReplaceAllString(text, "$1"))
	return p
}
func pageLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		runes := []rune(line)
		for len(runes) > 180 {
			lines = append(lines, string(runes[:180]))
			runes = runes[180:]
		}
		if len(runes) > 0 {
			lines = append(lines, string(runes))
		}
	}
	return lines
}
func htmlPage(base string, data []byte) (*Page, error) {
	root, e := html.Parse(bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	p := &Page{URL: base, Title: base}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if b.Len() > maxPageChars*4 {
			return
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "svg", "nav", "footer", "header":
				return
			case "title":
				if n.FirstChild != nil {
					p.Title = n.FirstChild.Data
				}
				return
			}
			if n.Data == "a" {
				href := ""
				for _, a := range n.Attr {
					if a.Key == "href" {
						href = a.Val
					}
				}
				var label strings.Builder
				var collect func(*html.Node)
				collect = func(c *html.Node) {
					if c.Type == html.TextNode {
						label.WriteString(c.Data)
					}
					for x := c.FirstChild; x != nil; x = x.NextSibling {
						collect(x)
					}
				}
				collect(n)
				b.WriteString(addPageLink(p, base, href, strings.TrimSpace(label.String())))
				b.WriteString(" ")
				return
			}
			switch n.Data {
			case "p", "div", "li", "br", "h1", "h2", "h3", "h4", "pre", "tr", "section", "article":
				b.WriteString("\n")
			}
		}
		if n.Type == html.TextNode {
			b.WriteString(strings.Join(strings.Fields(n.Data), " "))
			b.WriteString(" ")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	runes := []rune(b.String())
	if len(runes) > maxPageChars {
		runes = runes[:maxPageChars]
		p.Truncated = true
	}
	p.Lines = pageLines(string(runes))
	if len(p.Lines) == 0 {
		return nil, fmt.Errorf("page contained no readable text")
	}
	return p, nil
}
