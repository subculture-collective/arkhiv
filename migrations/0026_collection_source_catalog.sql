-- An engine collection imports its source catalog from the Internet Archive
-- collection with the same identifier. A nonempty source_collection names a
-- different source collection, so a second engine collection, such as a
-- local_preserve copy of a source-streaming collection, can import the same
-- source items under its own policy, budget and reserve. The empty default
-- keeps every existing collection unchanged. Democracy Now! stays
-- source-streaming only.
ALTER TABLE collections ADD COLUMN source_collection text NOT NULL DEFAULT '';
ALTER TABLE collections ADD CONSTRAINT source_collection_identity CHECK (
 source_collection='' OR (
  source_collection ~ '^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,200}$'
  AND source_collection<>id
  AND (source_collection<>'democracy_now_vid' OR policy='source_stream')));
