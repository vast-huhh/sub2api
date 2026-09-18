//go:build integration

package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// TestUsageLog_SessionIDPersistence proves session_id round-trips from insert to
// read and is omitted (NULL) when absent.
func TestUsageLog_SessionIDPersistence(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newUsageLogRepositoryWithSQL(client, integrationDB)

	user := mustCreateUser(t, client, &service.User{Email: "session-id-" + uuid.NewString() + "@example.com"})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-session-" + uuid.NewString(), Name: "k"})
	account := mustCreateAccount(t, client, &service.Account{Name: "acc-session-" + uuid.NewString()})

	sessionID := "sess-" + uuid.NewString()
	turnState := "prefix-" + strings.Repeat("x", 65536) + "-suffix"
	turnStateLength := len(turnState)

	withSession := &service.UsageLog{
		UserID:               user.ID,
		APIKeyID:             apiKey.ID,
		AccountID:            account.ID,
		RequestID:            uuid.NewString(),
		Model:                "claude-3",
		InputTokens:          10,
		OutputTokens:         5,
		TotalCost:            1.0,
		ActualCost:           1.0,
		SessionID:            &sessionID,
		CodexTurnState:       &turnState,
		CodexTurnStateLength: &turnStateLength,
		CreatedAt:            time.Now().UTC(),
	}
	_, err := repo.Create(ctx, withSession)
	require.NoError(t, err)
	require.NotZero(t, withSession.ID)

	withoutSession := &service.UsageLog{
		UserID:       user.ID,
		APIKeyID:     apiKey.ID,
		AccountID:    account.ID,
		RequestID:    uuid.NewString(),
		Model:        "claude-3",
		InputTokens:  7,
		OutputTokens: 3,
		TotalCost:    0.5,
		ActualCost:   0.5,
		CreatedAt:    time.Now().UTC(),
	}
	_, err = repo.Create(ctx, withoutSession)
	require.NoError(t, err)

	// Round-trip: session id survives insert → read.
	got, err := repo.GetByID(ctx, withSession.ID)
	require.NoError(t, err)
	require.NotNil(t, got.SessionID)
	require.Equal(t, sessionID, *got.SessionID)
	require.Equal(t, &turnState, got.CodexTurnState)
	require.Equal(t, &turnStateLength, got.CodexTurnStateLength)

	// Omission: absent session id reads back as nil (NULL), not empty string.
	gotNone, err := repo.GetByID(ctx, withoutSession.ID)
	require.NoError(t, err)
	require.Nil(t, gotNone.SessionID)
	require.Nil(t, gotNone.CodexTurnState)
	require.Nil(t, gotNone.CodexTurnStateLength)
}

// Old inbound values must remain intact for rollback but must not appear as
// response state in the current admin usage projection.
func TestUsageLog_LegacyInboundTurnStateIsNotResponseState(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newUsageLogRepositoryWithSQL(client, integrationDB)
	user := mustCreateUser(t, client, &service.User{Email: "response-state-" + uuid.NewString() + "@example.com"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-response-" + uuid.NewString(), Name: "k"})
	account := mustCreateAccount(t, client, &service.Account{Name: "acc-response-" + uuid.NewString()})
	log := &service.UsageLog{UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, RequestID: uuid.NewString(), Model: "gpt-5.4", CreatedAt: time.Now().UTC()}
	_, err := repo.Create(ctx, log)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, "UPDATE usage_logs SET codex_turn_state = $1, codex_turn_state_length = $2 WHERE id = $3", "legacy-inbound", len("legacy-inbound"), log.ID)
	require.NoError(t, err)
	got, err := repo.GetByID(ctx, log.ID)
	require.NoError(t, err)
	require.Nil(t, got.CodexTurnState)
	require.Nil(t, got.CodexTurnStateLength)
	var legacy string
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT codex_turn_state FROM usage_logs WHERE id = $1", log.ID).Scan(&legacy))
	require.Equal(t, "legacy-inbound", legacy)
}
