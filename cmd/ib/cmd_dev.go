package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"

	"github.com/mikaelchan/inbox-brain/internal/api"
)

// cmdDev serves the local dashboard and JSON API.
func cmdDev(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("dev", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	port := fs.Int("port", 0, "port to listen on (default: config port)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("ib dev: %w", err)
	}

	e, err := openEnv()
	if err != nil {
		return err
	}
	defer e.close()

	p := *port
	if p == 0 {
		p = e.cfg.Port
	}
	if p == 0 {
		p = 4173
	}
	handler := api.NewServer(e.st, e.cfg)
	fmt.Fprintf(stdout, "Inbox Brain dashboard: http://localhost:%d\n", p)
	if err := http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", p), handler); err != nil {
		return fmt.Errorf("serve dashboard: %w", err)
	}
	return nil
}
