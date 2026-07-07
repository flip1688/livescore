package thscore

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseMatchTime(t *testing.T) {
	got, err := ParseMatchTime("08-07-2026 20:30:00")
	if err != nil {
		t.Fatalf("ParseMatchTime: %v", err)
	}
	// 08-07-2026 20:30:00 GMT+7 == 2026-07-08 13:30:00 UTC.
	want := time.Date(2026, time.July, 8, 13, 30, 0, 0, time.UTC)
	if !got.UTC().Equal(want) {
		t.Fatalf("got %v (UTC %v), want %v", got, got.UTC(), want)
	}
}

func TestParseMatchTime_Invalid(t *testing.T) {
	if _, err := ParseMatchTime("not-a-time"); err == nil {
		t.Fatal("expected error for invalid input, got nil")
	}
}

func TestFlexString(t *testing.T) {
	var v struct {
		Str FlexString `json:"str"`
		Num FlexString `json:"num"`
		Nul FlexString `json:"nul"`
	}
	if err := json.Unmarshal([]byte(`{"str":"3521","num":797,"nul":null}`), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v.Str != "3521" || v.Num != "797" || v.Nul != "" {
		t.Fatalf("got %q/%q/%q, want 3521/797/(empty)", v.Str, v.Num, v.Nul)
	}
	if err := json.Unmarshal([]byte(`{"str":true}`), &v); err == nil {
		t.Fatal("expected error for bool input, got nil")
	}
}

func TestParseTimeAny(t *testing.T) {
	wantInstant := time.Unix(1751980800, 0).UTC()
	// 08-07-2026 01:29:31 GMT+7 == 2026-07-07 18:29:31 UTC.
	wantDatetime := time.Date(2026, time.July, 7, 18, 29, 31, 0, time.UTC)
	cases := []struct {
		name string
		in   any
		want time.Time
		ok   bool
	}{
		{"int64", int64(1751980800), wantInstant, true},
		{"int", int(1751980800), wantInstant, true},
		{"float64", float64(1751980800), wantInstant, true},
		{"string", "1751980800", wantInstant, true},
		{"datetime string", "08-07-2026 01:29:31", wantDatetime, true},
		{"empty string", "", time.Time{}, false},
		{"zero", int64(0), time.Time{}, false},
		{"negative", int64(-5), time.Time{}, false},
		{"non-time string", "not-a-time", time.Time{}, false},
		{"nil", nil, time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseTimeAny(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && !got.Equal(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
