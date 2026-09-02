package cli

import (
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"

	"github.com/abigotado/slack-agent-cli/internal/contract"
	"github.com/abigotado/slack-agent-cli/internal/errx"
)

func newVersionCommand(dependencies Dependencies) *cobra.Command {
	return &cobra.Command{Use: "version", Args: exactArgs(0), RunE: func(_ *cobra.Command, _ []string) error {
		version, commit := buildIdentity()
		return dependencies.Output.Success(map[string]any{"version": version, "commit": commit}, nil)
	}}
}

func buildIdentity() (string, string) {
	info, ok := debug.ReadBuildInfo()
	return resolveBuildIdentity(info, ok, archiveVersion, archiveCommit)
}

func resolveBuildIdentity(info *debug.BuildInfo, ok bool, fallbackVersion, fallbackCommit string) (string, string) {
	version := "dev"
	commit := "none"
	if ok && info != nil {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				commit = setting.Value
				break
			}
		}
	}
	if version == "dev" && validArchiveVersion(fallbackVersion) {
		version = fallbackVersion
	}
	if commit == "none" && validArchiveCommit(fallbackCommit) {
		commit = fallbackCommit
	}
	return version, commit
}

func validArchiveVersion(value string) bool {
	if len(value) < 6 || value[0] != 'v' {
		return false
	}
	parts := strings.Split(value[1:], ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return false
			}
		}
	}
	return true
}

func validArchiveCommit(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func newContractCommand(dependencies Dependencies) *cobra.Command {
	return &cobra.Command{Use: "contract", Args: exactArgs(0), RunE: func(_ *cobra.Command, _ []string) error {
		data := map[string]any{"envelope_version": contract.EnvelopeVersion, "exits": errx.Codes(), "limits": contract.V1Limits(), "slack_methods": contract.Routes()}
		return dependencies.Output.Success(data, nil)
	}}
}
