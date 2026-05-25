package db

import "github.com/google/uuid"

type NodeChannel struct {
	Base
	RelayNodeID uuid.UUID `gorm:"type:uuid;index;not null;uniqueIndex:idx_node_channel_active,where:deleted_at IS NULL" json:"relay_node_id"`
	ChannelID   uuid.UUID `gorm:"type:uuid;index;not null;uniqueIndex:idx_node_channel_active,where:deleted_at IS NULL" json:"channel_id"`
	Weight      int       `gorm:"default:0" json:"weight"`
	Enabled     bool      `gorm:"default:true;index" json:"enabled"`
}

func (NodeChannel) TableName() string { return "node_channels" }
