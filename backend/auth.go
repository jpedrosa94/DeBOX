package main

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ─── Context keys ────────────────────────────────────────────────────────────

type contextKey string

const (
	ctxSub   contextKey = "sub"
	ctxEmail contextKey = "email"
)

// ─── Google JWKS (cached RSA public keys for JWT verification) ───────────────

type cachedJWKS struct {
	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

var jwksCache = &cachedJWKS{}

func (c *cachedJWKS) getKey(kid string) (*rsa.PublicKey, error) {
	c.mu.RLock()
	if time.Since(c.fetchedAt) < time.Hour && c.keys != nil {
		if key, ok := c.keys[kid]; ok {
			c.mu.RUnlock()
			return key, nil
		}
		c.mu.RUnlock()
		return nil, fmt.Errorf("unknown kid: %s", kid)
	}
	c.mu.RUnlock()

	// Fetch fresh keys
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if time.Since(c.fetchedAt) < time.Hour && c.keys != nil {
		if key, ok := c.keys[kid]; ok {
			return key, nil
		}
		return nil, fmt.Errorf("unknown kid: %s", kid)
	}

	resp, err := http.Get("https://www.googleapis.com/oauth2/v3/certs")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Google JWKS: %w", err)
	}
	defer resp.Body.Close()

	var jwks struct {
		Keys []struct {
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return nil, fmt.Errorf("failed to parse Google JWKS: %w", err)
	}

	c.keys = make(map[string]*rsa.PublicKey, len(jwks.Keys))
	for _, k := range jwks.Keys {
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		e := 0
		for _, b := range eBytes {
			e = e<<8 + int(b)
		}
		c.keys[k.Kid] = &rsa.PublicKey{
			N: new(big.Int).SetBytes(nBytes),
			E: e,
		}
	}
	c.fetchedAt = time.Now()

	if key, ok := c.keys[kid]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("unknown kid: %s", kid)
}

// ─── JWT verification ────────────────────────────────────────────────────────

func verifyGoogleJWT(tokenString string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		kid, ok := token.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("missing kid in JWT header")
		}
		return jwksCache.getKey(kid)
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	// Verify issuer
	iss, _ := claims.GetIssuer()
	if iss != "https://accounts.google.com" && iss != "accounts.google.com" {
		return nil, fmt.Errorf("invalid issuer: %s", iss)
	}

	// Verify audience
	if googleClientID != "" {
		aud, _ := claims.GetAudience()
		if !slices.Contains(aud, googleClientID) {
			return nil, fmt.Errorf("invalid audience")
		}
	}

	return claims, nil
}

// ─── Auth middleware ──────────────────────────────────────────────────────────

func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			jsonError(w, http.StatusUnauthorized, "Missing authorization header")
			return
		}
		tokenStr := strings.TrimPrefix(auth, "Bearer ")

		claims, err := verifyGoogleJWT(tokenStr)
		if err != nil {
			slog.Warn("JWT verification failed", "error", err)
			jsonError(w, http.StatusUnauthorized, "Invalid token")
			return
		}

		sub, _ := claims["sub"].(string)
		email, _ := claims["email"].(string)
		if sub == "" {
			jsonError(w, http.StatusUnauthorized, "Missing sub claim")
			return
		}

		ctx := context.WithValue(r.Context(), ctxSub, sub)
		ctx = context.WithValue(ctx, ctxEmail, email)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// verifyAddressOwnership checks that the JWT sub owns the given address.
// On first use it creates the mapping (trust-on-first-use).
func verifyAddressOwnership(ctx context.Context, address string) error {
	sub := ctx.Value(ctxSub).(string)
	email, _ := ctx.Value(ctxEmail).(string)

	var existing UserMapping
	err := usersCol.FindOne(ctx, bson.M{"address": address}).Decode(&existing)
	if err == mongo.ErrNoDocuments {
		// First time: check if this sub already has a different address
		var bySub UserMapping
		err2 := usersCol.FindOne(ctx, bson.M{"sub": sub}).Decode(&bySub)
		if err2 == nil && bySub.Address != address {
			return fmt.Errorf("your account is bound to a different address")
		}
		// Create TOFU mapping
		_, err = usersCol.InsertOne(ctx, UserMapping{
			Sub:       sub,
			Address:   address,
			Email:     email,
			CreatedAt: time.Now().UTC(),
		})
		if err != nil && !mongo.IsDuplicateKeyError(err) {
			return fmt.Errorf("failed to create user mapping: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}
	if existing.Sub != sub {
		return fmt.Errorf("address belongs to another user")
	}
	return nil
}
