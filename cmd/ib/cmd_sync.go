package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/mikaelchan95/inbox-brain/internal/connector/telegram"
	"github.com/mikaelchan95/inbox-brain/internal/connector/wacli"
	"github.com/mikaelchan95/inbox-brain/internal/extract"
	"github.com/mikaelchan95/inbox-brain/internal/model"
)

// cmdTelegram handles "ib telegram connect": it validates TELEGRAM_BOT_TOKEN
// via getMe and stores the connector row.
func cmdTelegram(args []string, stdout io.Writer) error {
	if len(args) != 1 || args[0] != "connect" {
		return fmt.Errorf("usage: ib telegram connect")
	}
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		return fmt.Errorf("TELEGRAM_BOT_TOKEN is not set; create a bot with @BotFather and export TELEGRAM_BOT_TOKEN")
	}

	e, err := openEnv()
	if err != nil {
		return err
	}
	defer e.close()

	c, err := telegram.Connect(e.st, token)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Connected Telegram bot %q.\n", c.ConnectorRow.Name)
	fmt.Fprintln(stdout, "Next: ib sync telegram --once")
	return nil
}

// cmdSync dispatches "ib sync telegram", "ib sync email" and
// "ib sync whatsapp-wacli".
func cmdSync(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ib sync telegram [--once|--follow] | ib sync email [--once|--follow] | ib sync whatsapp-wacli --db PATH")
	}
	switch args[0] {
	case "telegram":
		return syncTelegram(args[1:], stdout)
	case "email":
		return syncEmail(args[1:], stdout)
	case "whatsapp-wacli":
		return syncWacli(args[1:], stdout)
	default:
		return fmt.Errorf("unknown sync source %q (expected telegram, email or whatsapp-wacli)", args[0])
	}
}

// syncTelegram rebuilds the connector from its stored row plus
// TELEGRAM_BOT_TOKEN, syncs once (default) or follows, then classifies the
// new conversations.
func syncTelegram(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("sync telegram", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	once := fs.Bool("once", false, "fetch pending updates once and exit")
	follow := fs.Bool("follow", false, "long-poll for updates until interrupted")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("ib sync telegram: %w", err)
	}
	if *once && *follow {
		return fmt.Errorf("choose one of --once or --follow")
	}
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		return fmt.Errorf("TELEGRAM_BOT_TOKEN is not set; export the bot token used for: ib telegram connect")
	}

	e, err := openEnv()
	if err != nil {
		return err
	}
	defer e.close()

	conns, err := e.st.ListConnectors()
	if err != nil {
		return err
	}
	var row *model.Connector
	for i := range conns {
		if conns[i].Channel == model.ChannelTelegram && conns[i].Provider == model.ProviderTelegramBotAPI {
			row = &conns[i]
			break
		}
	}
	if row == nil {
		return fmt.Errorf("no Telegram connector configured; run: ib telegram connect")
	}
	c := &telegram.Connector{Token: token, Store: e.st, Workspace: e.ws, ConnectorRow: *row}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if *follow {
		fmt.Fprintln(stdout, "Following Telegram updates (Ctrl-C to stop)...")
		if err := c.Follow(ctx); err != nil {
			return err
		}
	} else {
		n, err := c.SyncOnce(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Synced %d new message(s).\n", n)
	}
	return classifyAfterSync(e, stdout)
}

// defaultWacliDB is the conventional wacli database location.
func defaultWacliDB() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".wacli", "wacli.db")
	}
	return filepath.Join(home, ".wacli", "wacli.db")
}

// syncWacli imports WhatsApp chats from a wacli.db (read-only) and classifies
// the new conversations.
func syncWacli(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("sync whatsapp-wacli", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", defaultWacliDB(), "path to wacli.db")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("ib sync whatsapp-wacli: %w", err)
	}

	e, err := openEnv()
	if err != nil {
		return err
	}
	defer e.close()

	sum, err := wacli.Import(context.Background(), e.st, e.ws, *dbPath)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Imported %d conversation(s), %d new message(s) (%d already imported).\n",
		sum.Conversations, sum.Messages, sum.Skipped)
	return classifyAfterSync(e, stdout)
}

// classifyAfterSync runs the local classifier over the synced conversations
// and reports how many now wait in the review queue.
func classifyAfterSync(e *env, stdout io.Writer) error {
	pl, err := e.newPipeline(extract.NewRulesProvider(), stdout)
	if err != nil {
		return err
	}
	n, err := pl.ClassifyAll(context.Background())
	if err != nil {
		return err
	}
	all, err := e.st.ListConversationClassifications()
	if err != nil {
		return err
	}
	pending := 0
	for _, c := range all {
		if c.UserOverride == "" && !c.ReviewedByUser && c.Classification != model.ConvPersonal {
			pending++
		}
	}
	fmt.Fprintf(stdout, "Classified %d conversation(s); %d need review — run: ib classify review\n", n, pending)
	return nil
}
