package service

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// openAIOutboundLocale is the per-attempt outbound locale of one account. It is
// resolved once per send attempt (see OpenAIOutboundLocaleService.ResolveForAccount)
// and applied to the client-generated time contexts and web search location.
type openAIOutboundLocale struct {
	Timezone string
	location *time.Location
	// Search is non-nil only when search location follows the proxy and the
	// exit geo is complete. A partial location is never written.
	Search *openAIOutboundSearchLocation
	// Source describes where the value came from, for logs only.
	Source string
}

type openAIOutboundSearchLocation struct {
	Country  string
	Region   string
	City     string
	Timezone string
}

type openAIOutboundLocaleStats struct {
	EnvironmentBlocks int
	DesktopBlocks     int
	Dates             int
	SearchTools       int
}

func (s openAIOutboundLocaleStats) changed() bool {
	return s.EnvironmentBlocks+s.DesktopBlocks+s.SearchTools > 0
}

const (
	codexEnvironmentContextTag         = "environment_context"
	codexDesktopTimeContextTag         = "codex_apps_client_time_context"
	codexEnvironmentContextContentKind = "environments.environment_context"
	codexDesktopTimeContextContentKind = "additional_content.codex_apps_client_time_context"
	codexCurrentDateLayout             = "2006-01-02"
	// JSON timestamps outside the YYYY-MM-DD range are not usable evidence.
	codexCreateTimeMaxUnixSeconds = 253402300800
)

// applyOpenAIOutboundLocaleToResponsesBody rewrites a Responses-shaped body:
//
//   - user messages whose input_text is one complete <environment_context>
//     document, and developer messages whose input_text is one complete
//     <codex_apps_client_time_context> document (Codex Desktop): only the value
//     of the root's direct <timezone> child changes; every other byte is kept;
//   - <current_date> is converted only when the message carries a trustworthy
//     create_time and the original date matches it in the original timezone.
//     Without that evidence the date is left alone (never "today"-shifted), so
//     history renders identically on every request and prompt caching holds;
//   - when content_item_kinds classifications are present they are
//     authoritative; without them the role + complete root tag decide;
//   - web_search tools get a complete user_location when Search is set.
//
// Anything ambiguous (malformed XML, duplicate timezone tags, non-text content)
// is left untouched. The body is never re-marshaled; only selected string
// values are replaced.
func applyOpenAIOutboundLocaleToResponsesBody(body []byte, locale *openAIOutboundLocale) ([]byte, openAIOutboundLocaleStats) {
	var stats openAIOutboundLocaleStats
	if locale == nil || locale.location == nil || len(body) == 0 || !gjson.ValidBytes(body) {
		return body, stats
	}
	root := gjson.ParseBytes(body)
	if !root.IsObject() {
		return body, stats
	}
	next := body

	if input := root.Get("input"); input.IsArray() {
		input.ForEach(func(itemKey, item gjson.Result) bool {
			tag, kind := codexTimeContextTagForRole(item)
			if tag == "" {
				return true
			}
			metadata := item.Get("internal_chat_message_metadata_passthrough")
			kinds := metadata.Get("content_item_kinds")
			createdAt, haveCreatedAt := codexMessageCreateTime(metadata.Get("create_time"))
			content := item.Get("content")
			if !content.IsArray() {
				return true
			}
			content.ForEach(func(partKey, part gjson.Result) bool {
				if kinds.IsArray() {
					classification := kinds.Get(strconv.FormatInt(partKey.Int(), 10))
					if classification.Type != gjson.String || classification.Str != kind {
						return true
					}
				}
				if part.Get("type").String() != "input_text" {
					return true
				}
				text := part.Get("text")
				if text.Type != gjson.String {
					return true
				}
				rewritten, dateChanged, ok := rewriteCodexTimeContextText(text.Str, tag, locale, createdAt, haveCreatedAt)
				if !ok {
					return true
				}
				path := "input." + strconv.FormatInt(itemKey.Int(), 10) + ".content." + strconv.FormatInt(partKey.Int(), 10) + ".text"
				updated, err := setOpenAIJSONStringRaw(next, path, rewritten)
				if err != nil {
					return true
				}
				next = updated
				if tag == codexEnvironmentContextTag {
					stats.EnvironmentBlocks++
				} else {
					stats.DesktopBlocks++
				}
				if dateChanged {
					stats.Dates++
				}
				return true
			})
			return true
		})
	}

	if locale.Search != nil {
		if tools := root.Get("tools"); tools.IsArray() {
			raw := locale.Search.rawJSON()
			tools.ForEach(func(key, tool gjson.Result) bool {
				if !isOpenAIWebSearchTool(tool) {
					return true
				}
				updated, err := sjson.SetRawBytes(next, "tools."+strconv.FormatInt(key.Int(), 10)+".user_location", raw)
				if err != nil {
					return true
				}
				next = updated
				stats.SearchTools++
				return true
			})
		}
	}
	return next, stats
}

// applyOpenAIOutboundLocaleToAlphaSearchBody aligns /alpha/search
// settings.user_location (same shape as the Responses web_search location).
func applyOpenAIOutboundLocaleToAlphaSearchBody(body []byte, locale *openAIOutboundLocale) ([]byte, bool) {
	if locale == nil || locale.Search == nil || len(body) == 0 || !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return body, false
	}
	if settings := gjson.GetBytes(body, "settings"); settings.Exists() && !settings.IsObject() {
		return body, false
	}
	updated, err := sjson.SetRawBytes(body, "settings.user_location", locale.Search.rawJSON())
	if err != nil {
		return body, false
	}
	return updated, true
}

func codexTimeContextTagForRole(item gjson.Result) (tag, kind string) {
	if !item.IsObject() {
		return "", ""
	}
	if itemType := item.Get("type"); itemType.Exists() && itemType.String() != "message" {
		return "", ""
	}
	switch item.Get("role").String() {
	case "user":
		return codexEnvironmentContextTag, codexEnvironmentContextContentKind
	case "developer":
		return codexDesktopTimeContextTag, codexDesktopTimeContextContentKind
	}
	return "", ""
}

func codexMessageCreateTime(value gjson.Result) (time.Time, bool) {
	if value.Type != gjson.Number || value.Num < 0 || value.Num >= codexCreateTimeMaxUnixSeconds {
		return time.Time{}, false
	}
	return time.Unix(int64(value.Num), 0), true
}

func isOpenAIWebSearchTool(tool gjson.Result) bool {
	if !tool.IsObject() {
		return false
	}
	toolType := tool.Get("type").String()
	return toolType == "web_search" || strings.HasPrefix(toolType, "web_search_")
}

func (l *openAIOutboundSearchLocation) rawJSON() []byte {
	var b bytes.Buffer
	b.WriteString(`{"type":"approximate","country":`)
	b.Write(marshalOpenAIJSONStringNoHTMLEscape(l.Country))
	b.WriteString(`,"region":`)
	b.Write(marshalOpenAIJSONStringNoHTMLEscape(l.Region))
	b.WriteString(`,"city":`)
	b.Write(marshalOpenAIJSONStringNoHTMLEscape(l.City))
	b.WriteString(`,"timezone":`)
	b.Write(marshalOpenAIJSONStringNoHTMLEscape(l.Timezone))
	b.WriteString(`}`)
	return b.Bytes()
}

// setOpenAIJSONStringRaw writes a string without HTML escaping so that "<"/">"
// in context blocks keep the spelling Codex clients send (serde_json does not
// escape them).
func setOpenAIJSONStringRaw(body []byte, path, value string) ([]byte, error) {
	return sjson.SetRawBytes(body, path, marshalOpenAIJSONStringNoHTMLEscape(value))
}

func marshalOpenAIJSONStringNoHTMLEscape(value string) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(value)
	return bytes.TrimRight(b.Bytes(), "\n")
}

var errCodexTimeContextUnsupported = errors.New("unsupported time context")

// rewriteCodexTimeContextText returns the rewritten text and whether it was a
// recognized, rewritable context. Only the direct <timezone> (and, with a
// trustworthy create time, <current_date>) values of the root change.
func rewriteCodexTimeContextText(text, tag string, locale *openAIOutboundLocale, createdAt time.Time, haveCreatedAt bool) (string, bool, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "<"+tag+">") || !strings.HasSuffix(trimmed, "</"+tag+">") {
		return "", false, false
	}
	fields, err := scanCodexTimeContextFields(trimmed, tag)
	if err != nil || fields.timezone == nil {
		return "", false, false
	}
	original := strings.TrimSpace(trimmed[fields.timezone.start:fields.timezone.end])
	if original == locale.Timezone {
		return "", false, false
	}

	type replacement struct {
		span  codexTimeContextSpan
		value string
	}
	replacements := []replacement{{span: *fields.timezone, value: locale.Timezone}}
	dateChanged := false
	if fields.currentDate != nil && haveCreatedAt {
		if source, loadErr := time.LoadLocation(original); loadErr == nil {
			date := strings.TrimSpace(trimmed[fields.currentDate.start:fields.currentDate.end])
			if date == createdAt.In(source).Format(codexCurrentDateLayout) {
				converted := createdAt.In(locale.location).Format(codexCurrentDateLayout)
				if converted != date {
					replacements = append(replacements, replacement{span: *fields.currentDate, value: converted})
					dateChanged = true
				}
			}
		}
	}
	// Apply from the end so earlier offsets stay valid.
	if len(replacements) == 2 && replacements[1].span.start > replacements[0].span.start {
		replacements[0], replacements[1] = replacements[1], replacements[0]
	}
	out := trimmed
	for _, r := range replacements {
		out = out[:r.span.start] + r.value + out[r.span.end:]
	}
	lead := len(text) - len(strings.TrimLeft(text, " \t\r\n"))
	trail := len(text) - len(strings.TrimRight(text, " \t\r\n"))
	return text[:lead] + out + text[len(text)-trail:], dateChanged, true
}

type codexTimeContextSpan struct{ start, end int }

type codexTimeContextFields struct {
	timezone    *codexTimeContextSpan
	currentDate *codexTimeContextSpan
}

// scanCodexTimeContextFields validates that doc is a single well-formed XML
// element named root and locates the character data of its direct <timezone>
// and <current_date> children. Duplicate or non-text children are rejected.
func scanCodexTimeContextFields(doc, root string) (codexTimeContextFields, error) {
	var fields codexTimeContextFields
	dec := xml.NewDecoder(strings.NewReader(doc))
	dec.Strict = true
	depth := 0
	rootClosed := false
	var (
		currentField string
		fieldStart   int
		fieldEnd     int
		fieldParts   int
	)
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fields, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if rootClosed {
				return fields, errCodexTimeContextUnsupported
			}
			depth++
			if depth == 1 {
				if t.Name.Local != root || t.Name.Space != "" || len(t.Attr) != 0 {
					return fields, errCodexTimeContextUnsupported
				}
				continue
			}
			if depth == 2 && (t.Name.Local == "timezone" || t.Name.Local == "current_date") {
				if (t.Name.Local == "timezone" && fields.timezone != nil) ||
					(t.Name.Local == "current_date" && fields.currentDate != nil) {
					return fields, errCodexTimeContextUnsupported
				}
				currentField = t.Name.Local
				fieldStart = int(dec.InputOffset())
				fieldEnd = fieldStart
				fieldParts = 0
				continue
			}
			if depth == 3 && currentField != "" {
				return fields, errCodexTimeContextUnsupported
			}
		case xml.CharData:
			if depth == 2 && currentField != "" {
				fieldParts++
				fieldEnd = int(dec.InputOffset())
			} else if depth == 0 && len(bytes.TrimSpace(t)) != 0 {
				return fields, errCodexTimeContextUnsupported
			}
		case xml.EndElement:
			if depth == 2 && currentField != "" {
				if fieldParts != 1 || strings.TrimSpace(doc[fieldStart:fieldEnd]) == "" ||
					strings.ContainsAny(doc[fieldStart:fieldEnd], "&<") {
					return fields, errCodexTimeContextUnsupported
				}
				span := &codexTimeContextSpan{start: fieldStart, end: fieldEnd}
				// Keep surrounding whitespace inside the tag untouched.
				value := doc[fieldStart:fieldEnd]
				span.start += len(value) - len(strings.TrimLeft(value, " \t\r\n"))
				span.end -= len(value) - len(strings.TrimRight(value, " \t\r\n"))
				if currentField == "timezone" {
					fields.timezone = span
				} else {
					fields.currentDate = span
				}
				currentField = ""
			}
			depth--
			if depth == 0 {
				rootClosed = true
			}
		case xml.ProcInst, xml.Directive:
			return fields, errCodexTimeContextUnsupported
		}
	}
	if !rootClosed || depth != 0 {
		return fields, errCodexTimeContextUnsupported
	}
	return fields, nil
}
