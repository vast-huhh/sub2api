package repository

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func newTestExitLocationProber(t *testing.T, trace http.HandlerFunc, geo http.HandlerFunc) *proxyExitLocationProber {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/cdn-cgi/trace", trace)
	mux.HandleFunc("/json/", geo)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &proxyExitLocationProber{
		traceURL:   srv.URL + "/cdn-cgi/trace",
		geoBaseURL: srv.URL + "/json/",
		client:     func(string) (*http.Client, error) { return srv.Client(), nil },
	}
}

func TestProxyExitLocationProberUsesIPSeenByChatGPT(t *testing.T) {
	var geoPath string
	p := newTestExitLocationProber(t,
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("fl=1\nip=54.178.66.215\nloc=JP\n"))
		},
		func(w http.ResponseWriter, r *http.Request) {
			geoPath = r.URL.Path
			require.Contains(t, r.URL.RawQuery, "timezone")
			_, _ = w.Write([]byte(`{"status":"success","query":"54.178.66.215","countryCode":"JP","regionName":"Tokyo","city":"Tokyo","timezone":"Asia/Tokyo"}`))
		})
	res, err := p.ProbeExitLocation(context.Background(), "http://127.0.0.1:44004")
	require.NoError(t, err)
	require.Equal(t, "/json/54.178.66.215", geoPath, "geo lookup targets the exact IP OpenAI sees")
	require.Equal(t, "54.178.66.215", res.ExitIP)
	require.Equal(t, "Asia/Tokyo", res.Timezone)
	require.Equal(t, "JP", res.CountryCode)
	require.Equal(t, "chatgpt_trace", res.Source)
}

func TestProxyExitLocationProberRejectsCountryConflict(t *testing.T) {
	p := newTestExitLocationProber(t,
		func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ip=54.178.66.215\nloc=US\n")) },
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":"success","query":"54.178.66.215","countryCode":"JP","regionName":"Tokyo","city":"Tokyo","timezone":"Asia/Tokyo"}`))
		})
	_, err := p.ProbeExitLocation(context.Background(), "http://127.0.0.1:44004")
	require.Error(t, err)
	require.Contains(t, err.Error(), "conflict")
}

func TestProxyExitLocationProberRejectsGeoForAnotherIP(t *testing.T) {
	p := newTestExitLocationProber(t,
		func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ip=54.178.66.215\nloc=JP\n")) },
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":"success","query":"3.115.15.82","countryCode":"JP","regionName":"Tokyo","city":"Tokyo","timezone":"Asia/Tokyo"}`))
		})
	_, err := p.ProbeExitLocation(context.Background(), "http://127.0.0.1:44004")
	require.Error(t, err)
}

func TestProxyExitLocationProberFallsBackToGeoSelfWhenTraceFails(t *testing.T) {
	var geoPath string
	p := newTestExitLocationProber(t,
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) },
		func(w http.ResponseWriter, r *http.Request) {
			geoPath = r.URL.Path
			_, _ = w.Write([]byte(`{"status":"success","query":"52.221.235.111","countryCode":"SG","regionName":"Central Singapore","city":"Singapore","timezone":"Asia/Singapore"}`))
		})
	res, err := p.ProbeExitLocation(context.Background(), "http://127.0.0.1:44007")
	require.NoError(t, err)
	require.Equal(t, "/json/", geoPath)
	require.Equal(t, "ip_api_self", res.Source)
	require.Equal(t, "Asia/Singapore", res.Timezone)
}

func TestProxyExitLocationProberRequiresProxyAndTimezone(t *testing.T) {
	p := newTestExitLocationProber(t,
		func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ip=54.178.66.215\n")) },
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":"success","query":"54.178.66.215","countryCode":"JP"}`))
		})
	_, err := p.ProbeExitLocation(context.Background(), "")
	require.Error(t, err, "never probes from the host's own IP")
	_, err = p.ProbeExitLocation(context.Background(), "http://127.0.0.1:44004")
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "timezone"))
}
