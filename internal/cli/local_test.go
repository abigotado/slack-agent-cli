package cli

import (
	"runtime/debug"
	"testing"
)

func TestResolveBuildIdentity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		info        *debug.BuildInfo
		ok          bool
		wantVersion string
		wantCommit  string
	}{
		{name: "unavailable", wantVersion: "dev", wantCommit: "none"},
		{
			name:        "checkout",
			info:        &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "0123456789abcdef"}}},
			ok:          true,
			wantVersion: "dev",
			wantCommit:  "0123456789abcdef",
		},
		{
			name:        "module release",
			info:        &debug.BuildInfo{Main: debug.Module{Version: "v0.1.0"}},
			ok:          true,
			wantVersion: "v0.1.0",
			wantCommit:  "none",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			version, commit := resolveBuildIdentity(test.info, test.ok)
			if version != test.wantVersion || commit != test.wantCommit {
				t.Fatalf("got (%q, %q), want (%q, %q)", version, commit, test.wantVersion, test.wantCommit)
			}
		})
	}
}
