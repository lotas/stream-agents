package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"stream-agents/internal/report"
	"stream-agents/internal/store"
)

var apiLoc = time.FixedZone("UTC+3", 3*3600)

func apiSessions() []store.Session {
	opus := func(input int, cost float64) store.ModelUsage {
		return store.ModelUsage{Model: "claude-opus-5-5", TokenUsage: store.TokenUsage{InputTokens: input, OutputTokens: 1, CacheReadTokens: 2, CacheCreationTokens: 3}, Tokens: true, HasCost: true, Cost: cost}
	}
	return []store.Session{
		// 01:00 local on Oct 16 is still Oct 15 in UTC; it counts towards Oct 16.
		{Agent: "claude", ID: "a", Started: time.Date(2026, 10, 15, 22, 0, 0, 0, time.UTC), Usage: []store.ModelUsage{opus(100, 1)}},
		{Agent: "codex", ID: "b", Started: time.Date(2026, 10, 14, 9, 0, 0, 0, apiLoc), Usage: []store.ModelUsage{
			{Provider: "openai", Model: "gpt-5.5", TokenUsage: store.TokenUsage{InputTokens: 10}, Tokens: true, HasCost: true, Cost: 2},
		}},
		{Agent: "claude", ID: "c", Started: time.Date(2026, 10, 14, 10, 0, 0, 0, apiLoc), Usage: []store.ModelUsage{opus(5, .1), opus(5, .2)}},
		// Outside a 3-day window ending Oct 16.
		{Agent: "claude", ID: "d", Started: time.Date(2026, 10, 13, 23, 59, 0, 0, apiLoc), Usage: []store.ModelUsage{opus(9999, 99)}},
	}
}

var apiNow = time.Date(2026, 10, 16, 12, 0, 0, 0, apiLoc)

func apiMux(opts Options) http.Handler {
	idx := store.NewIndex(statsStore{apiSessions()})
	opts.Now = func() time.Time { return apiNow }
	opts.Location = apiLoc
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	return NewMux(idx, opts)
}

// localRequest is a request from a loopback peer; httptest.NewRequest
// defaults to a remote one, which only reaches /api/v1/.
func localRequest(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = "127.0.0.1:1234"
	return req
}

func apiGet(h http.Handler, path, remote string) *httptest.ResponseRecorder {
	req := localRequest("GET", path)
	if remote != "" {
		req.RemoteAddr = remote
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestBuildUsageZeroFillsLocalDays(t *testing.T) {
	u := BuildUsage(apiSessions(), apiNow, apiLoc, 3, map[string]float64{"codex": 1.75})
	var dates []string
	for _, d := range u.Days {
		dates = append(dates, d.Date)
	}
	if got := strings.Join(dates, " "); got != "2026-10-14 2026-10-15 2026-10-16" {
		t.Fatalf("dates = %s", got)
	}
	if u.Days[1].By == nil || len(u.Days[1].By) != 0 {
		t.Fatalf("empty day = %#v, want empty non-nil slice", u.Days[1].By)
	}
	oct14 := u.Days[0].By
	if len(oct14) != 2 || oct14[0].Agent != "claude" || oct14[0].InputTokens != 10 || oct14[0].Cost != 0.3 || oct14[0].Sessions != 1 {
		t.Fatalf("oct 14 = %+v", oct14)
	}
	if oct14[1].Agent != "codex" || oct14[1].Provider != "openai" || oct14[1].Cost != 2 {
		t.Fatalf("oct 14 codex = %+v", oct14[1])
	}
	if today := u.Days[2].By; len(today) != 1 || today[0].InputTokens != 100 || today[0].CacheWriteTokens != 3 {
		t.Fatalf("today = %+v", today)
	}
	if u.GeneratedAt != "2026-10-16T09:00:00Z" || u.Timezone != "UTC+3" || u.UTCOffset != "+03:00" || u.Currency != "USD" {
		t.Fatalf("header = %+v", u)
	}

	full := BuildUsage(apiSessions(), apiNow, apiLoc, 31, nil)
	if len(full.Days) != 31 || full.Days[0].Date != "2026-09-16" || full.Days[30].Date != "2026-10-16" {
		t.Fatalf("31 days = %s..%s (%d)", full.Days[0].Date, full.Days[len(full.Days)-1].Date, len(full.Days))
	}
}

// The API must agree with the stats CLI, which aggregates with report.Build.
func TestBuildUsageMatchesReport(t *testing.T) {
	sessions, err := store.NewIndex(statsStore{apiSessions()}).ListAll(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 10, 7, 0, 0, 0, 0, apiLoc)
	rep := report.Build(sessions, report.Options{Group: "day", From: from, To: from.AddDate(0, 0, 9)}, apiLoc)
	u := BuildUsage(sessions, apiNow, apiLoc, 10, nil)
	var cost, billed float64
	var input int
	for _, d := range u.Days {
		for _, e := range d.By {
			cost, billed, input = cost+e.Cost, billed+e.Billed, input+e.InputTokens
		}
	}
	if fmt.Sprintf("%.6f %.6f %d", cost, billed, input) != fmt.Sprintf("%.6f %.6f %d", rep.Total.Cost, rep.Total.Billed, rep.Total.Input) {
		t.Fatalf("api %v %v %d, report %+v", cost, billed, input, rep.Total)
	}
	if billed != cost-2+2*1.75 {
		t.Fatalf("billed %v does not apply the codex multiplier to cost %v", billed, cost)
	}
}

func keys(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// The JSON field names are a contract with other programs.
func TestUsageJSONShape(t *testing.T) {
	w := apiGet(apiMux(Options{}), "/api/v1/usage?days=3", "")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("status %d headers %v: %s", w.Code, w.Header(), w.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if got, want := keys(body), []string{"cost_multipliers", "currency", "days", "generated_at", "timezone", "utc_offset"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("top-level keys = %v, want %v", got, want)
	}
	days := body["days"].([]any)
	if len(days) != 3 {
		t.Fatalf("days = %v", days)
	}
	if got := keys(days[1].(map[string]any)); !reflect.DeepEqual(got, []string{"by", "date"}) {
		t.Fatalf("day keys = %v", got)
	}
	if by := days[1].(map[string]any)["by"].([]any); len(by) != 0 {
		t.Fatalf("zero day by = %v", by)
	}
	entry := days[0].(map[string]any)["by"].([]any)[0].(map[string]any)
	want := []string{"agent", "billed", "cache_read_tokens", "cache_write_tokens", "cost", "cost_partial", "input_tokens", "model", "output_tokens", "provider", "sessions"}
	if got := keys(entry); !reflect.DeepEqual(got, want) {
		t.Fatalf("entry keys = %v, want %v", got, want)
	}
	if body["cost_multipliers"].(map[string]any)["codex"] != 1.75 {
		t.Fatalf("cost_multipliers = %v", body["cost_multipliers"])
	}
}

func TestUsageDaysParam(t *testing.T) {
	h := apiMux(Options{})
	for _, tc := range []struct {
		query string
		code  int
		days  int
	}{{"", 200, 31}, {"?days=1", 200, 1}, {"?days=5000", 200, maxUsageDays}, {"?days=0", 400, 0}, {"?days=-3", 400, 0}, {"?days=x", 400, 0}} {
		w := apiGet(h, "/api/v1/usage"+tc.query, "")
		if w.Code != tc.code {
			t.Errorf("%q: status %d, want %d", tc.query, w.Code, tc.code)
			continue
		}
		if tc.code == 200 {
			var u UsageResponse
			json.Unmarshal(w.Body.Bytes(), &u)
			if len(u.Days) != tc.days {
				t.Errorf("%q: %d days, want %d", tc.query, len(u.Days), tc.days)
			}
		}
	}
}

func TestHealth(t *testing.T) {
	w := apiGet(apiMux(Options{Version: "v1.2.3", Host: "mini"}), "/api/v1/health", "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "{\n  \"ok\": true,\n  \"version\": \"v1.2.3\",\n  \"host\": \"mini\"\n}" {
		t.Fatalf("health %d: %s", w.Code, w.Body)
	}
}

func TestAPIRemoteAccessAndLog(t *testing.T) {
	var logs []string
	h := apiMux(Options{Logf: func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }})
	const remote = "192.168.1.20:5555"
	for _, path := range []string{"/api/v1/health", "/api/v1/usage"} {
		if w := apiGet(h, path, remote); w.Code != 200 {
			t.Errorf("remote %s: status %d, want 200", path, w.Code)
		}
	}
	apiGet(h, "/api/v1/usage?days=0", "[::1]:5555")
	apiGet(h, "/stats", "")
	want := []string{
		"api: 192.168.1.20:5555 GET /api/v1/health 200",
		"api: 192.168.1.20:5555 GET /api/v1/usage 200",
		"api: [::1]:5555 GET /api/v1/usage 400",
	}
	if !reflect.DeepEqual(logs, want) {
		t.Fatalf("logs = %q, want %q", logs, want)
	}
}

func TestRemoteUIIsLoopbackOnly(t *testing.T) {
	const remote = "100.64.0.2:5555"
	h := apiMux(Options{})
	for _, path := range []string{"/", "/stats", "/search?q=x", "/hot.json", "/notify", "/session/claude/a", "/assets/main.css", "/api/summary.json"} {
		if w := apiGet(h, path, remote); w.Code != 403 {
			t.Errorf("remote %s: status %d, want 403", path, w.Code)
		}
	}
	for _, path := range []string{"/stats", "/api/summary.json"} {
		if w := apiGet(h, path, ""); w.Code != 200 {
			t.Errorf("local %s: status %d", path, w.Code)
		}
	}

	open := apiMux(Options{AllowRemoteUI: true})
	for _, path := range []string{"/stats", "/api/summary.json"} {
		if w := apiGet(open, path, remote); w.Code != 200 {
			t.Errorf("opted-in remote %s: status %d", path, w.Code)
		}
	}
}

func TestAPIReadOnly(t *testing.T) {
	h := apiMux(Options{})
	for _, method := range []string{"POST", "PUT", "DELETE", "OPTIONS"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, localRequest(method, "/api/v1/usage"))
		if w.Code != 405 || !slices.Contains(strings.Split(w.Header().Get("Allow"), ", "), "GET") {
			t.Errorf("%s: status %d allow %q", method, w.Code, w.Header().Get("Allow"))
		}
	}
	if w := apiGet(h, "/api/v1/nope", ""); w.Code != 404 {
		t.Errorf("unknown api path: %d", w.Code)
	}
}
