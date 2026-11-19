package management

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/masgzy/CloudflareST/internal/integrations"
)

func (service *Service) writeConnection(response http.ResponseWriter, request *http.Request, parts []string, body []byte, operationID string) {
	action := request.Method + " " + request.URL.Path + " " + request.Header.Get("If-Match")
	if receipt, found, err := service.priorOperation(operationID, action, body); err != nil {
		writeError(response, err)
		return
	} else if found {
		writeJSON(response, 202, receipt)
		return
	}
	if request.Method == http.MethodDelete {
		if len(parts) != 2 {
			writeError(response, errNotFound)
			return
		}
		id := parts[1]
		receipt, _, err := service.mutate(operationID, action, body, func(tx *sql.Tx) (string, any, error) {
			if _, err := get[Connection](tx, "connections", id); err != nil {
				return "", nil, err
			}
			references, busy, err := connectionReferences(tx, id)
			if err != nil {
				return "", nil, err
			}
			if len(references) > 0 || busy {
				return "", nil, conflict("连接仍被任务引用或运行／投递使用，请先解除引用并等待收尾")
			}
			if err = remove(tx, "connections", id); err != nil {
				return "", nil, err
			}
			return id, map[string]string{"id": id}, nil
		})
		if err != nil {
			writeError(response, err)
			return
		}
		receipt.RequestHash = ""
		writeJSON(response, 202, receipt)
		return
	}
	if request.Method != http.MethodPost && request.Method != http.MethodPatch {
		writeError(response, &apiError{405, "method_not_allowed", "不支持的连接操作"})
		return
	}
	if request.Method == http.MethodPost && len(parts) != 1 || request.Method == http.MethodPatch && len(parts) != 2 {
		writeError(response, errNotFound)
		return
	}
	var input struct {
		OperationID string `json:"operation_id"`
		Connection  struct {
			ID        string            `json:"id"`
			Kind      string            `json:"kind"`
			Name      string            `json:"name"`
			AccountID string            `json:"account_id"`
			Secrets   map[string]string `json:"secrets"`
		} `json:"connection"`
	}
	if err := json.Unmarshal(body, &input); err != nil {
		writeError(response, invalid("连接正文无效"))
		return
	}
	input.Connection.Name = strings.TrimSpace(input.Connection.Name)
	if input.Connection.Name == "" || len([]rune(input.Connection.Name)) > 100 {
		writeError(response, invalid("连接名称必填，最多100字"))
		return
	}
	if request.Method == http.MethodPatch {
		input.Connection.ID = parts[1]
	}
	service.mu.Lock()
	old, oldErr := get[Connection](service.db, "connections", input.Connection.ID)
	service.mu.Unlock()
	if request.Method == http.MethodPatch && oldErr != nil {
		writeError(response, oldErr)
		return
	}
	if request.Method == http.MethodPost && input.Connection.ID != "" {
		writeError(response, invalid("新建连接不能自定连接标识"))
		return
	}
	secretsSupplied := input.Connection.Secrets != nil
	if request.Method == http.MethodPatch && !secretsSupplied {
		previousCredentials, err := service.credentials(old)
		if err != nil {
			writeError(response, err)
			return
		}
		input.Connection.Secrets = previousCredentials.Secrets
		if input.Connection.AccountID == "" {
			input.Connection.AccountID = old.AccountID
		}
	}
	if request.Method == http.MethodPatch {
		service.mu.Lock()
		references, busy, err := connectionReferences(service.db, input.Connection.ID)
		service.mu.Unlock()
		if err != nil {
			writeError(response, err)
			return
		}
		if busy {
			writeError(response, conflict("连接正被排队／运行任务或投递引用，暂不能替换凭据或目标身份"))
			return
		}
		if (old.Kind != input.Connection.Kind || old.AccountID != input.Connection.AccountID) && len(references) > 0 {
			writeError(response, conflict("更换服务类型或账号身份必须新建连接并先解除现有引用"))
			return
		}
		previousCredentials, err := service.credentials(old)
		if err != nil {
			writeError(response, err)
			return
		}
		if notificationIdentityChanged(old.Kind, previousCredentials.Secrets, input.Connection.Secrets) {
			writeError(response, conflict("通知目标身份发生改变，请新建连接，不要替换原目标"))
			return
		}
	}
	credentials := integrations.Connection{ID: input.Connection.ID, Kind: input.Connection.Kind, Name: input.Connection.Name, AccountID: input.Connection.AccountID, Secrets: input.Connection.Secrets}
	if err := integrations.ValidateConnection(credentials); err != nil {
		writeError(response, invalid(err.Error()))
		return
	}
	if credentials.Kind == "alidns" {
		if request.Method == http.MethodPatch && !secretsSupplied {
			credentials.AccountID = old.AccountID
		} else if err := service.validateAccount(request.Context(), &credentials); err != nil {
			writeError(response, invalid(safeError(err)))
			return
		}
	}
	input.Connection.AccountID = credentials.AccountID
	ciphertext, err := service.encrypt(input.Connection.Secrets)
	if err != nil {
		writeError(response, err)
		return
	}
	operation, _, err := service.mutate(operationID, action, body, func(tx *sql.Tx) (string, any, error) {
		connection := Connection{ID: input.Connection.ID, Kind: input.Connection.Kind, Name: input.Connection.Name, AccountID: input.Connection.AccountID, CredentialsSet: true, Ciphertext: ciphertext}
		if request.Method == http.MethodPost {
			connection.ID = randomID()
			connection.Revision = 1
		} else {
			previous, err := get[Connection](tx, "connections", connection.ID)
			if err != nil {
				return "", nil, err
			}
			revision := strings.Trim(request.Header.Get("If-Match"), "\"")
			if revision == "" || revision != strconv.Itoa(previous.Revision) || previous.Revision != old.Revision {
				return "", nil, &apiError{409, "REVISION_CONFLICT", "连接版本已改变，请重新读取后再替换"}
			}
			if previous.Kind != connection.Kind || previous.AccountID != connection.AccountID {
				return "", nil, conflict("不能将旧连接静默改成其他服务或账号身份")
			}
			previousCredentials, err := service.credentials(previous)
			if err != nil {
				return "", nil, err
			}
			if notificationIdentityChanged(previous.Kind, previousCredentials.Secrets, input.Connection.Secrets) {
				return "", nil, conflict("通知目标身份发生改变，请新建连接，不要替换原目标")
			}
			_, busy, err := connectionReferences(tx, connection.ID)
			if err != nil {
				return "", nil, err
			}
			if busy {
				return "", nil, conflict("连接已在队列、运行或收尾中使用，不能替换")
			}
			connection.Revision = previous.Revision + 1
		}
		if err := put(tx, "connections", connection.ID, connection); err != nil {
			return "", nil, err
		}
		return connection.ID, map[string]string{"id": connection.ID}, nil
	})
	if err != nil {
		writeError(response, err)
		return
	}
	operation.RequestHash = ""
	writeJSON(response, 202, operation)
}

func (service *Service) readConnection(response http.ResponseWriter, request *http.Request, parts []string) {
	if len(parts) != 3 {
		writeError(response, errNotFound)
		return
	}
	id := parts[1]
	service.mu.Lock()
	connection, err := get[Connection](service.db, "connections", id)
	if err != nil {
		service.mu.Unlock()
		writeError(response, err)
		return
	}
	credential, err := service.credentials(connection)
	service.mu.Unlock()
	if err != nil {
		writeError(response, err)
		return
	}
	switch parts[2] {
	case "credentials":
		writeJSON(response, 200, map[string]map[string]string{"secrets": credential.Secrets})
	case "domains":
		domains, err := service.provider.Domains(request.Context(), credential)
		if err != nil {
			writeError(response, invalid(safeError(err)))
			return
		}
		sort.Strings(domains)
		writeJSON(response, 200, domains)
	case "dns-records":
		domain := request.URL.Query().Get("domain")
		if strings.TrimSpace(domain) == "" {
			writeError(response, invalid("请先选择已验证域名"))
			return
		}
		records, err := service.provider.Records(request.Context(), credential, domain)
		if err != nil {
			writeError(response, invalid(safeError(err)))
			return
		}
		targets := make([]Target, 0, len(records))
		readAt := service.now()
		service.mu.Lock()
		for _, record := range records {
			if record.ReadAt == nil {
				record.ReadAt = &readAt
			}
			target := Target{Target: record, TargetID: targetKey(connection.AccountID, record.RecordID)}
			target.AccountID = connection.AccountID
			target.ConnectionID = id
			if err = service.appendTargetMetadata(service.db, &target); err != nil {
				break
			}
			targets = append(targets, target)
			if err = put(service.db, "target_catalog", target.TargetID, target); err != nil {
				break
			}
		}
		service.mu.Unlock()
		if err != nil {
			writeError(response, err)
			return
		}
		writeJSON(response, 200, targets)
	default:
		writeError(response, errNotFound)
	}
}

type dnsItem struct {
	Target Target `json:"target"`
	IP     string `json:"ip"`
}

func notificationIdentityChanged(kind string, previous, next map[string]string) bool {
	switch kind {
	case "telegram":
		previousBot, _, _ := strings.Cut(previous["bot_token"], ":")
		nextBot, _, _ := strings.Cut(next["bot_token"], ":")
		return previousBot != nextBot || previous["chat_id"] != next["chat_id"]
	case "wecom":
		return previous["webhook_url"] != next["webhook_url"]
	default:
		return false
	}
}

type notificationItem struct {
	Kind           string `json:"kind"`
	ConnectionID   string `json:"connection_id"`
	PriorAttemptID string `json:"prior_attempt_id"`
}

func (service *Service) makeManualDelivery(tx *sql.Tx, run *Run, kind, connectionID string, target *Target, ip, prior string, operationIDs ...string) (Delivery, error) {
	connection, err := get[Connection](tx, "connections", connectionID)
	if err != nil {
		return Delivery{}, err
	}
	if kind == "dns" && connection.Kind != "alidns" || kind != "dns" && connection.Kind != kind {
		return Delivery{}, invalid("投递类型与服务连接不匹配")
	}
	if len(run.Deliveries) > 1000 {
		return Delivery{}, conflict("本次执行的投递明细已达上限")
	}
	delivery := Delivery{ID: randomID(), Kind: kind, ConnectionID: connectionID, Target: target, IP: ip, State: "pending", PriorAttemptID: prior}
	if kind != "dns" && prior != "" {
		for _, original := range run.Deliveries {
			if original.ID == prior && original.Kind == kind && original.ConnectionID == connectionID {
				delivery.Cancellation = original.Cancellation
				break
			}
		}
	}
	if len(operationIDs) > 0 {
		delivery.OperationID = operationIDs[0]
	}
	run.Deliveries = append(run.Deliveries, delivery)
	if err = put(tx, "runs", run.ID, *run); err != nil {
		return Delivery{}, err
	}
	return delivery, nil
}

func (service *Service) manualDNS(tx *sql.Tx, runID, operationID string, items []dnsItem, risk bool) (string, any, error) {
	run, err := getRunMetadata(tx, runID)
	if err != nil {
		return "", nil, err
	}
	if active(run.Status) || run.DeletedAt != nil {
		return "", nil, conflict("请等待执行收尾结束，并从未删除记录选择结果")
	}
	if len(items) == 0 || len(items) > 100 {
		return "", nil, invalid("请选择1到100条明确 DNS 记录")
	}
	if !risk {
		return "", nil, invalid("请明确确认仅更新这些目标的Value，远端结果未确认时可能重复写入")
	}
	seen := map[string]bool{}
	ids := []string{}
	for _, item := range items {
		if item.Target.TargetID == "" || seen[item.Target.TargetID] {
			return "", nil, invalid("DNS目标标识缺失或重复")
		}
		seen[item.Target.TargetID] = true
		catalog, err := get[Target](tx, "target_catalog", item.Target.TargetID)
		if err != nil {
			return "", nil, invalid("DNS目标已过期，请重新从服务商读取")
		}
		selectedConnection, connectionErr := get[Connection](tx, "connections", item.Target.ConnectionID)
		if connectionErr != nil || selectedConnection.Kind != "alidns" || selectedConnection.AccountID != catalog.AccountID || targetKey(selectedConnection.AccountID, item.Target.RecordID) != item.Target.TargetID {
			return "", nil, invalid("所选连接与实际 DNS 目标身份不一致")
		}
		if catalog.RecordID != item.Target.RecordID || catalog.Type != item.Target.Type || item.Target.Domain != catalog.Domain || item.Target.RR != catalog.RR {
			return "", nil, invalid("DNS目标与最近读取的现存记录不匹配")
		}
		family := "ipv4"
		if catalog.Type == "AAAA" {
			family = "ipv6"
		}
		eligible, err := eligibleCandidate(tx, run.ID, item.IP, family)
		if err != nil {
			return "", nil, err
		}
		if !eligible {
			return "", nil, invalid("只能选择本次运行同地址族的最终合格 IP")
		}
		target := catalog
		target.ConnectionID = item.Target.ConnectionID
		if _, err := service.makeManualDelivery(tx, &run, "dns", target.ConnectionID, &target, item.IP, "", operationID); err != nil {
			return "", nil, err
		}
		ids = append(ids, item.Target.TargetID)
	}
	return run.ID, map[string]any{"run_id": run.ID, "targets": ids, "queued": true}, put(tx, "runs", run.ID, run)
}

func (service *Service) manualNotifications(tx *sql.Tx, runID string, channels []notificationItem, risk bool, operationIDs ...string) (string, any, error) {
	run, err := getRunMetadata(tx, runID)
	if err != nil {
		return "", nil, err
	}
	if active(run.Status) || run.DeletedAt != nil {
		return "", nil, conflict("运行尚未结束，暂不能发送结束摘要")
	}
	if len(channels) == 0 || len(channels) > 2 {
		return "", nil, invalid("请选择一个或两个通知渠道")
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, item := range channels {
		if item.Kind != "telegram" && item.Kind != "wecom" || item.ConnectionID == "" || seen[item.Kind] {
			return "", nil, invalid("通知渠道无效或重复")
		}
		seen[item.Kind] = true
		priorState := ""
		priorCancellation := false
		latestAttemptID := ""
		if item.PriorAttemptID != "" {
			for _, delivery := range run.Deliveries {
				if delivery.Kind == item.Kind {
					latestAttemptID = delivery.ID
				}
				if delivery.ID == item.PriorAttemptID && delivery.Kind == item.Kind && delivery.ConnectionID == item.ConnectionID {
					priorState = delivery.State
					priorCancellation = delivery.Cancellation
				}
			}
		}
		if isRunCancelled(run) && !priorCancellation {
			return "", nil, conflict("执行已取消，只能明确关联失败或未确认的取消摘要进行重发，不发起新的业务通知")
		}
		if item.PriorAttemptID != "" {
			if priorState != "failed" && priorState != "unconfirmed" {
				return "", nil, conflict("只能重发失败或未确认的原渠道尝试")
			}
			if latestAttemptID != item.PriorAttemptID {
				return "", nil, conflict("该渠道已有后续尝试，请刷新后仅重发最新失败或未确认的尝试")
			}
			if !risk {
				return "", nil, invalid("重发失败或未确认消息可能产生重复，请确认风险")
			}
		} else {
			for _, delivery := range run.Deliveries {
				if delivery.Kind == item.Kind && (delivery.State == "success" || delivery.State == "pending" || delivery.State == "dispatching" || delivery.State == "running" || delivery.State == "failed" || delivery.State == "unconfirmed") {
					return "", nil, conflict("该渠道已成功发送结束摘要；如需重发，请指定可重试的失败尝试")
				}
			}
			if !risk {
				return "", nil, invalid("首次手动发送摘要前请确认发送范围")
			}
		}
		delivery, err := service.makeManualDelivery(tx, &run, item.Kind, item.ConnectionID, nil, "", item.PriorAttemptID, operationIDs...)
		if err != nil {
			return "", nil, err
		}
		ids = append(ids, delivery.ID)
	}
	return run.ID, map[string]any{"run_id": run.ID, "delivery_ids": ids}, put(tx, "runs", run.ID, run)
}
