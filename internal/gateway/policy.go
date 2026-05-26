package gateway

import (
	"fmt"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type policyRuntime struct {
	policy db.AccessPolicy
	ok     bool
}

func (g *Gateway) loadPolicy(token db.Token) (db.AccessPolicy, bool, string, error) {
	policyID, hasPlan, planType, err := g.planPolicyID(token.ID)
	if err != nil {
		return db.AccessPolicy{}, false, "", err
	}
	if !hasPlan {
		return db.AccessPolicy{}, false, "", fmt.Errorf("no active subscription")
	}
	if policyID == nil || *policyID == uuid.Nil {
		return db.AccessPolicy{}, false, planType, nil
	}
	var policy db.AccessPolicy
	if err := g.db.Where("id = ? AND enabled = true AND deleted_at IS NULL", *policyID).First(&policy).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return db.AccessPolicy{}, false, "", fmt.Errorf("access policy disabled or not found")
		}
		return db.AccessPolicy{}, false, "", err
	}
	return policy, true, planType, nil
}

func (g *Gateway) planPolicyID(tokenID uuid.UUID) (*uuid.UUID, bool, string, error) {
	var row struct {
		PlanID   uuid.UUID
		PlanType string
		PolicyID *uuid.UUID
	}
	if err := g.db.Table("token_plans").
		Select("plans.id AS plan_id, plans.type AS plan_type, plans.policy_id").
		Joins("JOIN tokens ON tokens.user_id = token_plans.user_id AND tokens.id = ? AND tokens.enabled = true AND tokens.deleted_at IS NULL", tokenID).
		Joins("JOIN plans ON plans.id = token_plans.plan_id AND plans.enabled = true AND plans.deleted_at IS NULL").
		Where("token_plans.starts_at <= ? AND token_plans.expires_at > ?", time.Now(), time.Now()).
		Order("token_plans.created_at DESC").
		Limit(1).
		Scan(&row).Error; err != nil {
		return nil, false, "", err
	}
	if row.PlanID == uuid.Nil {
		return nil, false, "", nil
	}
	if row.PolicyID == nil || *row.PolicyID == uuid.Nil {
		return nil, true, row.PlanType, nil
	}
	return row.PolicyID, true, row.PlanType, nil
}
