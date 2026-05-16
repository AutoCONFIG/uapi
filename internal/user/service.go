package user

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/AutoCONFIG/cli-relay/internal/auth"
	"github.com/AutoCONFIG/cli-relay/internal/db"
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

	// Validate password length
	if len(req.Password) < 6 {
		return nil, errors.New("password must be at least 6 characters")
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
	var token db.Token
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var keyCount int64
		tx.Model(&db.Token{}).Where("user_id = ? AND deleted_at IS NULL", userID).Count(&keyCount)
		if keyCount >= int64(s.maxKeysPerUser) {
			return errors.New("maximum number of API keys reached")
		}
		keyUUID := uuid.New().String()
		key := "sk-relay-" + strings.ReplaceAll(keyUUID, "-", "")
		token = db.Token{
			UserID:  userID,
			Name:    req.Name,
			Key:     key,
			Enabled: true,
		}
		return tx.Create(&token).Error
	})
	if err != nil {
		return nil, err
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
	if err := s.db.Where("id = ? AND user_id = ? AND deleted_at IS NULL", keyID, userID).First(&token).Error; err != nil {
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
	if err := s.db.Where("enabled = ? AND deleted_at IS NULL", true).Find(&plans).Error; err != nil {
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

		// Find a token to attach subscription to (use the first one)
		var token db.Token
		if err := tx.Where("user_id = ? AND deleted_at IS NULL", userID).First(&token).Error; err != nil {
			return errors.New("no API key found")
		}

		tokenPlan := db.TokenPlan{
			TokenID: token.ID,
			PlanID:  plan.ID,
		}
		return tx.Create(&tokenPlan).Error
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