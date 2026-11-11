package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"stream-agents/internal/store"
)

func sessionsMux(sessions []store.Session, now time.Time) http.Handler {
	return NewMux(store.NewIndex(statsStore{sessions}), Options{
		Host: "mac-mini", Now: func() time.Time { return now }, Logf: func(string, ...any) {},
	})
}

func TestSessionsAPI(t *testing.T) {
	now := time.Date(2026, 11, 11, 10, 15, 2, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	sess := func(id, title string, turn store.TurnState) store.Session {
		return store.Session{Agent: "claude", ID: id, Project: "/Users/yarik/dev/carthing", Title: title, Modified: turn.LastActivity, Turn: turn}
	}
	h := sessionsMux([]store.Session{
		sess("work", "Add tests", store.TurnState{State: store.StateWorking, Since: ago(time.Minute), LastActivity: ago(5 * time.Second), Model: "claude-opus-5-5"}),
		sess("wait", "Fix the parser", store.TurnState{State: store.StateWaiting, Reason: store.ReasonTurnDone, Since: ago(142 * time.Second), LastActivity: ago(142 * time.Second), Model: "claude-fable-5-2"}),
		sess("stale", "Old", store.TurnState{State: store.StateWaiting, Reason: store.ReasonTurnDone, Since: ago(2 * time.Hour), LastActivity: ago(2 * time.Hour)}),
		{Agent: "claude", ID: "sub", Sidechain: true, Modified: now, Turn: store.TurnState{State: store.StateWorking, Since: now, LastActivity: now}},
	}, now)

	w := apiGet(h, "/api/v1/sessions", "192.168.2.50:5555")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d type %q: %s", w.Code, w.Header().Get("Content-Type"), w.Body)
	}
	var got SessionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := SessionsResponse{GeneratedAt: "2026-11-11T10:15:02Z", Host: "mac-mini", Sessions: []ActiveSession{
		{Agent: "claude", ID: "wait", Project: "carthing", Title: "Fix the parser", Model: "claude-fable-5-2", State: "waiting", WaitingReason: "turn_done", Since: "2026-11-11T10:12:40Z", LastActivity: "2026-11-11T10:12:40Z"},
		{Agent: "claude", ID: "work", Project: "carthing", Title: "Add tests", Model: "claude-opus-5-5", State: "working", WaitingReason: "", Since: "2026-11-11T10:14:02Z", LastActivity: "2026-11-11T10:14:57Z"},
	}}
	if got.GeneratedAt != want.GeneratedAt || got.Host != want.Host || len(got.Sessions) != len(want.Sessions) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	for i := range want.Sessions {
		if got.Sessions[i] != want.Sessions[i] {
			t.Errorf("session %d = %+v\nwant %+v", i, got.Sessions[i], want.Sessions[i])
		}
	}
	// Every field is always present, waiting_reason included.
	var raw struct{ Sessions []map[string]any }
	_ = json.Unmarshal(w.Body.Bytes(), &raw)
	for _, k := range []string{"agent", "id", "project", "title", "model", "state", "waiting_reason", "since", "last_activity"} {
		if _, ok := raw.Sessions[1][k]; !ok {
			t.Errorf("field %s missing", k)
		}
	}

	req := localRequest("POST", "/api/v1/sessions")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 405 {
		t.Errorf("POST: status %d", rec.Code)
	}
}

func TestSessionsAPIEmpty(t *testing.T) {
	w := apiGet(sessionsMux(nil, time.Now()), "/api/v1/sessions", "")
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["sessions"]) != "[]" {
		t.Errorf("sessions = %s, want []", raw["sessions"])
	}
}
