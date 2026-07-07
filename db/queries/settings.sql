-- name: GetSetting :one
SELECT value FROM settings WHERE key = ? LIMIT 1;

-- name: UpsertSetting :exec
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value;

-- name: ListEnterpriseDomains :many
SELECT * FROM enterprise_domains ORDER BY domain ASC;

-- name: AddEnterpriseDomain :one
INSERT INTO enterprise_domains (domain) VALUES (?)
ON CONFLICT(domain) DO UPDATE SET domain = excluded.domain
RETURNING *;

-- name: RemoveEnterpriseDomain :exec
DELETE FROM enterprise_domains WHERE id = ?;
