-- readd's schema, version 3 (docs/specs/api.md, Folders). A source can be
-- in one folder, which is one level: a name on the source, "" for no
-- folder. The folder is the owner's, given by sources.setFolder, by an
-- OPML import's folder outlines, or by neither.

ALTER TABLE sources ADD COLUMN folder TEXT NOT NULL DEFAULT '';
