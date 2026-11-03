package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	proxyExitLocationTraceURL       = "https://chatgpt.com/cdn-cgi/trace"
	proxyExitLocationGeoBaseURL     = "http://ip-api.com/json/"
	proxyExitLocationGeoFields      = "status,message,query,countryCode,regionName,city,timezone"
	proxyExitLocationRequestTimeout = 12 * time.Second
	proxyExitLocationMaxBodyBytes   = 16 << 10
)

// proxyExitLocationProber measures the egress OpenAI sees through a proxy:
//
//  1. chatgpt.com/cdn-cgi/trace through the proxy → the IP (and country) the
//     OpenAI edge observes, which is what matters for the account;
//  2. ip-api.com/json/<that IP> through the same proxy → timezone and geo,
//     cross-checked against the trace country;
//  3. if the trace is unreachable, ip-api's own view of the caller through the
//     proxy is used and marked as such.
//
// Nothing account-related (tokens, headers) is sent to either service.
type proxyExitLocationProber struct {
	traceURL   string
	geoBaseURL string
	client     func(proxyURL string) (*http.Client, error)
}

func NewProxyExitLocationProber() service.ProxyExitLocationProber {
	return &proxyExitLocationProber{
		traceURL:   proxyExitLocationTraceURL,
		geoBaseURL: proxyExitLocationGeoBaseURL,
		client: func(proxyURL string) (*http.Client, error) {
			return httpclient.GetClient(httpclient.Options{
				ProxyURL: proxyURL,
				Timeout:  proxyExitLocationRequestTimeout,
			})
		},
	}
}

type proxyExitLocationGeoResponse struct {
	Status      string `json:"status"`
	Message     string `json:"message"`
	Query       string `json:"query"`
	CountryCode string `json:"countryCode"`
	RegionName  string `json:"regionName"`
	City        string `json:"city"`
	Timezone    string `json:"timezone"`
}

func (p *proxyExitLocationProber) ProbeExitLocation(ctx context.Context, proxyURL string) (*service.ProxyExitLocationProbeResult, error) {
	if strings.TrimSpace(proxyURL) == "" {
		return nil, errors.New("exit location probe requires a proxy")
	}
	client, err := p.client(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("create proxy client: %w", err)
	}

	traceIP, traceCountry, traceErr := p.trace(ctx, client)
	if traceErr == nil {
		geo, err := p.geo(ctx, client, traceIP)
		if err != nil {
			return nil, err
		}
		if geo.Query != traceIP {
			return nil, fmt.Errorf("geo lookup answered for %s instead of %s", geo.Query, traceIP)
		}
		if traceCountry != "" && !strings.EqualFold(traceCountry, geo.CountryCode) {
			return nil, fmt.Errorf("exit country conflict: chatgpt.com sees %s, geo lookup says %s", traceCountry, geo.CountryCode)
		}
		return geoResult(geo, "chatgpt_trace"), nil
	}

	geo, err := p.geo(ctx, client, "")
	if err != nil {
		return nil, fmt.Errorf("chatgpt trace failed (%v); fallback geo lookup failed: %w", traceErr, err)
	}
	return geoResult(geo, "ip_api_self"), nil
}

func geoResult(geo *proxyExitLocationGeoResponse, source string) *service.ProxyExitLocationProbeResult {
	return &service.ProxyExitLocationProbeResult{
		ExitIP:      geo.Query,
		CountryCode: strings.ToUpper(strings.TrimSpace(geo.CountryCode)),
		Region:      strings.TrimSpace(geo.RegionName),
		City:        strings.TrimSpace(geo.City),
		Timezone:    strings.TrimSpace(geo.Timezone),
		Source:      source,
	}
}

func (p *proxyExitLocationProber) trace(ctx context.Context, client *http.Client) (string, string, error) {
	body, err := proxyExitLocationGet(ctx, client, p.traceURL)
	if err != nil {
		return "", "", err
	}
	var ip, loc string
	for _, line := range strings.Split(string(body), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		switch key {
		case "ip":
			ip = strings.TrimSpace(value)
		case "loc":
			loc = strings.TrimSpace(value)
		}
	}
	if net.ParseIP(ip) == nil {
		return "", "", errors.New("chatgpt trace returned no usable ip")
	}
	if strings.EqualFold(loc, "XX") || strings.EqualFold(loc, "T1") {
		loc = ""
	}
	return ip, loc, nil
}

func (p *proxyExitLocationProber) geo(ctx context.Context, client *http.Client, ip string) (*proxyExitLocationGeoResponse, error) {
	target := p.geoBaseURL + url.PathEscape(ip) + "?fields=" + url.QueryEscape(proxyExitLocationGeoFields)
	body, err := proxyExitLocationGet(ctx, client, target)
	if err != nil {
		return nil, fmt.Errorf("geo lookup: %w", err)
	}
	var geo proxyExitLocationGeoResponse
	if err := json.Unmarshal(body, &geo); err != nil {
		return nil, fmt.Errorf("geo lookup returned invalid json: %w", err)
	}
	if !strings.EqualFold(geo.Status, "success") {
		return nil, fmt.Errorf("geo lookup failed: %s", strings.TrimSpace(geo.Message))
	}
	if net.ParseIP(geo.Query) == nil {
		return nil, errors.New("geo lookup returned no ip")
	}
	if strings.TrimSpace(geo.Timezone) == "" {
		return nil, errors.New("geo lookup returned no timezone")
	}
	return &geo, nil
}

func proxyExitLocationGet(ctx context.Context, client *http.Client, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, proxyExitLocationMaxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > proxyExitLocationMaxBodyBytes {
		return nil, errors.New("response too large")
	}
	return body, nil
}
