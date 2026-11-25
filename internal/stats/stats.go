// Package stats aggregates mailbox messages into time periods and address rankings.
package stats

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/meantimecyber/mbox-summary/internal/mbox"
	"github.com/meantimecyber/mbox-summary/internal/model"
)

// Summarize builds all weekly, yearly, and address-ranking statistics.
func Summarize(messages []mbox.Message, identities []string, cutoff time.Time, top int) model.Summary {
	accumulator := NewAccumulator(identities, cutoff, top)
	for _, message := range messages {
		accumulator.Observe(message)
	}
	return accumulator.Summary()
}

// Accumulator incrementally aggregates messages without retaining the mailbox.
type Accumulator struct {
	owners          map[string]bool
	cutoff          time.Time
	top             int
	messageCount    int
	weeklyMap       map[string]*model.PeriodStats
	yearlyMap       map[string]*model.PeriodStats
	senderCounts    map[string]int
	senderNames     map[string]string
	senderLists     map[string]bool
	recipientCounts map[string]int
	recipientNames  map[string]string
	recipientLists  map[string]bool
	folderStats     map[string]*model.FolderStats
}

// NewAccumulator creates a bounded-memory mailbox accumulator.
func NewAccumulator(identities []string, cutoff time.Time, top int) *Accumulator {
	return &Accumulator{
		owners:          normalizedIdentitySet(identities),
		cutoff:          cutoff.UTC(),
		top:             top,
		weeklyMap:       make(map[string]*model.PeriodStats),
		yearlyMap:       make(map[string]*model.PeriodStats),
		senderCounts:    make(map[string]int),
		senderNames:     make(map[string]string),
		senderLists:     make(map[string]bool),
		recipientCounts: make(map[string]int),
		recipientNames:  make(map[string]string),
		recipientLists:  make(map[string]bool),
		folderStats:     make(map[string]*model.FolderStats),
	}
}

// Observe adds one message to bounded period and ranking maps.
func (a *Accumulator) Observe(message mbox.Message) {
	a.messageCount++
	if message.Date.IsZero() {
		return
	}

	date := message.Date.UTC()
	if date.After(a.cutoff) {
		return
	}
	sent := a.owners[message.From.Address] || message.Folder == mbox.FolderSent || message.SentFolder
	receivedAddresses := uniqueAddresses(message.To, message.Cc, message.Bcc)
	received := false
	for _, address := range receivedAddresses {
		if a.owners[address.Address] {
			received = true
			break
		}
		folderName := string(message.Folder)
		if folderName == "" {
			folderName = "Unknown"
		}
		folderEntry := a.folderStats[folderName]
		if folderEntry == nil {
			folderEntry = &model.FolderStats{Folder: folderName}
			a.folderStats[folderName] = folderEntry
		}
		folderEntry.Messages++
		if sent {
			folderEntry.Sent++
		}
		if received {
			folderEntry.Received++
		}
	}

	weeklyStart := weekStart(a.cutoff).AddDate(0, 0, -103*7)
	if !date.Before(weeklyStart) {
		period := isoWeekKey(date)
		entry := periodEntry(a.weeklyMap, period)
		if sent {
			entry.Sent++
		}
		if received {
			entry.Received++
		}
	} else {
		period := date.Format("2006")
		entry := periodEntry(a.yearlyMap, period)
		if sent {
			entry.Sent++
		}
		if received {
			entry.Received++
		}
	}

	if !sent && message.From.Address != "" {
		a.senderCounts[message.From.Address]++
		a.senderNames[message.From.Address] = preferName(a.senderNames[message.From.Address], message.From.Name)
		a.senderLists[message.From.Address] = a.senderLists[message.From.Address] || isMailingList(message)
	}

	for _, address := range receivedAddresses {
		if a.owners[address.Address] {
			continue
		}
		a.recipientCounts[address.Address]++
		a.recipientNames[address.Address] = preferName(a.recipientNames[address.Address], address.Name)
		a.recipientLists[address.Address] = a.recipientLists[address.Address] ||
			isMailingListAddress(message, address.Address)
	}
}

// Summary returns a completed report model from the accumulated maps.
func (a *Accumulator) Summary() model.Summary {
	summary := model.Summary{
		GeneratedAt:     time.Now().UTC(),
		Cutoff:          a.cutoff,
		MessageCount:    a.messageCount,
		OwnerIdentities: sortedOwners(a.owners),
		Weekly:          orderedWeekly(weekStart(a.cutoff).AddDate(0, 0, -103*7), a.cutoff, a.weeklyMap),
		Yearly:          orderedYearly(a.yearlyMap),
		TopSenders:      buildRanking(a.senderCounts, a.senderNames, a.senderLists, a.top),
		TopRecipients:   buildRanking(a.recipientCounts, a.recipientNames, a.recipientLists, a.top),
	}
	folderNames := make([]string, 0, len(a.folderStats))
	for folderName := range a.folderStats {
		folderNames = append(folderNames, folderName)
	}
	sort.Strings(folderNames)
	for _, folderName := range folderNames {
		summary.FolderStats = append(summary.FolderStats, *a.folderStats[folderName])
	}
	return summary
}

// normalizedIdentitySet creates a lower-case exact-address identity set.
func normalizedIdentitySet(identities []string) map[string]bool {
	result := make(map[string]bool)
	for _, identity := range identities {
		identity = strings.ToLower(strings.TrimSpace(identity))
		if identity != "" {
			result[identity] = true
		}
	}
	return result
}

// sortedOwners returns identity addresses in stable alphabetical order.
func sortedOwners(owners map[string]bool) []string {
	result := make([]string, 0, len(owners))
	for owner := range owners {
		result = append(result, owner)
	}
	sort.Strings(result)
	return result
}

// weekStart moves a UTC date to the Monday at the beginning of its ISO week.
func weekStart(date time.Time) time.Time {
	date = date.UTC()
	weekday := int(date.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	return time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC).
		AddDate(0, 0, -(weekday - 1))
}

// isoWeekKey returns a stable ISO year and ISO week label.
func isoWeekKey(date time.Time) string {
	year, week := date.ISOWeek()
	return time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).Format("2006") +
		"-W" + twoDigits(week)
}

// twoDigits formats a week number with a leading zero.
func twoDigits(value int) string {
	return fmt.Sprintf("%02d", value)
}

// periodEntry gets or creates a mutable period bucket.
func periodEntry(index map[string]*model.PeriodStats, period string) *model.PeriodStats {
	entry := index[period]
	if entry == nil {
		entry = &model.PeriodStats{Period: period}
		index[period] = entry
	}
	return entry
}

// orderedWeekly emits all 104 weekly buckets, including weeks with no email.
func orderedWeekly(start, cutoff time.Time, index map[string]*model.PeriodStats) []model.PeriodStats {
	result := make([]model.PeriodStats, 0, 104)
	for date := weekStart(start); !date.After(weekStart(cutoff)); date = date.AddDate(0, 0, 7) {
		period := isoWeekKey(date)
		entry := index[period]
		if entry == nil {
			entry = &model.PeriodStats{Period: period}
		}
		result = append(result, *entry)
	}
	return result
}

// orderedYearly sorts historical period buckets chronologically.
func orderedYearly(index map[string]*model.PeriodStats) []model.PeriodStats {
	keys := make([]string, 0, len(index))
	for key := range index {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := make([]model.PeriodStats, 0, len(keys))
	for _, key := range keys {
		result = append(result, *index[key])
	}
	return result
}

// uniqueAddresses combines recipient fields and counts each address only once per message.
func uniqueAddresses(groups ...[]mbox.Address) []mbox.Address {
	seen := make(map[string]bool)
	result := make([]mbox.Address, 0)
	for _, group := range groups {
		for _, address := range group {
			if address.Address == "" || seen[address.Address] {
				continue
			}
			seen[address.Address] = true
			result = append(result, address)
		}
	}
	return result
}

// isMailingList identifies a mailing-list message using standard List-* headers.
func isMailingList(message mbox.Message) bool {
	return message.ListHeaders["List-Id"] ||
		message.ListHeaders["List-Post"] ||
		message.ListHeaders["List-Unsubscribe"]
}

// isMailingListAddress matches a recipient only against list addresses or List-Id domains.
func isMailingListAddress(message mbox.Message, recipient string) bool {
	for _, address := range message.ListAddresses {
		if address.Address == recipient {
			return true
		}
	}

	at := strings.LastIndex(recipient, "@")
	if at >= 0 {
		domain := strings.ToLower(recipient[at+1:])
		for _, listDomain := range message.ListDomains {
			if domain == listDomain || strings.HasSuffix(domain, "."+listDomain) {
				return true
			}
		}
	}
	return false
}

// preferName keeps the first useful display name encountered for an address.
func preferName(current, candidate string) string {
	if current != "" {
		return current
	}
	return candidate
}

// buildRanking converts address counts into sorted, percentage-ranked entries.
func buildRanking(counts map[string]int, names map[string]string, lists map[string]bool, top int) []model.RankEntry {
	total := 0
	for _, count := range counts {
		total += count
	}

	result := make([]model.RankEntry, 0, len(counts))
	for address, count := range counts {
		percentage := 0.0
		if total > 0 {
			percentage = float64(count) * 100 / float64(total)
		}
		result = append(result, model.RankEntry{
			Address:     address,
			Name:        names[address],
			Count:       count,
			Percentage:  percentage,
			MailingList: lists[address],
		})
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].Address < result[j].Address
	})
	if len(result) > top {
		result = result[:top]
	}
	return result
}
