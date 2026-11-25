// Package main implements the mbox-summary command-line interface.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/meantimecyber/mbox-summary/internal/mbox"
	"github.com/meantimecyber/mbox-summary/internal/report"
	"github.com/meantimecyber/mbox-summary/internal/stats"
)

// identityFlags collects repeatable --identity arguments.
type identityFlags []string

// String returns the comma-separated representation used by the flag package.
func (f *identityFlags) String() string {
	return strings.Join(*f, ",")
}

// Set appends one normalized identity address.
func (f *identityFlags) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("identity cannot be empty")
	}
	*f = append(*f, value)
	return nil
}

// main is the command entry point. It exits non-zero after writing CLI errors to stderr.
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// run parses flags, validates the mailbox, infers identities, aggregates data, and writes HTML.
func run(args []string) error {
	fs := flag.NewFlagSet("mbox-summary", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	input := fs.String("input", "", "path to the input mbox file")
	output := fs.String("output", "summary.html", "path to the output HTML file")
	top := fs.Int("top", 50, "number of top sender and recipient addresses")
	asOf := fs.String("as-of", "", "optional UTC cutoff date in YYYY-MM-DD")

	var identities identityFlags
	fs.Var(&identities, "identity", "personal email address (repeatable)")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *input == "" {
		return errors.New("--input is required")
	}
	if *top < 1 {
		return errors.New("--top must be at least 1")
	}

	var cutoff time.Time
	if *asOf != "" {
		parsed, err := time.Parse("2006-01-02", *asOf)
		if err != nil {
			return fmt.Errorf("invalid --as-of date: %w", err)
		}
		cutoff = parsed
	}

	inputFile, err := os.Open(*input)
	if err != nil {
		return fmt.Errorf("open input: %w", err)
	}
	defer inputFile.Close()

	result, err := mbox.Parse(inputFile)
	if err != nil {
		return fmt.Errorf("parse mailbox: %w", err)
	}
	if len(result.Messages) == 0 {
		return errors.New("input contains no parseable emails")
	}

	if cutoff.IsZero() {
		cutoff = newestMessageDate(result.Messages)
	}

	inferred := mbox.InferIdentity(result.Messages)
	ownerIdentities := identities
	if len(ownerIdentities) == 0 {
		if inferred.Confident {
			ownerIdentities = identityFlags{inferred.Address}
			fmt.Fprintf(os.Stderr, "inferred mailbox identity: %s\n", inferred.Address)
		} else {
			return fmt.Errorf(
				"mailbox identity is ambiguous; pass --identity ADDRESS (candidates: %s)",
				mbox.CandidatesSummary(inferred),
			)
		}
	}

	summary := stats.Summarize(result.Messages, ownerIdentities, cutoff, *top)
	summary.ParseWarnings = result.Warnings
	summary.Inference = inferred

	if err := report.WriteFile(*output, summary); err != nil {
		return fmt.Errorf("write HTML report: %w", err)
	}

	fmt.Fprintf(os.Stdout, "wrote %s (%d messages)\n", *output, summary.MessageCount)
	return nil
}

// newestMessageDate finds the latest valid Date header for report determinism.
func newestMessageDate(messages []mbox.Message) time.Time {
	var newest time.Time
	for _, message := range messages {
		if message.Date.After(newest) {
			newest = message.Date
		}
	}
	return newest
}
