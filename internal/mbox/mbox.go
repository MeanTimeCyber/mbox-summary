// Package mbox reads mbox records and extracts the email metadata needed for summaries.
package mbox

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/mail"
	"strings"
	"time"

	emersion "github.com/emersion/go-mbox"
	"github.com/meantimecyber/mbox-summary/internal/model"
)

// Message contains normalized metadata extracted from one email.
type Message struct {
	Date        time.Time
	From        Address
	To          []Address
	Cc          []Address
	Bcc         []Address
	ListHeaders map[string]bool
	SentFolder  bool
}

// Address contains a normalized email address and display name.
type Address struct {
	Address string
	Name    string
}

// ParseResult contains successfully parsed messages and non-fatal warnings.
type ParseResult struct {
	Messages []Message
	Warnings []model.ParseWarning
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
	result := ParseResult{}
	reader := emersion.NewReader(r)

	for index := 0; ; index++ {
		record, err := reader.NextMessage()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if index == 0 {
				return result, err
			}
			result.Warnings = append(result.Warnings, model.ParseWarning{
				MessageIndex: index,
				Text:         err.Error(),
			})
			break
		}

		message, err := parseRecord(record)
		if err != nil {
			result.Warnings = append(result.Warnings, model.ParseWarning{
				MessageIndex: index,
				Text:         err.Error(),
			})
			continue
		}
		result.Messages = append(result.Messages, message)
	}

	return result, nil
}

// parseRecord parses one RFC 5322 record and returns only the metadata needed by the report.
func parseRecord(r io.Reader) (Message, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return Message{}, err
	}

	parsed, err := mail.ReadMessage(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		return Message{}, err
	}

	message := Message{
		To:  parseAddressHeader(parsed.Header, "To"),
		Cc:  parseAddressHeader(parsed.Header, "Cc"),
		Bcc: parseAddressHeader(parsed.Header, "Bcc"),
		ListHeaders: map[string]bool{
			"List-Id":          parsed.Header.Get("List-Id") != "",
			"List-Post":        parsed.Header.Get("List-Post") != "",
			"List-Unsubscribe": parsed.Header.Get("List-Unsubscribe") != "",
		},
	}

	message.From = firstAddress(parsed.Header.Get("From"))
	if resent := firstAddress(parsed.Header.Get("Resent-From")); message.From.Address == "" && resent.Address != "" {
		message.From = resent
	}

	if date, err := mail.ParseDate(parsed.Header.Get("Date")); err == nil {
		message.Date = date.UTC()
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
		header.Get("X-Mozilla-Status"),
		header.Get("X-MS-Has-Attach"),
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
		confident = len(names) == 1 || topFolder > folder[names[1]]
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
