package controller

import (
	"done-hub/common/config"
	"testing"
)

func TestParseBatchChannelKeysLineSplit(t *testing.T) {
	keys, mode, err := parseBatchChannelKeys("key-a\n\n key-b \nkey-a", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != "line_split" {
		t.Fatalf("expected mode line_split, got %s", mode)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 unique keys, got %d", len(keys))
	}
	if keys[0] != "key-a" || keys[1] != "key-b" {
		t.Fatalf("unexpected keys: %#v", keys)
	}
}

func TestParseBatchChannelKeysJSONArray(t *testing.T) {
	raw := `["k1", {"access_token":"a","refresh_token":"b","account_id":"c"}, "k2"]`
	keys, mode, err := parseBatchChannelKeys(raw, config.ChannelTypeCodex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != "json_array" {
		t.Fatalf("expected mode json_array, got %s", mode)
	}
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}
}

func TestParseBatchChannelKeysJSONBlocks(t *testing.T) {
	raw := `{
  "access_token": "a1",
  "refresh_token": "r1",
  "account_id": "c1"
}

{
  "access_token": "a2",
  "refresh_token": "r2",
  "account_id": "c2"
}`
	keys, mode, err := parseBatchChannelKeys(raw, config.ChannelTypeCodex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != "json_blocks" {
		t.Fatalf("expected mode json_blocks, got %s", mode)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
}
