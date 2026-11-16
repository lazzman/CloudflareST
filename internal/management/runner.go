package management

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/masgzy/CloudflareST/internal/optimizer"
)

func (service *Service) recoverRuns() error {
	tx, err := service.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	runs, err := pendingRuns(tx)
	if err != nil {
		return err
	}
	now := service.now()
	for _, run := range runs {
		changed := false
		if active(run.Status) {
			if run.Status == "running" {
				run.Status = "interrupted"
				run.EndedAt = nil
			} else {
				run.Status = "cancelled"
				run.EndedAt = &now
			}
			run.SlotBusy = false
			run.Stage = run.Status
			run.RetentionAt = &now
			run.Reason = "服务重启，未恢复执行、补跑或重放外部操作"
			changed = true
		}
		for index := range run.Deliveries {
			delivery := &run.Deliveries[index]
			if delivery.State == "running" {
				delivery.State = "unconfirmed"
				delivery.Reason = "服务重启，已发起的外部请求结果未确认，未重放"
				delivery.EndedAt = nil
				changed = true
				if delivery.Kind == "dns" && delivery.Target != nil {
					if err = service.protect(tx, *delivery.Target, delivery.ID, delivery.Reason); err != nil {
						return err
					}
				}
			} else if delivery.State == "pending" || delivery.State == "dispatching" {
				delivery.State = "skipped"
				delivery.Reason = "服务重启，未发起旧投递或补发摘要"
				delivery.EndedAt = &now
				changed = true
			}
		}
		if changed {
			if err = put(tx, "runs", run.ID, run); err != nil {
				return err
			}
			if err = service.log(tx, "系统", "restart_recovery", run.ID, run.Status, run.Reason); err != nil {
				return err
			}
		}
	}
	settings, err := get[Settings](tx, "settings", "system")
	if err != nil {
		return err
	}
	tasks, err := all[Task](tx, "tasks")
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if task.Archived || !task.Schedule.Enabled {
			continue
		}
		next, err := advanceSchedule(task.Schedule, now, settings.Timezone)
		if err != nil {
			return err
		}
		task.Schedule.NextRunAt = &next
		if err = put(tx, "tasks", task.ID, task); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (service *Service) loop() {
	defer service.wg.Done()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	cleanupAt := time.Time{}
	for {
		select {
		case <-service.ctx.Done():
			return
		case <-ticker.C:
		case <-service.wake:
		}
		service.mu.Lock()
		if service.ctx.Err() != nil {
			service.mu.Unlock()
			return
		}
		err := service.scheduleDue()
		if err == nil {
			err = service.settleOperations()
		}
		if err == nil && !service.now().Before(cleanupAt) {
			err = service.cleanup()
			cleanupAt = service.now().Add(time.Minute)
		}
		if err == nil {
			err = service.dispatch()
		}
		service.mu.Unlock()
		if err != nil {
			service.cancel()
			return
		}
	}
}

func (service *Service) scheduleDue() error {
	tasks, err := all[Task](service.db, "tasks")
	if err != nil {
		return err
	}
	settings, err := get[Settings](service.db, "settings", "system")
	if err != nil {
		return err
	}
	now := service.now()
	for _, task := range tasks {
		if task.Archived || !task.Schedule.Enabled || task.Schedule.NextRunAt == nil || task.Schedule.NextRunAt.After(now) {
			continue
		}
		tx, err := service.db.Begin()
		if err != nil {
			return err
		}
		next, err := advanceSchedule(task.Schedule, now, settings.Timezone)
		if err == nil {
			task.Schedule.NextRunAt = &next
			_, runErr := enqueue(tx, task, "scheduled", "", service)
			var expected *apiError
			if errors.As(runErr, &expected) && expected.Status == 409 {
				err = service.log(tx, "系统", "schedule_skipped", task.ID, "skipped", expected.Message)
			} else {
				err = runErr
				if err == nil {
					err = service.log(tx, "系统", "schedule_trigger", task.ID, "accepted", "")
				}
			}
		}
		if err == nil {
			err = put(tx, "tasks", task.ID, task)
		}
		if err == nil {
			err = tx.Commit()
		}
		_ = tx.Rollback()
		if err != nil {
			return err
		}
	}
	return nil
}

func (service *Service) dispatch() error {
	runs, err := pendingRuns(service.db)
	if err != nil {
		return err
	}
	orderedRuns(runs)
	if len(service.cancels) == 0 {
		for _, run := range runs {
			if run.Status != "queued" {
				continue
			}
			now := service.now()
			run.Status = "running"
			run.Stage = "sources"
			run.SlotBusy = true
			run.StartedAt = &now
			run.Progress = optimizer.Progress{Stage: "sources", Unit: "来源", Message: "实际开始后解析本轮域名来源"}
			service.recordProgress(&run, run.Progress)
			if err = put(service.db, "runs", run.ID, run); err != nil {
				return err
			}
			ctx, cancel := context.WithCancel(service.ctx)
			service.cancels[run.ID] = cancel
			service.wg.Add(1)
			go service.execute(ctx, run)
			break
		}
	}
	for _, run := range runs {
		for _, delivery := range run.Deliveries {
			if delivery.Automatic || delivery.State != "pending" || service.manualBusy >= 4 {
				continue
			}
			service.manualBusy++
			for index := range run.Deliveries {
				if run.Deliveries[index].ID == delivery.ID {
					run.Deliveries[index].State = "dispatching"
				}
			}
			if err = put(service.db, "runs", run.ID, run); err != nil {
				service.manualBusy--
				return err
			}
			service.wg.Add(1)
			go func(runID string, item Delivery) {
				defer service.wg.Done()
				service.performDelivery(service.ctx, runID, item.ID)
				service.mu.Lock()
				service.manualBusy--
				service.mu.Unlock()
				select {
				case service.wake <- struct{}{}:
				default:
				}
			}(run.ID, delivery)
		}
	}
	return nil
}

func (service *Service) execute(ctx context.Context, initial Run) {
	defer service.wg.Done()
	result, runErr := service.engine.Run(ctx, initial.Snapshot.Config, func(progress optimizer.Progress) {
		service.mu.Lock()
		defer service.mu.Unlock()
		run, err := getRunMetadata(service.db, initial.ID)
		if err != nil {
			service.cancel()
			return
		}
		service.recordProgress(&run, progress)
		if err = put(service.db, "runs", run.ID, run); err != nil {
			service.cancel()
		}
	})
	service.mu.Lock()
	run, err := getRunMetadata(service.db, initial.ID)
	if err == nil {
		if result.Status == "" {
			result.Status = "failed"
		}
		if runErr != nil && result.Error == "" {
			result.Error = safeError(runErr)
		}
		sanitizeResult(&result)
		run.Result = result
		run.Stage = "finalizing"
		run.ProgressRevision++
		err = put(service.db, "runs", run.ID, run)
	}
	service.mu.Unlock()
	if err != nil {
		service.cancel()
		return
	}
	if ctx.Err() == nil && (result.Status == "completed" || result.Status == "no_results") && run.Snapshot.DNSAuto {
		service.setAutomaticStage(run.ID, "dns")
		for _, target := range run.Snapshot.Targets {
			if ctx.Err() != nil {
				break
			}
			ip := ""
			for _, candidate := range result.Candidates {
				if candidate.Eligible && candidate.Rank == 1 && ((target.Type == "A" && candidate.Family == "ipv4") || (target.Type == "AAAA" && candidate.Family == "ipv6")) {
					ip = candidate.IP
					break
				}
			}
			delivery := Delivery{ID: randomID(), Kind: "dns", ConnectionID: target.ConnectionID, Target: &target, IP: ip, State: "pending", Automatic: true}
			if ip == "" {
				delivery.State = "skipped"
				delivery.Reason = "本轮没有该地址族的最终合格 IP，未改动目标"
				delivery.EndedAt = timePointer(service.now())
			}
			if !service.appendDelivery(run.ID, delivery) {
				if service.ctx.Err() != nil {
					return
				}
				break
			}
			if delivery.State == "pending" {
				service.performDelivery(ctx, run.ID, delivery.ID)
			}
		}
	}
	if ctx.Err() == nil {
		service.setAutomaticStage(run.ID, "notifications")
		service.autoNotifications(ctx, run.ID, false)
	}
	cancelSummaryDone := false
	var current Run
	for {
		service.mu.Lock()
		current, err = getRunMetadata(service.db, run.ID)
		if err != nil {
			service.mu.Unlock()
			service.cancel()
			return
		}
		if current.CancelRequested && service.ctx.Err() == nil && !cancelSummaryDone {
			service.mu.Unlock()
			service.autoNotifications(service.ctx, run.ID, true)
			cancelSummaryDone = true
			continue
		}
		break
	}
	defer service.mu.Unlock()
	for _, delivery := range current.Deliveries {
		if delivery.Automatic && !terminalDelivery(delivery.State) {
			service.cancel()
			return
		}
	}
	status := result.Status
	if service.ctx.Err() != nil {
		status = "interrupted"
		current.Reason = "服务停止，未恢复或重放外部操作"
	} else if current.CancelRequested {
		status = "cancelled"
		current.Reason = "使用者停止，已完成资源停止与允许的取消收尾"
	}
	if status != "completed" && status != "no_results" && status != "cancelled" && status != "interrupted" {
		status = "failed"
	}
	now := service.now()
	current.Status = status
	current.Stage = status
	current.SlotBusy = false
	current.EndedAt = &now
	current.RetentionAt = &now
	current.UpdatedAt = &now
	current.ProgressRevision++
	tx, err := service.db.Begin()
	if err == nil {
		err = put(tx, "runs", current.ID, current)
		if err == nil {
			err = service.log(tx, "系统", "run_finished", current.ID, status, current.Reason)
		}
		if err == nil {
			err = tx.Commit()
		}
		_ = tx.Rollback()
	}
	if err != nil {
		service.cancel()
		return
	}
	if cancel := service.cancels[run.ID]; cancel != nil {
		cancel()
		delete(service.cancels, run.ID)
	}
	select {
	case service.wake <- struct{}{}:
	default:
	}
}

func (service *Service) appendDelivery(runID string, delivery Delivery) bool {
	service.mu.Lock()
	defer service.mu.Unlock()
	run, err := getRunMetadata(service.db, runID)
	if err != nil {
		service.cancel()
		return false
	}
	if delivery.Automatic && run.CancelRequested && !delivery.Cancellation {
		return false
	}
	run.Deliveries = append(run.Deliveries, delivery)
	service.updateDeliveryStageLocked(&run, delivery)
	tx, err := service.db.Begin()
	if err != nil {
		service.cancel()
		return false
	}
	defer tx.Rollback()
	err = put(tx, "runs", run.ID, run)
	if err == nil && terminalDelivery(delivery.State) {
		err = service.log(tx, "系统", "delivery_result", delivery.ID, delivery.State, delivery.Reason)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		service.cancel()
		return false
	}
	return true
}

func (service *Service) setAutomaticStage(runID, stage string) {
	service.mu.Lock()
	defer service.mu.Unlock()
	run, err := getRunMetadata(service.db, runID)
	if err != nil {
		return
	}
	if run.CancelRequested {
		stage = "cancellation_notifications"
	}
	service.setStageLocked(&run, stage)
	_ = put(service.db, "runs", run.ID, run)
}

func (service *Service) setStageLocked(run *Run, stage string) {
	total, done := 0, 0
	counts := optimizer.StageCounts{}
	for _, delivery := range run.Deliveries {
		if !delivery.Automatic || (delivery.Kind == "dns") != (stage == "dns") || delivery.Cancellation != (stage == "cancellation_notifications") {
			continue
		}
		switch delivery.State {
		case "success":
			counts.Succeeded++
		case "failed":
			counts.Failed++
		case "skipped":
			counts.Skipped++
		case "unconfirmed":
			counts.Unconfirmed++
		}
	}
	if stage == "dns" {
		total = len(run.Snapshot.Targets)
		for _, delivery := range run.Deliveries {
			if delivery.Automatic && delivery.Kind == "dns" && !delivery.Cancellation && terminalDelivery(delivery.State) {
				done++
			}
		}
	} else {
		for _, channel := range []Channel{run.Snapshot.Notifications.Telegram, run.Snapshot.Notifications.Wecom} {
			if channel.Enabled && channel.ConnectionID != "" {
				total++
			}
		}
		for _, delivery := range run.Deliveries {
			if delivery.Automatic && delivery.Kind != "dns" && (delivery.Cancellation == (stage == "cancellation_notifications")) && terminalDelivery(delivery.State) {
				done++
			}
		}
	}
	unit := "渠道"
	if stage == "dns" {
		unit = "目标记录"
	}
	service.recordProgress(run, (optimizer.Progress{Stage: stage, Done: done, Total: &total, Unit: unit, Message: "已处理不等于成功；未确认或跳过也作为已有结论记录"}).WithCounts(counts))
}

func (service *Service) recordProgress(run *Run, progress optimizer.Progress) {
	progress.Message = safeMessage(progress.Message)
	run.Stage = progress.Stage
	run.Progress = progress
	run.ProgressRevision++
	run.UpdatedAt = timePointer(service.now())
	for index, previous := range run.StageProgress {
		if previous.Stage == progress.Stage && previous.Family == progress.Family && previous.Unit == progress.Unit {
			if progress.Counts == nil {
				progress.Counts = previous.Counts
			}
			run.StageProgress[index] = progress
			return
		}
	}
	run.StageProgress = append(run.StageProgress, progress)
}

func terminalDelivery(state string) bool {
	return state == "success" || state == "failed" || state == "unconfirmed" || state == "skipped"
}

func (service *Service) autoNotifications(ctx context.Context, runID string, cancellation bool) {
	service.mu.Lock()
	run, err := getRunMetadata(service.db, runID)
	service.mu.Unlock()
	if err != nil {
		service.cancel()
		return
	}
	for kind, channel := range map[string]Channel{"telegram": run.Snapshot.Notifications.Telegram, "wecom": run.Snapshot.Notifications.Wecom} {
		if !channel.Enabled || channel.ConnectionID == "" || ctx.Err() != nil {
			continue
		}
		exists := false
		for _, delivery := range run.Deliveries {
			if delivery.Automatic && delivery.Kind == kind && delivery.Cancellation == cancellation {
				exists = true
			}
		}
		if exists {
			continue
		}
		delivery := Delivery{ID: randomID(), Kind: kind, ConnectionID: channel.ConnectionID, State: "pending", Automatic: true, Cancellation: cancellation}
		if !service.appendDelivery(runID, delivery) {
			return
		}
		service.performDelivery(ctx, runID, delivery.ID)
	}
}

func (service *Service) cancelRun(tx *sql.Tx, id string) (string, any, error) {
	run, err := getRunMetadata(tx, id)
	if err != nil {
		return "", nil, err
	}
	if !active(run.Status) {
		return "", nil, conflict("执行已经结束，不能再停止")
	}
	run.CancelRequested = true
	if run.Status == "queued" {
		now := service.now()
		run.Status = "cancelled"
		run.Stage = "cancelled"
		run.EndedAt = &now
		run.RetentionAt = &now
		run.Reason = "使用者取消排队，未取得运行席位"
		for kind, channel := range map[string]Channel{"telegram": run.Snapshot.Notifications.Telegram, "wecom": run.Snapshot.Notifications.Wecom} {
			if channel.Enabled && channel.ConnectionID != "" {
				run.Deliveries = append(run.Deliveries, Delivery{ID: randomID(), Kind: kind, ConnectionID: channel.ConnectionID, State: "pending", Cancellation: true})
			}
		}
	}
	return run.ID, run, put(tx, "runs", run.ID, run)
}
