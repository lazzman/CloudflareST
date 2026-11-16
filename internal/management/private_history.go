package management

import (
	"database/sql"
	"encoding/json"

	"github.com/masgzy/CloudflareST/internal/integrations"
	"github.com/masgzy/CloudflareST/internal/optimizer"
	"github.com/masgzy/CloudflareST/internal/safedata"
)

const sensitiveURLRemoved = "[已移除敏感测速地址，请重新配置不含认证参数的文件URL]"

func safeMessage(value string) string { return integrations.Redact(value) }

func sanitizeResult(result *optimizer.Result) {
	result.Error = safeMessage(result.Error)
	for index := range result.Sources {
		result.Sources[index].Error = safeMessage(result.Sources[index].Error)
	}
	for index := range result.Candidates {
		candidate := &result.Candidates[index]
		candidate.Reason = safeMessage(candidate.Reason)
		for step := range candidate.Steps {
			candidate.Steps[step].Reason = safeMessage(candidate.Steps[step].Reason)
		}
	}
}

func compactOperationResult(value any) any {
	if run, ok := value.(Run); ok {
		return map[string]any{"run_id": run.ID, "status": run.Status, "slot_busy": run.SlotBusy}
	}
	if fields, ok := value.(map[string]any); ok {
		if _, snapshot := fields["snapshot"]; snapshot {
			if id, exists := fields["task_id"]; exists && id != nil {
				return map[string]any{"run_id": fields["id"], "status": fields["status"], "slot_busy": fields["slot_busy"]}
			}
		}
	}
	return value
}

func scrubPrivateFields(value any) {
	switch fields := value.(type) {
	case map[string]any:
		for key, child := range fields {
			if text, ok := child.(string); ok {
				if key == "download_url" && safedata.URLHasSecrets(text) {
					fields[key] = sensitiveURLRemoved
				}
				if key == "reason" || key == "error" || key == "message" {
					fields[key] = safeMessage(text)
				}
			} else {
				scrubPrivateFields(child)
			}
		}
	case []any:
		for _, child := range fields {
			scrubPrivateFields(child)
		}
	}
}

func migratePrivateHistory(database *sql.DB) error {
	return runTransaction(database, func(query querier) error {
		var version int
		if err := query.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
			return err
		}
		if version >= 4 {
			return nil
		}
		lastKind, lastID := "", ""
		for {
			var kind, id string
			var body []byte
			err := query.QueryRow("SELECT kind,id,body FROM objects WHERE kind>? OR (kind=? AND id>?) ORDER BY kind,id LIMIT 1", lastKind, lastKind, lastID).Scan(&kind, &id, &body)
			if err == sql.ErrNoRows {
				break
			}
			if err != nil {
				return err
			}
			var value map[string]any
			if err = json.Unmarshal(body, &value); err != nil {
				return err
			}
			if kind == "tasks" {
				if config, ok := value["config"].(map[string]any); ok {
					if address, ok := config["download_url"].(string); ok && safedata.URLHasSecrets(address) {
						value["configuration_issue"] = sensitiveURLRemoved
						if schedule, ok := value["schedule"].(map[string]any); ok {
							schedule["enabled"] = false
							delete(schedule, "next_run_at")
						}
						if revision, ok := value["revision"].(float64); ok {
							value["revision"] = revision + 1
						}
					}
				}
			}
			if kind == "operations" {
				value["result"] = compactOperationResult(value["result"])
			}
			scrubPrivateFields(value)
			if err = putRaw(query, kind, id, value); err != nil {
				return err
			}
			lastKind, lastID = kind, id
		}
		lastRun, lastPosition := "", -1
		for {
			var runID string
			var position int
			var body []byte
			err := query.QueryRow("SELECT run_id,position,body FROM run_candidates WHERE run_id>? OR (run_id=? AND position>?) ORDER BY run_id,position LIMIT 1", lastRun, lastRun, lastPosition).Scan(&runID, &position, &body)
			if err == sql.ErrNoRows {
				break
			}
			if err != nil {
				return err
			}
			{
				var value any
				if err = json.Unmarshal(body, &value); err != nil {
					return err
				}
				scrubPrivateFields(value)
				encoded, err := json.Marshal(value)
				if err != nil {
					return err
				}
				if _, err = query.Exec("UPDATE run_candidates SET body=? WHERE run_id=? AND position=?", encoded, runID, position); err != nil {
					return err
				}
			}
			lastRun, lastPosition = runID, position
		}
		_, err := query.Exec("PRAGMA user_version=4")
		return err
	})
}
