package main

import (
	"os"
	"strings"
)

// slackWorkspaceIDEnv is the environment variable consulted by every
// `gc slack` verb that takes `--workspace-id`. When set to a non-empty,
// non-whitespace value, it becomes the flag's default — making the
// flag optional. The CLI flag still wins when both are provided.
//
// This mirrors the discord-pack ergonomics: operators set
// SLACK_WORKSPACE_ID once in their shell profile and stop retyping the
// same workspace id across map-channel, map-rig, import-app,
// sync-commands, and status.
const slackWorkspaceIDEnv = "SLACK_WORKSPACE_ID"

// slackWorkspaceIDDefault reads slackWorkspaceIDEnv and returns the
// trimmed value. Whitespace-only values are treated as unset so a
// stray `export SLACK_WORKSPACE_ID=" "` cannot silently propagate a
// blank workspace id into a registry write.
//
// The function is read once per `gc` invocation at flag-registration
// time, never inside command handlers — that keeps the env-var
// contract single-sourced and the resolved string the only thing the
// rest of the verb sees.
func slackWorkspaceIDDefault() string {
	return strings.TrimSpace(os.Getenv(slackWorkspaceIDEnv))
}

// slackWorkspaceIDFlagUsage is the canonical usage string for every
// `--workspace-id` flag on `gc slack` verbs. Centralising it here keeps
// help text identical across verbs and makes the env-var fallback
// discoverable from --help.
//
// The "(required)" suffix is intentional: when SLACK_WORKSPACE_ID is
// unset, the flag IS required (cobra MarkFlagRequired re-engages); when
// it is set, the flag is implicitly satisfied by the env default. The
// help text describes the contract, not the dynamic state.
const slackWorkspaceIDFlagUsage = "Slack workspace (team) id, e.g. T0123456 (required; defaults to $" + slackWorkspaceIDEnv + " when set)"
