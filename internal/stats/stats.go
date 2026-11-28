// Package stats aggregates mailbox messages into time periods and address rankings.
package stats

import (
	"fmt"
	"path/filepath"
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
	threadNodes     map[string]*threadNode
	threadRefs      map[string]string
	threadSubjects  map[string]string
	attachmentTypes map[string]*model.AttachmentTypeStats
	exportedFiles   int
	exportedBytes   int64
}

// threadNode is a union-find node for one in-window conversation root.
type threadNode struct {
	Parent          string
	Subject         string
	Participants    map[string]string
	MailingList     bool
	MessageCount    int
	FirstSeen       time.Time
	LastSeen        time.Time
	WeeklyTimeline  map[string]int
	MonthlyTimeline map[string]int
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
		threadNodes:     make(map[string]*threadNode),
		threadRefs:      make(map[string]string),
		threadSubjects:  make(map[string]string),
		attachmentTypes: make(map[string]*model.AttachmentTypeStats),
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
	if sent {
		for _, attachment := range message.Attachments {
			fileType := strings.ToLower(strings.TrimPrefix(filepath.Ext(attachment.Name), "."))
			if fileType == "" {
				fileType = strings.Split(strings.ToLower(attachment.ContentType), ";")[0]
				if fileType == "" {
					fileType = "unknown"
				}
			}
			entry := a.attachmentTypes[fileType]
			if entry == nil {
				entry = &model.AttachmentTypeStats{FileType: fileType}
				a.attachmentTypes[fileType] = entry
			}
			entry.Count++
			entry.SizeBytes += attachment.Size
		}
	}
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
	// Annual summaries cover every calendar year represented in the export,
	// including recent years that also contribute to weekly buckets.
	yearlyEntry := periodEntry(a.yearlyMap, date.Format("2006"))
	if sent {
		yearlyEntry.Sent++
	}
	if received {
		yearlyEntry.Received++
	}
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

	// Build in-window conversation threads only. Thread records remain
	// bounded by the 104-week reporting window and do not retain message bodies.
	if date.Before(weekStart(a.cutoff).AddDate(0, 0, -103*7)) {
		// Older messages are excluded from conversation timelines.
	} else {
		a.observeThread(message, date)
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

// normalizeSubject removes reply and forward prefixes for subject-based grouping.
func normalizeSubject(subject string) string {
	value := strings.ToLower(strings.TrimSpace(subject))
	for {
		value = strings.TrimSpace(value)
		changed := false
		for _, prefix := range []string{"re:", "fwd:", "fw:"} {
			if strings.HasPrefix(value, prefix) {
				value = strings.TrimSpace(value[len(prefix):])
				changed = true
			}
		}
		if len(value) >= 2 && value[0] == '[' {
			if end := strings.Index(value, "]"); end > 0 {
				value = strings.TrimSpace(value[end+1:])
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	value = strings.Join(strings.Fields(value), " ")
	if value == "re" || value == "fwd" || value == "fw" {
		return ""
	}
	return value
}

// newThreadNode creates a union-find node and initializes thread metadata.
func newThreadNode(id, subject string) *threadNode {
	return &threadNode{
		Parent:          id,
		Subject:         subject,
		Participants:    make(map[string]string),
		WeeklyTimeline:  make(map[string]int),
		MonthlyTimeline: make(map[string]int),
	}
}

// findThread resolves a thread union-find root with path compression.
func (a *Accumulator) findThread(id string) string {
	node := a.threadNodes[id]
	if node == nil {
		return id
	}
	if node.Parent != id {
		node.Parent = a.findThread(node.Parent)
	}
	return node.Parent
}

// unionThreads merges two conversation roots and retains the earliest stable root.
func (a *Accumulator) unionThreads(left, right string) string {
	left = a.findThread(left)
	right = a.findThread(right)
	if left == right {
		return left
	}
	if left > right {
		left, right = right, left
	}
	leftNode := a.threadNodes[left]
	rightNode := a.threadNodes[right]
	if leftNode == nil {
		leftNode = newThreadNode(left, "")
		a.threadNodes[left] = leftNode
	}
	if rightNode != nil {
		leftNode.MessageCount += rightNode.MessageCount
		leftNode.MailingList = leftNode.MailingList || rightNode.MailingList
		for address, name := range rightNode.Participants {
			if _, exists := leftNode.Participants[address]; !exists {
				leftNode.Participants[address] = name
			}
		}
		for period, count := range rightNode.WeeklyTimeline {
			leftNode.WeeklyTimeline[period] += count
		}
		for period, count := range rightNode.MonthlyTimeline {
			leftNode.MonthlyTimeline[period] += count
		}
		if leftNode.FirstSeen.IsZero() || (!rightNode.FirstSeen.IsZero() && rightNode.FirstSeen.Before(leftNode.FirstSeen)) {
			leftNode.FirstSeen = rightNode.FirstSeen
		}
		if rightNode.LastSeen.After(leftNode.LastSeen) {
			leftNode.LastSeen = rightNode.LastSeen
		}
		if leftNode.Subject == "" {
			leftNode.Subject = rightNode.Subject
		}
		rightNode.Parent = left
	}
	a.threadNodes[right].Parent = left
	return left
}

// observeThread assigns one in-window message to an ID or subject conversation.
func (a *Accumulator) observeThread(message mbox.Message, date time.Time) {
	ids := append(append([]string{}, message.References...), message.InReplyTo...)
	if message.MessageID != "" {
		ids = append(ids, message.MessageID)
	}

	roots := make([]string, 0)
	for _, id := range ids {
		id = strings.ToLower(strings.Trim(strings.TrimSpace(id), "<>,"))
		if id == "" {
			continue
		}
		root, exists := a.threadRefs[id]
		if !exists {
			root = id
			a.threadNodes[root] = newThreadNode(root, normalizeSubject(message.Subject))
			a.threadRefs[id] = root
		}
		roots = append(roots, a.findThread(root))
	}

	subject := normalizeSubject(message.Subject)
	if len(roots) == 0 && subject != "" {
		root, exists := a.threadSubjects[subject]
		if !exists {
			root = "subject:" + subject
			a.threadNodes[root] = newThreadNode(root, subject)
			a.threadSubjects[subject] = root
		}
		roots = append(roots, a.findThread(root))
	}

	// A message with neither a thread ID nor usable subject gets one stable node.
	if len(roots) == 0 {
		root := "subject:" + message.MessageID
		if root == "subject:" {
			root = fmt.Sprintf("message:%d", a.messageCount)
		}
		a.threadNodes[root] = newThreadNode(root, strings.TrimSpace(message.Subject))
		roots = append(roots, root)
	}

	root := roots[0]
	for _, candidate := range roots[1:] {
		root = a.unionThreads(root, candidate)
	}
	if subject != "" {
		if subjectRoot, exists := a.threadSubjects[subject]; exists {
			root = a.unionThreads(root, subjectRoot)
		} else {
			a.threadSubjects[subject] = root
		}
	}
	for _, id := range ids {
		id = strings.ToLower(strings.Trim(strings.TrimSpace(id), "<>,"))
		if id != "" {
			a.threadRefs[id] = root
		}
	}
	root = a.findThread(root)
	node := a.threadNodes[root]
	if node == nil {
		node = newThreadNode(root, subject)
		a.threadNodes[root] = node
	}
	if strings.TrimSpace(message.Subject) != "" &&
		(node.Subject == "" ||
			strings.EqualFold(normalizeSubject(node.Subject), normalizeSubject(message.Subject))) {
		node.Subject = strings.TrimSpace(message.Subject)
	}
	node.MessageCount++
	if node.FirstSeen.IsZero() || date.Before(node.FirstSeen) {
		node.FirstSeen = date
	}
	if date.After(node.LastSeen) {
		node.LastSeen = date
	}
	node.MailingList = node.MailingList || isMailingList(message)

	addresses := make([]mbox.Address, 0)
	if message.From.Address != "" {
		addresses = append(addresses, message.From)
	}
	addresses = append(addresses, uniqueAddresses(message.To, message.Cc, message.Bcc)...)
	for _, address := range addresses {
		if address.Address == "" || a.owners[address.Address] {
			continue
		}
		if _, exists := node.Participants[address.Address]; !exists {
			node.Participants[address.Address] = address.Name
		}
	}

	year, week := date.ISOWeek()
	node.WeeklyTimeline[fmt.Sprintf("%04d-W%02d", year, week)]++
	node.MonthlyTimeline[date.Format("2006-01")]++
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
	summary.TopThreads = a.buildTopThreads(20)
	attachmentTypes := make([]model.AttachmentTypeStats, 0, len(a.attachmentTypes))
	for _, entry := range a.attachmentTypes {
		attachmentTypes = append(attachmentTypes, *entry)
	}
	sort.Slice(attachmentTypes, func(i, j int) bool {
		if attachmentTypes[i].SizeBytes != attachmentTypes[j].SizeBytes {
			return attachmentTypes[i].SizeBytes > attachmentTypes[j].SizeBytes
		}
		return attachmentTypes[i].FileType < attachmentTypes[j].FileType
	})
	if len(attachmentTypes) > 10 {
		attachmentTypes = attachmentTypes[:10]
	}
	summary.Attachments = model.AttachmentStats{
		ExportedFiles: a.exportedFiles,
		ExportedBytes: a.exportedBytes,
		TopSentTypes:  attachmentTypes,
	}
	return summary
}

// buildTopThreads ranks conversation nodes by distinct message count and builds timelines.
func (a *Accumulator) buildTopThreads(limit int) []model.ConversationThread {
	roots := make(map[string]*threadNode)
	for id := range a.threadNodes {
		root := a.findThread(id)
		rootNode := roots[root]
		if rootNode == nil {
			rootNode = a.threadNodes[root]
		}
		if rootNode != nil {
			roots[root] = rootNode
		}
	}

	result := make([]model.ConversationThread, 0, len(roots))
	for id, node := range roots {
		if node == nil || node.MessageCount == 0 {
			continue
		}
		participantNames := make([]model.ThreadParticipant, 0, len(node.Participants))
		for address, name := range node.Participants {
			participantNames = append(participantNames, model.ThreadParticipant{Address: address, Name: name})
		}
		sort.Slice(participantNames, func(i, j int) bool {
			return participantNames[i].Address < participantNames[j].Address
		})
		participantCount := len(participantNames)
		if len(participantNames) > 10 {
			participantNames = participantNames[:10]
		}

		result = append(result, model.ConversationThread{
			ID:               id,
			Subject:          node.Subject,
			Participants:     participantNames,
			ParticipantCount: participantCount,
			MessageCount:     node.MessageCount,
			FirstSeen:        node.FirstSeen,
			LastSeen:         node.LastSeen,
			MailingList:      node.MailingList,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].MessageCount != result[j].MessageCount {
			return result[i].MessageCount > result[j].MessageCount
		}
		if !result[i].LastSeen.Equal(result[j].LastSeen) {
			return result[i].LastSeen.After(result[j].LastSeen)
		}
		if result[i].ParticipantCount != result[j].ParticipantCount {
			return result[i].ParticipantCount > result[j].ParticipantCount
		}
		return result[i].ID < result[j].ID
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result
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
