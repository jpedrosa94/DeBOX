package main

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

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
