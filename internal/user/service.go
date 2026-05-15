package user

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AutoCONFIG/cli-relay/internal/auth"
	"github.com/AutoCONFIG/cli-relay/internal/db"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
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
	if !strings.Contains(req.Email, "@") {
		return nil, errors.New("invalid email format")
	}

	// Validate password length
	if len(req.Password) < 6 {
		return nil, errors.New("password must be at least 6 characters")
	}

	// Check email uniqueness
	var count int64
	s.db.Model(&db.User{}).Where("email = ?", req.Email).Count(&count)
	if count > 0 {
		return nil, errors.New("email already registered")
	}

	// Check username uniqueness
	s.db.Model(&db.User{}).Where("username = ?", req.Username).Count(&count)
	if count > 0 {
		return nil, errors.New("username already taken")
	}

	// Hash password
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), 10)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	// Create user
	user := db.User{
		Email:        req.Email,
		Username:     req.Username,
		PasswordHash: string(hash),
		Status:       "active",
	}
	if err := s.db.Create(&user).Error; err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}

	// Generate JWT
	token, err := s.generateUserToken(user.ID.String(), user.Username)
	if err != nil {
		return nil, err
	}

	return &LoginResponse{Token: token, ExpiresAt: time.Now().Add(s.jwtExpiry).Unix()}, nil
}

func (s *Service) Login(req *LoginRequest) (*LoginResponse, error) {
	var user db.User
	if err := s.db.Where("email = ?", req.Email).First(&user).Error; err != nil {
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
	if err := s.db.Where("id = ?", userID).First(&user).Error; err != nil {
		return nil, errors.New("user not found")
	}

	token, err := s.generateUserToken(userID, username)
	if err != nil {
		return nil, err
	}

	return &LoginResponse{Token: token, ExpiresAt: time.Now().Add(s.jwtExpiry).Unix()}, nil
}

func (s *Service) GetProfile(userID string) (*ProfileResponse, error) {
	var user db.User
	if err := s.db.Where("id = ?", userID).First(&user).Error; err != nil {
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
	var user db.User
	if err := s.db.Where("id = ?", userID).First(&user).Error; err != nil {
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
	var user db.User
	if err := s.db.Where("id = ?", userID).First(&user).Error; err != nil {
		return errors.New("user not found")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		return errors.New("incorrect password")
	}

	// Check email uniqueness
	var count int64
	s.db.Model(&db.User{}).Where("email = ?", req.Email).Count(&count)
	if count > 0 {
		return errors.New("email already in use")
	}

	return s.db.Model(&user).Update("email", req.Email).Error
}

func (s *Service) ListKeys(userID string) ([]KeyResponse, error) {
	var tokens []db.Token
	if err := s.db.Where("user_id = ?", userID).Find(&tokens).Error; err != nil {
		return nil, err
	}

	keys := make([]KeyResponse, len(tokens))
	for i, t := range tokens {
		keys[i] = KeyResponse{
			ID:        t.ID.String(),
			Name:      t.Name,
			Key:       t.Key,
			Enabled:   t.Enabled,
			CreatedAt: t.CreatedAt.Format(time.RFC3339),
		}
	}
	return keys, nil
}

func (s *Service) CreateKey(userID string, req *CreateKeyRequest) (*KeyResponse, error) {
	// Enforce max keys per user
	var keyCount int64
	s.db.Model(&db.Token{}).Where("user_id = ? AND deleted_at IS NULL", userID).Count(&keyCount)
	if keyCount >= int64(s.maxKeysPerUser) {
		return nil, errors.New("maximum number of API keys reached")
	}

	// Generate key: sk-relay- + UUID without dashes
	keyUUID := uuid.New().String()
	key := "sk-relay-" + strings.ReplaceAll(keyUUID, "-", "")

	token := db.Token{
		UserID:  userID,
		Name:    req.Name,
		Key:     key,
		Enabled: true,
	}
	if err := s.db.Create(&token).Error; err != nil {
		return nil, fmt.Errorf("create key: %w", err)
	}

	return &KeyResponse{
		ID:        token.ID.String(),
		Name:      token.Name,
		Key:       token.Key,
		Enabled:   token.Enabled,
		CreatedAt: token.CreatedAt.Format(time.RFC3339),
	}, nil
}

func (s *Service) DeleteKey(userID, keyID string) error {
	// Verify ownership
	var token db.Token
	if err := s.db.Where("id = ? AND user_id = ?", keyID, userID).First(&token).Error; err != nil {
		return errors.New("key not found")
	}

	// Soft delete
	return s.db.Delete(&token).Error
}

func (s *Service) GetUsage(userID string) (map[string]interface{}, error) {
	// Aggregate usage from logs joined with user's tokens
	var results []map[string]interface{}
	err := s.db.Model(&db.Log{}).
		Select("SUM(total_tokens) as total_tokens, SUM(prompt_tokens) as prompt_tokens, SUM(completion_tokens) as completion_tokens").
		Joins("JOIN tokens ON tokens.id = logs.token_id AND tokens.user_id = ?", userID).
		Scan(&results).Error
	if err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return map[string]interface{}{"total_tokens": 0, "prompt_tokens": 0, "completion_tokens": 0}, nil
	}
	return results[0], nil
}

func (s *Service) GetUsageLogs(userID string, page, limit int) (map[string]interface{}, error) {
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

	return map[string]interface{}{
		"total": total,
		"page":  page,
		"limit": limit,
		"logs":  logs,
	}, nil
}

func (s *Service) ListPlans() ([]map[string]interface{}, error) {
	var plans []db.Plan
	if err := s.db.Where("enabled = ?", true).Find(&plans).Error; err != nil {
		return nil, err
	}

	result := make([]map[string]interface{}, len(plans))
	for i, p := range plans {
		result[i] = map[string]interface{}{
			"id":   p.ID.String(),
			"name": p.Name,
			"type": p.Type,
		}
	}
	return result, nil
}

func (s *Service) GetSubscription(userID string) (*SubscriptionResponse, error) {
	var tokenPlan db.TokenPlan
	if err := s.db.Joins("JOIN tokens ON tokens.id = token_plans.token_id AND tokens.user_id = ?", userID).
		First(&tokenPlan).Error; err != nil {
		return nil, errors.New("no active subscription")
	}

	var plan db.Plan
	s.db.Where("id = ?", tokenPlan.PlanID).First(&plan)

	return &SubscriptionResponse{
		PlanID:   tokenPlan.PlanID.String(),
		PlanName: plan.Name,
		PlanType: plan.Type,
		Status:   "active",
	}, nil
}

func (s *Service) Subscribe(userID, planID string) error {
	// Find a token to attach subscription to (use the first one)
	var token db.Token
	if err := s.db.Where("user_id = ?", userID).First(&token).Error; err != nil {
		return errors.New("no API key found")
	}

	// Verify plan exists
	var plan db.Plan
	if err := s.db.Where("id = ? AND enabled = ?", planID, true).First(&plan).Error; err != nil {
		return errors.New("plan not found")
	}

	tokenPlan := db.TokenPlan{
		TokenID: token.ID,
		PlanID:  plan.ID,
	}
	return s.db.Create(&tokenPlan).Error
}

func (s *Service) RedeemCode(userID, code string) (int64, error) {
	var redeemCode db.RedeemCode
	if err := s.db.Where("code = ? AND status = ? AND expires_at > ?", code, "active", time.Now()).First(&redeemCode).Error; err != nil {
		return 0, errors.New("invalid or expired code")
	}

	now := time.Now()
	redeemCode.UsedBy = &userID
	redeemCode.UsedAt = &now
	redeemCode.Status = "used"
	s.db.Save(&redeemCode)

	// Add value to user balance
	s.db.Model(&db.User{}).Where("id = ?", userID).Update("balance", gorm.Expr("balance + ?", redeemCode.Value))

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
			// Expired is OK for refresh — extract from raw claims
			parser := jwt.NewParser(jwt.WithoutClaimsValidation())
			token, _, parseErr := parser.ParseUnverified(tokenStr, &auth.Claims{})
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