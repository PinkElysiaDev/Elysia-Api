-- name: ListGroups :many
SELECT id, name, enabled, strategy, max_retries, retry_interval,
       max_concurrency, daily_limit_max_requests, daily_limit_max_tokens,
       type, max_tokens, vision_capable, tools_capable
FROM model_groups ORDER BY name;

-- name: ListGroupMembers :many
SELECT group_id, model_id, source_id
FROM model_group_models ORDER BY group_id, position;

-- name: GetSetting :one
SELECT value FROM settings WHERE key = ?;

-- name: SetSetting :exec
INSERT INTO settings(key, value, updated_at) VALUES(?, ?, ?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at;
