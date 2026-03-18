package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

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

	walrusStart := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		walrusUploadTotal.WithLabelValues("failure").Inc()
		walrusUploadDuration.Observe(time.Since(walrusStart).Seconds())
		jsonError(w, http.StatusInternalServerError, "Failed to reach storage service")
		return
	}
	defer resp.Body.Close()
	walrusUploadDuration.Observe(time.Since(walrusStart).Seconds())

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		walrusUploadTotal.WithLabelValues("failure").Inc()
		jsonError(w, http.StatusInternalServerError, "Failed to read storage response")
		return
	}
	if resp.StatusCode != http.StatusOK {
		walrusUploadTotal.WithLabelValues("failure").Inc()
		jsonError(w, resp.StatusCode, string(body))
		return
	}
	walrusUploadTotal.WithLabelValues("success").Inc()

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

	opts := options.Find().SetSort(bson.D{{Key: "uploadedAt", Value: -1}})
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
