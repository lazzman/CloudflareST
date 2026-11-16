package management

import (
	"strings"
)

func (service *Service) settleOperations() error {
	return service.settleOperationsUsing(service.db)
}

func (service *Service) settleOperationsUsing(query querier) error {
	operations, err := all[Operation](query, "operations")
	if err != nil {
		return err
	}
	for _, operation := range operations {
		if operation.State != "accepted" {
			continue
		}
		fields := strings.Fields(operation.Action)
		if len(fields) < 2 {
			continue
		}
		path := fields[1]
		run, err := getRunMetadata(query, operation.ObjectID)
		if err == errNotFound {
			continue
		}
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, "/runs") || strings.HasSuffix(path, "/cancel") {
			if active(run.Status) || deliveryBusy(run) {
				continue
			}
			operation.State = "completed"
			operation.Result = map[string]any{"run_id": run.ID, "status": run.Status, "slot_busy": run.SlotBusy}
		} else if strings.HasSuffix(path, "/dns-deliveries") || strings.HasSuffix(path, "/notifications") {
			pending := false
			found := false
			uncertain := false
			results := []map[string]string{}
			for _, delivery := range run.Deliveries {
				if delivery.OperationID != operation.ID {
					continue
				}
				found = true
				if delivery.State == "pending" || delivery.State == "dispatching" || delivery.State == "running" {
					pending = true
				}
				if delivery.State == "unconfirmed" {
					uncertain = true
				}
				results = append(results, map[string]string{"id": delivery.ID, "state": delivery.State, "reason": delivery.Reason})
			}
			if !found || pending {
				continue
			}
			operation.State = "completed"
			if uncertain {
				operation.State = "unconfirmed"
			}
			operation.Result = map[string]any{"run_id": run.ID, "deliveries": results}
		} else {
			operation.State = "completed"
		}
		if err = put(query, "operations", operation.ID, operation); err != nil {
			return err
		}
	}
	return nil
}
