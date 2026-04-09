package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// JWTClaims represents the decoded JWT payload.
type JWTClaims struct {
	Sub   string `json:"sub"`
	Email string `json:"email"`
	Name  string `json:"name"`
	OrgID string `json:"org_id"`
	Role  string `json:"role"` // admin, member, viewer
	Exp   int64  `json:"exp"`
	Iat   int64  `json:"iat"`
}

// AuthConfig configures the authentication middleware.
type AuthConfig struct {
	SecretKey      string
	SkipPaths      []string // Paths that don't require auth
	EnableRBAC     bool
	AdminOnlyPaths []string
}

// DefaultAuthConfig returns a development-friendly config.
func DefaultAuthConfig() AuthConfig {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = "zerogate-dev-secret-key-change-in-production"
	}
	return AuthConfig{
		SecretKey: secret,
		SkipPaths: []string{
			"/api/v1/health",
			"/api/v1/auth/login",
			"/api/v1/auth/sso/login",
			"/api/v1/auth/sso/callback",
			"/api/v1/mcp/rpc",
		},
		EnableRBAC:     true,
		AdminOnlyPaths: []string{"/api/v1/audit-logs"},
	}
}

// JWTMiddleware returns a Fiber middleware that validates JWT tokens.
func JWTMiddleware(config AuthConfig) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Skip auth for whitelisted paths
		path := c.Path()
		for _, skip := range config.SkipPaths {
			if strings.HasPrefix(path, skip) {
				return c.Next()
			}
		}

		// WebSocket upgrade requests pass through
		if c.Get("Upgrade") == "websocket" {
			return c.Next()
		}

		// Extract token from Authorization header
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			// In dev mode, allow unauthenticated requests with default claims
			if os.Getenv("ZEROGATE_ENV") != "production" {
				c.Locals("user_id", "dev-user")
				c.Locals("user_email", "dev@zerogate.io")
				c.Locals("user_name", "Dev User")
				c.Locals("org_id", "dev-org")
				c.Locals("user_role", "admin")
				return c.Next()
			}
			return c.Status(401).JSON(fiber.Map{"error": "Authorization header required"})
		}

		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		claims, err := ValidateJWT(tokenStr, config.SecretKey)
		if err != nil {
			return c.Status(401).JSON(fiber.Map{"error": fmt.Sprintf("Invalid token: %v", err)})
		}

		// Check expiry
		if claims.Exp > 0 && time.Now().Unix() > claims.Exp {
			return c.Status(401).JSON(fiber.Map{"error": "Token expired"})
		}

		// Set user context
		c.Locals("user_id", claims.Sub)
		c.Locals("user_email", claims.Email)
		c.Locals("user_name", claims.Name)
		c.Locals("org_id", claims.OrgID)
		c.Locals("user_role", claims.Role)

		// RBAC: check admin-only paths
		if config.EnableRBAC {
			for _, adminPath := range config.AdminOnlyPaths {
				if strings.HasPrefix(path, adminPath) && claims.Role != "admin" {
					return c.Status(403).JSON(fiber.Map{"error": "Admin access required"})
				}
			}
		}

		return c.Next()
	}
}

// ValidateJWT performs a basic HMAC-SHA256 JWT validation.
func ValidateJWT(tokenStr, secret string) (*JWTClaims, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}

	// Verify signature
	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(parts[2]), []byte(expectedSig)) {
		return nil, fmt.Errorf("invalid signature")
	}

	// Decode payload
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid payload encoding")
	}

	var claims JWTClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	return &claims, nil
}

// GenerateJWT creates a signed JWT token (for dev/testing).
func GenerateJWT(claims JWTClaims, secret string) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))

	if claims.Iat == 0 {
		claims.Iat = time.Now().Unix()
	}
	if claims.Exp == 0 {
		claims.Exp = time.Now().Add(24 * time.Hour).Unix()
	}

	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)

	signingInput := header + "." + payload
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return signingInput + "." + signature, nil
}
