package cli

import (
	"runtime/debug"

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
	return resolveBuildIdentity(info, ok)
}

func resolveBuildIdentity(info *debug.BuildInfo, ok bool) (string, string) {
	version := "dev"
	commit := "none"
	if !ok || info == nil {
		return version, commit
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && setting.Value != "" {
			commit = setting.Value
			break
		}
	}
	return version, commit
}

func newContractCommand(dependencies Dependencies) *cobra.Command {
	return &cobra.Command{Use: "contract", Args: exactArgs(0), RunE: func(_ *cobra.Command, _ []string) error {
		data := map[string]any{"envelope_version": contract.EnvelopeVersion, "exits": errx.Codes(), "limits": contract.V1Limits(), "slack_methods": contract.Routes()}
		return dependencies.Output.Success(data, nil)
	}}
}
