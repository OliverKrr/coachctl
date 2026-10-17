package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/OliverKrr/coachctl"
	"github.com/spf13/cobra"
)

// Template file names that cannot carry their real name inside this repo
// (a .gitignore there would apply to the template itself).
var renamedOnInit = map[string]string{"gitignore": ".gitignore"}

// SkillVersionFile marks the installed skill with the binary's version, so
// doctor can tell a stale skill from a current one.
const SkillVersionFile = ".coachctl-version"

// StarterMain is the main.md `coachctl init` writes.
func StarterMain() string {
	data, err := fs.ReadFile(coachctl.Template, "workspace-template/workspace/coaching/main.md")
	if err != nil {
		return ""
	}
	return string(data)
}

func (a *App) setupCmd() *cobra.Command {
	var hostFile string
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Print the setup guide (written for an agent to follow)",
		Long: `Print the guide that takes a new user from nothing to a working coaching
workspace: install, keys, init, skill, verification. Ask Claude Code to
"run coachctl setup and follow it".

--host-file prints one of the files for an always-on host instead:
claude-remote-control.service, autosave.sh or crontab.example.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "workspace-template/SETUP.md"
			if hostFile != "" {
				if strings.ContainsAny(hostFile, `/\`) {
					return usagef("--host-file takes a file name, e.g. autosave.sh")
				}
				path = "workspace-template/host/" + hostFile
			}
			data, err := fs.ReadFile(coachctl.Template, path)
			if errors.Is(err, fs.ErrNotExist) {
				return usagef("no host file %q (claude-remote-control.service, autosave.sh, crontab.example)", hostFile)
			}
			if err != nil {
				return err
			}
			_, err = a.Stdout.Write(data)
			return err
		},
	}
	cmd.Flags().StringVar(&hostFile, "host-file", "", "print this host file instead of the guide")
	return cmd
}

func (a *App) initCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init <dir>",
		Short: "Create a coaching workspace: record skeleton, CLAUDE.md, permissions, routine runner",
		Long: `Write the workspace template into <dir> (created if needed). Existing files
are kept unless --force. Then set COACH_HOME=<dir>/coaching in the env file.`,
		Example: "  coachctl init ~/coaching-workspace",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			written, kept, err := writeTree(coachctl.Template, "workspace-template/workspace", dir, force, renamedOnInit)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(map[string]any{"dir": dir, "written": nonNil(written), "kept": nonNil(kept), "coach_home": filepath.Join(dir, "coaching")})
			}
			a.printf("Workspace %s: %d files written, %d existing files kept.\n", dir, len(written), len(kept))
			for _, k := range kept {
				a.printf("  kept %s\n", k)
			}
			cfgPath := "~/.config/coachctl/env"
			if cfg, err := a.config(); err == nil {
				cfgPath = cfg.Path
			}
			a.printf("\nNext: set COACH_HOME=%s in %s, then `coachctl doctor`.\n", filepath.Join(dir, "coaching"), cfgPath)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite files that already exist")
	return cmd
}

// writeTree copies an embedded directory to dst, keeping executable bits for
// shell scripts.
func writeTree(src fs.FS, root, dst string, force bool, rename map[string]string) (written, kept []string, err error) {
	err = fs.WalkDir(src, root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(path, root), "/")
		if rel == "" {
			return os.MkdirAll(dst, 0o755)
		}
		if newName, ok := rename[filepath.Base(rel)]; ok {
			rel = filepath.Join(filepath.Dir(rel), newName)
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if _, statErr := os.Stat(target); statErr == nil && !force {
			kept = append(kept, rel)
			return nil
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		data, err := fs.ReadFile(src, path)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(target, data, mode); err != nil {
			return err
		}
		written = append(written, rel)
		return nil
	})
	sort.Strings(written)
	sort.Strings(kept)
	return written, kept, err
}

func (a *App) skillCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "skill", Short: "Install the coachctl skill for Claude Code"}
	var dir string
	install := &cobra.Command{
		Use:   "install",
		Short: "Write the skill matching this binary into ~/.claude/skills/coachctl/",
		Long: `Replace ~/.claude/skills/coachctl/ with the skill embedded in this binary, so
the agent's instructions always match the installed commands. Run it after
every upgrade.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			target := dir
			if target == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				target = filepath.Join(home, ".claude", "skills", "coachctl")
			}
			if err := os.RemoveAll(target); err != nil {
				return fmt.Errorf("remove the old skill: %w", err)
			}
			written, _, err := writeTree(coachctl.Skill, "skill", target, true, nil)
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(target, SkillVersionFile), []byte(a.Version+"\n"), 0o644); err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(map[string]any{"dir": target, "files": written, "version": a.Version})
			}
			a.printf("Skill %s installed to %s (%d files).\n", a.Version, target, len(written))
			return nil
		},
	}
	install.Flags().StringVar(&dir, "dir", "", "install here instead of ~/.claude/skills/coachctl")
	cmd.AddCommand(install)
	return cmd
}
