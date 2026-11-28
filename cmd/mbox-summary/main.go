// Package main implements the mbox-summary command-line interface.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/meantimecyber/mbox-summary/internal/mbox"
	"github.com/meantimecyber/mbox-summary/internal/model"
	"github.com/meantimecyber/mbox-summary/internal/report"
	sqliteexport "github.com/meantimecyber/mbox-summary/internal/sqliteexport"
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

// folderFlags maps folder labels to export files or directories.
type folderFlags map[string]string

// String returns a stable list of folder mappings.
func (f *folderFlags) String() string {
	values := make([]string, 0, len(*f))
	for folder, path := range *f {
		values = append(values, folder+"="+path)
	}
	return strings.Join(values, ",")
}

// Set parses Folder=path mappings.
func (f *folderFlags) Set(value string) error {
	parts := strings.SplitN(value, "=", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return errors.New("folder mapping must use Folder=path")
	}
	if *f == nil {
		*f = make(map[string]string)
	}
	(*f)[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	return nil
}

// progressTracker renders one monotonic overall process percentage across
// parsing, analysis, and output phases.
type progressTracker struct {
	enabled bool
	last    time.Time
}

// overallPercent combines a phase base and bytes completed within that phase.
func overallPercent(phaseStart float64, completedBytes, totalBytes int64) float64 {
	if totalBytes <= 0 {
		return float64(phaseStart)
	}
	phaseCompleted := float64(completedBytes) * 100 / float64(totalBytes)
	if phaseCompleted > 100 {
		phaseCompleted = 100
	}
	overall := float64(phaseStart) + 45*phaseCompleted/100
	if overall > 100 {
		overall = 100
	}
	return overall
}

// renderFile updates a current pass and file percentage.
func (tracker *progressTracker) renderFile(phase string, phaseStart float64, fileIndex, fileCount int,
	fileName string, fileBytesRead, fileSize, completedBytes, totalBytes, messageCount, warningCount int64, force bool) {
	if !tracker.enabled {
		return
	}
	now := time.Now()
	if !force && tracker.last.IsZero() == false && now.Sub(tracker.last) < 250*time.Millisecond {
		return
	}
	tracker.last = now
	filePercent := 0.0
	if fileSize > 0 {
		filePercent = float64(fileBytesRead) * 100 / float64(fileSize)
		if filePercent > 100 {
			filePercent = 100
		}
	}
	overall := overallPercent(phaseStart, completedBytes, totalBytes)
	fmt.Fprintf(os.Stderr, "\r[%s] file %d/%d %s %0.1f%% | overall %0.1f%% | %d messages | %d warnings",
		phase, fileIndex, fileCount, filepath.Base(fileName), filePercent, overall,
		messageCount, warningCount)
}

// renderPhase prints a stable overall percentage at a completed phase.
func (tracker *progressTracker) renderPhase(label string, overall float64) {
	if !tracker.enabled {
		return
	}
	fmt.Fprintf(os.Stderr, "\r[%s] overall %0.1f%%", label, overall)
	tracker.last = time.Now()
}

// finish prints the final progress line.
func (tracker *progressTracker) finish() {
	if !tracker.enabled {
		return
	}
	fmt.Fprintln(os.Stderr, "\r[Complete] overall 100.0%")
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
	attachmentsDir := fs.String("attachments-dir", "", "optional folder where unique message attachments are exported")
	sqlitePath := fs.String("sqlite", "", "optional SQLite metadata database path")
	sqliteBatch := fs.Int("sqlite-batch", 5000, "email writes per SQLite transaction")

	var identities identityFlags
	fs.Var(&identities, "identity", "personal email address (repeatable)")
	var folders folderFlags
	fs.Var(&folders, "folder", "folder mapping in Folder=path form (repeatable)")

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
	if *sqliteBatch < 100 || *sqliteBatch > 100000 {
		return errors.New("--sqlite-batch must be between 100 and 100000")
	}

	var cutoff time.Time
	if *asOf != "" {
		parsed, err := time.Parse("2006-01-02", *asOf)
		if err != nil {
			return fmt.Errorf("invalid --as-of date: %w", err)
		}
		cutoff = parsed
	}

	sources, err := mbox.Discover(*input, map[string]string(folders))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("input directory contains no .mbox or .mbx files")
		}
		if errors.Is(err, os.ErrInvalid) {
			return errors.New("input is neither a supported mailbox file nor a directory")
		}
		return fmt.Errorf("discover input: %w", err)
	}

	var totalBytes int64
	for _, source := range sources {
		totalBytes += source.Size
		outputInfo, statErr := os.Stat(*output)
		if statErr == nil {
			sourceInfo, sourceErr := os.Stat(source.Path)
			if sourceErr == nil && os.SameFile(sourceInfo, outputInfo) {
				return errors.New("input and output paths refer to the same file")
			}
		}
	}

	parseContext, stopSignal := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignal()
	if *timeout > 0 {
		var cancel context.CancelFunc
		parseContext, cancel = context.WithTimeout(parseContext, *timeout)
		defer cancel()
	}

	tracker := progressTracker{enabled: *showProgress}
	progressOptions := func(source mbox.SourceFile, fileIndex int, phase string, phaseStart float64, completedBytes int64) mbox.ParseOptions {
		return mbox.ParseOptions{
			Context:    parseContext,
			TotalBytes: source.Size,
			Workers:    *workers,
			Progress: func(p mbox.ParseProgress) {
				if !*showProgress {
					return
				}
				tracker.renderFile(
					phase,
					phaseStart,
					fileIndex+1,
					len(sources),
					source.Path,
					p.BytesRead,
					source.Size,
					completedBytes+p.BytesRead,
					totalBytes,
					int64(p.MessageCount),
					int64(p.WarningCount),
					false,
				)
			},
		}
	}

	analyzer := mbox.NewIdentityAnalyzer()
	sourceCoverage := make([]model.SourceCoverage, 0, len(sources))
	allFirstWarnings := make([]model.ParseWarning, 0)
	completedBytes := int64(0)
	for sourceIndex, source := range sources {
		firstPassFile, err := os.Open(source.Path)
		if err != nil {
			return fmt.Errorf("open input %s: %w", source.Path, err)
		}
		firstOptions := progressOptions(source, sourceIndex, "Identity pass", 0, completedBytes)
		firstOptions.DiscardMessages = true
		firstOptions.SourceFolder = source.Folder
		firstOptions.SourceFile = source.Path
		firstOptions.MessageHandler = analyzer.Observe
		firstResult, err := mbox.ParseWithOptions(firstPassFile, firstOptions)
		closeErr := firstPassFile.Close()
		if err == nil && closeErr != nil {
			err = fmt.Errorf("close input %s: %w", source.Path, closeErr)
		}
		if err == nil {
			tracker.renderFile("Identity pass", 0, sourceIndex+1, len(sources), source.Path,
				source.Size, source.Size, completedBytes+source.Size, totalBytes,
				int64(firstResult.ParsedCount), int64(len(firstResult.Warnings)), true)
		}
		sourceCoverage = append(sourceCoverage, model.SourceCoverage{
			Path:     source.Path,
			Folder:   string(source.Folder),
			Messages: firstResult.ParsedCount,
			Warnings: len(firstResult.Warnings),
		})
		allFirstWarnings = append(allFirstWarnings, firstResult.Warnings...)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("mailbox parsing timed out after %s", *timeout)
			}
			if errors.Is(err, context.Canceled) {
				return errors.New("mailbox parsing canceled")
			}
			return fmt.Errorf("parse mailbox %s: %w", source.Path, err)
		}
		completedBytes += source.Size
	}
	tracker.renderPhase("Identity pass complete", 45)
	fmt.Fprintln(os.Stderr)
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
	ownerSet := make(map[string]bool)
	for _, owner := range ownerIdentities {
		owner = strings.ToLower(strings.TrimSpace(owner))
		if owner != "" {
			ownerSet[owner] = true
		}
	}

	var sqliteStore *sqliteexport.Store
	sqliteCompleted := false
	if *sqlitePath != "" {
		sqliteStore, err = sqliteexport.Open(*sqlitePath)
		if err != nil {
			return fmt.Errorf("open SQLite database: %w", err)
		}
		defer sqliteStore.Close()
		if err := sqliteStore.SetBatchSize(*sqliteBatch); err != nil {
			sqliteStore.Close()
			return err
		}
		owners := make([]string, 0, len(ownerIdentities))
		for _, owner := range ownerIdentities {
			owner = strings.ToLower(strings.TrimSpace(owner))
			if owner != "" {
				owners = append(owners, owner)
				ownerSet[owner] = true
			}
		}
		if _, err := sqliteStore.StartRun(*input, cutoff, owners); err != nil {
			sqliteStore.RollbackBatch()
			return fmt.Errorf("start SQLite run: %w", err)
		}
		defer func() {
			if !sqliteCompleted {
				sqliteStore.RollbackBatch()
				sqliteStore.FailRun()
			}
		}()
	}

	accumulator := stats.NewAccumulator(ownerIdentities, cutoff, *top)
	dedupe, err := mbox.OpenDedupeIndex()
	if err != nil {
		return fmt.Errorf("create deduplication index: %w", err)
	}
	defer dedupe.Close()

	allSecondWarnings := make([]model.ParseWarning, 0)
	completedBytes = 0
	exportedFiles := 0
	exportedBytes := int64(0)
	for sourceIndex, source := range sources {
		if sqliteStore != nil {
			if err := sqliteStore.UpsertSource(source, sourceCoverage[sourceIndex].Messages, sourceCoverage[sourceIndex].Warnings); err != nil {
				sqliteStore.FailRun()
				return fmt.Errorf("store SQLite source %s: %w", source.Path, err)
			}
		}
		secondPassFile, err := os.Open(source.Path)
		if err != nil {
			return fmt.Errorf("open input for aggregation %s: %w", source.Path, err)
		}
		secondOptions := progressOptions(source, sourceIndex, "Aggregation pass", 45, completedBytes)
		secondOptions.DiscardMessages = true
		secondOptions.SourceFolder = source.Folder
		secondOptions.SourceFile = source.Path
		secondOptions.ExtractAttachments = sqliteStore != nil || *attachmentsDir != ""
		secondOptions.ExtractAttachmentData = *attachmentsDir != ""
		secondOptions.MetadataOnlyAttachments = sqliteStore != nil && *attachmentsDir == ""
		duplicatesBefore := dedupe.DuplicateRecords
		var dedupeErr error
		secondOptions.MessageHandler = func(message mbox.Message) {
			unique, err := dedupe.Add(message)
			if err != nil {
				dedupeErr = err
				return
			}
			if err == nil && unique {
				accumulator.Observe(message)
				exported := make([]sqliteexport.ExportedAttachment, 0)
				if *attachmentsDir != "" {
					detailed, exportErr := mbox.ExportAttachmentsDetailed(*attachmentsDir, message)
					if exportErr != nil {
						dedupeErr = exportErr
						return
					}
					for _, item := range detailed {
						exportedFiles++
						exportedBytes += item.Size
						exported = append(exported, sqliteexport.ExportedAttachment{
							Index:       item.Index,
							Name:        item.Name,
							ExportPath:  item.ExportPath,
							Size:        item.Size,
							ContentType: item.ContentType,
							PayloadSHA:  item.PayloadSHA,
						})
					}
				}
				if sqliteStore != nil {
					if err := sqliteStore.UpsertEmail(source, message, ownerSet, exported); err != nil {
						dedupeErr = err
					}
				}
			}
		}
		secondResult, err := mbox.ParseWithOptions(secondPassFile, secondOptions)
		closeErr := secondPassFile.Close()
		if err == nil && closeErr != nil {
			err = fmt.Errorf("close input %s: %w", source.Path, closeErr)
		}
		if err == nil {
			tracker.renderFile("Aggregation pass", 45, sourceIndex+1, len(sources), source.Path,
				source.Size, source.Size, completedBytes+source.Size, totalBytes,
				int64(secondResult.ParsedCount), int64(len(secondResult.Warnings)), true)
		}
		sourceCoverage[sourceIndex].Duplicates = dedupe.DuplicateRecords - duplicatesBefore
		allSecondWarnings = append(allSecondWarnings, secondResult.Warnings...)
		if dedupeErr != nil {
			return fmt.Errorf("update deduplication index for %s: %w", source.Path, dedupeErr)
		}
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("mailbox parsing timed out after %s", *timeout)
			}
			if errors.Is(err, context.Canceled) {
				return errors.New("mailbox parsing canceled")
			}
			return fmt.Errorf("parse mailbox for aggregation %s: %w", source.Path, err)
		}
		completedBytes += source.Size
	}
	tracker.renderPhase("Aggregation pass complete", 90)
	fmt.Fprintln(os.Stderr)

	summary := accumulator.Summary()
	summary.ParseWarnings = append(allFirstWarnings, allSecondWarnings...)
	summary.Inference = inferred
	summary.Sources = sourceCoverage
	summary.Dedupe = model.DedupeStats{
		UniqueMessages:   dedupe.UniqueMessages,
		DuplicateRecords: dedupe.DuplicateRecords,
		MessageIDKeys:    dedupe.MessageIDKeys,
		FallbackKeys:     dedupe.FallbackKeys,
	}
	summary.Attachments.ExportedFiles = exportedFiles
	summary.Attachments.ExportedBytes = exportedBytes

	if sqliteStore != nil {
		tracker.renderPhase("Writing SQLite database", 92)
		if err := sqliteStore.StoreSummary(summary); err != nil {
			sqliteStore.FailRun()
			return fmt.Errorf("store SQLite aggregates: %w", err)
		}
		tracker.renderPhase("SQLite database complete", 95)
	}

	tracker.renderPhase("Writing HTML report", 95)
	if err := report.WriteFile(*output, summary); err != nil {
		return fmt.Errorf("write HTML report: %w", err)
	}
	if sqliteStore != nil {
		if err := sqliteStore.FinishRun(); err != nil {
			return fmt.Errorf("finish SQLite run: %w", err)
		}
		sqliteCompleted = true
	}
	tracker.finish()

	fmt.Fprintf(os.Stdout, "wrote %s (%d messages)\n", *output, summary.MessageCount)
	return nil
}
