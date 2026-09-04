package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type balanceCardRepository struct {
	db *sql.DB
}

func NewBalanceCardRepository(db *sql.DB) service.BalanceCardRepository {
	return &balanceCardRepository{db: db}
}

type balanceCardRowScanner interface {
	Scan(dest ...any) error
}

const balanceCardPlanColumns = `
	id, name, description, card_type, validity_days,
	daily_quota_usd::double precision, weekly_quota_usd::double precision,
	monthly_quota_usd::double precision, fallback_default, auto_reset_default,
	max_reset_count, status, sort_order, created_at, updated_at`

func scanBalanceCardPlan(row balanceCardRowScanner) (*service.BalanceCardPlan, error) {
	var p service.BalanceCardPlan
	if err := row.Scan(
		&p.ID, &p.Name, &p.Description, &p.CardType, &p.ValidityDays,
		&p.DailyQuotaUSD, &p.WeeklyQuotaUSD, &p.MonthlyQuotaUSD,
		&p.FallbackDefault, &p.AutoResetDefault,
		&p.MaxResetCount, &p.Status, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *balanceCardRepository) ListPlans(ctx context.Context, includeInactive bool) ([]service.BalanceCardPlan, error) {
	query := `SELECT ` + balanceCardPlanColumns + ` FROM balance_card_plans`
	if !includeInactive {
		query += ` WHERE status = 'active'`
	}
	query += ` ORDER BY sort_order ASC, id ASC`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]service.BalanceCardPlan, 0)
	for rows.Next() {
		plan, err := scanBalanceCardPlan(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *plan)
	}
	return result, rows.Err()
}

func (r *balanceCardRepository) GetPlan(ctx context.Context, id int64) (*service.BalanceCardPlan, error) {
	plan, err := scanBalanceCardPlan(r.db.QueryRowContext(ctx,
		`SELECT `+balanceCardPlanColumns+` FROM balance_card_plans WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrBalanceCardPlanNotFound
	}
	return plan, err
}

func (r *balanceCardRepository) CreatePlan(ctx context.Context, input *service.CreateBalanceCardPlanInput) (*service.BalanceCardPlan, error) {
	query := `INSERT INTO balance_card_plans
		(name, description, card_type, validity_days, daily_quota_usd, weekly_quota_usd,
		 monthly_quota_usd, fallback_default, auto_reset_default, max_reset_count, status, sort_order)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING ` + balanceCardPlanColumns
	return scanBalanceCardPlan(r.db.QueryRowContext(ctx, query,
		input.Name, input.Description, input.CardType, input.ValidityDays, input.DailyQuotaUSD,
		input.WeeklyQuotaUSD, input.MonthlyQuotaUSD, input.FallbackDefault, input.AutoResetDefault,
		input.MaxResetCount, input.Status, input.SortOrder))
}

func (r *balanceCardRepository) UpdatePlan(ctx context.Context, id int64, input *service.UpdateBalanceCardPlanInput) (*service.BalanceCardPlan, error) {
	query := `UPDATE balance_card_plans SET
		name=$1, description=$2, card_type=$3, validity_days=$4, daily_quota_usd=$5,
		weekly_quota_usd=$6, monthly_quota_usd=$7, fallback_default=$8,
		auto_reset_default=$9, max_reset_count=$10, status=$11,
		sort_order=$12, updated_at=NOW()
		WHERE id=$13 RETURNING ` + balanceCardPlanColumns
	plan, err := scanBalanceCardPlan(r.db.QueryRowContext(ctx, query,
		input.Name, input.Description, input.CardType, input.ValidityDays, input.DailyQuotaUSD,
		input.WeeklyQuotaUSD, input.MonthlyQuotaUSD, input.FallbackDefault,
		input.AutoResetDefault, input.MaxResetCount, input.Status, input.SortOrder, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrBalanceCardPlanNotFound
	}
	return plan, err
}

const balanceCardColumns = `
	c.id, c.user_id, COALESCE(u.email, ''), c.plan_id, c.plan_name, c.card_type,
	c.validity_days, c.daily_quota_usd::double precision,
	c.weekly_quota_usd::double precision, c.monthly_quota_usd::double precision,
	c.max_reset_count,
	c.starts_at, c.expires_at,
	CASE WHEN c.status IN ('active','pending') AND c.expires_at <= $1 THEN 'expired' ELSE c.status END,
	c.daily_window_start, c.daily_usage_usd::double precision,
	c.weekly_window_start, c.weekly_usage_usd::double precision,
	c.monthly_usage_usd::double precision,
	c.fallback_enabled, c.auto_reset_enabled, c.reset_count, c.assigned_by,
	c.assigned_at, c.activated_at, c.notes, c.created_at, c.updated_at`

func scanBalanceCard(row balanceCardRowScanner) (*service.UserBalanceCard, error) {
	var c service.UserBalanceCard
	if err := row.Scan(
		&c.ID, &c.UserID, &c.UserEmail, &c.PlanID, &c.PlanName, &c.CardType,
		&c.ValidityDays, &c.DailyQuotaUSD, &c.WeeklyQuotaUSD, &c.MonthlyQuotaUSD,
		&c.MaxResetCount, &c.StartsAt, &c.ExpiresAt, &c.Status,
		&c.DailyWindowStart, &c.DailyUsageUSD, &c.WeeklyWindowStart,
		&c.WeeklyUsageUSD, &c.MonthlyUsageUSD, &c.FallbackEnabled,
		&c.AutoResetEnabled, &c.ResetCount, &c.AssignedBy, &c.AssignedAt,
		&c.ActivatedAt, &c.Notes, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &c, nil
}

func getBalanceCardTx(ctx context.Context, tx *sql.Tx, id int64, now time.Time) (*service.UserBalanceCard, error) {
	query := `SELECT ` + balanceCardColumns + `
		FROM user_balance_cards c JOIN users u ON u.id=c.user_id
		WHERE c.id=$2 AND c.deleted_at IS NULL`
	card, err := scanBalanceCard(tx.QueryRowContext(ctx, query, now, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrBalanceCardNotFound
	}
	return card, err
}

func beginBalanceCardTx(ctx context.Context, db *sql.DB) (*sql.Tx, error) {
	return db.BeginTx(ctx, nil)
}

func rollbackBalanceCardTx(tx *sql.Tx) {
	if tx != nil {
		_ = tx.Rollback()
	}
}

func normalizeUserBalanceCardsTx(ctx context.Context, tx *sql.Tx, userID int64, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE user_balance_cards
		SET status='expired', updated_at=$2
		WHERE user_id=$1 AND status IN ('active','pending') AND expires_at <= $2`, userID, now); err != nil {
		return err
	}
	var activeID int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM user_balance_cards
		WHERE user_id=$1 AND status='active' AND starts_at <= $2 AND expires_at > $2
		LIMIT 1 FOR UPDATE`, userID, now).Scan(&activeID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `SELECT id FROM user_balance_cards
			WHERE user_id=$1 AND status='pending' AND starts_at <= $2 AND expires_at > $2
			ORDER BY starts_at ASC, id ASC LIMIT 1 FOR UPDATE`, userID, now).Scan(&activeID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			today := timezone.StartOfDay(now)
			weekStart := now
			if _, err := tx.ExecContext(ctx, `UPDATE user_balance_cards SET
				status='active', activated_at=COALESCE(activated_at,$2),
				daily_window_start=COALESCE(daily_window_start,$3),
				weekly_window_start=CASE WHEN weekly_quota_usd > 0
					THEN COALESCE(weekly_window_start,$4) ELSE weekly_window_start END,
				updated_at=$2 WHERE id=$1`, activeID, now, today, weekStart); err != nil {
				return err
			}
			if err := insertBalanceCardLedgerTx(ctx, tx, activeID, userID, "activate", 0, 0, 0, nil, nil, nil, nil, nil, nil, "", nil); err != nil {
				return err
			}
		}
	}
	if activeID == 0 {
		return nil
	}

	var cardType string
	var validityDays int
	var dailyWindowStart, weeklyWindowStart *time.Time
	var dailyUsage, weeklyQuota, weeklyUsage float64
	err = tx.QueryRowContext(ctx, `SELECT card_type, validity_days, daily_window_start,
		daily_usage_usd::double precision, weekly_quota_usd::double precision,
		weekly_window_start, weekly_usage_usd::double precision
		FROM user_balance_cards WHERE id=$1 FOR UPDATE`, activeID).
		Scan(&cardType, &validityDays, &dailyWindowStart, &dailyUsage,
			&weeklyQuota, &weeklyWindowStart, &weeklyUsage)
	if err != nil {
		return err
	}
	if validityDays > 1 && dailyWindowStart != nil && timezone.StartOfDay(*dailyWindowStart).Before(timezone.StartOfDay(now)) {
		today := timezone.StartOfDay(now)
		if _, err := tx.ExecContext(ctx, `UPDATE user_balance_cards SET
			daily_usage_usd=0, daily_window_start=$2, updated_at=$3 WHERE id=$1`, activeID, today, now); err != nil {
			return err
		}
		if err := insertBalanceCardLedgerTx(ctx, tx, activeID, userID, "natural_reset", 0, dailyUsage, 0, nil, nil, nil, nil, nil, nil, "", map[string]any{"window": "daily"}); err != nil {
			return err
		}
	}
	if weeklyQuota > 0 && cardType != service.BalanceCardTypeWeek {
		weekStart := now
		if weeklyWindowStart == nil {
			if _, err := tx.ExecContext(ctx, `UPDATE user_balance_cards SET
				weekly_window_start=$2, updated_at=$3 WHERE id=$1`, activeID, weekStart, now); err != nil {
				return err
			}
		} else if !now.Before(weeklyWindowStart.Add(7 * 24 * time.Hour)) {
			elapsedWindows := int64(now.Sub(*weeklyWindowStart) / (7 * 24 * time.Hour))
			weekStart = weeklyWindowStart.Add(time.Duration(elapsedWindows) * 7 * 24 * time.Hour)
			if _, err := tx.ExecContext(ctx, `UPDATE user_balance_cards SET
				weekly_usage_usd=0, weekly_window_start=$2, updated_at=$3 WHERE id=$1`, activeID, weekStart, now); err != nil {
				return err
			}
			if err := insertBalanceCardLedgerTx(ctx, tx, activeID, userID, "natural_reset", 0, 0, 0,
				nil, nil, nil, nil, nil, nil, "", map[string]any{
					"window": "weekly", "weekly_usage_before": weeklyUsage, "weekly_usage_after": 0,
				}); err != nil {
				return err
			}
		}
	}
	return nil
}

func getAssignableBalanceCardPlanTx(ctx context.Context, tx *sql.Tx, planID int64) (*service.BalanceCardPlan, error) {
	plan, err := scanBalanceCardPlan(tx.QueryRowContext(ctx,
		`SELECT `+balanceCardPlanColumns+` FROM balance_card_plans WHERE id=$1`, planID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrBalanceCardPlanNotFound
	}
	if err != nil {
		return nil, err
	}
	if plan.Status != service.StatusActive {
		return nil, service.ErrBalanceCardPlanInactive
	}
	return plan, nil
}

func assignBalanceCardTx(ctx context.Context, tx *sql.Tx, input *service.AssignBalanceCardInput, plan *service.BalanceCardPlan, now time.Time) (*service.UserBalanceCard, error) {
	var userExists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id=$1 AND deleted_at IS NULL`, input.UserID).Scan(&userExists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrUserNotFound
		}
		return nil, err
	}
	if err := normalizeUserBalanceCardsTx(ctx, tx, input.UserID, now); err != nil {
		return nil, err
	}
	var tail sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT MAX(expires_at) FROM user_balance_cards
		WHERE user_id=$1 AND status IN ('active','pending') AND expires_at>$2`, input.UserID, now).Scan(&tail); err != nil {
		return nil, err
	}
	startsAt := now
	status := service.BalanceCardStatusActive
	var activatedAt *time.Time
	if tail.Valid && tail.Time.After(startsAt) {
		startsAt = tail.Time
		status = service.BalanceCardStatusPending
	} else {
		activatedAt = &now
	}
	expiresAt := startsAt.Add(time.Duration(plan.ValidityDays) * 24 * time.Hour)
	var dailyStart *time.Time
	var weeklyStart *time.Time
	if status == service.BalanceCardStatusActive {
		t := timezone.StartOfDay(now)
		dailyStart = &t
		if plan.WeeklyQuotaUSD > 0 {
			w := now
			weeklyStart = &w
		}
	}
	var cardID int64
	err := tx.QueryRowContext(ctx, `INSERT INTO user_balance_cards
		(user_id, plan_id, plan_name, card_type, validity_days, daily_quota_usd,
		 weekly_quota_usd, monthly_quota_usd, max_reset_count, starts_at, expires_at,
		 status, daily_window_start, weekly_window_start, fallback_enabled,
		 auto_reset_enabled, assigned_by, activated_at, notes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,NULLIF($17,0),$18,$19)
		RETURNING id`, input.UserID, input.PlanID, plan.Name, plan.CardType,
		plan.ValidityDays, plan.DailyQuotaUSD, plan.WeeklyQuotaUSD, plan.MonthlyQuotaUSD,
		plan.MaxResetCount, startsAt, expiresAt, status, dailyStart, weeklyStart,
		plan.FallbackDefault, plan.AutoResetDefault, input.AssignedBy, activatedAt,
		strings.TrimSpace(input.Notes)).Scan(&cardID)
	if err != nil {
		return nil, err
	}
	if err := insertBalanceCardLedgerTx(ctx, tx, cardID, input.UserID, "assign", 0, 0, 0,
		nil, &expiresAt, nil, nil, nil, nilInt64Ptr(input.AssignedBy), input.Notes, map[string]any{"plan_id": input.PlanID}); err != nil {
		return nil, err
	}
	card, err := getBalanceCardTx(ctx, tx, cardID, now)
	if err != nil {
		return nil, err
	}
	return card, nil
}

func (r *balanceCardRepository) AssignCard(ctx context.Context, input *service.AssignBalanceCardInput, now time.Time) (_ *service.UserBalanceCard, err error) {
	tx, err := beginBalanceCardTx(ctx, r.db)
	if err != nil {
		return nil, err
	}
	defer rollbackBalanceCardTx(tx)
	plan, err := getAssignableBalanceCardPlanTx(ctx, tx, input.PlanID)
	if err != nil {
		return nil, err
	}
	card, err := assignBalanceCardTx(ctx, tx, input, plan, now)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return card, nil
}

func (r *balanceCardRepository) RedeemCard(ctx context.Context, redeemCodeID, userID, planID int64, code string, now time.Time) (_ *service.UserBalanceCard, err error) {
	tx, err := beginBalanceCardTx(ctx, r.db)
	if err != nil {
		return nil, err
	}
	defer rollbackBalanceCardTx(tx)

	plan, err := getAssignableBalanceCardPlanTx(ctx, tx, planID)
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE redeem_codes
		SET status='used', used_by=$2, used_at=$3
		WHERE id=$1 AND status='unused' AND (expires_at IS NULL OR expires_at>$3)`, redeemCodeID, userID, now)
	if err != nil {
		return nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected != 1 {
		return nil, service.ErrRedeemCodeUsed
	}
	card, err := assignBalanceCardTx(ctx, tx, &service.AssignBalanceCardInput{
		UserID: userID, PlanID: planID,
		Notes: fmt.Sprintf("通过兑换码 %s 兑换", strings.TrimSpace(code)),
	}, plan, now)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return card, nil
}

func (r *balanceCardRepository) AssignCards(ctx context.Context, inputs []*service.AssignBalanceCardInput, now time.Time) (_ []service.UserBalanceCard, err error) {
	if len(inputs) == 0 {
		return []service.UserBalanceCard{}, nil
	}
	if inputs[0] == nil {
		return nil, service.ErrBalanceCardInvalidInput
	}
	planID := inputs[0].PlanID
	for _, input := range inputs {
		if input == nil || input.UserID <= 0 || input.PlanID != planID {
			return nil, service.ErrBalanceCardInvalidInput
		}
	}
	tx, err := beginBalanceCardTx(ctx, r.db)
	if err != nil {
		return nil, err
	}
	defer rollbackBalanceCardTx(tx)
	plan, err := getAssignableBalanceCardPlanTx(ctx, tx, planID)
	if err != nil {
		return nil, err
	}
	result := make([]service.UserBalanceCard, 0, len(inputs))
	for _, input := range inputs {
		card, err := assignBalanceCardTx(ctx, tx, input, plan, now)
		if err != nil {
			return nil, err
		}
		result = append(result, *card)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return result, nil
}

func nilInt64Ptr(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

func (r *balanceCardRepository) ListUserCards(ctx context.Context, userID int64, now time.Time) ([]service.UserBalanceCard, error) {
	tx, err := beginBalanceCardTx(ctx, r.db)
	if err != nil {
		return nil, err
	}
	defer rollbackBalanceCardTx(tx)
	if err := normalizeUserBalanceCardsTx(ctx, tx, userID, now); err != nil {
		return nil, err
	}
	query := `SELECT ` + balanceCardColumns + ` FROM user_balance_cards c
		JOIN users u ON u.id=c.user_id WHERE c.user_id=$2 AND c.deleted_at IS NULL
		ORDER BY CASE c.status WHEN 'active' THEN 0 WHEN 'pending' THEN 1 ELSE 2 END,
		c.starts_at ASC, c.id ASC`
	rows, err := tx.QueryContext(ctx, query, now, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]service.UserBalanceCard, 0)
	for rows.Next() {
		card, err := scanBalanceCard(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *card)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return result, nil
}

func (r *balanceCardRepository) ListCards(ctx context.Context, params pagination.PaginationParams, filter service.BalanceCardListFilter, now time.Time) ([]service.UserBalanceCard, *pagination.PaginationResult, error) {
	countWhere := []string{"c.deleted_at IS NULL"}
	listWhere := []string{"c.deleted_at IS NULL"}
	countArgs := make([]any, 0, 3)
	listArgs := []any{now}
	appendFilter := func(expression string, value any) {
		countArgs = append(countArgs, value)
		listArgs = append(listArgs, value)
		countWhere = append(countWhere, fmt.Sprintf(expression, len(countArgs)))
		listWhere = append(listWhere, fmt.Sprintf(expression, len(listArgs)))
	}
	if filter.UserID != nil {
		appendFilter("c.user_id=$%d", *filter.UserID)
	}
	if filter.PlanID != nil {
		appendFilter("c.plan_id=$%d", *filter.PlanID)
	}
	if status := strings.TrimSpace(filter.Status); status != "" {
		appendFilter("(CASE WHEN c.status IN ('active','pending') AND c.expires_at <= NOW() THEN 'expired' ELSE c.status END)=$%d", status)
	}
	countWhereSQL := strings.Join(countWhere, " AND ")
	listWhereSQL := strings.Join(listWhere, " AND ")
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_balance_cards c WHERE `+countWhereSQL, countArgs...).Scan(&total); err != nil {
		return nil, nil, err
	}
	listArgs = append(listArgs, params.Limit(), params.Offset())
	query := `SELECT ` + balanceCardColumns + ` FROM user_balance_cards c
		JOIN users u ON u.id=c.user_id WHERE ` + listWhereSQL +
		fmt.Sprintf(` ORDER BY c.created_at DESC, c.id DESC LIMIT $%d OFFSET $%d`, len(listArgs)-1, len(listArgs))
	rows, err := r.db.QueryContext(ctx, query, listArgs...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	result := make([]service.UserBalanceCard, 0)
	for rows.Next() {
		card, err := scanBalanceCard(rows)
		if err != nil {
			return nil, nil, err
		}
		result = append(result, *card)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	limit := params.Limit()
	pages := int(math.Ceil(float64(total) / float64(limit)))
	return result, &pagination.PaginationResult{Total: total, Page: max(params.Page, 1), PageSize: limit, Pages: pages}, nil
}

func (r *balanceCardRepository) GetCard(ctx context.Context, id int64, now time.Time) (*service.UserBalanceCard, error) {
	query := `SELECT ` + balanceCardColumns + ` FROM user_balance_cards c
		JOIN users u ON u.id=c.user_id WHERE c.id=$2 AND c.deleted_at IS NULL`
	card, err := scanBalanceCard(r.db.QueryRowContext(ctx, query, now, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrBalanceCardNotFound
	}
	return card, err
}

func (r *balanceCardRepository) GetWalletSnapshot(ctx context.Context, userID int64, now time.Time) (_ *service.BalanceCardWalletSnapshot, err error) {
	tx, err := beginBalanceCardTx(ctx, r.db)
	if err != nil {
		return nil, err
	}
	defer rollbackBalanceCardTx(tx)
	if err := normalizeUserBalanceCardsTx(ctx, tx, userID, now); err != nil {
		return nil, err
	}
	var s service.BalanceCardWalletSnapshot
	err = tx.QueryRowContext(ctx, `SELECT id, user_id, plan_name, card_type, validity_days, expires_at,
		daily_window_start, daily_quota_usd::double precision, daily_usage_usd::double precision,
		weekly_quota_usd::double precision, weekly_window_start,
		weekly_usage_usd::double precision, monthly_quota_usd::double precision,
		monthly_usage_usd::double precision,
		fallback_enabled, auto_reset_enabled, reset_count, max_reset_count
		FROM user_balance_cards WHERE user_id=$1 AND status='active'
		AND starts_at <= $2 AND expires_at > $2 LIMIT 1`, userID, now).Scan(
		&s.CardID, &s.UserID, &s.PlanName, &s.CardType, &s.ValidityDays, &s.ExpiresAt,
		&s.DailyWindowStart, &s.DailyQuotaUSD, &s.DailyUsageUSD,
		&s.WeeklyQuotaUSD, &s.WeeklyWindowStart, &s.WeeklyUsageUSD,
		&s.MonthlyQuotaUSD, &s.MonthlyUsageUSD, &s.FallbackEnabled,
		&s.AutoResetEnabled, &s.ResetCount, &s.MaxResetCount)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		tx = nil
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return &s, nil
}

func (r *balanceCardRepository) UpdatePreferences(ctx context.Context, id, userID int64, input service.BalanceCardPreferencesInput, actorID int64, now time.Time) (_ *service.UserBalanceCard, err error) {
	tx, err := beginBalanceCardTx(ctx, r.db)
	if err != nil {
		return nil, err
	}
	defer rollbackBalanceCardTx(tx)
	query := `SELECT user_id FROM user_balance_cards WHERE id=$1`
	if userID > 0 {
		query += ` AND user_id=$2`
	}
	var ownerID int64
	if userID > 0 {
		err = tx.QueryRowContext(ctx, query, id, userID).Scan(&ownerID)
	} else {
		err = tx.QueryRowContext(ctx, query, id).Scan(&ownerID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrBalanceCardNotFound
	}
	if err != nil {
		return nil, err
	}
	sets := []string{"updated_at=$1"}
	args := []any{now}
	if input.FallbackEnabled != nil {
		args = append(args, *input.FallbackEnabled)
		sets = append(sets, fmt.Sprintf("fallback_enabled=$%d", len(args)))
	}
	if input.AutoResetEnabled != nil {
		args = append(args, *input.AutoResetEnabled)
		sets = append(sets, fmt.Sprintf("auto_reset_enabled=$%d", len(args)))
	}
	args = append(args, id)
	if _, err := tx.ExecContext(ctx, `UPDATE user_balance_cards SET `+strings.Join(sets, ",")+
		fmt.Sprintf(" WHERE id=$%d", len(args)), args...); err != nil {
		return nil, err
	}
	meta := map[string]any{"fallback_enabled": input.FallbackEnabled, "auto_reset_enabled": input.AutoResetEnabled}
	if err := insertBalanceCardLedgerTx(ctx, tx, id, ownerID, "preference_update", 0, 0, 0, nil, nil, nil, nil, nil, nilInt64Ptr(actorID), "", meta); err != nil {
		return nil, err
	}
	card, err := getBalanceCardTx(ctx, tx, id, now)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return card, nil
}

func (r *balanceCardRepository) ResetDaily(ctx context.Context, id, userID, actorID int64, operationKey string, now time.Time) (_ *service.UserBalanceCard, err error) {
	tx, err := beginBalanceCardTx(ctx, r.db)
	if err != nil {
		return nil, err
	}
	defer rollbackBalanceCardTx(tx)
	var ownerID int64
	ownerQuery := `SELECT user_id FROM user_balance_cards WHERE id=$1`
	if userID > 0 {
		err = tx.QueryRowContext(ctx, ownerQuery+` AND user_id=$2`, id, userID).Scan(&ownerID)
	} else {
		err = tx.QueryRowContext(ctx, ownerQuery, id).Scan(&ownerID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrBalanceCardNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := normalizeUserBalanceCardsTx(ctx, tx, ownerID, now); err != nil {
		return nil, err
	}
	if operationKey != "" {
		var existing int64
		err := tx.QueryRowContext(ctx, `SELECT user_balance_card_id FROM balance_card_ledgers
			WHERE operation_key=$1`, operationKey).Scan(&existing)
		if err == nil {
			if existing != id {
				return nil, service.ErrBalanceCardOperationConflict
			}
			card, err := getBalanceCardTx(ctx, tx, id, now)
			if err != nil {
				return nil, err
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			tx = nil
			return card, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	var status, cardType string
	var usage, quota, weeklyQuota, weeklyUsage, monthlyQuota, monthlyUsage float64
	var weeklyWindowStart *time.Time
	var resetCount, maxReset int
	var expiresAt time.Time
	err = tx.QueryRowContext(ctx, `SELECT status, card_type, daily_usage_usd::double precision,
		daily_quota_usd::double precision, weekly_quota_usd::double precision,
		weekly_usage_usd::double precision, weekly_window_start,
		monthly_quota_usd::double precision, monthly_usage_usd::double precision,
		reset_count, max_reset_count, expires_at
		FROM user_balance_cards WHERE id=$1 FOR UPDATE`, id).
		Scan(&status, &cardType, &usage, &quota, &weeklyQuota, &weeklyUsage,
			&weeklyWindowStart, &monthlyQuota, &monthlyUsage, &resetCount, &maxReset, &expiresAt)
	if err != nil {
		return nil, err
	}
	if status != service.BalanceCardStatusActive || !expiresAt.After(now) {
		return nil, service.ErrBalanceCardResetUnavailable
	}
	today := timezone.StartOfDay(now)
	resetState := &service.UserBalanceCard{
		Status: status, ExpiresAt: expiresAt, ResetCount: resetCount, MaxResetCount: maxReset,
		CardType: cardType, DailyQuotaUSD: quota, DailyUsageUSD: usage, DailyWindowStart: &today,
		WeeklyQuotaUSD: weeklyQuota, WeeklyWindowStart: weeklyWindowStart,
		WeeklyUsageUSD: weeklyUsage, MonthlyQuotaUSD: monthlyQuota,
		MonthlyUsageUSD: monthlyUsage,
	}
	resetWindow := resetState.PendingResetWindow(now)
	if resetWindow == "" {
		return nil, service.ErrBalanceCardResetUnavailable
	}
	if resetCount >= maxReset {
		return nil, service.ErrBalanceCardResetLimitExceeded
	}
	resetDuration := service.BalanceCardResetDuration(resetWindow, weeklyWindowStart, now)
	newExpiresAt := expiresAt.Add(-resetDuration)
	if resetDuration <= 0 || !newExpiresAt.After(now) {
		return nil, service.ErrBalanceCardInsufficientTerm
	}
	metadata := map[string]any{"window": resetWindow, "weekly_usage": weeklyUsage, "monthly_usage": monthlyUsage}
	if resetWindow == service.BalanceCardResetWindowWeekly {
		weekStart := now
		if _, err := tx.ExecContext(ctx, `UPDATE user_balance_cards SET
			daily_usage_usd=0, daily_window_start=$2,
			weekly_usage_usd=0, weekly_window_start=$3,
			expires_at=$4, reset_count=reset_count+1, updated_at=$5 WHERE id=$1`,
			id, today, weekStart, newExpiresAt, now); err != nil {
			return nil, err
		}
		metadata["weekly_usage_before"] = weeklyUsage
		metadata["weekly_usage_after"] = 0
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE user_balance_cards SET
			daily_usage_usd=0, daily_window_start=$2,
			expires_at=$3, reset_count=reset_count+1, updated_at=$4 WHERE id=$1`,
			id, today, newExpiresAt, now); err != nil {
			return nil, err
		}
	}
	if err := shiftPendingBalanceCardsTx(ctx, tx, ownerID, id, expiresAt, -resetDuration, now); err != nil {
		return nil, err
	}
	if err := insertBalanceCardLedgerTx(ctx, tx, id, ownerID, "manual_reset", 0, usage, 0,
		&expiresAt, &newExpiresAt, nil, nil, balanceCardStringPtr(operationKey), nilInt64Ptr(actorID), "", metadata); err != nil {
		return nil, err
	}
	card, err := getBalanceCardTx(ctx, tx, id, now)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return card, nil
}

func balanceCardStringPtr(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	v = strings.TrimSpace(v)
	return &v
}

func shiftPendingBalanceCardsTx(ctx context.Context, tx *sql.Tx, userID, excludeID int64, from time.Time, shift time.Duration, now time.Time) error {
	if shift == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE user_balance_cards SET
		starts_at=starts_at + ($1 * INTERVAL '1 second'),
		expires_at=expires_at + ($1 * INTERVAL '1 second'), updated_at=$4
		WHERE user_id=$2 AND status='pending' AND id<>$3 AND starts_at >= $5`,
		shift.Seconds(), userID, excludeID, now, from)
	return err
}

func (r *balanceCardRepository) ExtendCard(ctx context.Context, id int64, days int, actorID int64, now time.Time) (_ *service.UserBalanceCard, err error) {
	tx, err := beginBalanceCardTx(ctx, r.db)
	if err != nil {
		return nil, err
	}
	defer rollbackBalanceCardTx(tx)
	var ownerID int64
	err = tx.QueryRowContext(ctx, `SELECT user_id FROM user_balance_cards WHERE id=$1`, id).Scan(&ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrBalanceCardNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := normalizeUserBalanceCardsTx(ctx, tx, ownerID, now); err != nil {
		return nil, err
	}
	var startsAt, expiresAt time.Time
	var status string
	err = tx.QueryRowContext(ctx, `SELECT starts_at, expires_at, status
		FROM user_balance_cards WHERE id=$1 FOR UPDATE`, id).Scan(&startsAt, &expiresAt, &status)
	if err != nil {
		return nil, err
	}
	if status == service.BalanceCardStatusRevoked || status == service.BalanceCardStatusExpired {
		return nil, service.ErrBalanceCardOperationConflict
	}
	newExpiresAt := expiresAt.Add(time.Duration(days) * 24 * time.Hour)
	if !newExpiresAt.After(startsAt) || !newExpiresAt.After(now) {
		return nil, service.ErrBalanceCardInsufficientTerm
	}
	if _, err := tx.ExecContext(ctx, `UPDATE user_balance_cards SET expires_at=$2, updated_at=$3 WHERE id=$1`, id, newExpiresAt, now); err != nil {
		return nil, err
	}
	if err := shiftPendingBalanceCardsTx(ctx, tx, ownerID, id, expiresAt, time.Duration(days)*24*time.Hour, now); err != nil {
		return nil, err
	}
	if err := insertBalanceCardLedgerTx(ctx, tx, id, ownerID, "extend", 0, 0, 0,
		&expiresAt, &newExpiresAt, nil, nil, nil, nilInt64Ptr(actorID), "", map[string]any{"days": days}); err != nil {
		return nil, err
	}
	card, err := getBalanceCardTx(ctx, tx, id, now)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return card, nil
}

func (r *balanceCardRepository) RevokeCard(ctx context.Context, id, actorID int64, now time.Time) (_ *service.UserBalanceCard, err error) {
	tx, err := beginBalanceCardTx(ctx, r.db)
	if err != nil {
		return nil, err
	}
	defer rollbackBalanceCardTx(tx)
	var ownerID int64
	err = tx.QueryRowContext(ctx, `SELECT user_id FROM user_balance_cards WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrBalanceCardNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := normalizeUserBalanceCardsTx(ctx, tx, ownerID, now); err != nil {
		return nil, err
	}
	var startsAt, expiresAt time.Time
	var status string
	err = tx.QueryRowContext(ctx, `SELECT starts_at, expires_at, status
		FROM user_balance_cards WHERE id=$1 FOR UPDATE`, id).Scan(&startsAt, &expiresAt, &status)
	if err != nil {
		return nil, err
	}
	if status == service.BalanceCardStatusRevoked {
		return nil, service.ErrBalanceCardOperationConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE user_balance_cards SET status='revoked', updated_at=$2 WHERE id=$1`, id, now); err != nil {
		return nil, err
	}
	var shift time.Duration
	cutoff := expiresAt
	if status == service.BalanceCardStatusActive && expiresAt.After(now) {
		shift = now.Sub(expiresAt)
	} else if status == service.BalanceCardStatusPending {
		shift = startsAt.Sub(expiresAt)
	}
	if shift != 0 {
		if err := shiftPendingBalanceCardsTx(ctx, tx, ownerID, id, cutoff, shift, now); err != nil {
			return nil, err
		}
	}
	if err := insertBalanceCardLedgerTx(ctx, tx, id, ownerID, "revoke", 0, 0, 0,
		&expiresAt, &expiresAt, nil, nil, nil, nilInt64Ptr(actorID), "", nil); err != nil {
		return nil, err
	}
	if err := normalizeUserBalanceCardsTx(ctx, tx, ownerID, now); err != nil {
		return nil, err
	}
	card, err := getBalanceCardTx(ctx, tx, id, now)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return card, nil
}

func (r *balanceCardRepository) DeleteCard(ctx context.Context, id, actorID int64, now time.Time) (_ int64, err error) {
	tx, err := beginBalanceCardTx(ctx, r.db)
	if err != nil {
		return 0, err
	}
	defer rollbackBalanceCardTx(tx)

	var ownerID int64
	var status string
	err = tx.QueryRowContext(ctx, `SELECT user_id, status FROM user_balance_cards
		WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&ownerID, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, service.ErrBalanceCardNotFound
	}
	if err != nil {
		return 0, err
	}
	if status != service.BalanceCardStatusRevoked {
		return 0, service.ErrBalanceCardOperationConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE user_balance_cards
		SET deleted_at=$2, deleted_by=NULLIF($3,0), updated_at=$2
		WHERE id=$1`, id, now, actorID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	tx = nil
	return ownerID, nil
}

func insertBalanceCardLedgerTx(
	ctx context.Context,
	tx *sql.Tx,
	cardID, userID int64,
	eventType string,
	amount, usageBefore, usageAfter float64,
	expiresBefore, expiresAfter *time.Time,
	requestID *string,
	apiKeyID *int64,
	operationKey *string,
	actorID *int64,
	notes string,
	metadata map[string]any,
) error {
	if metadata == nil {
		metadata = map[string]any{}
	}
	metaJSON, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO balance_card_ledgers
		(user_balance_card_id, user_id, event_type, amount_usd, daily_usage_before,
		 daily_usage_after, expires_at_before, expires_at_after, request_id, api_key_id,
		 operation_key, actor_id, notes, metadata)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		cardID, userID, eventType, amount, usageBefore, usageAfter, expiresBefore, expiresAfter,
		requestID, apiKeyID, operationKey, actorID, strings.TrimSpace(notes), metaJSON)
	return err
}

func (r *balanceCardRepository) ListLedger(ctx context.Context, cardID int64, params pagination.PaginationParams) ([]service.BalanceCardLedger, *pagination.PaginationResult, error) {
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM balance_card_ledgers WHERE user_balance_card_id=$1`, cardID).Scan(&total); err != nil {
		return nil, nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, user_balance_card_id, user_id, event_type,
		amount_usd::double precision, daily_usage_before::double precision,
		daily_usage_after::double precision, expires_at_before, expires_at_after,
		request_id, api_key_id, operation_key, actor_id, notes, metadata, created_at
		FROM balance_card_ledgers WHERE user_balance_card_id=$1
		ORDER BY created_at DESC, id DESC LIMIT $2 OFFSET $3`, cardID, params.Limit(), params.Offset())
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	result := make([]service.BalanceCardLedger, 0)
	for rows.Next() {
		var item service.BalanceCardLedger
		var metaJSON []byte
		if err := rows.Scan(&item.ID, &item.UserBalanceCardID, &item.UserID, &item.EventType,
			&item.AmountUSD, &item.DailyUsageBefore, &item.DailyUsageAfter,
			&item.ExpiresAtBefore, &item.ExpiresAtAfter, &item.RequestID, &item.APIKeyID,
			&item.OperationKey, &item.ActorID, &item.Notes, &metaJSON, &item.CreatedAt); err != nil {
			return nil, nil, err
		}
		_ = json.Unmarshal(metaJSON, &item.Metadata)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	limit := params.Limit()
	pages := int(math.Ceil(float64(total) / float64(limit)))
	return result, &pagination.PaginationResult{Total: total, Page: max(params.Page, 1), PageSize: limit, Pages: pages}, nil
}
