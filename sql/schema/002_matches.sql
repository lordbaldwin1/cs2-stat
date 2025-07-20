-- +goose Up
CREATE TABLE matches (
  match_url TEXT PRIMARY KEY,
  w_avg_leetify_rating REAL NOT NULL,
  w_avg_personal_performance REAL NOT NULL,
  w_avg_hltv_rating REAL NOT NULL,
  w_avg_kd REAL NOT NULL,
  w_avg_aim REAL NOT NULL,
  w_avg_utility REAL NOT NULL,
  l_avg_leetify_rating REAL NOT NULL,
  l_avg_personal_performance REAL NOT NULL,
  l_avg_hltv_rating REAL NOT NULL,
  l_avg_kd REAL NOT NULL,
  l_avg_aim REAL NOT NULL,
  l_avg_utility REAL NOT NULL,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE matches;