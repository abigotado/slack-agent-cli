package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/abigotado/slack-agent-cli/internal/errx"
	"github.com/abigotado/slack-agent-cli/internal/policy"
	"github.com/abigotado/slack-agent-cli/internal/profile"
	"github.com/abigotado/slack-agent-cli/internal/slack"
)

type fakeFiles struct {
	calls   []string
	fail    bool
	wrongID bool
}

func (f *fakeFiles) FileInfo(context.Context, slack.Token, string) (slack.FileDetails, error) {
	f.calls = append(f.calls, "info")
	id := "F1"
	if f.wrongID {
		id = "F2"
	}
	return slack.FileDetails{File: slack.File{ID: id, Name: "../../untrusted.mp4", Size: 3, Mode: "hosted"}}, nil
}
func (f *fakeFiles) DownloadFile(_ context.Context, _ slack.Token, _ slack.FileDetails, workspace string, w io.Writer) (slack.DownloadResult, error) {
	f.calls = append(f.calls, "download")
	if workspace != "T1" {
		return slack.DownloadResult{}, errors.New("wrong workspace")
	}
	if _, err := io.WriteString(w, "abc"); err != nil {
		return slack.DownloadResult{}, err
	}
	if f.fail {
		return slack.DownloadResult{}, errx.New(errx.Retryable, "SLACK_READ_FAILED", "failed", "retry")
	}
	return slack.DownloadResult{Bytes: 3, SHA256: "digest"}, nil
}

func TestFilesEnforceAttachmentAndPolicyBeforeInfo(t *testing.T) {
	for _, scenario := range []string{"success", "thread", "missing profile", "blocked", "stale", "shared drift", "wrong message", "wrong file", "wrong info", "wrong thread"} {
		t.Run(scenario, func(t *testing.T) {
			d, store, api, out := newTestDependencies(t)
			p := seedSession(t, d, store, []profile.Capability{profile.CapabilityRead})
			no := false
			api.conversation = slack.Conversation{ID: "C1", IsChannel: true, IsShared: &no, IsExtShared: &no, IsOrgShared: &no}
			if scenario != "blocked" {
				if _, err := d.Policies.Replace(context.Background(), p, policy.Read, []policy.Target{{ConversationID: "C1"}}); err != nil {
					t.Fatal(err)
				}
			}
			api.messages = slack.MessagePage{Messages: []slack.Message{{TS: "2.0", Files: []slack.File{{ID: "F1"}}}}}
			f := &fakeFiles{}
			d.Files = f
			args := []string{"files", "get", "F1", "--profile", "work", "--conversation-id", "C1", "--message-ts", "2.0"}
			want := errx.OK
			switch scenario {
			case "thread":
				args = append(args, "--thread-ts", "1.0")
				api.messages.Messages[0].ThreadTS = "1.0"
			case "missing profile":
				args = []string{"files", "get", "F1", "--conversation-id", "C1", "--message-ts", "2.0"}
				want = errx.Usage
			case "blocked":
				want = errx.PermissionDenied
			case "stale":
				p.CredentialGeneration = "g2"
				if err := d.Profiles.Put(context.Background(), p); err != nil {
					t.Fatal(err)
				}
				want = errx.Conflict
			case "shared drift":
				yes := true
				api.conversation.IsShared = &yes
				want = errx.Conflict
			case "wrong message":
				api.messages.Messages[0].TS = "3.0"
				want = errx.NotFound
			case "wrong file":
				api.messages.Messages[0].Files[0].ID = "F2"
				want = errx.NotFound
			case "wrong info":
				f.wrongID = true
				want = errx.Internal
			case "wrong thread":
				args = append(args, "--thread-ts", "1.0")
				api.messages.Messages[0].ThreadTS = "3.0"
				want = errx.NotFound
			}
			if got := Run(context.Background(), args, d); got != want {
				t.Fatalf("exit %d want %d: %s", got, want, out)
			}
			if want != errx.OK && scenario != "wrong info" && len(f.calls) != 0 {
				t.Fatalf("file API called before proof: %v", f.calls)
			}
			if want == errx.OK {
				if !slices.Equal(f.calls, []string{"info"}) || !strings.Contains(out.String(), `"content_trust":"untrusted"`) {
					t.Fatalf("bad success: %v %s", f.calls, out)
				}
			}
		})
	}
}

func TestDownloadPublishesOnlyCompleteNewFile(t *testing.T) {
	for _, scenario := range []string{"success", "transfer failure", "existing", "symlink"} {
		t.Run(scenario, func(t *testing.T) {
			d, store, _, _ := newTestDependencies(t)
			p := seedSession(t, d, store, []profile.Capability{profile.CapabilityRead})
			f := &fakeFiles{fail: scenario == "transfer failure"}
			d.Files = f
			dir := t.TempDir()
			dest := filepath.Join(dir, "video.mp4")
			if scenario == "existing" {
				if err := os.WriteFile(dest, []byte("original"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "symlink" {
				if err := os.Symlink(filepath.Join(dir, "missing"), dest); err != nil {
					t.Fatal(err)
				}
			}
			result, path, err := saveDownload(context.Background(), d, session{profile: p, token: "sentinel"}, slack.FileDetails{File: slack.File{ID: "F1"}}, dest)
			entries, readErr := os.ReadDir(dir)
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".slack-download-") {
					t.Fatal("partial download left behind")
				}
			}
			if scenario == "success" {
				if err != nil || path != dest || result.Bytes != 3 {
					t.Fatalf("bad success: %v", err)
				}
				data, err := os.ReadFile(dest)
				if err != nil || string(data) != "abc" {
					t.Fatal("bad file")
				}
				info, err := os.Stat(dest)
				if err != nil || info.Mode().Perm() != 0o600 {
					t.Fatal("wrong permissions")
				}
			} else {
				if err == nil {
					t.Fatal("expected failure")
				}
				if scenario == "transfer failure" {
					if _, err := os.Lstat(dest); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("published partial file")
					}
				}
				if scenario == "existing" {
					data, err := os.ReadFile(dest)
					if err != nil || string(data) != "original" {
						t.Fatal("overwrote existing file")
					}
				}
				if scenario == "existing" || scenario == "symlink" {
					if len(f.calls) != 0 {
						t.Fatal("downloaded despite existing path")
					}
				}
			}
		})
	}
}

func TestDownloadCommandReturnsArtifactEnvelope(t *testing.T) {
	d, store, api, out := newTestDependencies(t)
	p := seedSession(t, d, store, []profile.Capability{profile.CapabilityRead})
	no := false
	api.conversation = slack.Conversation{ID: "C1", IsChannel: true, IsShared: &no, IsExtShared: &no, IsOrgShared: &no}
	if _, err := d.Policies.Replace(context.Background(), p, policy.Read, []policy.Target{{ConversationID: "C1"}}); err != nil {
		t.Fatal(err)
	}
	api.messages = slack.MessagePage{Messages: []slack.Message{{TS: "2.0", Files: []slack.File{{ID: "F1"}}}}}
	f := &fakeFiles{}
	d.Files = f
	dest := filepath.Join(t.TempDir(), "video.mp4")
	args := []string{"files", "download", "F1", "--profile", "work", "--conversation-id", "C1", "--message-ts", "2.0", "--output", dest}
	if exit := Run(context.Background(), args, d); exit != errx.OK {
		t.Fatalf("exit %d: %s", exit, out)
	}
	envelope := decodeEnvelope(t, out)
	data := envelope["data"].(map[string]any)
	if data["path"] != dest || data["bytes"] != float64(3) || data["sha256"] != "digest" || envelope["meta"].(map[string]any)["content_trust"] != "untrusted" {
		t.Fatalf("bad envelope: %v", envelope)
	}
	if !slices.Equal(f.calls, []string{"info", "download"}) || !slices.Equal(api.calls, []string{"conversations.info", "conversations.history"}) {
		t.Fatalf("bad calls %v %v", api.calls, f.calls)
	}
	content, err := os.ReadFile(dest)
	if err != nil || string(content) != "abc" {
		t.Fatal("missing artifact")
	}
}
