package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/abigotado/slack-agent-cli/internal/contract"
)

func TestDestinationUsesClosedSkillNamesForBothProviders(t *testing.T) {
	t.Parallel()
	root := scratchDir(t)
	for _, test := range []struct {
		skill    Skill
		provider Provider
		want     string
	}{
		{SkillSlack, ProviderCodex, filepath.Join(".agents", "skills", "slack")},
		{SkillAppProvisioning, ProviderCodex, filepath.Join(".agents", "skills", "slack-app-provisioning")},
		{SkillSlack, ProviderClaude, filepath.Join(".claude", "skills", "slack")},
		{SkillAppProvisioning, ProviderClaude, filepath.Join(".claude", "skills", "slack-app-provisioning")},
	} {
		destination, err := Destination(test.skill, test.provider, ScopeProject, root)
		if err != nil {
			t.Fatal(err)
		}
		if destination != filepath.Join(root, test.want) {
			t.Fatalf("skill=%s provider=%s destination=%q", test.skill, test.provider, destination)
		}
	}
	if _, err := Destination(Skill("../../escape"), ProviderCodex, ScopeProject, root); err == nil {
		t.Fatal("unknown Skill was accepted")
	}
}

func TestProvisioningSkillPinsCanonicalManifestsAndHooks(t *testing.T) {
	t.Parallel()
	files, err := canonicalFiles(SkillAppProvisioning)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"SKILL.md",
		"assets/manifests/all-channels-message-write.json",
		"assets/manifests/user-direct-message-read-only.json",
		"assets/manifests/user-workspace-message-write.json",
		"assets/scaffold/package-lock.json",
		"references/commands.md",
		"references/environment-cases.md",
	} {
		if _, ok := files[path]; !ok {
			t.Fatalf("canonical file %q is absent", path)
		}
	}
	var manifest struct {
		OAuthConfig struct {
			Scopes struct {
				Bot []string `json:"bot"`
			} `json:"scopes"`
		} `json:"oauth_config"`
	}
	if err := json.Unmarshal(files["assets/manifests/all-channels-message-write.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	want := []string{"channels:history", "channels:read", "chat:write", "files:read", "groups:history", "groups:read", "users:read"}
	if !slices.Equal(manifest.OAuthConfig.Scopes.Bot, want) {
		t.Fatalf("scopes=%v want=%v", manifest.OAuthConfig.Scopes.Bot, want)
	}
	var userManifest struct {
		Features    *json.RawMessage `json:"features"`
		OAuthConfig struct {
			RedirectURLs []string `json:"redirect_urls"`
			Scopes       struct {
				Bot  []string `json:"bot"`
				User []string `json:"user"`
			} `json:"scopes"`
		} `json:"oauth_config"`
	}
	if err := json.Unmarshal(files["assets/manifests/user-direct-message-read-only.json"], &userManifest); err != nil {
		t.Fatal(err)
	}
	userScopes := []string{"files:read", "im:history", "im:read", "users:read"}
	if !slices.Equal(userManifest.OAuthConfig.Scopes.User, userScopes) {
		t.Fatalf("user scopes=%v want=%v", userManifest.OAuthConfig.Scopes.User, userScopes)
	}
	if userManifest.Features != nil || len(userManifest.OAuthConfig.Scopes.Bot) != 0 || len(userManifest.OAuthConfig.RedirectURLs) != 0 {
		t.Fatalf("user-only manifest gained bot features, bot scopes, or redirect URLs: %+v", userManifest)
	}
	var userWorkspaceManifest struct {
		Features    *json.RawMessage `json:"features"`
		OAuthConfig struct {
			RedirectURLs []string `json:"redirect_urls"`
			Scopes       struct {
				Bot  []string `json:"bot"`
				User []string `json:"user"`
			} `json:"scopes"`
		} `json:"oauth_config"`
	}
	if err := json.Unmarshal(files["assets/manifests/user-workspace-message-write.json"], &userWorkspaceManifest); err != nil {
		t.Fatal(err)
	}
	wantUserWorkspaceScopes := []string{"channels:history", "channels:read", "chat:write", "files:read", "groups:history", "groups:read", "im:history", "im:read", "mpim:history", "mpim:read", "users:read"}
	if !slices.Equal(userWorkspaceManifest.OAuthConfig.Scopes.User, wantUserWorkspaceScopes) {
		t.Fatalf("user workspace scopes=%v want=%v", userWorkspaceManifest.OAuthConfig.Scopes.User, wantUserWorkspaceScopes)
	}
	if userWorkspaceManifest.Features != nil || len(userWorkspaceManifest.OAuthConfig.Scopes.Bot) != 0 || len(userWorkspaceManifest.OAuthConfig.RedirectURLs) != 0 {
		t.Fatalf("user workspace manifest gained bot features, bot scopes, or redirect URLs: %+v", userWorkspaceManifest)
	}
	lockfile := string(files["assets/scaffold/package-lock.json"])
	if !strings.Contains(lockfile, `"version": "2.0.0"`) || !strings.Contains(lockfile, "sha512-VLxGqJZwbrH3S+ovRhqlrcrKWHRDJtn3toraZKAcLaPqca5CgqTa/PmiCvCq3uUowiFw9B7FOB0y3ikQoDppTw==") {
		t.Fatal("canonical Slack CLI hooks lock is missing")
	}
	commands := string(files["references/commands.md"])
	for _, name := range []string{
		"ACCESSIBLE",
		"SLACK_API_URL",
		"SLACK_AUTO_REQUEST_AAA",
		"SLACK_CLI_APP_ICON_PATH",
		"SLACK_CLI_XAPP",
		"SLACK_CLI_XOXB",
		"SLACK_CONFIG_DIR",
		"SLACK_DISABLE_TELEMETRY",
		"SLACK_TEST_TRACE",
		"SLACK_TEST_VERSION",
	} {
		if !strings.Contains(commands, name) {
			t.Fatalf("official Slack CLI environment gate is missing %s", name)
		}
	}
	if !strings.Contains(commands, "getpwuid(getuid())") || !strings.Contains(commands, "Require caller `HOME` to equal `CANONICAL_HOME` exactly") {
		t.Fatal("canonical home derivation is missing")
	}
	if !strings.Contains(commands, "/usr/bin/env -i HOME=CANONICAL_HOME") || !strings.Contains(commands, "SLACK_DISABLE_TELEMETRY=1 ABSOLUTE_SLACK_BIN") {
		t.Fatal("sanitized Slack CLI environment is missing")
	}
	if !strings.Contains(commands, "direct argv vector") || !strings.Contains(commands, "path containing whitespace or metacharacters") {
		t.Fatal("safe argv and human quoting requirements are missing")
	}
	environmentCases := string(files["references/environment-cases.md"])
	for _, adversarialCase := range []string{"another existing, current-user-owned directory", "spaces or shell metacharacters", "contains a single quote", "accepts only a shell command string"} {
		if !strings.Contains(environmentCases, adversarialCase) {
			t.Fatalf("environment adversarial case %q is missing", adversarialCase)
		}
	}
	for _, line := range strings.Split(commands, "\n") {
		if strings.Contains(line, "--skip-update --no-color") && !strings.HasPrefix(line, "SANITIZED_PREFIX ") {
			t.Fatalf("Slack CLI command lacks sanitized prefix: %q", line)
		}
	}
	for _, required := range []string{"--token-kind user", "--capability message-write", "does not create one or grant OAuth scopes"} {
		if !strings.Contains(commands, required) {
			t.Fatalf("user-token handoff is missing %q", required)
		}
	}
	stateMachine := string(files["references/state-machine.md"])
	for _, required := range []string{"No Bot User OAuth Token", "admin-approval", "do not release"} {
		if !strings.Contains(stateMachine, required) {
			t.Fatalf("user-only acceptance gate is missing %q", required)
		}
	}
	manifestReference := string(files["references/manifests.md"])
	for _, required := range []string{"verify the other DM", "any exact valid user ID", "no user listing or search"} {
		if !strings.Contains(manifestReference, required) {
			t.Fatalf("user-scope disclosure is missing %q", required)
		}
	}
}

func scratchDir(t *testing.T) string {
	t.Helper()
	root := filepath.Join("..", "..", "work")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "skill-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	absolute, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return absolute
}

func TestDryRunDoesNotCreateDestination(t *testing.T) {
	t.Parallel()
	root := scratchDir(t)
	destination := filepath.Join(root, "missing", "skills", "slack")
	result, err := Install(context.Background(), SkillSlack, destination, ProviderCodex, ScopeProject, false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Applied {
		t.Fatalf("bad result: %+v", result)
	}
	if _, err := os.Stat(filepath.Dir(destination)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run mutated parent: %v", err)
	}
}

func TestCodexAndClaudeInstallIdenticalCanonicalBytes(t *testing.T) {
	t.Parallel()
	root := scratchDir(t)
	codex := filepath.Join(root, "codex", "slack")
	claude := filepath.Join(root, "claude", "slack")
	for _, item := range []struct {
		destination string
		provider    Provider
	}{{codex, ProviderCodex}, {claude, ProviderClaude}} {
		result, err := Install(context.Background(), SkillSlack, item.destination, item.provider, ScopeProject, true)
		if err != nil {
			t.Fatal(err)
		}
		if !result.Applied || !result.Changed {
			t.Fatalf("bad result: %+v", result)
		}
	}
	left, err := fileDigests(codex)
	if err != nil {
		t.Fatal(err)
	}
	right, err := fileDigests(claude)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != len(right) {
		t.Fatalf("file counts differ")
	}
	for path, digest := range left {
		if right[path] != digest {
			t.Fatalf("%s differs", path)
		}
	}
}

func TestModifiedOwnedFileBlocksUpgradeAndUninstall(t *testing.T) {
	t.Parallel()
	root := scratchDir(t)
	destination := filepath.Join(root, "skills", "slack")
	if _, err := Install(context.Background(), SkillSlack, destination, ProviderCodex, ScopeProject, true); err != nil {
		t.Fatal(err)
	}
	skillFile := filepath.Join(destination, "SKILL.md")
	if err := os.WriteFile(skillFile, []byte("modified"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), SkillSlack, destination, ProviderCodex, ScopeProject, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("upgrade should conflict: %v", err)
	}
	if _, err := Uninstall(context.Background(), SkillSlack, destination, ProviderCodex, ScopeProject, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("uninstall should conflict: %v", err)
	}
	payload, err := os.ReadFile(skillFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "modified" {
		t.Fatal("modified file was overwritten")
	}
}

func TestSymlinkDestinationRejected(t *testing.T) {
	t.Parallel()
	root := scratchDir(t)
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "slack")
	if err := os.Symlink(target, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), SkillSlack, destination, ProviderCodex, ScopeProject, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("symlink accepted: %v", err)
	}
}

func TestSymlinkParentRejectedWithoutCreatingOutsideAnchor(t *testing.T) {
	t.Parallel()
	root := scratchDir(t)
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	linkedParent := filepath.Join(root, "linked")
	if err := os.Symlink(outside, linkedParent); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(linkedParent, "skills", "slack")
	if _, err := Install(context.Background(), SkillSlack, destination, ProviderCodex, ScopeProject, true); err == nil {
		t.Fatal("symlink parent accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "skills")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("installer created a directory through symlink: %v", err)
	}
}

func TestOversizedManifestAndOwnedFileAreRejected(t *testing.T) {
	t.Parallel()
	root := scratchDir(t)
	destination := filepath.Join(root, "skills", "slack")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(destination, ".slack-agent-cli-manifest.json")
	if err := os.WriteFile(manifestPath, make([]byte, contract.MaxSkillManifestBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), SkillSlack, destination, ProviderCodex, ScopeProject, false); err == nil {
		t.Fatal("oversized ownership manifest was accepted")
	}
	if err := os.RemoveAll(destination); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), SkillSlack, destination, ProviderCodex, ScopeProject, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "SKILL.md"), make([]byte, contract.MaxSkillFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Uninstall(context.Background(), SkillSlack, destination, ProviderCodex, ScopeProject, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("oversized owned file was accepted: %v", err)
	}
}

func TestInterruptedInstallAndUninstallStatesRecoverSafely(t *testing.T) {
	t.Parallel()
	root := scratchDir(t)
	destination := filepath.Join(root, "skills", "slack")
	if _, err := Install(context.Background(), SkillSlack, destination, ProviderCodex, ScopeProject, true); err != nil {
		t.Fatal(err)
	}
	backupSource := filepath.Join(root, "backup-source")
	if _, err := Install(context.Background(), SkillSlack, backupSource, ProviderCodex, ScopeProject, true); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backupSource, destination+".previous"); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), SkillSlack, destination, ProviderCodex, ScopeProject, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("dry-run ignored interrupted install: %v", err)
	}
	if result, err := Install(context.Background(), SkillSlack, destination, ProviderCodex, ScopeProject, true); err != nil || !result.Applied {
		t.Fatalf("install recovery result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(destination + ".previous"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup was not cleaned: %v", err)
	}
	if err := os.Rename(destination, destination+".removing"); err != nil {
		t.Fatal(err)
	}
	if _, err := Uninstall(context.Background(), SkillSlack, destination, ProviderCodex, ScopeProject, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("dry-run ignored interrupted uninstall: %v", err)
	}
	if result, err := Uninstall(context.Background(), SkillSlack, destination, ProviderCodex, ScopeProject, true); err != nil || !result.Applied || !result.Changed {
		t.Fatalf("uninstall recovery result=%+v err=%v", result, err)
	}
}

func fileDigests(root string) (map[string]string, error) {
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(payload)
		result[filepath.ToSlash(relative)] = hex.EncodeToString(sum[:])
		return nil
	})
	return result, err
}

func TestAllManifestsRequireFileRead(t *testing.T) {
	files, err := canonicalFiles(SkillAppProvisioning)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"read-only", "read-message-write", "all-channels-message-write", "user-direct-message-read-only", "user-workspace-message-write"} {
		t.Run(name, func(t *testing.T) {
			var manifest struct {
				OAuthConfig struct {
					Scopes map[string][]string `json:"scopes"`
				} `json:"oauth_config"`
			}
			if err := json.Unmarshal(files["assets/manifests/"+name+".json"], &manifest); err != nil {
				t.Fatal(err)
			}
			kind := "bot"
			if strings.HasPrefix(name, "user-") {
				kind = "user"
			}
			if !slices.Contains(manifest.OAuthConfig.Scopes[kind], "files:read") {
				t.Fatalf("%s lacks files:read", name)
			}
		})
	}
}
