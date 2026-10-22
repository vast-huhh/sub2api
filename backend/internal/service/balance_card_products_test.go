package service

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBalanceCardProductValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		price   float64
		forSale bool
		link    string
		valid   bool
	}{
		{"unlisted legacy plan", 0, false, "", true},
		{"store product", 19.99, true, " https://store.example/card?id=1 ", true},
		{"large exact-cent price", 1234567890.12, true, "", true},
		{"contact administrator", 20, true, "", true},
		{"free listed product", 0, true, "", false},
		{"negative price", -1, false, "", false},
		{"fractional cent", 1.001, true, "", false},
		{"non-finite price", math.NaN(), true, "", false},
		{"script URL", 20, true, "javascript:alert(1)", false},
		{"relative URL", 20, true, "/card/1", false},
		{"credentials in URL", 20, true, "https://user:secret@store.example/card", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := &CreateBalanceCardPlanInput{
				Name: "Monthly card", ValidityDays: 30,
				PriceCNY: &tc.price, ForSale: &tc.forSale, PurchaseURL: &tc.link,
			}
			err := normalizeBalanceCardPlanInput(input)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, ErrBalanceCardInvalidInput)
			}
		})
	}
}

type productPlanRepo struct {
	BalanceCardRepository
	plans   []BalanceCardPlan
	updated *UpdateBalanceCardPlanInput
	err     error
}

func (r *productPlanRepo) ListPlans(context.Context, bool) ([]BalanceCardPlan, error) {
	return r.plans, r.err
}

func (r *productPlanRepo) GetPlan(context.Context, int64) (*BalanceCardPlan, error) {
	return &r.plans[0], r.err
}

func (r *productPlanRepo) UpdatePlan(_ context.Context, _ int64, input *UpdateBalanceCardPlanInput) (*BalanceCardPlan, error) {
	r.updated = input
	return &r.plans[0], r.err
}

func TestBalanceCardShopOnlyPublishesActivePricedProducts(t *testing.T) {
	repo := &productPlanRepo{plans: []BalanceCardPlan{
		{ID: 1, Name: "Published", Status: StatusActive, ForSale: true, PriceCNY: 20, PurchaseURL: "https://store.example/card"},
		{ID: 2, Name: "Draft", Status: StatusActive, ForSale: false, PriceCNY: 20},
		{ID: 3, Name: "Disabled", Status: "inactive", ForSale: true, PriceCNY: 20},
		{ID: 4, Name: "No price", Status: StatusActive, ForSale: true},
	}}
	svc := NewBalanceCardService(repo, nil)
	products, err := svc.ListProducts(context.Background())
	require.NoError(t, err)
	require.Len(t, products, 1)
	require.Equal(t, int64(1), products[0].ID)
	require.Equal(t, "https://store.example/card", products[0].PurchaseURL)

	repo.plans = nil
	products, err = svc.ListProducts(context.Background())
	require.NoError(t, err)
	require.NotNil(t, products)
	require.Empty(t, products)

	repo.err = errors.New("database unavailable")
	_, err = svc.ListProducts(context.Background())
	require.ErrorIs(t, err, repo.err)
}

func TestBalanceCardLegacyUpdatePreservesProductSettings(t *testing.T) {
	repo := &productPlanRepo{plans: []BalanceCardPlan{{PriceCNY: 20, ForSale: true, PurchaseURL: "https://store.example/card"}}}
	svc := NewBalanceCardService(repo, nil)
	_, err := svc.UpdatePlan(context.Background(), 1, &UpdateBalanceCardPlanInput{Name: "Updated", ValidityDays: 30})
	require.NoError(t, err)
	require.Equal(t, 20.0, *repo.updated.PriceCNY)
	require.True(t, *repo.updated.ForSale)
	require.Equal(t, "https://store.example/card", *repo.updated.PurchaseURL)

	forSale := false
	_, err = svc.UpdatePlan(context.Background(), 1, &UpdateBalanceCardPlanInput{Name: "Updated", ValidityDays: 30, ForSale: &forSale})
	require.NoError(t, err)
	require.False(t, *repo.updated.ForSale)
	require.Equal(t, 20.0, *repo.updated.PriceCNY)
	require.Equal(t, "https://store.example/card", *repo.updated.PurchaseURL)
}
