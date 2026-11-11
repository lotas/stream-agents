package server

import (
	"encoding/json"
	"testing"
)

func TestLimitsAPI(t *testing.T) {
	h := apiMux(Options{Host: "mac-mini", ClaudeJSONPaths: func() []string { return []string{"../../testdata/limits/claude.json"} }})
	w := apiGet(h, "/api/v1/limits", "192.168.2.50:5555")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"generated_at", "host", "available", "source", "fetched_at", "age_seconds", "five_hour", "seven_day", "seven_day_opus", "seven_day_sonnet", "session", "limits", "extra_usage", "weekly_breakdown"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("field %s missing", k)
		}
	}
	if string(raw["available"]) != "true" || string(raw["host"]) != `"mac-mini"` {
		t.Errorf("available %s host %s", raw["available"], raw["host"])
	}
}

func TestLimitsAPIWithoutCache(t *testing.T) {
	for _, opts := range []Options{{}, {ClaudeJSONPaths: func() []string { return []string{"/nonexistent"} }}} {
		w := apiGet(apiMux(opts), "/api/v1/limits", "")
		var raw map[string]json.RawMessage
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &raw) != nil {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		if string(raw["available"]) != "false" || string(raw["limits"]) != "[]" || string(raw["five_hour"]) != "null" {
			t.Errorf("body %s", w.Body)
		}
	}
}
