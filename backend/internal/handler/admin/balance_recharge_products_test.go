package admin_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type rechargeCatalogSettings struct {
	service.SettingRepository
	values map[string]string
}

func (r *rechargeCatalogSettings) GetMultiple(_ context.Context, _ []string) (map[string]string, error) {
	return r.values, nil
}

func (r *rechargeCatalogSettings) SetMultiple(_ context.Context, values map[string]string) error {
	for key, value := range values {
		r.values[key] = value
	}
	return nil
}

func TestBalanceRechargeProductAPISavesEmptyCatalogAndPublishesOnlyListedProducts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &rechargeCatalogSettings{values: map[string]string{}}
	svc := service.NewPaymentConfigService(nil, repo, nil)
	admin := adminhandler.NewPaymentHandler(nil, svc)
	user := handler.NewPaymentHandler(nil, svc)
	router := gin.New()
	router.PUT("/catalog", admin.UpdateBalanceRechargeProducts)
	router.GET("/catalog", admin.GetBalanceRechargeProducts)
	router.GET("/products", user.GetBalanceRechargeProducts)
	request := func(method, path, body string, wantStatus int) json.RawMessage {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		require.Equal(t, wantStatus, response.Code, response.Body.String())
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		return envelope.Data
	}

	// Explicit false and an empty array must bind successfully and stay empty.
	request("PUT", "/catalog", `{"enabled":false,"products":[]}`, 200)
	require.JSONEq(t, `[]`, string(request("GET", "/products", "", 200)))
	require.Equal(t, "[]", repo.values[service.SettingBalanceRechargeProducts])

	request("PUT", "/catalog", `{"enabled":true,"products":[
		{"id":"listed","name":"Balance","price_cny":1,"credit_usd":5,"purchase_url":"https://store.example/balance","for_sale":true},
		{"id":"draft","name":"Draft","price_cny":2,"credit_usd":10,"for_sale":false}
	]}`, 200)
	var products []service.BalanceRechargeProduct
	require.NoError(t, json.Unmarshal(request("GET", "/products", "", 200), &products))
	require.Len(t, products, 1)
	require.Equal(t, "listed", products[0].ID)
	require.Equal(t, "https://store.example/balance", products[0].PurchaseURL)

	// Missing fields cannot accidentally delete an existing catalog.
	request("PUT", "/catalog", `{"enabled":false}`, 400)
	var catalog service.BalanceRechargeCatalog
	require.NoError(t, json.Unmarshal(request("GET", "/catalog", "", 200), &catalog))
	require.True(t, catalog.Enabled)
	require.Len(t, catalog.Products, 2)
}
