package httputil

import (
	"encoding/json"
	"strings"

	"github.com/valyala/fasthttp"
)

// ExtractBearerToken returns the bearer token from Authorization header,
// x-api-key header, X-Goog-Api-Key header, or the "key" query parameter.
func ExtractBearerToken(ctx *fasthttp.RequestCtx, allowQueryKey bool) string {
	auth := string(ctx.Request.Header.Peek("Authorization"))
	if len(auth) > 7 && auth[:7] == "Bearer " {
		return auth[7:]
	}
	if key := strings.TrimSpace(string(ctx.Request.Header.Peek("x-api-key"))); key != "" {
		return key
	}
	if key := strings.TrimSpace(string(ctx.Request.Header.Peek("X-Goog-Api-Key"))); key != "" {
		return key
	}
	if allowQueryKey {
		if key := strings.TrimSpace(string(ctx.QueryArgs().Peek("key"))); key != "" {
			return key
		}
	}
	return ""
}

// ModelFromRequestPath returns the model name from the request body or
// extracts it from a /v1beta/models/<model> path.
func ModelFromRequestPath(path, bodyModel string) string {
	if strings.TrimSpace(bodyModel) != "" {
		return bodyModel
	}
	const prefix = "/v1beta/models/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(path, prefix)
	if rest == "" {
		return ""
	}
	if idx := strings.Index(rest, ":"); idx >= 0 {
		rest = rest[:idx]
	}
	if idx := strings.Index(rest, "/"); idx >= 0 {
		rest = rest[:idx]
	}
	return strings.TrimSpace(rest)
}

// ModelFromImageRequest extracts the model name from an image generation request.
func ModelFromImageRequest(ctx *fasthttp.RequestCtx) string {
	return ModelFromBodyOrForm(ctx)
}

// ModelFromBodyOrForm extracts a model from either a JSON body or multipart/form data.
func ModelFromBodyOrForm(ctx *fasthttp.RequestCtx) string {
	var body struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(ctx.PostBody(), &body) == nil && strings.TrimSpace(body.Model) != "" {
		return strings.TrimSpace(body.Model)
	}
	if model := strings.TrimSpace(string(ctx.FormValue("model"))); model != "" {
		return model
	}
	return ""
}

// CheckIPWhitelist verifies the client IP against the whitelist.
// Only trusts X-Forwarded-For / X-Real-IP when the direct connection IP
// matches a configured trusted proxy.
func CheckIPWhitelist(ctx *fasthttp.RequestCtx, whitelist string, trustedProxies []string) bool {
	for _, allowedIP := range strings.Split(whitelist, ",") {
		allowedIP = strings.TrimSpace(allowedIP)
		if allowedIP == "" {
			continue
		}
		for _, clientIP := range ClientIPCandidates(ctx, trustedProxies) {
			if allowedIP == clientIP {
				return true
			}
		}
	}
	return false
}

// ClientIPCandidates returns candidate client IPs for whitelist matching.
// Forwarded headers are only considered when the direct connection IP is a trusted proxy.
func ClientIPCandidates(ctx *fasthttp.RequestCtx, trustedProxies []string) []string {
	remoteIP := ctx.RemoteIP().String()
	candidates := []string{remoteIP}

	if !IsTrustedProxy(remoteIP, trustedProxies) {
		return candidates
	}

	if xRealIP := strings.TrimSpace(string(ctx.Request.Header.Peek("X-Real-IP"))); xRealIP != "" {
		candidates = append(candidates, xRealIP)
	}
	if forwardedFor := strings.TrimSpace(string(ctx.Request.Header.Peek("X-Forwarded-For"))); forwardedFor != "" {
		firstHop := strings.TrimSpace(strings.Split(forwardedFor, ",")[0])
		if firstHop != "" {
			candidates = append(candidates, firstHop)
		}
	}
	return candidates
}

// ClientIPForLog returns the best guess at the real client IP for logging.
func ClientIPForLog(ctx *fasthttp.RequestCtx, trustedProxies []string) string {
	candidates := ClientIPCandidates(ctx, trustedProxies)
	if len(candidates) == 0 {
		return ""
	}
	return candidates[len(candidates)-1]
}

// ClientIPForGatewayLog returns the client IP that Gateway should stamp into
// signed relay claims. Whitelist checks remain stricter via ClientIPCandidates;
// this function is logging-oriented so Docker/nginx deployments still show the
// original requester even when trusted_proxies has not been configured yet.
func ClientIPForGatewayLog(ctx *fasthttp.RequestCtx, trustedProxies []string) string {
	if ip := ClientIPForLog(ctx, trustedProxies); ip != "" && (ip != ctx.RemoteIP().String() || len(trustedProxies) > 0) {
		return ip
	}
	if forwardedFor := strings.TrimSpace(string(ctx.Request.Header.Peek("X-Forwarded-For"))); forwardedFor != "" {
		firstHop := strings.TrimSpace(strings.Split(forwardedFor, ",")[0])
		if firstHop != "" {
			return firstHop
		}
	}
	if xRealIP := strings.TrimSpace(string(ctx.Request.Header.Peek("X-Real-IP"))); xRealIP != "" {
		return xRealIP
	}
	return ctx.RemoteIP().String()
}

// IsTrustedProxy checks if the given IP is in the trusted proxies list.
func IsTrustedProxy(ip string, trustedProxies []string) bool {
	for _, trusted := range trustedProxies {
		if strings.TrimSpace(trusted) == ip {
			return true
		}
	}
	return false
}

// CSVList splits a comma-separated string into trimmed, non-empty items.
func CSVList(list string) []string {
	items := strings.Split(list, ",")
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

// CSVSet returns a set from a comma-separated string.
func CSVSet(list string) map[string]struct{} {
	items := CSVList(list)
	if len(items) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(items))
	for _, item := range items {
		set[item] = struct{}{}
	}
	return set
}

// ModelInList checks if a model name is in a comma-separated list.
func ModelInList(model, list string) bool {
	for _, m := range CSVList(list) {
		if m == model {
			return true
		}
	}
	return false
}

// PermissionInList checks if a permission string is in a comma-separated list.
func PermissionInList(permission, list string) bool {
	for _, item := range CSVList(list) {
		if strings.TrimSpace(item) == permission {
			return true
		}
	}
	return false
}

// AnyPermissionInList checks if any of the given permissions are in a comma-separated list.
func AnyPermissionInList(list string, permissions ...string) bool {
	set := CSVSet(list)
	for _, permission := range permissions {
		if _, ok := set[permission]; ok {
			return true
		}
	}
	return false
}

// JSONEscape returns a JSON-escaped version of the string without surrounding quotes.
func JSONEscape(s string) string {
	b, _ := json.Marshal(s)
	if len(b) >= 2 {
		return string(b[1 : len(b)-1])
	}
	return s
}
