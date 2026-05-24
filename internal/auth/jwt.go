package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrExpiredToken = errors.New("token expired")
)

type TokenType string

const (
	TokenTypeAdmin        TokenType = "admin"
	TokenTypeAdminRefresh TokenType = "admin_refresh"
	TokenTypeUser         TokenType = "user"
	TokenTypeUserRefresh  TokenType = "user_refresh"
)

type Claims struct {
	jwt.RegisteredClaims
	UserID   string    `json:"uid,omitempty"`
	Username string    `json:"username"`
	Type     TokenType `json:"type"`
	Version  string    `json:"ver,omitempty"`
}

func GenerateToken(secret string, userID, username string, tokenType TokenType, expiry time.Duration) (string, error) {
	return GenerateTokenWithVersion(secret, userID, username, tokenType, expiry, "")
}

func GenerateTokenWithVersion(secret string, userID, username string, tokenType TokenType, expiry time.Duration, version string) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(expiry)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
		UserID:   userID,
		Username: username,
		Type:     tokenType,
		Version:  version,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

func SecretVersion(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:16])
}

func ParseToken(secret string, tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return []byte(secret), nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		return nil, ErrInvalidToken
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
