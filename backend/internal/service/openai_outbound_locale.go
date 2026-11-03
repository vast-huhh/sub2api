package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

// Account extra keys (OpenAI accounts). Absent mode means off.
const (
	OpenAIOutboundTimezoneModeExtraKey     = "openai_outbound_timezone_mode"
	OpenAIOutboundTimezoneExtraKey         = "openai_outbound_timezone"
	OpenAIOutboundSearchLocationExtraKey   = "openai_outbound_search_location"
	OpenAIOutboundTimezoneModeFollowProxy  = "follow_proxy"
	OpenAIOutboundTimezoneModeManual       = "manual"
	openAIOutboundLocaleRefreshInterval    = 24 * time.Hour
	openAIOutboundLocaleRefreshJitter      = 30 * time.Minute
	openAIOutboundLocaleScanInterval       = 5 * time.Minute
	openAIOutboundLocaleCacheTTL           = time.Minute
	openAIOutboundLocaleProbeTimeout       = 30 * time.Second
	openAIOutboundLocaleOnDemandMinSpacing = 5 * time.Minute
	openAIOutboundLocaleLeaderLockKey      = "openai:outbound_locale:probe:leader"
	openAIOutboundLocaleLeaderLockTTL      = 10 * time.Minute
	openAIOutboundLocaleMaxConcurrentProbe = 2
)

var openAIOutboundTimezoneNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+/-]{0,63}$`)

// ProxyExitLocation is the last known egress location of one proxy. Location
// fields always belong to the last successful probe of ConfigFingerprint; a
// failed probe of the same configuration keeps them (last value wins), while a
// different configuration never inherits them.
type ProxyExitLocation struct {
	ProxyID           int64
	ConfigFingerprint string
	ExitIP            string
	CountryCode       string
	Region            string
	City              string
	Timezone          string
	Source            string
	SucceededAt       *time.Time
	AttemptedAt       time.Time
	NextProbeAt       time.Time
	FailureCount      int
	LastError         string
}

// ProxyExitLocationProbeResult is one successful egress probe.
type ProxyExitLocationProbeResult struct {
	ExitIP      string
	CountryCode string
	Region      string
	City        string
	Timezone    string
	Source      string
}

type ProxyExitLocationRepository interface {
	ListProxyExitLocations(ctx context.Context) ([]ProxyExitLocation, error)
	SaveProxyExitLocationSuccess(ctx context.Context, loc *ProxyExitLocation) error
	SaveProxyExitLocationFailure(ctx context.Context, proxyID int64, fingerprint, lastError string, attemptedAt, nextProbeAt time.Time) error
	// ListFollowProxyIDs returns proxies bound to at least one OpenAI account
	// whose outbound timezone follows its proxy.
	ListFollowProxyIDs(ctx context.Context) ([]int64, error)
}

// ProxyExitLocationProber probes the egress seen by OpenAI through proxyURL.
type ProxyExitLocationProber interface {
	ProbeExitLocation(ctx context.Context, proxyURL string) (*ProxyExitLocationProbeResult, error)
}

// ProxyConnectionFingerprint identifies the proxy connection settings an exit
// location was measured with. Renaming a proxy does not change it.
func ProxyConnectionFingerprint(p *Proxy) string {
	if p == nil {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.ToLower(strings.TrimSpace(p.Protocol)),
		strings.ToLower(strings.TrimSpace(p.Host)),
		strconv.Itoa(p.Port),
		p.Username,
		p.Password,
	}, "\x00")))
	return hex.EncodeToString(sum[:16])
}

func normalizeOpenAIOutboundTimezoneName(name string) (string, *time.Location, bool) {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, "Local") || !openAIOutboundTimezoneNamePattern.MatchString(name) {
		return "", nil, false
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return "", nil, false
	}
	return name, loc, true
}

// ValidateOpenAIOutboundLocaleExtra validates the outbound locale keys when present.
func ValidateOpenAIOutboundLocaleExtra(platform string, extra map[string]any) error {
	if platform != PlatformOpenAI || extra == nil {
		return nil
	}
	if raw, ok := extra[OpenAIOutboundTimezoneModeExtraKey]; ok && raw != nil {
		mode, isString := raw.(string)
		if !isString || (mode != "" && mode != OpenAIOutboundTimezoneModeFollowProxy && mode != OpenAIOutboundTimezoneModeManual) {
			return infraerrors.New(http.StatusBadRequest, "INVALID_OPENAI_OUTBOUND_TIMEZONE_MODE",
				"openai_outbound_timezone_mode must be follow_proxy or manual")
		}
		if mode == OpenAIOutboundTimezoneModeManual {
			tz, _ := extra[OpenAIOutboundTimezoneExtraKey].(string)
			if _, _, valid := normalizeOpenAIOutboundTimezoneName(tz); !valid {
				return infraerrors.New(http.StatusBadRequest, "INVALID_OPENAI_OUTBOUND_TIMEZONE",
					"openai_outbound_timezone must be a valid IANA timezone in manual mode")
			}
		}
	}
	if raw, ok := extra[OpenAIOutboundSearchLocationExtraKey]; ok && raw != nil {
		if _, isBool := raw.(bool); !isBool {
			return infraerrors.New(http.StatusBadRequest, "INVALID_OPENAI_OUTBOUND_SEARCH_LOCATION",
				"openai_outbound_search_location must be a boolean")
		}
	}
	return nil
}

func (a *Account) openAIOutboundTimezoneMode() string {
	if a == nil || !a.IsOpenAI() || a.Extra == nil {
		return ""
	}
	mode, _ := a.Extra[OpenAIOutboundTimezoneModeExtraKey].(string)
	switch mode {
	case OpenAIOutboundTimezoneModeFollowProxy, OpenAIOutboundTimezoneModeManual:
		return mode
	}
	return ""
}

// openAIOutboundSearchLocationEnabled defaults to on; only an explicit false
// keeps the client's search location.
func (a *Account) openAIOutboundSearchLocationEnabled() bool {
	if a == nil || a.Extra == nil {
		return true
	}
	if enabled, ok := a.Extra[OpenAIOutboundSearchLocationExtraKey].(bool); ok {
		return enabled
	}
	return true
}

type cachedProxyExitLocations struct {
	loadedAt time.Time
	byProxy  map[int64]ProxyExitLocation
}

// OpenAIOutboundLocaleService owns per-proxy exit locations and resolves the
// outbound locale of an account. Request paths only read an in-memory snapshot;
// probes run in the background (daily, plus on demand for unknown proxies).
type OpenAIOutboundLocaleService struct {
	repo      ProxyExitLocationRepository
	prober    ProxyExitLocationProber
	proxyRepo ProxyRepository

	lockCache  LeaderLockCache
	db         *sql.DB
	instanceID string

	now func() time.Time

	mu       sync.RWMutex
	cache    *cachedProxyExitLocations
	reloadMu sync.Mutex

	probeMu       sync.Mutex
	inflight      map[int64]struct{}
	lastOnDemand  map[int64]time.Time
	probeSlots    chan struct{}
	onDemandGroup sync.WaitGroup

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func NewOpenAIOutboundLocaleService(repo ProxyExitLocationRepository, prober ProxyExitLocationProber, proxyRepo ProxyRepository) *OpenAIOutboundLocaleService {
	return &OpenAIOutboundLocaleService{
		repo:         repo,
		prober:       prober,
		proxyRepo:    proxyRepo,
		instanceID:   uuid.NewString(),
		now:          time.Now,
		inflight:     make(map[int64]struct{}),
		lastOnDemand: make(map[int64]time.Time),
		probeSlots:   make(chan struct{}, openAIOutboundLocaleMaxConcurrentProbe),
		stopCh:       make(chan struct{}),
	}
}

// SetLeaderLock gates the periodic scan to one instance (best effort).
func (s *OpenAIOutboundLocaleService) SetLeaderLock(lockCache LeaderLockCache, db *sql.DB) {
	if s == nil {
		return
	}
	s.lockCache, s.db = lockCache, db
}

func (s *OpenAIOutboundLocaleService) Start() {
	if s == nil || s.repo == nil || s.prober == nil || s.proxyRepo == nil {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(openAIOutboundLocaleScanInterval)
		defer ticker.Stop()
		s.runDueProbes()
		for {
			select {
			case <-ticker.C:
				s.runDueProbes()
			case <-s.stopCh:
				return
			}
		}
	}()
}

func (s *OpenAIOutboundLocaleService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
	s.onDemandGroup.Wait()
}

// ResolveForAccount returns the outbound locale for one send attempt, or nil to
// keep the client's own values.
func (s *OpenAIOutboundLocaleService) ResolveForAccount(ctx context.Context, account *Account) *openAIOutboundLocale {
	switch account.openAIOutboundTimezoneMode() {
	case OpenAIOutboundTimezoneModeManual:
		tz, _ := account.Extra[OpenAIOutboundTimezoneExtraKey].(string)
		name, loc, ok := normalizeOpenAIOutboundTimezoneName(tz)
		if !ok {
			return nil
		}
		// A manual timezone has no trustworthy city: search location is kept.
		return &openAIOutboundLocale{Timezone: name, location: loc, Source: "manual"}
	case OpenAIOutboundTimezoneModeFollowProxy:
		if s == nil || account.ProxyID == nil || account.Proxy == nil || account.Proxy.ID != *account.ProxyID {
			return nil
		}
		exit, ok := s.currentExitLocation(ctx, account.Proxy)
		if !ok {
			return nil
		}
		name, loc, valid := normalizeOpenAIOutboundTimezoneName(exit.Timezone)
		if !valid {
			return nil
		}
		locale := &openAIOutboundLocale{
			Timezone: name,
			location: loc,
			Source:   fmt.Sprintf("proxy:%d", account.Proxy.ID),
		}
		if account.openAIOutboundSearchLocationEnabled() && exit.CountryCode != "" && exit.Region != "" && exit.City != "" {
			locale.Search = &openAIOutboundSearchLocation{
				Country:  exit.CountryCode,
				Region:   exit.Region,
				City:     exit.City,
				Timezone: name,
			}
		}
		return locale
	}
	return nil
}

// currentExitLocation returns the last successful location measured with the
// proxy's current connection settings. Unknown proxies get an on-demand probe
// in the background; the request itself never waits.
func (s *OpenAIOutboundLocaleService) currentExitLocation(ctx context.Context, proxy *Proxy) (ProxyExitLocation, bool) {
	fingerprint := ProxyConnectionFingerprint(proxy)
	exit, found := s.cachedExitLocation(ctx, proxy.ID)
	if found && exit.ConfigFingerprint == fingerprint && exit.SucceededAt != nil && exit.Timezone != "" {
		return exit, true
	}
	if !found || exit.ConfigFingerprint != fingerprint {
		s.triggerOnDemandProbe(proxy)
	}
	return ProxyExitLocation{}, false
}

// cachedExitLocation only blocks on the very first load (or right after a
// probe invalidated the snapshot). An expired snapshot keeps being served
// while a single background reload refreshes it.
func (s *OpenAIOutboundLocaleService) cachedExitLocation(ctx context.Context, proxyID int64) (ProxyExitLocation, bool) {
	s.mu.RLock()
	cache := s.cache
	s.mu.RUnlock()
	switch {
	case cache == nil:
		cache = s.loadCacheSingleFlight(ctx)
	case s.now().Sub(cache.loadedAt) > openAIOutboundLocaleCacheTTL && s.reloadMu.TryLock():
		s.onDemandGroup.Add(1)
		go func() {
			defer s.onDemandGroup.Done()
			defer s.reloadMu.Unlock()
			s.reloadCacheLocked(context.Background())
		}()
	}
	if cache == nil {
		return ProxyExitLocation{}, false
	}
	exit, ok := cache.byProxy[proxyID]
	return exit, ok
}

func (s *OpenAIOutboundLocaleService) loadCacheSingleFlight(ctx context.Context) *cachedProxyExitLocations {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	s.mu.RLock()
	cache := s.cache
	s.mu.RUnlock()
	if cache != nil {
		return cache
	}
	return s.reloadCacheLocked(ctx)
}

// reloadCacheLocked must be called with reloadMu held.
func (s *OpenAIOutboundLocaleService) reloadCacheLocked(ctx context.Context) *cachedProxyExitLocations {
	if s.repo == nil {
		return nil
	}
	loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	rows, err := s.repo.ListProxyExitLocations(loadCtx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		slog.Warn("openai.outbound_locale.cache_reload_failed", "error", err)
		// Keep serving the previous snapshot; retry on the next TTL window.
		if s.cache != nil {
			s.cache.loadedAt = s.now()
		}
		return s.cache
	}
	next := &cachedProxyExitLocations{loadedAt: s.now(), byProxy: make(map[int64]ProxyExitLocation, len(rows))}
	for _, row := range rows {
		next.byProxy[row.ProxyID] = row
	}
	s.cache = next
	return next
}

func (s *OpenAIOutboundLocaleService) invalidateCache() {
	s.mu.Lock()
	s.cache = nil
	s.mu.Unlock()
}

func (s *OpenAIOutboundLocaleService) triggerOnDemandProbe(proxy *Proxy) {
	if s == nil || s.prober == nil || s.repo == nil || proxy == nil {
		return
	}
	select {
	case <-s.stopCh:
		return
	default:
	}
	now := s.now()
	s.probeMu.Lock()
	if _, busy := s.inflight[proxy.ID]; busy {
		s.probeMu.Unlock()
		return
	}
	if last, ok := s.lastOnDemand[proxy.ID]; ok && now.Sub(last) < openAIOutboundLocaleOnDemandMinSpacing {
		s.probeMu.Unlock()
		return
	}
	s.lastOnDemand[proxy.ID] = now
	s.probeMu.Unlock()

	snapshot := *proxy
	s.onDemandGroup.Add(1)
	go func() {
		defer s.onDemandGroup.Done()
		ctx, cancel := context.WithTimeout(context.Background(), openAIOutboundLocaleProbeTimeout)
		defer cancel()
		_, _ = s.probeProxy(ctx, &snapshot)
	}()
}

// RefreshProxy probes one proxy now (admin action) and returns the stored state.
func (s *OpenAIOutboundLocaleService) RefreshProxy(ctx context.Context, proxyID int64) (*ProxyExitLocation, error) {
	if s == nil || s.proxyRepo == nil {
		return nil, errors.New("outbound locale service is not configured")
	}
	proxy, err := s.proxyRepo.GetByID(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	if proxy == nil {
		return nil, ErrProxyNotFound
	}
	probeCtx, cancel := context.WithTimeout(ctx, openAIOutboundLocaleProbeTimeout)
	defer cancel()
	return s.probeProxy(probeCtx, proxy)
}

// ProxyExitLocationView is the admin view of one proxy's exit location.
type ProxyExitLocationView struct {
	ProxyExitLocation
	// Current reports whether the location belongs to the proxy's current
	// connection settings (false after the proxy was edited).
	Current bool
}

func (s *OpenAIOutboundLocaleService) ListExitLocations(ctx context.Context) ([]ProxyExitLocationView, error) {
	if s == nil || s.repo == nil {
		return nil, nil
	}
	rows, err := s.repo.ListProxyExitLocations(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ProxyID)
	}
	current := map[int64]string{}
	if s.proxyRepo != nil && len(ids) > 0 {
		proxies, listErr := s.proxyRepo.ListByIDs(ctx, ids)
		if listErr != nil {
			return nil, listErr
		}
		for i := range proxies {
			current[proxies[i].ID] = ProxyConnectionFingerprint(&proxies[i])
		}
	}
	views := make([]ProxyExitLocationView, 0, len(rows))
	for _, row := range rows {
		fingerprint, exists := current[row.ProxyID]
		if !exists {
			continue
		}
		views = append(views, ProxyExitLocationView{ProxyExitLocation: row, Current: fingerprint == row.ConfigFingerprint})
	}
	return views, nil
}

func (s *OpenAIOutboundLocaleService) runDueProbes() {
	ctx, cancel := context.WithTimeout(context.Background(), openAIOutboundLocaleLeaderLockTTL-time.Minute)
	defer cancel()
	release, ok := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, openAIOutboundLocaleLeaderLockKey, s.instanceID, openAIOutboundLocaleLeaderLockTTL)
	if !ok {
		return
	}
	defer release()

	ids, err := s.repo.ListFollowProxyIDs(ctx)
	if err != nil {
		slog.Warn("openai.outbound_locale.list_follow_proxies_failed", "error", err)
		return
	}
	if len(ids) == 0 {
		return
	}
	proxies, err := s.proxyRepo.ListByIDs(ctx, ids)
	if err != nil {
		slog.Warn("openai.outbound_locale.list_proxies_failed", "error", err)
		return
	}
	rows, err := s.repo.ListProxyExitLocations(ctx)
	if err != nil {
		slog.Warn("openai.outbound_locale.list_locations_failed", "error", err)
		return
	}
	known := make(map[int64]ProxyExitLocation, len(rows))
	for _, row := range rows {
		known[row.ProxyID] = row
	}
	now := s.now()
	for i := range proxies {
		proxy := &proxies[i]
		if proxy.Status != "" && proxy.Status != StatusActive {
			continue
		}
		row, exists := known[proxy.ID]
		due := !exists || row.ConfigFingerprint != ProxyConnectionFingerprint(proxy) || !now.Before(row.NextProbeAt)
		if !due {
			continue
		}
		select {
		case <-s.stopCh:
			return
		default:
		}
		probeCtx, probeCancel := context.WithTimeout(ctx, openAIOutboundLocaleProbeTimeout)
		_, _ = s.probeProxy(probeCtx, proxy)
		probeCancel()
	}
}

func (s *OpenAIOutboundLocaleService) probeProxy(ctx context.Context, proxy *Proxy) (*ProxyExitLocation, error) {
	s.probeMu.Lock()
	if _, busy := s.inflight[proxy.ID]; busy {
		s.probeMu.Unlock()
		return nil, errors.New("exit location probe already running for this proxy")
	}
	s.inflight[proxy.ID] = struct{}{}
	s.probeMu.Unlock()
	defer func() {
		s.probeMu.Lock()
		delete(s.inflight, proxy.ID)
		s.probeMu.Unlock()
	}()

	select {
	case s.probeSlots <- struct{}{}:
		defer func() { <-s.probeSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	fingerprint := ProxyConnectionFingerprint(proxy)
	attemptedAt := s.now()
	id := proxy.ID
	proxyURL, err := resolveConfiguredProxyURL(ctx, nil, &id, proxy)
	var result *ProxyExitLocationProbeResult
	if err == nil {
		result, err = s.prober.ProbeExitLocation(ctx, proxyURL)
	}
	if err == nil {
		_, _, err = validateProxyExitLocationProbeResult(result)
	}
	defer s.invalidateCache()
	if err != nil {
		previousFailures := s.previousFailureCount(ctx, proxy.ID, fingerprint)
		next := attemptedAt.Add(openAIOutboundLocaleFailureBackoff(previousFailures + 1))
		message := truncateOpenAIOutboundLocaleError(err.Error())
		if saveErr := s.repo.SaveProxyExitLocationFailure(ctx, proxy.ID, fingerprint, message, attemptedAt, next); saveErr != nil {
			slog.Warn("openai.outbound_locale.save_failure_failed", "proxy_id", proxy.ID, "error", saveErr)
		}
		slog.Warn("openai.outbound_locale.probe_failed", "proxy_id", proxy.ID, "error", message)
		return s.storedExitLocation(ctx, proxy.ID), err
	}
	succeededAt := attemptedAt
	loc := &ProxyExitLocation{
		ProxyID:           proxy.ID,
		ConfigFingerprint: fingerprint,
		ExitIP:            result.ExitIP,
		CountryCode:       result.CountryCode,
		Region:            result.Region,
		City:              result.City,
		Timezone:          result.Timezone,
		Source:            result.Source,
		SucceededAt:       &succeededAt,
		AttemptedAt:       attemptedAt,
		NextProbeAt:       attemptedAt.Add(openAIOutboundLocaleRefreshInterval + openAIOutboundLocaleJitter()),
	}
	if err := s.repo.SaveProxyExitLocationSuccess(ctx, loc); err != nil {
		return nil, err
	}
	slog.Info("openai.outbound_locale.probe_succeeded", "proxy_id", proxy.ID, "timezone", loc.Timezone, "country", loc.CountryCode, "source", loc.Source)
	return loc, nil
}

func (s *OpenAIOutboundLocaleService) previousFailureCount(ctx context.Context, proxyID int64, fingerprint string) int {
	if row := s.storedExitLocation(ctx, proxyID); row != nil && row.ConfigFingerprint == fingerprint {
		return row.FailureCount
	}
	return 0
}

func (s *OpenAIOutboundLocaleService) storedExitLocation(ctx context.Context, proxyID int64) *ProxyExitLocation {
	rows, err := s.repo.ListProxyExitLocations(ctx)
	if err != nil {
		return nil
	}
	for i := range rows {
		if rows[i].ProxyID == proxyID {
			return &rows[i]
		}
	}
	return nil
}

func validateProxyExitLocationProbeResult(result *ProxyExitLocationProbeResult) (string, *time.Location, error) {
	if result == nil {
		return "", nil, errors.New("empty exit location result")
	}
	name, loc, ok := normalizeOpenAIOutboundTimezoneName(result.Timezone)
	if !ok {
		return "", nil, fmt.Errorf("exit location returned an invalid timezone %q", result.Timezone)
	}
	result.Timezone = name
	return name, loc, nil
}

// Failure backoff: 15 minutes, 1 hour, then every 6 hours. The last successful
// location keeps being used meanwhile.
func openAIOutboundLocaleFailureBackoff(failures int) time.Duration {
	switch {
	case failures <= 1:
		return 15 * time.Minute
	case failures == 2:
		return time.Hour
	default:
		return 6 * time.Hour
	}
}

func openAIOutboundLocaleJitter() time.Duration {
	return time.Duration(rand.Int64N(int64(openAIOutboundLocaleRefreshJitter)))
}

func truncateOpenAIOutboundLocaleError(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 500 {
		return message[:500]
	}
	return message
}

// SetOutboundLocaleService wires the per-account outbound locale.
func (s *OpenAIGatewayService) SetOutboundLocaleService(svc *OpenAIOutboundLocaleService) {
	s.outboundLocale = svc
}

func (s *OpenAIGatewayService) resolveOpenAIOutboundLocale(ctx context.Context, account *Account) *openAIOutboundLocale {
	if s == nil || account.openAIOutboundTimezoneMode() == "" {
		return nil
	}
	return s.outboundLocale.ResolveForAccount(ctx, account)
}

// applyOpenAIOutboundLocaleBody is the single entry used by every Responses
// send path (HTTP, compact, chat bridge, WS frames).
func (s *OpenAIGatewayService) applyOpenAIOutboundLocaleBody(ctx context.Context, account *Account, body []byte, path string) []byte {
	locale := s.resolveOpenAIOutboundLocale(ctx, account)
	if locale == nil {
		return body
	}
	next, stats := applyOpenAIOutboundLocaleToResponsesBody(body, locale)
	if stats.changed() {
		slog.Debug("openai.outbound_locale.applied",
			"account_id", account.ID,
			"path", path,
			"source", locale.Source,
			"timezone", locale.Timezone,
			"environment_blocks", stats.EnvironmentBlocks,
			"desktop_blocks", stats.DesktopBlocks,
			"dates", stats.Dates,
			"search_tools", stats.SearchTools,
		)
	}
	return next
}

func (s *OpenAIGatewayService) applyOpenAIOutboundLocaleAlphaSearchBody(ctx context.Context, account *Account, body []byte) []byte {
	locale := s.resolveOpenAIOutboundLocale(ctx, account)
	if locale == nil {
		return body
	}
	next, changed := applyOpenAIOutboundLocaleToAlphaSearchBody(body, locale)
	if changed {
		slog.Debug("openai.outbound_locale.applied", "account_id", account.ID, "path", "alpha_search", "source", locale.Source)
	}
	return next
}
