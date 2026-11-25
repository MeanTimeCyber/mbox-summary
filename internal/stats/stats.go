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
	owners := normalizedIdentitySet(identities)
	summary := model.Summary{
		GeneratedAt:     time.Now().UTC(),
		Cutoff:          cutoff.UTC(),
		MessageCount:    len(messages),
		OwnerIdentities: sortedOwners(owners),
	}

	weeklyStart := weekStart(cutoff).AddDate(0, 0, -103*7)
	weeklyMap := make(map[string]*model.PeriodStats)
	yearlyMap := make(map[string]*model.PeriodStats)

	senderCounts := make(map[string]int)
	senderNames := make(map[string]string)
	senderLists := make(map[string]bool)
	recipientCounts := make(map[string]int)
	recipientNames := make(map[string]string)
	recipientLists := make(map[string]bool)

	for _, message := range messages {
		if message.Date.IsZero() {
			continue
		}
		date := message.Date.UTC()
		sent := owners[message.From.Address]

		// Count one received event per message, while ranking every unique recipient address.
		receivedAddresses := uniqueAddresses(message.To, message.Cc, message.Bcc)
		received := false
		for _, address := range receivedAddresses {
			if owners[address.Address] {
				received = true
				break
			}
		}

		if !date.Before(weeklyStart) && !date.After(cutoff) {
			period := isoWeekKey(date)
			entry := periodEntry(weeklyMap, period)
			if sent {
				entry.Sent++
			}
			if received {
				entry.Received++
			}
		} else if date.Before(weeklyStart) {
			period := date.Format("2006")
			entry := periodEntry(yearlyMap, period)
			if sent {
				entry.Sent++
			}
			if received {
				entry.Received++
			}
		}

		if !sent && message.From.Address != "" {
			senderCounts[message.From.Address]++
			senderNames[message.From.Address] = preferName(senderNames[message.From.Address], message.From.Name)
			senderLists[message.From.Address] = senderLists[message.From.Address] || isMailingList(message)
		}

		for _, address := range receivedAddresses {
			if owners[address.Address] {
				continue
			}
			recipientCounts[address.Address]++
			recipientNames[address.Address] = preferName(recipientNames[address.Address], address.Name)
			recipientLists[address.Address] = recipientLists[address.Address] || isMailingList(message)
		}
	}

	summary.Weekly = orderedWeekly(weeklyStart, cutoff, weeklyMap)
	summary.Yearly = orderedYearly(yearlyMap)
	summary.TopSenders = buildRanking(senderCounts, senderNames, senderLists, top)
	summary.TopRecipients = buildRanking(recipientCounts, recipientNames, recipientLists, top)
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
