package management

import (
	"database/sql"
	"errors"
	"sort"
	"strings"

	"github.com/masgzy/CloudflareST/internal/optimizer"
)

func (service *Service) validateTask(query querier, task *Task) error {
	task.Name = strings.TrimSpace(task.Name)
	if task.Name == "" || len([]rune(task.Name)) > 100 {
		return invalid("任务名称必填，最多100字")
	}
	if err := optimizer.Validate(task.Config); err != nil {
		return invalid(err.Error())
	}
	settings, err := get[Settings](query, "settings", "system")
	if err != nil {
		return err
	}
	if _, err = nextSchedule(task.Schedule, service.now(), settings.Timezone); err != nil {
		return err
	}
	seen := map[string]bool{}
	for index := range task.Targets {
		target := &task.Targets[index]
		connection, err := get[Connection](query, "connections", target.ConnectionID)
		if err != nil || connection.Kind != "alidns" {
			return invalid("DNS 目标必须引用有效阿里云连接")
		}
		if target.RecordID == "" || target.Domain == "" || target.RR == "" || (target.Type != "A" && target.Type != "AAAA") {
			return invalid("请选择已有 A 或 AAAA 记录，不能新建或使用任意目标")
		}
		catalog, err := get[Target](query, "target_catalog", targetKey(connection.AccountID, target.RecordID))
		if err != nil || catalog.Domain != target.Domain || catalog.RR != target.RR || catalog.Type != target.Type {
			return invalid("DNS 记录不在已查询目标中，请重新选择现存记录")
		}
		if seen[catalog.TargetID] {
			return invalid("同一个实际 DNS 目标不能重复选择")
		}
		seen[catalog.TargetID] = true
		catalog.ConnectionID = target.ConnectionID
		catalog.LastKnownWrite = nil
		catalog.Protection = nil
		*target = catalog
	}
	if task.DNSAuto && len(task.Targets) == 0 {
		return invalid("开启自动 DNS 前请先明确选择目标记录")
	}
	for kind, channel := range map[string]Channel{"telegram": task.Notifications.Telegram, "wecom": task.Notifications.Wecom} {
		if channel.ConnectionID == "" {
			if channel.Enabled {
				return invalid("开启通知前请选择对应服务连接")
			}
			continue
		}
		connection, err := get[Connection](query, "connections", channel.ConnectionID)
		if err != nil || connection.Kind != kind {
			return invalid("通知渠道与服务连接不匹配")
		}
	}
	return nil
}

func (service *Service) saveTask(tx *sql.Tx, task Task, id string, revision int) (string, any, error) {
	if err := service.validateTask(tx, &task); err != nil {
		return "", nil, err
	}
	task.ConfigurationIssue = ""
	if id == "" {
		task.ID = randomID()
		task.Revision = 1
		task.Archived = false
		task.Schedule.NextRunAt = nil
	} else {
		previous, err := get[Task](tx, "tasks", id)
		if err != nil {
			return "", nil, err
		}
		if revision != previous.Revision {
			return "", nil, &apiError{409, "REVISION_CONFLICT", "任务已被其他页面更新，请重新读取后再保存"}
		}
		if previous.Archived {
			return "", nil, conflict("归档任务需先恢复，恢复后定时默认关闭")
		}
		task.ID = id
		task.Revision = previous.Revision + 1
		task.Archived = false
		if !scheduleChanged(previous.Schedule, task.Schedule) {
			task.Schedule.NextRunAt = previous.Schedule.NextRunAt
		} else {
			task.Schedule.NextRunAt = nil
		}
	}
	if !task.Schedule.Enabled {
		task.Schedule.NextRunAt = nil
	} else if task.Schedule.NextRunAt == nil || id == "" {
		settings, err := get[Settings](tx, "settings", "system")
		if err != nil {
			return "", nil, err
		}
		next, err := nextSchedule(task.Schedule, service.now(), settings.Timezone)
		if err != nil {
			return "", nil, err
		}
		task.Schedule.NextRunAt = &next
	}
	return task.ID, task, put(tx, "tasks", task.ID, task)
}

func taskConnections(task Task) map[string]bool {
	ids := map[string]bool{}
	for _, target := range task.Targets {
		ids[target.ConnectionID] = true
	}
	for _, channel := range []Channel{task.Notifications.Telegram, task.Notifications.Wecom} {
		if channel.ConnectionID != "" {
			ids[channel.ConnectionID] = true
		}
	}
	return ids
}

func connectionReferences(query querier, id string) ([]string, bool, error) {
	references := []string{}
	tasks, err := all[Task](query, "tasks")
	if err != nil {
		return nil, false, err
	}
	for _, task := range tasks {
		if taskConnections(task)[id] {
			references = append(references, task.ID)
		}
	}
	runs, err := all[Run](query, "runs")
	if err != nil {
		return nil, false, err
	}
	busy := false
	for _, run := range runs {
		if active(run.Status) && taskConnections(run.Snapshot)[id] {
			busy = true
		}
		for _, delivery := range run.Deliveries {
			if delivery.ConnectionID == id && (delivery.State == "pending" || delivery.State == "dispatching" || delivery.State == "running") {
				busy = true
			}
		}
	}
	return references, busy, nil
}

func enqueue(query querier, task Task, trigger, operationID string, service *Service) (Run, error) {
	if task.ConfigurationIssue != "" {
		return Run{}, conflict(task.ConfigurationIssue)
	}
	if task.Archived {
		return Run{}, conflict("归档任务不能触发运行")
	}
	runs, err := all[Run](query, "runs")
	if err != nil {
		return Run{}, err
	}
	var sequence int64
	for _, run := range runs {
		if run.TaskID == task.ID && active(run.Status) {
			return Run{}, conflict("任务已在运行或排队，自动收尾结束前不能重复触发")
		}
		if run.Sequence > sequence {
			sequence = run.Sequence
		}
	}
	run := Run{ID: randomID(), TaskID: task.ID, TaskName: task.Name, Trigger: trigger, Status: "queued", Stage: "queued", QueuedAt: service.now(), Snapshot: task, Sequence: sequence + 1, OperationID: operationID, Deliveries: []Delivery{}, Result: optimizer.Result{Candidates: []optimizer.Candidate{}, Sources: []optimizer.SourceResult{}, Budgets: []optimizer.Budget{}}}
	return run, put(query, "runs", run.ID, run)
}

func (service *Service) taskState(tx *sql.Tx, id, action string, enabled bool) (string, any, error) {
	task, err := get[Task](tx, "tasks", id)
	if err != nil {
		return "", nil, err
	}
	switch action {
	case "archive":
		runs, err := all[Run](tx, "runs")
		if err != nil {
			return "", nil, err
		}
		for _, run := range runs {
			if run.TaskID == id && active(run.Status) {
				return "", nil, conflict("任务仍有排队、运行或自动收尾，不能归档")
			}
		}
		task.Archived = true
		task.Schedule.Enabled = false
		task.Schedule.NextRunAt = nil
	case "restore":
		task.Archived = false
		task.Schedule.Enabled = false
		task.Schedule.NextRunAt = nil
	case "schedule-state":
		if enabled && task.ConfigurationIssue != "" {
			return "", nil, conflict(task.ConfigurationIssue)
		}
		if task.Archived {
			return "", nil, conflict("归档任务不能开启定时")
		}
		if task.Schedule.Enabled != enabled {
			task.Schedule.Enabled = enabled
			task.Schedule.NextRunAt = nil
			if enabled {
				settings, err := get[Settings](tx, "settings", "system")
				if err != nil {
					return "", nil, err
				}
				next, err := nextSchedule(task.Schedule, service.now(), settings.Timezone)
				if err != nil {
					return "", nil, err
				}
				task.Schedule.NextRunAt = &next
			}
		}
	default:
		return "", nil, errNotFound
	}
	task.Revision++
	return task.ID, task, put(tx, "tasks", id, task)
}

func (service *Service) updateSettings(tx *sql.Tx, settings Settings) (string, any, error) {
	if settings.HistoryDays < 1 || settings.HistoryDays > 3650 {
		return "", nil, invalid("运行记录保留期须为1到3650天")
	}
	if _, err := nextSchedule(Schedule{Kind: "interval", IntervalMinutes: 1}, service.now(), settings.Timezone); err != nil {
		return "", nil, err
	}
	previous, err := get[Settings](tx, "settings", "system")
	if err != nil {
		return "", nil, err
	}
	if previous.Timezone != settings.Timezone {
		tasks, err := all[Task](tx, "tasks")
		if err != nil {
			return "", nil, err
		}
		for _, task := range tasks {
			if task.Schedule.Enabled && !task.Archived {
				next, err := nextSchedule(task.Schedule, service.now(), settings.Timezone)
				if err != nil {
					return "", nil, err
				}
				task.Schedule.NextRunAt = &next
				task.Revision++
				if err = put(tx, "tasks", task.ID, task); err != nil {
					return "", nil, err
				}
			}
		}
	}
	return "system", settings, put(tx, "settings", "system", settings)
}

func orderedRuns(runs []Run) {
	sort.Slice(runs, func(first, second int) bool {
		if runs[first].Sequence == runs[second].Sequence {
			return runs[first].QueuedAt.Before(runs[second].QueuedAt)
		}
		return runs[first].Sequence < runs[second].Sequence
	})
}

func ignoreNotFound(err error) error {
	if errors.Is(err, errNotFound) {
		return nil
	}
	return err
}
