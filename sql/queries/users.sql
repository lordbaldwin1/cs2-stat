-- name: CreatePlayer :one
INSERT INTO players (steam_id, name, country, faceit_url, avatar, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
ON CONFLICT(steam_id) DO UPDATE SET
  name = excluded.name,
  country = excluded.country,
  faceit_url = excluded.faceit_url,
  avatar = excluded.avatar,
  updated_at = CURRENT_TIMESTAMP
RETURNING steam_id, name, country, faceit_url, avatar, created_at, updated_at;