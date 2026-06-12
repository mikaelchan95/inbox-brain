package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"

	"github.com/mikaelchan95/inbox-brain/internal/connector/email"
	"github.com/mikaelchan95/inbox-brain/internal/model"
)

// cmdEmail dispatches "ib email add" and "ib email list".
func cmdEmail(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ib email add --user ADDRESS [--host HOST] | ib email list")
	}
	switch args[0] {
	case "add":
		return emailAdd(args[1:], stdout)
	case "list":
		return emailList(stdout)
	default:
		return fmt.Errorf("unknown email subcommand %q (expected add or list)", args[0])
	}
}

// emailAdd validates an IMAP account (password from IMAP_PASSWORD), stores it
// in the 0600 accounts file and upserts the connector row. Well-known
// domains (Gmail, Yahoo, Outlook, iCloud) don't need --host.
func emailAdd(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("email add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	user := fs.String("user", "", "email address (also the IMAP login)")
	host := fs.String("host", "", "IMAP server (inferred for well-known providers)")
	port := fs.Int("port", 993, "IMAP TLS port")
	folder := fs.String("folder", "INBOX", "folder to sync")
	sinceDays := fs.Int("since-days", 30, "how many days of history the first sync imports")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("ib email add: %w", err)
	}
	if *user == "" || !strings.Contains(*user, "@") {
		return fmt.Errorf("ib email add: --user must be an email address")
	}
	if *host == "" {
		*host = email.DefaultHost(*user)
		if *host == "" {
			return fmt.Errorf("ib email add: unknown provider for %s; pass --host (e.g. --host imap.thewinery.com.sg)", *user)
		}
	}
	password := os.Getenv("IMAP_PASSWORD")
	if password == "" {
		return fmt.Errorf("IMAP_PASSWORD is not set; export the account's IMAP password (for Gmail/Yahoo create an app password)")
	}

	e, err := openEnv()
	if err != nil {
		return err
	}
	defer e.close()

	account := email.Account{
		Address:     strings.ToLower(*user),
		Host:        *host,
		Port:        *port,
		Password:    password,
		Folder:      *folder,
		InitialDays: *sinceDays,
	}
	if _, err := email.Connect(e.st, account); err != nil {
		return err
	}
	if err := email.UpsertAccount(e.home, account); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Connected %s (%s, folder %s).\n", account.Address, account.Addr(), account.FolderOrDefault())
	fmt.Fprintln(stdout, "Next: ib sync email --once")
	return nil
}

// emailList prints the configured accounts (never their passwords).
func emailList(stdout io.Writer) error {
	e, err := openEnv()
	if err != nil {
		return err
	}
	defer e.close()
	accounts, err := email.LoadAccounts(e.home)
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		fmt.Fprintln(stdout, "No email accounts configured. Add one with: ib email add --user you@example.com")
		return nil
	}
	for _, a := range accounts {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", a.Address, a.Addr(), a.FolderOrDefault())
	}
	return nil
}

// syncEmail rebuilds one connector per stored account, syncs once (default)
// or follows all accounts concurrently, then classifies the new
// conversations.
func syncEmail(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("sync email", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	once := fs.Bool("once", false, "fetch new messages once and exit")
	follow := fs.Bool("follow", false, "poll for new messages until interrupted")
	accountFlag := fs.String("account", "", "sync only this account address")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("ib sync email: %w", err)
	}
	if *once && *follow {
		return fmt.Errorf("choose one of --once or --follow")
	}

	e, err := openEnv()
	if err != nil {
		return err
	}
	defer e.close()

	accounts, err := email.LoadAccounts(e.home)
	if err != nil {
		return err
	}
	if *accountFlag != "" {
		var filtered []email.Account
		for _, a := range accounts {
			if strings.EqualFold(a.Address, *accountFlag) {
				filtered = append(filtered, a)
			}
		}
		accounts = filtered
	}
	if len(accounts) == 0 {
		return fmt.Errorf("no email accounts configured; run: ib email add --user you@example.com")
	}

	var connectors []*email.Connector
	for _, a := range accounts {
		row, err := e.st.UpsertConnector(model.Connector{
			WorkspaceID: e.ws.ID,
			Channel:     model.ChannelEmail,
			Provider:    model.ProviderIMAP,
			Name:        a.Address,
			Status:      model.ConnectorActive,
		})
		if err != nil {
			return err
		}
		connectors = append(connectors, &email.Connector{
			Account: a, Store: e.st, Workspace: e.ws, ConnectorRow: row,
		})
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if *follow {
		fmt.Fprintf(stdout, "Following %d email account(s) (Ctrl-C to stop)...\n", len(connectors))
		var wg sync.WaitGroup
		for _, c := range connectors {
			wg.Add(1)
			go func(c *email.Connector) {
				defer wg.Done()
				_ = c.Follow(ctx) // Follow only returns on ctx cancel
			}(c)
		}
		wg.Wait()
	} else {
		total := 0
		for _, c := range connectors {
			n, err := c.SyncOnce(ctx)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "%s: %d new message(s).\n", c.Account.Address, n)
			total += n
		}
		fmt.Fprintf(stdout, "Synced %d new message(s).\n", total)
	}
	return classifyAfterSync(e, stdout)
}
