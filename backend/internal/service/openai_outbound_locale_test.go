package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type memoryProxyExitLocationRepo struct {
	mu      sync.Mutex
	rows    map[int64]ProxyExitLocation
	follow  []int64
	listErr error
}

func newMemoryProxyExitLocationRepo() *memoryProxyExitLocationRepo {
	return &memoryProxyExitLocationRepo{rows: map[int64]ProxyExitLocation{}}
}

func (r *memoryProxyExitLocationRepo) ListProxyExitLocations(context.Context) ([]ProxyExitLocation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.listErr != nil {
		return nil, r.listErr
	}
	out := make([]ProxyExitLocation, 0, len(r.rows))
	for _, row := range r.rows {
		out = append(out, row)
	}
	return out, nil
}

func (r *memoryProxyExitLocationRepo) SaveProxyExitLocationSuccess(_ context.Context, loc *ProxyExitLocation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	copied := *loc
	copied.FailureCount = 0
	copied.LastError = ""
	r.rows[loc.ProxyID] = copied
	return nil
}

// Mirrors the SQL CASE semantics of the real repository.
func (r *memoryProxyExitLocationRepo) SaveProxyExitLocationFailure(_ context.Context, proxyID int64, fingerprint, lastError string, attemptedAt, nextProbeAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	row, exists := r.rows[proxyID]
	if !exists || row.ConfigFingerprint != fingerprint {
		row = ProxyExitLocation{ProxyID: proxyID}
	}
	row.ConfigFingerprint = fingerprint
	row.FailureCount++
	row.LastError = lastError
	row.AttemptedAt = attemptedAt
	row.NextProbeAt = nextProbeAt
	r.rows[proxyID] = row
	return nil
}

func (r *memoryProxyExitLocationRepo) ListFollowProxyIDs(context.Context) ([]int64, error) {
	return r.follow, nil
}

type scriptedExitProber struct {
	mu      sync.Mutex
	results []*ProxyExitLocationProbeResult
	errs    []error
	calls   int
	block   chan struct{}
}

func (p *scriptedExitProber) ProbeExitLocation(context.Context, string) (*ProxyExitLocationProbeResult, error) {
	if p.block != nil {
		<-p.block
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	i := p.calls
	p.calls++
	if i < len(p.errs) && p.errs[i] != nil {
		return nil, p.errs[i]
	}
	if i < len(p.results) {
		copied := *p.results[i]
		return &copied, nil
	}
	return nil, errors.New("no scripted result")
}

func (p *scriptedExitProber) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type outboundLocaleProxyRepo struct {
	ProxyRepository
	proxies map[int64]Proxy
}

func (r *outboundLocaleProxyRepo) GetByID(_ context.Context, id int64) (*Proxy, error) {
	p, ok := r.proxies[id]
	if !ok {
		return nil, ErrProxyNotFound
	}
	return &p, nil
}

func (r *outboundLocaleProxyRepo) ListByIDs(_ context.Context, ids []int64) ([]Proxy, error) {
	out := []Proxy{}
	for _, id := range ids {
		if p, ok := r.proxies[id]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

func tokyoResult() *ProxyExitLocationProbeResult {
	return &ProxyExitLocationProbeResult{ExitIP: "54.178.66.215", CountryCode: "JP", Region: "Tokyo", City: "Tokyo", Timezone: "Asia/Tokyo", Source: "chatgpt_trace"}
}

func followAccount(proxy *Proxy, extra map[string]any) *Account {
	id := proxy.ID
	if extra == nil {
		extra = map[string]any{}
	}
	extra[OpenAIOutboundTimezoneModeExtraKey] = OpenAIOutboundTimezoneModeFollowProxy
	return &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ProxyID: &id, Proxy: proxy, Extra: extra}
}

func TestOutboundLocaleResolveModes(t *testing.T) {
	var nilSvc *OpenAIOutboundLocaleService
	require.Nil(t, nilSvc.ResolveForAccount(context.Background(), &Account{Platform: PlatformOpenAI}))

	manual := &Account{Platform: PlatformOpenAI, Extra: map[string]any{
		OpenAIOutboundTimezoneModeExtraKey: OpenAIOutboundTimezoneModeManual,
		OpenAIOutboundTimezoneExtraKey:     "Asia/Singapore",
	}}
	locale := nilSvc.ResolveForAccount(context.Background(), manual)
	require.NotNil(t, locale)
	require.Equal(t, "Asia/Singapore", locale.Timezone)
	require.Nil(t, locale.Search, "manual timezone never writes a search location")

	manual.Extra[OpenAIOutboundTimezoneExtraKey] = "Mars/Base"
	require.Nil(t, nilSvc.ResolveForAccount(context.Background(), manual))

	apiKey := &Account{Platform: PlatformAnthropic, Extra: map[string]any{OpenAIOutboundTimezoneModeExtraKey: OpenAIOutboundTimezoneModeManual, OpenAIOutboundTimezoneExtraKey: "UTC"}}
	require.Nil(t, nilSvc.ResolveForAccount(context.Background(), apiKey), "only OpenAI accounts")
}

func TestOutboundLocaleFollowProxyUsesLastSuccessAndKeepsItOnFailure(t *testing.T) {
	proxy := &Proxy{ID: 64, Protocol: "http", Host: "127.0.0.1", Port: 44004}
	repo := newMemoryProxyExitLocationRepo()
	prober := &scriptedExitProber{results: []*ProxyExitLocationProbeResult{tokyoResult()}, errs: []error{nil, errors.New("proxy down")}}
	svc := NewOpenAIOutboundLocaleService(repo, prober, &outboundLocaleProxyRepo{proxies: map[int64]Proxy{64: *proxy}})
	account := followAccount(proxy, nil)

	// Never probed: keep the client's value, but schedule a probe in the background.
	require.Nil(t, svc.ResolveForAccount(context.Background(), account))
	svc.onDemandGroup.Wait()
	require.Equal(t, 1, prober.callCount())

	locale := svc.ResolveForAccount(context.Background(), account)
	require.NotNil(t, locale)
	require.Equal(t, "Asia/Tokyo", locale.Timezone)
	require.Equal(t, &openAIOutboundSearchLocation{Country: "JP", Region: "Tokyo", City: "Tokyo", Timezone: "Asia/Tokyo"}, locale.Search)

	// A failed refresh of the same configuration keeps the last value.
	_, err := svc.RefreshProxy(context.Background(), 64)
	require.Error(t, err)
	locale = svc.ResolveForAccount(context.Background(), account)
	require.NotNil(t, locale)
	require.Equal(t, "Asia/Tokyo", locale.Timezone)
	row := repo.rows[64]
	require.Equal(t, 1, row.FailureCount)
	require.WithinDuration(t, time.Now().Add(15*time.Minute), row.NextProbeAt, time.Minute)
}

func TestOutboundLocaleSearchLocationCanBeDisabled(t *testing.T) {
	proxy := &Proxy{ID: 64, Protocol: "http", Host: "127.0.0.1", Port: 44004}
	repo := newMemoryProxyExitLocationRepo()
	svc := NewOpenAIOutboundLocaleService(repo, &scriptedExitProber{results: []*ProxyExitLocationProbeResult{tokyoResult()}}, &outboundLocaleProxyRepo{proxies: map[int64]Proxy{64: *proxy}})
	_, err := svc.RefreshProxy(context.Background(), 64)
	require.NoError(t, err)

	locale := svc.ResolveForAccount(context.Background(), followAccount(proxy, map[string]any{OpenAIOutboundSearchLocationExtraKey: false}))
	require.NotNil(t, locale)
	require.Nil(t, locale.Search)

	partial := newMemoryProxyExitLocationRepo()
	noCity := tokyoResult()
	noCity.City = ""
	svc = NewOpenAIOutboundLocaleService(partial, &scriptedExitProber{results: []*ProxyExitLocationProbeResult{noCity}}, &outboundLocaleProxyRepo{proxies: map[int64]Proxy{64: *proxy}})
	_, err = svc.RefreshProxy(context.Background(), 64)
	require.NoError(t, err)
	locale = svc.ResolveForAccount(context.Background(), followAccount(proxy, nil))
	require.NotNil(t, locale)
	require.Equal(t, "Asia/Tokyo", locale.Timezone, "timezone still applies")
	require.Nil(t, locale.Search, "incomplete geo never produces a partial search location")
}

func TestOutboundLocaleEditedProxyDoesNotReuseOldLocation(t *testing.T) {
	proxy := &Proxy{ID: 64, Protocol: "http", Host: "127.0.0.1", Port: 44004}
	repo := newMemoryProxyExitLocationRepo()
	prober := &scriptedExitProber{results: []*ProxyExitLocationProbeResult{tokyoResult()}, errs: []error{nil, errors.New("down")}}
	proxyRepo := &outboundLocaleProxyRepo{proxies: map[int64]Proxy{64: *proxy}}
	svc := NewOpenAIOutboundLocaleService(repo, prober, proxyRepo)
	_, err := svc.RefreshProxy(context.Background(), 64)
	require.NoError(t, err)

	edited := *proxy
	edited.Port = 44099
	proxyRepo.proxies[64] = edited
	require.Nil(t, svc.ResolveForAccount(context.Background(), followAccount(&edited, nil)), "location measured through another egress is not used")
	svc.onDemandGroup.Wait()
	require.Equal(t, 2, prober.callCount(), "edited proxy is probed on demand")
	row := repo.rows[64]
	require.Equal(t, ProxyConnectionFingerprint(&edited), row.ConfigFingerprint)
	require.Empty(t, row.Timezone, "failure for a new configuration clears the old location")
	require.Nil(t, row.SucceededAt)

	views, err := svc.ListExitLocations(context.Background())
	require.NoError(t, err)
	require.Len(t, views, 1)
	require.True(t, views[0].Current)
}

func TestOutboundLocaleOnDemandProbeIsDedupedAndNonBlocking(t *testing.T) {
	proxy := &Proxy{ID: 64, Protocol: "http", Host: "127.0.0.1", Port: 44004}
	prober := &scriptedExitProber{results: []*ProxyExitLocationProbeResult{tokyoResult()}, block: make(chan struct{})}
	svc := NewOpenAIOutboundLocaleService(newMemoryProxyExitLocationRepo(), prober, &outboundLocaleProxyRepo{proxies: map[int64]Proxy{64: *proxy}})
	account := followAccount(proxy, nil)

	start := time.Now()
	for i := 0; i < 5; i++ {
		require.Nil(t, svc.ResolveForAccount(context.Background(), account))
	}
	require.Less(t, time.Since(start), time.Second, "requests never wait for a probe")
	close(prober.block)
	svc.onDemandGroup.Wait()
	require.Equal(t, 1, prober.callCount())
}

func TestOutboundLocaleRunDueProbesOnlyProbesDueFollowProxies(t *testing.T) {
	due := Proxy{ID: 64, Protocol: "http", Host: "127.0.0.1", Port: 44004, Status: StatusActive}
	fresh := Proxy{ID: 70, Protocol: "http", Host: "127.0.0.1", Port: 44008, Status: StatusActive}
	disabled := Proxy{ID: 71, Protocol: "http", Host: "127.0.0.1", Port: 44007, Status: "inactive"}
	repo := newMemoryProxyExitLocationRepo()
	repo.follow = []int64{64, 70, 71}
	now := time.Now()
	repo.rows[70] = ProxyExitLocation{ProxyID: 70, ConfigFingerprint: ProxyConnectionFingerprint(&fresh), Timezone: "Asia/Singapore", SucceededAt: &now, NextProbeAt: now.Add(time.Hour)}
	prober := &scriptedExitProber{results: []*ProxyExitLocationProbeResult{tokyoResult()}}
	svc := NewOpenAIOutboundLocaleService(repo, prober, &outboundLocaleProxyRepo{proxies: map[int64]Proxy{64: due, 70: fresh, 71: disabled}})

	svc.runDueProbes()
	require.Equal(t, 1, prober.callCount())
	require.Equal(t, "Asia/Tokyo", repo.rows[64].Timezone)
	require.WithinDuration(t, now.Add(24*time.Hour), repo.rows[64].NextProbeAt, 31*time.Minute)
}

func TestOutboundLocaleFailureBackoff(t *testing.T) {
	require.Equal(t, 15*time.Minute, openAIOutboundLocaleFailureBackoff(1))
	require.Equal(t, time.Hour, openAIOutboundLocaleFailureBackoff(2))
	require.Equal(t, 6*time.Hour, openAIOutboundLocaleFailureBackoff(3))
	require.Equal(t, 6*time.Hour, openAIOutboundLocaleFailureBackoff(9))
}

func TestValidateOpenAIOutboundLocaleExtra(t *testing.T) {
	require.NoError(t, ValidateOpenAIOutboundLocaleExtra(PlatformOpenAI, map[string]any{}))
	require.NoError(t, ValidateOpenAIOutboundLocaleExtra(PlatformOpenAI, map[string]any{OpenAIOutboundTimezoneModeExtraKey: OpenAIOutboundTimezoneModeFollowProxy, OpenAIOutboundSearchLocationExtraKey: false}))
	require.NoError(t, ValidateOpenAIOutboundLocaleExtra(PlatformOpenAI, map[string]any{OpenAIOutboundTimezoneModeExtraKey: OpenAIOutboundTimezoneModeManual, OpenAIOutboundTimezoneExtraKey: "America/Los_Angeles"}))
	require.Error(t, ValidateOpenAIOutboundLocaleExtra(PlatformOpenAI, map[string]any{OpenAIOutboundTimezoneModeExtraKey: "auto"}))
	require.Error(t, ValidateOpenAIOutboundLocaleExtra(PlatformOpenAI, map[string]any{OpenAIOutboundTimezoneModeExtraKey: OpenAIOutboundTimezoneModeManual, OpenAIOutboundTimezoneExtraKey: "Local"}))
	require.Error(t, ValidateOpenAIOutboundLocaleExtra(PlatformOpenAI, map[string]any{OpenAIOutboundTimezoneModeExtraKey: OpenAIOutboundTimezoneModeManual, OpenAIOutboundTimezoneExtraKey: `Asia/"x`}))
	require.Error(t, ValidateOpenAIOutboundLocaleExtra(PlatformOpenAI, map[string]any{OpenAIOutboundSearchLocationExtraKey: "yes"}))
	require.NoError(t, ValidateOpenAIOutboundLocaleExtra(PlatformAnthropic, map[string]any{OpenAIOutboundTimezoneModeExtraKey: "auto"}))
}

func TestProxyConnectionFingerprintIgnoresName(t *testing.T) {
	a := &Proxy{ID: 1, Name: "a", Protocol: "http", Host: "127.0.0.1", Port: 44004}
	b := &Proxy{ID: 1, Name: "renamed", Protocol: "HTTP", Host: "127.0.0.1", Port: 44004}
	require.Equal(t, ProxyConnectionFingerprint(a), ProxyConnectionFingerprint(b))
	b.Password = "secret"
	require.NotEqual(t, ProxyConnectionFingerprint(a), ProxyConnectionFingerprint(b))
}

type countingExitLocationRepo struct {
	*memoryProxyExitLocationRepo
	mu    sync.Mutex
	lists int
	gate  chan struct{}
}

func (r *countingExitLocationRepo) ListProxyExitLocations(ctx context.Context) ([]ProxyExitLocation, error) {
	r.mu.Lock()
	r.lists++
	gate := r.gate
	r.mu.Unlock()
	if gate != nil {
		<-gate
	}
	return r.memoryProxyExitLocationRepo.ListProxyExitLocations(ctx)
}

func TestOutboundLocaleExpiredCacheServesStaleWhileReloading(t *testing.T) {
	proxy := &Proxy{ID: 64, Protocol: "http", Host: "127.0.0.1", Port: 44004}
	inner := newMemoryProxyExitLocationRepo()
	now := time.Now()
	inner.rows[64] = ProxyExitLocation{ProxyID: 64, ConfigFingerprint: ProxyConnectionFingerprint(proxy), Timezone: "Asia/Tokyo", SucceededAt: &now}
	repo := &countingExitLocationRepo{memoryProxyExitLocationRepo: inner}
	svc := NewOpenAIOutboundLocaleService(repo, &scriptedExitProber{}, &outboundLocaleProxyRepo{proxies: map[int64]Proxy{64: *proxy}})
	clock := now
	svc.now = func() time.Time { return clock }
	account := followAccount(proxy, nil)

	require.NotNil(t, svc.ResolveForAccount(context.Background(), account))
	require.Equal(t, 1, repo.lists)

	// Expire the snapshot and block the reload: requests must not wait.
	clock = clock.Add(2 * openAIOutboundLocaleCacheTTL)
	repo.mu.Lock()
	repo.gate = make(chan struct{})
	repo.mu.Unlock()
	start := time.Now()
	for i := 0; i < 10; i++ {
		locale := svc.ResolveForAccount(context.Background(), account)
		require.NotNil(t, locale)
		require.Equal(t, "Asia/Tokyo", locale.Timezone)
	}
	require.Less(t, time.Since(start), time.Second)
	close(repo.gate)
	svc.onDemandGroup.Wait()
	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Equal(t, 2, repo.lists, "a single background reload")
}
