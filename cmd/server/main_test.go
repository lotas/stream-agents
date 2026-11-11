package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDefaultListenIsLoopback(t *testing.T) {
	cfg, err := loadServeConfig(defaultListen)
	if err != nil {
		t.Fatal(err)
	}
	if defaultListen != "127.0.0.1:7777" || cfg.remote || !reflect.DeepEqual(cfg.addrs, []string{"127.0.0.1:7777"}) {
		t.Fatalf("default config = %+v", cfg)
	}
}

func TestLoadServeConfigAddrs(t *testing.T) {
	for _, tc := range []struct {
		listen string
		remote bool
		browse string
	}{
		{"localhost:1", false, "localhost:1"},
		{"[::1]:1", false, "[::1]:1"},
		{"0.0.0.0:1", true, "0.0.0.0:1"},
		{":1", true, ":1"},
		{"100.64.0.1:1", true, "100.64.0.1:1"},
		{"100.64.0.1:1, 127.0.0.1:2", true, "127.0.0.1:2"},
	} {
		cfg, err := loadServeConfig(tc.listen)
		if err != nil {
			t.Errorf("%q: %v", tc.listen, err)
			continue
		}
		if cfg.remote != tc.remote || cfg.browseAddr() != tc.browse {
			t.Errorf("%q: remote %v browse %q, want %v %q", tc.listen, cfg.remote, cfg.browseAddr(), tc.remote, tc.browse)
		}
	}
	for _, bad := range []string{"", " , ", "127.0.0.1", "127.0.0.1:1,nope"} {
		if _, err := loadServeConfig(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

func TestApplyHookWritesState(t *testing.T) {
	dir := t.TempDir()
	payload := `{"session_id":"abc","transcript_path":"/x.jsonl","cwd":"/p","permission_mode":"default","hook_event_name":"Notification","message":"Claude needs your permission to use Bash","title":"Permission needed","notification_type":"permission_prompt"}`
	if err := applyHook(strings.NewReader(payload), dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "claude", "abc.json")); err != nil {
		t.Fatal(err)
	}
	if err := applyHook(strings.NewReader(`{"session_id":"abc","hook_event_name":"Stop","stop_hook_active":false}`), dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "claude", "abc.json")); !os.IsNotExist(err) {
		t.Fatalf("state after Stop: %v", err)
	}
	if err := applyHook(strings.NewReader("not json"), dir, time.Now()); err == nil {
		t.Error("bad payload: want error")
	}
}
