package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"stream-agents/internal/store"
)

func TestParseCostMultipliers(t *testing.T) {
	got, err := store.ParseCostMultipliers("")
	if err != nil || got["codex"] != 1.75 || len(got) != 1 {
		t.Fatalf("defaults = %v, %v", got, err)
	}
	got, err = store.ParseCostMultipliers(" claude=1.2, codex=1 ")
	if err != nil || got["claude"] != 1.2 || got["codex"] != 1 {
		t.Fatalf("overrides = %v, %v", got, err)
	}
	if store.DefaultCostMultipliers["codex"] != 1.75 {
		t.Fatal("overrides modified the defaults")
	}
	for _, bad := range []string{"codex", "codex=x", "=2", "codex=-1", "codex=NaN", "codex=Inf", "codex=-inf", "codex=1e999"} {
		if _, err := store.ParseCostMultipliers(bad); err == nil {
			t.Errorf("ParseCostMultipliers(%q) succeeded", bad)
		}
	}
}

func TestFormatCostMultipliers(t *testing.T) {
	if got := store.FormatCostMultipliers(map[string]float64{"codex": 1.75, "claude": 1, "opencode": 2}); got != "codex ×1.75, opencode ×2" {
		t.Errorf("got %q", got)
	}
	if got := store.FormatCostMultipliers(map[string]float64{"codex": 1}); got != "none" {
		t.Errorf("got %q", got)
	}
}

func TestIndexListAllBilled(t *testing.T) {
	root := t.TempDir()
	dayDir := filepath.Join(root, "2026", "09", "01")
	os.MkdirAll(dayDir, 0o755)
	rollout := `{"timestamp":"2026-09-01T10:00:00Z","type":"turn_context","payload":{"model":"gpt-5.5"}}
{"timestamp":"2026-09-01T10:00:01Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000000,"cached_input_tokens":0,"output_tokens":0}}}}
`
	os.WriteFile(filepath.Join(dayDir, "rollout-2026-09-01T10-00-00-0000.jsonl"), []byte(rollout), 0o644)
	idx := store.NewIndex(store.NewCodexStore(root))
	// Listing twice checks billing never compounds into the cached sessions.
	for _, pass := range []string{"first", "second"} {
		sessions, err := idx.ListAll(context.Background(), "", "")
		if err != nil {
			t.Fatal(err)
		}
		if len(sessions) != 1 || sessions[0].Cost != 5 || sessions[0].Billed != 8.75 {
			t.Fatalf("%s pass: sessions = %+v, want cost 5 billed 8.75", pass, sessions)
		}
		if u := sessions[0].Usage; len(u) != 1 || u[0].Billed != 8.75 {
			t.Fatalf("%s pass: usage = %+v", pass, u)
		}
	}
	idx.CostMultipliers = map[string]float64{"codex": 1}
	if sessions, _ := idx.ListAll(context.Background(), "", ""); sessions[0].Billed != 5 {
		t.Errorf("override: billed %v, want 5", sessions[0].Billed)
	}
}
