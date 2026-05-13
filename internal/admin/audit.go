package admin

import (
	"encoding/json"
	"log"
	"time"

	"github.com/AutoCONFIG/cli-relay/internal/db"
	"gorm.io/gorm"
)

// AuditWriter writes audit logs asynchronously.
type AuditWriter struct {
	db   *gorm.DB
	ch   chan db.AuditLog
	done chan struct{}
}

func NewAuditWriter(database *gorm.DB) *AuditWriter {
	aw := &AuditWriter{
		db:   database,
		ch:   make(chan db.AuditLog, 256),
		done: make(chan struct{}),
	}
	go aw.run()
	return aw
}

func (aw *AuditWriter) Write(entry db.AuditLog) {
	select {
	case aw.ch <- entry:
	default:
		log.Println("audit channel full, dropping entry")
	}
}

func (aw *AuditWriter) Stop() {
	close(aw.ch)
	<-aw.done
}

func (aw *AuditWriter) run() {
	defer close(aw.done)
	batch := make([]db.AuditLog, 0, 32)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := aw.db.Create(&batch).Error; err != nil {
			log.Printf("audit flush error: %v", err)
		}
		batch = batch[:0]
	}

	for {
		select {
		case entry, ok := <-aw.ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, entry)
			if len(batch) >= 32 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// LogAudit is a convenience function to queue an audit entry.
func (aw *AuditWriter) LogAudit(user, action, resource, resourceID string, oldVal, newVal interface{}, ip string) {
	var oldJSON, newJSON string
	if oldVal != nil {
		b, _ := json.Marshal(oldVal)
		oldJSON = string(b)
	}
	if newVal != nil {
		b, _ := json.Marshal(newVal)
		newJSON = string(b)
	}
	aw.Write(db.AuditLog{
		User:       user,
		Action:     action,
		Resource:   resource,
		ResourceID: resourceID,
		OldValue:   oldJSON,
		NewValue:   newJSON,
		IPAddress:  ip,
	})
}

// CleanupOldLogs deletes logs older than retention days.
func CleanupOldLogs(database *gorm.DB, retentionDays int) error {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	if err := database.Where("created_at < ?", cutoff).Delete(&db.Log{}).Error; err != nil {
		return err
	}
	return database.Where("created_at < ?", cutoff).Delete(&db.AuditLog{}).Error
}
