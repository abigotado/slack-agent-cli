package cli

import (
	"github.com/spf13/cobra"

	"github.com/abigotado/slack-agent-cli/internal/skills"
)

func newSkillCommand(dependencies Dependencies) *cobra.Command {
	command := &cobra.Command{Use: "skill", Args: exactArgs(0), RunE: subcommandRequired}
	command.AddCommand(newSkillLifecycleCommand(dependencies, true), newSkillLifecycleCommand(dependencies, false))
	return command
}

func newSkillLifecycleCommand(dependencies Dependencies, install bool) *cobra.Command {
	var providerValue, scopeValue, projectDir, skillValue string
	var dryRun, yes bool
	name := "uninstall"
	if install {
		name = "install"
	}
	command := &cobra.Command{Use: name, Args: exactArgs(0), RunE: func(command *cobra.Command, _ []string) error {
		if providerValue == "" || scopeValue == "" {
			return usageError("SKILL_TARGET_REQUIRED", "--provider and --scope are required", nil)
		}
		if dryRun == yes {
			return usageError("SKILL_CONFIRMATION_REQUIRED", "choose exactly one of --dry-run or --yes", nil)
		}
		provider := skills.Provider(providerValue)
		scope := skills.Scope(scopeValue)
		skill := skills.SkillSlack
		if skillValue != "" {
			var err error
			skill, err = skills.ParseSkill(skillValue)
			if err != nil {
				return usageError("INVALID_SKILL", "Skill selector is invalid", err)
			}
		}
		destination, err := skills.Destination(skill, provider, scope, projectDir)
		if err != nil {
			return usageError("INVALID_SKILL_TARGET", "Skill destination is invalid", err)
		}
		var result skills.Result
		if install {
			result, err = skills.Install(command.Context(), skill, destination, provider, scope, yes)
		} else {
			result, err = skills.Uninstall(command.Context(), skill, destination, provider, scope, yes)
		}
		if err != nil {
			return err
		}
		return dependencies.Output.Success(result, nil)
	}}
	command.Flags().StringVar(&providerValue, "provider", "", "codex or claude")
	command.Flags().StringVar(&scopeValue, "scope", "", "user or project")
	command.Flags().StringVar(&skillValue, "skill", "", "explicit Skill: slack or slack-app-provisioning (default: slack)")
	command.Flags().StringVar(&projectDir, "project-dir", "", "explicit project root for project scope")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "inspect the exact local change without writing")
	command.Flags().BoolVar(&yes, "yes", false, "apply the exact local change")
	return command
}
