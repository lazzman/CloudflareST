package management

import (
	"encoding/json"
	"time"
)

type strategyRun struct {
	ID       string     `json:"id"`
	TaskID   string     `json:"task_id"`
	Status   string     `json:"status"`
	QueuedAt time.Time  `json:"queued_at"`
	EndedAt  *time.Time `json:"ended_at,omitempty"`
}
type strategyTask struct {
	Task
	LatestRun *strategyRun `json:"latest_run"`
}

func strategyTasks(query querier, tasks []Task) ([]strategyTask, error) {
	rows, err := query.Query(`SELECT body FROM (SELECT body,ROW_NUMBER() OVER (PARTITION BY json_extract(body,'$.task_id') ORDER BY julianday(json_extract(body,'$.queued_at')) DESC,id DESC) AS ordinal FROM objects WHERE kind='runs' AND json_extract(body,'$.deleted_at') IS NULL) WHERE ordinal=1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	latest := map[string]*strategyRun{}
	for rows.Next() {
		var body []byte
		var run strategyRun
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(body, &run); err != nil {
			return nil, err
		}
		latest[run.TaskID] = &run
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	result := make([]strategyTask, 0, len(tasks))
	for _, task := range tasks {
		result = append(result, strategyTask{Task: task, LatestRun: latest[task.ID]})
	}
	return result, nil
}
