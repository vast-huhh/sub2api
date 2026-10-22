package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/websearch"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

type webReadCommand struct {
	Kind, Ref, Pattern string
	Line, ID           int
}
type alphaWebCommands struct {
	Queries []string
	Reads   []webReadCommand
	Length  string
}

func webOutputBudget(length string) int {
	switch length {
	case "short":
		return 6000
	case "long":
		return 24000
	default:
		return 12000
	}
}
func webReadScope(c *gin.Context, body []byte) string {
	var keyID int64
	if v, ok := c.Get("api_key"); ok {
		if key, ok := v.(*APIKey); ok && key != nil {
			keyID = key.ID
		}
	}
	if keyID == 0 { // Unit-test/direct service calls must not share anonymous state.
		if scope, ok := c.Get("web_read_anonymous_scope"); ok {
			return scope.(string)
		}
		scope := "anonymous:" + uuid.NewString()
		c.Set("web_read_anonymous_scope", scope)
		return scope
	}
	session := ExtractClientSessionID(c)
	if session == "" {
		session = sanitizeSessionID(gjson.GetBytes(body, "id").String())
	}
	if session == "" {
		session = sanitizeSessionID(gjson.GetBytes(body, "prompt_cache_key").String())
	}
	return fmt.Sprintf("key:%d:session:%s", keyID, session)
}
func parseAlphaWebCommands(body []byte) (alphaWebCommands, error) {
	out := alphaWebCommands{Length: "medium"}
	commands := gjson.GetBytes(body, "commands")
	if !commands.IsObject() {
		return out, fmt.Errorf("simulated web requests require commands")
	}
	for k, v := range commands.Map() {
		if !v.Exists() || v.Type == gjson.Null || v.Raw == "[]" {
			continue
		}
		switch k {
		case "search_query", "open", "find", "click", "response_length":
		default:
			return out, fmt.Errorf("simulated web requests do not support command %q", k)
		}
	}
	if v := commands.Get("response_length"); v.Exists() && v.Type != gjson.Null {
		if v.Type != gjson.String || (v.String() != "short" && v.String() != "medium" && v.String() != "long") {
			return out, fmt.Errorf("response_length must be short, medium or long")
		}
		out.Length = v.String()
	}
	for _, path := range []string{"settings.filters", "settings.user_location"} {
		if v := gjson.GetBytes(body, path); v.Exists() && v.Type != gjson.Null && v.Raw != "{}" {
			return out, fmt.Errorf("simulated web requests do not support %s", path)
		}
	}
	if v := gjson.GetBytes(body, "settings.external_web_access"); v.Exists() && !v.Bool() {
		return out, fmt.Errorf("simulated web requests require external_web_access=true")
	}
	for _, kind := range []string{"search_query", "open", "find", "click"} {
		arr := commands.Get(kind)
		if !arr.Exists() || arr.Type == gjson.Null {
			continue
		}
		if !arr.IsArray() {
			return out, fmt.Errorf("%s must be an array", kind)
		}
		if kind == "search_query" && len(arr.Array()) > responsesSearchLimit {
			return out, fmt.Errorf("at most %d search queries are allowed", responsesSearchLimit)
		}
		for _, v := range arr.Array() {
			if !v.IsObject() {
				return out, fmt.Errorf("%s entries must be objects", kind)
			}
			allowed := map[string]bool{"ref_id": true}
			switch kind {
			case "search_query":
				allowed = map[string]bool{"q": true}
			case "open":
				allowed["lineno"] = true
			case "find":
				allowed["pattern"] = true
			case "click":
				allowed["id"] = true
			}
			for k, x := range v.Map() {
				if !allowed[k] && x.Type != gjson.Null && x.Raw != "[]" {
					return out, fmt.Errorf("unsupported %s option %q", kind, k)
				}
			}
			if kind == "search_query" {
				q := v.Get("q")
				if q.Type != gjson.String || strings.TrimSpace(q.String()) == "" || len([]rune(q.String())) > 400 {
					return out, fmt.Errorf("search q must be a nonempty string of at most 400 characters")
				}
				out.Queries = append(out.Queries, strings.TrimSpace(q.String()))
				continue
			}
			ref := v.Get("ref_id")
			if ref.Type != gjson.String || strings.TrimSpace(ref.String()) == "" || len(ref.String()) > 8192 {
				return out, fmt.Errorf("%s requires a valid ref_id or URL", kind)
			}
			cmd := webReadCommand{Kind: kind, Ref: strings.TrimSpace(ref.String())}
			if kind == "find" {
				p := v.Get("pattern")
				if p.Type != gjson.String || p.String() == "" || len([]rune(p.String())) > 300 {
					return out, fmt.Errorf("find requires a nonempty pattern of at most 300 characters")
				}
				cmd.Pattern = p.String()
			}
			field := "lineno"
			if kind == "click" {
				field = "id"
			}
			if n := v.Get(field); n.Exists() && n.Type != gjson.Null {
				if n.Type != gjson.Number || n.Float() != float64(n.Int()) || n.Int() < 0 || n.Int() > 1000000 {
					return out, fmt.Errorf("%s must be a nonnegative integer", field)
				}
				if kind == "click" {
					cmd.ID = int(n.Int())
				} else {
					cmd.Line = int(n.Int())
				}
			} else if kind == "click" {
				return out, fmt.Errorf("click requires a link id")
			}
			out.Reads = append(out.Reads, cmd)
		}
	}
	count := len(out.Queries) + len(out.Reads)
	if count < 1 || count > 8 {
		return out, fmt.Errorf("provide between 1 and 8 web operations per request")
	}
	return out, nil
}
func executeWebRead(ctx context.Context, reader *websearch.Reader, scope, proxy string, cmd webReadCommand, lines int) (websearch.PageView, websearch.ReadStats, error) {
	if reader == nil {
		return websearch.PageView{}, websearch.ReadStats{}, fmt.Errorf("webpage reader unavailable")
	}
	lines = max(3, min(lines, 120))
	switch cmd.Kind {
	case "open":
		return reader.Open(ctx, scope, cmd.Ref, proxy, cmd.Line, lines)
	case "find":
		return reader.Find(ctx, scope, cmd.Ref, proxy, cmd.Pattern, lines)
	case "click":
		return reader.Click(ctx, scope, cmd.Ref, proxy, cmd.ID, lines)
	}
	return websearch.PageView{}, websearch.ReadStats{}, fmt.Errorf("unknown web operation")
}
func currentWebReader() *websearch.Reader {
	if manager := getWebSearchManager(); manager != nil {
		return manager.Reader()
	}
	return nil
}
func logWebReadStats(result *OpenAIForwardResult) {
	slog.Info("web operations completed", "request_id", result.RequestID, "search_calls", result.WebSearchCalls+result.EmulatedWebSearchCalls, "page_fetches", result.WebReadCalls, "extract_calls", result.WebExtractCalls, "cache_hits", result.WebReadCacheHits)
}
func addWebReadStats(result *OpenAIForwardResult, s websearch.ReadStats) {
	result.WebReadCalls += s.Fetches
	result.WebExtractCalls += s.Extracts
	result.WebReadCacheHits += s.CacheHits
}
func runePrefix(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:max(n, 0)]) + "…"
	}
	return s
}
func hasEmulatedWebReference(body []byte) bool {
	for _, kind := range []string{"open", "find", "click"} {
		for _, entry := range gjson.GetBytes(body, "commands."+kind).Array() {
			ref := entry.Get("ref_id").String()
			if strings.HasPrefix(ref, "page_") || strings.HasPrefix(ref, "search_") {
				return true
			}
		}
	}
	return false
}

func parseInternalWebRead(raw string) (webReadCommand, error) {
	var input map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &input) != nil {
		return webReadCommand{}, fmt.Errorf("invalid webpage reader arguments")
	}
	kind := gjson.Get(raw, "operation").String()
	if kind != "open" && kind != "find" && kind != "click" {
		return webReadCommand{}, fmt.Errorf("invalid webpage reader operation")
	}
	delete(input, "operation")
	body, _ := json.Marshal(map[string]any{"commands": map[string]any{kind: []any{input}}})
	commands, err := parseAlphaWebCommands(body)
	if err != nil {
		return webReadCommand{}, err
	}
	return commands.Reads[0], nil
}
