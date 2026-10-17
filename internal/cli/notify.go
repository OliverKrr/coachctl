package cli

import (
	"context"
	"strings"

	"github.com/OliverKrr/coachctl/internal/config"
	"github.com/OliverKrr/coachctl/internal/notify"
	"github.com/spf13/cobra"
)

func (a *App) telegram() (*notify.Telegram, error) {
	cfg, err := a.config()
	if err != nil {
		return nil, err
	}
	if !cfg.Has(config.KeyTelegramToken) || !cfg.Has(config.KeyTelegramChat) {
		return nil, usagef("Telegram is not configured: add %s and %s to %s", config.KeyTelegramToken, config.KeyTelegramChat, cfg.Path)
	}
	return notify.New(cfg.Get(config.KeyTelegramBase), cfg.Get(config.KeyTelegramToken), cfg.Get(config.KeyTelegramChat)), nil
}

func (a *App) notifyCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "notify",
		Short: "Send the athlete a Telegram message (text on stdin)",
		Long: `Send a plain-text push to the athlete's phone from their Telegram bot: a
routine's check-in, or a short safety flag. It arrives hours away from any
conversation, so make it self-contained. It is not a chat channel: replies do
not reach the session. Hard limit 4096 characters; a longer message is refused,
not trimmed (a push is a headline plus one action, the long version belongs in
the journal).`,
		Example: `  coachctl notify <<'EOF'
Readiness: HRV 18 % unter Basis, Ruhepuls +6
Heute locker statt Intervalle, die 6x1k auf Donnerstag.
EOF`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			text, err := a.readStdin("the message")
			if err != nil {
				return err
			}
			text = strings.TrimSpace(text)
			if text == "" {
				return usagef("message is empty")
			}
			n := notify.Length(text)
			if n > notify.MaxChars {
				return usagef("message is %d characters; Telegram's limit is %d. Shorten it and put the detail in the journal", n, notify.MaxChars)
			}
			if dryRun {
				if a.JSON {
					return a.printJSON(map[string]any{"dry_run": true, "chars": n, "text": text})
				}
				a.printf("Dry run, nothing sent (%d characters):\n%s\n", n, text)
				return nil
			}
			t, err := a.telegram()
			if err != nil {
				return err
			}
			id, err := t.Send(context.Background(), text)
			if err != nil {
				return err
			}
			if a.JSON {
				return a.printJSON(map[string]any{"sent": true, "chars": n, "message_id": id})
			}
			a.printf("Sent (%d characters).\n", n)
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "check length and print the message, send nothing")
	return cmd
}
