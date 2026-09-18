package service

import (
	"net/http"
	"strings"
)

// codexTurnStateFromResponse preserves the full upstream response value. A nil
// header map means no response headers were captured; an observed response
// without this header has length zero. Never fall back to an inbound value.
func codexTurnStateFromResponse(headers http.Header) (*string, *int) {
	if headers == nil {
		return nil, nil
	}
	state := headers.Get(openAICodexTurnStateHeader)
	length := len(state)
	if state == "" {
		return nil, &length
	}
	return &state, &length
}

func optionalTrimmedStringPtr(raw string) *string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func optionalStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

// coalesceRequestedReasoningEffort prefers the client-requested value and falls
// back to the effective/forwarded effort for historical or unmapped rows.
func coalesceRequestedReasoningEffort(requested, forwarded *string) *string {
	if trimmed := optionalStringValue(requested); trimmed != "" {
		return &trimmed
	}
	if trimmed := optionalStringValue(forwarded); trimmed != "" {
		return &trimmed
	}
	return nil
}

func forwardResultBillingModel(requestedModel, upstreamModel string) string {
	if trimmed := strings.TrimSpace(requestedModel); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(upstreamModel)
}

func optionalInt64Ptr(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}
