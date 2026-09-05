-- name: CreateUser :one
INSERT INTO users (email,password_hash,name,admin) VALUES ($1,$2,$3,$4) RETURNING *;
-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;
-- name: ListUsers :many
SELECT id,email,name,admin,created_at FROM users ORDER BY id;
-- name: CreateSession :exec
INSERT INTO sessions (token_hash,user_id,expires_at) VALUES ($1,$2,$3);
-- name: SessionUser :one
SELECT u.id,u.email,u.name,u.admin FROM users u JOIN sessions s ON s.user_id=u.id WHERE s.token_hash=$1 AND s.expires_at > now();
-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash=$1;
-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= now();

-- name: ListLocales :many
SELECT * FROM locales ORDER BY code;
-- name: SaveLocale :one
INSERT INTO locales (code,name) VALUES ($1,$2) ON CONFLICT (code) DO UPDATE SET name=excluded.name RETURNING *;
-- name: DeleteLocale :execrows
DELETE FROM locales WHERE code=$1;

-- name: CreateProject :one
INSERT INTO projects (slug,name,source_locale) VALUES ($1,$2,$3) RETURNING *;
-- name: ListProjects :many
SELECT p.* FROM projects p WHERE sqlc.arg(is_admin)::boolean OR EXISTS (SELECT 1 FROM memberships m WHERE m.project_id=p.id AND m.user_id=sqlc.arg(user_id)) ORDER BY p.name;
-- name: GetProject :one
SELECT * FROM projects WHERE id=$1;
-- name: RenameProject :one
UPDATE projects SET name=$2 WHERE id=$1 RETURNING *;
-- name: DeleteProject :execrows
DELETE FROM projects WHERE id=$1;
-- name: ProjectLocales :many
SELECT pl.locale AS code, l.name FROM project_locales pl JOIN locales l ON l.code=pl.locale WHERE pl.project_id=$1 ORDER BY pl.locale;
-- name: AddProjectLocale :exec
INSERT INTO project_locales (project_id,locale) VALUES ($1,$2) ON CONFLICT DO NOTHING;
-- name: RemoveProjectLocale :exec
DELETE FROM project_locales WHERE project_id=$1 AND locale=$2;

-- name: GetRole :one
SELECT role FROM memberships WHERE project_id=$1 AND user_id=$2;
-- name: SaveMember :exec
INSERT INTO memberships (project_id,user_id,role) VALUES ($1,$2,$3) ON CONFLICT (project_id,user_id) DO UPDATE SET role=excluded.role;
-- name: ListMembers :many
SELECT m.user_id,u.email,u.name,m.role FROM memberships m JOIN users u ON u.id=m.user_id WHERE project_id=$1 ORDER BY u.email;
-- name: DeleteMember :exec
DELETE FROM memberships WHERE project_id=$1 AND user_id=$2;

-- name: CreateRepository :one
INSERT INTO repositories (project_id,name,url,branch) VALUES ($1,$2,$3,$4) RETURNING *;
-- name: ListRepositories :many
SELECT * FROM repositories WHERE project_id=$1 ORDER BY name;
-- name: GetRepository :one
SELECT * FROM repositories WHERE id=$1;
-- name: DeleteRepository :execrows
DELETE FROM repositories WHERE id=$1;
-- name: RepositoriesByURL :many
SELECT * FROM repositories WHERE url=$1 ORDER BY id;
-- name: RepositoryComponents :many
SELECT * FROM components WHERE repository_id=$1 ORDER BY id;

-- name: CreateComponent :one
INSERT INTO components (project_id,slug,name,format,repository_id,file_pattern) VALUES ($1,$2,$3,$4,$5,$6) RETURNING *;
-- name: ListComponents :many
SELECT * FROM components WHERE project_id=$1 ORDER BY name;
-- name: GetComponent :one
SELECT * FROM components WHERE id=$1;
-- name: DeleteComponent :execrows
DELETE FROM components WHERE id=$1;
-- name: UpdateComponent :one
UPDATE components SET name=$2,repository_id=$3,file_pattern=$4 WHERE id=$1 RETURNING *;

-- name: SaveDocument :exec
INSERT INTO documents (component_id,locale,content) VALUES ($1,$2,$3) ON CONFLICT (component_id,locale) DO UPDATE SET content=excluded.content;
-- name: GetDocument :one
SELECT * FROM documents WHERE component_id=$1 AND locale=$2;
-- name: UpsertUnit :one
INSERT INTO units (component_id,key,source) VALUES ($1,$2,$3) ON CONFLICT (component_id,key) DO UPDATE SET source=excluded.source RETURNING *;
-- name: GetUnit :one
SELECT * FROM units WHERE id=$1;
-- name: FindUnit :one
SELECT * FROM units WHERE component_id=$1 AND key=$2;
-- name: ListUnits :many
SELECT u.id,u.key,u.source,coalesce(t.value,'')::text AS value,coalesce(t.status,'untranslated')::text AS status,coalesce(t.version,0)::bigint AS version,t.updated_at
FROM units u LEFT JOIN translations t ON t.unit_id=u.id AND t.locale=sqlc.arg(locale)
WHERE u.component_id=sqlc.arg(component_id)
 AND (sqlc.arg(query)::text = '' OR u.key ILIKE sqlc.arg(query) OR u.source ILIKE sqlc.arg(query) OR t.value ILIKE sqlc.arg(query))
 AND (sqlc.arg(status)::text = '' OR coalesce(t.status,'untranslated') = sqlc.arg(status))
ORDER BY u.key LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);
-- name: CountUnits :one
SELECT count(*)::bigint FROM units u LEFT JOIN translations t ON t.unit_id=u.id AND t.locale=sqlc.arg(locale)
WHERE u.component_id=sqlc.arg(component_id)
 AND (sqlc.arg(query)::text = '' OR u.key ILIKE sqlc.arg(query) OR u.source ILIKE sqlc.arg(query) OR t.value ILIKE sqlc.arg(query))
 AND (sqlc.arg(status)::text = '' OR coalesce(t.status,'untranslated') = sqlc.arg(status));
-- name: SaveTranslation :one
INSERT INTO translations (unit_id,locale,value,status,updated_by)
SELECT sqlc.arg(unit_id),sqlc.arg(locale),sqlc.arg(value),sqlc.arg(status),sqlc.narg(updated_by) WHERE sqlc.arg(expected_version)::bigint=0
ON CONFLICT (unit_id,locale) DO NOTHING RETURNING *;
-- name: UpdateTranslation :one
UPDATE translations SET value=sqlc.arg(value),status=sqlc.arg(status),version=version+1,updated_by=sqlc.narg(updated_by),updated_at=now()
WHERE unit_id=sqlc.arg(unit_id) AND locale=sqlc.arg(locale) AND version=sqlc.arg(expected_version) RETURNING *;
-- name: ImportTranslation :exec
INSERT INTO translations (unit_id,locale,value,updated_by) VALUES ($1,$2,$3,$4)
ON CONFLICT (unit_id,locale) DO UPDATE SET value=excluded.value,status='translated',version=translations.version+1,updated_by=excluded.updated_by,updated_at=now()
WHERE translations.value IS DISTINCT FROM excluded.value;
-- name: MarkNeedsReview :exec
UPDATE translations SET status='needs_review',version=version+1,updated_by=NULL,updated_at=now() WHERE unit_id=$1 AND status <> 'needs_review';

-- name: ComponentStats :many
SELECT pl.locale,
 count(u.id)::bigint AS total,
 count(t.unit_id) FILTER (WHERE t.status IN ('translated','reviewed'))::bigint AS translated,
 count(t.unit_id) FILTER (WHERE t.status = 'reviewed')::bigint AS reviewed,
 count(t.unit_id) FILTER (WHERE t.status = 'needs_review')::bigint AS needs_review
FROM components c JOIN project_locales pl ON pl.project_id=c.project_id
LEFT JOIN units u ON u.component_id=c.id
LEFT JOIN translations t ON t.unit_id=u.id AND t.locale=pl.locale
WHERE c.id=$1 GROUP BY pl.locale ORDER BY pl.locale;
-- name: ProjectStats :many
SELECT c.id AS component_id, pl.locale,
 count(u.id)::bigint AS total,
 count(t.unit_id) FILTER (WHERE t.status IN ('translated','reviewed'))::bigint AS translated,
 count(t.unit_id) FILTER (WHERE t.status = 'reviewed')::bigint AS reviewed,
 count(t.unit_id) FILTER (WHERE t.status = 'needs_review')::bigint AS needs_review
FROM components c JOIN project_locales pl ON pl.project_id=c.project_id
LEFT JOIN units u ON u.component_id=c.id
LEFT JOIN translations t ON t.unit_id=u.id AND t.locale=pl.locale
WHERE c.project_id=$1 GROUP BY c.id, pl.locale ORDER BY c.id, pl.locale;
-- name: UnitHistory :many
SELECT h.id,h.value,h.status,h.version,h.changed_at,h.changed_by,coalesce(u.name,'')::text AS changed_by_name
FROM translation_history h LEFT JOIN users u ON u.id=h.changed_by
WHERE h.unit_id=$1 AND h.locale=$2 ORDER BY h.id DESC LIMIT 50;
-- name: ComponentHistory :many
SELECT h.id,h.unit_id,un.key,h.locale,h.value,h.status,h.version,h.changed_at,h.changed_by,coalesce(u.name,'')::text AS changed_by_name
FROM translation_history h JOIN units un ON un.id=h.unit_id LEFT JOIN users u ON u.id=h.changed_by
WHERE un.component_id=sqlc.arg(component_id) AND (sqlc.arg(locale)::text = '' OR h.locale=sqlc.arg(locale)) ORDER BY h.id DESC LIMIT 100;
-- name: ProjectHistory :many
SELECT h.id,h.unit_id,un.key,un.component_id,c.name AS component_name,h.locale,h.value,h.status,h.version,h.changed_at,h.changed_by,coalesce(u.name,'')::text AS changed_by_name
FROM translation_history h JOIN units un ON un.id=h.unit_id JOIN components c ON c.id=un.component_id LEFT JOIN users u ON u.id=h.changed_by
WHERE c.project_id=$1 ORDER BY h.id DESC LIMIT 100;

-- name: EnqueueDelivery :execrows
INSERT INTO webhook_deliveries (delivery_id,repository_url,ref) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING;
-- name: ClaimDelivery :one
SELECT * FROM webhook_deliveries WHERE status='pending' ORDER BY received_at FOR UPDATE SKIP LOCKED LIMIT 1;
-- name: FinishDelivery :exec
UPDATE webhook_deliveries SET status=$2,error=$3 WHERE delivery_id=$1;
-- name: ListDeliveries :many
SELECT * FROM webhook_deliveries ORDER BY received_at DESC LIMIT 100;
-- name: RetryDelivery :execrows
UPDATE webhook_deliveries SET status='pending',error='' WHERE delivery_id=$1 AND status='failed';
