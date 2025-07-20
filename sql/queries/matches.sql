-- name: CreateMatch :exec
INSERT INTO matches (
  match_url,
  w_avg_leetify_rating,
  w_avg_personal_performance,
  w_avg_hltv_rating,
  w_avg_kd,
  w_avg_aim,
  w_avg_utility,
  l_avg_leetify_rating,
  l_avg_personal_performance,
  l_avg_hltv_rating,
  l_avg_kd,
  l_avg_aim,
  l_avg_utility,
  created_at,
  updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
ON CONFLICT(match_url) DO UPDATE SET
  match_url = excluded.match_url,
  w_avg_leetify_rating = excluded.w_avg_leetify_rating,
  w_avg_personal_performance = excluded.w_avg_personal_performance,
  w_avg_hltv_rating = excluded.w_avg_hltv_rating,
  w_avg_kd = excluded.w_avg_kd,
  w_avg_aim = excluded.w_avg_aim,
  w_avg_utility = excluded.w_avg_utility,
  l_avg_leetify_rating = excluded.l_avg_leetify_rating,
  l_avg_personal_performance = excluded.l_avg_personal_performance,
  l_avg_hltv_rating = excluded.l_avg_hltv_rating,
  l_avg_kd = excluded.l_avg_kd,
  l_avg_aim = excluded.l_avg_aim,
  l_avg_utility = excluded.l_avg_utility,
  updated_at = CURRENT_TIMESTAMP;