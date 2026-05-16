package admin

import (
	"github.com/AutoCONFIG/cli-relay/internal/db"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// createAuditLog writes an audit log entry to the database.
func createAuditLog(database *gorm.DB, action, resource, resourceID, user string) {
	entry := db.AuditLog{
		User:       user,
		Action:     action,
		Resource:   resource,
		ResourceID: resourceID,
	}
	if err := database.Create(&entry).Error; err != nil {
		// Audit logging is best-effort; do not fail the request
		_ = err
	}
}

// auditCreate logs a create action.
func auditCreate(database *gorm.DB, resource string, id uuid.UUID, user string) {
	createAuditLog(database, "create", resource, id.String(), user)
}

// auditUpdate logs an update action.
func auditUpdate(database *gorm.DB, resource string, id uuid.UUID, user string) {
	createAuditLog(database, "update", resource, id.String(), user)
}

// auditDelete logs a delete action.
func auditDelete(database *gorm.DB, resource string, id uuid.UUID, user string) {
	createAuditLog(database, "delete", resource, id.String(), user)
}
