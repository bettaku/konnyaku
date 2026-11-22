-- Import provenance is committed with the catalog, so failed imports never
-- become a baseline for publishing. No second copy of the file is stored.
CREATE TABLE repository_imports (
 component_id bigint NOT NULL,
 locale text NOT NULL,
 repository_id bigint NOT NULL REFERENCES repositories ON DELETE CASCADE,
 path text NOT NULL,
 blob_sha text NOT NULL,
 source_sha text NOT NULL,
 commit_sha text NOT NULL,
 file_pattern text NOT NULL,
 format text NOT NULL,
 synced_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (component_id, locale),
 FOREIGN KEY (component_id, locale) REFERENCES documents ON DELETE CASCADE
);
CREATE INDEX repository_imports_repository ON repository_imports(repository_id);

-- One reviewed, frozen export per job. Persist every remote side-effect checkpoint
-- so HTTP retries and process restarts can find the same branch and PR.
CREATE TABLE repository_publications (
 id bigserial PRIMARY KEY,
 repository_id bigint NOT NULL REFERENCES repositories ON DELETE CASCADE,
 fingerprint text NOT NULL,
 branch text NOT NULL,
 base_sha text NOT NULL,
 tree_sha text NOT NULL,
 title text NOT NULL,
 body text NOT NULL,
 changes jsonb NOT NULL,
 commit_sha text NOT NULL DEFAULT '',
 url text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (repository_id, fingerprint),
 UNIQUE (repository_id, branch)
);
