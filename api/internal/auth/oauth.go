package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gofiber/fiber/v2"
)

// OAuthConfig holds the configuration for Ory Hydra or another OIDC provider.
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	AuthURL      string
	TokenURL     string
	RedirectURI  string
}

// DefaultOAuthConfig gets config from environment.
func DefaultOAuthConfig() OAuthConfig {
	return OAuthConfig{
		ClientID:     os.Getenv("OAUTH_CLIENT_ID"),
		ClientSecret: os.Getenv("OAUTH_CLIENT_SECRET"),
		AuthURL:      os.Getenv("OAUTH_AUTH_URL"), // e.g., http://localhost:4444/oauth2/auth
		TokenURL:     os.Getenv("OAUTH_TOKEN_URL"), // e.g., http://localhost:4444/oauth2/token
		RedirectURI:  os.Getenv("OAUTH_REDIRECT_URI"), // e.g., http://localhost:3000/auth/callback
	}
}

// OAuthLogin initiates the OAuth2 authorization code flow.
func OAuthLogin(config OAuthConfig) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Generate random state
		b := make([]byte, 16)
		rand.Read(b)
		state := base64.URLEncoding.EncodeToString(b)
		
		// Typically you'd store state in a secure cookie to verify later
		c.Cookie(&fiber.Cookie{
			Name:     "oauth_state",
			Value:    state,
			Expires:  time.Now().Add(10 * time.Minute),
			HTTPOnly: true,
		})

		url := fmt.Sprintf("%s?client_id=%s&response_type=code&scope=openid profile email&redirect_uri=%s&state=%s",
			config.AuthURL, config.ClientID, config.RedirectURI, state)

		return c.Redirect(url, http.StatusTemporaryRedirect)
	}
}

// OAuthCallback handles the redirect back from the provider.
// Note: This is an uncompleted skeleton mapping to Phase 3 Enterprise requirement.
// It will exchange the code for a token and generate a local JWT.
func OAuthCallback(config OAuthConfig, jwtSecret string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		code := c.Query("code")
		state := c.Query("state")
		savedState := c.Cookies("oauth_state")

		if state == "" || state != savedState {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid state parameter"})
		}

		if code == "" {
			return c.Status(400).JSON(fiber.Map{"error": "Authorization code missing"})
		}

		// 1. Exchange code for token (omitted HTTP call for brevity)
		// 2. Fetch user profile from OIDC provider
		// 3. Find or create user in DB
		// 4. Generate local ZEROGATE JWT:
		
		claims := JWTClaims{
			Sub:   "sso-user-123", // Replaced by actual ID
			Email: "user@enterprise.com",
			Name:  "Enterprise User",
			OrgID: "org-xyz",
			Role:  "member",
		}

		token, err := GenerateJWT(claims, jwtSecret)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Failed to generate session token"})
		}
		
		// Return JWT to frontend (e.g. via redirect with hash, or JSON if API)
		return c.JSON(fiber.Map{
			"status": "success",
			"token": token,
		})
	}
}
