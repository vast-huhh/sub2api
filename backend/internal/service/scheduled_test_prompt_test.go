//go:build unit

package service

import (
	"context"
	"net/http"
	"regexp"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestScheduledTestPrompt(t *testing.T) {
	t.Parallel()
	pattern := regexp.MustCompile(`^[1-9][+*/-][1-9]=\?$`)
	for range 1000 {
		require.Regexp(t, pattern, newScheduledTestPrompt())
	}

	ctx := context.Background()
	require.Equal(t, "hi", scheduledTestPrompt(ctx, "hi"))
	require.Equal(t, "custom prompt", scheduledTestPrompt(ctx, "custom prompt"))
	for _, prompt := range []string{"1+1=?", "1-9=?", "9*9=?", "7/3=?"} {
		scheduledCtx := context.WithValue(ctx, scheduledTestPromptKey{}, prompt)
		require.Equal(t, prompt, scheduledTestPrompt(scheduledCtx, "hi"))
		require.Equal(t, prompt, scheduledTestPrompt(scheduledCtx, ""))
	}
	require.Equal(t, "hi", scheduledTestPrompt(ctx, "hi"))
}

func TestRunTestBackground_ArithmeticPrompt(t *testing.T) {
	for _, tc := range []struct {
		name        string
		platform    string
		accountType string
		model       string
		credentials map[string]any
		extra       map[string]any
		promptPath  string
		response    string
	}{
		{
			name: "openai_oauth", platform: PlatformOpenAI, accountType: AccountTypeOAuth,
			model: "gpt-5.4", credentials: map[string]any{"access_token": "test-token"},
			promptPath: "input.0.content.0.text",
			response:   "data: {\"type\":\"response.completed\"}\n\n",
		},
		{
			name: "openai_pat", platform: PlatformOpenAI, accountType: AccountTypeOAuth,
			model: "gpt-5.4", credentials: map[string]any{
				"access_token": "test-pat", "auth_mode": OpenAIAuthModePersonalAccessToken,
			},
			promptPath: "input.0.content.0.text",
			response:   "data: {\"type\":\"response.completed\"}\n\n",
		},
		{
			name: "openai_responses", platform: PlatformOpenAI, accountType: AccountTypeAPIKey,
			model: "gpt-5.4", credentials: map[string]any{"api_key": "test-key"},
			extra:      map[string]any{openai_compat.ExtraKeyResponsesSupported: true},
			promptPath: "input.0.content.0.text",
			response:   "data: {\"type\":\"response.completed\"}\n\n",
		},
		{
			name: "openai_chat", platform: PlatformOpenAI, accountType: AccountTypeAPIKey,
			model: "gpt-4o", credentials: map[string]any{"api_key": "test-key"},
			extra:      map[string]any{openai_compat.ExtraKeyResponsesSupported: false},
			promptPath: "messages.0.content",
			response:   "data: {\"choices\":[{\"delta\":{\"content\":\"42\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
		},
		{
			name: "grok", platform: PlatformGrok, accountType: AccountTypeAPIKey,
			model: "grok-4.3", credentials: map[string]any{"api_key": "test-key"},
			promptPath: "input",
			response:   "data: {\"type\":\"response.completed\"}\n\n",
		},
		{
			name: "claude", platform: PlatformAnthropic, accountType: AccountTypeAPIKey,
			model: "claude-sonnet-4-6", credentials: map[string]any{"api_key": "test-key"},
			promptPath: "messages.0.content.0.text",
			response:   "data: {\"type\":\"message_stop\"}\n\n",
		},
		{
			name: "gemini", platform: PlatformGemini, accountType: AccountTypeAPIKey,
			model: "gemini-2.5-flash", credentials: map[string]any{"api_key": "test-key"},
			promptPath: "contents.0.parts.0.text",
			response:   "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"42\"}]},\"finishReason\":\"STOP\"}]}\n\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{
				ID: 1, Platform: tc.platform, Type: tc.accountType, Status: StatusActive,
				Concurrency: 1, Credentials: tc.credentials, Extra: tc.extra,
			}
			svc, upstream := adaptiveCNAccountTestService(account,
				newJSONResponse(http.StatusOK, tc.response),
				newJSONResponse(http.StatusOK, tc.response),
			)

			result, err := svc.RunTestBackground(context.Background(), account.ID, tc.model)
			require.NoError(t, err)
			require.Equal(t, "success", result.Status, result.ErrorMessage)
			require.Len(t, upstream.requests, 1)
			require.Regexp(t, `^[1-9][+*/-][1-9]=\?$`, gjson.GetBytes(upstream.lastBody, tc.promptPath).String())

			// A later manual test must not inherit the scheduled prompt.
			c, _ := newTestContext()
			require.NoError(t, svc.TestAccountConnection(c, account.ID, tc.model, "", AccountTestModeDefault))
			require.Equal(t, "hi", gjson.GetBytes(upstream.lastBody, tc.promptPath).String())
		})
	}
}

func TestRunTestBackground_ShadowPATArithmeticPrompt(t *testing.T) {
	parentID := int64(1)
	parent := &Account{
		ID: parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Credentials: map[string]any{"access_token": "parent-pat", "auth_mode": OpenAIAuthModePersonalAccessToken},
	}
	shadow := &Account{
		ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		ParentAccountID: &parentID, QuotaDimension: QuotaDimensionSpark,
	}
	svc, upstream := adaptiveCNAccountTestService(shadow, adaptiveCNResponsesTestResponse())
	svc.accountRepo.(*openAIAccountTestRepo).accountsByID[parentID] = parent
	result, err := svc.RunTestBackground(context.Background(), shadow.ID, "gpt-5.3-codex-spark")
	require.NoError(t, err)
	require.Equal(t, "success", result.Status, result.ErrorMessage)
	require.Equal(t, "Bearer parent-pat", upstream.lastReq.Header.Get("Authorization"))
	require.Regexp(t, `^[1-9][+*/-][1-9]=\?$`, gjson.GetBytes(upstream.lastBody, "input.0.content.0.text").String())
}

func TestRunTestBackground_AdaptiveUsesOnePrompt(t *testing.T) {
	account := adaptiveCNAccountTestAccount(1, PlatformDeepseek)
	svc, upstream := adaptiveCNAccountTestService(account,
		adaptiveCNChatTestResponse(), adaptiveCNAnthropicTestResponse(), adaptiveCNResponsesTestResponse(),
	)
	result, err := svc.RunTestBackground(context.Background(), account.ID, "deepseek-chat")
	require.NoError(t, err)
	require.Equal(t, "success", result.Status, result.ErrorMessage)
	require.Len(t, upstream.requests, 3)
	var prompt string
	for i, path := range []string{"messages.0.content", "messages.0.content.0.text", "input.0.content.0.text"} {
		body := upstream.bodies[i]
		got := gjson.GetBytes(body, path).String()
		require.Regexp(t, `^[1-9][+*/-][1-9]=\?$`, got)
		if i == 0 {
			prompt = got
		} else {
			require.Equal(t, prompt, got)
		}
	}
}

func TestRunTestBackground_ImagePromptUnchanged(t *testing.T) {
	for _, tc := range []struct {
		platform, model, path, expected, response string
	}{
		{PlatformOpenAI, "gpt-image-2", "prompt", defaultOpenAIImageTestPrompt, `{"data":[{"b64_json":"aGVsbG8="}]}`},
		{PlatformGemini, "gemini-2.5-flash-image", "contents.0.parts.0.text", defaultGeminiImageTestPrompt,
			"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"image\"}]},\"finishReason\":\"STOP\"}]}\n\n"},
	} {
		t.Run(tc.platform, func(t *testing.T) {
			account := &Account{ID: 1, Platform: tc.platform, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test-key"}}
			svc, upstream := adaptiveCNAccountTestService(account, newJSONResponse(http.StatusOK, tc.response))
			result, err := svc.RunTestBackground(context.Background(), account.ID, tc.model)
			require.NoError(t, err)
			require.Equal(t, "success", result.Status, result.ErrorMessage)
			require.Equal(t, tc.expected, gjson.GetBytes(upstream.lastBody, tc.path).String())
		})
	}
}

func TestScheduledTestPrompt_Antigravity(t *testing.T) {
	svc := &AntigravityGatewayService{}
	for _, tc := range []struct {
		name, model string
		build       func(context.Context, string, string) ([]byte, error)
	}{
		{"gemini", "gemini-2.5-flash", svc.buildGeminiTestRequest},
		{"claude", "claude-sonnet-4-6", svc.buildClaudeTestRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), scheduledTestPromptKey{}, "9/3=?")
			body, err := tc.build(ctx, "test-project", tc.model)
			require.NoError(t, err)
			require.Equal(t, "9/3=?", gjson.GetBytes(body, "request.contents.0.parts.0.text").String())
			require.EqualValues(t, 128, gjson.GetBytes(body, "request.generationConfig.maxOutputTokens").Int())

			body, err = tc.build(context.Background(), "test-project", tc.model)
			require.NoError(t, err)
			require.Equal(t, ".", gjson.GetBytes(body, "request.contents.0.parts.0.text").String())
			require.EqualValues(t, 1, gjson.GetBytes(body, "request.generationConfig.maxOutputTokens").Int())
		})
	}
	ctx := context.WithValue(context.Background(), scheduledTestPromptKey{}, "9/3=?")
	body, err := svc.buildGeminiTestRequest(ctx, "test-project", "gemini-2.5-flash-image")
	require.NoError(t, err)
	require.Equal(t, ".", gjson.GetBytes(body, "request.contents.0.parts.0.text").String())
	require.EqualValues(t, 1, gjson.GetBytes(body, "request.generationConfig.maxOutputTokens").Int())
}

func TestRunTestBackground_UpstreamErrorStillFails(t *testing.T) {
	account := &Account{
		ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "test-token"},
	}
	svc, _ := adaptiveCNAccountTestService(account, newJSONResponse(http.StatusServiceUnavailable, `{"error":"unavailable"}`))
	result, err := svc.RunTestBackground(context.Background(), account.ID, "gpt-5.4")
	require.NoError(t, err)
	require.Equal(t, "failed", result.Status)
	require.Contains(t, result.ErrorMessage, "503")
}
