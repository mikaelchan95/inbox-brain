package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mikaelchan95/inbox-brain/internal/connector/demo"
	"github.com/mikaelchan95/inbox-brain/internal/extract"
)

// cmdDemo handles "ib demo seed --scenario NAME": it seeds the demo scenario
// and classifies the new conversations so the review queue is immediately
// populated.
func cmdDemo(args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "seed" {
		return fmt.Errorf("usage: ib demo seed --scenario NAME (scenarios: %s)",
			strings.Join(demo.Scenarios(), ", "))
	}
	fs := flag.NewFlagSet("demo seed", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	scenario := fs.String("scenario", "tuition-center", "demo scenario to load")
	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("ib demo seed: %w", err)
	}

	e, err := openEnv()
	if err != nil {
		return err
	}
	defer e.close()

	sum, err := demo.Seed(e.st, e.ws, *scenario)
	if err != nil {
		return err
	}
	pl, err := e.newPipeline(extract.NewRulesProvider(), stdout)
	if err != nil {
		return err
	}
	classified, err := pl.ClassifyAll(context.Background())
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Seeded scenario %q: %d conversation(s), %d message(s).\n",
		*scenario, sum.Conversations, sum.Messages)
	fmt.Fprintf(stdout, "Classified %d conversation(s).\n", classified)
	fmt.Fprintf(stdout, "\nNext: review suggested business chats: ib classify review\n")
	return nil
}
