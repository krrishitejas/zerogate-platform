package auth

import (
	"encoding/json"
	"time"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/zerogate/api/internal/db"
)

// AuditMiddleware logs all incoming requests to the DB for SOC2 compliance.
func AuditMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()

		// Proceed with request
		err := c.Next()

		// Skip audit logging for certain high-volume/noise endpoints
		path := c.Path()
		if strings.HasPrefix(path, "/api/v1/health") || strings.HasPrefix(path, "/api/v1/ws/") {
			return err
		}

		// Only log /api/ routes
		if !strings.HasPrefix(path, "/api/") {
			return err
		}

		// Extract user from context (set by JWTMiddleware)
		userID, _ := c.Locals("user_id").(string)
		if userID == "" {
			userID = "anonymous"
		}
		
		userEmail, _ := c.Locals("user_email").(string)
		orgID, _ := c.Locals("org_id").(string)

		method := c.Method()
		status := c.Response().StatusCode()
		duration := time.Since(start).Milliseconds()

		meta := map[string]interface{}{
			"status":   status,
			"duration": duration,
			"query":    c.Request().URI().QueryArgs().String(),
		}
		
		// Optional: capture request body context if safe
		
		metaBytes, _ := json.Marshal(meta)

		auditLog := &db.AuditLog{
			UserID:    userID,
			UserEmail: userEmail,
			OrgID:     orgID,
			Action:    method + " " + path,
			Resource:  "endpoint",
			IPAddress: c.IP(),
			UserAgent: string(c.Request().Header.UserAgent()),
			Metadata:  string(metaBytes),
		}

		// Asynchronous logging to not block response
		go func(entry *db.AuditLog) {
			_ = db.LogAuditEvent(entry)
		}(auditLog)

		return err
	}
}
