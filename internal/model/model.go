// Package model defines shared mailbox summary types.
package model

import "time"

// PeriodStats contains sent and received totals for one reporting period.
type PeriodStats struct {
	Period   string
	Sent     int
	Received int
}

// RankEntry describes one sender or recipient address.
type RankEntry struct {
	Address     string
	Name        string
	Count       int
	Percentage  float64
	MailingList bool
}

// ParseWarning records one non-fatal mailbox parsing problem.
type ParseWarning struct {
	MessageIndex int
	Text         string
}

// IdentityInference records how the mailbox owner address was inferred.
type IdentityInference struct {
	Address        string
	Confident      bool
	FolderEvidence int
	Frequency      int
	Candidates     []string
}

// Summary is the complete HTML report data model.
type Summary struct {
	GeneratedAt     time.Time
	Cutoff          time.Time
	Weekly          []PeriodStats
	Yearly          []PeriodStats
	TopSenders      []RankEntry
	TopRecipients   []RankEntry
	MessageCount    int
	ParseWarnings   []ParseWarning
	Inference       IdentityInference
	OwnerIdentities []string
}
