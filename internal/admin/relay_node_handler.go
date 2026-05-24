package admin

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

const (
	RelayNodeStatusActive   = "active"
	RelayNodeStatusDraining = "draining"
	RelayNodeStatusDisabled = "disabled"

	RelayNodeHealthHealthy   = "healthy"
	RelayNodeHealthUnhealthy = "unhealthy"
	RelayNodeHealthOffline   = "offline"
)

// HandleRelayNodes routes relay node CRUD operations by HTTP method.
func (h *Handler) HandleRelayNodes(ctx *fasthttp.RequestCtx) {
	switch string(ctx.Method()) {
	case "GET":
		h.listRelayNodes(ctx)
	case "POST":
		h.createRelayNode(ctx)
	case "PUT":
		h.updateRelayNode(ctx)
	case "DELETE":
		h.deleteRelayNode(ctx)
	default:
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) listRelayNodes(ctx *fasthttp.RequestCtx) {
	page, limit := h.parsePagination(ctx)
	offset := (page - 1) * limit
	var total int64
	var items []db.RelayNode
	h.db.Model(&db.RelayNode{}).Where("deleted_at IS NULL").Count(&total)
	h.db.Where("deleted_at IS NULL").Order("created_at desc").Limit(limit).Offset(offset).Find(&items)
	h.jsonResponse(ctx, 200, PaginatedResponse{Total: total, Page: page, Limit: limit, Items: items})
}

func (h *Handler) createRelayNode(ctx *fasthttp.RequestCtx) {
	var req CreateRelayNodeRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	if errMsg := validateRelayNodeCreate(req); errMsg != "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, errMsg)
		return
	}
	status := req.Status
	if status == "" {
		status = RelayNodeStatusDisabled
	}
	health := req.HealthStatus
	if health == "" {
		health = RelayNodeHealthHealthy
	}
	node := db.RelayNode{
		Name:           strings.TrimSpace(req.Name),
		BaseURL:        strings.TrimRight(strings.TrimSpace(req.BaseURL), "/"),
		Region:         strings.TrimSpace(req.Region),
		EgressIP:       strings.TrimSpace(req.EgressIP),
		Weight:         req.Weight,
		MaxConcurrency: req.MaxConcurrency,
		Status:         status,
		HealthStatus:   health,
	}
	node.ID = uuid.New()
	if err := h.db.Create(&node).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "create failed")
		return
	}
	auditCreate(h.db, "relay_node", node.ID, h.getAdminUser(ctx))
	h.jsonResponse(ctx, 200, node)
}

func (h *Handler) updateRelayNode(ctx *fasthttp.RequestCtx) {
	id, err := uuid.Parse(string(ctx.QueryArgs().Peek("id")))
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	var req UpdateRelayNodeRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	var existing db.RelayNode
	if err := h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "not found")
		return
	}
	updates := map[string]interface{}{}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "name is required")
			return
		}
		updates["name"] = name
	}
	if req.BaseURL != nil {
		baseURL := strings.TrimRight(strings.TrimSpace(*req.BaseURL), "/")
		if !validRelayNodeURL(baseURL) {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "base_url must be a valid http or https URL")
			return
		}
		updates["base_url"] = baseURL
	}
	if req.Region != nil {
		updates["region"] = strings.TrimSpace(*req.Region)
	}
	if req.EgressIP != nil {
		updates["egress_ip"] = strings.TrimSpace(*req.EgressIP)
	}
	if req.Weight != nil {
		if *req.Weight < 0 || *req.Weight > 10000 {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "weight must be between 0 and 10000")
			return
		}
		updates["weight"] = *req.Weight
	}
	if req.MaxConcurrency != nil {
		if *req.MaxConcurrency < 0 || *req.MaxConcurrency > 100000 {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "max_concurrency must be between 0 and 100000")
			return
		}
		updates["max_concurrency"] = *req.MaxConcurrency
	}
	if req.Status != nil {
		if !validRelayNodeStatus(*req.Status) {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid status")
			return
		}
		updates["status"] = *req.Status
	}
	if req.HealthStatus != nil {
		if !validRelayNodeHealth(*req.HealthStatus) {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid health_status")
			return
		}
		updates["health_status"] = *req.HealthStatus
	}
	updates["updated_at"] = time.Now()
	if err := h.db.Model(&existing).Updates(updates).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "update failed")
		return
	}
	if err := h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "reload failed")
		return
	}
	auditUpdate(h.db, "relay_node", id, h.getAdminUser(ctx))
	h.jsonResponse(ctx, 200, existing)
}

func (h *Handler) deleteRelayNode(ctx *fasthttp.RequestCtx) {
	id, err := uuid.Parse(string(ctx.QueryArgs().Peek("id")))
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	now := time.Now()
	result := h.db.Model(&db.RelayNode{}).Where("id = ? AND deleted_at IS NULL", id).Updates(map[string]interface{}{
		"status":     RelayNodeStatusDisabled,
		"deleted_at": now,
	})
	if result.Error != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "delete failed")
		return
	}
	if result.RowsAffected == 0 {
		h.jsonError(ctx, fasthttp.StatusNotFound, "not found")
		return
	}
	auditDelete(h.db, "relay_node", id, h.getAdminUser(ctx))
	h.jsonResponse(ctx, 200, map[string]interface{}{"deleted": true})
}

func validateRelayNodeCreate(req CreateRelayNodeRequest) string {
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.BaseURL) == "" {
		return "name and base_url are required"
	}
	if !validRelayNodeURL(strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")) {
		return "base_url must be a valid http or https URL"
	}
	if req.Weight < 0 || req.Weight > 10000 {
		return "weight must be between 0 and 10000"
	}
	if req.MaxConcurrency < 0 || req.MaxConcurrency > 100000 {
		return "max_concurrency must be between 0 and 100000"
	}
	if req.Status != "" && !validRelayNodeStatus(req.Status) {
		return "invalid status"
	}
	if req.HealthStatus != "" && !validRelayNodeHealth(req.HealthStatus) {
		return "invalid health_status"
	}
	return ""
}

func validRelayNodeURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
}

func validRelayNodeStatus(status string) bool {
	switch status {
	case RelayNodeStatusActive, RelayNodeStatusDraining, RelayNodeStatusDisabled:
		return true
	default:
		return false
	}
}

func validRelayNodeHealth(status string) bool {
	switch status {
	case RelayNodeHealthHealthy, RelayNodeHealthUnhealthy, RelayNodeHealthOffline:
		return true
	default:
		return false
	}
}
