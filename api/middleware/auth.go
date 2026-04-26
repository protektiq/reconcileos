package middleware

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"reconcileos.dev/api/db"

	"github.com/MicahParks/keyfunc/v2"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const (
	maxAuthHeaderLength = 8192
	maxTokenLength      = 4096
)

type authErrorResponse struct {
	Error string `json:"error"`
}

func JWTAuthMiddleware(supabaseURL string, clients *db.SupabaseClients) (gin.HandlerFunc, error) {
	cleanSupabaseURL := strings.TrimSpace(supabaseURL)
	if cleanSupabaseURL == "" {
		return nil, errors.New("supabase URL must not be empty")
	}
	if clients == nil {
		return nil, errors.New("supabase clients must not be nil")
	}

	jwksURL, err := buildJWKSURL(cleanSupabaseURL)
	if err != nil {
		return nil, fmt.Errorf("build jwks URL: %w", err)
	}

	var jwks *keyfunc.JWKS
	var jwksErr error
	var jwksMutex sync.RWMutex

	return func(c *gin.Context) {
		authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
		if authHeader == "" || len(authHeader) > maxAuthHeaderLength {
			abortUnauthorized(c, "missing or invalid authorization header")
			return
		}

		tokenString, ok := parseBearerToken(authHeader)
		if !ok {
			abortUnauthorized(c, "missing or invalid bearer token")
			return
		}

		resolvedJWKS, err := getOrInitJWKS(jwksURL, clients.AnonKey, &jwks, &jwksErr, &jwksMutex)
		if err != nil {
			abortUnauthorized(c, "invalid or expired token")
			return
		}

		claims := jwt.RegisteredClaims{}
		token, err := jwt.ParseWithClaims(tokenString, &claims, resolvedJWKS.Keyfunc)
		if err != nil || token == nil || !token.Valid {
			abortUnauthorized(c, "invalid or expired token")
			return
		}

		userID := strings.TrimSpace(claims.Subject)
		if userID == "" || len(userID) > 128 {
			abortUnauthorized(c, "invalid token subject")
			return
		}

		orgID, err := lookupOrgID(c.Request.Context(), clients, userID)
		if err != nil {
			abortUnauthorized(c, "invalid token scope")
			return
		}

		c.Set("user_id", userID)
		c.Set("org_id", orgID)
		c.Next()
	}, nil
}

func parseBearerToken(authHeader string) (string, bool) {
	const bearerPrefix = "Bearer "
	if !strings.HasPrefix(authHeader, bearerPrefix) {
		return "", false
	}

	token := strings.TrimSpace(strings.TrimPrefix(authHeader, bearerPrefix))
	if token == "" || len(token) > maxTokenLength {
		return "", false
	}

	return token, true
}

func buildJWKSURL(supabaseURL string) (string, error) {
	parsed, err := url.Parse(supabaseURL)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("supabase URL must include scheme and host")
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/auth/v1/keys"

	return parsed.String(), nil
}

func getOrInitJWKS(
	jwksURL string,
	anonKey string,
	cachedJWKS **keyfunc.JWKS,
	cachedErr *error,
	jwksMutex *sync.RWMutex,
) (*keyfunc.JWKS, error) {
	jwksMutex.RLock()
	if *cachedJWKS != nil {
		defer jwksMutex.RUnlock()
		return *cachedJWKS, nil
	}
	jwksMutex.RUnlock()

	jwksMutex.Lock()
	defer jwksMutex.Unlock()

	if *cachedJWKS != nil {
		return *cachedJWKS, nil
	}

	jwks, err := keyfunc.Get(jwksURL, keyfunc.Options{
		RefreshInterval:   time.Hour,
		RefreshUnknownKID: true,
		Client:            &http.Client{Timeout: 10 * time.Second},
		RequestFactory: func(ctx context.Context, url string) (*http.Request, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, bytes.NewReader(nil))
			if err != nil {
				return nil, err
			}
			trimmedAnonKey := strings.TrimSpace(anonKey)
			if trimmedAnonKey != "" {
				req.Header.Set("apikey", trimmedAnonKey)
				req.Header.Set("Authorization", "Bearer "+trimmedAnonKey)
			}
			return req, nil
		},
		RefreshErrorHandler: func(err error) {
			*cachedErr = err
		},
	})
	if err != nil {
		*cachedErr = err
		return nil, err
	}

	*cachedJWKS = jwks
	*cachedErr = nil
	return *cachedJWKS, nil
}

func lookupOrgID(_ context.Context, clients *db.SupabaseClients, userID string) (string, error) {
	type row struct {
		OrgID string `json:"org_id"`
	}

	var result row
	query := clients.AdminPostgrest().
		From("users").
		Select("org_id", "", false).
		Eq("id", userID).
		Limit(1, "").
		Single()

	_, err := query.ExecuteTo(&result)
	if err != nil {
		return "", err
	}
	orgID := strings.TrimSpace(result.OrgID)
	if orgID == "" || len(orgID) > 128 {
		return "", errors.New("organization not found")
	}

	return orgID, nil
}

func abortUnauthorized(c *gin.Context, message string) {
	c.AbortWithStatusJSON(401, authErrorResponse{Error: message})
}
