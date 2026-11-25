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

// SourceCoverage describes one mailbox file discovered for a directory input.
type SourceCoverage struct {
	Path       string
	Folder     string
	Messages   int
	Warnings   int
	Duplicates int
}

// DedupeStats summarizes cross-folder duplicate removal.
type DedupeStats struct {
	UniqueMessages   int
	DuplicateRecords int
	MessageIDKeys    int
	FallbackKeys     int
}

// FolderStats contains sent and received totals for one export folder role.
type FolderStats struct {
	Folder   string
	Sent     int
	Received int
	Messages int
}

// ThreadParticipant identifies one non-owner address in a conversation thread.
type ThreadParticipant struct {
	Address string
	Name    string
}

// ConversationThread describes a ranked email conversation.
type ConversationThread struct {
	ID               string
	Subject          string
	Participants     []ThreadParticipant
	ParticipantCount int
	MessageCount     int
	FirstSeen        time.Time
	LastSeen         time.Time
	MailingList      bool
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
	Sources         []SourceCoverage
	Dedupe          DedupeStats
	FolderStats     []FolderStats
	TopThreads      []ConversationThread
}
