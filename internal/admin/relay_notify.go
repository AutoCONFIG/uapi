package admin

import (
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/valyala/fasthttp"
)

func (h *Handler) notifyRelayReloadAsync(nodeID string) {
	go h.notifyRelayReload(nodeID)
}

func (h *Handler) notifyAllRelaysReloadAsync() {
	go func() {
		var nodes []db.RelayNode
		if err := h.db.Where("deleted_at IS NULL AND status = ?", RelayNodeStatusActive).Find(&nodes).Error; err != nil {
			logger.Warnf("relay.reload", "load relay nodes failed", logger.Err(err))
			return
		}
		for _, node := range nodes {
			h.notifyRelayReload(node.ID.String())
		}
	}()
}

func (h *Handler) notifyRelayReload(nodeID string) {
	if h.cfg == nil || h.cfg.Gateway.InternalSecret == "" {
		return
	}
	var node db.RelayNode
	if err := h.db.Where("id = ? AND deleted_at IS NULL", nodeID).First(&node).Error; err != nil {
		return
	}
	baseURL := strings.TrimRight(strings.TrimSpace(node.BaseURL), "/")
	if baseURL == "" {
		return
	}
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)
	req.SetRequestURI(baseURL + "/internal/reload")
	req.Header.SetMethod(fasthttp.MethodPost)
	req.Header.Set("X-UAPI-Internal-Secret", h.cfg.Gateway.InternalSecret)
	if err := fasthttp.DoTimeout(req, resp, 10*time.Second); err != nil {
		logger.Warnf("relay.reload", "notify relay reload failed", logger.F("node_id", nodeID), logger.Err(err))
		return
	}
	if resp.StatusCode() >= 300 {
		logger.Warnf("relay.reload", "notify relay reload rejected", logger.F("node_id", nodeID), logger.F("status", resp.StatusCode()))
	}
}
