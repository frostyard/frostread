-- readd's schema, version 2 (docs/adr/0007-*). The blob sweep records when
-- it first found a blob that no row names: a blob goes only after it has
-- been without a reference for the grace period. A row is not a
-- reference, and it has no foreign key: the hash may name a file with no
-- blobs row, such as an article's original.

CREATE TABLE blob_orphans (
  hash  TEXT PRIMARY KEY,
  since TEXT NOT NULL
) STRICT;
