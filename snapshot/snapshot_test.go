package snapshot

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateSnapshotID(t *testing.T) {
	id := generateSnapshotID()

	// Should be in the format 20060102T150405Z
	if len(id) != 16 {
		t.Errorf("Expected ID length 16, got %d", len(id))
	}

	// Should be parseable as a time
	_, err := time.Parse("20060102T150405Z", id)
	if err != nil {
		t.Errorf("Failed to parse snapshot ID as time: %v", err)
	}
}

func TestSnapshotKey(t *testing.T) {
	id := "20240101T120000Z"
	key := snapshotKey(id)

	expected := "snap/20240101T120000Z"
	if key != expected {
		t.Errorf("Expected key %s, got %s", expected, key)
	}
}

func TestReadFileWithChecksum(t *testing.T) {
	// Create a temp file
	tmpDir, err := os.MkdirTemp("", "snapshot_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "test.txt")
	testData := []byte("hello world")
	if err := os.WriteFile(testFile, testData, 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	data, checksum, err := readFileWithChecksum(testFile)
	if err != nil {
		t.Fatalf("readFileWithChecksum failed: %v", err)
	}

	if string(data) != string(testData) {
		t.Errorf("Data mismatch: expected %s, got %s", testData, data)
	}

	// Known SHA256 hash for "hello world"
	expectedChecksum := "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
	if checksum != expectedChecksum {
		t.Errorf("Checksum mismatch: expected %s, got %s", expectedChecksum, checksum)
	}
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config.Interval != time.Hour {
		t.Errorf("Expected Interval 1h, got %v", config.Interval)
	}

	if config.Retention != 24*time.Hour {
		t.Errorf("Expected Retention 24h, got %v", config.Retention)
	}

	if config.MaxSnapshots != 24 {
		t.Errorf("Expected MaxSnapshots 24, got %d", config.MaxSnapshots)
	}
}

func TestSnapshot_Fields(t *testing.T) {
	now := time.Now()
	snap := Snapshot{
		ID:        "test-id",
		CreatedAt: now,
		Size:      1024,
		Checksum:  "abc123",
		NodeID:    "node-1",
		Metadata: map[string]string{
			"key": "value",
		},
	}

	if snap.ID != "test-id" {
		t.Errorf("Expected ID 'test-id', got %s", snap.ID)
	}

	if snap.Size != 1024 {
		t.Errorf("Expected Size 1024, got %d", snap.Size)
	}

	if snap.Checksum != "abc123" {
		t.Errorf("Expected Checksum 'abc123', got %s", snap.Checksum)
	}

	if snap.NodeID != "node-1" {
		t.Errorf("Expected NodeID 'node-1', got %s", snap.NodeID)
	}

	if snap.Metadata["key"] != "value" {
		t.Errorf("Expected Metadata['key'] = 'value', got %s", snap.Metadata["key"])
	}
}
