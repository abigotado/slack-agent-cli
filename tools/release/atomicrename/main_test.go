package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRunPublishesWithoutReplacement(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "asset"), []byte("verified"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{source, target}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source still exists: %v", err)
	}
	payload, err := os.ReadFile(filepath.Join(target, "asset"))
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "verified" {
		t.Fatalf("target payload = %q", payload)
	}
}

func TestRunRefusesExistingTarget(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{source, target}); err == nil {
		t.Fatal("existing target was replaced")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source disappeared: %v", err)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("target changed: %v", entries)
	}
}
