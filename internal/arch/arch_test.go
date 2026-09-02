package arch_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const module = "github.com/abigotado/slack-agent-cli"

func TestSlackDoesNotImportAuthOrProfile(t *testing.T) {
	if !packageExists(t, "internal/slack") {
		t.Skip("Slack client not implemented yet")
	}
	forbidden := map[string]bool{module + "/internal/auth": true, module + "/internal/profile": true}
	for _, dependency := range dependencies(t, module+"/internal/slack") {
		if forbidden[dependency] {
			t.Errorf("internal/slack depends on %s", dependency)
		}
	}
}

func TestCLIDoesNotImportNetHTTP(t *testing.T) {
	if !packageExists(t, "internal/cli") {
		t.Skip("CLI not implemented yet")
	}
	for _, imported := range directImports(t, module+"/internal/cli") {
		if imported == "net/http" {
			t.Error("internal/cli imports net/http")
		}
	}
}

func TestErrxIsLeaf(t *testing.T) {
	for _, dependency := range dependencies(t, module+"/internal/errx") {
		if dependency != module+"/internal/errx" && strings.HasPrefix(dependency, module+"/") {
			t.Errorf("internal/errx depends on %s", dependency)
		}
	}
}

func dependencies(t *testing.T, pkg string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", pkg).CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	return strings.Fields(string(out))
}

func directImports(t *testing.T, pkg string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`, pkg).CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	return strings.Fields(string(out))
}

func packageExists(t *testing.T, relative string) bool {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("module root not found")
		}
		dir = parent
	}
	info, err := os.Stat(filepath.Join(dir, relative))
	return err == nil && info.IsDir()
}
