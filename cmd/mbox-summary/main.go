// Package main implements the mbox-summary command-line interface.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
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
	timeout := fs.Duration("timeout", 0, "maximum parsing time; 0 disables the timeout")
	showProgress := fs.Bool("progress", true, "show parsing progress")
	workers := fs.Int("workers", min(runtime.GOMAXPROCS(0), 4), "number of header-parsing workers")

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
	if *workers < 1 {
		return errors.New("--workers must be at least 1")
	}

	var cutoff time.Time
	if *asOf != "" {
		parsed, err := time.Parse("2006-01-02", *asOf)
		if err != nil {
			return fmt.Errorf("invalid --as-of date: %w", err)
		}
		cutoff = parsed
	}

	inputInfo, err := os.Stat(*input)
	if err != nil {
		return fmt.Errorf("stat input: %w", err)
	}
	if !inputInfo.Mode().IsRegular() {
		return fmt.Errorf("input is not a regular file: %s", *input)
	}
	if outputInfo, err := os.Stat(*output); err == nil && os.SameFile(inputInfo, outputInfo) {
		return errors.New("input and output paths refer to the same file")
	}

	parseContext, stopSignal := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignal()
	if *timeout > 0 {
		var cancel context.CancelFunc
		parseContext, cancel = context.WithTimeout(parseContext, *timeout)
		defer cancel()
	}

	var lastProgress time.Time
	progressOptions := func() mbox.ParseOptions {
		return mbox.ParseOptions{
			Context:    parseContext,
			TotalBytes: inputInfo.Size(),
			Workers:    *workers,
			Progress: func(p mbox.ParseProgress) {
				if !*showProgress {
					return
				}
				now := time.Now()
				if p.MessageIndex > 0 && now.Sub(lastProgress) < 250*time.Millisecond {
					return
				}
				lastProgress = now
				if p.TotalBytes > 0 {
					percent := float64(p.BytesRead) * 100 / float64(p.TotalBytes)
					if percent > 100 {
						percent = 100
					}
					fmt.Fprintf(os.Stderr, "\rparsing: %.1f%%, %d messages, %d warnings",
						percent, p.MessageCount, p.WarningCount)
					return
				}
				fmt.Fprintf(os.Stderr, "\rparsing: %d messages, %d warnings",
					p.MessageCount, p.WarningCount)
			},
		}
	}

	firstPassFile, err := os.Open(*input)
	if err != nil {
		return fmt.Errorf("open input for identity pass: %w", err)
	}
	analyzer := mbox.NewIdentityAnalyzer()
	firstOptions := progressOptions()
	firstOptions.DiscardMessages = true
	firstOptions.MessageHandler = analyzer.Observe
	firstResult, err := mbox.ParseWithOptions(firstPassFile, firstOptions)
	closeErr := firstPassFile.Close()
	if *showProgress {
		fmt.Fprintln(os.Stderr)
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("mailbox parsing timed out after %s", *timeout)
		}
		if errors.Is(err, context.Canceled) {
			return errors.New("mailbox parsing canceled")
		}
		return fmt.Errorf("parse mailbox: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close input after identity pass: %w", closeErr)
	}
	if analyzer.Count() == 0 {
		return errors.New("input contains no parseable emails")
	}

	if cutoff.IsZero() {
		cutoff = analyzer.Newest()
	}

	inferred := analyzer.Inference()
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

	secondPassFile, err := os.Open(*input)
	if err != nil {
		return fmt.Errorf("open input for aggregation pass: %w", err)
	}
	accumulator := stats.NewAccumulator(ownerIdentities, cutoff, *top)
	secondOptions := progressOptions()
	secondOptions.DiscardMessages = true
	secondOptions.MessageHandler = accumulator.Observe
	secondResult, err := mbox.ParseWithOptions(secondPassFile, secondOptions)
	closeErr = secondPassFile.Close()
	if *showProgress {
		fmt.Fprintln(os.Stderr)
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("mailbox parsing timed out after %s", *timeout)
		}
		if errors.Is(err, context.Canceled) {
			return errors.New("mailbox parsing canceled")
		}
		return fmt.Errorf("parse mailbox for aggregation: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close input after aggregation pass: %w", closeErr)
	}

	summary := accumulator.Summary()
	summary.ParseWarnings = append(firstResult.Warnings, secondResult.Warnings...)
	summary.Inference = inferred

	if err := report.WriteFile(*output, summary); err != nil {
		return fmt.Errorf("write HTML report: %w", err)
	}

	fmt.Fprintf(os.Stdout, "wrote %s (%d messages)\n", *output, summary.MessageCount)
	return nil
}
