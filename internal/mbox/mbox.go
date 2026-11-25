// Package mbox reads mbox records and extracts the email metadata needed for summaries.
package mbox

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/mail"
	"runtime"
	"strings"
	"sync"
	"time"

	emersion "github.com/emersion/go-mbox"
	"github.com/meantimecyber/mbox-summary/internal/model"
)

// Message contains normalized metadata extracted from one email.
type Message struct {
	Date          time.Time
	DateValid     bool
	MessageID     string
	Subject       string
	References    []string
	InReplyTo     []string
	From          Address
	To            []Address
	Cc            []Address
	Bcc           []Address
	ListHeaders   map[string]bool
	ListDomains   []string
	ListAddresses []Address
	SentFolder    bool
	Folder        FolderRole
	SourceFile    string
}

// Address contains a normalized email address and display name.
type Address struct {
	Address string
	Name    string
}

// ParseResult contains successfully parsed messages and non-fatal warnings.
type ParseResult struct {
	Messages    []Message
	Warnings    []model.ParseWarning
	ParsedCount int
}

// ParseProgress reports streaming parser progress and byte throughput position.
type ParseProgress struct {
	MessageIndex int
	MessageCount int
	WarningCount int
	BytesRead    int64
	TotalBytes   int64
}

// ParseOptions configures cancellation and progress reporting for a mailbox parse.
type ParseOptions struct {
	Context         context.Context
	Progress        func(ParseProgress)
	TotalBytes      int64
	DiscardMessages bool
	MessageHandler  func(Message)
	Workers         int
	MaxHeaderBytes  int64
	ByteOffset      int64
	SourceFolder    FolderRole
	SourceFile      string
}

// countingReader records bytes consumed while emersion/go-mbox streams a mailbox.
type countingReader struct {
	reader    io.Reader
	bytesRead int64
}

// Read updates the byte counter after a successful underlying read.
func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.bytesRead += int64(n)
	return n, err
}

// headerJob contains a bounded header block for worker-thread parsing.
type headerJob struct {
	Index  int
	Header []byte
}

// headerResult contains the parsed metadata or an extraction error for one record.
type headerResult struct {
	Index   int
	Message Message
	Err     error
}

// extractHeaders reads only through the RFC 5322 header separator, then discards
// the remaining body before the next mbox record is requested.
func extractHeaders(r io.Reader, maxHeaderBytes int64) ([]byte, error) {
	buffered := bufio.NewReader(r)
	header := make([]byte, 0, 4096)
	var bytesRead int64

	for {
		line, err := buffered.ReadBytes('\n')
		if len(line) > 0 {
			bytesRead += int64(len(line))
			if bytesRead > maxHeaderBytes {
				_, _ = io.Copy(io.Discard, buffered)
				return nil, errors.New("email header block exceeds configured size limit")
			}
			header = append(header, line...)
		}
		if len(line) == 1 && line[0] == '\n' ||
			len(line) == 2 && line[0] == '\r' && line[1] == '\n' {
			_, err = io.Copy(io.Discard, buffered)
			if err != nil {
				return nil, err
			}
			return header, nil
		}
		if errors.Is(err, io.EOF) {
			if len(header) == 0 {
				return nil, io.ErrUnexpectedEOF
			}
			_, copyErr := io.Copy(io.Discard, buffered)
			if copyErr != nil {
				return nil, copyErr
			}
			return header, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// processHeaderResult applies one worker result to parser output in archive order.
func processHeaderResult(result *ParseResult, parsedCount *int, options ParseOptions, bytesRead int64, value headerResult) {
	if value.Err != nil {
		result.Warnings = append(result.Warnings, model.ParseWarning{
			MessageIndex: value.Index,
			Text:         value.Err.Error(),
		})
		reportProgress(options, bytesRead, value.Index, *parsedCount, len(result.Warnings))
		return
	}
	if !value.Message.DateValid {
		result.Warnings = append(result.Warnings, model.ParseWarning{
			MessageIndex: value.Index,
			Text:         "message is missing a valid Date header",
		})
	}
	value.Message.Folder = options.SourceFolder
	value.Message.SourceFile = options.SourceFile
	if value.Message.Folder == FolderSent {
		value.Message.SentFolder = true
	}
	if options.MessageHandler != nil {
		options.MessageHandler(value.Message)
	}
	if !options.DiscardMessages {
		result.Messages = append(result.Messages, value.Message)
	}
	(*parsedCount)++
	result.ParsedCount = *parsedCount
	reportProgress(options, bytesRead, value.Index, *parsedCount, len(result.Warnings))
}

// ProviderDomains lists common consumer email provider domains as weak inference hints.
var ProviderDomains = []string{
	"gmail.com",
	"outlook.com",
	"hotmail.com",
	"live.com",
	"msn.com",
	"yahoo.com",
	"icloud.com",
	"me.com",
	"mac.com",
	"aol.com",
	"proton.me",
	"protonmail.com",
	"zoho.com",
	"gmx.com",
	"mail.com",
	"yandex.com",
	"fastmail.com",
	"tutanota.com",
	"tuta.com",
	"hey.com",
}

// Parse validates and streams an mbox using emersion/go-mbox, then extracts RFC 5322 metadata.
func Parse(r io.Reader) (ParseResult, error) {
	return ParseWithOptions(r, ParseOptions{})
}

// ParseWithOptions validates and streams an mbox while supporting cancellation
// and progress callbacks. It deliberately parses records sequentially so the
// archive stream and per-message memory stay bounded.
func ParseWithOptions(r io.Reader, options ParseOptions) (ParseResult, error) {
	result := ParseResult{}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	workers := options.Workers
	if workers < 1 {
		workers = runtime.GOMAXPROCS(0)
		if workers > 4 {
			workers = 4
		}
	}
	maxHeaderBytes := options.MaxHeaderBytes
	if maxHeaderBytes < 1 {
		maxHeaderBytes = 1024 * 1024
	}

	counted := &countingReader{reader: r, bytesRead: options.ByteOffset}
	reader := emersion.NewReader(counted)
	parsedCount := 0
	jobs := make(chan headerJob, workers*2)
	results := make(chan headerResult, workers*2)
	var workerGroup sync.WaitGroup

	for i := 0; i < workers; i++ {
		workerGroup.Add(1)
		go func() {
			defer workerGroup.Done()
			for job := range jobs {
				message, err := parseRecord(bytes.NewReader(job.Header))
				select {
				case results <- headerResult{Index: job.Index, Message: message, Err: err}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		workerGroup.Wait()
		close(results)
	}()

	pending := make(map[int]headerResult)
	nextResult := 0
	processResults := func() error {
		for {
			select {
			case resultValue, ok := <-results:
				if !ok {
					results = nil
					for _, value := range pending {
						processHeaderResult(&result, &parsedCount, options, counted.bytesRead, value)
					}
					pending = nil
					return nil
				}
				pending[resultValue.Index] = resultValue
			default:
				for pending != nil {
					value, exists := pending[nextResult]
					if !exists {
						return nil
					}
					delete(pending, nextResult)
					processHeaderResult(&result, &parsedCount, options, counted.bytesRead, value)
					nextResult++
				}
				return nil
			}
		}
	}

	for index := 0; ; index++ {
		if err := ctx.Err(); err != nil {
			close(jobs)
			return result, err
		}
		record, err := reader.NextMessage()
		if errors.Is(err, io.EOF) {
			close(jobs)
			for value := range results {
				pending[value.Index] = value
			}
			for nextResult < index {
				value, exists := pending[nextResult]
				if exists {
					processHeaderResult(&result, &parsedCount, options, counted.bytesRead, value)
				}
				nextResult++
			}
			reportProgress(options, counted.bytesRead, index, parsedCount, len(result.Warnings))
			break
		}
		if err != nil {
			close(jobs)
			if index == 0 {
				return result, err
			}
			result.Warnings = append(result.Warnings, model.ParseWarning{
				MessageIndex: index,
				Text:         err.Error(),
			})
			reportProgress(options, counted.bytesRead, index, parsedCount, len(result.Warnings))
			break
		}
		header, headerErr := extractHeaders(record, maxHeaderBytes)
		if headerErr == nil {
			job := headerJob{Index: index, Header: header}
			select {
			case jobs <- job:
			case <-ctx.Done():
				close(jobs)
				return result, ctx.Err()
			}
		} else {
			pending[index] = headerResult{Index: index, Err: headerErr}
		}
		if err := processResults(); err != nil {
			close(jobs)
			return result, err
		}
	}

	result.ParsedCount = parsedCount
	return result, nil
}

// reportProgress invokes the optional callback with a consistent progress snapshot.
func reportProgress(options ParseOptions, bytesRead int64, messageIndex, messageCount, warningCount int) {
	if options.Progress == nil {
		return
	}
	options.Progress(ParseProgress{
		MessageIndex: messageIndex,
		MessageCount: messageCount,
		WarningCount: warningCount,
		BytesRead:    bytesRead,
		TotalBytes:   options.TotalBytes,
	})
}

// parseRecord parses one RFC 5322 record and returns only the metadata needed by the report.
func parseRecord(r io.Reader) (Message, error) {
	// ReadMessage parses headers while exposing the body separately. It does not
	// need to consume the body here because only metadata is used by summaries.
	parsed, err := mail.ReadMessage(r)
	if err != nil {
		return Message{}, err
	}

	message := Message{
		MessageID:  strings.ToLower(strings.Trim(strings.TrimSpace(parsed.Header.Get("Message-ID")), "<>")),
		Subject:    strings.TrimSpace(parsed.Header.Get("Subject")),
		References: parseMessageIDList(parsed.Header.Get("References")),
		InReplyTo:  parseMessageIDList(parsed.Header.Get("In-Reply-To")),
		To:         parseAddressHeader(parsed.Header, "To"),
		Cc:         parseAddressHeader(parsed.Header, "Cc"),
		Bcc:        parseAddressHeader(parsed.Header, "Bcc"),
		ListHeaders: map[string]bool{
			"List-Id":          parsed.Header.Get("List-Id") != "",
			"List-Post":        parsed.Header.Get("List-Post") != "",
			"List-Unsubscribe": parsed.Header.Get("List-Unsubscribe") != "",
		},
	}
	message.ListDomains = parseListDomains(parsed.Header.Get("List-Id"))
	message.ListAddresses = parseListPostAddresses(parsed.Header.Get("List-Post"))

	message.From = firstAddress(parsed.Header.Get("From"))
	if resent := firstAddress(parsed.Header.Get("Resent-From")); message.From.Address == "" && resent.Address != "" {
		message.From = resent
	}

	if date, err := mail.ParseDate(parsed.Header.Get("Date")); err == nil {
		message.Date = date.UTC()
		message.DateValid = true
	}

	message.SentFolder = hasSentFolderHeader(parsed.Header)
	return message, nil
}

// firstAddress parses the first normalized address from an RFC 5322 address header.
func firstAddress(value string) Address {
	addresses, err := mail.ParseAddressList(value)
	if err != nil || len(addresses) == 0 {
		return Address{}
	}
	return normalizeAddress(addresses[0])
}

// parseMessageIDList extracts and normalizes message IDs from reply/thread headers.
func parseMessageIDList(value string) []string {
	fields := strings.Fields(value)
	seen := make(map[string]bool)
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		id := strings.ToLower(strings.Trim(strings.TrimSpace(field), "<>,"))
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, id)
	}
	return result
}

// parseAddressHeader parses, deduplicates, and normalizes one address header.
func parseAddressHeader(header mail.Header, key string) []Address {
	addresses, err := mail.ParseAddressList(header.Get(key))
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	result := make([]Address, 0, len(addresses))
	for _, address := range addresses {
		normalized := normalizeAddress(address)
		if normalized.Address == "" || seen[normalized.Address] {
			continue
		}
		seen[normalized.Address] = true
		result = append(result, normalized)
	}
	return result
}

// parseListDomains extracts the domain-like identifier from List-Id headers.
func parseListDomains(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	start := strings.LastIndex(value, "<")
	end := strings.LastIndex(value, ">")
	if start >= 0 && end > start {
		value = value[start+1 : end]
	}
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "@")
	if value == "" {
		return nil
	}
	return []string{value}
}

// parseListPostAddresses extracts mailto addresses from a List-Post header.
func parseListPostAddresses(value string) []Address {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	lower := strings.ToLower(value)
	mailto := strings.Index(lower, "mailto:")
	if mailto >= 0 {
		value = value[mailto+len("mailto:"):]
		if end := strings.IndexAny(value, ">, "); end >= 0 {
			value = value[:end]
		}
		address := normalizeAddress(&mail.Address{Address: value})
		if address.Address != "" {
			return []Address{address}
		}
	}

	addresses, err := mail.ParseAddressList(value)
	if err != nil {
		return nil
	}
	result := make([]Address, 0, len(addresses))
	for _, address := range addresses {
		normalized := normalizeAddress(address)
		if normalized.Address != "" {
			result = append(result, normalized)
		}
	}
	return result
}

// normalizeAddress lowercases and trims an address and decodes its display name.
func normalizeAddress(address *mail.Address) Address {
	if address == nil {
		return Address{}
	}
	return Address{
		Address: strings.ToLower(strings.TrimSpace(address.Address)),
		Name:    strings.TrimSpace(address.Name),
	}
}

// hasSentFolderHeader recognizes sent-folder metadata commonly found in mbox exports.
func hasSentFolderHeader(header mail.Header) bool {
	values := []string{
		header.Get("X-Gmail-Labels"),
		header.Get("X-Folder"),
	}
	for _, value := range values {
		lower := strings.ToLower(value)
		if strings.Contains(lower, "sent mail") ||
			strings.Contains(lower, "sent items") ||
			strings.Contains(lower, "/sent") ||
			lower == "sent" {
			return true
		}
	}
	return false
}

// Inference reports the likely mailbox owner address and ambiguity candidates.
type Inference struct {
	Address    string
	Confident  bool
	Candidates []AddressCandidate
}

// AddressCandidate combines folder and frequency evidence for an inferred identity.
type AddressCandidate struct {
	Address        string
	FolderEvidence int
	Frequency      int
}

// InferIdentity chooses a mailbox owner using sent-folder metadata and From frequency.
func InferIdentity(messages []Message) model.IdentityInference {
	folder := make(map[string]int)
	frequency := make(map[string]int)

	for _, message := range messages {
		if message.From.Address == "" {
			continue
		}
		frequency[message.From.Address]++
		if message.SentFolder {
			folder[message.From.Address]++
		}
	}

	names := make([]string, 0, len(frequency))
	for address := range frequency {
		names = append(names, address)
	}
	sortAddressCandidates(names, folder, frequency)

	candidates := make([]string, 0, len(names))
	for _, address := range names {
		candidates = append(candidates, address)
	}

	if len(names) == 0 {
		return model.IdentityInference{Candidates: candidates}
	}

	top := names[0]
	topFolder := folder[top]
	topFrequency := frequency[top]
	confident := false

	if topFolder > 0 {
		// Folder evidence is strong only for a unique address or a clear lead.
		confident = len(names) == 1 ||
			(topFolder > folder[names[1]] && topFolder >= topFrequency/2)
	} else if topFrequency >= 2 && (len(names) == 1 || topFrequency >= frequency[names[1]]*2) {
		confident = true
	}

	return model.IdentityInference{
		Address:        top,
		Confident:      confident,
		FolderEvidence: topFolder,
		Frequency:      topFrequency,
		Candidates:     candidates,
	}
}

// IdentityAnalyzer incrementally tracks owner inference evidence without retaining messages.
type IdentityAnalyzer struct {
	folder    map[string]int
	frequency map[string]int
	newest    time.Time
	count     int
}

// NewIdentityAnalyzer creates empty streaming identity and cutoff evidence.
func NewIdentityAnalyzer() *IdentityAnalyzer {
	return &IdentityAnalyzer{
		folder:    make(map[string]int),
		frequency: make(map[string]int),
	}
}

// Observe adds one message to streaming identity and cutoff evidence.
func (a *IdentityAnalyzer) Observe(message Message) {
	a.count++
	if message.DateValid && message.Date.After(a.newest) {
		a.newest = message.Date
	}
	if message.From.Address == "" {
		return
	}
	a.frequency[message.From.Address]++
	if message.SentFolder {
		a.folder[message.From.Address]++
	}
}

// Newest returns the latest valid message date observed.
func (a *IdentityAnalyzer) Newest() time.Time {
	return a.newest
}

// Count returns the number of parseable messages observed.
func (a *IdentityAnalyzer) Count() int {
	return a.count
}

// Inference calculates the likely owner from all observed evidence.
func (a *IdentityAnalyzer) Inference() model.IdentityInference {
	names := make([]string, 0, len(a.frequency))
	for address := range a.frequency {
		names = append(names, address)
	}
	sortAddressCandidates(names, a.folder, a.frequency)

	candidates := make([]string, 0, len(names))
	for _, address := range names {
		candidates = append(candidates, address)
	}
	if len(names) == 0 {
		return model.IdentityInference{Candidates: candidates}
	}

	top := names[0]
	topFolder := a.folder[top]
	topFrequency := a.frequency[top]
	confident := false
	if topFolder > 0 {
		confident = len(names) == 1 ||
			(topFolder > a.folder[names[1]] && topFolder >= topFrequency/2)
	} else if topFrequency >= 2 &&
		(len(names) == 1 || topFrequency >= a.frequency[names[1]]*2) {
		confident = true
	}

	return model.IdentityInference{
		Address:        top,
		Confident:      confident,
		FolderEvidence: topFolder,
		Frequency:      topFrequency,
		Candidates:     candidates,
	}
}

// CandidatesSummary formats candidates for a useful ambiguous-identity error.
func CandidatesSummary(inference model.IdentityInference) string {
	if len(inference.Candidates) == 0 {
		return "(none)"
	}
	return strings.Join(inference.Candidates, ", ")
}

// sortAddressCandidates ranks by folder evidence first, then sent frequency.
func sortAddressCandidates(addresses []string, folder, frequency map[string]int) {
	// A simple stable insertion sort keeps this logic clear and avoids extra sort keys.
	for i := 1; i < len(addresses); i++ {
		address := addresses[i]
		j := i
		for j > 0 {
			left := addresses[j-1]
			if folder[left] > folder[address] ||
				(folder[left] == folder[address] && frequency[left] >= frequency[address]) {
				break
			}
			addresses[j] = left
			j--
		}
		addresses[j] = address
	}
}
