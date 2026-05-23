package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/internalauth"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/relay"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
)

const (
	statusActive           = "active"
	healthHealthy          = "healthy"
	defaultCacheTTL        = 5 * time.Minute
	passiveFailCooldown    = 10 * time.Second
	defaultTokenCacheSize  = 10000
)

type Gateway struct {
	db       *gorm.DB
	billing  *relay.BillingService
	fallback fasthttp.RequestHandler
	client   *fasthttp.Client
	limiter  *relay.ConcurrencyLimiter

	internalSecret string
	gatewayID      string
	trustedProxies []string

	mu        sync.Mutex
	nodes     []*nodeState
	routes    []*routeCandidate
	loadedAt  time.Time
	cacheTTL  time.Duration
	lastError error

	tokenMu      sync.Mutex
	tokenCache   *tokenLRUCache
	modelMu      sync.Mutex
	modelCache   map[string]modelDiscoveryCacheEntry
}

type nodeState struct {
	ID             uuid.UUID
	Name           string
	BaseURL        string
	Weight         int
	MaxConcurrency int
	Current        int
	FailUntil      time.Time
}

type routeCandidate struct {
	Node          *nodeState
	ChannelID     uuid.UUID
	AccountID     uuid.UUID
	AccountWeight int
	ChannelModels string
}

type tokenCacheEntry struct {
	token     db.Token
	expiresAt time.Time
}

// tokenLRUCache is a bounded LRU cache with TTL-based expiry.
type tokenLRUCache struct {
	capacity int
	cache    map[string]*cacheNode
	head     *cacheNode
	tail     *cacheNode
}

type cacheNode struct {
	key  string
	val  tokenCacheEntry
	prev *cacheNode
	next *cacheNode
}

func newTokenLRUCache(capacity int) *tokenLRUCache {
	if capacity <= 0 {
		capacity = defaultTokenCacheSize
	}
	c := &tokenLRUCache{
		capacity: capacity,
		cache:    make(map[string]*cacheNode, capacity),
	}
	return c
}

func (c *tokenLRUCache) Get(key string) (tokenCacheEntry, bool) {
	if node, ok := c.cache[key]; ok {
		// TTL expired — remove and report miss
		if !node.val.expiresAt.IsZero() && time.Now().After(node.val.expiresAt) {
			c.removeNode(node)
			delete(c.cache, node.key)
			return tokenCacheEntry{}, false
		}
		c.moveToFront(node)
		return node.val, true
	}
	return tokenCacheEntry{}, false
}

func (c *tokenLRUCache) Put(key string, entry tokenCacheEntry) {
	if node, ok := c.cache[key]; ok {
		node.val = entry
		c.moveToFront(node)
		return
	}
	// Evict if at capacity
	for len(c.cache) >= c.capacity {
		evicted := c.tail
		if evicted != nil {
			c.removeNode(evicted)
			delete(c.cache, evicted.key)
		}
	}
	node := &cacheNode{key: key, val: entry}
	c.cache[key] = node
	c.pushFront(node)
}

func (c *tokenLRUCache) moveToFront(node *cacheNode) {
	if node == c.head {
		return
	}
	c.removeNode(node)
	c.pushFront(node)
}

func (c *tokenLRUCache) removeNode(node *cacheNode) {
	if node.prev != nil {
		node.prev.next = node.next
	} else {
		c.head = node.next
	}
	if node.next != nil {
		node.next.prev = node.prev
	} else {
		c.tail = node.prev
	}
	node.prev = nil
	node.next = nil
}

func (c *tokenLRUCache) pushFront(node *cacheNode) {
	node.prev = nil
	node.next = c.head
	if c.head != nil {
		c.head.prev = node
	}
	c.head = node
	if c.tail == nil {
		c.tail = node
	}
}

type authenticatedToken struct {
	token     db.Token
	policy    db.AccessPolicy
	hasPolicy bool
}

type relayRequest struct {
	Model     string `json:"model"`
	Stream    bool   `json:"stream"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

func New(database *gorm.DB, billing *relay.BillingService, fallback fasthttp.RequestHandler, internalSecret, gatewayID string, concLimiter *relay.ConcurrencyLimiter, cacheTTL time.Duration, trustedProxies []string) *Gateway {
	if cacheTTL <= 0 {
		cacheTTL = defaultCacheTTL
	}
	if concLimiter == nil {
		concLimiter = relay.NewConcurrencyLimiter(0)
	}
	return &Gateway{
		db:             database,
		billing:        billing,
		fallback:       fallback,
		cacheTTL:       cacheTTL,
		internalSecret: internalSecret,
		gatewayID:      gatewayID,
		limiter:        concLimiter,
		trustedProxies: trustedProxies,
		tokenCache:     newTokenLRUCache(defaultTokenCacheSize),
		modelCache:     make(map[string]modelDiscoveryCacheEntry),
		client: &fasthttp.Client{
			ReadTimeout:                   0,
			WriteTimeout:                  30 * time.Second,
			MaxConnDuration:               0,
			StreamResponseBody:            true,
			NoDefaultUserAgentHeader:      true,
			DisableHeaderNamesNormalizing: true,
		},
	}
}

func (g *Gateway) Handle(ctx *fasthttp.RequestCtx) {
	if string(ctx.Method()) == fasthttp.MethodGet && string(ctx.Path()) == "/v1/models" {
		g.handleModels(ctx)
		return
	}
	if string(ctx.Method()) == fasthttp.MethodGet && string(ctx.Path()) == "/v1beta/models" {
		g.handleGeminiModels(ctx)
		return
	}

	body := ctx.PostBody()
	var req relayRequest
	_ = json.Unmarshal(body, &req)
	req.Model = modelFromRequestPath(string(ctx.Path()), req.Model)
	if req.Model == "" && strings.HasPrefix(string(ctx.Path()), "/v1/images/") {
		req.Model = modelFromImageRequest(ctx)
	}
	if req.Model == "" && strings.HasPrefix(string(ctx.Path()), "/v1/images/") {
		req.Model = "gpt-image-1"
	}
	if req.Model == "" {
		ctx.Error(`{"error":"model is required"}`, fasthttp.StatusBadRequest)
		return
	}

	authInfo, ok := g.authenticate(ctx, req.Model)
	if !ok {
		return
	}
	token := authInfo.token
	tokenID := token.ID.String()
	limitKey := tokenID
	if authInfo.hasPolicy && authInfo.policy.MaxConcurrency > 0 {
		limitKey = authInfo.policy.ID.String()
		if !g.limiter.AcquireWithLimit(limitKey, authInfo.policy.MaxConcurrency) {
			ctx.Error(`{"error":"concurrent request limit exceeded"}`, fasthttp.StatusTooManyRequests)
			return
		}
	} else {
		if !g.limiter.Acquire(tokenID) {
			ctx.Error(`{"error":"concurrent request limit exceeded"}`, fasthttp.StatusTooManyRequests)
			return
		}
	}
	defer g.limiter.Release(limitKey)

	if authInfo.hasPolicy {
		if err := g.checkPolicyWindows(authInfo.policy, token.ID); err != nil {
			ctx.Error(`{"error":"`+jsonEscape(err.Error())+`"}`, fasthttp.StatusTooManyRequests)
			return
		}
	}

	route, releaseNode, ok := g.pickRoute(req.Model)
	if !ok {
		g.fallback(ctx)
		return
	}
	node := route.Node
	defer func() {
		if releaseNode != nil {
			releaseNode(false)
		}
	}()

	estimatedTokens := req.MaxTokens
	if estimatedTokens <= 0 {
		estimatedTokens = 1000
	}
	if g.billing != nil {
		if err := g.billing.CheckLimit(tokenID); err != nil {
			ctx.Error(`{"error":"rate limit exceeded"}`, fasthttp.StatusTooManyRequests)
			return
		}
		if token.UserID != "" {
			if err := g.billing.CheckUserBalance(token.UserID, tokenID); err != nil {
				ctx.Error(`{"error":"`+jsonEscape(err.Error())+`"}`, fasthttp.StatusPaymentRequired)
				return
			}
		}
		if _, err := g.billing.PreConsume(tokenID, req.Model, estimatedTokens); err != nil {
			logger.Warnf("gateway.billing", "pre-consume failed", logger.F("token_id", tokenID), logger.Err(err))
		}
	}
	precharged := g.billing != nil

	upReq := fasthttp.AcquireRequest()
	upResp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(upReq)

	if err := g.buildRequest(ctx, upReq, node.BaseURL); err != nil {
		fasthttp.ReleaseResponse(upResp)
		if precharged {
			go g.billing.Refund(tokenID, estimatedTokens)
		}
		ctx.Error(`{"error":"gateway build request failed"}`, fasthttp.StatusBadGateway)
		return
	}
	internalauth.StripHeaders(upReq)
	upReq.Header.Del("Authorization")
	if err := internalauth.SignRequest(upReq, g.internalSecret, internalauth.Claims{
		GatewayID:       g.gatewayID,
		TokenID:         tokenID,
		UserID:          token.UserID,
		Model:           req.Model,
		EstimatedTokens: estimatedTokens,
		Precharged:      precharged,
		ClientIP:        g.clientIP(ctx),
		RequestID:       uuid.NewString(),
		ChannelID:       route.ChannelID.String(),
		AccountID:       route.AccountID.String(),
	}, time.Now()); err != nil {
		fasthttp.ReleaseResponse(upResp)
		if precharged {
			go g.billing.Refund(tokenID, estimatedTokens)
		}
		ctx.Error(`{"error":"gateway signing failed"}`, fasthttp.StatusInternalServerError)
		return
	}

	start := time.Now()
	if err := g.client.Do(upReq, upResp); err != nil {
		fasthttp.ReleaseResponse(upResp)
		logger.Warnf("gateway.proxy", "relay proxy failed, trying local fallback", logger.F("node", node.Name), logger.F("url", node.BaseURL), logger.Err(err))
		releaseNode(true)
		releaseNode = nil
		if precharged {
			go g.billing.Refund(tokenID, estimatedTokens)
		}
		// Fallback to local relayer when external relay fails
		if g.fallback != nil {
			g.fallback(ctx)
			return
		}
		ctx.Error(`{"error":"relay node unavailable"}`, fasthttp.StatusBadGateway)
		return
	}

	copyResponseHeaders(ctx, upResp)
	ctx.SetStatusCode(upResp.StatusCode())
	ctx.Response.Header.Set("X-UAPI-Relay-Node", node.Name)
	if upResp.StatusCode() >= 500 {
		g.markPassiveFailure(node.ID)
	}

	stream := upResp.BodyStream()
	if stream != nil {
		rel := releaseNode
		resp := upResp
		statusCode := upResp.StatusCode()
		ctx.Response.SetBodyStream(&releaseReader{
			reader: stream,
			close: func() {
				rel(false)
				fasthttp.ReleaseResponse(resp)
			},
		}, -1)
		releaseNode = nil
		logProxy(node.Name, start, statusCode)
		return
	}

	bodyResp := upResp.Body()
	bodyCopy := make([]byte, len(bodyResp))
	copy(bodyCopy, bodyResp)
	fasthttp.ReleaseResponse(upResp)
	ctx.SetBody(bodyCopy)
	logProxy(node.Name, start, ctx.Response.StatusCode())
}

type modelListResponse struct {
	Object string          `json:"object"`
	Data   []modelListItem `json:"data"`
}

type modelListItem struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

func (g *Gateway) handleModels(ctx *fasthttp.RequestCtx) {
	authInfo, ok := g.authenticateForModels(ctx)
	if !ok {
		return
	}
	models, err := g.availableModelInfos(authInfo)
	if err != nil {
		logger.Warnf("gateway.models", "list models failed", logger.F("token_id", authInfo.token.ID.String()), logger.Err(err))
		ctx.Error(`{"error":"list models failed"}`, fasthttp.StatusInternalServerError)
		return
	}

	if strings.TrimSpace(string(ctx.Request.Header.Peek("x-api-key"))) != "" && strings.TrimSpace(string(ctx.Request.Header.Peek("Authorization"))) == "" {
		g.writeAnthropicModels(ctx, models, authInfo.token.ID.String())
		return
	}

	data := make([]modelListItem, 0, len(models))
	for _, model := range models {
		data = append(data, modelListItem{
			ID:      model.ID,
			Object:  "model",
			Created: model.Created,
			OwnedBy: model.OwnedBy,
		})
	}
	body, _ := json.Marshal(modelListResponse{Object: "list", Data: data})
	ctx.SetContentType("application/json")
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBody(body)
	logger.Debugf("gateway.models", "listed models", logger.F("token_id", authInfo.token.ID.String()), logger.F("count", len(models)))
}

func (g *Gateway) handleGeminiModels(ctx *fasthttp.RequestCtx) {
	authInfo, ok := g.authenticateForModels(ctx)
	if !ok {
		return
	}
	models, err := g.availableModelInfos(authInfo)
	if err != nil {
		logger.Warnf("gateway.models", "list gemini models failed", logger.F("token_id", authInfo.token.ID.String()), logger.Err(err))
		ctx.Error(`{"error":"list models failed"}`, fasthttp.StatusInternalServerError)
		return
	}
	g.writeGeminiModels(ctx, models, authInfo.token.ID.String())
}

func (g *Gateway) authenticateForModels(ctx *fasthttp.RequestCtx) (authenticatedToken, bool) {
	tokenKey := extractBearerToken(ctx)
	if tokenKey == "" {
		ctx.Error(`{"error":"missing authorization"}`, fasthttp.StatusUnauthorized)
		return authenticatedToken{}, false
	}
	token, err := g.getToken(tokenKey)
	if err != nil || !token.Enabled || token.DeletedAt != nil {
		ctx.Error(`{"error":"invalid token"}`, fasthttp.StatusUnauthorized)
		return authenticatedToken{}, false
	}
	if token.ExpiresAt != nil && time.Now().After(*token.ExpiresAt) {
		ctx.Error(`{"error":"token expired"}`, fasthttp.StatusUnauthorized)
		return authenticatedToken{}, false
	}
	if token.IPWhitelist != "" && !checkIPWhitelist(ctx, token.IPWhitelist, g.trustedProxies) {
		ctx.Error(`{"error":"ip not whitelisted"}`, fasthttp.StatusForbidden)
		return authenticatedToken{}, false
	}
	if token.Permissions != "" && !anyPermissionInList(token.Permissions, "chat", "responses", "messages", "gemini") {
		ctx.Error(`{"error":"permission not allowed for token"}`, fasthttp.StatusForbidden)
		return authenticatedToken{}, false
	}
	policy, hasPolicy, err := g.loadPolicy(token)
	if err != nil {
		ctx.Error(`{"error":"`+jsonEscape(err.Error())+`"}`, fasthttp.StatusForbidden)
		return authenticatedToken{}, false
	}
	return authenticatedToken{token: token, policy: policy, hasPolicy: hasPolicy}, true
}

func (g *Gateway) availableModelInfos(authInfo authenticatedToken) ([]modelDiscoveryItem, error) {
	var rows []struct {
		Models string
	}
	if err := g.db.Table("channels").
		Select("DISTINCT channels.models").
		Joins("JOIN accounts ON accounts.channel_id = channels.id AND accounts.enabled = true AND accounts.deleted_at IS NULL").
		Where("channels.enabled = true AND channels.deleted_at IS NULL AND channels.models <> ''").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	allowed := authInfo.token.Models
	if authInfo.hasPolicy {
		allowed = authInfo.policy.AllowedModels
	}
	allowedSet := csvSet(allowed)
	seen := map[string]modelDiscoveryItem{}
	for _, row := range rows {
		for _, model := range csvList(row.Models) {
			if len(allowedSet) > 0 {
				if _, ok := allowedSet[model]; !ok {
					continue
				}
			}
			seen[model] = modelDiscoveryItem{ID: model, OwnedBy: "uapi"}
		}
	}
	for _, model := range g.discoverStandardModels() {
		if len(allowedSet) > 0 {
			if _, ok := allowedSet[model.ID]; !ok {
				continue
			}
		}
		if existing, ok := seen[model.ID]; ok {
			if existing.OwnedBy == "" || existing.OwnedBy == "uapi" {
				seen[model.ID] = model
			}
			continue
		}
		seen[model.ID] = model
	}
	models := make([]modelDiscoveryItem, 0, len(seen))
	for _, model := range seen {
		if model.OwnedBy == "" {
			model.OwnedBy = "uapi"
		}
		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

func (g *Gateway) authenticate(ctx *fasthttp.RequestCtx, model string) (authenticatedToken, bool) {
	tokenKey := extractBearerToken(ctx)
	if tokenKey == "" {
		ctx.Error(`{"error":"missing authorization"}`, fasthttp.StatusUnauthorized)
		return authenticatedToken{}, false
	}
	token, err := g.getToken(tokenKey)
	if err != nil || !token.Enabled || token.DeletedAt != nil {
		ctx.Error(`{"error":"invalid token"}`, fasthttp.StatusUnauthorized)
		return authenticatedToken{}, false
	}
	if token.ExpiresAt != nil && time.Now().After(*token.ExpiresAt) {
		ctx.Error(`{"error":"token expired"}`, fasthttp.StatusUnauthorized)
		return authenticatedToken{}, false
	}
	if token.IPWhitelist != "" && !checkIPWhitelist(ctx, token.IPWhitelist, g.trustedProxies) {
		ctx.Error(`{"error":"ip not whitelisted"}`, fasthttp.StatusForbidden)
		return authenticatedToken{}, false
	}
	policy, hasPolicy, err := g.loadPolicy(token)
	if err != nil {
		ctx.Error(`{"error":"`+jsonEscape(err.Error())+`"}`, fasthttp.StatusForbidden)
		return authenticatedToken{}, false
	}
	allowedModels := token.Models
	if hasPolicy {
		allowedModels = policy.AllowedModels
	}
	if allowedModels != "" && !modelInList(model, allowedModels) {
		ctx.Error(`{"error":"model not allowed for token"}`, fasthttp.StatusForbidden)
		return authenticatedToken{}, false
	}
	permission := permissionForPath(string(ctx.Path()))
	if token.Permissions != "" && !permissionInList(permission, token.Permissions) {
		ctx.Error(`{"error":"permission not allowed for token"}`, fasthttp.StatusForbidden)
		return authenticatedToken{}, false
	}
	return authenticatedToken{token: token, policy: policy, hasPolicy: hasPolicy}, true
}

func (g *Gateway) getToken(key string) (db.Token, error) {
	now := time.Now()
	g.tokenMu.Lock()
	if entry, ok := g.tokenCache.Get(key); ok && now.Before(entry.expiresAt) {
		g.tokenMu.Unlock()
		return entry.token, nil
	}
	g.tokenMu.Unlock()

	var token db.Token
	err := g.db.Where("key = ? AND enabled = true AND deleted_at IS NULL", key).First(&token).Error
	if err != nil {
		return db.Token{}, err
	}
	g.tokenMu.Lock()
	g.tokenCache.Put(key, tokenCacheEntry{token: token, expiresAt: now.Add(g.cacheTTL)})
	g.tokenMu.Unlock()
	return token, nil
}

func (g *Gateway) pickRoute(model string) (*routeCandidate, func(bool), bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if time.Since(g.loadedAt) >= g.cacheTTL {
		g.reloadLocked()
	}
	now := time.Now()
	var best *routeCandidate
	bestScore := math.MaxFloat64
	for _, route := range g.routes {
		node := route.Node
		if route.AccountWeight <= 0 || !modelInList(model, route.ChannelModels) {
			continue
		}
		if node.Weight <= 0 || now.Before(node.FailUntil) {
			continue
		}
		if node.MaxConcurrency > 0 && node.Current >= node.MaxConcurrency {
			continue
		}
		effectiveWeight := node.Weight * route.AccountWeight
		score := float64(node.Current+1) / float64(effectiveWeight)
		if score < bestScore {
			best = route
			bestScore = score
		}
	}
	if best == nil {
		return nil, nil, false
	}
	best.Node.Current++
	nodeID := best.Node.ID
	release := func(failed bool) {
		g.mu.Lock()
		defer g.mu.Unlock()
		for _, node := range g.nodes {
			if node.ID == nodeID {
				if node.Current > 0 {
					node.Current--
				}
				if failed {
					node.FailUntil = time.Now().Add(passiveFailCooldown)
				}
				return
			}
		}
	}
	copyNode := *best.Node
	copyRoute := *best
	copyRoute.Node = &copyNode
	return &copyRoute, release, true
}

func (g *Gateway) reloadLocked() {
	var rows []struct {
		NodeID         uuid.UUID
		NodeName       string
		BaseURL        string
		NodeWeight     int
		MaxConcurrency int
		ChannelID      uuid.UUID
		AccountID      uuid.UUID
		AccountWeight  int
		ChannelModels  string
	}
	err := g.db.Table("relay_nodes").
		Select(`relay_nodes.id AS node_id, relay_nodes.name AS node_name, relay_nodes.base_url,
			relay_nodes.weight AS node_weight, relay_nodes.max_concurrency,
			accounts.channel_id, accounts.id AS account_id, node_accounts.weight AS account_weight,
			channels.models AS channel_models`).
		Joins("JOIN node_accounts ON node_accounts.relay_node_id = relay_nodes.id AND node_accounts.enabled = true AND node_accounts.deleted_at IS NULL").
		Joins("JOIN accounts ON accounts.id = node_accounts.account_id AND accounts.enabled = true AND accounts.deleted_at IS NULL").
		Joins("JOIN channels ON channels.id = accounts.channel_id AND channels.enabled = true AND channels.deleted_at IS NULL").
		Where("relay_nodes.deleted_at IS NULL AND relay_nodes.status = ? AND relay_nodes.health_status = ?", statusActive, healthHealthy).
		Order("relay_nodes.created_at asc").
		Scan(&rows).Error
	g.loadedAt = time.Now()
	if err != nil {
		g.lastError = err
		logger.Warnf("gateway.routes", "reload relay routes failed", logger.Err(err))
		return
	}
	existing := make(map[uuid.UUID]*nodeState, len(g.nodes))
	for _, node := range g.nodes {
		existing[node.ID] = node
	}
	nextNodes := make([]*nodeState, 0, len(rows))
	nextRoutes := make([]*routeCandidate, 0, len(rows))
	seenNodes := make(map[uuid.UUID]*nodeState)
	for _, row := range rows {
		baseURL := strings.TrimRight(row.BaseURL, "/")
		if _, err := url.ParseRequestURI(baseURL); err != nil {
			continue
		}
		state := seenNodes[row.NodeID]
		if state == nil {
			state = existing[row.NodeID]
		}
		if state == nil {
			state = &nodeState{ID: row.NodeID}
		}
		if _, ok := seenNodes[row.NodeID]; !ok {
			seenNodes[row.NodeID] = state
			nextNodes = append(nextNodes, state)
		}
		state.Name = row.NodeName
		state.BaseURL = baseURL
		state.Weight = row.NodeWeight
		state.MaxConcurrency = row.MaxConcurrency
		nextRoutes = append(nextRoutes, &routeCandidate{
			Node:          state,
			ChannelID:     row.ChannelID,
			AccountID:     row.AccountID,
			AccountWeight: row.AccountWeight,
			ChannelModels: row.ChannelModels,
		})
	}
	g.nodes = nextNodes
	g.routes = nextRoutes
	g.lastError = nil
}

func (g *Gateway) markPassiveFailure(id uuid.UUID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, node := range g.nodes {
		if node.ID == id {
			node.FailUntil = time.Now().Add(passiveFailCooldown)
			return
		}
	}
}

func (g *Gateway) buildRequest(ctx *fasthttp.RequestCtx, out *fasthttp.Request, baseURL string) error {
	path := string(ctx.RequestURI())
	target := baseURL + path
	if _, err := url.ParseRequestURI(target); err != nil {
		return fmt.Errorf("invalid target URL: %w", err)
	}
	ctx.Request.CopyTo(out)
	out.SetRequestURI(target)
	out.Header.Del("Connection")
	out.Header.Del("Proxy-Connection")
	out.Header.Del("Keep-Alive")
	out.Header.Del("Transfer-Encoding")
	out.Header.Set("X-Forwarded-For", g.clientIP(ctx))
	out.Header.Set("X-Forwarded-Proto", string(ctx.URI().Scheme()))
	return nil
}

func copyResponseHeaders(ctx *fasthttp.RequestCtx, resp *fasthttp.Response) {
	resp.Header.VisitAll(func(k, v []byte) {
		key := string(k)
		if strings.EqualFold(key, "Connection") || strings.EqualFold(key, "Transfer-Encoding") || strings.EqualFold(key, "Keep-Alive") {
			return
		}
		ctx.Response.Header.SetBytesKV(k, v)
	})
}

type releaseReader struct {
	reader io.Reader
	once   sync.Once
	close  func()
}

func (r *releaseReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if err != nil {
		r.Close()
	}
	return n, err
}

func (r *releaseReader) Close() error {
	r.once.Do(func() {
		if closer, ok := r.reader.(io.Closer); ok {
			_ = closer.Close()
		}
		if r.close != nil {
			r.close()
		}
	})
	return nil
}

func extractBearerToken(ctx *fasthttp.RequestCtx) string {
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
	return ""
}

func modelFromRequestPath(path, bodyModel string) string {
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

func checkIPWhitelist(ctx *fasthttp.RequestCtx, whitelist string, trustedProxies []string) bool {
	for _, allowedIP := range strings.Split(whitelist, ",") {
		allowedIP = strings.TrimSpace(allowedIP)
		if allowedIP == "" {
			continue
		}
		for _, clientIP := range clientIPCandidates(ctx, trustedProxies) {
			if allowedIP == clientIP {
				return true
			}
		}
	}
	return false
}

// clientIPCandidates returns candidate client IPs for whitelist matching.
// Forwarded headers are only considered when the direct connection IP is a trusted proxy.
func clientIPCandidates(ctx *fasthttp.RequestCtx, trustedProxies []string) []string {
	remoteIP := ctx.RemoteIP().String()
	candidates := []string{remoteIP}

	// Only trust forwarded headers if the direct connection is from a trusted proxy
	if !isTrustedProxy(remoteIP, trustedProxies) {
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

// isTrustedProxy checks if the given IP is in the trusted proxies list.
func isTrustedProxy(ip string, trustedProxies []string) bool {
	for _, trusted := range trustedProxies {
		if strings.TrimSpace(trusted) == ip {
			return true
		}
	}
	return false
}

func (g *Gateway) clientIP(ctx *fasthttp.RequestCtx) string {
	ips := clientIPCandidates(ctx, g.trustedProxies)
	if len(ips) == 0 {
		return ""
	}
	return ips[0]
}

func modelInList(model, list string) bool {
	for _, m := range csvList(list) {
		if m == model {
			return true
		}
	}
	return false
}

func csvList(list string) []string {
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

func csvSet(list string) map[string]struct{} {
	items := csvList(list)
	if len(items) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(items))
	for _, item := range items {
		set[item] = struct{}{}
	}
	return set
}

func permissionForPath(path string) string {
	switch {
	case strings.HasPrefix(path, "/v1/messages"):
		return "messages"
	case strings.HasPrefix(path, "/v1beta/"):
		return "gemini"
	case strings.HasPrefix(path, "/v1/responses"):
		return "responses"
	case strings.HasPrefix(path, "/v1/images/"):
		return "images"
	default:
		return "chat"
	}
}

func modelFromImageRequest(ctx *fasthttp.RequestCtx) string {
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

func permissionInList(permission, list string) bool {
	for _, item := range csvList(list) {
		if item == permission {
			return true
		}
	}
	return false
}

func anyPermissionInList(list string, permissions ...string) bool {
	set := csvSet(list)
	for _, permission := range permissions {
		if _, ok := set[permission]; ok {
			return true
		}
	}
	return false
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	if len(b) >= 2 {
		return string(b[1 : len(b)-1])
	}
	return s
}

func logProxy(node string, start time.Time, status int) {
	if status >= 500 {
		logger.Infof("gateway.relay", "relay request completed", logger.F("node", node), logger.F("status", status), logger.F("latency_ms", time.Since(start).Milliseconds()))
	}
}
