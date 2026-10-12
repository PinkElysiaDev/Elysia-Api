package storage

import "database/sql"

func decodeUsageDayRows(rows *sql.Rows) ([]usageDayRow, error) {
	defer rows.Close()
	out := []usageDayRow{}
	for rows.Next() {
		var r usageDayRow
		if err := rows.Scan(&r.dayKey, &r.model, &r.requests, &r.success, &r.inputTokens, &r.outputTokens, &r.cacheHitTokens, &r.cacheCreationTokens, &r.totalTokens); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func decodeUsageModelRows(rows *sql.Rows) ([]usageModelRow, error) {
	defer rows.Close()
	out := []usageModelRow{}
	for rows.Next() {
		var r usageModelRow
		if err := rows.Scan(&r.model, &r.requests, &r.failed, &r.tokens); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
