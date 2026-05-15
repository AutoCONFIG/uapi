package auth

import (
	"encoding/json"
	"strings"

	"github.com/valyala/fasthttp"
)

// RequireUser returns a middleware that requires a valid user JWT.
func RequireUser(secret string) func(fasthttp.RequestHandler) fasthttp.RequestHandler {
	return requireToken(secret, TokenTypeUser)
}

func requireToken(secret string, expectedType TokenType) func(fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			authHeader := string(ctx.Request.Header.Peek("Authorization"))
			if !strings.HasPrefix(authHeader, "Bearer ") {
				writeUnauthorized(ctx, "missing authorization header")
				return
			}
			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
			claims, err := ParseToken(secret, tokenStr)
			if err != nil {
				writeUnauthorized(ctx, err.Error())
				return
			}
			if claims.Type != expectedType {
				writeUnauthorized(ctx, "invalid token type")
				return
			}
			ctx.SetUserValue("claims", claims)
			next(ctx)
		}
	}
}

func writeUnauthorized(ctx *fasthttp.RequestCtx, msg string) {
	ctx.SetStatusCode(401)
	ctx.SetContentType("application/json")
	json.NewEncoder(ctx).Encode(map[string]interface{}{
		"code":    401,
		"message": msg,
	})
}
