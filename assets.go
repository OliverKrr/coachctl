// Package coachctl embeds the files the binary ships: the agent skill and the
// workspace template. Embedding keeps them in step with the installed version.
package coachctl

import "embed"

// Skill is skill/, installed by `coachctl skill install`.
//
//go:embed all:skill
var Skill embed.FS

// Template is workspace-template/: SETUP.md (printed by `coachctl setup`),
// workspace/ (written by `coachctl init`) and host/ (always-on host files).
//
//go:embed all:workspace-template
var Template embed.FS
