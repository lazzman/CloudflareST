package management

import (
	"database/sql"
	"time"
)

func deliveryBusy(run Run) bool {
	for _, delivery := range run.Deliveries {
		if delivery.State == "pending" || delivery.State == "dispatching" || delivery.State == "running" {
			return true
		}
	}
	return false
}

func (service *Service) historyAction(tx *sql.Tx, id string, restore bool) (string, any, error) {
	if err := service.settleOperationsUsing(tx); err != nil {
		return "", nil, err
	}
	run, err := getRunMetadata(tx, id)
	if err != nil {
		return "", nil, err
	}
	if active(run.Status) || deliveryBusy(run) {
		return "", nil, conflict("执行或投递仍在处理中，不能删除或恢复记录")
	}
	now := service.now()
	if restore {
		if run.DeletedAt == nil {
			return "", nil, conflict("该记录不在回收站")
		}
		if !now.Before(run.DeletedAt.Add(7 * 24 * time.Hour)) {
			return "", nil, conflict("7天恢复期已过，不能恢复")
		}
		run.DeletedAt = nil
		run.RetentionAt = &now
		if err = service.log(tx, "共享管理身份", "history_restore", run.ID, "restored", "运行历史已恢复；不会重跑或重放外部操作"); err != nil {
			return "", nil, err
		}
	} else {
		if run.DeletedAt != nil {
			return "", nil, conflict("该记录已在回收站")
		}
		run.DeletedAt = &now
		if err = service.log(tx, "共享管理身份", "history_delete", run.ID, "recycled", "运行历史进入7天回收站；不撤销外部操作"); err != nil {
			return "", nil, err
		}
	}
	return run.ID, map[string]any{"id": run.ID, "deleted_at": run.DeletedAt, "retention_at": run.RetentionAt}, put(tx, "runs", run.ID, run)
}

func (service *Service) cleanup() error {
	tx, err := service.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = service.settleOperationsUsing(tx); err != nil {
		return err
	}
	settings, err := get[Settings](tx, "settings", "system")
	if err != nil {
		return err
	}
	now := service.now()
	runs, err := all[Run](tx, "runs")
	if err != nil {
		return err
	}
	for _, run := range runs {
		if active(run.Status) || deliveryBusy(run) {
			continue
		}
		if run.DeletedAt != nil {
			if !now.Before(run.DeletedAt.Add(7 * 24 * time.Hour)) {
				if err = remove(tx, "runs", run.ID); err != nil {
					return err
				}
				if err = service.log(tx, "系统", "history_purge", run.ID, "removed", "回收站7天到期，DNS保护和最近写入信息独立保留"); err != nil {
					return err
				}
			}
			continue
		}
		anchor := run.RetentionAt
		if anchor == nil {
			anchor = run.EndedAt
		}
		if anchor != nil && !now.Before(anchor.Add(time.Duration(settings.HistoryDays)*24*time.Hour)) {
			run.DeletedAt = &now
			if err = put(tx, "runs", run.ID, run); err != nil {
				return err
			}
			if err = service.log(tx, "系统", "history_expire", run.ID, "recycled", "超过记录保留期，移入7天回收站"); err != nil {
				return err
			}
		}
	}
	logs, err := all[Log](tx, "logs")
	if err != nil {
		return err
	}
	for _, entry := range logs {
		if !now.Before(entry.At.Add(30 * 24 * time.Hour)) {
			if err = remove(tx, "logs", entry.ID); err != nil {
				return err
			}
		}
	}
	operations, err := all[Operation](tx, "operations")
	if err != nil {
		return err
	}
	for _, operation := range operations {
		if operation.State != "accepted" && operation.State != "expired" && !now.Before(operation.CreatedAt.Add(30*24*time.Hour)) {
			operation.State = "expired"
			operation.Result = nil
			if err = put(tx, "operations", operation.ID, operation); err != nil {
				return err
			}
		}
	}
	sessions, err := all[sessionRecord](tx, "sessions")
	if err != nil {
		return err
	}
	for _, session := range sessions {
		if !now.Before(session.ExpiresAt) || session.PasswordVersion != service.passwordVersion {
			if err = remove(tx, "sessions", session.ID); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
