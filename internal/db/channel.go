package db

type Channel struct {
	Base
	Name        string `gorm:"size:100;not null" json:"name"`
	Type        string `gorm:"size:50;not null" json:"type"`
	Group       string `gorm:"column:channel_group;size:100;default:'默认渠道'" json:"group"`
	Endpoint    string `gorm:"size:500;not null" json:"endpoint"`
	Enabled     bool   `gorm:"default:true" json:"enabled"`
	Models      string `gorm:"type:text" json:"models"`
	Priority    int    `gorm:"default:0" json:"priority"`
	APIFormat   string `gorm:"size:20;default:'standard'" json:"api_format"`
	ForceStream bool   `gorm:"default:false" json:"force_stream"`
	AffinityTTL int    `gorm:"default:0" json:"affinity_ttl"`
}

func (Channel) TableName() string { return "channels" }
