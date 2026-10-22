package websearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

const pageTTL = time.Hour
const maxCachedPages = 128
const maxPageChars = 180000

// Page is an immutable fetched snapshot. References and link IDs are scoped to
// the authenticated caller, not to the provider account chosen by the scheduler.
type Page struct {
	FetchMethod string   `json:"fetch_method,omitempty"`
	Ref         string   `json:"ref_id"`
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	Lines       []string `json:"lines,omitempty"`
	Links       []string `json:"links,omitempty"`
	Truncated   bool     `json:"truncated,omitempty"`
	Expires     int64    `json:"expires"`
	URLKey      string   `json:"url_key"`
}
type ReadStats struct{ Fetches, Extracts, CacheHits int }
type PageView struct {
	Ref        string `json:"ref_id"`
	URL        string `json:"url"`
	Title      string `json:"title"`
	Content    string `json:"content"`
	TotalLines int    `json:"total_lines"`
	NextLine   *int   `json:"next_line,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	Found      *bool  `json:"found,omitempty"`
}
type PageFetch func(context.Context, string, string) (*Page, int, error)
type Reader struct {
	redis  *redis.Client
	fetch  PageFetch
	mu     sync.Mutex
	memory map[string]*Page
	group  singleflight.Group
}

func NewReader(client *redis.Client, fetch PageFetch) *Reader {
	return &Reader{redis: client, fetch: fetch, memory: make(map[string]*Page)}
}
func hashPageKey(s string) string   { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func pageScope(scope string) string { return "webread:v2:{" + hashPageKey(scope) + "}" }
func newPageRef() string            { return "page_" + strings.ReplaceAll(uuid.NewString(), "-", "") }

// Cache scripts cap stored snapshots per identity and remove matching URL
// indexes on eviction. Individual entries still expire without further traffic.
var putPageScript = redis.NewScript(`
redis.call('SET',KEYS[1],ARGV[1],'EX',ARGV[2])
redis.call('HSET',KEYS[2],ARGV[3],ARGV[4])
redis.call('ZADD',KEYS[3],ARGV[5],ARGV[4]..'|'..ARGV[3])
redis.call('EXPIRE',KEYS[2],ARGV[2]); redis.call('EXPIRE',KEYS[3],ARGV[2])
local count=redis.call('ZCARD',KEYS[3])
if count > tonumber(ARGV[6]) then
 local old=redis.call('ZRANGE',KEYS[3],0,count-tonumber(ARGV[6])-1)
 for _,member in ipairs(old) do
  local ref,urlkey=string.match(member,'^(.-)|(.+)$')
  local key=ARGV[7]..':'..ref
  if redis.call('HGET',KEYS[2],urlkey)==ref then redis.call('HDEL',KEYS[2],urlkey) end
  redis.call('DEL',key); redis.call('ZREM',KEYS[3],member)
 end
end
return 1`)

func (r *Reader) put(ctx context.Context, scope string, p *Page) error {
	p.Expires = time.Now().Add(pageTTL).Unix()
	p.URLKey = hashPageKey(p.URL)
	base := pageScope(scope)
	if r.redis != nil {
		raw, err := json.Marshal(p)
		if err != nil {
			return err
		}
		return putPageScript.Run(ctx, r.redis, []string{base + ":" + p.Ref, base + ":urls", base + ":order"}, string(raw), int(pageTTL.Seconds()), p.URLKey, p.Ref, time.Now().UnixMilli(), maxCachedPages, base).Err()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Memory mode is bounded globally and is used when Redis is not configured.
	for key, p := range r.memory {
		if p.Expires <= time.Now().Unix() {
			delete(r.memory, key)
		}
	}
	if len(r.memory) >= maxCachedPages {
		var oldest string
		var expiry int64 = 1<<63 - 1
		for k, p := range r.memory {
			if p.Expires < expiry {
				oldest, expiry = k, p.Expires
			}
		}
		delete(r.memory, oldest)
	}
	copy := *p
	r.memory[base+":"+p.Ref] = &copy
	return nil
}
func (r *Reader) get(ctx context.Context, scope, ref string) (*Page, error) {
	base := pageScope(scope)
	if r.redis != nil {
		raw, err := r.redis.Get(ctx, base+":"+ref).Bytes()
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("page cache unavailable")
		}
		var p Page
		if json.Unmarshal(raw, &p) != nil {
			return nil, fmt.Errorf("invalid cached page")
		}
		if p.Expires <= time.Now().Unix() {
			return nil, nil
		}
		return &p, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.memory[base+":"+ref]
	if p == nil || p.Expires <= time.Now().Unix() {
		return nil, nil
	}
	copy := *p
	return &copy, nil
}
func (r *Reader) byURL(ctx context.Context, scope, u string) (*Page, error) {
	if r.redis != nil {
		ref, err := r.redis.HGet(ctx, pageScope(scope)+":urls", hashPageKey(u)).Result()
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("page cache unavailable")
		}
		return r.get(ctx, scope, ref)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	prefix := pageScope(scope) + ":"
	for k, p := range r.memory {
		if strings.HasPrefix(k, prefix) && p.URL == u && p.Expires > time.Now().Unix() {
			copy := *p
			return &copy, nil
		}
	}
	return nil, nil
}
func (r *Reader) Remember(ctx context.Context, scope, ref, u, title string) error {
	u, err := normalizePublicURL(u)
	if err != nil {
		return err
	}
	if p, err := r.byURL(ctx, scope, u); err != nil {
		return err
	} else if p != nil && len(p.Lines) > 0 {
		copy := *p
		copy.Ref = ref
		return r.put(ctx, scope, &copy)
	}
	return r.put(ctx, scope, &Page{Ref: ref, URL: u, Title: title})
}
func (r *Reader) resolve(ctx context.Context, scope, ref, proxy string) (*Page, ReadStats, error) {
	var stats ReadStats
	var p *Page
	var err error
	u := ""
	if strings.HasPrefix(ref, "https://") || strings.HasPrefix(ref, "http://") {
		u, err = normalizePublicURL(ref)
		if err == nil {
			p, err = r.byURL(ctx, scope, u)
		}
	} else {
		if len(ref) > 160 || strings.ContainsAny(ref, "/ :\n\r\t") {
			return nil, stats, fmt.Errorf("invalid page reference")
		}
		p, err = r.get(ctx, scope, ref)
		if err == nil && p == nil {
			return nil, stats, fmt.Errorf("unknown or expired page reference; open the source URL again")
		}
		if p != nil {
			u = p.URL
		}
	}
	if err != nil {
		return nil, stats, err
	}
	if p != nil && len(p.Lines) > 0 {
		stats.CacheHits = 1
		return p, stats, nil
	}
	executed := false
	v, err, _ := r.group.Do(pageScope(scope)+":"+hashPageKey(u), func() (any, error) {
		if cached, e := r.byURL(ctx, scope, u); e != nil {
			return nil, e
		} else if cached != nil && len(cached.Lines) > 0 {
			return struct {
				p *Page
				s ReadStats
			}{cached, ReadStats{CacheHits: 1}}, nil
		}
		executed = true
		fetched, extracts, e := r.fetch(ctx, u, proxy)
		if e != nil {
			return nil, e
		}
		if fetched == nil || len(fetched.Lines) == 0 {
			return nil, fmt.Errorf("page contained no readable text")
		}
		fetched.Ref = newPageRef()
		if p != nil {
			fetched.Ref = p.Ref
		}
		fetched.URL = u
		if e = r.put(ctx, scope, fetched); e != nil {
			return nil, fmt.Errorf("page cache unavailable")
		}
		return struct {
			p *Page
			s ReadStats
		}{fetched, ReadStats{Fetches: 1, Extracts: extracts}}, nil
	})
	if err != nil {
		return nil, stats, err
	}
	result := v.(struct {
		p *Page
		s ReadStats
	})
	if !executed {
		result.s = ReadStats{CacheHits: 1}
	}
	return result.p, result.s, nil
}
func viewPage(p *Page, start, lines int) PageView {
	if start < 0 {
		start = 0
	}
	if start > len(p.Lines) {
		start = len(p.Lines)
	}
	end := min(start+lines, len(p.Lines))
	var b strings.Builder
	for i := start; i < end; i++ {
		fmt.Fprintf(&b, "L%d: %s\n", i, p.Lines[i])
	}
	v := PageView{Ref: p.Ref, URL: p.URL, Title: p.Title, Content: b.String(), TotalLines: len(p.Lines), Truncated: p.Truncated}
	if end < len(p.Lines) {
		v.NextLine = &end
	}
	return v
}
func (r *Reader) Open(ctx context.Context, scope, ref, proxy string, line, lines int) (PageView, ReadStats, error) {
	p, s, e := r.resolve(ctx, scope, ref, proxy)
	if e != nil {
		return PageView{}, s, e
	}
	return viewPage(p, line, lines), s, nil
}
func (r *Reader) Find(ctx context.Context, scope, ref, proxy, pattern string, lines int) (PageView, ReadStats, error) {
	p, s, e := r.resolve(ctx, scope, ref, proxy)
	if e != nil {
		return PageView{}, s, e
	}
	found := false
	var b strings.Builder
	remaining := lines
	previousEnd := 0
	for i, text := range p.Lines {
		if !strings.Contains(strings.ToLower(text), strings.ToLower(pattern)) {
			continue
		}
		found = true
		start, end := max(previousEnd, i-2), min(len(p.Lines), i+3)
		if remaining <= 0 {
			break
		}
		end = min(end, start+remaining)
		if end <= start {
			continue
		}
		b.WriteString(viewPage(p, start, end-start).Content)
		remaining -= end - start
		previousEnd = end
	}
	return PageView{Ref: p.Ref, URL: p.URL, Title: p.Title, Content: b.String(), TotalLines: len(p.Lines), Truncated: p.Truncated, Found: &found}, s, nil
}
func (r *Reader) Click(ctx context.Context, scope, ref, proxy string, id, lines int) (PageView, ReadStats, error) {
	p, s, e := r.resolve(ctx, scope, ref, proxy)
	if e != nil {
		return PageView{}, s, e
	}
	if id < 0 || id >= len(p.Links) {
		return PageView{}, s, fmt.Errorf("link ID not found on this page")
	}
	v, next, e := r.Open(ctx, scope, p.Links[id], proxy, 0, lines)
	s.Fetches += next.Fetches
	s.Extracts += next.Extracts
	s.CacheHits += next.CacheHits
	return v, s, e
}
func normalizePublicURL(raw string) (string, error) {
	if len(raw) > 8192 {
		return "", fmt.Errorf("URL too long")
	}
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return "", fmt.Errorf("only public HTTP(S) URLs without credentials are supported")
	}
	if !publicHost(u.Hostname()) {
		return "", fmt.Errorf("non-public URL is not allowed")
	}
	if port := u.Port(); port != "" && port != "80" && port != "443" {
		return "", fmt.Errorf("only standard web ports are supported")
	}
	u.Fragment = ""
	return u.String(), nil
}
