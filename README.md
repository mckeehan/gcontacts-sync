# gcontacts-sync

Bidirectional sync between **Google Contacts** and local **Markdown files**.

Each contact becomes a `.md` file: YAML front-matter holds every structured field, and the Notes/biography field becomes the file body. Edit either side and the changes flow through on the next sync.

---

## Example file

```markdown
---
resource_name: people/c123456789012345678
etag: '"Abc123XyzEtag"'
synced_hash: 4f3a2b1c...
display_name: Jane O'Connell
given_name: Jane
family_name: O'Connell
company: Acme Corp
job_title: VP Engineering
emails:
  - label: work
    value: jane@acme.com
phones:
  - label: mobile
    value: +1-555-123-4567
birthday: "1990-03-15"
created_at: "2020-01-15T08:30:00Z"
updated_at: "2024-06-01T12:00:00Z"
---

Met Jane at GopherCon 2023. Great talk on distributed tracing.
Follow up about her open-source observability project.
```

Filenames are derived from the display name (or company name) with special characters removed and spaces replaced by hyphens:

- `Jane O'Connell` → `Jane-OConnell.md`
- `Acme Corp. Ltd.` → `Acme-Corp.-Ltd.md`

If two contacts produce the same filename, the second gets the numeric suffix of its resource name appended (e.g. `Jane-OConnell-9876543.md`).

---

## Prerequisites

- Go 1.22+
- A Google Cloud project with the **People API** enabled

---

## Setup

### 1. Enable the People API

1. Go to [Google Cloud Console](https://console.cloud.google.com/)
2. Create or select a project
3. Navigate to **APIs & Services → Library**
4. Search for **"Google People API"** and click **Enable**

### 2. Create OAuth 2.0 credentials

1. Go to **APIs & Services → Credentials**
2. Click **Create Credentials → OAuth client ID**
3. Application type: **Desktop app**
4. Download the JSON file and save it as `credentials.json` in the project root

### 3. Fetch dependencies

```bash
go mod download
```

Or use the setup script which also verifies the build:

```bash
chmod +x setup.sh && ./setup.sh
```

### 4. First run (OAuth flow)

```bash
./gcontacts-sync --pull
```

On first run the program prints a URL. Open it in your browser, grant access to your contacts, and paste the authorisation code back into the terminal. The token is cached in `token.json` for all future runs.

---

## Usage

```
Usage of gcontacts-sync:
  -credentials string
        Path to OAuth2 credentials JSON (default "credentials.json")
  -dir string
        Directory to store markdown files (default "./contacts")
  -dry-run
        Preview changes without writing any files
  -pull
        Only pull from Google Contacts → Markdown
  -push
        Only push from Markdown → Google Contacts
  -delete-orphans
        Delete local files for contacts that have been deleted in Google
  -token string
        Path to store/load OAuth2 token (default "token.json")
  -verbose
        Print a line for every file created, updated, or skipped
```

### Initial import

Pull all your Google Contacts into a fresh directory:

```bash
./gcontacts-sync --pull --dir ~/notes/contacts --verbose
```

### Ongoing bidirectional sync (default)

```bash
./gcontacts-sync --dir ~/notes/contacts
```

### Pull only (Google → Markdown)

```bash
./gcontacts-sync --pull --dir ~/notes/contacts
```

### Push only (Markdown → Google)

```bash
./gcontacts-sync --push --dir ~/notes/contacts
```

### Preview without making changes

```bash
./gcontacts-sync --dry-run --verbose
```

### Remove local files for contacts deleted in Google

```bash
./gcontacts-sync --delete-orphans
```

By default, contacts deleted on the Google side are reported as warnings but local files are left untouched. Pass `--delete-orphans` to remove them. Use `--dry-run --delete-orphans --verbose` to preview what would be deleted first.

---

## How change detection works

Each file contains a `synced_hash` field in its front-matter. This is a SHA-256 of all user-editable content (names, contact info, notes, etc.) computed at the time the file was last written by a pull from Google.

On each sync run:

- **Has the local file changed?** Recompute the hash of the current file content and compare to `synced_hash`. If they differ, the user has edited the file locally.
- **Has Google changed?** Compare the `updated_at` timestamp stored in the file (from the last pull) against the timestamp returned by the People API for the `CONTACT` source. Sub-second differences are ignored.

This means local change detection is **content-based**, not filesystem-mtime-based, so pulling from Google never falsely marks a file as locally modified.

---

## Conflict resolution

| Local changed? | Google changed? | Result |
|---|---|---|
| No | No | Skipped |
| No | Yes | Google version written to disk |
| Yes | No | Local version pushed to Google |
| Yes | Yes | Google wins; warning added to report |
| File has no `resource_name` | — | Created in Google on next push/sync |
| `resource_name` not found in Google | — | Warned (or deleted with `--delete-orphans`) |

When a push encounters a stale ETag (contact changed in Google since last pull), the cached ETag from the current `ListAll` response is used automatically — no extra API call is made.

Write API calls are throttled with a 500 ms pause between requests to stay within Google's per-minute quota. Transient 429 rate-limit errors are retried up to 3 times with exponential backoff (1 s, 2 s, 4 s).

---

## YAML field reference

| YAML key | Google Contacts field |
|---|---|
| `resource_name` | Internal People API ID (`people/cXXX`) |
| `etag` | ETag used for conflict-safe updates |
| `synced_hash` | Content hash written on every pull (do not edit) |
| `display_name` | Full formatted name |
| `given_name` / `family_name` | First / last name |
| `middle_name` | Middle name |
| `name_prefix` / `name_suffix` | Dr., Jr., etc. |
| `nickname` | Nickname |
| `company` | Organisation name |
| `job_title` | Job title |
| `department` | Department |
| `emails[]` | Email addresses, each with a `label` (work, home, …) |
| `phones[]` | Phone numbers, each with a `label` |
| `addresses[]` | Structured postal addresses |
| `urls[]` | Websites |
| `social_profiles[]` | Social media profiles |
| `ims[]` | Instant messaging handles |
| `birthday` | Birthday — `YYYY-MM-DD` or `--MM-DD` (no year) |
| `events[]` | Custom dates (anniversary, etc.) |
| `relations[]` | Related people (spouse, assistant, etc.) |
| `groups[]` | Contact group / label memberships |
| `created_at` / `updated_at` | Timestamps in `YYYY-MM-DDTHH:MM:SSZ` format |

**Notes / biography** → plain text body of the file, below the closing `---`.

> **Do not edit** `resource_name`, `etag`, or `synced_hash` manually. These are sync metadata managed by the program. Editing them will cause unpredictable behaviour.

---

## Security notes

- `credentials.json` and `token.json` contain sensitive OAuth data — they are listed in `.gitignore` and should never be committed.
- The token grants full read/write access to Google Contacts. Store it securely (e.g. `chmod 600 token.json`).
- The program requests only the `https://www.googleapis.com/auth/contacts` scope.

---

## Running tests

```bash
go test ./...
```

---

## Project structure

```
gcontacts-sync/
├── main.go                     # CLI entry point and flags
├── go.mod
├── setup.sh                    # First-time setup helper
├── internal/
│   ├── auth/
│   │   └── auth.go             # OAuth2 flow and token cache
│   ├── contacts/
│   │   └── service.go          # Google People API wrapper
│   ├── markdown/
│   │   ├── store.go            # Read/write .md files, content hashing
│   │   └── store_test.go       # Unit tests
│   ├── model/
│   │   └── contact.go          # Canonical Contact struct
│   └── sync/
│       ├── syncer.go           # Pull / Push / Sync / orphan sweep
│       ├── report.go           # Summary report printed after each run
│       └── fs.go               # (reserved for future filesystem helpers)
└── example-output/
    └── Jane-OConnell.md        # Example of a synced markdown file
```
