-- name: CreateUser :one
INSERT INTO users (id, created_at, updated_at, email)
VALUES (?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, ?)
RETURNING id, created_at, updated_at, email;