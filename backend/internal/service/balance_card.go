package service

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

const (
	BalanceCardTypeDay            = "day"
	BalanceCardTypeWeek           = "week"
	BalanceCardTypeMonth          = "month"
	BalanceCardTypeCustom         = "custom"
	BalanceCardPlanStatusInactive = "inactive"

	BalanceCardStatusPending   = "pending"
	BalanceCardStatusActive    = "active"
	BalanceCardStatusExpired   = "expired"
	BalanceCardStatusSuspended = "suspended"
	BalanceCardStatusRevoked   = "revoked"

	BalanceCardResetWindowDaily  = "daily"
	BalanceCardResetWindowWeekly = "weekly"
)

var (
	ErrBalanceCardPlanNotFound         = infraerrors.NotFound("BALANCE_CARD_PLAN_NOT_FOUND", "balance card plan not found")
	ErrBalanceCardNotFound             = infraerrors.NotFound("BALANCE_CARD_NOT_FOUND", "balance card not found")
	ErrBalanceCardPlanInactive         = infraerrors.BadRequest("BALANCE_CARD_PLAN_INACTIVE", "balance card plan is inactive")
	ErrBalanceCardInvalidInput         = infraerrors.BadRequest("BALANCE_CARD_INVALID_INPUT", "invalid balance card input")
	ErrBalanceCardDailyLimitExceeded   = infraerrors.TooManyRequests("BALANCE_CARD_DAILY_LIMIT_EXCEEDED", "balance card daily quota exhausted")
	ErrBalanceCardWeeklyLimitExceeded  = infraerrors.TooManyRequests("BALANCE_CARD_WEEKLY_LIMIT_EXCEEDED", "balance card weekly quota exhausted")
	ErrBalanceCardMonthlyLimitExceeded = infraerrors.TooManyRequests("BALANCE_CARD_MONTHLY_LIMIT_EXCEEDED", "balance card monthly quota exhausted")
	ErrBalanceCardResetUnavailable     = infraerrors.Conflict("BALANCE_CARD_RESET_UNAVAILABLE", "balance card quota cannot be reset")
	ErrBalanceCardResetLimitExceeded   = infraerrors.Conflict("BALANCE_CARD_RESET_LIMIT_EXCEEDED", "balance card reset limit exceeded")
	ErrBalanceCardInsufficientTerm     = infraerrors.Conflict("BALANCE_CARD_INSUFFICIENT_TERM", "balance card has less than one day remaining")
	ErrBalanceCardOperationConflict    = infraerrors.Conflict("BALANCE_CARD_OPERATION_CONFLICT", "balance card operation conflicts with current state")
)

type BalanceCardPlan struct {
	ID               int64     `json:"id"`
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	CardType         string    `json:"card_type"`
	ValidityDays     int       `json:"validity_days"`
	DailyQuotaUSD    float64   `json:"daily_quota_usd"`
	WeeklyQuotaUSD   float64   `json:"weekly_quota_usd"`
	MonthlyQuotaUSD  float64   `json:"monthly_quota_usd"`
	FallbackDefault  bool      `json:"fallback_default"`
	AutoResetDefault bool      `json:"auto_reset_default"`
	MaxResetCount    int       `json:"max_reset_count"`
	Status           string    `json:"status"`
	SortOrder        int       `json:"sort_order"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type UserBalanceCard struct {
	ID                        int64      `json:"id"`
	UserID                    int64      `json:"user_id"`
	UserEmail                 string     `json:"user_email,omitempty"`
	PlanID                    *int64     `json:"plan_id"`
	PlanName                  string     `json:"plan_name"`
	CardType                  string     `json:"card_type"`
	ValidityDays              int        `json:"validity_days"`
	DailyQuotaUSD             float64    `json:"daily_quota_usd"`
	WeeklyQuotaUSD            float64    `json:"weekly_quota_usd"`
	MonthlyQuotaUSD           float64    `json:"monthly_quota_usd"`
	MaxResetCount             int        `json:"max_reset_count"`
	StartsAt                  time.Time  `json:"starts_at"`
	ExpiresAt                 time.Time  `json:"expires_at"`
	Status                    string     `json:"status"`
	DailyWindowStart          *time.Time `json:"daily_window_start"`
	DailyUsageUSD             float64    `json:"daily_usage_usd"`
	WeeklyWindowStart         *time.Time `json:"weekly_window_start"`
	WeeklyDailyAdvanceSeconds int64      `json:"weekly_daily_advance_seconds"`
	WeeklyUsageUSD            float64    `json:"weekly_usage_usd"`
	MonthlyUsageUSD           float64    `json:"monthly_usage_usd"`
	FallbackEnabled           bool       `json:"fallback_enabled"`
	AutoResetEnabled          bool       `json:"auto_reset_enabled"`
	ResetCount                int        `json:"reset_count"`
	AssignedBy                *int64     `json:"assigned_by"`
	AssignedAt                time.Time  `json:"assigned_at"`
	ActivatedAt               *time.Time `json:"activated_at"`
	Notes                     string     `json:"notes"`
	CreatedAt                 time.Time  `json:"created_at"`
	UpdatedAt                 time.Time  `json:"updated_at"`
}

func (c *UserBalanceCard) EffectiveDailyUsage(now time.Time) float64 {
	if c == nil || c.DailyWindowStart == nil {
		return 0
	}
	if c.ValidityDays <= 1 {
		return c.DailyUsageUSD
	}
	if timezone.StartOfDay(*c.DailyWindowStart).Before(timezone.StartOfDay(now)) {
		return 0
	}
	return c.DailyUsageUSD
}

func (c *UserBalanceCard) DailyRemaining(now time.Time) float64 {
	if c == nil {
		return 0
	}
	return balanceCardLimitRemaining(c.DailyQuotaUSD, c.EffectiveDailyUsage(now))
}

func effectiveBalanceCardWeeklyUsage(cardType string, quota, usage float64, windowStart *time.Time, now time.Time) float64 {
	if quota <= 0 {
		return 0
	}
	// A week card's weekly allowance is its whole-card total and never rolls
	// over, even when an administrator extends its validity.
	if cardType == BalanceCardTypeWeek {
		return usage
	}
	// Month cards follow the subscription system's activation-anchored rolling
	// seven-day window instead of resetting on the calendar week boundary.
	if windowStart == nil || !now.Before(windowStart.Add(7*24*time.Hour)) {
		return 0
	}
	return usage
}

func balanceCardLimitRemaining(limit, usage float64) float64 {
	if limit <= 0 {
		return math.Inf(1)
	}
	return math.Max(0, limit-usage)
}

func balanceCardAvailableRemaining(daily, weekly, monthly float64) float64 {
	return math.Max(0, math.Min(daily, math.Min(weekly, monthly)))
}

func (c *UserBalanceCard) EffectiveWeeklyUsage(now time.Time) float64 {
	if c == nil {
		return 0
	}
	return effectiveBalanceCardWeeklyUsage(c.CardType, c.WeeklyQuotaUSD, c.WeeklyUsageUSD, c.WeeklyWindowStart, now)
}

func (c *UserBalanceCard) WeeklyRemaining(now time.Time) float64 {
	if c == nil {
		return 0
	}
	return balanceCardLimitRemaining(c.WeeklyQuotaUSD, c.EffectiveWeeklyUsage(now))
}

func (c *UserBalanceCard) MonthlyRemaining() float64 {
	if c == nil {
		return 0
	}
	return balanceCardLimitRemaining(c.MonthlyQuotaUSD, c.MonthlyUsageUSD)
}

func (c *UserBalanceCard) AvailableRemaining(now time.Time) float64 {
	if c == nil {
		return 0
	}
	return balanceCardAvailableRemaining(c.DailyRemaining(now), c.WeeklyRemaining(now), c.MonthlyRemaining())
}

func balanceCardResetWindow(cardType string, dailyQuota, dailyRemaining, weeklyQuota, weeklyRemaining, monthlyRemaining float64) string {
	// A month card may advance its activation-anchored seven-day window when
	// that allowance is exhausted. Advancing a week also starts a fresh daily
	// window, just as seven naturally elapsed days would.
	if cardType == BalanceCardTypeMonth && weeklyQuota > 0 && weeklyRemaining <= 1e-8 {
		remaining := balanceCardAvailableRemaining(
			balanceCardLimitRemaining(dailyQuota, 0),
			balanceCardLimitRemaining(weeklyQuota, 0),
			monthlyRemaining,
		)
		if remaining > 1e-8 {
			return BalanceCardResetWindowWeekly
		}
	}
	if dailyQuota > 0 && dailyRemaining <= 1e-8 {
		remaining := balanceCardAvailableRemaining(
			balanceCardLimitRemaining(dailyQuota, 0),
			weeklyRemaining,
			monthlyRemaining,
		)
		if remaining > 1e-8 {
			return BalanceCardResetWindowDaily
		}
	}
	return ""
}

func BalanceCardResetDuration(window string, weeklyWindowStart *time.Time, now time.Time, dailyAdvanceSeconds int64) time.Duration {
	if window == BalanceCardResetWindowWeekly {
		if weeklyWindowStart == nil {
			return 7 * 24 * time.Hour
		}
		// Daily advance resets already paid for part of this week. Do not
		// charge that same validity again when advancing the weekly allowance.
		remaining := weeklyWindowStart.Add(7*24*time.Hour).Sub(now) - time.Duration(dailyAdvanceSeconds)*time.Second
		if remaining > 0 {
			return remaining
		}
		return 0
	}
	if window == BalanceCardResetWindowDaily {
		return 24 * time.Hour
	}
	return 0
}

// PendingResetWindow returns the exhausted allowance that an advance reset
// would restore. It intentionally does not check reset count or remaining
// validity so callers can return the most useful validation error.
func (c *UserBalanceCard) PendingResetWindow(now time.Time) string {
	if c == nil || c.Status != BalanceCardStatusActive {
		return ""
	}
	return balanceCardResetWindow(
		c.CardType,
		c.DailyQuotaUSD,
		c.DailyRemaining(now),
		c.WeeklyQuotaUSD,
		c.WeeklyRemaining(now),
		c.MonthlyRemaining(),
	)
}

func (c *UserBalanceCard) AdvanceResetWindow(now time.Time) string {
	window := c.PendingResetWindow(now)
	if window == "" || c.ResetCount >= c.MaxResetCount {
		return ""
	}
	return resetWindowWithSufficientTerm(window, c.WeeklyWindowStart, c.ExpiresAt, now, c.WeeklyDailyAdvanceSeconds)
}

func (c *UserBalanceCard) CanAdvanceReset(now time.Time) bool {
	return c != nil && c.AdvanceResetWindow(now) != ""
}

type BalanceCardLedger struct {
	ID                int64          `json:"id"`
	UserBalanceCardID int64          `json:"user_balance_card_id"`
	UserID            int64          `json:"user_id"`
	EventType         string         `json:"event_type"`
	AmountUSD         float64        `json:"amount_usd"`
	DailyUsageBefore  float64        `json:"daily_usage_before"`
	DailyUsageAfter   float64        `json:"daily_usage_after"`
	ExpiresAtBefore   *time.Time     `json:"expires_at_before"`
	ExpiresAtAfter    *time.Time     `json:"expires_at_after"`
	RequestID         *string        `json:"request_id,omitempty"`
	APIKeyID          *int64         `json:"api_key_id,omitempty"`
	OperationKey      *string        `json:"operation_key,omitempty"`
	ActorID           *int64         `json:"actor_id,omitempty"`
	Notes             string         `json:"notes"`
	Metadata          map[string]any `json:"metadata"`
	CreatedAt         time.Time      `json:"created_at"`
}

type BalanceCardWalletSnapshot struct {
	CardID                    int64      `json:"card_id"`
	UserID                    int64      `json:"user_id"`
	PlanName                  string     `json:"plan_name"`
	CardType                  string     `json:"card_type"`
	ValidityDays              int        `json:"validity_days"`
	ExpiresAt                 time.Time  `json:"expires_at"`
	DailyWindowStart          *time.Time `json:"daily_window_start"`
	DailyQuotaUSD             float64    `json:"daily_quota_usd"`
	DailyUsageUSD             float64    `json:"daily_usage_usd"`
	WeeklyQuotaUSD            float64    `json:"weekly_quota_usd"`
	WeeklyWindowStart         *time.Time `json:"weekly_window_start"`
	WeeklyDailyAdvanceSeconds int64      `json:"weekly_daily_advance_seconds"`
	WeeklyUsageUSD            float64    `json:"weekly_usage_usd"`
	MonthlyQuotaUSD           float64    `json:"monthly_quota_usd"`
	MonthlyUsageUSD           float64    `json:"monthly_usage_usd"`
	FallbackEnabled           bool       `json:"fallback_enabled"`
	AutoResetEnabled          bool       `json:"auto_reset_enabled"`
	ResetCount                int        `json:"reset_count"`
	MaxResetCount             int        `json:"max_reset_count"`
}

func (s *BalanceCardWalletSnapshot) EffectiveDailyUsage(now time.Time) float64 {
	if s == nil || s.DailyWindowStart == nil {
		return 0
	}
	if s.ValidityDays <= 1 {
		return s.DailyUsageUSD
	}
	if timezone.StartOfDay(*s.DailyWindowStart).Before(timezone.StartOfDay(now)) {
		return 0
	}
	return s.DailyUsageUSD
}

func (s *BalanceCardWalletSnapshot) DailyRemaining(now time.Time) float64 {
	if s == nil {
		return 0
	}
	return balanceCardLimitRemaining(s.DailyQuotaUSD, s.EffectiveDailyUsage(now))
}

func (s *BalanceCardWalletSnapshot) EffectiveWeeklyUsage(now time.Time) float64 {
	if s == nil {
		return 0
	}
	return effectiveBalanceCardWeeklyUsage(s.CardType, s.WeeklyQuotaUSD, s.WeeklyUsageUSD, s.WeeklyWindowStart, now)
}

func (s *BalanceCardWalletSnapshot) WeeklyRemaining(now time.Time) float64 {
	if s == nil {
		return 0
	}
	return balanceCardLimitRemaining(s.WeeklyQuotaUSD, s.EffectiveWeeklyUsage(now))
}

func (s *BalanceCardWalletSnapshot) MonthlyRemaining() float64 {
	if s == nil {
		return 0
	}
	return balanceCardLimitRemaining(s.MonthlyQuotaUSD, s.MonthlyUsageUSD)
}

func (s *BalanceCardWalletSnapshot) AvailableRemaining(now time.Time) float64 {
	if s == nil {
		return 0
	}
	return balanceCardAvailableRemaining(s.DailyRemaining(now), s.WeeklyRemaining(now), s.MonthlyRemaining())
}

func resetWindowWithSufficientTerm(window string, weeklyWindowStart *time.Time, expiresAt, now time.Time, dailyAdvanceSeconds int64) string {
	duration := BalanceCardResetDuration(window, weeklyWindowStart, now, dailyAdvanceSeconds)
	if window == "" || !expiresAt.After(now.Add(duration)) {
		return ""
	}
	return window
}

func (s *BalanceCardWalletSnapshot) PendingResetWindow(now time.Time) string {
	if s == nil {
		return ""
	}
	return balanceCardResetWindow(
		s.CardType,
		s.DailyQuotaUSD,
		s.DailyRemaining(now),
		s.WeeklyQuotaUSD,
		s.WeeklyRemaining(now),
		s.MonthlyRemaining(),
	)
}

func (s *BalanceCardWalletSnapshot) AdvanceResetWindow(now time.Time) string {
	if s == nil || s.ResetCount >= s.MaxResetCount {
		return ""
	}
	return resetWindowWithSufficientTerm(s.PendingResetWindow(now), s.WeeklyWindowStart, s.ExpiresAt, now, s.WeeklyDailyAdvanceSeconds)
}

func (s *BalanceCardWalletSnapshot) LimitError(now time.Time) error {
	if s == nil {
		return ErrBalanceCardDailyLimitExceeded
	}
	if s.WeeklyQuotaUSD > 0 && s.WeeklyRemaining(now) <= 1e-8 {
		return ErrBalanceCardWeeklyLimitExceeded
	}
	if s.MonthlyQuotaUSD > 0 && s.MonthlyRemaining() <= 1e-8 {
		return ErrBalanceCardMonthlyLimitExceeded
	}
	if s.DailyRemaining(now) <= 1e-8 {
		return ErrBalanceCardDailyLimitExceeded
	}
	return ErrBalanceCardDailyLimitExceeded
}

func (s *BalanceCardWalletSnapshot) CanAdvanceReset(now time.Time) bool {
	return s != nil && s.AdvanceResetWindow(now) != ""
}

type CreateBalanceCardPlanInput struct {
	Name             string  `json:"name"`
	Description      string  `json:"description"`
	CardType         string  `json:"card_type"`
	ValidityDays     int     `json:"validity_days"`
	DailyQuotaUSD    float64 `json:"daily_quota_usd"`
	WeeklyQuotaUSD   float64 `json:"weekly_quota_usd"`
	MonthlyQuotaUSD  float64 `json:"monthly_quota_usd"`
	FallbackDefault  bool    `json:"fallback_default"`
	AutoResetDefault bool    `json:"auto_reset_default"`
	MaxResetCount    int     `json:"max_reset_count"`
	Status           string  `json:"status"`
	SortOrder        int     `json:"sort_order"`
}

type UpdateBalanceCardPlanInput = CreateBalanceCardPlanInput

type AssignBalanceCardInput struct {
	UserID     int64
	PlanID     int64
	AssignedBy int64
	Notes      string
}

type BalanceCardListFilter struct {
	UserID *int64
	PlanID *int64
	Status string
}

type BalanceCardPreferencesInput struct {
	FallbackEnabled  *bool `json:"fallback_enabled"`
	AutoResetEnabled *bool `json:"auto_reset_enabled"`
}

type BalanceCardRepository interface {
	ListPlans(ctx context.Context, includeInactive bool) ([]BalanceCardPlan, error)
	GetPlan(ctx context.Context, id int64) (*BalanceCardPlan, error)
	CreatePlan(ctx context.Context, input *CreateBalanceCardPlanInput) (*BalanceCardPlan, error)
	UpdatePlan(ctx context.Context, id int64, input *UpdateBalanceCardPlanInput) (*BalanceCardPlan, error)

	AssignCard(ctx context.Context, input *AssignBalanceCardInput, now time.Time) (*UserBalanceCard, error)
	AssignCards(ctx context.Context, inputs []*AssignBalanceCardInput, now time.Time) ([]UserBalanceCard, error)
	RedeemCard(ctx context.Context, redeemCodeID, userID, planID int64, code string, now time.Time) (*UserBalanceCard, error)
	ListUserCards(ctx context.Context, userID int64, now time.Time) ([]UserBalanceCard, error)
	ListCards(ctx context.Context, params pagination.PaginationParams, filter BalanceCardListFilter, now time.Time) ([]UserBalanceCard, *pagination.PaginationResult, error)
	GetCard(ctx context.Context, id int64, now time.Time) (*UserBalanceCard, error)
	GetWalletSnapshot(ctx context.Context, userID int64, now time.Time) (*BalanceCardWalletSnapshot, error)
	UpdatePreferences(ctx context.Context, id, userID int64, input BalanceCardPreferencesInput, actorID int64, now time.Time) (*UserBalanceCard, error)
	ResetDaily(ctx context.Context, id, userID, actorID int64, operationKey string, now time.Time) (*UserBalanceCard, error)
	ExtendCard(ctx context.Context, id int64, days int, actorID int64, now time.Time) (*UserBalanceCard, error)
	RevokeCard(ctx context.Context, id, actorID int64, now time.Time) (*UserBalanceCard, error)
	DeleteCard(ctx context.Context, id, actorID int64, now time.Time) (int64, error)
	ListLedger(ctx context.Context, cardID int64, params pagination.PaginationParams) ([]BalanceCardLedger, *pagination.PaginationResult, error)
}

type BalanceCardCache interface {
	Get(ctx context.Context, userID int64) (*BalanceCardWalletSnapshot, bool, error)
	Set(ctx context.Context, userID int64, snapshot *BalanceCardWalletSnapshot) error
	Invalidate(ctx context.Context, userID int64) error
}

type BalanceCardService struct {
	repo  BalanceCardRepository
	cache BalanceCardCache
}

func NewBalanceCardService(repo BalanceCardRepository, cache BalanceCardCache) *BalanceCardService {
	return &BalanceCardService{repo: repo, cache: cache}
}

func normalizeBalanceCardPlanInput(input *CreateBalanceCardPlanInput) error {
	if input == nil {
		return ErrBalanceCardInvalidInput
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.CardType = strings.ToLower(strings.TrimSpace(input.CardType))
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	if input.CardType == "" {
		input.CardType = BalanceCardTypeCustom
	}
	if input.Status == "" {
		input.Status = StatusActive
	}
	if input.MaxResetCount < 0 || input.MaxResetCount > 1000 {
		return ErrBalanceCardInvalidInput
	}
	if input.Name == "" || input.ValidityDays <= 0 || input.ValidityDays > MaxValidityDays || input.DailyQuotaUSD < 0 || input.WeeklyQuotaUSD < 0 || input.MonthlyQuotaUSD < 0 {
		return ErrBalanceCardInvalidInput
	}
	switch input.CardType {
	case BalanceCardTypeDay, BalanceCardTypeCustom:
		input.WeeklyQuotaUSD = 0
		input.MonthlyQuotaUSD = 0
	case BalanceCardTypeWeek:
		input.MonthlyQuotaUSD = 0
	case BalanceCardTypeMonth:
	default:
		return ErrBalanceCardInvalidInput
	}
	if input.Status != StatusActive && input.Status != BalanceCardPlanStatusInactive {
		return ErrBalanceCardInvalidInput
	}
	return nil
}

func (s *BalanceCardService) ListPlans(ctx context.Context, includeInactive bool) ([]BalanceCardPlan, error) {
	return s.repo.ListPlans(ctx, includeInactive)
}

func (s *BalanceCardService) GetPlan(ctx context.Context, id int64) (*BalanceCardPlan, error) {
	if id <= 0 {
		return nil, ErrBalanceCardInvalidInput
	}
	return s.repo.GetPlan(ctx, id)
}

func (s *BalanceCardService) CreatePlan(ctx context.Context, input *CreateBalanceCardPlanInput) (*BalanceCardPlan, error) {
	if err := normalizeBalanceCardPlanInput(input); err != nil {
		return nil, err
	}
	return s.repo.CreatePlan(ctx, input)
}

func (s *BalanceCardService) UpdatePlan(ctx context.Context, id int64, input *UpdateBalanceCardPlanInput) (*BalanceCardPlan, error) {
	if id <= 0 {
		return nil, ErrBalanceCardInvalidInput
	}
	if err := normalizeBalanceCardPlanInput(input); err != nil {
		return nil, err
	}
	return s.repo.UpdatePlan(ctx, id, input)
}

func (s *BalanceCardService) Assign(ctx context.Context, input *AssignBalanceCardInput) (*UserBalanceCard, error) {
	if input == nil || input.UserID <= 0 || input.PlanID <= 0 {
		return nil, ErrBalanceCardInvalidInput
	}
	card, err := s.repo.AssignCard(ctx, input, timezone.Now())
	if err == nil {
		s.invalidate(input.UserID)
	}
	return card, err
}

func (s *BalanceCardService) BulkAssign(ctx context.Context, userIDs []int64, planID, actorID int64, notes string) ([]UserBalanceCard, error) {
	if len(userIDs) == 0 || len(userIDs) > 1000 || planID <= 0 {
		return nil, ErrBalanceCardInvalidInput
	}
	seen := make(map[int64]struct{}, len(userIDs))
	inputs := make([]*AssignBalanceCardInput, 0, len(userIDs))
	for _, userID := range userIDs {
		if userID <= 0 {
			return nil, ErrBalanceCardInvalidInput
		}
		if _, exists := seen[userID]; exists {
			continue
		}
		seen[userID] = struct{}{}
		inputs = append(inputs, &AssignBalanceCardInput{UserID: userID, PlanID: planID, AssignedBy: actorID, Notes: notes})
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].UserID < inputs[j].UserID })
	result, err := s.repo.AssignCards(ctx, inputs, timezone.Now())
	if err != nil {
		return nil, err
	}
	for userID := range seen {
		s.invalidate(userID)
	}
	return result, nil
}

func (s *BalanceCardService) Redeem(ctx context.Context, redeemCodeID, userID, planID int64, code string) (*UserBalanceCard, error) {
	if redeemCodeID <= 0 || userID <= 0 || planID <= 0 || strings.TrimSpace(code) == "" {
		return nil, ErrBalanceCardInvalidInput
	}
	card, err := s.repo.RedeemCard(ctx, redeemCodeID, userID, planID, strings.TrimSpace(code), timezone.Now())
	if err == nil {
		s.invalidate(userID)
	}
	return card, err
}

func (s *BalanceCardService) ListMine(ctx context.Context, userID int64) ([]UserBalanceCard, error) {
	return s.repo.ListUserCards(ctx, userID, timezone.Now())
}

func (s *BalanceCardService) ListCards(ctx context.Context, page, pageSize int, filter BalanceCardListFilter) ([]UserBalanceCard, *pagination.PaginationResult, error) {
	return s.repo.ListCards(ctx, pagination.PaginationParams{Page: page, PageSize: pageSize}, filter, timezone.Now())
}

func (s *BalanceCardService) UpdateMyPreferences(ctx context.Context, id, userID int64, input BalanceCardPreferencesInput) (*UserBalanceCard, error) {
	if input.FallbackEnabled == nil && input.AutoResetEnabled == nil {
		return nil, ErrBalanceCardInvalidInput
	}
	card, err := s.repo.UpdatePreferences(ctx, id, userID, input, userID, timezone.Now())
	if err == nil {
		s.invalidate(userID)
	}
	return card, err
}

func (s *BalanceCardService) ResetMyDaily(ctx context.Context, id, userID int64, operationKey string) (*UserBalanceCard, error) {
	if strings.TrimSpace(operationKey) == "" {
		return nil, ErrBalanceCardInvalidInput
	}
	card, err := s.repo.ResetDaily(ctx, id, userID, userID, strings.TrimSpace(operationKey), timezone.Now())
	if err == nil {
		s.invalidate(userID)
	}
	return card, err
}

func (s *BalanceCardService) AdminResetDaily(ctx context.Context, id, actorID int64, operationKey string) (*UserBalanceCard, error) {
	if strings.TrimSpace(operationKey) == "" {
		return nil, ErrBalanceCardInvalidInput
	}
	card, err := s.repo.ResetDaily(ctx, id, 0, actorID, strings.TrimSpace(operationKey), timezone.Now())
	if err == nil && card != nil {
		s.invalidate(card.UserID)
	}
	return card, err
}

func (s *BalanceCardService) Extend(ctx context.Context, id int64, days int, actorID int64) (*UserBalanceCard, error) {
	if days == 0 || days < -MaxValidityDays || days > MaxValidityDays {
		return nil, ErrBalanceCardInvalidInput
	}
	card, err := s.repo.ExtendCard(ctx, id, days, actorID, timezone.Now())
	if err == nil && card != nil {
		s.invalidate(card.UserID)
	}
	return card, err
}

func (s *BalanceCardService) Revoke(ctx context.Context, id, actorID int64) (*UserBalanceCard, error) {
	card, err := s.repo.RevokeCard(ctx, id, actorID, timezone.Now())
	if err == nil && card != nil {
		s.invalidate(card.UserID)
	}
	return card, err
}

func (s *BalanceCardService) Delete(ctx context.Context, id, actorID int64) error {
	if id <= 0 {
		return ErrBalanceCardInvalidInput
	}
	userID, err := s.repo.DeleteCard(ctx, id, actorID, timezone.Now())
	if err == nil {
		s.invalidate(userID)
	}
	return err
}

func (s *BalanceCardService) ListLedger(ctx context.Context, cardID, userID int64, page, pageSize int) ([]BalanceCardLedger, *pagination.PaginationResult, error) {
	card, err := s.repo.GetCard(ctx, cardID, timezone.Now())
	if err != nil {
		return nil, nil, err
	}
	if userID > 0 && card.UserID != userID {
		return nil, nil, ErrBalanceCardNotFound
	}
	return s.repo.ListLedger(ctx, cardID, pagination.PaginationParams{Page: page, PageSize: pageSize})
}

func (s *BalanceCardService) invalidate(userID int64) {
	if s.cache == nil || userID <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.cache.Invalidate(ctx, userID)
}
