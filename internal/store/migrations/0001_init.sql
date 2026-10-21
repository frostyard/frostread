-- readd's schema, version 1 (docs/specs/api.md, State). Times are UTC in
-- the store's fixed width, so they sort as text. Items and sources are
-- STRICT tables: a wrong type is a bug, not a coercion.

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
) STRICT;

CREATE TABLE sources (
  id            INTEGER PRIMARY KEY,
  url           TEXT NOT NULL UNIQUE,
  site_url      TEXT NOT NULL DEFAULT '',
  title         TEXT NOT NULL DEFAULT '',
  etag          TEXT NOT NULL DEFAULT '',
  last_modified TEXT NOT NULL DEFAULT '',
  polled_at     TEXT,
  next_poll_at  TEXT NOT NULL,
  error_count   INTEGER NOT NULL DEFAULT 0,
  last_error    TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL
) STRICT;

CREATE TABLE items (
  id            INTEGER PRIMARY KEY,
  source_id     INTEGER REFERENCES sources (id) ON DELETE CASCADE,
  guid          TEXT NOT NULL DEFAULT '',
  url           TEXT NOT NULL DEFAULT '',
  title         TEXT NOT NULL DEFAULT '',
  author        TEXT NOT NULL DEFAULT '',
  published_at  TEXT NOT NULL,
  fetched_at    TEXT NOT NULL,
  summary_html  TEXT NOT NULL DEFAULT '',
  content_html  TEXT NOT NULL DEFAULT '',
  text          TEXT NOT NULL DEFAULT '',
  original_hash TEXT NOT NULL DEFAULT '',
  extract_state TEXT NOT NULL DEFAULT 'pending'
                CHECK (extract_state IN ('pending', 'done', 'failed')),
  extract_error TEXT NOT NULL DEFAULT '',
  read_at       TEXT,
  starred       INTEGER NOT NULL DEFAULT 0,
  archived      INTEGER NOT NULL DEFAULT 0,
  saved         INTEGER NOT NULL DEFAULT 0,
  note_path     TEXT,
  created_at    TEXT NOT NULL,
  UNIQUE (source_id, guid)
) STRICT;

CREATE INDEX items_published ON items (published_at DESC, id DESC);
CREATE INDEX items_source ON items (source_id, published_at DESC, id DESC);
CREATE INDEX items_read ON items (read_at, archived);
-- A saved link is idempotent by URL: saving it twice returns the same
-- item. Feed items carry saved = 0, so a feed's item and a saved link of
-- the same URL are two items.
CREATE UNIQUE INDEX items_saved_url ON items (url) WHERE saved = 1;

CREATE TABLE blobs (
  hash       TEXT PRIMARY KEY,
  mime       TEXT NOT NULL,
  size       INTEGER NOT NULL,
  created_at TEXT NOT NULL
) STRICT;

CREATE TABLE item_blobs (
  item_id INTEGER NOT NULL REFERENCES items (id) ON DELETE CASCADE,
  hash    TEXT NOT NULL,
  PRIMARY KEY (item_id, hash)
) STRICT;

-- Full text over a title, an author and an article's text. It is an
-- external-content table: items holds the words, and the triggers keep the
-- index in step, so rowid = items.id.
CREATE VIRTUAL TABLE items_fts USING fts5 (
  title, author, text,
  content = 'items', content_rowid = 'id',
  tokenize = 'unicode61 remove_diacritics 2', prefix = '2 3'
);

CREATE TRIGGER items_fts_ai AFTER INSERT ON items BEGIN
  INSERT INTO items_fts (rowid, title, author, text) VALUES (new.id, new.title, new.author, new.text);
END;

CREATE TRIGGER items_fts_ad AFTER DELETE ON items BEGIN
  INSERT INTO items_fts (items_fts, rowid, title, author, text) VALUES ('delete', old.id, old.title, old.author, old.text);
END;

CREATE TRIGGER items_fts_au AFTER UPDATE ON items BEGIN
  INSERT INTO items_fts (items_fts, rowid, title, author, text) VALUES ('delete', old.id, old.title, old.author, old.text);
  INSERT INTO items_fts (rowid, title, author, text) VALUES (new.id, new.title, new.author, new.text);
END;
