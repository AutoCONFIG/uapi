package user

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/auth"
	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct {
	db             *gorm.DB
	jwtSecret      string
	jwtExpiry      time.Duration
	maxKeysPerUser int
}

func NewService(database *gorm.DB, jwtSecret string, jwtExpiry time.Duration, maxKeysPerUser int) *Service {
	return &Service{db: database, jwtSecret: jwtSecret, jwtExpiry: jwtExpiry, maxKeysPerUser: maxKeysPerUser}
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
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Generate JWT
	token, err := s.generateUserToken(newUser.ID.String(), newUser.Username)
	if err != nil {
		return nil, err
	}

	return &LoginResponse{Token: token, ExpiresAt: time.Now().Add(s.jwtExpiry).Unix()}, nil
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

	token, err := s.generateUserToken(user.ID.String(), user.Username)
	if err != nil {
		return nil, err
	}

	return &LoginResponse{Token: token, ExpiresAt: time.Now().Add(s.jwtExpiry).Unix()}, nil
}

func (s *Service) RefreshToken(tokenStr string) (*LoginResponse, error) {
	// Parse the old token - we allow expired tokens for refresh
	userID, username, err := s.parseTokenAllowExpired(tokenStr)
	if err != nil {
		return nil, errors.New("invalid token")
	}

	// Verify user still exists and is active
	var user db.User
	if err := s.db.Where("id = ? AND deleted_at IS NULL AND status = 'active'", userID).First(&user).Error; err != nil {
		return nil, errors.New("user not found or inactive")
	}

	token, err := s.generateUserToken(userID, username)
	if err != nil {
		return nil, err
	}

	return &LoginResponse{Token: token, ExpiresAt: time.Now().Add(s.jwtExpiry).Unix()}, nil
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
		Balance:   user.Balance,
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
			Key:         maskKey(t.Key),
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
	expiresAt, err := parseOptionalTime(req.ExpiresAt)
	if err != nil {
		return nil, err
	}
	var token db.Token
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var keyCount int64
		tx.Model(&db.Token{}).Where("user_id = ? AND deleted_at IS NULL", userID).Count(&keyCount)
		if keyCount >= int64(s.maxKeysPerUser) {
			return errors.New("maximum number of API keys reached")
		}
		keyUUID := uuid.New().String()
		key := "sk-relay-" + strings.ReplaceAll(keyUUID, "-", "")
		token = db.Token{
			UserID:      userID,
			Name:        name,
			Key:         key,
			Enabled:     true,
			IPWhitelist: normalizeCSV(req.IPWhitelist),
			ExpiresAt:   expiresAt,
			Models:      normalizeCSV(req.Models),
			Permissions: normalizeCSV(req.Permissions),
		}
		return tx.Create(&token).Error
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
		Order("total_tokens DESC").
		Limit(12).
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
	s.db.Model(&db.Log{}).
		Joins("JOIN tokens ON tokens.id = logs.token_id AND tokens.user_id = ?", userID).
		Count(&total)

	var logs []db.Log
	s.db.Model(&db.Log{}).
		Joins("JOIN tokens ON tokens.id = logs.token_id AND tokens.user_id = ?", userID).
		Offset(offset).Limit(limit).
		Order("created_at DESC").
		Find(&logs)

	items := make([]UsageLogItem, len(logs))
	for i, log := range logs {
		items[i] = UsageLogItem{
			ID:               log.ID,
			CreatedAt:        log.CreatedAt.Format(time.RFC3339),
			Model:            log.Model,
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

func (s *Service) ListPlans() ([]map[string]interface{}, error) {
	var plans []db.Plan
	if err := s.db.Where("enabled = ? AND deleted_at IS NULL", true).Find(&plans).Error; err != nil {
		return nil, err
	}

	result := make([]map[string]interface{}, len(plans))
	for i, p := range plans {
		result[i] = map[string]interface{}{
			"id":           p.ID.String(),
			"name":         p.Name,
			"type":         p.Type,
			"token_quota":  p.TokenQuota,
			"enabled":      p.Enabled,
		}
	}
	return result, nil
}

func (s *Service) GetSubscription(userID string) (*SubscriptionResponse, error) {
	var tokenPlan db.TokenPlan
	if err := s.db.Joins("JOIN tokens ON tokens.id = token_plans.token_id AND tokens.user_id = ? AND tokens.deleted_at IS NULL", userID).
		First(&tokenPlan).Error; err != nil {
		return nil, errors.New("no active subscription")
	}

	var plan db.Plan
	if err := s.db.Where("id = ? AND enabled = ? AND deleted_at IS NULL", tokenPlan.PlanID, true).First(&plan).Error; err != nil {
		return nil, errors.New("plan not found")
	}

	return &SubscriptionResponse{
		PlanID:   tokenPlan.PlanID.String(),
		PlanName: plan.Name,
		PlanType: plan.Type,
		Status:   "active",
	}, nil
}

func (s *Service) Subscribe(userID, planID string) error {
	// Verify plan exists
	var plan db.Plan
	if err := s.db.Where("id = ? AND enabled = ? AND deleted_at IS NULL", planID, true).First(&plan).Error; err != nil {
		return errors.New("plan not found")
	}

	// Wrap existence check and create in a transaction to prevent race
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var existingCount int64
		tx.Model(&db.TokenPlan{}).
			Joins("JOIN tokens ON tokens.id = token_plans.token_id AND tokens.user_id = ? AND tokens.deleted_at IS NULL", userID).
			Count(&existingCount)
		if existingCount > 0 {
			return errors.New("already subscribed to a plan")
		}

		// Attach subscription to ALL active tokens for this user
		var tokens []db.Token
		if err := tx.Where("user_id = ? AND deleted_at IS NULL", userID).Find(&tokens).Error; err != nil {
			return errors.New("failed to find API keys")
		}
		if len(tokens) == 0 {
			return errors.New("no API key found")
		}
		for _, t := range tokens {
			tokenPlan := db.TokenPlan{
				TokenID: t.ID,
				PlanID:  plan.ID,
			}
			if err := tx.Create(&tokenPlan).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

func (s *Service) RedeemCode(userID, code string) (int64, error) {
	var redeemCode db.RedeemCode
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("code = ? AND status = ? AND expires_at > ?", code, "active", time.Now()).
			First(&redeemCode).Error; err != nil {
			return errors.New("invalid or expired code")
		}
		now := time.Now()
		redeemCode.UsedBy = &userID
		redeemCode.UsedAt = &now
		redeemCode.Status = "used"
		if err := tx.Save(&redeemCode).Error; err != nil {
			return err
		}
		return tx.Model(&db.User{}).Where("id = ?", userID).Update("balance", gorm.Expr("balance + ?", redeemCode.Value)).Error
	})
	if err != nil {
		return 0, err
	}
	return redeemCode.Value, nil
}

// Helper: generate user JWT using auth package
func (s *Service) generateUserToken(userID, username string) (string, error) {
	return auth.GenerateToken(s.jwtSecret, userID, username, auth.TokenTypeUser, s.jwtExpiry)
}

// Helper: parse token allowing expired (for refresh)
func (s *Service) parseTokenAllowExpired(tokenStr string) (string, string, error) {
	claims, err := auth.ParseToken(s.jwtSecret, tokenStr)
	if err != nil {
		if errors.Is(err, auth.ErrExpiredToken) {
			// Expired is OK for refresh — verify signature but skip expiration check
			token, parseErr := jwt.ParseWithClaims(tokenStr, &auth.Claims{}, func(t *jwt.Token) (interface{}, error) {
				return []byte(s.jwtSecret), nil
			}, jwt.WithoutClaimsValidation())
			if parseErr != nil {
				return "", "", parseErr
			}
			if c, ok := token.Claims.(*auth.Claims); ok {
				return c.UserID, c.Username, nil
			}
			return "", "", fmt.Errorf("invalid token claims")
		}
		return "", "", err
	}
	return claims.UserID, claims.Username, nil
}

// maskKey returns a masked version of the API key showing only prefix and last 4 chars.
func maskKey(key string) string {
	if len(key) <= 8 {
		return key
	}
	return key[:8] + "****" + key[len(key)-4:]
}
