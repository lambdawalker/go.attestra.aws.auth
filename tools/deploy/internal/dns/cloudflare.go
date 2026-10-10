package dns

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

type Record struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl,omitempty"`
	Proxied bool   `json:"proxied"`
}
type cloudflareAPIError struct {
	Code    int                  `json:"code"`
	Message string               `json:"message"`
	Chain   []cloudflareAPIError `json:"error_chain"`
}

// Only provider error fields are displayed, never raw bodies, headers or queries.
func (c *Client) responseError(status int, method, path string, details []cloudflareAPIError) error {
	clean := func(s string) string {
		if c.token != "" {
			s = strings.ReplaceAll(s, c.token, "[redacted]")
		}
		s = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				return ' '
			}
			return r
		}, s)
		r := []rune(s)
		if len(r) > 240 {
			s = string(r[:240]) + "…"
		}
		return s
	}
	message := fmt.Sprintf("Cloudflare HTTP %d during %s %s", status, method, clean(strings.SplitN(path, "?", 2)[0]))
	count := 0
	var appendErrors func([]cloudflareAPIError)
	appendErrors = func(items []cloudflareAPIError) {
		for _, item := range items {
			if count >= 3 {
				return
			}
			count++
			message += fmt.Sprintf("; code %d: %s", item.Code, clean(item.Message))
			appendErrors(item.Chain)
		}
	}
	appendErrors(details)
	switch status {
	case 400:
		message += "; request rejected; check the error above and enter only the API token value (no Bearer prefix)"
	case 401, 403:
		message += "; check API token validity and zone-scoped Zone Read / DNS Edit permissions"
	case 429:
		message += "; rate limit reached; retry later"
	default:
		if count == 0 {
			message += "; invalid or unsuccessful response (body suppressed)"
		}
	}
	return errors.New(message + "; completed changes are retained")
}

type Zone struct{ ID, Name, Status string }
type dnsChange struct {
	Record     Record
	Action, ID string
}
type Client struct {
	token, BaseURL string
	http           *http.Client
}

func NewClient(token string) *Client {
	return &Client{token: token, BaseURL: "https://api.cloudflare.com/client/v4", http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) Request(method, path string, body any, result any) (int, error) {
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return 0, err
		}
	}
	req, err := http.NewRequest(method, c.BaseURL+path, bytes.NewReader(data))
	if err != nil {
		return 0, errors.New("cannot prepare Cloudflare request")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return 0, errors.New("Cloudflare request failed; check connectivity and token (details suppressed)")
	}
	defer res.Body.Close()
	var envelope struct {
		Success    bool
		Errors     []cloudflareAPIError `json:"errors"`
		Result     json.RawMessage
		ResultInfo struct {
			TotalPages int `json:"total_pages"`
		} `json:"result_info"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&envelope)
	if decodeErr != nil || res.StatusCode < 200 || res.StatusCode >= 300 || !envelope.Success {
		if decodeErr != nil {
			envelope.Errors = nil
		}
		return 0, c.responseError(res.StatusCode, method, path, envelope.Errors)
	}
	if result != nil && json.Unmarshal(envelope.Result, result) != nil {
		return 0, errors.New("invalid Cloudflare result")
	}
	return envelope.ResultInfo.TotalPages, nil
}
func Name(s string) string { return strings.TrimSuffix(strings.ToLower(s), ".") }
func WithinZone(name, zone string) bool {
	return Name(name) == Name(zone) || strings.HasSuffix(Name(name), "."+Name(zone))
}
func ValidID(id string) bool {
	return regexp.MustCompile(`^[a-fA-F0-9]{32}$`).MatchString(id)
}
func (c *Client) Zones(domain string) ([]Zone, error) {
	var zones []Zone
	for page := 1; ; page++ {
		var batch []Zone
		pages, err := c.Request("GET", fmt.Sprintf("/zones?per_page=50&page=%d", page), nil, &batch)
		if err != nil {
			return nil, err
		}
		for _, zone := range batch {
			if zone.Status == "active" && ValidID(zone.ID) && WithinZone(domain, zone.Name) {
				zones = append(zones, zone)
			}
		}
		if (pages > 0 && page >= pages) || (pages == 0 && len(batch) < 50) {
			break
		}
	}
	return zones, nil
}
func (c *Client) Records(zone, name string) ([]Record, error) {
	if !ValidID(zone) {
		return nil, errors.New("invalid Cloudflare zone ID")
	}
	var records []Record
	for page := 1; ; page++ {
		var batch []Record
		pages, err := c.Request("GET", fmt.Sprintf("/zones/%s/dns_records?name=%s&per_page=100&page=%d", zone, url.QueryEscape(name), page), nil, &batch)
		if err != nil {
			return nil, err
		}
		for _, record := range batch {
			if Name(record.Name) != Name(name) {
				return nil, errors.New("Cloudflare returned a record outside the requested name")
			}
			records = append(records, record)
		}
		if (pages > 0 && page >= pages) || (pages == 0 && len(batch) < 100) {
			break
		}
	}
	return records, nil
}
func PlanRecord(want Record, existing []Record) (dnsChange, error) {
	change := dnsChange{Record: want, Action: "create"}
	if want.Type != "TXT" && want.Type != "CNAME" {
		return change, errors.New("only SES TXT and CNAME records are supported")
	}
	matched := false
	for _, old := range existing {
		if want.Type == "TXT" {
			if old.Type == "CNAME" || old.Type == "NS" {
				return change, fmt.Errorf("conflicting %s at %s; resolve it manually before retrying", old.Type, want.Name)
			}
			if old.Type == "TXT" && strings.Trim(old.Content, `"`) == want.Content {
				matched = true
			}
			continue
		}
		if old.Type != "CNAME" || Name(old.Content) != Name(want.Content) || len(existing) > 1 {
			return change, fmt.Errorf("conflicting record at %s; no automatic replacement; review it in Cloudflare", want.Name)
		}
		matched = true
		if old.Proxied {
			if !ValidID(old.ID) {
				return change, errors.New("invalid Cloudflare DNS record ID")
			}
			change.Action, change.ID = "unproxy", old.ID
			return change, nil
		}
	}
	if matched {
		change.Action = "keep"
	}
	return change, nil
}
func (c *Client) Plan(zone string, desired []Record) ([]dnsChange, error) {
	var changes []dnsChange
	for _, want := range desired {
		existing, err := c.Records(zone, want.Name)
		if err != nil {
			return nil, err
		}
		change, err := PlanRecord(want, existing)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}
	return changes, nil
}
func (c *Client) Apply(zone string, changes []dnsChange) error {
	// Re-read every name before writing: changed/conflicting records need a new review.
	for _, change := range changes {
		existing, err := c.Records(zone, change.Record.Name)
		if err != nil {
			return err
		}
		current, err := PlanRecord(change.Record, existing)
		if err != nil {
			return err
		}
		if current.Action == "keep" {
			continue
		}
		if current.Action != change.Action || current.ID != change.ID {
			return errors.New("DNS changed since review; rerun to review the new plan")
		}
		path := "/zones/" + zone + "/dns_records"
		if current.Action == "unproxy" {
			_, err = c.Request("PATCH", path+"/"+current.ID, map[string]bool{"proxied": false}, nil)
		} else {
			record := change.Record
			record.TTL = 300
			record.Proxied = false
			_, err = c.Request("POST", path, record, nil)
		}
		if err != nil {
			return err
		}
		fmt.Printf("✓ %s %s %s\n", current.Action, change.Record.Type, change.Record.Name)
	}
	desired := make([]Record, 0, len(changes))
	for _, change := range changes {
		desired = append(desired, change.Record)
	}
	after, err := c.Plan(zone, desired)
	if err != nil {
		return err
	}
	for _, change := range after {
		if change.Action != "keep" {
			return errors.New("DNS read-back verification incomplete; rerun setup, keeping successful changes")
		}
	}
	return nil
}

func DeletionCandidates(want Record, existing []Record) ([]Record, error) {
	var matches []Record
	for _, r := range existing {
		if Name(r.Name) != Name(want.Name) {
			return nil, errors.New("unexpected DNS name")
		}
		same := r.Type == want.Type && ((r.Type == "TXT" && strings.Trim(r.Content, `"`) == strings.Trim(want.Content, `"`)) || (r.Type == "CNAME" && Name(r.Content) == Name(want.Content)))
		if same {
			if !ValidID(r.ID) {
				return nil, errors.New("invalid DNS record ID")
			}
			matches = append(matches, r)
		} else if want.Type == "CNAME" || r.Type == "CNAME" || r.Type == "NS" {
			return nil, fmt.Errorf("DNS conflict at %s; inspect it before teardown", want.Name)
		}
	}
	return matches, nil
}
func (c *Client) DeleteDNS(zone string, records []Record) error {
	for _, approved := range records {
		current, err := c.Records(zone, approved.Name)
		if err != nil {
			return err
		}
		matches, err := DeletionCandidates(approved, current)
		if err != nil {
			return err
		}
		found := false
		for _, r := range matches {
			if r.ID == approved.ID {
				found = true
			} else {
				return errors.New("matching DNS record was replaced since approval; inspect the saved teardown plan")
			}
		}
		if !found {
			continue
		}
		if _, err = c.Request("DELETE", "/zones/"+zone+"/dns_records/"+approved.ID, nil, nil); err != nil {
			return err
		}
		fmt.Println("✓ Deleted DNS record", approved.Type, approved.Name)
	}
	for _, approved := range records {
		current, err := c.Records(zone, approved.Name)
		if err != nil {
			return err
		}
		for _, r := range current {
			if r.ID == approved.ID {
				return errors.New("DNS deletion read-back failed")
			}
		}
	}
	return nil
}
