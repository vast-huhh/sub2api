package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type proxyExitLocationRepository struct {
	db *sql.DB
}

func NewProxyExitLocationRepository(db *sql.DB) service.ProxyExitLocationRepository {
	return &proxyExitLocationRepository{db: db}
}

func (r *proxyExitLocationRepository) ListProxyExitLocations(ctx context.Context) ([]service.ProxyExitLocation, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT proxy_id, config_fingerprint, exit_ip, country_code, region, city, timezone, source,
		       succeeded_at, attempted_at, next_probe_at, failure_count, last_error
		FROM proxy_exit_locations`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []service.ProxyExitLocation
	for rows.Next() {
		var (
			loc         service.ProxyExitLocation
			succeededAt sql.NullTime
		)
		if err := rows.Scan(&loc.ProxyID, &loc.ConfigFingerprint, &loc.ExitIP, &loc.CountryCode, &loc.Region,
			&loc.City, &loc.Timezone, &loc.Source, &succeededAt, &loc.AttemptedAt, &loc.NextProbeAt,
			&loc.FailureCount, &loc.LastError); err != nil {
			return nil, err
		}
		if succeededAt.Valid {
			t := succeededAt.Time
			loc.SucceededAt = &t
		}
		out = append(out, loc)
	}
	return out, rows.Err()
}

func (r *proxyExitLocationRepository) SaveProxyExitLocationSuccess(ctx context.Context, loc *service.ProxyExitLocation) error {
	if loc == nil {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO proxy_exit_locations (
			proxy_id, config_fingerprint, exit_ip, country_code, region, city, timezone, source,
			succeeded_at, attempted_at, next_probe_at, failure_count, last_error, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 0, '', NOW())
		ON CONFLICT (proxy_id) DO UPDATE SET
			config_fingerprint = EXCLUDED.config_fingerprint,
			exit_ip = EXCLUDED.exit_ip,
			country_code = EXCLUDED.country_code,
			region = EXCLUDED.region,
			city = EXCLUDED.city,
			timezone = EXCLUDED.timezone,
			source = EXCLUDED.source,
			succeeded_at = EXCLUDED.succeeded_at,
			attempted_at = EXCLUDED.attempted_at,
			next_probe_at = EXCLUDED.next_probe_at,
			failure_count = 0,
			last_error = '',
			updated_at = NOW()`,
		loc.ProxyID, loc.ConfigFingerprint, loc.ExitIP, loc.CountryCode, loc.Region, loc.City, loc.Timezone,
		loc.Source, loc.SucceededAt, loc.AttemptedAt, loc.NextProbeAt)
	return err
}

// SaveProxyExitLocationFailure keeps the last successful location when the
// configuration is unchanged; a failure for a new configuration clears it so
// a location measured through another egress is never reused.
func (r *proxyExitLocationRepository) SaveProxyExitLocationFailure(ctx context.Context, proxyID int64, fingerprint, lastError string, attemptedAt, nextProbeAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO proxy_exit_locations (
			proxy_id, config_fingerprint, attempted_at, next_probe_at, failure_count, last_error, updated_at
		) VALUES ($1, $2, $3, $4, 1, $5, NOW())
		ON CONFLICT (proxy_id) DO UPDATE SET
			exit_ip      = CASE WHEN proxy_exit_locations.config_fingerprint = EXCLUDED.config_fingerprint THEN proxy_exit_locations.exit_ip ELSE '' END,
			country_code = CASE WHEN proxy_exit_locations.config_fingerprint = EXCLUDED.config_fingerprint THEN proxy_exit_locations.country_code ELSE '' END,
			region       = CASE WHEN proxy_exit_locations.config_fingerprint = EXCLUDED.config_fingerprint THEN proxy_exit_locations.region ELSE '' END,
			city         = CASE WHEN proxy_exit_locations.config_fingerprint = EXCLUDED.config_fingerprint THEN proxy_exit_locations.city ELSE '' END,
			timezone     = CASE WHEN proxy_exit_locations.config_fingerprint = EXCLUDED.config_fingerprint THEN proxy_exit_locations.timezone ELSE '' END,
			source       = CASE WHEN proxy_exit_locations.config_fingerprint = EXCLUDED.config_fingerprint THEN proxy_exit_locations.source ELSE '' END,
			succeeded_at = CASE WHEN proxy_exit_locations.config_fingerprint = EXCLUDED.config_fingerprint THEN proxy_exit_locations.succeeded_at ELSE NULL END,
			failure_count = CASE WHEN proxy_exit_locations.config_fingerprint = EXCLUDED.config_fingerprint THEN proxy_exit_locations.failure_count + 1 ELSE 1 END,
			config_fingerprint = EXCLUDED.config_fingerprint,
			attempted_at = EXCLUDED.attempted_at,
			next_probe_at = EXCLUDED.next_probe_at,
			last_error = EXCLUDED.last_error,
			updated_at = NOW()`,
		proxyID, fingerprint, attemptedAt, nextProbeAt, lastError)
	return err
}

func (r *proxyExitLocationRepository) ListFollowProxyIDs(ctx context.Context) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT proxy_id
		FROM accounts
		WHERE deleted_at IS NULL
		  AND proxy_id IS NOT NULL
		  AND platform = $1
		  AND extra->>$2 = $3`,
		service.PlatformOpenAI, service.OpenAIOutboundTimezoneModeExtraKey, service.OpenAIOutboundTimezoneModeFollowProxy)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
