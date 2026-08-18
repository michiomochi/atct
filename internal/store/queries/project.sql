-- name: CreateProject :exec
INSERT INTO projects (id, name, root_path, created_at)
VALUES (?, ?, ?, ?);

-- name: ListProjects :many
SELECT id, name, root_path, created_at
FROM projects
ORDER BY created_at;

-- name: ResolveProject :one
SELECT id, name, root_path, created_at
FROM projects
WHERE ? = root_path OR ? LIKE root_path || '/%'
ORDER BY LENGTH(root_path) DESC
LIMIT 1;
