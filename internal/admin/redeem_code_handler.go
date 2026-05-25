package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

type CreateRedeemCodeRequest struct {
	Code      string    `json:"code"`
	PlanID    uuid.UUID `json:"plan_id"`
	ExpiresAt string    `json:"expires_at"`
}

func (h *Handler) HandleRedeemCodes(ctx *fasthttp.RequestCtx) {
	switch string(ctx.Method()) {
	case "GET":
		h.listRedeemCodes(ctx)
	case "POST":
		h.createRedeemCode(ctx)
	case "DELETE":
		h.deleteRedeemCode(ctx)
	default:
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) listRedeemCodes(ctx *fasthttp.RequestCtx) {
	page, limit := h.parsePagination(ctx)
	offset := (page - 1) * limit
	status := strings.TrimSpace(string(ctx.QueryArgs().Peek("status")))
	now := time.Now()
	h.db.Model(&db.RedeemCode{}).Where("status = ? AND expires_at <= ?", "active", now).Update("status", "expired")
	query := h.db.Model(&db.RedeemCode{})
	if status != "" && status != "all" {
		query = query.Where("status = ?", status)
	}
	var total int64
	var items []db.RedeemCode
	query.Count(&total)
	query.Order("created_at desc").Limit(limit).Offset(offset).Find(&items)
	if items == nil {
		items = []db.RedeemCode{}
	}
	h.jsonResponse(ctx, 200, PaginatedResponse{Total: total, Page: page, Limit: limit, Items: items})
}

func (h *Handler) createRedeemCode(ctx *fasthttp.RequestCtx) {
	var req CreateRedeemCodeRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	if req.PlanID == uuid.Nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "plan_id is required")
		return
	}
	var plan db.Plan
	if err := h.db.Where("id = ? AND enabled = true AND deleted_at IS NULL", req.PlanID).First(&plan).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "plan not found")
		return
	}
	expiresAt := time.Now().AddDate(0, 1, 0)
	if strings.TrimSpace(req.ExpiresAt) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(req.ExpiresAt))
		if err != nil || !parsed.After(time.Now()) {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "expires_at must be a future RFC3339 time")
			return
		}
		expiresAt = parsed
	}
	code := strings.TrimSpace(req.Code)
	if code == "" {
		code = randomRedeemCode()
	}
	item := db.RedeemCode{Code: code, PlanID: req.PlanID, Value: plan.TokenQuota, Status: "active", ExpiresAt: expiresAt}
	item.ID = uuid.New()
	if err := h.db.Create(&item).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "create failed")
		return
	}
	auditCreateCtx(h.db, "redeem_code", item.ID, h.getAdminUser(ctx), ctx, map[string]interface{}{"code": item.Code, "plan_id": item.PlanID, "plan_name": plan.Name, "expires_at": item.ExpiresAt})
	h.jsonResponse(ctx, 200, item)
}

func (h *Handler) deleteRedeemCode(ctx *fasthttp.RequestCtx) {
	id, err := uuid.Parse(string(ctx.QueryArgs().Peek("id")))
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.Delete(&db.RedeemCode{}, "id = ?", id).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "delete failed")
		return
	}
	auditDeleteCtx(h.db, "redeem_code", id, h.getAdminUser(ctx), ctx, nil)
	h.jsonResponse(ctx, 200, map[string]interface{}{"deleted": true})
}

func randomRedeemCode() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return "UAPI-" + strings.ToUpper(hex.EncodeToString(buf))
}
