package db

import "github.com/google/uuid"

type NodeChannel struct {
	Base
	RelayNodeID uuid.UUID `gorm:"type:uuid;index;not null;uniqueIndex:idx_node_channel" json:"relay_node_id"`
	ChannelID   uuid.UUID `gorm:"type:uuid;index;not null;uniqueIndex:idx_node_channel" json:"channel_id"`
	Weight      int       `gorm:"default:0" json:"weight"`
	Enabled     bool      `gorm:"default:true;index" json:"enabled"`
}

func (NodeChannel) TableName() string { return "node_channels" }
