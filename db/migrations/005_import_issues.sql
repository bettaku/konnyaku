-- Keys found in translation files that the source catalog does not contain.
-- Replaced on every import of the same component and locale; rows disappear
-- once the source gains the key or a manager dismisses them.
CREATE TABLE import_issues (
 component_id bigint NOT NULL REFERENCES components ON DELETE CASCADE,
 locale text NOT NULL,
 key text NOT NULL,
 value text NOT NULL,
 seen_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (component_id, locale, key)
);
