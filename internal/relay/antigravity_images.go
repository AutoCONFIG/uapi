package relay

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/antigravity"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

type openAIImageGenerationRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              int    `json:"n"`
	Size           string `json:"size"`
	Quality        string `json:"quality"`
	ImageSize      string `json:"image_size"`
	ImageSizeCamel string `json:"imageSize"`
	ResponseFormat string `json:"response_format"`
}

func antigravityImageGenerationRequest(body []byte, account *db.Account, fallbackModel string) ([]byte, error) {
	var req openAIImageGenerationRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid image request body: %w", err)
	}
	return antigravityImageRequest(req, nil, account, fallbackModel)
}

func antigravityImageMultipartRequest(ctx *fasthttp.RequestCtx, account *db.Account, fallbackModel string, requestType relayRequestType) ([]byte, error) {
	form, err := ctx.MultipartForm()
	if err != nil {
		return nil, fmt.Errorf("invalid multipart image request: %w", err)
	}
	req := openAIImageGenerationRequest{
		Model:          formValue(form, "model"),
		Prompt:         formValue(form, "prompt"),
		N:              formInt(form, "n"),
		Size:           formValue(form, "size"),
		Quality:        formValue(form, "quality"),
		ImageSize:      formValue(form, "image_size"),
		ImageSizeCamel: formValue(form, "imageSize"),
		ResponseFormat: formValue(form, "response_format"),
	}
	if strings.TrimSpace(req.Prompt) == "" && requestType == requestTypeImageVariation {
		req.Prompt = "Create a high quality variation of the provided image."
	}
	refs, err := multipartInlineImages(form)
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, fmt.Errorf("missing image")
	}
	return antigravityImageRequest(req, refs, account, fallbackModel)
}

func antigravityImageRequest(req openAIImageGenerationRequest, refs []inlineImage, account *db.Account, fallbackModel string) ([]byte, error) {
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		return nil, fmt.Errorf("missing prompt")
	}
	if strings.TrimSpace(req.Model) == "" {
		req.Model = fallbackModel
	}
	if strings.TrimSpace(req.Model) == "" {
		req.Model = "nano-banana-2"
	}
	model := antigravity.UpstreamModelID(req.Model)
	if !antigravity.IsImageToolModel(req.Model) && !strings.Contains(strings.ToLower(model), "image") {
		model = antigravity.UpstreamModelID("nano-banana-2")
	}

	imageConfig := antigravityImageConfig(req)
	parts := make([]map[string]interface{}, 0, 1+len(refs))
	parts = append(parts, map[string]interface{}{"text": req.Prompt})
	for _, ref := range refs {
		parts = append(parts, map[string]interface{}{
			"inlineData": map[string]string{
				"mimeType": ref.mimeType,
				"data":     ref.data,
			},
		})
	}
	request := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"role":  "user",
				"parts": parts,
			},
		},
		"generationConfig": map[string]interface{}{
			"candidateCount": imageCandidateCount(req.N),
			"imageConfig":    imageConfig,
		},
		"safetySettings": []map[string]string{
			{"category": "HARM_CATEGORY_HARASSMENT", "threshold": "OFF"},
			{"category": "HARM_CATEGORY_HATE_SPEECH", "threshold": "OFF"},
			{"category": "HARM_CATEGORY_SEXUALLY_EXPLICIT", "threshold": "OFF"},
			{"category": "HARM_CATEGORY_DANGEROUS_CONTENT", "threshold": "OFF"},
			{"category": "HARM_CATEGORY_CIVIC_INTEGRITY", "threshold": "OFF"},
		},
	}
	out := map[string]interface{}{
		"model":       model,
		"userAgent":   "antigravity",
		"requestType": "image_gen",
		"requestId":   fmt.Sprintf("image_gen/%d/%s/12", time.Now().UnixMilli(), uuid.NewString()),
		"request":     request,
	}
	if projectID := antigravity.ProjectID(account); projectID != "" {
		out["project"] = projectID
	}
	return json.Marshal(out)
}

func formValue(form *multipart.Form, key string) string {
	if form == nil || len(form.Value[key]) == 0 {
		return ""
	}
	return strings.TrimSpace(form.Value[key][0])
}

func formInt(form *multipart.Form, key string) int {
	value := formValue(form, key)
	if value == "" {
		return 0
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return n
}

func multipartInlineImages(form *multipart.Form) ([]inlineImage, error) {
	if form == nil {
		return nil, nil
	}
	out := make([]inlineImage, 0)
	keys := make([]string, 0, len(form.File))
	for key := range form.File {
		if key == "mask" || key == "image" || (strings.HasPrefix(key, "image") && key != "image_size" && key != "imageSize") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, fh := range form.File[key] {
			img, err := multipartInlineImage(fh)
			if err != nil {
				return nil, err
			}
			out = append(out, img)
		}
	}
	return out, nil
}

func multipartInlineImage(fh *multipart.FileHeader) (inlineImage, error) {
	f, err := fh.Open()
	if err != nil {
		return inlineImage{}, fmt.Errorf("read image: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(maxResponseSize)+1))
	if err != nil {
		return inlineImage{}, fmt.Errorf("read image: %w", err)
	}
	if len(data) > maxResponseSize {
		return inlineImage{}, fmt.Errorf("image too large")
	}
	mimeType := fh.Header.Get("Content-Type")
	if strings.TrimSpace(mimeType) == "" {
		mimeType = "image/png"
	}
	return inlineImage{mimeType: mimeType, data: base64.StdEncoding.EncodeToString(data)}, nil
}

func imageCandidateCount(n int) int {
	if n < 1 {
		return 1
	}
	if n > 10 {
		return 10
	}
	return n
}

func antigravityImageConfig(req openAIImageGenerationRequest) map[string]string {
	cfg := map[string]string{
		"aspectRatio": "1:1",
		"imageSize":   "1K",
	}
	if aspect := aspectRatioFromSize(req.Size); aspect != "" {
		cfg["aspectRatio"] = aspect
	}
	imageSize := firstNonEmptyString(req.ImageSizeCamel, req.ImageSize)
	switch strings.ToLower(strings.TrimSpace(firstNonEmptyString(imageSize, req.Quality))) {
	case "4k", "hd":
		cfg["imageSize"] = "4K"
	case "2k", "medium":
		cfg["imageSize"] = "2K"
	case "1k", "standard", "low":
		cfg["imageSize"] = "1K"
	}
	return cfg
}

func antigravityImagesOpenAIResponse(respBody []byte, responseFormat string) ([]byte, error) {
	var root map[string]interface{}
	if err := json.Unmarshal(respBody, &root); err != nil {
		return nil, err
	}
	raw := root
	if nested, ok := root["response"].(map[string]interface{}); ok {
		raw = nested
	}
	images := make([]map[string]string, 0)
	for _, data := range inlineImagesFromGeminiResponse(raw) {
		if strings.EqualFold(responseFormat, "url") {
			images = append(images, map[string]string{"url": "data:" + data.mimeType + ";base64," + data.data})
		} else {
			images = append(images, map[string]string{"b64_json": data.data})
		}
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("no images generated")
	}
	out, _ := json.Marshal(map[string]interface{}{
		"created": time.Now().Unix(),
		"data":    images,
	})
	return out, nil
}

type inlineImage struct {
	mimeType string
	data     string
}

func inlineImagesFromGeminiResponse(raw map[string]interface{}) []inlineImage {
	candidates, _ := raw["candidates"].([]interface{})
	out := make([]inlineImage, 0)
	for _, candRaw := range candidates {
		cand, _ := candRaw.(map[string]interface{})
		content, _ := cand["content"].(map[string]interface{})
		parts, _ := content["parts"].([]interface{})
		for _, partRaw := range parts {
			part, _ := partRaw.(map[string]interface{})
			inline, _ := part["inlineData"].(map[string]interface{})
			if inline == nil {
				inline, _ = part["inline_data"].(map[string]interface{})
			}
			if inline == nil {
				continue
			}
			data, _ := inline["data"].(string)
			if strings.TrimSpace(data) == "" {
				continue
			}
			mimeType, _ := inline["mimeType"].(string)
			if mimeType == "" {
				mimeType, _ = inline["mime_type"].(string)
			}
			if mimeType == "" {
				mimeType = "image/png"
			}
			out = append(out, inlineImage{mimeType: mimeType, data: data})
		}
	}
	return out
}

func aspectRatioFromSize(size string) string {
	size = strings.ToLower(strings.TrimSpace(size))
	switch size {
	case "21:9", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3", "5:4", "4:5", "1:1":
		return size
	}
	parts := strings.Split(size, "x")
	if len(parts) != 2 {
		return ""
	}
	w, errW := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	h, errH := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if errW != nil || errH != nil || w <= 0 || h <= 0 {
		return ""
	}
	ratio := w / h
	candidates := []struct {
		label string
		value float64
	}{
		{"21:9", 21.0 / 9.0},
		{"16:9", 16.0 / 9.0},
		{"4:3", 4.0 / 3.0},
		{"3:4", 3.0 / 4.0},
		{"9:16", 9.0 / 16.0},
		{"3:2", 3.0 / 2.0},
		{"2:3", 2.0 / 3.0},
		{"5:4", 5.0 / 4.0},
		{"4:5", 4.0 / 5.0},
		{"1:1", 1.0},
	}
	bestLabel := "1:1"
	bestDelta := math.MaxFloat64
	for _, c := range candidates {
		if delta := math.Abs(ratio - c.value); delta < bestDelta {
			bestDelta = delta
			bestLabel = c.label
		}
	}
	if bestDelta > 0.05 {
		return "1:1"
	}
	return bestLabel
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
