-- name: CreateProject :exec
INSERT INTO projects (id, name, root_path, created_at)
VALUES (?, ?, ?, ?);

-- name: GetProject :one
SELECT id, name, root_path, created_at, claimed_by, claimed_at
FROM projects
WHERE id = ?;

-- name: ListProjects :many
SELECT id, name, root_path, created_at, claimed_by, claimed_at
FROM projects
ORDER BY created_at;

-- name: ResolveProject :one
SELECT id, name, root_path, created_at, claimed_by, claimed_at
FROM projects
WHERE ? = root_path OR ? LIKE root_path || '/%'
ORDER BY LENGTH(root_path) DESC
LIMIT 1;

-- name: ClaimProject :execresult
UPDATE projects
SET claimed_by = ?, claimed_at = ?
WHERE id = ?;

-- name: ReleaseProject :execresult
UPDATE projects
SET claimed_by = '', claimed_at = NULL
WHERE id = ?;
