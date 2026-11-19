package management

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/masgzy/CloudflareST/internal/optimizer"
)

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeError(response http.ResponseWriter, err error) {
	var failure *apiError
	if errors.As(err, &failure) {
		writeJSON(response, failure.Status, failure)
		return
	}
	if errors.Is(err, errNotFound) {
		writeJSON(response, 404, &apiError{404, "not_found", "对象不存在或已被清理"})
		return
	}
	writeJSON(response, 500, &apiError{500, "internal_error", "服务处理失败；系统会自动查询上次结果，仍未确认时请核对实际对象，勿换号重复提交"})
}

func decodeJSON(response http.ResponseWriter, request *http.Request, value any) error {
	if !strings.HasPrefix(strings.ToLower(request.Header.Get("Content-Type")), "application/json") {
		return invalid("请求正文必须使用 application/json")
	}
	request.Body = http.MaxBytesReader(response, request.Body, 4<<20)
	decoder := json.NewDecoder(request.Body)
	if err := decoder.Decode(value); err != nil {
		return invalid("JSON 正文无效或超过4MB限制")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return invalid("JSON 正文包含多余内容")
	}
	return nil
}

func (service *Service) Handler() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("X-Frame-Options", "DENY")
		response.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'")
		if request.URL.Path == "/healthz" {
			if service.ctx.Err() != nil {
				writeJSON(response, 503, map[string]string{"status": "stopping"})
			} else {
				writeJSON(response, 200, map[string]string{"status": "ok"})
			}
			return
		}
		if !strings.HasPrefix(request.URL.Path, "/api/") {
			service.serveWeb(response, request)
			return
		}
		response.Header().Set("Cache-Control", "no-store")
		if request.URL.Path == "/api/v1/session" {
			service.handleSession(response, request)
			return
		}
		service.mu.Lock()
		session, err := service.session(request)
		service.mu.Unlock()
		if err != nil {
			writeError(response, &apiError{401, "unauthenticated", "需要管理密码认证，请登录后继续；后台执行不受影响"})
			return
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead && !validCSRF(request, session.CSRF) {
			writeError(response, &apiError{403, "csrf_invalid", "会话校验失败，请重新读取页面再操作"})
			return
		}
		if request.Method == http.MethodGet {
			service.readAPI(response, request)
			return
		}
		service.writeAPI(response, request)
	})
}

func (service *Service) readAPI(response http.ResponseWriter, request *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/"), "/"), "/")
	if len(parts) >= 3 && parts[0] == "connections" {
		service.readConnection(response, request, parts)
		return
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if len(parts) == 2 && parts[0] == "operations" {
		if err := service.settleOperations(); err != nil {
			writeError(response, err)
			return
		}
	}
	var value any
	var err error
	switch {
	case len(parts) == 1 && parts[0] == "defaults":
		settings, readErr := get[Settings](service.db, "settings", "system")
		err = readErr
		value = map[string]any{"config": optimizer.DefaultConfig(), "timezone": settings.Timezone, "history_days": settings.HistoryDays}
	case len(parts) == 1 && parts[0] == "settings":
		settings, readErr := get[Settings](service.db, "settings", "system")
		err = readErr
		available, message := optimizer.CheckICMP()
		value = map[string]any{"timezone": settings.Timezone, "history_days": settings.HistoryDays, "password_source": service.passwordSource, "icmp": map[string]any{"available": available, "message": message}}
	case len(parts) == 1 && parts[0] == "tasks":
		var tasks []Task
		tasks, err = all[Task](service.db, "tasks")
		for index := range tasks {
			for targetIndex := range tasks[index].Targets {
				if metadataErr := service.appendTargetMetadata(service.db, &tasks[index].Targets[targetIndex]); metadataErr != nil {
					err = metadataErr
				}
			}
		}
		sort.Slice(tasks, func(first, second int) bool { return tasks[first].Name < tasks[second].Name })
		value = tasks
		if request.URL.Query().Get("view") == "strategies" && err == nil {
			value, err = strategyTasks(service.db, tasks)
		}
	case len(parts) == 2 && parts[0] == "tasks":
		var task Task
		task, err = get[Task](service.db, "tasks", parts[1])
		for index := range task.Targets {
			if metadataErr := service.appendTargetMetadata(service.db, &task.Targets[index]); metadataErr != nil {
				err = metadataErr
			}
		}
		value = task
	case len(parts) == 1 && parts[0] == "connections":
		var connections []Connection
		connections, err = all[Connection](service.db, "connections")
		for index := range connections {
			connections[index].Ciphertext = ""
			refs, _, refErr := connectionReferences(service.db, connections[index].ID)
			if refErr != nil {
				err = refErr
			}
			connections[index].ReferencedBy = refs
		}
		value = connections
	case len(parts) == 2 && parts[0] == "connections":
		connection, readErr := get[Connection](service.db, "connections", parts[1])
		err = readErr
		if err == nil {
			connection.ReferencedBy, _, err = connectionReferences(service.db, connection.ID)
			connection.Ciphertext = ""
		}
		value = connection
	case len(parts) == 3 && parts[0] == "dns-targets" && parts[2] == "auto-write-protection":
		value, err = get[Protection](service.db, "dns_protection", parts[1])
	case len(parts) == 1 && parts[0] == "queue":
		var runs []Run
		runs, err = activeRuns(service.db)
		orderedRuns(runs)
		var current *Run
		waiting := []Run{}
		for index := range runs {
			if runs[index].Status == "running" {
				current = &runs[index]
			} else if runs[index].Status == "queued" {
				waiting = append(waiting, runs[index])
			}
		}
		value = map[string]any{"current": current, "waiting": waiting, "server_time": service.now()}
	case len(parts) == 2 && parts[0] == "operations":
		operation, readErr := get[Operation](service.db, "operations", parts[1])
		err = readErr
		operation.RequestHash = ""
		if errors.Is(err, errNotFound) {
			err = &apiError{404, "operation_unknown", "查不到原操作收据，不能认定尚未执行；请核查对象，不要自动换号重发"}
		}
		if err == nil {
			err = service.attachRecovery(&operation)
		}
		if err == nil && operation.State == "expired" && operation.Recovery == nil {
			writeJSON(response, 410, map[string]any{"code": "operation_expired", "message": "原收据已过期，不能认定未执行；请人工核对", "operation_id": operation.ID, "action": recoverableAction(operation.Action)})
			return
		}
		value = operation
	case len(parts) >= 3 && parts[0] == "tasks" && (parts[2] == "runs" || parts[2] == "recycle-bin"):
		if len(parts) >= 4 {
			run, readErr := getRunMetadata(service.db, parts[3])
			err = readErr
			if err == nil && (run.TaskID != parts[1] || run.DeletedAt != nil) {
				err = errNotFound
			}
			if err == nil {
				err = service.enrichRun(&run)
			}
			if err != nil {
				writeError(response, err)
				return
			}
			run.ServerTime = timePointer(service.now())
			value = run
			if len(parts) == 4 && request.URL.Query().Get("view") != "summary" && err == nil {
				run.Result.Candidates, err = loadCandidates(service.db, run.ID)
				value = run
			}
			if len(parts) == 5 {
				switch parts[4] {
				case "progress":
					value = map[string]any{"id": run.ID, "status": run.Status, "stage": run.Stage, "slot_busy": run.SlotBusy, "progress": run.Progress, "stage_progress": run.StageProgress, "progress_revision": run.ProgressRevision, "result_status": run.Result.Status, "candidate_summary": run.CandidateSummary, "deliveries": run.Deliveries, "server_time": service.now(), "updated_at": run.UpdatedAt}
				case "snapshot":
					value = run.Snapshot
				case "candidates":
					page, size, pageErr := pageParameters(request, 50)
					if pageErr != nil {
						err = pageErr
						break
					}
					value, err = readCandidatePage(service.db, run.ID, page, size, request.URL.Query().Get("family"), request.URL.Query().Get("eligibility"), request.URL.Query().Get("sort"), request.URL.Query().Get("direction"))
				default:
					err = errNotFound
				}
			} else if len(parts) != 4 {
				err = errNotFound
			}
		} else {
			value, err = readRunPage(service.db, parts[1], parts[2] == "recycle-bin", request)
		}
	case len(parts) == 2 && parts[0] == "runs":
		run, readErr := get[Run](service.db, "runs", parts[1])
		err = readErr
		if run.DeletedAt != nil {
			err = errNotFound
		}
		if err == nil {
			err = service.enrichRun(&run)
		}
		value = run
	case len(parts) == 3 && parts[0] == "runs" && parts[2] == "deliveries":
		run, readErr := getRunMetadata(service.db, parts[1])
		err = readErr
		if run.DeletedAt != nil {
			err = errNotFound
		}
		if err == nil {
			err = service.enrichRun(&run)
		}
		value = run.Deliveries
	case len(parts) == 1 && parts[0] == "operation-logs":
		var entries []Log
		entries, err = all[Log](service.db, "logs")
		filtered := []Log{}
		for _, entry := range entries {
			matches, filterErr := timeMatches(entry.At, request)
			if filterErr != nil {
				err = filterErr
				break
			}
			query := request.URL.Query()
			if matches && (query.Get("action") == "" || strings.Contains(entry.Action, query.Get("action"))) && (query.Get("object") == "" || strings.Contains(entry.Object, query.Get("object"))) && (query.Get("result") == "" || entry.Result == query.Get("result")) {
				filtered = append(filtered, entry)
			}
		}
		sort.Slice(filtered, func(first, second int) bool { return filtered[first].At.After(filtered[second].At) })
		value = filtered
	default:
		err = errNotFound
	}
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, 200, value)
}

func (service *Service) enrichRun(run *Run) error {
	for index := range run.Snapshot.Targets {
		if err := service.appendTargetMetadata(service.db, &run.Snapshot.Targets[index]); err != nil {
			return err
		}
	}
	for index := range run.Deliveries {
		if run.Deliveries[index].Target != nil {
			if err := service.appendTargetMetadata(service.db, run.Deliveries[index].Target); err != nil {
				return err
			}
		}
	}
	return nil
}

func timeMatches(at time.Time, request *http.Request) (bool, error) {
	for _, key := range []string{"start", "end"} {
		if raw := request.URL.Query().Get(key); raw != "" {
			bound, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return false, invalid("时间筛选必须使用包含时区的 RFC3339 时间")
			}
			if key == "start" && at.Before(bound) || key == "end" && at.After(bound) {
				return false, nil
			}
		}
	}
	return true, nil
}

func runMatches(run Run, request *http.Request) (bool, error) {
	matched, err := timeMatches(run.QueuedAt, request)
	if err != nil || !matched {
		return matched, err
	}
	query := request.URL.Query()
	if query.Get("status") != "" && query.Get("status") != run.Status || query.Get("trigger") != "" && query.Get("trigger") != run.Trigger {
		return false, nil
	}
	for _, filter := range []struct {
		key string
		dns bool
	}{{"dns_state", true}, {"notification_state", false}} {
		if state := query.Get(filter.key); state != "" {
			found := false
			for _, delivery := range run.Deliveries {
				if (delivery.Kind == "dns") == filter.dns && delivery.State == state {
					found = true
				}
			}
			if !found {
				return false, nil
			}
		}
	}
	return true, nil
}

func (service *Service) writeAPI(response http.ResponseWriter, request *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/"), "/"), "/")
	input := struct {
		OperationID string             `json:"operation_id"`
		Task        Task               `json:"task"`
		Enabled     bool               `json:"enabled"`
		Timezone    string             `json:"timezone"`
		HistoryDays int                `json:"history_days"`
		Revision    int                `json:"revision"`
		Risk        bool               `json:"risk_acknowledged"`
		Items       []dnsItem          `json:"items"`
		Channels    []notificationItem `json:"channels"`
	}{}
	var body []byte
	if request.Method == http.MethodDelete {
		input.OperationID = request.Header.Get("Idempotency-Key")
		body = []byte("{}")
		if prior := request.Header.Get("X-Prior-Operation-ID"); prior != "" {
			body, _ = json.Marshal(recoveryReference{PriorID: prior, RecoveryID: request.Header.Get("X-Recovery-ID")})
		}
	} else {
		var raw json.RawMessage
		if err := decodeJSON(response, request, &raw); err != nil {
			writeError(response, err)
			return
		}
		var canonical any
		if err := json.Unmarshal(raw, &canonical); err != nil {
			writeError(response, invalid("JSON 无效"))
			return
		}
		body, _ = json.Marshal(canonical)
		if err := json.Unmarshal(body, &input); err != nil {
			writeError(response, invalid("字段类型无效，请检查数字、开关或列表"))
			return
		}
	}
	if len(parts) == 3 && parts[0] == "operations" && parts[2] == "recovery" && request.Method == http.MethodPost {
		if header := request.Header.Get("Idempotency-Key"); header != "" && header != input.OperationID {
			writeError(response, invalid("操作号不一致"))
			return
		}
		service.handleOperationRecovery(response, request, parts[1], body, input.OperationID)
		return
	}
	if len(parts) > 0 && parts[0] == "connections" {
		if headerID := request.Header.Get("Idempotency-Key"); headerID != "" && headerID != input.OperationID {
			writeError(response, invalid("Idempotency-Key 与 operation_id 不一致"))
			return
		}
		service.writeConnection(response, request, parts, body, input.OperationID)
		return
	}
	if headerID := request.Header.Get("Idempotency-Key"); headerID != "" && headerID != input.OperationID {
		writeError(response, invalid("Idempotency-Key 与 operation_id 不一致"))
		return
	}
	operation, _, err := service.mutate(input.OperationID, request.Method+" "+request.URL.Path+" "+request.Header.Get("If-Match"), body, func(tx *sql.Tx) (string, any, error) {
		switch {
		case len(parts) == 1 && parts[0] == "tasks" && request.Method == http.MethodPost:
			return service.saveTask(tx, input.Task, "", 0)
		case len(parts) == 2 && parts[0] == "tasks" && request.Method == http.MethodPatch:
			revision, _ := strconv.Atoi(strings.Trim(request.Header.Get("If-Match"), "\""))
			return service.saveTask(tx, input.Task, parts[1], revision)
		case len(parts) == 3 && parts[0] == "tasks" && request.Method == http.MethodPost:
			if parts[2] == "runs" {
				task, err := get[Task](tx, "tasks", parts[1])
				if err != nil {
					return "", nil, err
				}
				run, err := enqueue(tx, task, "manual", input.OperationID, service)
				if err == nil {
					run.OperationID = input.OperationID
					err = put(tx, "runs", run.ID, run)
				}
				return run.ID, map[string]string{"id": run.ID, "status": run.Status}, err
			}
			return service.taskState(tx, parts[1], parts[2], input.Enabled)
		case len(parts) == 3 && parts[0] == "runs" && request.Method == http.MethodPost:
			switch parts[2] {
			case "cancel":
				return service.cancelRun(tx, parts[1])
			case "restore":
				return service.historyAction(tx, parts[1], true)
			case "dns-deliveries":
				return service.manualDNS(tx, parts[1], input.OperationID, input.Items, input.Risk)
			case "notifications":
				return service.manualNotifications(tx, parts[1], input.Channels, input.Risk, input.OperationID)
			}
		case len(parts) == 2 && parts[0] == "runs" && request.Method == http.MethodDelete:
			return service.historyAction(tx, parts[1], false)
		case len(parts) == 4 && parts[0] == "dns-targets" && parts[2] == "auto-write-protection" && parts[3] == "clear" && request.Method == http.MethodPost:
			return service.clearProtection(tx, parts[1], input.Revision, input.Risk)
		case len(parts) == 1 && parts[0] == "settings" && request.Method == http.MethodPatch:
			return service.updateSettings(tx, Settings{Timezone: input.Timezone, HistoryDays: input.HistoryDays})
		}
		return "", nil, errNotFound
	})
	if err != nil {
		writeError(response, err)
		return
	}
	if len(parts) == 3 && parts[0] == "runs" && parts[2] == "cancel" {
		service.mu.Lock()
		if cancel := service.cancels[parts[1]]; cancel != nil {
			cancel()
		}
		service.mu.Unlock()
	}
	operation.RequestHash = ""
	writeJSON(response, 202, operation)
}
