# mbox-summary

`mbox-summary` parses an RFC 5322 mbox export and creates a standalone HTML report containing weekly email volume for the latest 104 ISO weeks, yearly volume for older mail, top sender rankings, and top recipient rankings.

> [!CAUTION]
> Built entirely using GitHub Copilot. As sensibly as possible, of course, but it's still made by clankers.

## Requirements

- Go 1.22 or newer
- An mbox file with Unix-style `From ` separator lines

## Build

```bash
make build
```

Other useful local targets:

```bash
make tidy   # Update Go module dependencies
make fmt    # Format Go source
make test   # Run the test suite
make race   # Run tests with the race detector
make vet    # Run Go vet
make clean  # Remove the compiled binary
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
| `--workers` | up to 4 | Number of parallel RFC 5322 header-parsing workers |
| `--attachments-dir` | Disabled | Export unique attachment payloads into this folder |

## Attachments

The HTML report includes the top 10 sent attachment file types ranked by total sent byte size. Attachment totals include decoded MIME attachment payload sizes from unique emails. Inline MIME parts are excluded unless they have an attachment disposition or filename.

Export all unique-email attachments to a local folder:

```bash
./mbox-summary \
  --input mailbox.mbox \
  --output summary.html \
  --attachments-dir ./attachments
```

Export filenames use a date, a 12-character truncated Message-ID, a three-digit attachment index, and a sanitized original filename. Existing filenames receive a numeric suffix instead of being overwritten.
| `--folder` | Inferred | Folder mapping in `Folder=path` form; repeat for multiple exports |

## Directory exports

Pass a Thunderbird export directory as `--input`:

```bash
./mbox-summary \
  --input /path/to/Thunderbird/exports \
  --output summary.html \
  --identity owner@example.com
```

Directory discovery recursively includes `.mbox` and `.mbx` files and extensionless files whose first line is a valid Unix mbox separator. This supports Firefox/Thunderbird exports named only `Inbox`, `Sent`, or `Drafts`. Folder roles are inferred from file names and parent directories. Override ambiguous exports with repeatable mappings:

```bash
./mbox-summary \
  --input /exports \
  --folder Sent=/exports/Sent \
  --folder Inbox=/exports/Inbox \
  --output summary.html
```

Cross-folder duplicates are removed using `Message-ID`. Messages without one use a stable SHA-256 fallback derived from date, sender, recipients, and subject. Deduplication is stored in a temporary disk-backed index. Sent exports are processed first for deterministic sent-folder precedence.

To run directly through the Makefile:

```bash
make run INPUT=mailbox.mbox OUTPUT=summary.html IDENTITY=owner@example.com
```

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
- The report ranks the top 20 in-window conversation threads using References/In-Reply-To IDs and normalized subject fallback.

## Large exports

The tool streams the mbox in two bounded-memory passes. The first pass infers the mailbox owner and latest date. The second pass aggregates weekly/yearly periods and address rankings. Parsed message records are not retained in memory. Ranking and period maps are bounded by the number of unique addresses and years/weeks present in the archive.

The mbox stream is read sequentially because `emersion/go-mbox` owns separator detection. Extracted header blocks are processed by a bounded worker pool, while message bodies are discarded. This keeps memory flat and avoids I/O contention on multi-gigabyte inputs. Use `--timeout 2h` for a hard parsing deadline and `--progress=false` to suppress stderr progress in scripted environments. Pressing Ctrl-C cancels both passes through Go context cancellation.
