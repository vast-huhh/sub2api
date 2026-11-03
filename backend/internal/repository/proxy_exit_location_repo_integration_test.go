//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestProxyExitLocationRepositoryLifecycle(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	proxy := mustCreateProxy(t, client, &service.Proxy{Name: "exit-location-it", Port: 44004})
	other := mustCreateProxy(t, client, &service.Proxy{Name: "exit-location-it-other", Port: 44005})
	proxyID := proxy.ID
	otherID := other.ID
	follow := mustCreateAccount(t, client, &service.Account{
		Name: "exit-location-follow", Platform: service.PlatformOpenAI, ProxyID: &proxyID,
		Extra: map[string]any{service.OpenAIOutboundTimezoneModeExtraKey: service.OpenAIOutboundTimezoneModeFollowProxy},
	})
	plain := mustCreateAccount(t, client, &service.Account{Name: "exit-location-plain", Platform: service.PlatformOpenAI, ProxyID: &otherID})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM proxy_exit_locations WHERE proxy_id IN ($1, $2)`, proxyID, otherID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id IN ($1, $2)`, follow.ID, plain.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM proxies WHERE id IN ($1, $2)`, proxyID, otherID)
	})

	repo := NewProxyExitLocationRepository(integrationDB)

	ids, err := repo.ListFollowProxyIDs(ctx)
	require.NoError(t, err)
	require.Contains(t, ids, proxyID)
	require.NotContains(t, ids, otherID)

	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, repo.SaveProxyExitLocationSuccess(ctx, &service.ProxyExitLocation{
		ProxyID: proxyID, ConfigFingerprint: "fp-a", ExitIP: "54.178.66.215", CountryCode: "JP",
		Region: "Tokyo", City: "Tokyo", Timezone: "Asia/Tokyo", Source: "chatgpt_trace",
		SucceededAt: &now, AttemptedAt: now, NextProbeAt: now.Add(24 * time.Hour),
	}))

	// Same configuration fails twice: the last location survives.
	require.NoError(t, repo.SaveProxyExitLocationFailure(ctx, proxyID, "fp-a", "down", now.Add(time.Minute), now.Add(16*time.Minute)))
	require.NoError(t, repo.SaveProxyExitLocationFailure(ctx, proxyID, "fp-a", "still down", now.Add(2*time.Minute), now.Add(time.Hour)))
	row := findExitLocation(t, repo, proxyID)
	require.Equal(t, "Asia/Tokyo", row.Timezone)
	require.NotNil(t, row.SucceededAt)
	require.Equal(t, 2, row.FailureCount)
	require.Equal(t, "still down", row.LastError)

	// A new configuration failing must not inherit the old location.
	require.NoError(t, repo.SaveProxyExitLocationFailure(ctx, proxyID, "fp-b", "down", now.Add(3*time.Minute), now.Add(18*time.Minute)))
	row = findExitLocation(t, repo, proxyID)
	require.Equal(t, "fp-b", row.ConfigFingerprint)
	require.Empty(t, row.Timezone)
	require.Empty(t, row.City)
	require.Nil(t, row.SucceededAt)
	require.Equal(t, 1, row.FailureCount)

	// Success resets failure state.
	require.NoError(t, repo.SaveProxyExitLocationSuccess(ctx, &service.ProxyExitLocation{
		ProxyID: proxyID, ConfigFingerprint: "fp-b", ExitIP: "3.115.15.82", CountryCode: "JP",
		Region: "Tokyo", City: "Tokyo", Timezone: "Asia/Tokyo", Source: "chatgpt_trace",
		SucceededAt: &now, AttemptedAt: now, NextProbeAt: now.Add(24 * time.Hour),
	}))
	row = findExitLocation(t, repo, proxyID)
	require.Zero(t, row.FailureCount)
	require.Empty(t, row.LastError)
	require.Equal(t, "3.115.15.82", row.ExitIP)
}

func findExitLocation(t *testing.T, repo service.ProxyExitLocationRepository, proxyID int64) service.ProxyExitLocation {
	t.Helper()
	rows, err := repo.ListProxyExitLocations(context.Background())
	require.NoError(t, err)
	for _, row := range rows {
		if row.ProxyID == proxyID {
			return row
		}
	}
	t.Fatalf("exit location for proxy %d not found", proxyID)
	return service.ProxyExitLocation{}
}
