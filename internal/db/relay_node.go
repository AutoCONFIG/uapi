package db

import "time"

type RelayNode struct {
	Base
	Name               string     `gorm:"size:100;not null" json:"name"`
	BaseURL            string     `gorm:"size:500;not null" json:"base_url"`
	Region             string     `gorm:"size:50" json:"region"`
	EgressIP           string     `gorm:"size:100" json:"egress_ip"`
	Weight             int        `gorm:"default:0" json:"weight"`
	MaxConcurrency     int        `gorm:"default:0" json:"max_concurrency"`
	Status             string     `gorm:"size:20;default:'disabled';index" json:"status"`
	HealthStatus       string     `gorm:"size:20;default:'healthy';index" json:"health_status"`
	CurrentConcurrency int        `gorm:"default:0" json:"current_concurrency"`
	AvgLatencyMS       int        `gorm:"default:0" json:"avg_latency_ms"`
	ErrorRate          string     `gorm:"size:32;default:'0'" json:"error_rate"`
	LastHeartbeatAt    *time.Time `json:"last_heartbeat_at,omitempty"`
}

func (RelayNode) TableName() string { return "relay_nodes" }
