package service

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBalanceRechargeCatalogPersistsAndFiltersProducts(t *testing.T) {
	ctx := context.Background()
	repo := &paymentConfigSettingRepoStub{}
	svc := NewPaymentConfigService(nil, repo, nil)
	catalog, err := svc.GetBalanceRechargeCatalog(ctx)
	require.NoError(t, err)
	require.True(t, catalog.Enabled)
	require.Len(t, catalog.Products, 1)
	require.Equal(t, "余额充值永不过期", catalog.Products[0].Name)
	require.Equal(t, 1.0, catalog.Products[0].PriceCNY)
	require.Equal(t, 5.0, catalog.Products[0].CreditUSD)

	first := catalog.Products[0]
	first.SortOrder = 10
	first.PurchaseURL = " https://store.example/balance?id=1 "
	draft := first
	draft.ID = ""
	draft.Name = "Unlisted product"
	draft.ForSale = false
	draft.SortOrder = 0
	catalog.Products = []BalanceRechargeProduct{first, draft}
	saved, err := svc.UpdateBalanceRechargeCatalog(ctx, *catalog)
	require.NoError(t, err)
	require.NotEmpty(t, saved.Products[0].ID)
	require.Equal(t, "Unlisted product", saved.Products[0].Name)
	require.Equal(t, "https://store.example/balance?id=1", saved.Products[1].PurchaseURL)

	// A fresh service reads persisted products and stable IDs after a restart.
	svc = NewPaymentConfigService(nil, repo, nil)
	reloaded, err := svc.GetBalanceRechargeCatalog(ctx)
	require.NoError(t, err)
	require.Equal(t, saved, reloaded)
	products, err := svc.ListBalanceRechargeProducts(ctx)
	require.NoError(t, err)
	require.Len(t, products, 1)
	require.Equal(t, first.ID, products[0].ID)

	reloaded.Enabled = false
	_, err = svc.UpdateBalanceRechargeCatalog(ctx, *reloaded)
	require.NoError(t, err)
	require.Equal(t, "false", repo.values[SettingManualRechargeEnabled])
	products, err = svc.ListBalanceRechargeProducts(ctx)
	require.NoError(t, err)
	require.NotNil(t, products)
	require.Empty(t, products)
}

func TestBalanceRechargeCatalogEmptyDoesNotRestoreDefaultProduct(t *testing.T) {
	repo := &paymentConfigSettingRepoStub{}
	svc := NewPaymentConfigService(nil, repo, nil)
	_, err := svc.UpdateBalanceRechargeCatalog(context.Background(), BalanceRechargeCatalog{
		Enabled: true, Products: []BalanceRechargeProduct{},
	})
	require.NoError(t, err)
	require.Equal(t, "[]", repo.values[SettingBalanceRechargeProducts])
	catalog, err := svc.GetBalanceRechargeCatalog(context.Background())
	require.NoError(t, err)
	require.Empty(t, catalog.Products)
}

func TestBalanceRechargeProductAmountPrecision(t *testing.T) {
	require.True(t, validRechargeProductAmount(1234567890.12, 2))
	require.True(t, validRechargeProductAmount(1234567890.12, 8))
	require.True(t, validRechargeProductAmount(0.00000001, 8))
	require.False(t, validRechargeProductAmount(0.000000001, 8))
	require.False(t, validRechargeProductAmount(19.991, 2))
}

func TestBalanceRechargeProductRejectsInvalidSettingsBeforeSaving(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*BalanceRechargeProduct)
	}{
		{"missing name", func(p *BalanceRechargeProduct) { p.Name = " " }},
		{"negative price", func(p *BalanceRechargeProduct) { p.PriceCNY = -1 }},
		{"fractional cent", func(p *BalanceRechargeProduct) { p.PriceCNY = 1.001 }},
		{"zero credit", func(p *BalanceRechargeProduct) { p.CreditUSD = 0 }},
		{"infinite credit", func(p *BalanceRechargeProduct) { p.CreditUSD = math.Inf(1) }},
		{"script URL", func(p *BalanceRechargeProduct) { p.PurchaseURL = "javascript:alert(1)" }},
		{"credentials", func(p *BalanceRechargeProduct) { p.PurchaseURL = "https://user:secret@store.example/" }},
		{"relative URL", func(p *BalanceRechargeProduct) { p.PurchaseURL = "/balance/1" }},
		{"oversized description", func(p *BalanceRechargeProduct) { p.Description = strings.Repeat("长", 2001) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &paymentConfigSettingRepoStub{}
			svc := NewPaymentConfigService(nil, repo, nil)
			product := defaultBalanceRechargeProducts()[0]
			tc.change(&product)
			_, err := svc.UpdateBalanceRechargeCatalog(context.Background(), BalanceRechargeCatalog{
				Enabled: true, Products: []BalanceRechargeProduct{product},
			})
			require.Error(t, err)
			require.Nil(t, repo.updates)
		})
	}
}
