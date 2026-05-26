package user

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/auth"
	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct {
	db                 *gorm.DB
	jwtSecret          string
	accessTokenExpiry  time.Duration
	refreshTokenExpiry time.Duration
	maxKeysPerUser     int
}

func NewService(database *gorm.DB, jwtSecret string, accessTokenExpiry, refreshTokenExpiry time.Duration, maxKeysPerUser int) *Service {
	return &Service{
		db:                 database,
		jwtSecret:          jwtSecret,
		accessTokenExpiry:  accessTokenExpiry,
		refreshTokenExpiry: refreshTokenExpiry,
		maxKeysPerUser:     maxKeysPerUser,
	}
}

func (s *Service) Register(req *RegisterRequest) (*LoginResponse, error) {
	// Validate email format
	if _, err := mail.ParseAddress(req.Email); err != nil {
		return nil, errors.New("invalid email format")
	}

	// Validate username
	trimmed := strings.TrimSpace(req.Username)
	if trimmed == "" || len(trimmed) > 100 {
		return nil, errors.New("username must be 1-100 characters")
	}
	req.Username = trimmed

	// Validate password length
	if len(req.Password) < 8 {
		return nil, errors.New("password must be at least 8 characters")
	}

	// Hash password (do this outside the transaction since it's CPU-bound)
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), 10)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	var newUser db.User
	err = s.db.Transaction(func(tx *gorm.DB) error {
		// Check email uniqueness within transaction
		var count int64
		tx.Model(&db.User{}).Where("email = ? AND deleted_at IS NULL", req.Email).Count(&count)
		if count > 0 {
			return errors.New("email already registered")
		}

		// Check username uniqueness within transaction
		tx.Model(&db.User{}).Where("username = ? AND deleted_at IS NULL", req.Username).Count(&count)
		if count > 0 {
			return errors.New("username already taken")
		}

		// Create user
		newUser = db.User{
			Email:        req.Email,
			Username:     req.Username,
			PasswordHash: string(hash),
			Status:       "active",
		}
		if err := tx.Create(&newUser).Error; err != nil {
			// Fallback: catch DB unique constraint violation from concurrent race
			if strings.Contains(err.Error(), "UNIQUE constraint") ||
				strings.Contains(err.Error(), "duplicate key") {
				return errors.New("email or username already registered")
			}
			return err
		}
		if s.maxKeysPerUser > 0 {
			var err error
			_, err = createUserTokenTx(tx, newUser.ID.String(), &CreateKeyRequest{Name: "默认密钥"})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return s.issueTokenPair(newUser.ID.String(), newUser.Username, newUser.PasswordHash)
}

func (s *Service) Login(req *LoginRequest) (*LoginResponse, error) {
	var user db.User
	if err := s.db.Where("email = ? AND deleted_at IS NULL AND status = 'active'", req.Email).First(&user).Error; err != nil {
		// Dummy bcrypt to prevent timing-based email enumeration
		bcrypt.CompareHashAndPassword([]byte("$2a$10$000000000000000000000uGYAyOEPv8VQ8H1Vw8BrSbxWJvOXqWK"), []byte(req.Password))
		return nil, errors.New("invalid email or password")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		return nil, errors.New("invalid email or password")
	}

	return s.issueTokenPair(user.ID.String(), user.Username, user.PasswordHash)
}

func (s *Service) RefreshToken(tokenStr string) (*LoginResponse, error) {
	claims, err := auth.ParseToken(s.jwtSecret, tokenStr)
	if err != nil || claims.Type != auth.TokenTypeUserRefresh {
		return nil, errors.New("invalid refresh token")
	}

	var user db.User
	if err := s.db.Where("id = ? AND deleted_at IS NULL AND status = 'active'", claims.UserID).First(&user).Error; err != nil {
		return nil, errors.New("user not found or inactive")
	}
	if claims.Version != auth.SecretVersion(user.PasswordHash) {
		return nil, errors.New("invalid refresh token")
	}

	return s.issueTokenPair(user.ID.String(), user.Username, user.PasswordHash)
}

func (s *Service) GetProfile(userID string) (*ProfileResponse, error) {
	var user db.User
	if err := s.db.Where("id = ? AND deleted_at IS NULL", userID).First(&user).Error; err != nil {
		return nil, errors.New("user not found")
	}
	return &ProfileResponse{
		ID:        user.ID.String(),
		Email:     user.Email,
		Username:  user.Username,
		Status:    user.Status,
		CreatedAt: user.CreatedAt.Format(time.RFC3339),
	}, nil
}

func (s *Service) UpdatePassword(userID string, req *UpdatePasswordRequest) error {
	if len(req.NewPassword) < 8 {
		return errors.New("new password must be at least 8 characters")
	}

	var user db.User
	if err := s.db.Where("id = ? AND deleted_at IS NULL AND status = 'active'", userID).First(&user).Error; err != nil {
		return errors.New("user not found")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.OldPassword)); err != nil {
		return errors.New("incorrect old password")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), 10)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	return s.db.Model(&user).Update("password_hash", string(hash)).Error
}

func (s *Service) UpdateEmail(userID string, req *UpdateEmailRequest) error {
	// Validate email format
	if _, err := mail.ParseAddress(req.Email); err != nil {
		return errors.New("invalid email format")
	}

	var user db.User
	if err := s.db.Where("id = ? AND deleted_at IS NULL AND status = 'active'", userID).First(&user).Error; err != nil {
		return errors.New("user not found")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		return errors.New("incorrect password")
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		// Check email uniqueness within transaction to prevent TOCTOU race
		var count int64
		tx.Model(&db.User{}).Where("email = ? AND deleted_at IS NULL", req.Email).Count(&count)
		if count > 0 {
			return errors.New("email already in use")
		}

		return tx.Model(&user).Update("email", req.Email).Error
	})
}

func (s *Service) ListKeys(userID string) ([]KeyResponse, error) {
	var tokens []db.Token
	if err := s.db.Where("user_id = ? AND deleted_at IS NULL", userID).Find(&tokens).Error; err != nil {
		return nil, err
	}

	keys := make([]KeyResponse, len(tokens))
	for i, t := range tokens {
		keys[i] = KeyResponse{
			ID:          t.ID.String(),
			Name:        t.Name,
			Key:         t.Key,
			Enabled:     t.Enabled,
			IPWhitelist: t.IPWhitelist,
			ExpiresAt:   formatOptionalTime(t.ExpiresAt),
			Models:      t.Models,
			Permissions: t.Permissions,
			CreatedAt:   t.CreatedAt.Format(time.RFC3339),
		}
	}
	return keys, nil
}

func (s *Service) CreateKey(userID string, req *CreateKeyRequest) (*KeyResponse, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errors.New("name is required")
	}
	var token db.Token
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var keyCount int64
		tx.Model(&db.Token{}).Where("user_id = ? AND deleted_at IS NULL", userID).Count(&keyCount)
		if keyCount >= int64(s.maxKeysPerUser) {
			return errors.New("maximum number of API keys reached")
		}
		var err error
		token, err = createUserTokenTx(tx, userID, req)
		if err != nil {
			return err
		}
		return s.attachActivePlanToNewTokenTx(tx, userID, &token)
	})
	if err != nil {
		return nil, err
	}
	return &KeyResponse{
		ID:          token.ID.String(),
		Name:        token.Name,
		Key:         token.Key,
		Enabled:     token.Enabled,
		IPWhitelist: token.IPWhitelist,
		ExpiresAt:   formatOptionalTime(token.ExpiresAt),
		Models:      token.Models,
		Permissions: token.Permissions,
		CreatedAt:   token.CreatedAt.Format(time.RFC3339),
	}, nil
}

func createUserTokenTx(tx *gorm.DB, userID string, req *CreateKeyRequest) (db.Token, error) {
	expiresAt, err := parseOptionalTime(req.ExpiresAt)
	if err != nil {
		return db.Token{}, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "默认密钥"
	}
	keyUUID := uuid.New().String()
	token := db.Token{
		UserID:      userID,
		Name:        name,
		Key:         "sk-relay-" + strings.ReplaceAll(keyUUID, "-", ""),
		Enabled:     true,
		IPWhitelist: normalizeCSV(req.IPWhitelist),
		ExpiresAt:   expiresAt,
		Models:      normalizeCSV(req.Models),
		Permissions: normalizeCSV(req.Permissions),
	}
	if err := tx.Create(&token).Error; err != nil {
		return db.Token{}, err
	}
	return token, nil
}

func (s *Service) attachActivePlanToNewTokenTx(tx *gorm.DB, userID string, token *db.Token) error {
	var existing db.TokenPlan
	if err := tx.Joins("JOIN tokens ON tokens.id = token_plans.token_id AND tokens.user_id = ? AND tokens.deleted_at IS NULL", userID).
		Where("token_plans.starts_at <= ? AND token_plans.expires_at > ?", time.Now(), time.Now()).
		Order("token_plans.created_at DESC").
		First(&existing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}

	var plan db.Plan
	if err := tx.Where("id = ? AND enabled = ? AND deleted_at IS NULL", existing.PlanID, true).First(&plan).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}

	tokenPlan := db.TokenPlan{TokenID: token.ID, PlanID: plan.ID, StartsAt: existing.StartsAt, ExpiresAt: existing.ExpiresAt}
	return tx.Create(&tokenPlan).Error
}

func (s *Service) DeleteKey(userID, keyID string) error {
	// Verify ownership
	var token db.Token
	if err := s.db.Where("id = ? AND user_id = ? AND deleted_at IS NULL", keyID, userID).First(&token).Error; err != nil {
		return errors.New("key not found")
	}

	return s.db.Model(&token).Update("deleted_at", time.Now()).Error
}

func parseOptionalTime(value *string) (*time.Time, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*value))
	if err != nil {
		return nil, errors.New("expires_at must be RFC3339")
	}
	if !parsed.After(time.Now()) {
		return nil, errors.New("expires_at must be in the future")
	}
	return &parsed, nil
}

func formatOptionalTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.Format(time.RFC3339)
	return &formatted
}

func normalizeCSV(value string) string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return strings.Join(out, ",")
}

func (s *Service) GetUsage(userID string) (*UsageSummaryResponse, error) {
	var totals struct {
		TotalRequests    int64
		FailedRequests   int64
		TotalTokens      int64
		PromptTokens     int64
		CompletionTokens int64
	}
	err := s.db.Model(&db.Log{}).
		Select(`COUNT(*) as total_requests,
			COALESCE(SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END), 0) as failed_requests,
			COALESCE(SUM(total_tokens), 0) as total_tokens,
			COALESCE(SUM(prompt_tokens), 0) as prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) as completion_tokens`).
		Joins("JOIN tokens ON tokens.id = logs.token_id AND tokens.user_id = ?", userID).
		Scan(&totals).Error
	if err != nil {
		return nil, err
	}

	var byModel []UsageModelPoint
	if err := s.db.Model(&db.Log{}).
		Select("model, COUNT(*) as requests, COALESCE(SUM(total_tokens), 0) as total_tokens").
		Joins("JOIN tokens ON tokens.id = logs.token_id AND tokens.user_id = ?", userID).
		Group("model").
		Order("COUNT(*) DESC").
		Limit(10).
		Scan(&byModel).Error; err != nil {
		return nil, err
	}

	var daily []UsageDailyPoint
	if err := s.db.Model(&db.Log{}).
		Select("TO_CHAR(DATE_TRUNC('day', logs.created_at), 'YYYY-MM-DD') as date, COUNT(*) as requests, COALESCE(SUM(total_tokens), 0) as total_tokens").
		Joins("JOIN tokens ON tokens.id = logs.token_id AND tokens.user_id = ?", userID).
		Where("logs.created_at >= ?", time.Now().AddDate(0, 0, -6)).
		Group("DATE_TRUNC('day', logs.created_at)").
		Order("date ASC").
		Scan(&daily).Error; err != nil {
		return nil, err
	}

	successRate := 1.0
	if totals.TotalRequests > 0 {
		successRate = float64(totals.TotalRequests-totals.FailedRequests) / float64(totals.TotalRequests)
	}
	return &UsageSummaryResponse{
		TotalRequests:    totals.TotalRequests,
		FailedRequests:   totals.FailedRequests,
		SuccessRate:      successRate,
		TotalTokens:      totals.TotalTokens,
		PromptTokens:     totals.PromptTokens,
		CompletionTokens: totals.CompletionTokens,
		ByModel:          byModel,
		Daily:            daily,
	}, nil
}

func (s *Service) GetUsageLogs(userID string, page, limit int) (*UsageLogsResponse, error) {
	offset := (page - 1) * limit

	var total int64
	if err := s.db.Model(&db.Log{}).
		Joins("JOIN tokens ON tokens.id = logs.token_id AND tokens.user_id = ?", userID).
		Count(&total).Error; err != nil {
		return nil, fmt.Errorf("failed to count usage logs: %w", err)
	}

	var logs []db.Log
	if err := s.db.Table("logs").
		Select("logs.id, logs.created_at, logs.token_id, logs.client_ip, logs.channel_id, logs.account_id, logs.model, logs.is_stream, logs.prompt_tokens, logs.completion_tokens, logs.total_tokens, logs.latency_ms, logs.status_code, logs.error_message").
		Joins("JOIN tokens ON tokens.id = logs.token_id AND tokens.user_id = ?", userID).
		Offset(offset).Limit(limit).
		Order("logs.created_at DESC").
		Scan(&logs).Error; err != nil {
		return nil, fmt.Errorf("failed to query usage logs: %w", err)
	}

	items := make([]UsageLogItem, len(logs))
	for i, log := range logs {
		items[i] = UsageLogItem{
			ID:               log.ID,
			CreatedAt:        log.CreatedAt.Format(time.RFC3339),
			Model:            log.Model,
			ClientIP:         log.ClientIP,
			IsStream:         log.IsStream,
			PromptTokens:     log.PromptTokens,
			CompletionTokens: log.CompletionTokens,
			TotalTokens:      log.TotalTokens,
			LatencyMs:        log.LatencyMs,
			StatusCode:       log.StatusCode,
			ErrorMessage:     log.ErrorMessage,
		}
	}
	return &UsageLogsResponse{Total: total, Page: page, Limit: limit, Logs: items}, nil
}

func (s *Service) GetSubscription(userID string) (*SubscriptionResponse, error) {
	var tokenPlan db.TokenPlan
	if err := s.db.Joins("JOIN tokens ON tokens.id = token_plans.token_id AND tokens.user_id = ? AND tokens.deleted_at IS NULL", userID).
		Where("token_plans.starts_at <= ? AND token_plans.expires_at > ?", time.Now(), time.Now()).
		Order("token_plans.created_at DESC").
		First(&tokenPlan).Error; err != nil {
		return nil, errors.New("no active subscription")
	}

	var plan db.Plan
	if err := s.db.Where("id = ? AND enabled = ? AND deleted_at IS NULL", tokenPlan.PlanID, true).First(&plan).Error; err != nil {
		return nil, errors.New("plan not found")
	}

	remainingQuota := plan.TokenQuota - tokenPlan.UsedQuota
	if remainingQuota < 0 {
		remainingQuota = 0
	}
	windows, err := s.subscriptionWindows(tokenPlan.TokenID, plan.PolicyID)
	if err != nil {
		return nil, err
	}

	return &SubscriptionResponse{
		PlanID:         tokenPlan.PlanID.String(),
		PlanName:       plan.Name,
		PlanType:       plan.Type,
		TokenQuota:     plan.TokenQuota,
		UsedQuota:      tokenPlan.UsedQuota,
		RemainingQuota: remainingQuota,
		Windows:        windows,
		StartsAt:       tokenPlan.StartsAt.Format(time.RFC3339),
		ExpiresAt:      tokenPlan.ExpiresAt.Format(time.RFC3339),
		Status:         "active",
	}, nil
}

func (s *Service) subscriptionWindows(tokenID uuid.UUID, policyID *uuid.UUID) ([]SubscriptionWindow, error) {
	if policyID == nil || *policyID == uuid.Nil {
		return []SubscriptionWindow{}, nil
	}
	var policy db.AccessPolicy
	if err := s.db.Where("id = ? AND enabled = true AND deleted_at IS NULL", *policyID).First(&policy).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return []SubscriptionWindow{}, nil
		}
		return nil, err
	}
	now := time.Now().UTC()
	specs := []struct {
		name  string
		limit int
		start time.Time
		end   time.Time
	}{
		{name: "hour", limit: policy.HourlyLimit, start: currentHour(now), end: currentHour(now).Add(time.Hour)},
		{name: "week", limit: policy.WeeklyLimit, start: currentWeek(now), end: currentWeek(now).AddDate(0, 0, 7)},
		{name: "month", limit: policy.MonthlyLimit, start: currentMonth(now), end: currentMonth(now).AddDate(0, 1, 0)},
	}
	windows := make([]SubscriptionWindow, 0, len(specs))
	for _, spec := range specs {
		var usage db.PolicyUsageWindow
		err := s.db.Where("policy_id = ? AND token_id = ? AND window_type = ? AND window_start = ?", *policyID, tokenID, spec.name, spec.start).First(&usage).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		used := 0
		if err == nil {
			used = usage.UsedCount
		}
		remaining := spec.limit - used
		if remaining < 0 {
			remaining = 0
		}
		windows = append(windows, SubscriptionWindow{
			Type:      spec.name,
			Limit:     spec.limit,
			Used:      used,
			Remaining: remaining,
			ResetAt:   spec.end.Format(time.RFC3339),
		})
	}
	return windows, nil
}

func currentHour(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, time.UTC)
}

func currentWeek(now time.Time) time.Time {
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(weekday - 1))
}

func currentMonth(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func (s *Service) RedeemCode(userID, code string) (*SubscriptionResponse, error) {
	var subscription *SubscriptionResponse
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var redeemCode db.RedeemCode
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("code = ? AND status = ?", code, "active").
			First(&redeemCode).Error; err != nil {
			return errors.New("invalid or used code")
		}
		if redeemCode.MaxUses <= 0 {
			redeemCode.MaxUses = 1
		}
		if redeemCode.UsedCount >= redeemCode.MaxUses {
			return errors.New("invalid or used code")
		}
		var plan db.Plan
		if err := tx.Where("id = ? AND enabled = true AND deleted_at IS NULL", redeemCode.PlanID).First(&plan).Error; err != nil {
			return errors.New("plan not found")
		}
		now := time.Now()
		var currentPlan db.Plan
		currentErr := tx.Table("plans").
			Select("plans.*").
			Joins("JOIN token_plans ON token_plans.plan_id = plans.id").
			Joins("JOIN tokens ON tokens.id = token_plans.token_id AND tokens.user_id = ? AND tokens.deleted_at IS NULL", userID).
			Where("token_plans.starts_at <= ? AND token_plans.expires_at > ?", now, now).
			Where("plans.enabled = true AND plans.deleted_at IS NULL").
			Order("token_plans.created_at DESC").
			First(&currentPlan).Error
		if currentErr != nil && !errors.Is(currentErr, gorm.ErrRecordNotFound) {
			return currentErr
		}
		if currentErr == nil && plan.TokenQuota <= currentPlan.TokenQuota {
			return errors.New("can only upgrade to a higher quota plan")
		}
		if err := tx.Model(&db.TokenPlan{}).
			Joins("JOIN tokens ON tokens.id = token_plans.token_id AND tokens.user_id = ? AND tokens.deleted_at IS NULL", userID).
			Where("token_plans.expires_at > ?", now).
			Update("expires_at", now).Error; err != nil {
			return err
		}
		tokens, err := s.activeTokensOrCreateDefaultTx(tx, userID)
		if err != nil {
			return errors.New("failed to find API keys")
		}
		startsAt := now
		expiresAt := startsAt.AddDate(0, 0, plan.DurationDays)
		for _, t := range tokens {
			if err := tx.Create(&db.TokenPlan{TokenID: t.ID, PlanID: plan.ID, StartsAt: startsAt, ExpiresAt: expiresAt}).Error; err != nil {
				return err
			}
		}
		redeemCode.UsedBy = &userID
		redeemCode.UsedAt = &now
		redeemCode.UsedCount++
		if redeemCode.UsedCount >= redeemCode.MaxUses {
			redeemCode.Status = "used"
		}
		if err := tx.Save(&redeemCode).Error; err != nil {
			return err
		}
		subscription = &SubscriptionResponse{PlanID: plan.ID.String(), PlanName: plan.Name, PlanType: plan.Type, TokenQuota: plan.TokenQuota, UsedQuota: 0, RemainingQuota: plan.TokenQuota, Windows: []SubscriptionWindow{}, StartsAt: startsAt.Format(time.RFC3339), ExpiresAt: expiresAt.Format(time.RFC3339), Status: "active"}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return subscription, nil
}

func (s *Service) activeTokensOrCreateDefaultTx(tx *gorm.DB, userID string) ([]db.Token, error) {
	var tokens []db.Token
	if err := tx.Where("user_id = ? AND deleted_at IS NULL", userID).Find(&tokens).Error; err != nil {
		return nil, err
	}
	if len(tokens) > 0 {
		return tokens, nil
	}
	if s.maxKeysPerUser <= 0 {
		return nil, errors.New("maximum number of API keys reached")
	}
	token, err := createUserTokenTx(tx, userID, &CreateKeyRequest{Name: "默认密钥"})
	if err != nil {
		return nil, err
	}
	return []db.Token{token}, nil
}

func (s *Service) issueTokenPair(userID, username, passwordHash string) (*LoginResponse, error) {
	now := time.Now()
	version := auth.SecretVersion(passwordHash)
	accessToken, err := auth.GenerateTokenWithVersion(s.jwtSecret, userID, username, auth.TokenTypeUser, s.accessTokenExpiry, version)
	if err != nil {
		return nil, err
	}
	refreshToken, err := auth.GenerateTokenWithVersion(s.jwtSecret, userID, username, auth.TokenTypeUserRefresh, s.refreshTokenExpiry, version)
	if err != nil {
		return nil, err
	}
	return &LoginResponse{
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		AccessExpiresAt:  now.Add(s.accessTokenExpiry).Unix(),
		RefreshExpiresAt: now.Add(s.refreshTokenExpiry).Unix(),
	}, nil
}
