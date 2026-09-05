CREATE TABLE repositories (
 id bigserial PRIMARY KEY,
 project_id bigint NOT NULL REFERENCES projects ON DELETE CASCADE,
 name text NOT NULL,
 url text NOT NULL,
 branch text NOT NULL DEFAULT 'main',
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (project_id, url, branch)
);
INSERT INTO repositories (project_id, name, url, branch)
 SELECT DISTINCT project_id, repository_url, repository_url, branch FROM components WHERE repository_url <> '';
ALTER TABLE components ADD COLUMN repository_id bigint REFERENCES repositories ON DELETE SET NULL;
UPDATE components c SET repository_id = r.id FROM repositories r
 WHERE r.project_id = c.project_id AND r.url = c.repository_url AND r.branch = c.branch;
ALTER TABLE components DROP COLUMN repository_url, DROP COLUMN branch;
CREATE INDEX components_repository ON components (repository_id);

CREATE TABLE project_locales (
 project_id bigint NOT NULL REFERENCES projects ON DELETE CASCADE,
 locale text NOT NULL REFERENCES locales,
 PRIMARY KEY (project_id, locale)
);
INSERT INTO project_locales (project_id, locale)
 SELECT DISTINCT c.project_id, t.locale FROM translations t JOIN units u ON u.id = t.unit_id JOIN components c ON c.id = u.component_id
 ON CONFLICT DO NOTHING;

CREATE TABLE translation_history (
 id bigserial PRIMARY KEY,
 unit_id bigint NOT NULL REFERENCES units ON DELETE CASCADE,
 locale text NOT NULL,
 value text NOT NULL,
 status text NOT NULL,
 version bigint NOT NULL,
 changed_by bigint REFERENCES users ON DELETE SET NULL,
 changed_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX translation_history_unit ON translation_history (unit_id, locale, id DESC);
CREATE FUNCTION record_translation_history() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO translation_history (unit_id, locale, value, status, version, changed_by, changed_at)
 VALUES (NEW.unit_id, NEW.locale, NEW.value, NEW.status, NEW.version, NEW.updated_by, NEW.updated_at);
 RETURN NEW;
END $$;
CREATE TRIGGER translations_history AFTER INSERT OR UPDATE ON translations
 FOR EACH ROW EXECUTE FUNCTION record_translation_history();
INSERT INTO translation_history (unit_id, locale, value, status, version, changed_by, changed_at)
 SELECT unit_id, locale, value, status, version, updated_by, updated_at FROM translations;
