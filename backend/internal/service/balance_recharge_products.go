package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const SettingBalanceRechargeProducts = "BALANCE_RECHARGE_PRODUCTS"

// BalanceRechargeProduct describes a balance product sold through an external store.
type BalanceRechargeProduct struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	PriceCNY    float64 `json:"price_cny"`
	CreditUSD   float64 `json:"credit_usd"`
	PurchaseURL string  `json:"purchase_url"`
	ForSale     bool    `json:"for_sale"`
	SortOrder   int     `json:"sort_order"`
}

type BalanceRechargeCatalog struct {
	Enabled  bool                     `json:"enabled"`
	Products []BalanceRechargeProduct `json:"products"`
}

func defaultBalanceRechargeProducts() []BalanceRechargeProduct {
	return []BalanceRechargeProduct{{
		ID: "balance-recharge-default", Name: "余额充值永不过期",
		PriceCNY: 1, CreditUSD: 5, ForSale: true,
	}}
}

func validRechargeProductAmount(value float64, decimalPlaces int) bool {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value > 9999999999.99 {
		return false
	}
	raw := strconv.FormatFloat(value, 'f', -1, 64)
	dot := strings.IndexByte(raw, '.')
	return dot < 0 || len(raw)-dot-1 <= decimalPlaces
}

func normalizeBalanceRechargeProducts(products []BalanceRechargeProduct) ([]BalanceRechargeProduct, error) {
	invalid := func(message string) ([]BalanceRechargeProduct, error) {
		return nil, infraerrors.BadRequest("INVALID_RECHARGE_PRODUCT", message)
	}
	if products == nil || len(products) > 100 {
		return invalid("Provide a product list with at most 100 products")
	}
	result := make([]BalanceRechargeProduct, 0, len(products))
	ids := make(map[string]bool, len(products))
	for _, product := range products {
		product.ID = strings.TrimSpace(product.ID)
		if product.ID == "" {
			product.ID = uuid.NewString()
		}
		if len(product.ID) > 64 || ids[product.ID] {
			return invalid("Product IDs must be unique and at most 64 characters")
		}
		ids[product.ID] = true
		product.Name = strings.TrimSpace(product.Name)
		product.Description = strings.TrimSpace(product.Description)
		product.PurchaseURL = strings.TrimSpace(product.PurchaseURL)
		if product.Name == "" || utf8.RuneCountInString(product.Name) > 100 || utf8.RuneCountInString(product.Description) > 2000 {
			return invalid("Product names are required (up to 100 characters); descriptions allow up to 2000 characters")
		}
		if !validRechargeProductAmount(product.PriceCNY, 2) {
			return invalid("Product price must be positive with at most two decimal places")
		}
		if !validRechargeProductAmount(product.CreditUSD, 8) {
			return invalid("Credited balance must be positive with at most eight decimal places")
		}
		if product.SortOrder < 0 || product.SortOrder > 100000 {
			return invalid("Sort order must be between 0 and 100000")
		}
		if product.PurchaseURL != "" {
			parsed, err := url.Parse(product.PurchaseURL)
			if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" || parsed.User != nil || len(product.PurchaseURL) > 2048 {
				return invalid("Purchase link must be an HTTP(S) URL without embedded credentials (up to 2048 characters)")
			}
		}
		result = append(result, product)
	}
	// Preserve the administrator's relative order when sort values are equal.
	sort.SliceStable(result, func(i, j int) bool { return result[i].SortOrder < result[j].SortOrder })
	return result, nil
}

func (s *PaymentConfigService) GetBalanceRechargeCatalog(ctx context.Context) (*BalanceRechargeCatalog, error) {
	values, err := s.settingRepo.GetMultiple(ctx, []string{SettingBalanceRechargeProducts, SettingManualRechargeEnabled})
	if err != nil {
		return nil, fmt.Errorf("get balance recharge products: %w", err)
	}
	products := defaultBalanceRechargeProducts()
	if raw := strings.TrimSpace(values[SettingBalanceRechargeProducts]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &products); err != nil {
			return nil, fmt.Errorf("decode balance recharge products: %w", err)
		}
	}
	products, err = normalizeBalanceRechargeProducts(products)
	if err != nil {
		return nil, err
	}
	return &BalanceRechargeCatalog{Enabled: values[SettingManualRechargeEnabled] != "false", Products: products}, nil
}

func (s *PaymentConfigService) UpdateBalanceRechargeCatalog(ctx context.Context, catalog BalanceRechargeCatalog) (*BalanceRechargeCatalog, error) {
	products, err := normalizeBalanceRechargeProducts(catalog.Products)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(products)
	if err != nil {
		return nil, err
	}
	if err := s.settingRepo.SetMultiple(ctx, map[string]string{
		SettingBalanceRechargeProducts: string(raw),
		SettingManualRechargeEnabled:   strconv.FormatBool(catalog.Enabled),
	}); err != nil {
		return nil, fmt.Errorf("save balance recharge products: %w", err)
	}
	return &BalanceRechargeCatalog{Enabled: catalog.Enabled, Products: products}, nil
}

func (s *PaymentConfigService) ListBalanceRechargeProducts(ctx context.Context) ([]BalanceRechargeProduct, error) {
	catalog, err := s.GetBalanceRechargeCatalog(ctx)
	if err != nil {
		return nil, err
	}
	products := make([]BalanceRechargeProduct, 0, len(catalog.Products))
	if catalog.Enabled {
		for _, product := range catalog.Products {
			if product.ForSale {
				products = append(products, product)
			}
		}
	}
	return products, nil
}
