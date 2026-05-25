package admin

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
)

// createAuditLog writes an audit log entry to the database.
func createAuditLog(database *gorm.DB, action, resource, resourceID, user string) {
	createAuditLogWithValues(database, action, resource, resourceID, user, "", "", "")
}

func createAuditLogWithValues(database *gorm.DB, action, resource, resourceID, user, ip, oldValue, newValue string) {
	entry := db.AuditLog{
		User:       user,
		Action:     action,
		Resource:   resource,
		ResourceID: resourceID,
		OldValue:   oldValue,
		NewValue:   newValue,
		IPAddress:  ip,
	}
	if err := database.Create(&entry).Error; err != nil {
		// Audit logging is best-effort; do not fail the request.
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

func auditCreateCtx(database *gorm.DB, resource string, id uuid.UUID, user string, ctx *fasthttp.RequestCtx, details map[string]interface{}) {
	createAuditLogWithValues(database, "create", resource, id.String(), user, auditIP(ctx), "", auditJSON(details))
}

func auditUpdateCtx(database *gorm.DB, resource string, id uuid.UUID, user string, ctx *fasthttp.RequestCtx, changes map[string]interface{}) {
	createAuditLogWithValues(database, "update", resource, id.String(), user, auditIP(ctx), "", auditJSON(sanitizeAuditMap(changes)))
}

func auditDeleteCtx(database *gorm.DB, resource string, id uuid.UUID, user string, ctx *fasthttp.RequestCtx, details map[string]interface{}) {
	createAuditLogWithValues(database, "delete", resource, id.String(), user, auditIP(ctx), auditJSON(details), "")
}

func auditIP(ctx *fasthttp.RequestCtx) string {
	if ctx == nil {
		return ""
	}
	return ctx.RemoteIP().String()
}

func auditJSON(value interface{}) string {
	if value == nil {
		return ""
	}
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(b)
}

func sanitizeAuditMap(values map[string]interface{}) map[string]interface{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(values))
	for key, value := range values {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "password") || strings.Contains(lower, "credential") || strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "key") {
			out[key] = "<redacted>"
			continue
		}
		out[key] = value
	}
	return out
}
