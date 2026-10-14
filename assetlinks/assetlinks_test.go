package assetlinks

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	fp := strings.Repeat("AB:", 31) + "AB"
	s, e := Render([]Application{{"com.example.dev", []string{strings.ToLower(fp)}}})
	if e != nil || !strings.Contains(s, fp) || !strings.Contains(s, "get_login_creds") {
		t.Fatal(s, e)
	}
	var statements []Statement
	if json.Unmarshal([]byte(s), &statements) != nil || len(statements) != 1 {
		t.Fatal(s)
	}
}
func TestReject(t *testing.T) {
	fp := strings.Repeat("AB:", 31) + "AB"
	for _, a := range [][]Application{{{"bad/package", []string{fp}}}, {{"com.example", nil}}, {{"com.example", []string{"AB"}}}, {{"com.example", []string{fp, fp}}}, {{"com.example", []string{fp}}, {"com.example", []string{fp}}}} {
		if _, e := Render(a); e == nil {
			t.Fatal(a)
		}
	}
}
func TestEmptyRevokesAll(t *testing.T) {
	s, e := Render(nil)
	if e != nil || s != "[]\n" {
		t.Fatal(s, e)
	}
}
