-- 0001_init.sql — initial schema, verbatim from docs/03-DATA-MODEL.md §1.1.
--
-- Forward-only. A later migration may never drop or retype a column an older
-- binary reads: podman auto-update (D-35) can put the previous binary back in
-- front of this schema at any moment (03-DATA-MODEL.md §1.3).

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
