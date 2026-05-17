package relay

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/AutoCONFIG/cli-relay/internal/crypto"
	"github.com/AutoCONFIG/cli-relay/internal/db"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

// refreshGroup deduplicates concurrent OAuth refresh calls for the same account.
var refreshGroup singleflight.Group

// EnsureValidCredentials checks if account credentials are valid, refreshes OAuth tokens if needed.
// Returns the decrypted credential string ready for API use.
func EnsureValidCredentials(account *db.Account, database *gorm.DB) (string, error) {
	if account.CredType == "api_key" || account.CredType == "" {
		return crypto.Decrypt(account.Credentials)
	}

	// OAuth token — check expiry
	if account.TokenExpiry != nil && time.Now().After(*account.TokenExpiry) {
		accountID := account.ID.String()
		v, err, _ := refreshGroup.Do(accountID, func() (interface{}, error) {
			return refreshOAuthToken(account, database)
		})
		if err != nil {
			return "", err
		}
		return v.(string), nil
	}

	return crypto.Decrypt(account.Credentials)
}

func refreshOAuthToken(account *db.Account, database *gorm.DB) (string, error) {
	refreshToken, err := crypto.Decrypt(account.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("decrypt refresh token: %w", err)
	}

	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {account.ClientID},
	}

	resp, err := (&http.Client{Timeout: 15 * time.Second}).PostForm(account.TokenURL, data)
	if err != nil {
		return "", fmt.Errorf("refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Error        string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode refresh response: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("refresh failed: %s", result.Error)
	}

	// Async update database — fire and forget
	go func() {
		newExpiry := time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
		newCreds, encErr := crypto.Encrypt(result.AccessToken)
		if encErr != nil {
			log.Printf("failed to encrypt refreshed credentials for account %s: %v", account.ID, encErr)
			return
		}
		updates := map[string]interface{}{
			"credentials":  newCreds,
			"token_expiry": newExpiry,
		}
		if result.RefreshToken != "" {
			newRefresh, encErr := crypto.Encrypt(result.RefreshToken)
			if encErr == nil {
				updates["refresh_token"] = newRefresh
			}
		}
		if err := database.Model(&db.Account{}).Where("id = ?", account.ID).Updates(updates).Error; err != nil {
			log.Printf("failed to update refreshed credentials for account %s: %v", account.ID, err)
			return
		}
		// Update in-memory state so subsequent requests use the new credentials
		account.Credentials = newCreds
		account.TokenExpiry = &newExpiry
	}()

	return result.AccessToken, nil
}
