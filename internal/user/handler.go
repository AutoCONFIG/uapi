package user

import (
	"encoding/json"
	"strconv"

	"github.com/AutoCONFIG/uapi/internal/auth"
	"github.com/valyala/fasthttp"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Register(ctx *fasthttp.RequestCtx) {
	var req RegisterRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		sendError(ctx, 400, "invalid request body")
		return
	}
	if req.Email == "" || req.Username == "" || req.Password == "" {
		sendError(ctx, 400, "email, username and password are required")
		return
	}

	resp, err := h.service.Register(&req)
	if err != nil {
		sendError(ctx, 400, err.Error())
		return
	}
	sendSuccess(ctx, resp)
}

func (h *Handler) Login(ctx *fasthttp.RequestCtx) {
	var req LoginRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		sendError(ctx, 400, "invalid request body")
		return
	}

	resp, err := h.service.Login(&req)
	if err != nil {
		sendError(ctx, 401, err.Error())
		return
	}
	sendSuccess(ctx, resp)
}

func (h *Handler) RefreshToken(ctx *fasthttp.RequestCtx) {
	var req RefreshRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		sendError(ctx, 400, "invalid request body")
		return
	}

	resp, err := h.service.RefreshToken(req.RefreshToken)
	if err != nil {
		sendError(ctx, 401, err.Error())
		return
	}
	sendSuccess(ctx, resp)
}

func (h *Handler) GetProfile(ctx *fasthttp.RequestCtx) {
	userID := getUserID(ctx)
	if userID == "" {
		sendError(ctx, 401, "unauthorized")
		return
	}

	resp, err := h.service.GetProfile(userID)
	if err != nil {
		sendError(ctx, 404, err.Error())
		return
	}
	sendSuccess(ctx, resp)
}

func (h *Handler) UpdatePassword(ctx *fasthttp.RequestCtx) {
	userID := getUserID(ctx)
	if userID == "" {
		sendError(ctx, 401, "unauthorized")
		return
	}

	var req UpdatePasswordRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		sendError(ctx, 400, "invalid request body")
		return
	}

	if err := h.service.UpdatePassword(userID, &req); err != nil {
		sendError(ctx, 400, err.Error())
		return
	}
	sendSuccess(ctx, nil)
}

func (h *Handler) UpdateEmail(ctx *fasthttp.RequestCtx) {
	userID := getUserID(ctx)
	if userID == "" {
		sendError(ctx, 401, "unauthorized")
		return
	}

	var req UpdateEmailRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		sendError(ctx, 400, "invalid request body")
		return
	}

	if err := h.service.UpdateEmail(userID, &req); err != nil {
		sendError(ctx, 400, err.Error())
		return
	}
	sendSuccess(ctx, nil)
}

func (h *Handler) ListKeys(ctx *fasthttp.RequestCtx) {
	userID := getUserID(ctx)
	if userID == "" {
		sendError(ctx, 401, "unauthorized")
		return
	}

	keys, err := h.service.ListKeys(userID)
	if err != nil {
		sendError(ctx, 500, err.Error())
		return
	}
	sendSuccess(ctx, keys)
}

func (h *Handler) CreateKey(ctx *fasthttp.RequestCtx) {
	userID := getUserID(ctx)
	if userID == "" {
		sendError(ctx, 401, "unauthorized")
		return
	}

	var req CreateKeyRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		sendError(ctx, 400, "invalid request body")
		return
	}

	resp, err := h.service.CreateKey(userID, &req)
	if err != nil {
		sendError(ctx, 500, err.Error())
		return
	}
	sendSuccess(ctx, resp)
}

func (h *Handler) DeleteKey(ctx *fasthttp.RequestCtx) {
	userID := getUserID(ctx)
	if userID == "" {
		sendError(ctx, 401, "unauthorized")
		return
	}

	keyID, ok := ctx.UserValue("keyID").(string)
	if !ok || keyID == "" {
		sendError(ctx, 400, "missing key ID")
		return
	}
	if err := h.service.DeleteKey(userID, keyID); err != nil {
		sendError(ctx, 404, err.Error())
		return
	}
	sendSuccess(ctx, nil)
}

func (h *Handler) GetUsage(ctx *fasthttp.RequestCtx) {
	userID := getUserID(ctx)
	if userID == "" {
		sendError(ctx, 401, "unauthorized")
		return
	}

	usage, err := h.service.GetUsage(userID)
	if err != nil {
		sendError(ctx, 500, err.Error())
		return
	}
	sendSuccess(ctx, usage)
}

func (h *Handler) GetUsageLogs(ctx *fasthttp.RequestCtx) {
	userID := getUserID(ctx)
	if userID == "" {
		sendError(ctx, 401, "unauthorized")
		return
	}

	pageStr := string(ctx.QueryArgs().Peek("page"))
	limitStr := string(ctx.QueryArgs().Peek("limit"))
	page, _ := strconv.Atoi(pageStr)
	limit, _ := strconv.Atoi(limitStr)
	if page == 0 {
		page = 1
	}
	if limit > 100 {
		limit = 100
	}
	if limit < 1 {
		limit = 20
	}

	logs, err := h.service.GetUsageLogs(userID, page, limit)
	if err != nil {
		sendError(ctx, 500, err.Error())
		return
	}
	sendSuccess(ctx, logs)
}

func (h *Handler) GetSubscription(ctx *fasthttp.RequestCtx) {
	userID := getUserID(ctx)
	if userID == "" {
		sendError(ctx, 401, "unauthorized")
		return
	}

	sub, err := h.service.GetSubscription(userID)
	if err != nil {
		sendError(ctx, 404, err.Error())
		return
	}
	sendSuccess(ctx, sub)
}

func (h *Handler) Subscribe(ctx *fasthttp.RequestCtx) {
	userID := getUserID(ctx)
	if userID == "" {
		sendError(ctx, 401, "unauthorized")
		return
	}

	planID, ok := ctx.UserValue("planID").(string)
	if !ok || planID == "" {
		sendError(ctx, 400, "missing plan ID")
		return
	}
	if err := h.service.Subscribe(userID, planID); err != nil {
		sendError(ctx, 400, err.Error())
		return
	}
	sendSuccess(ctx, nil)
}

func (h *Handler) RedeemCode(ctx *fasthttp.RequestCtx) {
	userID := getUserID(ctx)
	if userID == "" {
		sendError(ctx, 401, "unauthorized")
		return
	}

	var req RedeemRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		sendError(ctx, 400, "invalid request body")
		return
	}

	value, err := h.service.RedeemCode(userID, req.Code)
	if err != nil {
		sendError(ctx, 400, err.Error())
		return
	}
	sendSuccess(ctx, map[string]interface{}{"value": value})
}

func (h *Handler) ListPlans(ctx *fasthttp.RequestCtx) {
	plans, err := h.service.ListPlans()
	if err != nil {
		sendError(ctx, 500, err.Error())
		return
	}
	sendSuccess(ctx, plans)
}

// getUserID extracts the user ID from JWT claims set in the context
func getUserID(ctx *fasthttp.RequestCtx) string {
	v := ctx.UserValue("claims")
	if v == nil {
		return ""
	}
	if claims, ok := v.(*auth.Claims); ok {
		return claims.UserID
	}
	return ""
}

func sendSuccess(ctx *fasthttp.RequestCtx, data interface{}) {
	resp := map[string]interface{}{
		"code":    0,
		"message": "ok",
	}
	if data != nil {
		resp["data"] = data
	}
	body, _ := json.Marshal(resp)
	ctx.SetContentType("application/json")
	ctx.SetBody(body)
}

func sendError(ctx *fasthttp.RequestCtx, code int, message string) {
	resp := map[string]interface{}{
		"code":    code,
		"message": message,
	}
	body, _ := json.Marshal(resp)
	ctx.SetContentType("application/json")
	ctx.SetStatusCode(code)
	ctx.SetBody(body)
}
