package main

import "testing"

func TestCaptureConcurrency(t *testing.T) {
	for _, tt := range []struct {
		name, reserved, worker   string
		wantReserved, wantWorker int
		invalid                  bool
	}{
		{"defaults", "", "", -1, 2, false},
		{"dev shared pool", "-1", "2", -1, 2, false},
		{"production overrides", "20", "10", 20, 10, false},
		{"zero disables functions", "0", "2", 0, 0, true},
		{"negative reservation", "-2", "2", 0, 0, true},
		{"invalid reservation", "abc", "2", 0, 0, true},
		{"invalid worker", "5", "abc", 0, 0, true},
		{"worker too small", "-1", "1", 0, 0, true},
		{"worker too large", "-1", "1001", 0, 0, true},
		{"worker exceeds reservation", "5", "6", 0, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, w, err := parseCaptureConcurrency(tt.reserved, tt.worker)
			if tt.invalid {
				if err == nil {
					t.Fatal("expected invalid configuration to be rejected")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if r != tt.wantReserved || w != tt.wantWorker {
				t.Fatalf("got (%d,%d), want (%d,%d)", r, w, tt.wantReserved, tt.wantWorker)
			}
		})
	}
}

func TestEffectiveCaptureReservation(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int
		bad   bool
	}{{"", 5, false}, {"5", 5, false}, {"-1", -1, false}, {"0", 0, true}, {"10", 0, true}, {"invalid", 0, true}} {
		got, err := effectiveCaptureReservation(5, tc.value)
		if (err != nil) != tc.bad || (!tc.bad && got != tc.want) {
			t.Fatal(tc, got, err)
		}
	}
}
