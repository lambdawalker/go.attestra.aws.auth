package capture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"image"
	"image/jpeg"
	"testing"
	"time"
)

type memoryStore struct {
	account Account
	records map[string]Record
	events  int
}

func (m *memoryStore) Account(_ context.Context, owner string) (Account, error) {
	return m.account, nil
}
func (m *memoryStore) Get(_ context.Context, owner, id string) (Record, error) {
	r, ok := m.records[id]
	if !ok || r.Owner != owner {
		return Record{}, ErrNotFound
	}
	return r, nil
}
func (m *memoryStore) FindCreate(ctx context.Context, owner, key string) (Record, error) {
	for _, r := range m.records {
		if r.Owner == owner && r.CreateKey == key {
			return r, nil
		}
	}
	return Record{}, ErrNotFound
}
func (m *memoryStore) Create(_ context.Context, a, b Account, r Record) error {
	if m.account.Revision != a.Revision {
		return ErrConflict
	}
	m.account = b
	m.records[r.ID] = r
	return nil
}
func (m *memoryStore) Swap(_ context.Context, a, b Record, ready bool) error {
	if m.records[a.ID].Revision != a.Revision {
		return ErrConflict
	}
	m.records[a.ID] = b
	if ready {
		m.events++
	}
	return nil
}

type memoryObjects struct {
	data        []byte
	readVersion string
	onRead      func()
	deleted     bool
}

func (m *memoryObjects) Presign(_ context.Context, u Upload) (string, map[string]string, error) {
	return "https://bucket.s3.us-east-1.amazonaws.com/" + u.Key, map[string]string{}, nil
}
func (m *memoryObjects) Head(_ context.Context, u Upload) (Asset, error) {
	return Asset{VersionID: "frozen-version", Size: u.Size, SHA256: u.SHA256}, nil
}
func (m *memoryObjects) Read(_ context.Context, a Asset) ([]byte, error) {
	m.readVersion = a.VersionID
	if m.onRead != nil {
		m.onRead()
	}
	return bytes.Clone(m.data), nil
}
func (m *memoryObjects) Write(_ context.Context, r Record, slot string, b []byte) (Asset, error) {
	sum := sha256.Sum256(b)
	return Asset{Slot: slot, Key: "processed/" + r.ID + "/" + slot, VersionID: "processed-version", Size: int64(len(b)), SHA256: base64.StdEncoding.EncodeToString(sum[:])}, nil
}
func (m *memoryObjects) DeleteCapture(context.Context, string) error { m.deleted = true; return nil }
func fixture(t *testing.T) (Service, *memoryStore, *memoryObjects, *time.Time) {
	t.Helper()
	now := time.Unix(1800000000, 0)
	m := &memoryStore{records: map[string]Record{}}
	o := &memoryObjects{}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 16, 16)), nil); err != nil {
		t.Fatal(err)
	}
	o.data = b.Bytes()
	s := Service{Store: m, Objects: o, Enabled: true, Now: func() time.Time { return now }}
	return s, m, o, &now
}
func freeze(t *testing.T, s Service, o *memoryObjects) Record {
	t.Helper()
	ctx := context.Background()
	r, e := s.Create(ctx, "owner", "create_operation_1", "sample_card")
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(o.data)
	for _, slot := range []string{"front", "back"} {
		reply, e := s.Upload(ctx, "owner", r.ID, UploadRequest{OperationKey: "upload_operation_" + slot, Revision: r.Revision, Slot: slot, SHA256: base64.StdEncoding.EncodeToString(sum[:]), Size: int64(len(o.data))})
		if e != nil {
			t.Fatal(e)
		}
		r = reply.Capture
	}
	r, e = s.Finalize(ctx, "owner", r.ID, FinalizeRequest{OperationKey: "finalize_operation_1", Revision: r.Revision, UploadIDs: r.Slots})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestCreateReplayAndOwnership(t *testing.T) {
	s, _, _, _ := fixture(t)
	ctx := context.Background()
	r, e := s.Create(ctx, "owner", "create_operation_1", "sample_card")
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.Create(ctx, "owner", "create_operation_1", "sample_card")
	if e != nil || r.ID != again.ID {
		t.Fatal("create was not idempotent", e)
	}
	if _, e = s.Get(ctx, "other", r.ID); e != ErrNotFound {
		t.Fatal("cross-account read", e)
	}
}
func TestFinalizePinsVersionsAndPublishesOnce(t *testing.T) {
	s, m, o, _ := fixture(t)
	r := freeze(t, s, o)
	job := Job{Owner: r.Owner, ID: r.ID, Generation: r.Generation, Work: r.Work}
	if e := s.Work(context.Background(), job); e != nil {
		t.Fatal(e)
	}
	if e := s.Work(context.Background(), job); e != nil {
		t.Fatal(e)
	}
	if m.records[r.ID].State != "ready" || m.events != 1 || o.readVersion != "frozen-version" {
		t.Fatal("manifest/ready event not preserved")
	}
}
func TestCancelFencesRunningWorker(t *testing.T) {
	s, m, o, _ := fixture(t)
	r := freeze(t, s, o)
	o.onRead = func() {
		o.onRead = nil
		_, e := s.Cancel(context.Background(), r.Owner, r.ID)
		if e != nil {
			t.Fatal(e)
		}
	}
	_ = s.Work(context.Background(), Job{r.Owner, r.ID, r.Generation, r.Work})
	if m.records[r.ID].State != "cancelled" || m.events != 0 {
		t.Fatal("stale worker published")
	}
}
func TestInvalidJPEGRequiresRecapture(t *testing.T) {
	s, m, o, _ := fixture(t)
	o.data = []byte("not a jpeg")
	r := freeze(t, s, o)
	if e := s.Work(context.Background(), Job{r.Owner, r.ID, r.Generation, r.Work}); e != nil {
		t.Fatal(e)
	}
	if m.records[r.ID].State != "requires_recapture" || m.events != 0 {
		t.Fatal("invalid bytes accepted")
	}
}
func TestExpiredSessionAndCleanup(t *testing.T) {
	s, m, o, now := fixture(t)
	r, e := s.Create(context.Background(), "owner", "create_operation_1", "sample_card")
	if e != nil {
		t.Fatal(e)
	}
	*now = now.Add(25 * time.Hour)
	got, _ := s.Get(context.Background(), r.Owner, r.ID)
	if got.State != "expired" {
		t.Fatal("expiry ignored")
	}
	if e = s.Work(context.Background(), Job{r.Owner, r.ID, r.Generation, r.Work}); e != nil {
		t.Fatal(e)
	}
	if !o.deleted || m.records[r.ID].Work != "" {
		t.Fatal("cleanup incomplete")
	}
}
func TestStaleFinalizeCannotSelectOldSlots(t *testing.T) {
	s, _, o, _ := fixture(t)
	r := freeze(t, s, o)
	_, e := s.Finalize(context.Background(), r.Owner, r.ID, FinalizeRequest{OperationKey: "another_finalize_1", Revision: 1, UploadIDs: r.Slots})
	if e != ErrConflict {
		t.Fatal(e)
	}
}
func TestExpiredFinalizingCaptureCannotPublishReady(t *testing.T) {
	s, m, o, now := fixture(t)
	r := freeze(t, s, o)
	*now = now.Add(25 * time.Hour)
	if e := s.Work(context.Background(), Job{r.Owner, r.ID, r.Generation, r.Work}); e != nil {
		t.Fatal(e)
	}
	if m.events != 0 || m.records[r.ID].State != "expired" || m.records[r.ID].Work != "cleanup" {
		t.Fatal("expired finalizing evidence promoted")
	}
}
