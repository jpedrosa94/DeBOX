package main

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ─── Configuration ───────────────────────────────────────────────────────────

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

var (
	port             = getEnvInt("BACKEND_PORT", 3001)
	walrusPublisher  = getEnv("WALRUS_PUBLISHER", "https://publisher.walrus-testnet.walrus.space")
	walrusAggregator = getEnv("WALRUS_AGGREGATOR", "https://aggregator.walrus-testnet.walrus.space")
	epochs           = getEnvInt("WALRUS_EPOCHS", 5)
	corsOrigin       = getEnv("CORS_ORIGIN", "http://localhost:5173")
	mongoURI         = getEnv("MONGODB_URI", "mongodb://localhost:27017")
	mongoDatabase    = getEnv("MONGODB_DATABASE", "debox")
	googleClientID   = os.Getenv("GOOGLE_CLIENT_ID")
)

// ─── MongoDB collections ─────────────────────────────────────────────────────

var (
	filesCol *mongo.Collection
	usersCol *mongo.Collection
)

// ─── Types ───────────────────────────────────────────────────────────────────

type FileEntry struct {
	ID          bson.ObjectID `bson:"_id,omitempty" json:"-"`
	Address     string        `bson:"address"       json:"-"`
	BlobID      string        `bson:"blobId"        json:"blobId"`
	Filename    string        `bson:"filename"      json:"filename"`
	MimeType    string        `bson:"mimeType"      json:"mimeType"`
	Size        int64         `bson:"size"           json:"size"`
	Status      string        `bson:"status"        json:"status"`
	IsEncrypted bool          `bson:"isEncrypted"   json:"isEncrypted"`
	UploadedAt  time.Time     `bson:"uploadedAt"    json:"uploadedAt"`
}

type UserMapping struct {
	Sub       string    `bson:"sub"`
	Address   string    `bson:"address"`
	Email     string    `bson:"email"`
	CreatedAt time.Time `bson:"createdAt"`
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":%q}`, msg)
}

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

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

type contextKey string

const (
	ctxSub   contextKey = "sub"
	ctxEmail contextKey = "email"
)

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
		valid := false
		for _, a := range aud {
			if a == googleClientID {
				valid = true
				break
			}
		}
		if !valid {
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
			log.Printf("JWT verification failed: %v", err)
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

// ─── CORS wrapper ────────────────────────────────────────────────────────────

type corsHandler struct{ mux *http.ServeMux }

func (h corsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", corsOrigin)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	h.mux.ServeHTTP(w, r)
}

// ─── Request logging middleware ──────────────────────────────────────────────

type loggingHandler struct{ next http.Handler }

func (h loggingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	lw := &statusWriter{ResponseWriter: w, status: 200}
	h.next.ServeHTTP(lw, r)
	log.Printf("%s %s %d %s", r.Method, r.URL.Path, lw.status, time.Since(start).Round(time.Millisecond))
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// ─── Handlers ────────────────────────────────────────────────────────────────

// POST /api/upload
func handleUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		jsonError(w, http.StatusBadRequest, "failed to parse form: "+err.Error())
		return
	}
	f, header, err := r.FormFile("file")
	if err != nil {
		jsonError(w, http.StatusBadRequest, "No file provided")
		return
	}
	defer f.Close()

	fileBytes, err := io.ReadAll(f)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Failed to read file")
		return
	}

	mimeType := r.FormValue("mimeType")
	if mimeType == "" {
		mimeType = header.Header.Get("Content-Type")
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	filename := r.FormValue("filename")
	if filename == "" {
		filename = header.Filename
	}

	originalSize := int64(len(fileBytes))
	if s := r.FormValue("originalSize"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			originalSize = n
		}
	}

	// PUT to Walrus publisher
	walrusURL := fmt.Sprintf("%s/v1/blobs?epochs=%d", walrusPublisher, epochs)
	req, err := http.NewRequest(http.MethodPut, walrusURL, bytes.NewReader(fileBytes))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Failed to create Walrus request")
		return
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Failed to reach storage service")
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Failed to read storage response")
		return
	}
	if resp.StatusCode != http.StatusOK {
		jsonError(w, resp.StatusCode, string(body))
		return
	}

	var walrusResp struct {
		NewlyCreated *struct {
			BlobObject struct {
				BlobID string `json:"blobId"`
			} `json:"blobObject"`
		} `json:"newlyCreated"`
		AlreadyCertified *struct {
			BlobID string `json:"blobId"`
		} `json:"alreadyCertified"`
	}
	if err := json.Unmarshal(body, &walrusResp); err != nil {
		jsonError(w, http.StatusInternalServerError, "Failed to parse storage response")
		return
	}

	var blobID, status string
	if walrusResp.NewlyCreated != nil {
		blobID = walrusResp.NewlyCreated.BlobObject.BlobID
		status = "newly_created"
	} else if walrusResp.AlreadyCertified != nil {
		blobID = walrusResp.AlreadyCertified.BlobID
		status = "already_certified"
	}
	if blobID == "" {
		jsonError(w, http.StatusInternalServerError, "No blob ID in storage response")
		return
	}

	jsonOK(w, map[string]any{
		"blobId":   blobID,
		"url":      fmt.Sprintf("%s/v1/blobs/%s", walrusAggregator, blobID),
		"filename": filename,
		"mimeType": mimeType,
		"size":     originalSize,
		"status":   status,
	})
}

// GET /api/blob/{blobId}
func handleGetBlob(w http.ResponseWriter, r *http.Request) {
	blobID := r.PathValue("blobId")
	resp, err := http.Get(fmt.Sprintf("%s/v1/blobs/%s", walrusAggregator, blobID))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Failed to reach storage service")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		jsonError(w, resp.StatusCode, "Blob not found")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	io.Copy(w, resp.Body)
}

// GET /api/files/{address}
func handleListFiles(w http.ResponseWriter, r *http.Request) {
	address := r.PathValue("address")

	if err := verifyAddressOwnership(r.Context(), address); err != nil {
		jsonError(w, http.StatusForbidden, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	opts := options.Find().SetSort(bson.D{{"uploadedAt", -1}})
	cursor, err := filesCol.Find(ctx, bson.M{"address": address}, opts)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Database query failed")
		return
	}
	defer cursor.Close(ctx)

	var entries []FileEntry
	if err := cursor.All(ctx, &entries); err != nil {
		jsonError(w, http.StatusInternalServerError, "Failed to read results")
		return
	}
	if entries == nil {
		entries = []FileEntry{}
	}
	jsonOK(w, entries)
}

// POST /api/files/{address}
func handleSaveFile(w http.ResponseWriter, r *http.Request) {
	address := r.PathValue("address")

	if err := verifyAddressOwnership(r.Context(), address); err != nil {
		jsonError(w, http.StatusForbidden, err.Error())
		return
	}

	var body struct {
		BlobID      string `json:"blobId"`
		Filename    string `json:"filename"`
		MimeType    string `json:"mimeType"`
		Size        int64  `json:"size"`
		Status      string `json:"status"`
		IsEncrypted *bool  `json:"isEncrypted"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	if body.BlobID == "" || body.Filename == "" {
		jsonError(w, http.StatusBadRequest, "Missing blobId or filename")
		return
	}

	isEncrypted := true
	if body.IsEncrypted != nil {
		isEncrypted = *body.IsEncrypted
	}
	entry := FileEntry{
		Address:     address,
		BlobID:      body.BlobID,
		Filename:    body.Filename,
		MimeType:    body.MimeType,
		Size:        body.Size,
		Status:      body.Status,
		IsEncrypted: isEncrypted,
		UploadedAt:  time.Now().UTC(),
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	_, err := filesCol.InsertOne(ctx, entry)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Failed to save file entry")
		return
	}
	jsonOK(w, entry)
}

// DELETE /api/files/{address}/{blobId}
func handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	address := r.PathValue("address")
	blobID := r.PathValue("blobId")

	if err := verifyAddressOwnership(r.Context(), address); err != nil {
		jsonError(w, http.StatusForbidden, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	_, err := filesCol.DeleteOne(ctx, bson.M{"address": address, "blobId": blobID})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Failed to delete file entry")
		return
	}
	jsonOK(w, map[string]bool{"ok": true})
}

// GET /health
func handleHealth(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]string{"status": "ok"})
}

// ─── Main ────────────────────────────────────────────────────────────────────

func main() {
	// Connect to MongoDB
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI(mongoURI))
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer client.Disconnect(context.Background())

	// Ping to verify connection
	if err := client.Ping(ctx, nil); err != nil {
		log.Fatalf("Failed to ping MongoDB: %v", err)
	}
	log.Printf("Connected to MongoDB at %s", mongoURI)

	db := client.Database(mongoDatabase)
	filesCol = db.Collection("files")
	usersCol = db.Collection("users")

	// Create indexes
	filesCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{"address", 1}, {"uploadedAt", -1}},
	})
	usersCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{"sub", 1}},
		Options: options.Index().SetUnique(true),
	})
	usersCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{"address", 1}},
		Options: options.Index().SetUnique(true),
	})

	// Routes
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/upload", authMiddleware(handleUpload))
	mux.HandleFunc("GET /api/blob/{blobId}", handleGetBlob) // no auth — content-addressed
	mux.HandleFunc("GET /api/files/{address}", authMiddleware(handleListFiles))
	mux.HandleFunc("POST /api/files/{address}", authMiddleware(handleSaveFile))
	mux.HandleFunc("DELETE /api/files/{address}/{blobId}", authMiddleware(handleDeleteFile))
	mux.HandleFunc("GET /health", handleHealth)

	addr := fmt.Sprintf(":%d", port)
	log.Printf("Backend running at http://localhost%s", addr)
	log.Printf("Walrus publisher: %s", walrusPublisher)
	if googleClientID == "" {
		log.Printf("WARNING: GOOGLE_CLIENT_ID not set — JWT audience verification disabled")
	}

	handler := loggingHandler{next: corsHandler{mux}}
	log.Fatal(http.ListenAndServe(addr, handler))
}
