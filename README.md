# mbox-summary

`mbox-summary` parses an RFC 5322 mbox export and creates a standalone HTML report containing weekly email volume for the latest 104 ISO weeks, yearly volume for older mail, top sender rankings, and top recipient rankings.

## Requirements

- Go 1.22 or newer
- An mbox file with Unix-style `From ` separator lines

## Build

```bash
go mod tidy
go build -o mbox-summary ./cmd/mbox-summary
```

## Usage

```bash
./mbox-summary --input mailbox.mbox --output summary.html
```

If the mailbox owner cannot be inferred reliably, the command reports likely addresses and requires `--identity`:

```bash
./mbox-summary \
  --input mailbox.mbox \
  --output summary.html \
  --identity owner@example.com \
  --identity owner@company.example \
  --top 50
```

Available flags:

| Flag | Default | Purpose |
|---|---|---|
| `--input` | Required | Input mbox path |
| `--output` | `summary.html` | Output HTML path |
| `--identity` | Inferred | Personal address; repeat to add addresses |
| `--top` | `50` | Number of sender and recipient ranking entries |
| `--as-of` | Latest valid message date | UTC reporting cutoff in `YYYY-MM-DD` format |
| `--timeout` | `0` (disabled) | Maximum parsing duration, for example `30m` or `2h` |
| `--progress` | `true` | Show byte-based parsing progress on stderr |

## Identity inference

The tool first looks for sent-folder evidence such as Gmail labels or sent-folder headers. It then falls back to `From` address frequency. A top result is only accepted when folder evidence or frequency is clearly dominant. Ambiguous exports require an explicit identity.

Common consumer-provider domains are maintained in the implementation as weak hints; exact inferred addresses remain the classification key. The inexact nature of provider-only matching means that a shared domain is never sufficient to identify the mailbox owner.

## Report behavior

- Sent mail means a message `From` address matches an explicit or inferred owner identity.
- Received mail means an owner identity appears in `To`, `Cc`, or `Bcc`.
- Each recipient address is counted once per message.
- Weekly reporting uses ISO year/week buckets and UTC.
- The latest 104 ISO weeks include zero-volume weeks.
- Older mail is grouped by UTC calendar year.
- Mailing lists are identified by `List-Id`, `List-Post`, or `List-Unsubscribe`.
- Individual malformed messages produce warnings and do not prevent the remaining messages from being reported.

## Large exports

The tool streams the mbox in two bounded-memory passes. The first pass infers the mailbox owner and latest date. The second pass aggregates weekly/yearly periods and address rankings. Parsed message records are not retained in memory. Ranking and period maps are bounded by the number of unique addresses and years/weeks present in the archive.

Parsing is sequential because `emersion/go-mbox` owns the streaming separator reader and advancing it concurrently would duplicate or buffer complete messages. This is preferable for multi-gigabyte inputs: it keeps memory flat and avoids I/O/read-ahead contention. Use `--timeout 2h` for a hard parsing deadline and `--progress=false` to suppress stderr progress in scripted environments. Pressing Ctrl-C cancels both passes through Go context cancellation.
