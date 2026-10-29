package capture

import (
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"
)

// CardData is transcription, not verified identity. Empty means absent or unreadable.
// Numbers remain strings to preserve leading zeros; dates remain as printed.
type CardData struct {
	FullName       string `json:"full_name"`
	GivenNames     string `json:"given_names"`
	Surnames       string `json:"surnames"`
	BirthDate      string `json:"birth_date"`
	DocumentNumber string `json:"document_number"`
	PersonalNumber string `json:"personal_number"`
	Address        string `json:"address"`
	IssueDate      string `json:"issue_date"`
	ExpiryDate     string `json:"expiry_date"`
	IssuingCountry string `json:"issuing_country"`
}

func (d CardData) Valid() bool {
	for _, v := range []string{d.FullName, d.GivenNames, d.Surnames, d.BirthDate, d.DocumentNumber, d.PersonalNumber, d.Address, d.IssueDate, d.ExpiryDate, d.IssuingCountry} {
		if !utf8.ValidString(v) || len(v) > 512 || strings.ContainsAny(v, "\x00\r") {
			return false
		}
	}
	return true
}
func (d CardData) Empty() bool { return d == (CardData{}) }

type Extraction struct {
	Status string   `json:"status"`
	Data   CardData `json:"data"`
	Error  string   `json:"error,omitempty"`
}
type ReviewedCard struct {
	OriginCountry string   `json:"origin_country,omitempty"`
	Data          CardData `json:"data"`
	SavedAt       int64    `json:"saved_at"`
}
type ReviewRequest struct {
	OriginCountry string   `json:"origin_country,omitempty"`
	Revision      int64    `json:"expected_revision"`
	Data          CardData `json:"data"`
}
type CardImage struct {
	Slot string
	JPEG []byte
}
type Extractor interface {
	Extract(context.Context, []CardImage) (CardData, error)
}

func (s Service) Review(ctx context.Context, owner, id string, in ReviewRequest) (Record, error) {
	if !in.Data.Valid() || in.Data.Empty() || !ValidOrigin(in.OriginCountry) {
		return Record{}, ErrInvalid
	}
	r, e := s.Get(ctx, owner, id)
	if e != nil {
		return r, e
	}
	if r.State == "expired" {
		return Record{}, ErrExpired
	}
	if r.State != "ready" || r.Extraction == nil || (r.Extraction.Status != "complete" && r.Extraction.Status != "failed") {
		return Record{}, ErrConflict
	}
	// Identical retry after a lost response is safe, without persisting PII in operation checkpoints.
	if r.Reviewed != nil && r.Reviewed.Data == in.Data && r.Reviewed.OriginCountry == in.OriginCountry {
		return r, nil
	}
	if r.Revision != in.Revision {
		return Record{}, ErrConflict
	}
	before := r
	r.Revision++
	r.Reviewed = &ReviewedCard{OriginCountry: in.OriginCountry, Data: in.Data, SavedAt: s.now().Unix()}
	if e = s.Store.Swap(ctx, before, r, false); e != nil {
		return Record{}, e
	}
	return r, nil
}

func (s Service) extract(ctx context.Context, r Record) error {
	if r.State != "ready" || r.Extraction == nil || r.Extraction.Status != "pending" {
		return nil
	}
	if s.now().Unix() >= r.DeleteAfter {
		return s.expireExtraction(ctx, r)
	}
	if r.ExtractionAttempts >= 3 {
		return s.finishExtraction(ctx, r, CardData{}, ErrInvalid)
	}
	before := r
	r.Revision++
	r.ExtractionAttempts++
	r.LeaseUntil = s.now().Add(120 * time.Second).Unix()
	r.Due = r.LeaseUntil
	if e := s.Store.Swap(ctx, before, r, false); e != nil {
		return e
	}
	images := []CardImage{}
	defer func() {
		for _, im := range images {
			clear(im.JPEG)
		}
	}()
	var extractionErr error
	for _, side := range []string{"front", "back"} {
		var asset *Asset
		for i := range r.Processed {
			if r.Processed[i].Slot == side {
				asset = &r.Processed[i]
				break
			}
		}
		if asset == nil {
			extractionErr = ErrImage
			break
		}
		b, e := s.Objects.Read(ctx, *asset)
		if e != nil {
			extractionErr = e
			break
		}
		images = append(images, CardImage{side, b})
		if e = ValidateJPEG(b, *asset); e != nil {
			extractionErr = e
			break
		}
	}
	var data CardData
	if extractionErr == nil {
		if s.Extractor == nil {
			extractionErr = ErrDisabled
		} else {
			bounded, cancel := context.WithTimeout(ctx, 60*time.Second)
			data, extractionErr = s.Extractor.Extract(bounded, images)
			cancel()
			if extractionErr == nil && !data.Valid() {
				extractionErr = ErrInvalid
			}
		}
	}
	if s.now().Unix() >= r.DeleteAfter {
		return s.expireExtraction(ctx, r)
	}
	return s.finishExtraction(ctx, r, data, extractionErr)
}
func (s Service) expireExtraction(ctx context.Context, r Record) error {
	before := r
	r.Revision++
	r.Generation++
	r.State = "expired"
	r.Extraction = nil
	r.Reviewed = nil
	r.Work = "cleanup"
	r.Due = s.now().Unix()
	r.LeaseUntil = 0
	return s.Store.Swap(ctx, before, r, false)
}
func (s Service) finishExtraction(ctx context.Context, r Record, data CardData, err error) error {
	slog.Info("capture.extraction_result", "capture_id", r.ID, "attempt", r.ExtractionAttempts, "failed", err != nil)
	before := r
	r.Revision++
	r.LeaseUntil = 0
	if err == nil {
		r.Extraction = &Extraction{Status: "complete", Data: data}
		r.Work = "cleanup"
		r.Due = r.DeleteAfter
	} else if r.ExtractionAttempts >= 3 {
		r.Extraction = &Extraction{Status: "failed", Error: "extraction_unavailable"}
		r.Work = "cleanup"
		r.Due = r.DeleteAfter
	} else {
		r.Due = s.now().Add(time.Duration(r.ExtractionAttempts) * time.Minute).Unix()
	}
	return s.Store.Swap(ctx, before, r, false)
}
