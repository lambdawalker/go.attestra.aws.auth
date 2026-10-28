package capture

import (
	"context"
	"errors"
	"testing"
	"time"
)

type extractorFunc func(context.Context, []CardImage) (CardData, error)

func (f extractorFunc) Extract(ctx context.Context, images []CardImage) (CardData, error) {
	return f(ctx, images)
}
func extractionFixture(t *testing.T) (Service, *memoryStore, *memoryObjects, *time.Time, Record) {
	s, m, o, now := fixture(t)
	s.Extractor = extractorFunc(func(_ context.Context, images []CardImage) (CardData, error) {
		if len(images) != 2 || images[0].Slot != "front" || images[1].Slot != "back" {
			t.Fatal("missing ordered images")
		}
		return CardData{FullName: "Test Person", DocumentNumber: "000123"}, nil
	})
	r := freeze(t, s, o)
	if e := s.Work(context.Background(), Job{r.Owner, r.ID, r.Generation, r.Work}); e != nil {
		t.Fatal(e)
	}
	return s, m, o, now, m.records[r.ID]
}
func TestExtractionPublishesSeparateDataAndReview(t *testing.T) {
	s, m, _, _, r := extractionFixture(t)
	if r.State != "ready" || r.Extraction == nil || r.Extraction.Status != "pending" || r.Work != "extract" {
		t.Fatal("extraction was not scheduled")
	}
	job := Job{r.Owner, r.ID, r.Generation, r.Work}
	if e := s.Work(context.Background(), job); e != nil {
		t.Fatal(e)
	}
	r = m.records[r.ID]
	if r.Extraction.Status != "complete" || r.Extraction.Data.DocumentNumber != "000123" || r.Reviewed != nil || m.events != 1 {
		t.Fatal("bad extraction result")
	}
	if e := s.Work(context.Background(), job); e != nil {
		t.Fatal(e)
	}
	data := r.Extraction.Data
	data.FullName = "Corrected Person"
	reviewed, e := s.Review(context.Background(), r.Owner, r.ID, ReviewRequest{Revision: r.Revision, Data: data})
	if e != nil || reviewed.Reviewed.Data.FullName != data.FullName || reviewed.Extraction.Data.FullName != "Test Person" {
		t.Fatal("review overwrote evidence", e)
	}
	if _, e = s.Review(context.Background(), "other", r.ID, ReviewRequest{Revision: r.Revision, Data: data}); e != ErrNotFound {
		t.Fatal("cross-owner review", e)
	}
	data.FullName = "Stale"
	if _, e = s.Review(context.Background(), r.Owner, r.ID, ReviewRequest{Revision: r.Revision, Data: data}); e != ErrConflict {
		t.Fatal("stale review", e)
	}
}
func TestExtractionCancellationFencesPublication(t *testing.T) {
	s, m, _, _, r := extractionFixture(t)
	s.Extractor = extractorFunc(func(context.Context, []CardImage) (CardData, error) {
		_, e := s.Cancel(context.Background(), r.Owner, r.ID)
		if e != nil {
			t.Fatal(e)
		}
		return CardData{FullName: "secret"}, nil
	})
	if e := s.Work(context.Background(), Job{r.Owner, r.ID, r.Generation, r.Work}); e != ErrConflict {
		t.Fatal("stale extraction not fenced", e)
	}
	if m.records[r.ID].Extraction != nil {
		t.Fatal("cancelled data exposed")
	}
}
func TestExtractionFailureRetryAndExpiry(t *testing.T) {
	s, m, _, now, r := extractionFixture(t)
	s.Extractor = extractorFunc(func(context.Context, []CardImage) (CardData, error) { return CardData{}, errors.New("provider secret") })
	for i := 0; i < 3; i++ {
		r = m.records[r.ID]
		*now = time.Unix(r.Due, 0)
		if e := s.Work(context.Background(), Job{r.Owner, r.ID, r.Generation, r.Work}); e != nil {
			t.Fatal(e)
		}
	}
	r = m.records[r.ID]
	if r.State != "ready" || r.Extraction.Status != "failed" || r.Extraction.Error != "extraction_unavailable" {
		t.Fatal("failure did not preserve images")
	}
	r, e := s.Retry(context.Background(), r.Owner, r.ID, "retry_extraction_1")
	if e != nil || r.Work != "extract" {
		t.Fatal("retry failed", e)
	}
	*now = time.Unix(r.DeleteAfter, 0)
	if e := s.Work(context.Background(), Job{r.Owner, r.ID, r.Generation, r.Work}); e != nil {
		t.Fatal(e)
	}
	if m.records[r.ID].Extraction != nil {
		t.Fatal("expired extraction retained")
	}
}

func TestExpiredAndCancelledFieldsNeverReturned(t *testing.T) {
	s, m, _, now, r := extractionFixture(t)
	if e := s.Work(context.Background(), Job{r.Owner, r.ID, r.Generation, r.Work}); e != nil {
		t.Fatal(e)
	}
	r = m.records[r.ID]
	r, e := s.Review(context.Background(), r.Owner, r.ID, ReviewRequest{Revision: r.Revision, Data: CardData{FullName: "Private"}})
	if e != nil {
		t.Fatal(e)
	}
	*now = time.Unix(r.DeleteAfter, 0)
	got, e := s.Get(context.Background(), r.Owner, r.ID)
	if e != nil || got.State != "expired" || got.Extraction != nil || got.Reviewed != nil {
		t.Fatal("expired PII returned", e)
	}
	if _, e = s.Review(context.Background(), r.Owner, r.ID, ReviewRequest{Revision: r.Revision, Data: CardData{FullName: "Other"}}); e != ErrExpired {
		t.Fatal("expired edit allowed", e)
	}
	if e = s.Work(context.Background(), Job{r.Owner, r.ID, r.Generation, r.Work}); e != nil {
		t.Fatal(e)
	}
	if m.records[r.ID].Reviewed != nil || m.records[r.ID].Extraction != nil {
		t.Fatal("cleanup retained fields")
	}
}
