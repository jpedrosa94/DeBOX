package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ─── Config helpers ──────────────────────────────────────────────────────────

func TestGetEnv(t *testing.T) {
	t.Run("returns fallback when unset", func(t *testing.T) {
		if got := getEnv("TEST_NONEXISTENT_VAR_12345", "default"); got != "default" {
			t.Errorf("got %q, want %q", got, "default")
		}
	})

	t.Run("returns env value when set", func(t *testing.T) {
		t.Setenv("TEST_GETENV_VAR", "custom")
		if got := getEnv("TEST_GETENV_VAR", "default"); got != "custom" {
			t.Errorf("got %q, want %q", got, "custom")
		}
	})

	t.Run("returns fallback for empty string", func(t *testing.T) {
		t.Setenv("TEST_GETENV_EMPTY", "")
		if got := getEnv("TEST_GETENV_EMPTY", "fallback"); got != "fallback" {
			t.Errorf("got %q, want %q", got, "fallback")
		}
	})
}

func TestGetEnvInt(t *testing.T) {
	t.Run("returns fallback when unset", func(t *testing.T) {
		if got := getEnvInt("TEST_NONEXISTENT_INT_12345", 42); got != 42 {
			t.Errorf("got %d, want %d", got, 42)
		}
	})

	t.Run("returns parsed int when set", func(t *testing.T) {
		t.Setenv("TEST_GETENVINT_VAR", "8080")
		if got := getEnvInt("TEST_GETENVINT_VAR", 42); got != 8080 {
			t.Errorf("got %d, want %d", got, 8080)
		}
	})

	t.Run("returns fallback for non-numeric value", func(t *testing.T) {
		t.Setenv("TEST_GETENVINT_BAD", "not-a-number")
		if got := getEnvInt("TEST_GETENVINT_BAD", 99); got != 99 {
			t.Errorf("got %d, want %d", got, 99)
		}
	})
}

// ─── JSON helpers ────────────────────────────────────────────────────────────

func TestJsonError(t *testing.T) {
	w := httptest.NewRecorder()
	jsonError(w, http.StatusBadRequest, "something broke")

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"] != "something broke" {
		t.Errorf("error = %q, want %q", body["error"], "something broke")
	}
}

func TestJsonOK(t *testing.T) {
	w := httptest.NewRecorder()
	jsonOK(w, map[string]string{"status": "ok"})

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Errorf("status = %q, want %q", body["status"], "ok")
	}
}

// ─── CORS ────────────────────────────────────────────────────────────────────

func TestCorsHandler(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := corsHandler{mux}

	t.Run("sets CORS headers on regular request", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if origin := w.Header().Get("Access-Control-Allow-Origin"); origin != corsOrigin {
			t.Errorf("Allow-Origin = %q, want %q", origin, corsOrigin)
		}
		if methods := w.Header().Get("Access-Control-Allow-Methods"); methods == "" {
			t.Error("Allow-Methods header is empty")
		}
		if headers := w.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(headers, "Authorization") {
			t.Errorf("Allow-Headers = %q, should contain Authorization", headers)
		}
	})

	t.Run("OPTIONS returns 204 without forwarding", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/test", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
		}
	})
}

// ─── Status writer ───────────────────────────────────────────────────────────

func TestStatusWriter(t *testing.T) {
	t.Run("captures status code", func(t *testing.T) {
		w := httptest.NewRecorder()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		sw.WriteHeader(http.StatusNotFound)

		if sw.status != http.StatusNotFound {
			t.Errorf("status = %d, want %d", sw.status, http.StatusNotFound)
		}
	})

	t.Run("defaults to 200", func(t *testing.T) {
		w := httptest.NewRecorder()
		sw := &statusWriter{ResponseWriter: w}

		// Without any WriteHeader call, status should be zero-value
		// The loggingHandler sets it to 200 if still 0 after ServeHTTP
		if sw.status != 0 {
			t.Errorf("status = %d, want 0 (unset)", sw.status)
		}
	})
}

// ─── Health endpoint ─────────────────────────────────────────────────────────

func TestHandleHealth(t *testing.T) {
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Errorf("body = %v, want status=ok", body)
	}
}

// ─── Auth middleware ─────────────────────────────────────────────────────────

func TestAuthMiddleware(t *testing.T) {
	inner := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	}
	handler := authMiddleware(inner)

	t.Run("rejects missing Authorization header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		handler(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
		if !strings.Contains(w.Body.String(), "Missing authorization header") {
			t.Errorf("body = %q, should mention missing header", w.Body.String())
		}
	})

	t.Run("rejects non-Bearer auth", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
		w := httptest.NewRecorder()
		handler(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})

	t.Run("rejects invalid JWT", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer invalid.jwt.token")
		w := httptest.NewRecorder()
		handler(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
		if !strings.Contains(w.Body.String(), "Invalid token") {
			t.Errorf("body = %q, should mention invalid token", w.Body.String())
		}
	})
}

// ─── Upload handler (with mock Walrus) ───────────────────────────────────────

func TestHandleUpload(t *testing.T) {
	// Mock Walrus publisher
	walrusServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("Walrus request method = %s, want PUT", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/octet-stream" {
			t.Errorf("Walrus Content-Type = %q, want application/octet-stream", ct)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"newlyCreated": map[string]any{
				"blobObject": map[string]any{
					"blobId": "test-blob-123",
				},
			},
		})
	}))
	defer walrusServer.Close()

	// Override walrus publisher URL
	origPublisher := walrusPublisher
	origAggregator := walrusAggregator
	walrusPublisher = walrusServer.URL
	walrusAggregator = "https://aggregator.test"
	defer func() {
		walrusPublisher = origPublisher
		walrusAggregator = origAggregator
	}()

	t.Run("successful upload returns blob ID", func(t *testing.T) {
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		part, _ := writer.CreateFormFile("file", "test.txt")
		part.Write([]byte("hello world"))
		writer.WriteField("mimeType", "text/plain")
		writer.WriteField("filename", "test.txt")
		writer.WriteField("originalSize", "11")
		writer.Close()

		req := httptest.NewRequest("POST", "/api/upload", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()
		handleUpload(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusOK, w.Body.String())
		}

		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["blobId"] != "test-blob-123" {
			t.Errorf("blobId = %v, want test-blob-123", resp["blobId"])
		}
		if resp["filename"] != "test.txt" {
			t.Errorf("filename = %v, want test.txt", resp["filename"])
		}
		if resp["mimeType"] != "text/plain" {
			t.Errorf("mimeType = %v, want text/plain", resp["mimeType"])
		}
		if resp["status"] != "newly_created" {
			t.Errorf("status = %v, want newly_created", resp["status"])
		}
		if size, ok := resp["size"].(float64); !ok || int64(size) != 11 {
			t.Errorf("size = %v, want 11", resp["size"])
		}
	})

	t.Run("already certified blob returns blob ID", func(t *testing.T) {
		certifiedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"alreadyCertified": map[string]any{
					"blobId": "existing-blob-456",
				},
			})
		}))
		defer certifiedServer.Close()
		walrusPublisher = certifiedServer.URL

		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		part, _ := writer.CreateFormFile("file", "dup.txt")
		part.Write([]byte("duplicate"))
		writer.Close()

		req := httptest.NewRequest("POST", "/api/upload", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()
		handleUpload(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}

		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["blobId"] != "existing-blob-456" {
			t.Errorf("blobId = %v, want existing-blob-456", resp["blobId"])
		}
		if resp["status"] != "already_certified" {
			t.Errorf("status = %v, want already_certified", resp["status"])
		}
	})

	t.Run("rejects request without file", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/upload", strings.NewReader(""))
		req.Header.Set("Content-Type", "multipart/form-data; boundary=xxx")
		w := httptest.NewRecorder()
		handleUpload(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})

	t.Run("handles Walrus error response", func(t *testing.T) {
		errServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, "service down")
		}))
		defer errServer.Close()
		walrusPublisher = errServer.URL

		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		part, _ := writer.CreateFormFile("file", "fail.txt")
		part.Write([]byte("data"))
		writer.Close()

		req := httptest.NewRequest("POST", "/api/upload", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()
		handleUpload(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
		}
	})

	t.Run("defaults mimeType to application/octet-stream", func(t *testing.T) {
		walrusPublisher = walrusServer.URL

		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		part, _ := writer.CreateFormFile("file", "noext")
		part.Write([]byte("binary data"))
		writer.Close()

		req := httptest.NewRequest("POST", "/api/upload", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()
		handleUpload(w, req)

		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["mimeType"] != "application/octet-stream" {
			t.Errorf("mimeType = %v, want application/octet-stream", resp["mimeType"])
		}
	})
}

// ─── Blob download (with mock Walrus aggregator) ─────────────────────────────

func TestHandleGetBlob(t *testing.T) {
	blobContent := []byte("decrypted blob content here")

	aggServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/missing-id") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(blobContent)
	}))
	defer aggServer.Close()

	origAgg := walrusAggregator
	walrusAggregator = aggServer.URL
	defer func() { walrusAggregator = origAgg }()

	t.Run("returns blob content with cache headers", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/blob/{blobId}", handleGetBlob)

		req := httptest.NewRequest("GET", "/api/blob/test-blob-123", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
			t.Errorf("Content-Type = %q, want application/octet-stream", ct)
		}
		if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
			t.Errorf("Cache-Control = %q, should contain immutable", cc)
		}
		if !bytes.Equal(w.Body.Bytes(), blobContent) {
			t.Errorf("body = %q, want %q", w.Body.String(), string(blobContent))
		}
	})

	t.Run("returns error for missing blob", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/blob/{blobId}", handleGetBlob)

		req := httptest.NewRequest("GET", "/api/blob/missing-id", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})
}

// ─── MongoDB integration tests ───────────────────────────────────────────────
// These tests require a running MongoDB instance. They are skipped when
// MONGODB_URI is not reachable. Run with: make db && go test ./... -v

func setupTestDB(t *testing.T) func() {
	t.Helper()

	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		uri = "mongodb://localhost:27017"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Skipf("MongoDB not available: %v", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		t.Skipf("MongoDB not reachable: %v", err)
	}

	dbName := fmt.Sprintf("debox_test_%d", time.Now().UnixNano())
	db := client.Database(dbName)
	filesCol = db.Collection("files")
	usersCol = db.Collection("users")

	// Create indexes
	filesCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "address", Value: 1}, {Key: "uploadedAt", Value: -1}},
	})
	usersCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "sub", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	usersCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "address", Value: 1}},
		Options: options.Index().SetUnique(true),
	})

	return func() {
		db.Drop(context.Background())
		client.Disconnect(context.Background())
	}
}

// withAuth creates a context with sub and email values, simulating authenticated requests.
func withAuth(ctx context.Context, sub, email string) context.Context {
	ctx = context.WithValue(ctx, ctxSub, sub)
	ctx = context.WithValue(ctx, ctxEmail, email)
	return ctx
}

func TestVerifyAddressOwnership(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	addr1 := "0x" + strings.Repeat("a", 64)
	addr2 := "0x" + strings.Repeat("b", 64)

	t.Run("TOFU: first request creates mapping", func(t *testing.T) {
		ctx := withAuth(context.Background(), "google-sub-1", "user1@test.com")
		err := verifyAddressOwnership(ctx, addr1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify mapping was created
		var user UserMapping
		err = usersCol.FindOne(context.Background(), bson.M{"sub": "google-sub-1"}).Decode(&user)
		if err != nil {
			t.Fatalf("user not found: %v", err)
		}
		if user.Address != addr1 {
			t.Errorf("address = %q, want %q", user.Address, addr1)
		}
		if user.Email != "user1@test.com" {
			t.Errorf("email = %q, want %q", user.Email, "user1@test.com")
		}
	})

	t.Run("same sub + same address succeeds", func(t *testing.T) {
		ctx := withAuth(context.Background(), "google-sub-1", "user1@test.com")
		err := verifyAddressOwnership(ctx, addr1)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("different sub for same address is rejected", func(t *testing.T) {
		ctx := withAuth(context.Background(), "google-sub-attacker", "attacker@test.com")
		err := verifyAddressOwnership(ctx, addr1)
		if err == nil {
			t.Error("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "belongs to another user") {
			t.Errorf("error = %q, should mention belongs to another user", err.Error())
		}
	})

	t.Run("same sub for different address is rejected", func(t *testing.T) {
		ctx := withAuth(context.Background(), "google-sub-1", "user1@test.com")
		err := verifyAddressOwnership(ctx, addr2)
		if err == nil {
			t.Error("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "bound to a different address") {
			t.Errorf("error = %q, should mention bound to different address", err.Error())
		}
	})
}

func TestHandleListFiles_Integration(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	addr := "0x" + strings.Repeat("c", 64)
	sub := "google-sub-list"

	// Seed user mapping
	usersCol.InsertOne(context.Background(), UserMapping{
		Sub: sub, Address: addr, Email: "list@test.com", CreatedAt: time.Now().UTC(),
	})

	// Seed file entries
	now := time.Now().UTC()
	filesCol.InsertOne(context.Background(), FileEntry{
		Address: addr, BlobID: "blob-1", Filename: "first.txt",
		MimeType: "text/plain", Size: 100, Status: "newly_created",
		IsEncrypted: true, UploadedAt: now.Add(-time.Hour),
	})
	filesCol.InsertOne(context.Background(), FileEntry{
		Address: addr, BlobID: "blob-2", Filename: "second.txt",
		MimeType: "text/plain", Size: 200, Status: "already_certified",
		IsEncrypted: false, UploadedAt: now,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/files/{address}", handleListFiles)

	t.Run("returns files sorted by uploadedAt desc", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/files/"+addr, nil)
		req = req.WithContext(withAuth(req.Context(), sub, "list@test.com"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusOK, w.Body.String())
		}

		var files []FileEntry
		json.Unmarshal(w.Body.Bytes(), &files)
		if len(files) != 2 {
			t.Fatalf("got %d files, want 2", len(files))
		}
		if files[0].BlobID != "blob-2" {
			t.Errorf("first file blobId = %q, want blob-2 (most recent)", files[0].BlobID)
		}
		if files[1].BlobID != "blob-1" {
			t.Errorf("second file blobId = %q, want blob-1", files[1].BlobID)
		}
	})

	t.Run("returns empty array for address with no files", func(t *testing.T) {
		emptyAddr := "0x" + strings.Repeat("d", 64)
		usersCol.InsertOne(context.Background(), UserMapping{
			Sub: "google-sub-empty", Address: emptyAddr, Email: "empty@test.com", CreatedAt: time.Now().UTC(),
		})

		req := httptest.NewRequest("GET", "/api/files/"+emptyAddr, nil)
		req = req.WithContext(withAuth(req.Context(), "google-sub-empty", "empty@test.com"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}

		var files []FileEntry
		json.Unmarshal(w.Body.Bytes(), &files)
		if len(files) != 0 {
			t.Errorf("got %d files, want 0", len(files))
		}
	})

	t.Run("rejects unauthorized user", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/files/"+addr, nil)
		req = req.WithContext(withAuth(req.Context(), "google-sub-wrong", "wrong@test.com"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
		}
	})
}

func TestHandleSaveFile_Integration(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	addr := "0x" + strings.Repeat("e", 64)
	sub := "google-sub-save"

	usersCol.InsertOne(context.Background(), UserMapping{
		Sub: sub, Address: addr, Email: "save@test.com", CreatedAt: time.Now().UTC(),
	})

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/files/{address}", handleSaveFile)

	t.Run("saves file entry to MongoDB", func(t *testing.T) {
		payload := `{"blobId":"new-blob","filename":"doc.pdf","mimeType":"application/pdf","size":5000,"status":"newly_created","isEncrypted":true}`
		req := httptest.NewRequest("POST", "/api/files/"+addr, strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(withAuth(req.Context(), sub, "save@test.com"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusOK, w.Body.String())
		}

		var resp FileEntry
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.BlobID != "new-blob" {
			t.Errorf("blobId = %q, want new-blob", resp.BlobID)
		}
		if resp.Filename != "doc.pdf" {
			t.Errorf("filename = %q, want doc.pdf", resp.Filename)
		}
		if !resp.IsEncrypted {
			t.Error("isEncrypted = false, want true")
		}

		// Verify it's in the database
		count, _ := filesCol.CountDocuments(context.Background(), bson.M{"address": addr, "blobId": "new-blob"})
		if count != 1 {
			t.Errorf("document count = %d, want 1", count)
		}
	})

	t.Run("defaults isEncrypted to true when omitted", func(t *testing.T) {
		payload := `{"blobId":"no-enc-field","filename":"plain.txt","mimeType":"text/plain","size":10}`
		req := httptest.NewRequest("POST", "/api/files/"+addr, strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(withAuth(req.Context(), sub, "save@test.com"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		var resp FileEntry
		json.Unmarshal(w.Body.Bytes(), &resp)
		if !resp.IsEncrypted {
			t.Error("isEncrypted should default to true")
		}
	})

	t.Run("rejects missing blobId", func(t *testing.T) {
		payload := `{"filename":"no-blob.txt"}`
		req := httptest.NewRequest("POST", "/api/files/"+addr, strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(withAuth(req.Context(), sub, "save@test.com"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})

	t.Run("rejects missing filename", func(t *testing.T) {
		payload := `{"blobId":"has-blob"}`
		req := httptest.NewRequest("POST", "/api/files/"+addr, strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(withAuth(req.Context(), sub, "save@test.com"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})

	t.Run("rejects invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/files/"+addr, strings.NewReader("{bad json"))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(withAuth(req.Context(), sub, "save@test.com"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestHandleDeleteFile_Integration(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	addr := "0x" + strings.Repeat("f", 64)
	sub := "google-sub-delete"

	usersCol.InsertOne(context.Background(), UserMapping{
		Sub: sub, Address: addr, Email: "delete@test.com", CreatedAt: time.Now().UTC(),
	})

	// Seed a file to delete
	filesCol.InsertOne(context.Background(), FileEntry{
		Address: addr, BlobID: "delete-me", Filename: "gone.txt",
		MimeType: "text/plain", Size: 50, IsEncrypted: true, UploadedAt: time.Now().UTC(),
	})

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/files/{address}/{blobId}", handleDeleteFile)

	t.Run("deletes file entry from MongoDB", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/files/"+addr+"/delete-me", nil)
		req = req.WithContext(withAuth(req.Context(), sub, "delete@test.com"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusOK, w.Body.String())
		}

		var resp map[string]bool
		json.Unmarshal(w.Body.Bytes(), &resp)
		if !resp["ok"] {
			t.Error("response ok = false, want true")
		}

		// Verify it's gone
		count, _ := filesCol.CountDocuments(context.Background(), bson.M{"address": addr, "blobId": "delete-me"})
		if count != 0 {
			t.Errorf("document count = %d, want 0", count)
		}
	})

	t.Run("succeeds even if blobId does not exist", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/files/"+addr+"/nonexistent", nil)
		req = req.WithContext(withAuth(req.Context(), sub, "delete@test.com"))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
}

// ─── Full CRUD integration round-trip ────────────────────────────────────────

func TestCRUD_Integration(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	addr := "0x" + strings.Repeat("1", 64)
	sub := "google-sub-crud"

	usersCol.InsertOne(context.Background(), UserMapping{
		Sub: sub, Address: addr, Email: "crud@test.com", CreatedAt: time.Now().UTC(),
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/files/{address}", handleListFiles)
	mux.HandleFunc("POST /api/files/{address}", handleSaveFile)
	mux.HandleFunc("DELETE /api/files/{address}/{blobId}", handleDeleteFile)

	authCtx := withAuth(context.Background(), sub, "crud@test.com")

	// 1. List — empty
	req := httptest.NewRequest("GET", "/api/files/"+addr, nil)
	req = req.WithContext(authCtx)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var files []FileEntry
	json.Unmarshal(w.Body.Bytes(), &files)
	if len(files) != 0 {
		t.Fatalf("initial list: got %d files, want 0", len(files))
	}

	// 2. Save two files
	for _, blob := range []string{"blob-a", "blob-b"} {
		payload := fmt.Sprintf(`{"blobId":"%s","filename":"%s.txt","mimeType":"text/plain","size":42}`, blob, blob)
		req = httptest.NewRequest("POST", "/api/files/"+addr, strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(authCtx)
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("save %s: status = %d, body = %s", blob, w.Code, w.Body.String())
		}
	}

	// 3. List — 2 files
	req = httptest.NewRequest("GET", "/api/files/"+addr, nil)
	req = req.WithContext(authCtx)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	json.Unmarshal(w.Body.Bytes(), &files)
	if len(files) != 2 {
		t.Fatalf("after save: got %d files, want 2", len(files))
	}

	// 4. Delete one
	req = httptest.NewRequest("DELETE", "/api/files/"+addr+"/blob-a", nil)
	req = req.WithContext(authCtx)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete: status = %d", w.Code)
	}

	// 5. List — 1 file remaining
	req = httptest.NewRequest("GET", "/api/files/"+addr, nil)
	req = req.WithContext(authCtx)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	json.Unmarshal(w.Body.Bytes(), &files)
	if len(files) != 1 {
		t.Fatalf("after delete: got %d files, want 1", len(files))
	}
	if files[0].BlobID != "blob-b" {
		t.Errorf("remaining file = %q, want blob-b", files[0].BlobID)
	}
}

// ─── Logging handler ─────────────────────────────────────────────────────────

func TestLoggingHandler(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, "created")
	})
	handler := loggingHandler{next: inner}

	req := httptest.NewRequest("POST", "/api/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", w.Code, http.StatusCreated)
	}
	if w.Body.String() != "created" {
		t.Errorf("body = %q, want created", w.Body.String())
	}
}
