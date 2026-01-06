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
- **Gmail-specific support:**
  - Automatic Gmail server detection
  - Skip duplicate emails (All Mail folder)
  - Gmail labels extraction and storage
  - Configurable folder filtering

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

### Gmail Configuration

Gmail IMAP has special characteristics that require specific handling. This tool automatically detects Gmail servers and applies optimized settings:

```yaml
imap:
  host: imap.gmail.com
  port: 993
  username: your-email@gmail.com
  password: your-app-password  # Use App Password, not regular password
  tls: true

storage:
  path: ./gmail-backup.sqlite3

gmail:
  # Enable/disable Gmail-specific handling (default: true, auto-detect)
  enabled: true

  # Skip [Gmail]/All Mail to avoid duplicates (default: true)
  # All Mail contains ALL emails, so syncing it creates duplicates
  skip_all_mail: true

  # Fetch Gmail labels using X-GM-LABELS extension (default: true)
  # Stores which labels each email has in Gmail
  fetch_labels: true

  # Exclude specific folders (optional)
  # Use this to skip folders you don't want to backup
  exclude_folders:
    - "[Gmail]/Spam"
    - "[Gmail]/Trash"

  # Include only specific folders (optional, takes precedence)
  # When set, ONLY these folders will be synced
  # include_folders:
  #   - "INBOX"
  #   - "[Gmail]/Sent Mail"
```

**Gmail Notes:**

1. **App Passwords**: Gmail requires App Passwords when 2FA is enabled. Generate one at https://myaccount.google.com/apppasswords
2. **All Mail**: Contains duplicates of all emails from other folders. Skipped by default to save space and time.
3. **Labels vs Folders**: Gmail uses labels, not folders. An email can have multiple labels and appear in multiple "folders".
4. **Localized Folders**: Gmail folder names vary by language (`[Gmail]` in English, `[Google Mail]` in German, etc.). The tool handles both.
5. **Non-Selectable Folders**: The `[Gmail]` folder itself is just a namespace container and is automatically skipped.
6. **Recommended Folders**:
   - `INBOX` - Your inbox
   - `[Gmail]/Sent Mail` - Sent emails
   - `[Gmail]/Drafts` - Draft emails
   - `[Gmail]/Starred` - Starred emails

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
- Gmail labels (when syncing from Gmail with labels enabled)
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
