package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const (
	relationRootThread  = "019a6d2c-1b5e-7c3a-9f10-1234567890ab"
	relationChildThread = "019a6d2c-3c4d-7e5f-8a21-abcdef012345"
	relationTurn        = "019a6d2d-0000-7000-8000-000000000001"
)

func relationTestAccount(id int64) *Account {
	return &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{
		"chatgpt_account_id": fmt.Sprintf("chatgpt-account-%d", id),
		"chatgpt_user_id":    fmt.Sprintf("user-%d", id),
	}}
}

func relationTurnMetadata(t *testing.T, raw string) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &out))
	return out
}

// Root Codex requests use the root thread ID as session_id and prompt_cache_key;
// the window ID embeds it. Scoping must keep all of these relations.
func TestCodexAccountIdentityPreservesRootThreadRelations(t *testing.T) {
	account := relationTestAccount(11)
	turnMetadata := fmt.Sprintf(`{"session_id":%q,"thread_id":%q,"turn_id":%q,"window_id":"%s:3"}`, relationRootThread, relationRootThread, relationTurn, relationRootThread)
	body := []byte(fmt.Sprintf(`{"prompt_cache_key":%q,"client_metadata":{"session_id":%q,"thread_id":%q,"x-codex-window-id":"%s:3","x-codex-turn-metadata":%q}}`,
		relationRootThread, relationRootThread, relationRootThread, relationRootThread, turnMetadata))

	scoped, changed, err := applyCodexAccountIdentityClientMetadataRaw(body, account, 77)
	require.NoError(t, err)
	require.True(t, changed)

	session := gjson.GetBytes(scoped, "client_metadata.session_id").String()
	require.NotEqual(t, relationRootThread, session)
	require.Equal(t, session, gjson.GetBytes(scoped, "client_metadata.thread_id").String(), "root thread must stay equal to session")
	require.Equal(t, session, gjson.GetBytes(scoped, "prompt_cache_key").String())
	require.Equal(t, session+":3", gjson.GetBytes(scoped, "client_metadata.x-codex-window-id").String(), "window keeps thread prefix and number")

	embedded := relationTurnMetadata(t, gjson.GetBytes(scoped, "client_metadata.x-codex-turn-metadata").String())
	require.Equal(t, session, embedded["session_id"])
	require.Equal(t, session, embedded["thread_id"])
	require.Equal(t, session+":3", embedded["window_id"])
	require.NotEqual(t, relationTurn, embedded["turn_id"])
}

// Subagent requests carry a distinct thread plus references to the parent.
func TestCodexAccountIdentityPreservesSubagentReferences(t *testing.T) {
	account := relationTestAccount(11)
	turnMetadata := fmt.Sprintf(`{"session_id":%q,"thread_id":%q,"parent_thread_id":%q,"turn_id":%q,"root_turn_id":%q,"window_id":"%s:0"}`,
		relationRootThread, relationChildThread, relationRootThread, relationTurn, relationTurn, relationChildThread)
	body := []byte(fmt.Sprintf(`{"prompt_cache_key":"review:%s","client_metadata":{"session_id":%q,"thread_id":%q,"x-codex-parent-thread-id":%q,"x-codex-turn-metadata":%q}}`,
		relationRootThread, relationRootThread, relationChildThread, relationRootThread, turnMetadata))

	scoped, _, err := applyCodexAccountIdentityClientMetadataRaw(body, account, 77)
	require.NoError(t, err)

	rootScoped := scopeCodexAccountIdentityValue(account, 77, "session", relationRootThread)
	childScoped := gjson.GetBytes(scoped, "client_metadata.thread_id").String()
	require.NotEqual(t, rootScoped, childScoped, "distinct threads stay distinct")
	require.NotEqual(t, relationChildThread, childScoped)
	require.Equal(t, rootScoped, gjson.GetBytes(scoped, "client_metadata.session_id").String())
	require.Equal(t, rootScoped, gjson.GetBytes(scoped, "client_metadata.x-codex-parent-thread-id").String(), "parent reference points at the scoped parent")
	require.Equal(t, "review:"+rootScoped, gjson.GetBytes(scoped, "prompt_cache_key").String(), "composite prompt cache key keeps its source")

	embedded := relationTurnMetadata(t, gjson.GetBytes(scoped, "client_metadata.x-codex-turn-metadata").String())
	require.Equal(t, childScoped, embedded["thread_id"])
	require.Equal(t, rootScoped, embedded["parent_thread_id"])
	require.Equal(t, embedded["turn_id"], embedded["root_turn_id"], "root turn reference follows turn_id")
	require.Equal(t, childScoped+":0", embedded["window_id"])
	require.NotContains(t, string(scoped), relationRootThread, "raw client thread IDs must not leak upstream")
	require.NotContains(t, string(scoped), relationChildThread)
}

func TestCodexAccountIdentityHeadersPreserveRelations(t *testing.T) {
	account := relationTestAccount(11)
	headers := http.Header{}
	headers.Set("session-id", relationRootThread)
	headers.Set("thread-id", relationChildThread)
	headers.Set("x-codex-parent-thread-id", relationRootThread)
	headers.Set("x-codex-window-id", relationChildThread+":2")

	applyCodexAccountIdentityHeaders(headers, account, 77)

	rootScoped := headers.Get("session-id")
	childScoped := headers.Get("thread-id")
	require.NotEqual(t, relationRootThread, rootScoped)
	require.NotEqual(t, rootScoped, childScoped)
	require.Equal(t, rootScoped, headers.Get("x-codex-parent-thread-id"))
	require.Equal(t, childScoped+":2", headers.Get("x-codex-window-id"))
}

// Deploying the relation fix must not churn session_id / prompt_cache_key,
// otherwise every live conversation loses its upstream prompt cache once.
func TestCodexAccountIdentitySessionDerivationIsUnchanged(t *testing.T) {
	account := relationTestAccount(11)
	legacy := deriveStableUUIDv4(fmt.Sprintf(
		"sub2api:codex-account-identity:%s:user:%d:account:%s:kind:%s:value:%s",
		codexAccountIdentityNamespaceVersion, int64(77), codexAccountIdentityNamespace(account), "session", relationRootThread,
	))
	require.Equal(t, legacy, scopeCodexAccountIdentityValue(account, 77, "session", relationRootThread))
	require.Equal(t, legacy, scopeCodexAccountIdentityValue(account, 77, "thread", relationRootThread))
}

func TestCodexAccountIdentityNonCompositeValuesKeepWholeStringScoping(t *testing.T) {
	account := relationTestAccount(11)
	for _, raw := range []string{"client-window", "not-a-uuid:3", relationRootThread + ":x"} {
		scoped := scopeCodexAccountIdentityValue(account, 77, "window", raw)
		require.NotEqual(t, raw, scoped)
		require.False(t, strings.Contains(scoped, ":"), raw)
	}
	plainKey := scopeCodexAccountIdentityValue(account, 77, "prompt-cache", "my app key")
	require.NotEqual(t, "my app key", plainKey)
	require.False(t, strings.Contains(plainKey, ":"))
}

func TestCodexAccountIdentityCompositeStillIsolatesAccounts(t *testing.T) {
	first := scopeCodexAccountIdentityValue(relationTestAccount(11), 77, "window", relationRootThread+":1")
	second := scopeCodexAccountIdentityValue(relationTestAccount(19), 77, "window", relationRootThread+":1")
	otherKey := scopeCodexAccountIdentityValue(relationTestAccount(11), 78, "window", relationRootThread+":1")
	require.NotEqual(t, first, second)
	require.NotEqual(t, first, otherKey)
}
