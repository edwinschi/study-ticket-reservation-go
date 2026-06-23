-- name: CreateVisitorSession :one
INSERT INTO visitor_sessions (
    anonymous_token_hash,
    last_seen_at,
    expires_at
)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetVisitorSessionByID :one
SELECT *
FROM visitor_sessions
WHERE id = $1;

-- name: GetVisitorSessionByTokenHash :one
SELECT *
FROM visitor_sessions
WHERE anonymous_token_hash = $1;

-- name: AttachUserToVisitorSession :one
UPDATE visitor_sessions
SET
    user_id = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: TouchVisitorSession :one
UPDATE visitor_sessions
SET
    last_seen_at = now(),
    updated_at = now()
WHERE id = $1
RETURNING *;

