-- name: ListRunners :many
SELECT * FROM runners ORDER BY name ASC;

-- name: GetRunner :one
SELECT * FROM runners WHERE id = ? LIMIT 1;

-- name: GetRunnerByName :one
SELECT * FROM runners WHERE name = ? LIMIT 1;

-- name: GetRunnerByURLMode :one
SELECT * FROM runners WHERE url = ? AND mode = ? LIMIT 1;

-- name: CreateRunner :one
INSERT INTO runners (
    name, mode, url, dir, pat, max_runners, labels, runner_group
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?
) RETURNING *;

-- name: UpdateRunner :one
UPDATE runners
SET 
    pat = COALESCE(sqlc.narg('pat'), pat),
    max_runners = COALESCE(sqlc.narg('max_runners'), max_runners),
    labels = COALESCE(sqlc.narg('labels'), labels),
    runner_group = COALESCE(sqlc.narg('runner_group'), runner_group)
WHERE id = sqlc.arg('id')
RETURNING *;

-- name: DeleteRunner :exec
DELETE FROM runners WHERE id = ?;

-- name: DeleteRunnerByName :exec
DELETE FROM runners WHERE name = ?;

-- name: IncrementJobsCompleted :exec
UPDATE runners SET jobs_completed = jobs_completed + 1 WHERE name = ?;
