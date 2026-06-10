package main

import (
	"context"
	"flag"
	"fmt"
	"io"
)

// cmdExtract runs action extraction over approved conversations. The
// --approved-only flag is required in v0.1: only approved extraction exists.
func cmdExtract(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("extract", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	approvedOnly := fs.Bool("approved-only", false, "extract only from approved business conversations")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("ib extract: %w", err)
	}
	if !*approvedOnly {
		return fmt.Errorf("the --approved-only flag is required: v0.1 only extracts from " +
			"approved business conversations (run: ib extract --approved-only)")
	}

	e, err := openEnv()
	if err != nil {
		return err
	}
	defer e.close()

	pl, err := e.newPipeline(chooseProvider(e.cfg, stdout), stdout)
	if err != nil {
		return err
	}
	sum, err := pl.ProcessApproved(context.Background())
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Processed %d conversation(s), skipped %d, created %d action(s), %d failure(s).\n",
		sum.ConversationsProcessed, sum.ConversationsSkipped, sum.ActionsCreated, sum.Failures)
	if sum.ActionsCreated > 0 {
		fmt.Fprintln(stdout, "Next: see what needs doing: ib today")
	}
	return nil
}
