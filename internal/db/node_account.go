package db

import "github.com/google/uuid"

type NodeAccount struct {
	Base
	RelayNodeID uuid.UUID `gorm:"type:uuid;index;not null;uniqueIndex:idx_node_account" json:"relay_node_id"`
	AccountID   uuid.UUID `gorm:"type:uuid;index;not null;uniqueIndex:idx_node_account" json:"account_id"`
	Weight      int       `gorm:"default:0" json:"weight"`
	Enabled     bool      `gorm:"default:true;index" json:"enabled"`
}

func (NodeAccount) TableName() string { return "node_accounts" }
