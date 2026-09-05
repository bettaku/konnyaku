CREATE TABLE users (
 id bigserial PRIMARY KEY,
 email text NOT NULL UNIQUE CHECK (email = lower(email)),
 password_hash text NOT NULL,
 name text NOT NULL,
 admin boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE sessions (
 token_hash text PRIMARY KEY,
 user_id bigint NOT NULL REFERENCES users ON DELETE CASCADE,
 expires_at timestamptz NOT NULL
);
CREATE INDEX sessions_expiry ON sessions(expires_at);
CREATE TABLE locales (
 code text PRIMARY KEY,
 name text NOT NULL
);
CREATE TABLE projects (
 id bigserial PRIMARY KEY,
 slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9_-]{0,63}$'),
 name text NOT NULL,
 source_locale text NOT NULL REFERENCES locales,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE memberships (
 project_id bigint NOT NULL REFERENCES projects ON DELETE CASCADE,
 user_id bigint NOT NULL REFERENCES users ON DELETE CASCADE,
 role text NOT NULL CHECK (role IN ('viewer', 'translator', 'manager')),
 PRIMARY KEY (project_id, user_id)
);
CREATE TABLE components (
 id bigserial PRIMARY KEY,
 project_id bigint NOT NULL REFERENCES projects ON DELETE CASCADE,
 slug text NOT NULL CHECK (slug ~ '^[a-z0-9][a-z0-9_-]{0,63}$'),
 name text NOT NULL,
 format text NOT NULL CHECK (format IN ('json','yaml','po','android')),
 repository_url text NOT NULL DEFAULT '',
 branch text NOT NULL DEFAULT 'main',
 file_pattern text NOT NULL DEFAULT 'locales/{locale}.json',
 UNIQUE (project_id, slug)
);
CREATE TABLE documents (
 component_id bigint NOT NULL REFERENCES components ON DELETE CASCADE,
 locale text NOT NULL REFERENCES locales,
 content bytea NOT NULL,
 PRIMARY KEY(component_id, locale)
);
CREATE TABLE units (
 id bigserial PRIMARY KEY,
 component_id bigint NOT NULL REFERENCES components ON DELETE CASCADE,
 key text NOT NULL,
 source text NOT NULL,
 UNIQUE(component_id, key)
);
CREATE TABLE translations (
 unit_id bigint NOT NULL REFERENCES units ON DELETE CASCADE,
 locale text NOT NULL REFERENCES locales,
 value text NOT NULL,
 status text NOT NULL DEFAULT 'translated' CHECK (status IN ('translated','reviewed','needs_review')),
 version bigint NOT NULL DEFAULT 1,
 updated_by bigint REFERENCES users ON DELETE SET NULL,
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(unit_id, locale)
);
CREATE TABLE webhook_deliveries (
 delivery_id text PRIMARY KEY,
 received_at timestamptz NOT NULL DEFAULT now(),
 repository_url text NOT NULL,
 ref text NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','done','failed')),
 error text NOT NULL DEFAULT ''
);
