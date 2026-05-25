package gateway

import (
	"encoding/json"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/valyala/fasthttp"
)

type modelDiscoveryItem struct {
	ID                         string
	OwnedBy                    string
	Created                    int64
	DisplayName                string
	Version                    string
	SupportedGenerationMethods []string
}

func (g *Gateway) writeAnthropicModels(ctx *fasthttp.RequestCtx, models []modelDiscoveryItem, tokenID string) {
	data := make([]map[string]interface{}, 0, len(models))
	for _, model := range models {
		item := map[string]interface{}{
			"id":           model.ID,
			"type":         "model",
			"display_name": firstNonEmpty(model.DisplayName, model.ID),
			"created_at":   "",
		}
		data = append(data, item)
	}
	body, _ := json.Marshal(map[string]interface{}{
		"data":     data,
		"has_more": false,
		"first_id": firstModelID(models),
		"last_id":  lastModelID(models),
	})
	ctx.SetContentType("application/json")
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBody(body)
	logger.Debugf("gateway.models", "listed anthropic models", logger.F("token_id", tokenID), logger.F("count", len(models)))
}

func (g *Gateway) writeGeminiModels(ctx *fasthttp.RequestCtx, models []modelDiscoveryItem, tokenID string) {
	data := make([]map[string]interface{}, 0, len(models))
	for _, model := range models {
		methods := model.SupportedGenerationMethods
		if len(methods) == 0 {
			methods = []string{"generateContent", "streamGenerateContent"}
		}
		item := map[string]interface{}{
			"name":                       "models/" + model.ID,
			"version":                    model.Version,
			"displayName":                firstNonEmpty(model.DisplayName, model.ID),
			"supportedGenerationMethods": methods,
		}
		data = append(data, item)
	}
	body, _ := json.Marshal(map[string]interface{}{"models": data})
	ctx.SetContentType("application/json")
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBody(body)
	logger.Debugf("gateway.models", "listed gemini models", logger.F("token_id", tokenID), logger.F("count", len(models)))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstModelID(models []modelDiscoveryItem) string {
	if len(models) == 0 {
		return ""
	}
	return models[0].ID
}

func lastModelID(models []modelDiscoveryItem) string {
	if len(models) == 0 {
		return ""
	}
	return models[len(models)-1].ID
}
