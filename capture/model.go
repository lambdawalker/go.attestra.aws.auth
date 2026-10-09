// Package capture owns private document acquisition, never identity validation.
package capture

import (
 "context"
 "errors"
 "time"
)

var (
 ErrInvalid = errors.New("invalid_request")
 ErrNotFound = errors.New("not_found")
 ErrConflict = errors.New("revision_conflict")
 ErrExpired = errors.New("capture_expired")
 ErrLimited = errors.New("capture_limit")
 ErrDisabled = errors.New("capture_disabled")
 ErrIncomplete = errors.New("uploads_incomplete")
 ErrImage = errors.New("invalid_image")
)
const MaxBytes int64 = 4 * 1024 * 1024
const MaxPixels int64 = 20_000_000
const SessionLifetime = 24 * time.Hour
const UploadLifetime = 5 * time.Minute

type Policy struct {
 Enabled bool `json:"enabled"`
 Version string `json:"policy_version"`
 DocumentType string `json:"document_type"`
 Slots []string `json:"required_slots"`
 ContentType string `json:"content_type"`
 MaxBytes int64 `json:"max_bytes"`
 Purpose string `json:"purpose"`
 Jurisdiction string `json:"jurisdiction"`
 RetentionDays int `json:"retention_days"`
}
type Upload struct {
 ID string `json:"upload_id"`
 Slot string `json:"slot"`
 SHA256 string `json:"sha256"`
 Size int64 `json:"size"`
 Key string `json:"-"`
 OperationKey string `json:"-"`
}
type Asset struct {
 UploadID string `json:"upload_id"`
 Slot string `json:"slot"`
 Key string `json:"-"`
 VersionID string `json:"-"`
 SHA256 string `json:"sha256"`
 Size int64 `json:"size"`
}
type Record struct {
 ID string `json:"capture_id" dynamodbav:"CaptureID"`
 Owner string `json:"-"`
 EvidenceVersion int64 `json:"evidence_version"`
 Revision int64 `json:"revision"`
 PolicyVersion string `json:"policy_version"`
 DocumentType string `json:"document_type"`
 State string `json:"state"`
 ExpiresAt int64 `json:"expires_at"`
 CreatedAt int64 `json:"created_at"`
 Slots map[string]string `json:"selected_uploads"`
 Uploads []Upload `json:"uploads"`
 Assets []Asset `json:"assets,omitempty"`
 Processed []Asset `json:"processed,omitempty"`
 ProcessingVersion string `json:"processing_version,omitempty"`
 Error string `json:"error,omitempty"`
 CreateKey string `json:"-"`
 FinalizeKey string `json:"-"`
 RetryKey string `json:"-"`
 Generation int64 `json:"-"`
 Attempts int `json:"-"`
 LeaseUntil int64 `json:"-"`
 Work string `json:"-"`
 Due int64 `json:"-"`
 DeleteAfter int64 `json:"delete_after,omitempty"`
 TTL int64 `json:"-"`
}
type Account struct {
 Owner string
 CurrentID string
 Revision int64
 Version int64
 Day string
 CreatedToday int
}
type Store interface {
 Account(context.Context, string) (Account, error)
 Get(context.Context, string, string) (Record, error)
 FindCreate(context.Context, string, string) (Record, error)
 Create(context.Context, Account, Account, Record) error
 // Swap is conditional on the record revision and optionally atomically writes a ready outbox event.
 Swap(context.Context, Record, Record, bool) error
}
type Objects interface {
 Presign(context.Context, Upload) (string, map[string]string, error)
 Head(context.Context, Upload) (Asset, error)
 Read(context.Context, Asset) ([]byte, error)
 Write(context.Context, Record, string, []byte) (Asset, error)
 DeleteCapture(context.Context, string) error
}
type UploadRequest struct {
 OperationKey string `json:"operation_key"`
 Revision int64 `json:"expected_revision"`
 Slot string `json:"slot"`
 SHA256 string `json:"sha256"`
 Size int64 `json:"size"`
}
type FinalizeRequest struct {
 OperationKey string `json:"operation_key"`
 Revision int64 `json:"expected_revision"`
 UploadIDs map[string]string `json:"upload_ids"`
}
type UploadReply struct {
 Capture Record `json:"capture"`
 UploadID string `json:"upload_id"`
 URL string `json:"url"`
 Headers map[string]string `json:"headers"`
 ExpiresIn int `json:"expires_in"`
}
type Job struct { Owner string `json:"owner"`; ID string `json:"capture_id"`; Generation int64 `json:"generation"`; Work string `json:"work"` }
