package admin

import (
	"encoding/json"

	"github.com/AutoCONFIG/uapi/internal/config"
	"github.com/valyala/fasthttp"
)

type AdminSettingsResponse struct {
	LogRetentionDays        int `json:"log_retention_days"`
	RedeemCodeRetentionDays int `json:"redeem_code_retention_days"`
}

type UpdateAdminSettingsRequest struct {
	LogRetentionDays        *int `json:"log_retention_days"`
	RedeemCodeRetentionDays *int `json:"redeem_code_retention_days"`
}

func (h *Handler) HandleSettings(ctx *fasthttp.RequestCtx) {
	switch string(ctx.Method()) {
	case "GET":
		h.jsonResponse(ctx, 200, h.settingsResponse())
	case "PUT":
		var req UpdateAdminSettingsRequest
		if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
			return
		}
		changes := map[string]interface{}{}
		if req.LogRetentionDays != nil {
			if *req.LogRetentionDays <= 0 {
				h.jsonError(ctx, fasthttp.StatusBadRequest, "log_retention_days must be greater than 0")
				return
			}
			h.cfg.Logging.RetentionDays = *req.LogRetentionDays
			changes["log_retention_days"] = *req.LogRetentionDays
		}
		if req.RedeemCodeRetentionDays != nil {
			if *req.RedeemCodeRetentionDays <= 0 {
				h.jsonError(ctx, fasthttp.StatusBadRequest, "redeem_code_retention_days must be greater than 0")
				return
			}
			h.cfg.Logging.RedeemCodeRetentionDays = *req.RedeemCodeRetentionDays
			changes["redeem_code_retention_days"] = *req.RedeemCodeRetentionDays
		}
		if len(changes) == 0 {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "no fields to update")
			return
		}
		if err := config.Save(h.cfg, h.cfgPath); err != nil {
			h.jsonError(ctx, fasthttp.StatusInternalServerError, "save settings failed")
			return
		}
		createAuditLogWithValues(h.db, "update", "settings", "logging", h.getAdminUser(ctx), auditIP(ctx), "", auditJSON(changes))
		h.jsonResponse(ctx, 200, h.settingsResponse())
	default:
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) settingsResponse() AdminSettingsResponse {
	return AdminSettingsResponse{
		LogRetentionDays:        h.cfg.Logging.RetentionDays,
		RedeemCodeRetentionDays: h.cfg.Logging.RedeemCodeRetentionDays,
	}
}
