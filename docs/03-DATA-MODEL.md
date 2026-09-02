# Homesink — Data Model

Two independent stores. Neither is derivable from the other; the API in `02-API.md` is the only bridge.

---

## 1. Server — SQLite (`.homesink/db/homesink.db`)

Pure-Go driver `modernc.org/sqlite` — **no cgo**, so the binary stays static and the container stays
`FROM scratch`-adjacent. Opened with:

```
_pragma=journal_mode(WAL)      _pragma=busy_timeout(5000)
_pragma=foreign_keys(ON)       _pragma=synchronous(NORMAL)
```
`WAL` because the ingest path writes while the browse path reads. `synchronous=NORMAL` is safe under
WAL and roughly triples small-write throughput.

### 1.1 Schema (`backend/migrations/0001_init.sql`, `go:embed`ed)

```sql
CREATE TABLE server_meta (               -- schema_version, server_id, tls_key_pem, tls_spki
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
) STRICT;

-- Content. One row per distinct uploaded byte stream.
CREATE TABLE blobs (
  hash                TEXT PRIMARY KEY,          -- sha256 of what the CLIENT uploaded, forever (D-08)
  size_bytes          INTEGER NOT NULL,
  mime_type           TEXT    NOT NULL,
  media_type          TEXT    NOT NULL CHECK (media_type IN ('image','video','audio')),
  rel_path            TEXT    NOT NULL,          -- canonical file, relative to library root
  state               TEXT    NOT NULL DEFAULT 'stored' CHECK (state IN ('stored','missing')),
  stored_hash         TEXT,                      -- hash of the file NOW; differs after transcode (D-08)
  original_replaced_at INTEGER,                  -- set when the transcode replaced the original
  width               INTEGER,
  height              INTEGER,
  duration_ms         INTEGER,
  created_at          INTEGER NOT NULL
) STRICT;

-- Placement. One row per (blob, album/year/month/filename).
CREATE TABLE items (
  item_id               TEXT PRIMARY KEY,        -- "itm_" + 16 hex
  hash                  TEXT NOT NULL REFERENCES blobs(hash) ON DELETE CASCADE,
  album                 TEXT NOT NULL,           -- already sanitised (D-10)
  year                  INTEGER NOT NULL,
  month                 INTEGER NOT NULL CHECK (month BETWEEN 1 AND 12),
  filename              TEXT NOT NULL,           -- collision-suffixed (D-13)
  rel_path              TEXT NOT NULL UNIQUE,    -- album/year/month/filename
  captured_at_ms        INTEGER NOT NULL,
  captured_offset_min   INTEGER NOT NULL,
  device_id             TEXT REFERENCES devices(device_id) ON DELETE SET NULL,
  created_at            INTEGER NOT NULL
) STRICT;
CREATE INDEX idx_items_browse ON items(album, year, month, captured_at_ms DESC, item_id DESC);
CREATE INDEX idx_items_recent ON items(captured_at_ms DESC, item_id DESC);
CREATE INDEX idx_items_hash   ON items(hash);

-- Derived files: thumbnails and transcodes. Keyed by content, not by placement.
CREATE TABLE blob_variants (
  hash       TEXT NOT NULL REFERENCES blobs(hash) ON DELETE CASCADE,
  kind       TEXT NOT NULL CHECK (kind IN ('thumb256','thumb1024','transcode')),
  rel_path   TEXT NOT NULL,
  size_bytes INTEGER NOT NULL,
  width      INTEGER, height INTEGER,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (hash, kind)
) STRICT;

-- Denormalised counters. Maintained in the same transaction as items (D-11 efficiency budget).
CREATE TABLE album_stats (
  album  TEXT NOT NULL, year INTEGER NOT NULL, month INTEGER NOT NULL,
  item_count INTEGER NOT NULL DEFAULT 0,
  size_bytes INTEGER NOT NULL DEFAULT 0,
  latest_captured_at_ms INTEGER,
  PRIMARY KEY (album, year, month)
) STRICT;

CREATE TABLE upload_sessions (
  hash           TEXT PRIMARY KEY,               -- the blob being staged; no session ids (D-16)
  declared_size  INTEGER NOT NULL,
  received_bytes INTEGER NOT NULL DEFAULT 0,
  device_id      TEXT,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
) STRICT;
CREATE INDEX idx_upload_sweep ON upload_sessions(updated_at);   -- 7-day sweeper (open question 3)

CREATE TABLE jobs (
  job_id          INTEGER PRIMARY KEY AUTOINCREMENT,
  kind            TEXT NOT NULL CHECK (kind IN ('thumbnail','transcode','probe')),
  payload         TEXT NOT NULL,                 -- JSON
  priority        INTEGER NOT NULL DEFAULT 100,  -- lower runs first; thumbs 10, transcode 100 (D-32)
  state           TEXT NOT NULL DEFAULT 'queued'
                    CHECK (state IN ('queued','running','done','failed')),
  attempts        INTEGER NOT NULL DEFAULT 0,
  next_attempt_at INTEGER NOT NULL DEFAULT 0,
  last_error      TEXT,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
) STRICT;
CREATE INDEX idx_jobs_claim ON jobs(state, priority, next_attempt_at);

CREATE TABLE devices (
  device_id        TEXT PRIMARY KEY,
  name             TEXT NOT NULL,
  token_hash       TEXT NOT NULL,                -- Argon2id, never the token (D-01)
  platform         TEXT NOT NULL,
  app_version_code INTEGER,
  created_at       INTEGER NOT NULL,
  last_seen_at     INTEGER,
  revoked_at       INTEGER
) STRICT;
CREATE INDEX idx_devices_token ON devices(token_hash) WHERE revoked_at IS NULL;

CREATE TABLE pairing_codes (
  code_hash  TEXT PRIMARY KEY,
  expires_at INTEGER NOT NULL,
  used_at    INTEGER,
  attempts   INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE TABLE app_releases (
  version_code INTEGER PRIMARY KEY,
  version_name TEXT NOT NULL,
  filename     TEXT NOT NULL,
  sha256       TEXT NOT NULL,
  size_bytes   INTEGER NOT NULL,
  min_sdk      INTEGER,
  release_notes TEXT,
  created_at   INTEGER NOT NULL
) STRICT;
```

### 1.2 Invariants the code must uphold

| # | Invariant | Enforced by |
|---|---|---|
| S1 | A `blobs` row exists **only** after the file is verified and renamed into `library/` | `ingest.Commit` writes the row inside the same tx as the rename |
| S2 | `items.rel_path` is unique and matches `album/year/month/filename` | UNIQUE index + `library.Place` builds both from one function |
| S3 | `album_stats` always equals the aggregate over `items` | Same transaction as every item insert/delete; `homesinkd fsck` re-derives |
| S4 | `blobs.hash` is never rewritten | Review rule; the transcoder writes `stored_hash` only (D-08) |
| S5 | Every `blob_variants.rel_path` file exists, or the row is absent | Variant rows written after `fsync`+rename |
| S6 | No `items` row references a missing file | `homesinkd fsck` marks `blobs.state='missing'` |

### 1.3 Migrations
Numbered, forward-only, embedded, applied in a transaction, recorded in `server_meta.schema_version`.
**A migration may never drop or retype a column an older binary reads** — the auto-update rollback path
(D-35) can put the previous binary back in front of the new schema at any moment.

---

## 2. Client — Room (`homesink.db`, schema version exported to `client/app/schemas/`)

### 2.1 Entities

```kotlin
@Entity(tableName = "media_item",
        indices = [Index(value = ["mediaStoreId"], unique = true),
                   Index(value = ["state"]), Index(value = ["capturedAtMs"])])
data class MediaItemEntity(
    @PrimaryKey(autoGenerate = true) val id: Long = 0,
    val mediaStoreId: Long,          // MediaStore._ID — the join key back to the OS
    val contentUri: String,
    val filename: String,
    val album: String,               // BUCKET_DISPLAY_NAME, raw (D-09)
    val mimeType: String,
    val mediaType: String,           // image | video | audio
    val sizeBytes: Long,
    val capturedAtMs: Long,
    val capturedOffsetMin: Int,
    val dateModifiedSec: Long,       // + size ⇒ hash cache key (D-05)
    val width: Int?, val height: Int?, val durationMs: Long?,

    val hash: String?,               // null until hashed
    val hashedAtMs: Long?,

    val state: String,               // see 2.2
    val mode: String,                // UPLOAD | UPLOAD_AND_DELETE  (defaults: 06-ALGORITHMS §2)
    val selected: Boolean = true,    // requirement: everything preselected
    val remoteItemId: String?,
    val uploadedBytes: Long = 0,     // resume point across process death
    val attemptCount: Int = 0,
    val lastErrorCode: String?,
    val syncedAtMs: Long?,
    val runId: Long?
)

@Entity(tableName = "sync_run")
data class SyncRunEntity(
    @PrimaryKey(autoGenerate = true) val id: Long = 0,
    val startedAtMs: Long, val finishedAtMs: Long?,
    val totalFiles: Int, val doneFiles: Int, val failedFiles: Int,
    val totalBytes: Long, val sentBytes: Long,
    val state: String                // RUNNING | SUCCESS | PARTIAL | FAILED | CANCELLED
)

@Entity(tableName = "interaction_event", indices = [Index(value = ["dayOfWeek","minuteOfDay"])])
data class InteractionEventEntity(
    @PrimaryKey(autoGenerate = true) val id: Long = 0,
    val timestampMs: Long,
    val dayOfWeek: Int,              // java.time.DayOfWeek value, 1=Mon … 7=Sun
    val minuteOfDay: Int,            // 0..1439
    val type: String                 // D-27
)

@Entity(tableName = "learned_schedule")
data class LearnedScheduleEntity(
    @PrimaryKey val dayOfWeek: Int,
    val minuteOfDay: Int,
    val confidence: Float,           // 0..1, effective-sample based (06-ALGORITHMS §1.4)
    val updatedAtMs: Long
)

@Entity(tableName = "pending_deletion")
data class PendingDeletionEntity(   // survives the app being killed before the consent dialog (D-22)
    @PrimaryKey val mediaStoreId: Long,
    val contentUri: String,
    val runId: Long,
    val confirmedUploadAtMs: Long
)
```

### 2.2 `media_item.state` — the state machine

```
DISCOVERED ──hash──▶ HASHED ──preflight──▶ QUEUED ──▶ UPLOADING ──commit──▶ SYNCED
     │                                        │            │                   │
     │                                        │            └─ fail ─▶ FAILED   ├─ mode=UPLOAD_AND_DELETE
     │                                        └── duplicate ──────────▶ SYNCED │        │
     └── unsupported/too large ─▶ SKIPPED                                      ▼        ▼
                                                                    SYNCED_DELETED  SYNCED_KEPT
```
- **`PENDING` in the notification rule (D-26) means** `state IN (DISCOVERED, HASHED, QUEUED, FAILED)` —
  i.e. anything not yet successfully synced and not deliberately skipped. Deselecting a file does not
  change its state, so it keeps counting; that is intentional.
- `SYNCED_KEPT` = uploaded, deletion offered, user declined. Never re-offered for deletion (D-22).
- `FAILED` is retried on the next run; `SKIPPED` never is.

### 2.3 Settings — DataStore Preferences, not Room

| Key | Type | Default | Source |
|---|---|---|---|
| `notify_threshold` | Int | **10** | requirement |
| `custom_schedule_enabled` | Bool | false | requirement toggle |
| `same_time_every_day` | Bool | true | requirement toggle |
| `custom_time_all` | Int (min of day) | 1320 (22:00) | requirement |
| `custom_time_mon…sun` | Int × 7 | 1320 | requirement |
| `wifi_only` | Bool | **true** | D-17 |
| `allow_manage_media` | Bool | false | D-22 |
| `ignored_albums` | Set\<String\> | ∅ | D-15 |
| `server_host` / `server_port` / `server_spki` / `server_id` | String | — | D-02/D-03 |
| `dismissed_update_code` | Int | 0 | D-34 |
| `last_notified_date` | String (ISO date) | "" | D-26 once-a-day guard |

The **device token lives in `EncryptedSharedPreferences`, not DataStore** — it is the one secret.

### 2.4 Client invariants

| # | Invariant | Enforced by |
|---|---|---|
| C1 | A local file is deleted only after a `SYNCED` state derived from a 2xx or a `duplicate` verdict | `DeletionManager` reads only from `pending_deletion`, which only `SyncEngine` writes on success |
| C2 | `uploadedBytes` never exceeds what the server confirmed | Written from `X-Homesink-Received`, never from the local write count |
| C3 | Rescan never re-hashes an unchanged file | Cache key `(mediaStoreId, sizeBytes, dateModifiedSec)`; any change clears `hash` |
| C4 | At most one sync run is `RUNNING` | WorkManager unique work `sync` with `ExistingWorkPolicy.KEEP` |
| C5 | At most one notification per calendar day | `last_notified_date` checked and written in the same transaction as posting |
