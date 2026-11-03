package admin

import (
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// SetOutboundLocaleService attaches the per-proxy exit location service.
func (h *ProxyHandler) SetOutboundLocaleService(svc *service.OpenAIOutboundLocaleService) {
	h.outboundLocale = svc
}

type proxyExitLocationResponse struct {
	ProxyID      int64      `json:"proxy_id"`
	Current      bool       `json:"current"`
	ExitIP       string     `json:"exit_ip"`
	CountryCode  string     `json:"country_code"`
	Region       string     `json:"region"`
	City         string     `json:"city"`
	Timezone     string     `json:"timezone"`
	Source       string     `json:"source"`
	SucceededAt  *time.Time `json:"succeeded_at"`
	AttemptedAt  time.Time  `json:"attempted_at"`
	NextProbeAt  time.Time  `json:"next_probe_at"`
	FailureCount int        `json:"failure_count"`
	LastError    string     `json:"last_error"`
}

func proxyExitLocationToResponse(loc service.ProxyExitLocation, current bool) proxyExitLocationResponse {
	return proxyExitLocationResponse{
		ProxyID:      loc.ProxyID,
		Current:      current,
		ExitIP:       loc.ExitIP,
		CountryCode:  loc.CountryCode,
		Region:       loc.Region,
		City:         loc.City,
		Timezone:     loc.Timezone,
		Source:       loc.Source,
		SucceededAt:  loc.SucceededAt,
		AttemptedAt:  loc.AttemptedAt,
		NextProbeAt:  loc.NextProbeAt,
		FailureCount: loc.FailureCount,
		LastError:    loc.LastError,
	}
}

// ListExitLocations returns the last known egress location of every proxy.
// GET /api/v1/admin/proxies/exit-locations
func (h *ProxyHandler) ListExitLocations(c *gin.Context) {
	views, err := h.outboundLocale.ListExitLocations(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]proxyExitLocationResponse, 0, len(views))
	for _, view := range views {
		out = append(out, proxyExitLocationToResponse(view.ProxyExitLocation, view.Current))
	}
	response.Success(c, out)
}

// RefreshExitLocation probes one proxy's egress location now.
// POST /api/v1/admin/proxies/:id/exit-location/refresh
func (h *ProxyHandler) RefreshExitLocation(c *gin.Context) {
	proxyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid proxy ID")
		return
	}
	loc, probeErr := h.outboundLocale.RefreshProxy(c.Request.Context(), proxyID)
	if loc == nil {
		if probeErr == nil {
			response.InternalError(c, "exit location unavailable")
			return
		}
		response.ErrorFrom(c, probeErr)
		return
	}
	payload := gin.H{"location": proxyExitLocationToResponse(*loc, true)}
	if probeErr != nil {
		payload["error"] = probeErr.Error()
	}
	response.Success(c, payload)
}
