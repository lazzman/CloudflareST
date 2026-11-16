package management

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/masgzy/CloudflareST/internal/optimizer"
	"github.com/masgzy/CloudflareST/internal/safedata"
)

func runTransaction(query querier, change func(querier) error) error {
	database, ok := query.(*sql.DB)
	if !ok {
		return change(query)
	}
	transaction, err := database.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if err = change(transaction); err != nil {
		return err
	}
	return transaction.Commit()
}

func getRunMetadata(query querier, id string) (Run, error) {
	return getRaw[Run](query, "runs", id)
}

func summarizeCandidates(candidates []optimizer.Candidate) *CandidateSummary {
	summary := &CandidateSummary{Total: len(candidates), Families: []FamilySummary{{Family: "ipv4"}, {Family: "ipv6"}}}
	for _, candidate := range candidates {
		index := 0
		if candidate.Family == "ipv6" {
			index = 1
		}
		family := &summary.Families[index]
		family.Count++
		if !candidate.Eligible {
			continue
		}
		family.Eligible++
		if candidate.Rank == 1 {
			best := candidate
			best.Sources = nil
			best.Steps = nil
			family.Best = &best
		}
	}
	return summary
}

func putRun(query querier, id string, run Run) error {
	run.ServerTime = nil
	if safedata.URLHasSecrets(run.Snapshot.Config.DownloadURL) {
		run.Snapshot.Config.DownloadURL = sensitiveURLRemoved
	}
	run.Reason = safeMessage(run.Reason)
	sanitizeResult(&run.Result)
	return runTransaction(query, func(transaction querier) error {
		if run.Result.Candidates != nil {
			run.CandidateSummary = summarizeCandidates(run.Result.Candidates)
			if _, err := transaction.Exec("DELETE FROM run_candidates WHERE run_id=?", id); err != nil {
				return err
			}
			for start := 0; start < len(run.Result.Candidates); start += 200 {
				end := min(start+200, len(run.Result.Candidates))
				values := make([]string, 0, end-start)
				arguments := make([]any, 0, 7*(end-start))
				for index := start; index < end; index++ {
					candidate := run.Result.Candidates[index]
					body, err := json.Marshal(candidate)
					if err != nil {
						return err
					}
					values = append(values, "(?,?,?,?,?,?,?)")
					arguments = append(arguments, id, index, candidate.IP, candidate.Family, candidate.Eligible, candidate.State, body)
				}
				if _, err := transaction.Exec("INSERT INTO run_candidates(run_id,position,ip,family,eligible,state,body) VALUES "+strings.Join(values, ","), arguments...); err != nil {
					return err
				}
			}
			run.Result.Candidates = nil
		}
		return putRaw(transaction, "runs", id, run)
	})
}

func loadCandidates(query querier, runID string) ([]optimizer.Candidate, error) {
	rows, err := query.Query("SELECT body FROM run_candidates WHERE run_id=? ORDER BY position", runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates := []optimizer.Candidate{}
	for rows.Next() {
		var body []byte
		var candidate optimizer.Candidate
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(body, &candidate); err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

type candidatePage struct {
	Items    []optimizer.Candidate `json:"items"`
	Total    int                   `json:"total"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"page_size"`
}

func readCandidatePage(query querier, runID string, page, size int, family, eligibility string, sorting ...string) (candidatePage, error) {
	result := candidatePage{Items: []optimizer.Candidate{}, Page: page, PageSize: size}
	order := "position"
	if len(sorting) > 0 && sorting[0] != "" {
		field := sorting[0]
		if field != "latency_ms" && field != "speed_mb_s" && field != "loss_percent" {
			return result, invalid("排序字段须为延迟、下载速度或失败比例")
		}
		direction := "asc"
		if len(sorting) > 1 && sorting[1] != "" {
			direction = sorting[1]
		}
		if direction != "asc" && direction != "desc" {
			return result, invalid("排序方向须为asc或desc")
		}
		expression := "json_extract(body,'$." + field + "')"
		order = expression + " IS NULL, " + expression + " " + direction
		if field != "latency_ms" {
			order += ", json_extract(body,'$.latency_ms') IS NULL, json_extract(body,'$.latency_ms') ASC"
		}
		if field != "speed_mb_s" {
			order += ", json_extract(body,'$.speed_mb_s') IS NULL, json_extract(body,'$.speed_mb_s') DESC"
		}
		order += ", position"
	}
	where := "run_id=?"
	arguments := []any{runID}
	if family != "" {
		if family != "ipv4" && family != "ipv6" {
			return result, invalid("候选地址族必须为 ipv4 或 ipv6")
		}
		where += " AND family=?"
		arguments = append(arguments, family)
	}
	if eligibility != "" {
		if eligibility != "eligible" && eligibility != "excluded" {
			return result, invalid("候选筛选必须为 eligible 或 excluded")
		}
		where += " AND eligible=?"
		arguments = append(arguments, eligibility == "eligible")
	}
	if err := query.QueryRow("SELECT COUNT(*) FROM run_candidates WHERE "+where, arguments...).Scan(&result.Total); err != nil {
		return result, err
	}
	result.Page = min(page, max(1, (result.Total+size-1)/size))
	arguments = append(arguments, size, (result.Page-1)*size)
	rows, err := query.Query("SELECT body FROM run_candidates WHERE "+where+" ORDER BY "+order+" LIMIT ? OFFSET ?", arguments...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var body []byte
		var candidate optimizer.Candidate
		if err = rows.Scan(&body); err != nil {
			return result, err
		}
		if err = json.Unmarshal(body, &candidate); err != nil {
			return result, err
		}
		result.Items = append(result.Items, candidate)
	}
	return result, rows.Err()
}

func eligibleCandidate(query querier, runID, ip, family string) (bool, error) {
	var count int
	err := query.QueryRow("SELECT COUNT(*) FROM run_candidates WHERE run_id=? AND ip=? AND family=? AND eligible=1", runID, ip, family).Scan(&count)
	return count == 1, err
}

func migrateRunStorage(database *sql.DB) error {
	return runTransaction(database, func(query querier) error {
		var version int
		if err := query.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
			return err
		}
		if _, err := query.Exec(`CREATE TABLE IF NOT EXISTS run_candidates (
run_id TEXT NOT NULL, position INTEGER NOT NULL, ip TEXT NOT NULL, family TEXT NOT NULL,
eligible INTEGER NOT NULL, state TEXT NOT NULL, body BLOB NOT NULL, PRIMARY KEY(run_id,position));
CREATE INDEX IF NOT EXISTS candidate_ip ON run_candidates(run_id,ip,family,eligible);
CREATE INDEX IF NOT EXISTS candidate_filter ON run_candidates(run_id,family,eligible,position);
CREATE INDEX IF NOT EXISTS run_task_time ON objects(json_extract(body,'$.task_id'),json_extract(body,'$.queued_at') DESC,id) WHERE kind='runs';
CREATE INDEX IF NOT EXISTS run_status ON objects(json_extract(body,'$.status')) WHERE kind='runs';`); err != nil {
			return err
		}
		if version >= 2 {
			return nil
		}
		lastID := ""
		for {
			var id string
			var body []byte
			err := query.QueryRow("SELECT id,body FROM objects WHERE kind='runs' AND id>? ORDER BY id LIMIT 1", lastID).Scan(&id, &body)
			if err == sql.ErrNoRows {
				break
			}
			if err != nil {
				return err
			}
			var run Run
			if err = json.Unmarshal(body, &run); err != nil {
				return fmt.Errorf("旧运行记录无法迁移：%w", err)
			}
			if run.Result.Candidates != nil {
				if err = putRun(query, id, run); err != nil {
					return err
				}
			}
			lastID = id
		}
		_, err := query.Exec("PRAGMA user_version=2")
		return err
	})
}
