-- +goose Up
CREATE TABLE players (
  steam_id UUID PRIMARY KEY,
  name TEXT NOT NULL,
  country TEXT NOT NULL,
  faceit_url TEXT NOT NULL,
  avatar TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE players;