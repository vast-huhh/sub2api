package service

import (
	"math"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

func TestUserBalanceCardDailyRemainingResetsOnNextCalendarDay(t *testing.T) {
	now := timezone.Now()
	yesterday := timezone.StartOfDay(now).Add(-24 * time.Hour)
	card := &UserBalanceCard{
		ValidityDays:     30,
		DailyQuotaUSD:    10,
		DailyUsageUSD:    10,
		DailyWindowStart: &yesterday,
	}
	require.InDelta(t, 10, card.DailyRemaining(now), 0.000001)
}

func TestDayCardDoesNotGainAnotherDailyWindow(t *testing.T) {
	now := timezone.Now()
	yesterday := timezone.StartOfDay(now).Add(-24 * time.Hour)
	card := &UserBalanceCard{
		ValidityDays:     1,
		DailyQuotaUSD:    10,
		DailyUsageUSD:    10,
		DailyWindowStart: &yesterday,
	}
	require.Zero(t, card.DailyRemaining(now))
}

func TestBalanceCardAdvanceResetRequiresLimitAndFullRemainingDay(t *testing.T) {
	now := timezone.Now()
	card := &UserBalanceCard{
		Status:           BalanceCardStatusActive,
		ExpiresAt:        now.Add(48 * time.Hour),
		DailyQuotaUSD:    10,
		DailyUsageUSD:    10,
		DailyWindowStart: &now,
		ResetCount:       1,
		MaxResetCount:    2,
	}
	require.True(t, card.CanAdvanceReset(now))

	card.ResetCount = 2
	require.False(t, card.CanAdvanceReset(now))
	card.ResetCount = 0
	card.ExpiresAt = now.Add(24 * time.Hour)
	require.False(t, card.CanAdvanceReset(now))
	card.ExpiresAt = now.Add(48 * time.Hour)
	card.DailyQuotaUSD = 0
	require.False(t, card.CanAdvanceReset(now))
}

func TestMonthCardWeeklyResetAdvancesOnlyRemainingWeekTerm(t *testing.T) {
	now := timezone.Now()
	weekStart := now.Add(-5 * 24 * time.Hour)
	card := &UserBalanceCard{
		Status:            BalanceCardStatusActive,
		CardType:          BalanceCardTypeMonth,
		ExpiresAt:         now.Add(20 * 24 * time.Hour),
		DailyQuotaUSD:     60,
		DailyUsageUSD:     40,
		DailyWindowStart:  &now,
		WeeklyQuotaUSD:    300,
		WeeklyUsageUSD:    300,
		WeeklyWindowStart: &weekStart,
		MonthlyQuotaUSD:   1000,
		MonthlyUsageUSD:   300,
		MaxResetCount:     20,
	}

	require.Equal(t, BalanceCardResetWindowWeekly, card.PendingResetWindow(now))
	require.Equal(t, BalanceCardResetWindowWeekly, card.AdvanceResetWindow(now))
	require.Equal(t, 48*time.Hour, BalanceCardResetDuration(BalanceCardResetWindowWeekly, &weekStart, now))
}

func TestMonthCardWeeklyResetRestoresDailyAndWeeklyButNotMonthlyCapacity(t *testing.T) {
	now := timezone.Now()
	weekStart := now.Add(-5 * 24 * time.Hour)
	card := &UserBalanceCard{
		Status:            BalanceCardStatusActive,
		CardType:          BalanceCardTypeMonth,
		ExpiresAt:         now.Add(20 * 24 * time.Hour),
		DailyQuotaUSD:     60,
		DailyUsageUSD:     60,
		DailyWindowStart:  &now,
		WeeklyQuotaUSD:    300,
		WeeklyUsageUSD:    300,
		WeeklyWindowStart: &weekStart,
		MonthlyQuotaUSD:   1000,
		MonthlyUsageUSD:   990,
		MaxResetCount:     20,
	}

	require.Equal(t, BalanceCardResetWindowWeekly, card.AdvanceResetWindow(now))
	card.DailyUsageUSD = 0
	card.WeeklyUsageUSD = 0
	card.WeeklyWindowStart = &now
	require.InDelta(t, 10, card.AvailableRemaining(now), 0.000001)

	card.MonthlyUsageUSD = 1000
	card.WeeklyUsageUSD = 300
	require.Empty(t, card.PendingResetWindow(now))
}

func TestWeekCardTotalCannotBeAdvancedAsAWeeklyWindow(t *testing.T) {
	now := timezone.Now()
	weekStart := now.Add(-5 * 24 * time.Hour)
	card := &UserBalanceCard{
		Status:            BalanceCardStatusActive,
		CardType:          BalanceCardTypeWeek,
		ExpiresAt:         now.Add(20 * 24 * time.Hour),
		DailyQuotaUSD:     60,
		DailyUsageUSD:     40,
		DailyWindowStart:  &now,
		WeeklyQuotaUSD:    300,
		WeeklyUsageUSD:    300,
		WeeklyWindowStart: &weekStart,
		MaxResetCount:     20,
	}

	require.Empty(t, card.PendingResetWindow(now))
}

func TestNormalizeBalanceCardPlanDefaultsAndBounds(t *testing.T) {
	input := &CreateBalanceCardPlanInput{Name: " Month Card ", ValidityDays: 30, DailyQuotaUSD: 20}
	require.NoError(t, normalizeBalanceCardPlanInput(input))
	require.Equal(t, "Month Card", input.Name)
	require.Equal(t, BalanceCardTypeCustom, input.CardType)
	require.Equal(t, StatusActive, input.Status)

	input.MaxResetCount = 1001
	require.ErrorIs(t, normalizeBalanceCardPlanInput(input), ErrBalanceCardInvalidInput)
}

func TestNormalizeBalanceCardPlanAllowsUnlimitedQuotas(t *testing.T) {
	week := &CreateBalanceCardPlanInput{
		Name: "Week Card", CardType: BalanceCardTypeWeek, ValidityDays: 7,
	}
	require.NoError(t, normalizeBalanceCardPlanInput(week))
	require.Zero(t, week.MonthlyQuotaUSD)

	month := &CreateBalanceCardPlanInput{
		Name: "Month Card", CardType: BalanceCardTypeMonth, ValidityDays: 30,
	}
	require.NoError(t, normalizeBalanceCardPlanInput(month))

	custom := &CreateBalanceCardPlanInput{
		Name: "Custom Card", CardType: BalanceCardTypeCustom, ValidityDays: 10,
		DailyQuotaUSD: 10, WeeklyQuotaUSD: 50, MonthlyQuotaUSD: 200,
	}
	require.NoError(t, normalizeBalanceCardPlanInput(custom))
	require.Zero(t, custom.WeeklyQuotaUSD)
	require.Zero(t, custom.MonthlyQuotaUSD)

	month.DailyQuotaUSD = -1
	require.ErrorIs(t, normalizeBalanceCardPlanInput(month), ErrBalanceCardInvalidInput)
}

func TestBalanceCardZeroQuotaMeansUnlimited(t *testing.T) {
	now := timezone.Now()
	card := &UserBalanceCard{CardType: BalanceCardTypeMonth}
	require.True(t, math.IsInf(card.DailyRemaining(now), 1))
	require.True(t, math.IsInf(card.WeeklyRemaining(now), 1))
	require.True(t, math.IsInf(card.MonthlyRemaining(), 1))
	require.True(t, math.IsInf(card.AvailableRemaining(now), 1))

	card.WeeklyQuotaUSD = 50
	card.WeeklyUsageUSD = 47
	card.WeeklyWindowStart = &now
	require.InDelta(t, 3, card.AvailableRemaining(now), 0.000001)
}

func TestBalanceCardAvailableRemainingUsesTightestLimit(t *testing.T) {
	now := timezone.Now()
	today := timezone.StartOfDay(now)
	weekStart := timezone.StartOfWeek(now)
	card := &UserBalanceCard{
		CardType: BalanceCardTypeMonth, DailyQuotaUSD: 10, DailyUsageUSD: 4,
		DailyWindowStart: &today, WeeklyQuotaUSD: 50, WeeklyUsageUSD: 47,
		WeeklyWindowStart: &weekStart, MonthlyQuotaUSD: 200, MonthlyUsageUSD: 170,
	}
	require.InDelta(t, 3, card.AvailableRemaining(now), 0.000001)
}

func TestBalanceCardWeeklyWindowOnlyRollsForMonthCards(t *testing.T) {
	now := timezone.Now()
	previousWindow := now.Add(-8 * 24 * time.Hour)
	weekCard := &UserBalanceCard{
		CardType: BalanceCardTypeWeek, WeeklyQuotaUSD: 50,
		WeeklyUsageUSD: 40, WeeklyWindowStart: &previousWindow,
	}
	monthCard := &UserBalanceCard{
		CardType: BalanceCardTypeMonth, WeeklyQuotaUSD: 50,
		WeeklyUsageUSD: 40, WeeklyWindowStart: &previousWindow,
	}
	require.InDelta(t, 10, weekCard.WeeklyRemaining(now), 0.000001)
	require.InDelta(t, 50, monthCard.WeeklyRemaining(now), 0.000001)
}

func TestMonthCardTotalDoesNotResetWithWeeklyWindow(t *testing.T) {
	now := timezone.Now()
	previousWindow := now.Add(-8 * 24 * time.Hour)
	card := &UserBalanceCard{
		CardType: BalanceCardTypeMonth, DailyQuotaUSD: 10,
		WeeklyQuotaUSD: 50, WeeklyUsageUSD: 40, WeeklyWindowStart: &previousWindow,
		MonthlyQuotaUSD: 200, MonthlyUsageUSD: 190,
	}
	require.InDelta(t, 10, card.AvailableRemaining(now), 0.000001)
}

func TestMonthCardWeeklyWindowIsAnchoredToActivation(t *testing.T) {
	now := timezone.Now()
	windowStart := now.Add(-6*24*time.Hour - 23*time.Hour)
	card := &UserBalanceCard{
		CardType: BalanceCardTypeMonth, WeeklyQuotaUSD: 50,
		WeeklyUsageUSD: 40, WeeklyWindowStart: &windowStart,
	}
	require.InDelta(t, 10, card.WeeklyRemaining(now), 0.000001)

	now = now.Add(2 * time.Hour)
	require.InDelta(t, 50, card.WeeklyRemaining(now), 0.000001)
}

func TestBalanceCardLimitErrorPrefersPeriodLimit(t *testing.T) {
	now := timezone.Now()
	today := timezone.StartOfDay(now)
	weekStart := timezone.StartOfWeek(now)
	snapshot := &BalanceCardWalletSnapshot{
		CardType: BalanceCardTypeMonth, DailyQuotaUSD: 10, DailyUsageUSD: 10,
		DailyWindowStart: &today, WeeklyQuotaUSD: 50, WeeklyUsageUSD: 50,
		WeeklyWindowStart: &weekStart, MonthlyQuotaUSD: 200, MonthlyUsageUSD: 150,
	}
	require.ErrorIs(t, snapshot.LimitError(now), ErrBalanceCardWeeklyLimitExceeded)
}
