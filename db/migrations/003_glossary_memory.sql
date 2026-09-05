CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX units_source_trgm ON units USING gin (source gin_trgm_ops);
CREATE TABLE glossary_terms (
 id bigserial PRIMARY KEY,
 project_id bigint NOT NULL REFERENCES projects ON DELETE CASCADE,
 locale text NOT NULL REFERENCES locales,
 term text NOT NULL CHECK (term <> '' AND length(term) <= 200),
 translation text NOT NULL CHECK (length(translation) <= 500),
 note text NOT NULL DEFAULT '' CHECK (length(note) <= 1000),
 updated_by bigint REFERENCES users ON DELETE SET NULL,
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX glossary_terms_unique ON glossary_terms (project_id, locale, lower(term));
