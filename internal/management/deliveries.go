package management

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/masgzy/CloudflareST/internal/integrations"
	"github.com/masgzy/CloudflareST/internal/optimizer"
)

type identityProvider interface {
	AccountIdentity(context.Context, integrations.Connection) (string, error)
}

func (service *Service) protect(query querier, target Target, attemptID, reason string) error {
	key := targetKey(target.AccountID, target.RecordID)
	previous, err := get[Protection](query, "dns_protection", key)
	revision := 1
	if err == nil {
		revision = previous.Revision + 1
	} else if !errors.Is(err, errNotFound) {
		return err
	}
	protection := Protection{Active: true, Revision: revision, Reason: integrations.Redact(reason), CreatedAt: service.now(), AttemptID: attemptID}
	return put(query, "dns_protection", key, protection)
}

func (service *Service) appendTargetMetadata(query querier, target *Target) error {
	key := targetKey(target.AccountID, target.RecordID)
	if last, err := get[LastWrite](query, "dns_last_write", key); err == nil {
		target.LastKnownWrite = &last
	} else if !errors.Is(err, errNotFound) {
		return err
	}
	if protection, err := get[Protection](query, "dns_protection", key); err == nil {
		target.Protection = &protection
	} else if !errors.Is(err, errNotFound) {
		return err
	}
	target.TargetID = key
	return nil
}

func (service *Service) validateAccount(ctx context.Context, connection *integrations.Connection) error {
	if connection.Kind != "alidns" {
		return nil
	}
	identity, ok := service.provider.(identityProvider)
	if !ok {
		return errors.New("DNS 提供商无法验证账号身份，当前连接未保存")
	}
	accountID, err := identity.AccountIdentity(ctx, *connection)
	if err != nil {
		return err
	}
	if connection.AccountID != "" && connection.AccountID != accountID {
		return errors.New("阿里云凭据对应的账号与连接声明不一致，请新建连接，不得跨账号替换")
	}
	connection.AccountID = accountID
	return nil
}

func (service *Service) performDelivery(ctx context.Context, runID, deliveryID string) {
	service.mu.Lock()
	run, err := getRunMetadata(service.db, runID)
	if err != nil {
		service.mu.Unlock()
		return
	}
	index := -1
	for i := range run.Deliveries {
		if run.Deliveries[i].ID == deliveryID {
			index = i
			break
		}
	}
	if index < 0 {
		service.mu.Unlock()
		return
	}
	delivery := run.Deliveries[index]
	if delivery.State != "pending" && delivery.State != "dispatching" {
		service.mu.Unlock()
		return
	}
	if ctx.Err() != nil || isRunCancelled(run) && !delivery.Cancellation {
		delivery.State = "skipped"
		delivery.Reason = "运行已取消，未发起新的业务投递"
		delivery.EndedAt = timePointer(service.now())
		run.Deliveries[index] = delivery
		service.updateDeliveryStageLocked(&run, delivery)
		if err = service.saveDeliveryConclusionLocked(run, delivery); err != nil {
			service.cancel()
		}
		service.mu.Unlock()
		return
	}
	if delivery.Automatic {
		stage := "notifications"
		if delivery.Cancellation {
			stage = "cancellation_notifications"
		} else if delivery.Kind == "dns" {
			stage = "dns"
		}
		service.setStageLocked(&run, stage)
	}
	connection, err := get[Connection](service.db, "connections", delivery.ConnectionID)
	if err != nil {
		delivery.State = "failed"
		delivery.Reason = "服务连接不存在"
		delivery.EndedAt = timePointer(service.now())
		run.Deliveries[index] = delivery
		service.updateDeliveryStageLocked(&run, delivery)
		if err = service.saveDeliveryConclusionLocked(run, delivery); err != nil {
			service.cancel()
		}
		service.mu.Unlock()
		return
	}
	credential, err := service.credentials(connection)
	if err != nil {
		delivery.State = "failed"
		delivery.Reason = safeError(err)
		delivery.EndedAt = timePointer(service.now())
		run.Deliveries[index] = delivery
		service.updateDeliveryStageLocked(&run, delivery)
		if err = service.saveDeliveryConclusionLocked(run, delivery); err != nil {
			service.cancel()
		}
		service.mu.Unlock()
		return
	}
	var key string
	if delivery.Kind == "dns" && delivery.Target != nil {
		key = targetKey(delivery.Target.AccountID, delivery.Target.RecordID)
	}
	if delivery.Automatic && key != "" {
		protection, readErr := get[Protection](service.db, "dns_protection", key)
		if readErr == nil && protection.Active {
			delivery.State = "skipped"
			delivery.Reason = "目标存在未确认写入保护，已跳过且不排队等待解除：" + protection.Reason
			delivery.EndedAt = timePointer(service.now())
			run.Deliveries[index] = delivery
			service.setStageLocked(&run, "dns")
			if err = service.saveDeliveryConclusionLocked(run, delivery); err != nil {
				service.cancel()
			}
			service.mu.Unlock()
			return
		}
		if readErr != nil && !errors.Is(readErr, errNotFound) {
			service.finishUnsentLocked(&run, index, "DNS 保护状态无法读取，未发起写入；请修复数据后人工核查")
			service.mu.Unlock()
			return
		}
	}
	service.mu.Unlock()

	var targetMutex *sync.Mutex
	if key != "" {
		targetMutex = service.targetLock(key)
		targetMutex.Lock()
		defer targetMutex.Unlock()
	}

	service.mu.Lock()
	run, err = getRunMetadata(service.db, runID)
	if err != nil {
		service.cancel()
		service.mu.Unlock()
		return
	}
	index = -1
	for i := range run.Deliveries {
		if run.Deliveries[i].ID == deliveryID {
			index = i
			break
		}
	}
	if index < 0 {
		service.cancel()
		service.mu.Unlock()
		return
	}
	delivery = run.Deliveries[index]
	if ctx.Err() != nil || isRunCancelled(run) && !delivery.Cancellation {
		delivery.State = "skipped"
		delivery.Reason = "运行已取消，未发起新的业务投递"
		delivery.EndedAt = timePointer(service.now())
		run.Deliveries[index] = delivery
		service.updateDeliveryStageLocked(&run, delivery)
		if err = service.saveDeliveryConclusionLocked(run, delivery); err != nil {
			service.cancel()
		}
		service.mu.Unlock()
		return
	}
	if delivery.Automatic && delivery.Kind == "dns" && delivery.Target != nil {
		protection, protectionErr := get[Protection](service.db, "dns_protection", key)
		if protectionErr == nil && protection.Active {
			delivery.State = "skipped"
			delivery.Reason = "目标存在未确认写入保护，已跳过且不排队等待解除：" + protection.Reason
			delivery.EndedAt = timePointer(service.now())
			run.Deliveries[index] = delivery
			service.updateDeliveryStageLocked(&run, delivery)
			if err = service.saveDeliveryConclusionLocked(run, delivery); err != nil {
				service.cancel()
			}
			service.mu.Unlock()
			return
		}
		if protectionErr != nil && !errors.Is(protectionErr, errNotFound) {
			service.finishUnsentLocked(&run, index, "DNS 保护状态无法读取，未发起写入；请修复数据后人工核查")
			service.mu.Unlock()
			return
		}
	}
	started := service.now()
	delivery.State = "running"
	delivery.StartedAt = &started
	run.Deliveries[index] = delivery
	service.updateDeliveryStageLocked(&run, delivery)
	if err = put(service.db, "runs", runID, run); err != nil {
		service.cancel()
		service.mu.Unlock()
		return
	}
	service.mu.Unlock()

	outcome := service.callProvider(ctx, credential, delivery, run)
	if outcome.State != "success" && outcome.State != "failed" && outcome.State != "skipped" && outcome.State != "unconfirmed" {
		outcome.State = "unconfirmed"
		outcome.Reason = "外部响应没有明确结论，未重放操作"
	}

	service.mu.Lock()
	defer service.mu.Unlock()
	tx, err := service.db.Begin()
	if err != nil {
		service.cancel()
		return
	}
	defer tx.Rollback()
	run, err = getRunMetadata(tx, runID)
	if err != nil {
		service.cancel()
		return
	}
	for i := range run.Deliveries {
		if run.Deliveries[i].ID != deliveryID {
			continue
		}
		current := &run.Deliveries[i]
		current.State = outcome.State
		current.Verified = outcome.Verified
		current.NoWrite = outcome.NoWrite
		current.Before = outcome.Before
		current.Readback = outcome.Readback
		current.Reason = integrations.Redact(outcome.Reason)
		current.EndedAt = timePointer(service.now())
		if current.Kind == "dns" && current.Target != nil {
			if outcome.State == "unconfirmed" {
				if err = service.protect(tx, *current.Target, current.ID, current.Reason); err != nil {
					service.cancel()
					return
				}
			}
			if outcome.State == "success" && !outcome.NoWrite {
				last := LastWrite{TaskID: run.TaskID, RunID: run.ID, WrittenAt: service.now(), IP: current.IP}
				if err = put(tx, "dns_last_write", targetKey(current.Target.AccountID, current.Target.RecordID), last); err != nil {
					service.cancel()
					return
				}
			}
		}
		break
	}
	if delivery.Automatic {
		stage := "notifications"
		if delivery.Cancellation {
			stage = "cancellation_notifications"
		} else if delivery.Kind == "dns" {
			stage = "dns"
		}
		service.setStageLocked(&run, stage)
	}
	if err = put(tx, "runs", runID, run); err == nil {
		err = service.log(tx, "系统", "delivery_result", delivery.ID, outcome.State, outcome.Reason)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		service.cancel()
	}
}

func (service *Service) saveDeliveryConclusionLocked(run Run, delivery Delivery) error {
	tx, err := service.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = put(tx, "runs", run.ID, run); err != nil {
		return err
	}
	if err = service.log(tx, "系统", "delivery_result", delivery.ID, delivery.State, delivery.Reason); err != nil {
		return err
	}
	return tx.Commit()
}

func (service *Service) finishUnsentLocked(run *Run, index int, reason string) {
	delivery := run.Deliveries[index]
	delivery.State = "failed"
	delivery.Reason = reason
	delivery.EndedAt = timePointer(service.now())
	run.Deliveries[index] = delivery
	service.updateDeliveryStageLocked(run, delivery)
	if err := service.saveDeliveryConclusionLocked(*run, delivery); err != nil {
		service.cancel()
	}
}

func (service *Service) updateDeliveryStageLocked(run *Run, delivery Delivery) {
	if !delivery.Automatic {
		return
	}
	stage := "notifications"
	if delivery.Cancellation {
		stage = "cancellation_notifications"
	} else if delivery.Kind == "dns" {
		stage = "dns"
	}
	service.setStageLocked(run, stage)
}

func (service *Service) callProvider(ctx context.Context, connection integrations.Connection, delivery Delivery, run Run) integrations.Outcome {
	if delivery.Kind == "dns" {
		if delivery.Target == nil {
			return integrations.Outcome{State: "failed", Reason: "DNS 目标缺失"}
		}
		if delivery.IP == "" {
			return integrations.Outcome{State: "skipped", Reason: "没有匹配地址族的合格 IP"}
		}
		if (delivery.Target.Type == "A" && !strings.Contains(delivery.IP, ".")) || (delivery.Target.Type == "AAAA" && !strings.Contains(delivery.IP, ":")) {
			return integrations.Outcome{State: "failed", Reason: "候选地址族与 DNS 记录类型不匹配"}
		}
		integrated := integrations.Target{ConnectionID: delivery.Target.ConnectionID, AccountID: delivery.Target.AccountID, RecordID: delivery.Target.RecordID, Domain: delivery.Target.Domain, RR: delivery.Target.RR, Type: delivery.Target.Type, Line: delivery.Target.Line, TTL: delivery.Target.TTL, Value: delivery.Target.Value}
		return service.provider.Update(ctx, connection, integrated, delivery.IP)
	}
	message := summary(run, delivery.Cancellation)
	return service.provider.Notify(ctx, connection, message)
}

func summary(run Run, cancelled bool) string {
	state := run.Result.Status
	if !active(run.Status) && run.Status != "" {
		state = run.Status
	}
	if state == "" {
		state = "unknown"
	}
	if cancelled {
		state = "cancelled"
	}
	lines := []string{fmt.Sprintf("CloudflareST 任务 %s（%s）", run.TaskName, run.TaskID), fmt.Sprintf("执行 %s；结束结论 %s", run.ID, state)}
	if run.Result.Status != "" && run.Result.Status != state {
		lines = append(lines, "优选阶段结论 "+run.Result.Status+"；不替代执行最终状态")
	}
	for _, family := range []string{"ipv4", "ipv6"} {
		familyName := "IPv4"
		enabled := run.Snapshot.Config.IPv4Enabled
		if family == "ipv6" {
			familyName = "IPv6"
			enabled = run.Snapshot.Config.IPv6Enabled
		}
		if !enabled {
			lines = append(lines, familyName+"：未启用")
			continue
		}
		line := fmt.Sprintf("%s 合格 %d", familyName, eligible(run, family))
		var best *optimizer.Candidate
		if run.CandidateSummary != nil {
			for _, result := range run.CandidateSummary.Families {
				if result.Family == family {
					best = result.Best
				}
			}
		}
		for _, candidate := range run.Result.Candidates {
			if candidate.Family != family || !candidate.Eligible {
				continue
			}
			if candidate.Rank == 1 {
				best = &candidate
				break
			}
			if best == nil {
				best = &candidate
			}
		}
		if best != nil {
			latency, loss, speed := "未测", "未测", "未测"
			if best.LatencyMS != nil {
				latency = fmt.Sprintf("%.2f ms", *best.LatencyMS)
			}
			if best.LossPercent != nil {
				loss = fmt.Sprintf("%.2f%%", *best.LossPercent)
			}
			if best.SpeedMBS != nil {
				speed = fmt.Sprintf("%.2f MB/s", *best.SpeedMBS)
			}
			line += fmt.Sprintf("；最佳 %s；延迟 %s；失败比例 %s；下载 %s", best.IP, latency, loss, speed)
		}
		lines = append(lines, line)
	}
	dns := false
	for _, delivery := range run.Deliveries {
		if delivery.Kind == "dns" && delivery.Target != nil {
			dns = true
			lines = append(lines, fmt.Sprintf("DNS %s（%s）：%s %s", delivery.Target.RecordID, delivery.Target.Type, delivery.State, delivery.Reason))
		}
	}
	if !dns {
		lines = append(lines, "DNS：未执行／未启用或无匹配合格结果")
	}
	if cancelled {
		lines = append(lines, "已有结果与外部操作不回滚，未确认的 DNS 目标需人工核对。")
	}
	return integrations.Redact(strings.Join(lines, "\n"))
}

func eligible(run Run, family string) int {
	if run.CandidateSummary != nil {
		for _, result := range run.CandidateSummary.Families {
			if result.Family == family {
				return result.Eligible
			}
		}
	}
	count := 0
	for _, candidate := range run.Result.Candidates {
		if candidate.Family == family && candidate.Eligible {
			count++
		}
	}
	return count
}

func timePointer(value time.Time) *time.Time { return &value }

func (service *Service) clearProtection(tx *sql.Tx, key string, revision int, risk bool) (string, any, error) {
	protection, err := get[Protection](tx, "dns_protection", key)
	if err != nil {
		return "", nil, err
	}
	if !risk {
		return "", nil, invalid("明确解除前必须确认旧远端请求可能仍在执行")
	}
	if protection.Revision != revision {
		return "", nil, &apiError{409, "TARGET_PROTECTION_CHANGED", "保护版本已变化，请重新核对当前未确认原因"}
	}
	protection.Active = false
	protection.Revision++
	if err = put(tx, "dns_protection", key, protection); err != nil {
		return "", nil, err
	}
	if err = service.log(tx, "共享管理身份", "dns_protection_clear", key, "cleared", protection.Reason); err != nil {
		return "", nil, err
	}
	return key, protection, nil
}

func (service *Service) recordTarget(query querier, connectionID string, record integrations.Target) (Target, error) {
	connection, err := get[Connection](query, "connections", connectionID)
	if err != nil {
		return Target{}, err
	}
	target := Target{Target: record, TargetID: targetKey(connection.AccountID, record.RecordID)}
	target.AccountID = connection.AccountID
	target.ConnectionID = connectionID
	if err = service.appendTargetMetadata(query, &target); err != nil {
		return Target{}, err
	}
	return target, nil
}

func redactJSON(value any) string {
	body, _ := json.Marshal(value)
	return integrations.Redact(string(body))
}
