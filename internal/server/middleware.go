package server

import "github.com/valyala/fasthttp"

type Middleware func(next fasthttp.RequestHandler) fasthttp.RequestHandler

func Chain(middlewares ...Middleware) Middleware {
	return func(final fasthttp.RequestHandler) fasthttp.RequestHandler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			final = middlewares[i](final)
		}
		return final
	}
}

func CORSMiddleware() Middleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")
			ctx.Response.Header.Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
			ctx.Response.Header.Set("Access-Control-Allow-Headers", "Content-Type,Authorization")
			if string(ctx.Method()) == "OPTIONS" {
				ctx.SetStatusCode(204)
				return
			}
			next(ctx)
		}
	}
}

func RequestLoggerMiddleware() Middleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			next(ctx)
		}
	}
}
