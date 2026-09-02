package safefile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadRegularIsBoundedAndRejectsSymlink(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadRegular(target, 4); err == nil {
		t.Fatal("oversized file was accepted")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadRegular(link, 10); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("symlink was accepted or misclassified: %v", err)
	}
}
