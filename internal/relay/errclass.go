package relay

import (
	"bytes"
	"strings"

	"github.com/valyala/fasthttp"
)

// ErrorClass 表示上游错误的语义分类，决定 failover 走哪条路径。
type ErrorClass int

const (
	// ErrUnknown 表示无法分类的错误，按保守策略（账号侧）处理。
	ErrUnknown ErrorClass = iota
	// ErrServerSide 表示上游服务器/基础设施问题（408/5xx/网络层）。
	// 处理：本次请求跳过该 channel，账号状态不变。
	ErrServerSide
	// ErrAccountSide 表示账号侧问题（凭据/配额/权限）但可恢复（401/402/403/429）。
	// 处理：cooldown 该账号一段时间后自动恢复。
	ErrAccountSide
	// ErrAccountTerminal 表示账号已死，无法自动恢复（token revoked / 账号封禁等）。
	// 处理：永久 disable 账号，需管理员手动重新启用。
	ErrAccountTerminal
	// ErrConfigSide 表示渠道配置问题（404 + model 关键词），(channel, model) 维度不可用。
	// 处理：标记 (channel, model) blocklist 短期内不参与候选。
	ErrConfigSide
	// ErrClientSide 表示用户请求本身错误（400/422 等），直接返回客户端不重试。
	ErrClientSide
)

func (c ErrorClass) String() string {
	switch c {
	case ErrServerSide:
		return "server_side"
	case ErrAccountSide:
		return "account_side"
	case ErrAccountTerminal:
		return "account_terminal"
	case ErrConfigSide:
		return "config_side"
	case ErrClientSide:
		return "client_side"
	default:
		return "unknown"
	}
}

// 关键词集合 — 用于响应体内容辅助判定。
var (
	// authKeywords 用于辅助确认 401 是账号凭据问题（而非 upstream 鉴权挂掉）。
	authKeywords = [][]byte{
		[]byte("unauthorized"),
		[]byte("unauthenticated"),
		[]byte("invalid_api_key"),
		[]byte("invalid_key"),
		[]byte("invalid token"),
		[]byte("invalid api key"),
		[]byte("expired"),
		[]byte("authentication"),
		[]byte("auth_error"),
		[]byte("api key"),
		[]byte("bearer"),
	}

	// quotaKeywords 用于判定 402/429 是否是配额相关。
	quotaKeywords = [][]byte{
		[]byte("quota"),
		[]byte("rate_limit"),
		[]byte("rate limit"),
		[]byte("insufficient_quota"),
		[]byte("billing"),
		[]byte("too many requests"),
	}

	// modelKeywords 用于判定 404 是否是模型不存在。
	// 必须含 "model" 关键词，避免把 "route not found" 等误判为模型问题。
	modelKeywords = [][]byte{
		[]byte("model"),
	}

	// terminalKeywords 表示账号已死的强信号关键词。
	// 命中任一即升级为 ErrAccountTerminal。
	terminalKeywords = [][]byte{
		// OAuth/Token 永久失效
		[]byte("invalid_grant"),
		[]byte("token_revoked"),
		[]byte("token revoked"),
		[]byte("refresh_token_expired"),
		[]byte("refresh token expired"),
		[]byte("refresh_token_invalid"),
		[]byte("refresh token invalid"),
		// 账号封禁/停用
		[]byte("account_disabled"),
		[]byte("account disabled"),
		[]byte("account_suspended"),
		[]byte("account suspended"),
		[]byte("account_terminated"),
		[]byte("account terminated"),
		[]byte("account_banned"),
		[]byte("account banned"),
		// 凭据彻底无效
		[]byte("api_key_revoked"),
		[]byte("api key revoked"),
		[]byte("credential_invalid"),
		[]byte("credential invalid"),
		[]byte("credentials_invalid"),
		[]byte("invalid api key"),
		[]byte("incorrect api key"),
		[]byte("api key is invalid"),
		[]byte("apikey不存在"),
		[]byte("api key不存在"),
		[]byte("api_key不存在"),
		[]byte("api key 不存在"),
		[]byte("apikey 不存在"),
		[]byte("api key配置错误"),
		[]byte("apikey配置错误"),
		[]byte("api_key配置错误"),
		[]byte("凭据无效"),
		[]byte("认证过期"),
		[]byte("认证已过期"),
		[]byte("认证失败"),
		// 组织/账户级永久拒绝
		[]byte("organization has been disabled"),
		[]byte("organization_disabled"),
		[]byte("account is not authorized"),
		[]byte("account has no access"),
	}
)

// ClassifyUpstreamError 根据状态码 + 响应体内容分类错误。
// body 可以为 nil（仅按状态码判定）。
func ClassifyUpstreamError(statusCode int, body []byte) ErrorClass {
	// 优先判定终态认证错误（最高优先级）
	if IsTerminalAuthError(statusCode, body) {
		return ErrAccountTerminal
	}

	bodyLower := lowerBody(body)

	switch {
	case statusCode == fasthttp.StatusRequestTimeout, // 408
		statusCode >= 500 && statusCode <= 599:
		return ErrServerSide

	case statusCode == fasthttp.StatusUnauthorized: // 401
		// 默认账号侧，但若响应体没有 auth 关键词，可能是 upstream 鉴权挂了
		if len(bodyLower) > 0 && !containsAny(bodyLower, authKeywords) {
			return ErrServerSide
		}
		return ErrAccountSide

	case statusCode == fasthttp.StatusPaymentRequired, // 402
		statusCode == fasthttp.StatusTooManyRequests: // 429
		return ErrAccountSide

	case statusCode == fasthttp.StatusForbidden: // 403
		return ErrAccountSide

	case statusCode == fasthttp.StatusNotFound: // 404
		if len(bodyLower) > 0 && containsAny(bodyLower, modelKeywords) {
			return ErrConfigSide
		}
		return ErrClientSide

	case statusCode == fasthttp.StatusBadRequest, // 400
		statusCode == fasthttp.StatusUnprocessableEntity: // 422
		return ErrClientSide

	default:
		// 其他 4xx 按客户端侧处理
		if statusCode >= 400 && statusCode < 500 {
			return ErrClientSide
		}
		return ErrUnknown
	}
}

// IsTerminalAuthError 判定是否是"账号已死"的终态错误。
// 同时复用 account_disable.go 中的现有终态判定逻辑。
func IsTerminalAuthError(statusCode int, body []byte) bool {
	// 终态关键词直接命中
	bodyLower := lowerBody(body)
	if len(bodyLower) > 0 && containsAny(bodyLower, terminalKeywords) {
		return true
	}

	// 复用 account_disable.go 中更细致的字段解析。401 只有明确终态信号时
	// 才会返回 true，普通账号侧 401 仍走冷却/跳过路径。
	if statusCode == fasthttp.StatusForbidden {
		if _, terminal := terminalAccountDisableReason(statusCode, body); terminal {
			return true
		}
	}
	// 字段级强信号：is_forbidden / PERMISSION_DENIED / invalid_api_key
	// 这些已在 terminalAccountDisableReason 中处理，使用一个特殊探针调用
	if len(body) > 0 && hasStructuredTerminalSignal(body) {
		return true
	}

	return false
}

// hasStructuredTerminalSignal 检查响应体中是否有结构化的终态字段。
// 利用 account_disable.go 中已有的 collectErrorFields/boolField/stringField。
func hasStructuredTerminalSignal(body []byte) bool {
	fields := collectErrorFields(body)
	if len(fields) == 0 {
		return false
	}
	// is_forbidden 等强字段
	if boolField(fields, "is_forbidden") ||
		boolField(fields, "_forbidden") ||
		boolField(fields, "quota.is_forbidden") {
		return true
	}
	// PERMISSION_DENIED 等状态字段
	status := strings.ToUpper(firstNonEmptyDisableString(
		stringField(fields, "error.status"),
		stringField(fields, "status"),
	))
	if status == "PERMISSION_DENIED" {
		return true
	}
	// invalid_api_key 等强 code
	code := strings.ToLower(firstNonEmptyDisableString(
		stringField(fields, "error.code"),
		stringField(fields, "code"),
		stringField(fields, "error.type"),
		stringField(fields, "type"),
	))
	if strings.Contains(code, "invalid_api_key") ||
		strings.Contains(code, "invalid_key") ||
		strings.Contains(code, "api_key_revoked") ||
		strings.Contains(code, "token_revoked") ||
		strings.Contains(code, "invalid_grant") {
		return true
	}
	return false
}

// IsQuotaError 判定响应体是否含配额相关关键词。
// 用于细分 429/402 的语义（保持与现有 isUpstreamQuotaExhausted 兼容）。
func IsQuotaError(body []byte) bool {
	bodyLower := lowerBody(body)
	if len(bodyLower) == 0 {
		return false
	}
	return containsAny(bodyLower, quotaKeywords)
}

func containsAny(b []byte, keywords [][]byte) bool {
	for _, kw := range keywords {
		if bytes.Contains(b, kw) {
			return true
		}
	}
	return false
}

// lowerBody 返回 body 的小写副本。body 为空时返回 nil。
// 限制处理长度，避免巨大响应体的开销（一般错误响应很短）。
func lowerBody(body []byte) []byte {
	if len(body) == 0 {
		return nil
	}
	const maxScan = 16 * 1024 // 16KB 足以覆盖绝大多数错误响应
	src := body
	if len(src) > maxScan {
		src = src[:maxScan]
	}
	return bytes.ToLower(src)
}
