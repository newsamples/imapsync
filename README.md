# imapsync

A lightweight email backup tool that syncs emails from IMAP servers to local storage using SQLite3.

## Features

- Full email backup from IMAP servers
- Incremental sync (only new emails after initial backup)
- Preserves email metadata (flags, headers, body)
- Stores complete raw RFC822 messages
- Tracks mailbox state for efficient syncing
- Uses SQLite3 for reliable local storage
- Supports TLS connections
- Built-in web UI for browsing stored emails
- Progress bars showing sync status
- Graceful shutdown support (Ctrl+C)
- Automatic reconnection on network errors with exponential backoff
- Resilient to transient network issues

## Installation

```bash
go build -o imapsync .
```

## Configuration

Create a `config.yaml` file:

```yaml
imap:
  host: privateemail.com
  port: 993
  username: your-email@example.com
  password: your-password
  tls: true

storage:
  path: ./emails-backup.sqlite3
```

See `config.yaml.example` for a template.

## Usage

### Sync Emails

Sync emails from your IMAP server to local storage:

```bash
./imapsync sync -c config.yaml
```

Disable progress bars (use plain logs):

```bash
./imapsync sync -c config.yaml --progress=false
```

### Browse Emails

Start a web server to browse your stored emails:

```bash
./imapsync serve -c config.yaml --addr :8080
```

Then open your browser at `http://localhost:8080`

### Options

**Global flags:**
- `-c, --config`: Path to configuration file (default: config.yaml)
- `--verbose`: Enable verbose logging

**Sync-specific flags:**
- `--progress`: Show progress bars (default: true)

**Server-specific flags:**
- `--addr`: Server address to listen on (default: :8080)

## How It Works

1. **First Run**: Performs a full backup of all mailboxes and emails
2. **Subsequent Runs**: Only syncs new emails since the last sync
3. **UIDValidity Check**: Detects mailbox resets and performs full resync if needed
4. **INBOX Priority**: Always syncs INBOX folder first before other mailboxes

The tool stores:

- Email content (headers and body)
- Email metadata (subject, from, to, date, size, flags)
- Mailbox state (UIDValidity, last synced UID)

## Storage

Emails are stored in a SQLite3 database (single `.sqlite3` file) at the path specified in the configuration. The database contains:

- `emails` table: Individual email records with full message content
- `mailbox_state` table: Mailbox synchronization state

**Benefits of SQLite3:**
- Single file storage (easy to backup)
- No corruption issues
- Standard SQL interface
- Can be inspected with any SQLite tool
- Read-only mode for web server (safe concurrent access)
- Pure Go implementation (no CGO required)
- Gzip compression for email content (saves disk space)

## Requirements

- Go 1.25.3 or later
- IMAP server with username/password authentication
- Disk space for email storage
- No CGO dependency (uses pure Go SQLite implementation)

## License

This is a lab project for testing purposes.
