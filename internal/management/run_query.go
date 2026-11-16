package management

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type runPage struct {
	Items    []Run `json:"items"`
	Total    int   `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

func pageParameters(request *http.Request, defaultSize int) (int, int, error) {
	page, size := 1, defaultSize
	var err error
	if raw := request.URL.Query().Get("page"); raw != "" {
		page, err = strconv.Atoi(raw)
		if err != nil {
			return 0, 0, invalid("页码必须为正整数")
		}
	}
	if raw := request.URL.Query().Get("page_size"); raw != "" {
		size, err = strconv.Atoi(raw)
	}
	if err != nil || page < 1 || size < 1 || size > 1000 || page > 1000000 {
		return 0, 0, invalid("分页须使用正整数，page_size 最大1000")
	}
	return page, size, nil
}

func queryRuns(query querier, where, order string, arguments ...any) ([]Run, error) {
	rows, err := query.Query("SELECT body FROM objects WHERE kind='runs' AND ("+where+") "+order, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []Run{}
	for rows.Next() {
		var body []byte
		var run Run
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(body, &run); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func activeRuns(query querier) ([]Run, error) {
	return queryRuns(query, "json_extract(body,'$.status') IN ('queued','running')", "ORDER BY json_extract(body,'$.sequence'),id")
}

func pendingRuns(query querier) ([]Run, error) {
	return queryRuns(query, "json_extract(body,'$.status') IN ('queued','running') OR EXISTS(SELECT 1 FROM json_each(objects.body,'$.deliveries') WHERE json_extract(value,'$.state') IN ('pending','dispatching','running'))", "ORDER BY json_extract(body,'$.sequence'),id")
}

func readRunPage(query querier, taskID string, recycled bool, request *http.Request) (any, error) {
	page, size, err := pageParameters(request, 20)
	if err != nil {
		return nil, err
	}
	conditions := []string{"json_extract(body,'$.task_id')=?"}
	arguments := []any{taskID}
	deleted := "json_extract(body,'$.deleted_at') IS NULL"
	if recycled {
		deleted = "json_extract(body,'$.deleted_at') IS NOT NULL"
	}
	conditions = append(conditions, deleted)
	values := request.URL.Query()
	for _, key := range []string{"status", "trigger"} {
		if value := values.Get(key); value != "" {
			conditions = append(conditions, "json_extract(body,'$."+key+"')=?")
			arguments = append(arguments, value)
		}
	}
	for _, key := range []string{"start", "end"} {
		if value := values.Get(key); value != "" {
			boundary, err := time.Parse(time.RFC3339, value)
			if err != nil {
				return nil, invalid("时间筛选必须使用包含时区的 RFC3339 时间")
			}
			operator := ">="
			if key == "end" {
				operator = "<="
			}
			conditions = append(conditions, "julianday(json_extract(body,'$.queued_at')) "+operator+" julianday(?)")
			arguments = append(arguments, boundary.UTC().Format(time.RFC3339Nano))
		}
	}
	for _, key := range []string{"dns_state", "notification_state"} {
		if state := values.Get(key); state != "" {
			operator := "="
			if key == "notification_state" {
				operator = "<>"
			}
			conditions = append(conditions, "EXISTS(SELECT 1 FROM json_each(objects.body,'$.deliveries') WHERE json_extract(value,'$.kind') "+operator+" 'dns' AND json_extract(value,'$.state')=?)")
			arguments = append(arguments, state)
		}
	}
	where := strings.Join(conditions, " AND ")
	order := "ORDER BY julianday(json_extract(body,'$.queued_at')) DESC,id DESC"
	if values.Get("page") == "" && values.Get("page_size") == "" {
		return queryRuns(query, where, order, arguments...)
	}
	result := runPage{Page: page, PageSize: size}
	if err = query.QueryRow("SELECT COUNT(*) FROM objects WHERE kind='runs' AND "+where, arguments...).Scan(&result.Total); err != nil {
		return nil, err
	}
	result.Page = min(page, max(1, (result.Total+size-1)/size))
	result.Items, err = queryRuns(query, where, order+" LIMIT ? OFFSET ?", append(arguments, size, (result.Page-1)*size)...)
	return result, err
}
