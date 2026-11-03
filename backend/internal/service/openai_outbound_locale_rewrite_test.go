package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Shape captured from Codex CLI 0.163.0 through a custom provider (cwd redacted).
const capturedCodexEnvironmentContext = "<environment_context>\n  <cwd>/work/demo</cwd>\n  <shell>bash</shell>\n  <current_date>2026-11-03</current_date>\n  <timezone>Asia/Shanghai</timezone>\n  <filesystem><workspace_roots><root>/work/demo</root></workspace_roots><permission_profile type=\"managed\"><file_system type=\"restricted\"><entry access=\"read\"><special>:root</special></entry></file_system></permission_profile></filesystem>\n</environment_context>"

func testOutboundLocale(t *testing.T, tz string, search *openAIOutboundSearchLocation) *openAIOutboundLocale {
	t.Helper()
	loc, err := time.LoadLocation(tz)
	require.NoError(t, err)
	return &openAIOutboundLocale{Timezone: tz, location: loc, Search: search, Source: "test"}
}

func testResponsesBody(t *testing.T, input []any, tools []any) []byte {
	t.Helper()
	body := map[string]any{"model": "gpt-5.5", "input": input}
	if tools != nil {
		body["tools"] = tools
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	return raw
}

func textMessage(role string, texts ...string) map[string]any {
	parts := make([]any, 0, len(texts))
	for _, text := range texts {
		parts = append(parts, map[string]any{"type": "input_text", "text": text})
	}
	return map[string]any{"type": "message", "role": role, "content": parts}
}

func TestOutboundLocaleRewritesCapturedEnvironmentContextTimezoneOnly(t *testing.T) {
	body := testResponsesBody(t, []any{
		textMessage("developer", "You are Codex"),
		textMessage("user", capturedCodexEnvironmentContext),
		textMessage("user", "hi <timezone>Asia/Shanghai</timezone>"),
	}, nil)

	out, stats := applyOpenAIOutboundLocaleToResponsesBody(body, testOutboundLocale(t, "Asia/Tokyo", nil))

	require.Equal(t, 1, stats.EnvironmentBlocks)
	require.Zero(t, stats.Dates, "no create_time: date must stay as the client wrote it")
	want := strings.Replace(capturedCodexEnvironmentContext, "<timezone>Asia/Shanghai</timezone>", "<timezone>Asia/Tokyo</timezone>", 1)
	require.Equal(t, want, gjson.GetBytes(out, "input.1.content.0.text").String())
	require.Equal(t, "hi <timezone>Asia/Shanghai</timezone>", gjson.GetBytes(out, "input.2.content.0.text").String(), "ordinary user text is never touched")
	require.Equal(t, "You are Codex", gjson.GetBytes(out, "input.0.content.0.text").String())
	require.Contains(t, gjson.GetBytes(out, "input.1.content.0.text").Raw, "<timezone>Asia/Tokyo</timezone>", "rewritten markup keeps literal angle brackets")
}

func TestOutboundLocaleIsDeterministicForHistory(t *testing.T) {
	body := testResponsesBody(t, []any{
		textMessage("user", capturedCodexEnvironmentContext),
		textMessage("user", "first"),
		textMessage("user", strings.Replace(capturedCodexEnvironmentContext, "2026-11-03", "2026-11-01", 1)),
	}, nil)
	locale := testOutboundLocale(t, "Asia/Tokyo", nil)
	first, stats := applyOpenAIOutboundLocaleToResponsesBody(body, locale)
	second, _ := applyOpenAIOutboundLocaleToResponsesBody(body, locale)
	require.Equal(t, 2, stats.EnvironmentBlocks)
	require.Equal(t, string(first), string(second))
	require.Contains(t, gjson.GetBytes(first, "input.2.content.0.text").String(), "<current_date>2026-11-01</current_date>", "historical dates are not shifted")

	again, againStats := applyOpenAIOutboundLocaleToResponsesBody(first, locale)
	require.Zero(t, againStats.EnvironmentBlocks, "already aligned blocks are left alone")
	require.Equal(t, string(first), string(again))
}

func TestOutboundLocaleConvertsDateOnlyWithTrustworthyCreateTime(t *testing.T) {
	// 2026-11-03 23:30 Asia/Shanghai == 2026-11-04 00:30 Asia/Tokyo.
	createdAt := time.Date(2026, 11, 3, 23, 30, 0, 0, time.FixedZone("CST", 8*3600))
	item := textMessage("user", capturedCodexEnvironmentContext)
	item["internal_chat_message_metadata_passthrough"] = map[string]any{
		"create_time":        createdAt.Unix(),
		"content_item_kinds": []any{codexEnvironmentContextContentKind},
	}
	body := testResponsesBody(t, []any{item}, nil)

	out, stats := applyOpenAIOutboundLocaleToResponsesBody(body, testOutboundLocale(t, "Asia/Tokyo", nil))
	require.Equal(t, 1, stats.Dates)
	text := gjson.GetBytes(out, "input.0.content.0.text").String()
	require.Contains(t, text, "<current_date>2026-11-04</current_date>")
	require.Contains(t, text, "<timezone>Asia/Tokyo</timezone>")

	// A date that does not match the create time (pasted or forged) is kept.
	mismatched := textMessage("user", strings.Replace(capturedCodexEnvironmentContext, "2026-11-03", "2026-10-01", 1))
	mismatched["internal_chat_message_metadata_passthrough"] = map[string]any{"create_time": createdAt.Unix()}
	out, stats = applyOpenAIOutboundLocaleToResponsesBody(testResponsesBody(t, []any{mismatched}, nil), testOutboundLocale(t, "Asia/Tokyo", nil))
	require.Zero(t, stats.Dates)
	require.Contains(t, gjson.GetBytes(out, "input.0.content.0.text").String(), "<current_date>2026-10-01</current_date>")
}

func TestOutboundLocaleRespectsContentClassifications(t *testing.T) {
	item := textMessage("user", capturedCodexEnvironmentContext, capturedCodexEnvironmentContext)
	item["internal_chat_message_metadata_passthrough"] = map[string]any{
		"content_item_kinds": []any{"user_message", codexEnvironmentContextContentKind},
	}
	out, stats := applyOpenAIOutboundLocaleToResponsesBody(testResponsesBody(t, []any{item}, nil), testOutboundLocale(t, "Asia/Tokyo", nil))
	require.Equal(t, 1, stats.EnvironmentBlocks)
	require.Contains(t, gjson.GetBytes(out, "input.0.content.0.text").String(), "Asia/Shanghai", "a block classified as user text stays")
	require.Contains(t, gjson.GetBytes(out, "input.0.content.1.text").String(), "Asia/Tokyo")
}

func TestOutboundLocaleRewritesDesktopDeveloperTimeContext(t *testing.T) {
	desktop := "<codex_apps_client_time_context>\n<current_date>2026-11-03</current_date>\n<timezone>Asia/Shanghai</timezone>\n</codex_apps_client_time_context>"
	body := testResponsesBody(t, []any{
		textMessage("developer", desktop),
		textMessage("user", desktop), // wrong role for this tag
	}, nil)
	out, stats := applyOpenAIOutboundLocaleToResponsesBody(body, testOutboundLocale(t, "Asia/Singapore", nil))
	require.Equal(t, 1, stats.DesktopBlocks)
	require.Contains(t, gjson.GetBytes(out, "input.0.content.0.text").String(), "<timezone>Asia/Singapore</timezone>")
	require.Contains(t, gjson.GetBytes(out, "input.1.content.0.text").String(), "Asia/Shanghai")
}

func TestOutboundLocaleLeavesAmbiguousContextsUntouched(t *testing.T) {
	cases := map[string]string{
		"duplicate timezone": "<environment_context><timezone>Asia/Shanghai</timezone><timezone>UTC</timezone></environment_context>",
		"nested timezone":    "<environment_context><timezone><b>Asia/Shanghai</b></timezone></environment_context>",
		"malformed":          "<environment_context><timezone>Asia/Shanghai</environment_context>",
		"trailing text":      "<environment_context><timezone>Asia/Shanghai</timezone></environment_context> extra",
		"two roots":          "<environment_context><timezone>Asia/Shanghai</timezone></environment_context><environment_context></environment_context>",
		"no timezone":        "<environment_context><cwd>/x</cwd></environment_context>",
		"timezone too deep":  "<environment_context><filesystem><timezone>Asia/Shanghai</timezone></filesystem></environment_context>",
		"entity in value":    "<environment_context><timezone>Asia&amp;Shanghai</timezone></environment_context>",
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			body := testResponsesBody(t, []any{textMessage("user", text)}, nil)
			out, stats := applyOpenAIOutboundLocaleToResponsesBody(body, testOutboundLocale(t, "Asia/Tokyo", nil))
			require.Zero(t, stats.EnvironmentBlocks)
			require.Equal(t, string(body), string(out))
		})
	}
}

func TestOutboundLocaleWritesCompleteSearchLocation(t *testing.T) {
	search := &openAIOutboundSearchLocation{Country: "JP", Region: "Tokyo", City: "Tokyo", Timezone: "Asia/Tokyo"}
	body := testResponsesBody(t, []any{textMessage("user", "hi")}, []any{
		map[string]any{"type": "web_search", "external_web_access": false, "search_content_types": []any{"text", "image"}},
		map[string]any{"type": "web_search_preview", "user_location": map[string]any{"type": "approximate", "city": "Shanghai", "extra": "x"}},
		map[string]any{"type": "function", "name": "shell"},
	})
	out, stats := applyOpenAIOutboundLocaleToResponsesBody(body, testOutboundLocale(t, "Asia/Tokyo", search))
	require.Equal(t, 2, stats.SearchTools)
	want := `{"type":"approximate","country":"JP","region":"Tokyo","city":"Tokyo","timezone":"Asia/Tokyo"}`
	require.JSONEq(t, want, gjson.GetBytes(out, "tools.0.user_location").Raw, "inserted when the client sent none")
	require.JSONEq(t, want, gjson.GetBytes(out, "tools.1.user_location").Raw, "rebuilt as a whole, never half-updated")
	require.False(t, gjson.GetBytes(out, "tools.2.user_location").Exists())
	require.Equal(t, "false", gjson.GetBytes(out, "tools.0.external_web_access").Raw)

	plain, plainStats := applyOpenAIOutboundLocaleToResponsesBody(body, testOutboundLocale(t, "Asia/Tokyo", nil))
	require.Zero(t, plainStats.SearchTools, "search location off: tools untouched")
	require.Equal(t, string(body), string(plain))
}

func TestOutboundLocaleAlphaSearchSettings(t *testing.T) {
	search := &openAIOutboundSearchLocation{Country: "SG", Region: "Central Singapore", City: "Singapore", Timezone: "Asia/Singapore"}
	out, ok := applyOpenAIOutboundLocaleToAlphaSearchBody([]byte(`{"query":"x","settings":{"search_context_size":"low"}}`), testOutboundLocale(t, "Asia/Singapore", search))
	require.True(t, ok)
	require.Equal(t, "low", gjson.GetBytes(out, "settings.search_context_size").String())
	require.Equal(t, "Singapore", gjson.GetBytes(out, "settings.user_location.city").String())

	out, ok = applyOpenAIOutboundLocaleToAlphaSearchBody([]byte(`{"query":"x"}`), testOutboundLocale(t, "Asia/Singapore", search))
	require.True(t, ok)
	require.Equal(t, "approximate", gjson.GetBytes(out, "settings.user_location.type").String())

	_, ok = applyOpenAIOutboundLocaleToAlphaSearchBody([]byte(`{"query":"x","settings":"bad"}`), testOutboundLocale(t, "Asia/Singapore", search))
	require.False(t, ok)
}

func TestOutboundLocaleNilOrInvalidBodyIsNoop(t *testing.T) {
	body := []byte(`{"input":[`)
	out, stats := applyOpenAIOutboundLocaleToResponsesBody(body, testOutboundLocale(t, "Asia/Tokyo", nil))
	require.Equal(t, string(body), string(out))
	require.False(t, stats.changed())
	valid := testResponsesBody(t, []any{textMessage("user", capturedCodexEnvironmentContext)}, nil)
	out, _ = applyOpenAIOutboundLocaleToResponsesBody(valid, nil)
	require.Equal(t, string(valid), string(out))
}
