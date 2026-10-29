package capture

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"reflect"
	"regexp"
	"time"
)

var operationPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,80}$`)
var idPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func ValidID(v string) bool        { return idPattern.MatchString(v) }
func ValidOperation(v string) bool { return operationPattern.MatchString(v) }
func Prefix(owner, id string) string {
	sum := sha256.Sum256([]byte(owner))
	return "uploads/" + hex.EncodeToString(sum[:]) + "/" + id + "/"
}
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

type Service struct {
	Extractor    Extractor
	PolicySource func(context.Context) (Policy, error)
	Store        Store
	Objects      Objects
	Enabled      bool
	DocumentType string
	Purpose      string
	Jurisdiction string
	Now          func() time.Time
	Retention    time.Duration
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
func (s Service) Policy() Policy {
	kind := s.DocumentType
	if kind == "" {
		kind = "sample_card"
	}
	return Policy{Enabled: s.Enabled, DocumentType: kind, Slots: []string{"front", "back"}, ContentType: "image/jpeg", MaxBytes: MaxBytes, Purpose: s.Purpose, Jurisdiction: s.Jurisdiction, RetentionDays: 7}.WithVersion()
}

// WithVersion binds every consent-relevant field, including deployment defaults.
func (p Policy) WithVersion() Policy {
	p.Version = ""
	b, _ := json.Marshal(p)
	sum := sha256.Sum256(b)
	p.Version = "capture-sha256-" + hex.EncodeToString(sum[:])
	return p
}
func (s Service) CurrentPolicy(ctx context.Context) (Policy, error) {
	if s.PolicySource != nil {
		return s.PolicySource(ctx)
	}
	return s.Policy(), nil
}
func (s Service) Get(ctx context.Context, owner, id string) (Record, error) {
	if owner == "" || !ValidID(id) {
		return Record{}, ErrNotFound
	}
	r, e := s.Store.Get(ctx, owner, id)
	if e != nil {
		return r, e
	}
	if (r.State == "uploading" && s.now().Unix() >= r.ExpiresAt) || (r.State == "ready" && r.DeleteAfter > 0 && s.now().Unix() >= r.DeleteAfter) {
		r.State = "expired"
	}
	// A ready record with no extraction job cannot ever leave the client's loading screen.
	// Recover overdue durable intent as well, even when the dispatcher/worker is unavailable.
	// Allow five minutes past the scheduled attempt, without interrupting a live lease.
	// The revision fence rejects late worker publication.
	if r.State == "ready" && (r.Extraction == nil ||
		(r.Extraction.Status == "pending" && r.LeaseUntil <= s.now().Unix() &&
			(r.Work != "extract" || s.now().Unix() >= r.Due+300))) {
		before := r
		r.Revision++
		r.LeaseUntil = 0
		r.Extraction = &Extraction{Status: "failed", Error: "extraction_unavailable"}
		r.Work = "cleanup"
		r.Due = r.DeleteAfter
		if e = s.Store.Swap(ctx, before, r, false); e != nil {
			return Record{}, e
		}
		slog.Warn("capture.extraction_recovered", "capture_id", r.ID, "revision", r.Revision)
	}
	if r.State == "expired" || r.State == "cancelled" {
		r.Extraction = nil
		r.Reviewed = nil
	}
	return r, nil
}
func (s Service) Current(ctx context.Context, owner string) (*Record, error) {
	a, e := s.Store.Account(ctx, owner)
	if e != nil {
		return nil, e
	}
	if a.CurrentID == "" {
		return nil, nil
	}
	r, e := s.Get(ctx, owner, a.CurrentID)
	if e == ErrNotFound {
		return nil, nil
	}
	return &r, e
}
func (s Service) Create(ctx context.Context, owner, key, kind string, expected ...string) (Record, error) {
	version := ""
	if len(expected) >= 1 {
		version = expected[0]
	}
	language := "en"
	if len(expected) >= 2 {
		language = expected[1]
	}
	if language != "en" && language != "es" && language != "fr" {
		return Record{}, ErrInvalid
	}
	if owner == "" || !ValidOperation(key) {
		return Record{}, ErrInvalid
	}
	// Replay precedes current admission; a disabled or changed policy must not destroy an existing intent.
	if old, e := s.Store.FindCreate(ctx, owner, key); e == nil {
		if old.DocumentType != kind || old.ExpectedPolicyVersion != version || (old.DisclosureLanguage != "" && old.DisclosureLanguage != language) {
			return Record{}, ErrConflict
		}
		return s.Get(ctx, owner, old.ID)
	} else if e != ErrNotFound {
		return Record{}, e
	}
	policy, e := s.CurrentPolicy(ctx)
	if e != nil {
		return Record{}, e
	}
	if version == "" || version != policy.Version {
		return Record{}, ErrPolicyChanged
	}
	if !policy.Enabled {
		return Record{}, ErrDisabled
	}
	if language != policy.Purposes.Language(language) {
		return Record{}, ErrInvalid
	}
	if kind != policy.DocumentType {
		return Record{}, ErrInvalid
	}

	a, e := s.Store.Account(ctx, owner)
	if e != nil {
		return Record{}, e
	}
	if a.CurrentID != "" {
		old, e := s.Get(ctx, owner, a.CurrentID)
		if e != nil && e != ErrNotFound {
			return Record{}, e
		}
		if old.State == "uploading" || old.State == "finalizing" {
			return Record{}, ErrConflict
		}
	}
	next := a
	next.Owner = owner
	next.Revision++
	next.Version++
	day := s.now().UTC().Format("2006-01-02")
	if next.Day != day {
		next.Day = day
		next.CreatedToday = 0
	}
	if next.CreatedToday >= 5 {
		return Record{}, ErrLimited
	}
	next.CreatedToday++
	id := newID()
	next.CurrentID = id
	now := s.now().Unix()
	expires := s.now().Add(SessionLifetime).Unix()
	r := Record{DisclosureLanguage: language, ID: id, Owner: owner, EvidenceVersion: next.Version, Revision: 1, PolicyVersion: policy.Version, PolicySnapshot: &policy, ExpectedPolicyVersion: version, DocumentType: kind, State: "uploading", CreatedAt: now, ExpiresAt: expires, Slots: map[string]string{}, Uploads: []Upload{}, CreateKey: key, Work: "cleanup", Due: expires + int64((UploadLifetime + time.Minute).Seconds()), TTL: now + 90*86400}
	if e = s.Store.Create(ctx, a, next, r); e != nil {
		return Record{}, e
	}
	return r, nil
}
func (s Service) Upload(ctx context.Context, owner, id string, in UploadRequest) (UploadReply, error) {
	r, e := s.Get(ctx, owner, id)
	if e != nil {
		return UploadReply{}, e
	}
	if r.State == "expired" {
		return UploadReply{}, ErrExpired
	}
	if r.State != "uploading" {
		return UploadReply{}, ErrConflict
	}
	sum, e := base64.StdEncoding.DecodeString(in.SHA256)
	if e != nil || len(sum) != 32 || in.Size < 4 || in.Size > MaxBytes || !ValidOperation(in.OperationKey) || (in.Slot != "front" && in.Slot != "back") {
		return UploadReply{}, ErrInvalid
	}
	var selected *Upload
	for i := range r.Uploads {
		u := r.Uploads[i]
		if u.OperationKey == in.OperationKey {
			if u.Slot != in.Slot || u.SHA256 != in.SHA256 || u.Size != in.Size || r.Slots[u.Slot] != u.ID {
				return UploadReply{}, ErrConflict
			}
			selected = &u
			break
		}
	}
	if selected == nil {
		if in.Revision != r.Revision {
			return UploadReply{}, ErrConflict
		}
		if len(r.Uploads) >= 20 {
			return UploadReply{}, ErrLimited
		}
		u := Upload{ID: newID(), Slot: in.Slot, SHA256: in.SHA256, Size: in.Size, OperationKey: in.OperationKey}
		u.Key = Prefix(owner, id) + u.ID + ".jpg"
		before := r
		r.Slots = cloneSlots(r.Slots)
		r.Slots[in.Slot] = u.ID
		r.Uploads = append(append([]Upload{}, r.Uploads...), u)
		r.Revision++
		if e = s.Store.Swap(ctx, before, r, false); e != nil {
			return UploadReply{}, e
		}
		selected = &u
	}
	url, headers, e := s.Objects.Presign(ctx, *selected)
	if e != nil {
		return UploadReply{}, e
	}
	return UploadReply{r, selected.ID, url, headers, int(UploadLifetime.Seconds())}, nil
}
func cloneSlots(v map[string]string) map[string]string {
	out := map[string]string{}
	for k, x := range v {
		out[k] = x
	}
	return out
}
func (s Service) Finalize(ctx context.Context, owner, id string, in FinalizeRequest) (Record, error) {
	if !ValidOperation(in.OperationKey) {
		return Record{}, ErrInvalid
	}
	r, e := s.Get(ctx, owner, id)
	if e != nil {
		return r, e
	}
	if r.FinalizeKey == in.OperationKey {
		if !reflect.DeepEqual(r.Slots, in.UploadIDs) {
			return Record{}, ErrConflict
		}
		return r, nil
	}
	if r.State == "expired" {
		return r, ErrExpired
	}
	if r.State != "uploading" || r.Revision != in.Revision {
		return r, ErrConflict
	}
	if len(in.UploadIDs) != 2 || in.UploadIDs["front"] == "" || in.UploadIDs["back"] == "" || !reflect.DeepEqual(r.Slots, in.UploadIDs) {
		return r, ErrIncomplete
	}
	assets := []Asset{}
	for _, side := range []string{"front", "back"} {
		found := false
		for _, u := range r.Uploads {
			if u.ID == r.Slots[side] {
				a, e := s.Objects.Head(ctx, u)
				if e != nil {
					return r, e
				}
				if a.VersionID == "" || a.VersionID == "null" || a.Size != u.Size || a.SHA256 != u.SHA256 {
					return r, ErrImage
				}
				a.UploadID = u.ID
				a.Slot = side
				a.Key = u.Key
				assets = append(assets, a)
				found = true
				break
			}
		}
		if !found {
			return r, ErrIncomplete
		}
	}
	before := r
	r.Assets = assets
	r.FinalizeKey = in.OperationKey
	r.State = "finalizing"
	r.Work = "validate"
	r.Due = s.now().Unix()
	r.Generation++
	r.Revision++
	if e = s.Store.Swap(ctx, before, r, false); e != nil {
		return Record{}, e
	}
	return r, nil
}
func (s Service) Cancel(ctx context.Context, owner, id string) (Record, error) {
	r, e := s.Store.Get(ctx, owner, id)
	if e != nil {
		return r, e
	}
	if r.State == "cancelled" || r.State == "expired" {
		return r, nil
	}
	before := r
	r.State = "cancelled"
	r.Extraction = nil
	r.Reviewed = nil
	r.Work = "cleanup"
	r.Due = s.now().Add(UploadLifetime + time.Minute).Unix()
	r.Generation++
	r.LeaseUntil = 0
	r.Revision++
	if e = s.Store.Swap(ctx, before, r, false); e != nil {
		return Record{}, e
	}
	return r, nil
}
func (s Service) Retry(ctx context.Context, owner, id, key string) (Record, error) {
	if !ValidOperation(key) {
		return Record{}, ErrInvalid
	}
	r, e := s.Get(ctx, owner, id)
	if e != nil {
		return r, e
	}
	if r.RetryKey == key {
		return r, nil
	}
	if r.State == "ready" && r.Extraction != nil && r.Extraction.Status == "failed" {
		before := r
		r.Extraction = &Extraction{Status: "pending"}
		r.ExtractionAttempts = 0
		r.Work = "extract"
		r.Due = s.now().Unix()
		r.RetryKey = key
		r.Generation++
		r.Revision++
		r.LeaseUntil = 0
		if e = s.Store.Swap(ctx, before, r, false); e != nil {
			return Record{}, e
		}
		return r, nil
	}
	if r.State != "failed" || s.now().Unix() >= r.ExpiresAt {
		return r, ErrConflict
	}
	before := r
	r.State = "finalizing"
	r.Error = ""
	r.Work = "validate"
	r.Due = s.now().Unix()
	r.RetryKey = key
	r.Generation++
	r.Attempts = 0
	r.LeaseUntil = 0
	r.Revision++
	if e = s.Store.Swap(ctx, before, r, false); e != nil {
		return Record{}, e
	}
	return r, nil
}
