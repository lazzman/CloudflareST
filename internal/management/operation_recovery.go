package management

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type operationRecovery struct {
	ID         string    `json:"id"`
	PriorID    string    `json:"prior_operation_id"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	CreatedAt  time.Time `json:"created_at"`
	ConsumedBy string    `json:"consumed_by,omitempty"`
}

type recoveryReference struct {
	PriorID    string `json:"prior_operation_id"`
	RecoveryID string `json:"recovery_id"`
}

var recoveryScope = regexp.MustCompile(`^/api/v1/(tasks(?:/[A-Za-z0-9_-]+(?:/(?:runs|archive|restore|schedule-state))?)?|connections(?:/[A-Za-z0-9_-]+)?|runs/[A-Za-z0-9_-]+(?:/(?:cancel|restore|dns-deliveries|notifications))?|settings|dns-targets/[A-Za-z0-9_-]+/auto-write-protection/clear)$`)

func validRecoveryScope(method, path string) bool {
	if !recoveryScope.MatchString(path) {
		return false
	}
	return method == "POST" || method == "PATCH" || method == "DELETE"
}

func recoverableAction(action string) string {
	fields := strings.Fields(action)
	if len(fields) >= 2 && validRecoveryScope(fields[0], fields[1]) {
		return fields[0] + " " + fields[1]
	}
	return ""
}

func (service *Service) handleOperationRecovery(response http.ResponseWriter, request *http.Request, priorID string, body []byte, operationID string) {
	var input struct {
		Method string `json:"method"`
		Path   string `json:"path"`
		Risk   bool   `json:"risk_acknowledged"`
	}
	if json.Unmarshal(body, &input) != nil || !input.Risk || !validRecoveryScope(input.Method, input.Path) {
		writeError(response, invalid("请核查原对象、明确新尝试范围，并确认原动作可能已执行的风险"))
		return
	}
	if len(priorID) < 8 || len(priorID) > 128 || priorID == operationID {
		writeError(response, invalid("原操作号无效或与本次核查号相同"))
		return
	}
	operation, _, err := service.mutate(operationID, "POST "+request.URL.Path, body, func(tx *sql.Tx) (string, any, error) {
		prior, err := get[Operation](tx, "operations", priorID)
		if err != nil && !errors.Is(err, errNotFound) {
			return "", nil, err
		}
		if err == nil {
			fields := strings.Fields(prior.Action)
			if len(fields) >= 2 && (fields[0] != input.Method || fields[1] != input.Path) {
				return "", nil, conflict("新尝试范围与原收据不一致，请核对原动作")
			}
			if prior.RecoveryOperationID != "" || prior.State != "unconfirmed" && prior.State != "expired" && prior.State != "unknown" {
				return "", nil, conflict("原动作已受理、已确认或已转人工处理，请查看原收据，不可再次领取恢复许可")
			}
		} else {
			prior = Operation{ID: priorID, State: "unknown", Action: input.Method + " " + input.Path, CreatedAt: service.now()}
		}
		prior.RecoveryOperationID = operationID
		if err = put(tx, "operations", priorID, prior); err != nil {
			return "", nil, err
		}
		recovery := operationRecovery{ID: operationID, PriorID: priorID, Method: input.Method, Path: input.Path, CreatedAt: service.now()}
		if err = put(tx, "operation_recoveries", operationID, recovery); err != nil {
			return "", nil, err
		}
		if err = service.log(tx, "共享管理身份", "operation_recovery", priorID, "reviewed", "已核查原对象并确认重复风险；仅许可一次同范围新尝试，不执行原动作"); err != nil {
			return "", nil, err
		}
		return priorID, recovery, nil
	})
	if err != nil {
		writeError(response, err)
		return
	}
	service.mu.Lock()
	err = service.attachRecovery(&operation)
	service.mu.Unlock()
	if err != nil {
		writeError(response, err)
		return
	}
	operation.RequestHash = ""
	writeJSON(response, 202, operation)
}

func (service *Service) attachRecovery(operation *Operation) error {
	id := operation.RecoveryOperationID
	if id == "" {
		id = operation.ID
	}
	recovery, err := get[operationRecovery](service.db, "operation_recoveries", id)
	if errors.Is(err, errNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	operation.Recovery = &recovery
	if id == operation.ID {
		operation.Result = recovery
	}
	return nil
}

func (service *Service) consumeRecovery(tx *sql.Tx, operationID, action string, body []byte) (recoveryReference, error) {
	var reference recoveryReference
	if err := json.Unmarshal(body, &reference); err != nil {
		return reference, nil
	}
	if reference.PriorID == "" && reference.RecoveryID == "" {
		return reference, nil
	}
	if reference.PriorID == "" || reference.RecoveryID == "" || reference.PriorID == operationID {
		return reference, invalid("人工新尝试必须携带原操作号和核查许可")
	}
	recovery, err := get[operationRecovery](tx, "operation_recoveries", reference.RecoveryID)
	if err != nil {
		return reference, conflict("核查许可不可用，请保留原操作号重新核查")
	}
	fields := strings.Fields(action)
	if len(fields) < 2 || fields[0] != recovery.Method || fields[1] != recovery.Path || recovery.PriorID != reference.PriorID || recovery.ConsumedBy != "" {
		return reference, conflict("核查许可范围不符或已使用，不执行本次新动作")
	}
	recovery.ConsumedBy = operationID
	return reference, put(tx, "operation_recoveries", recovery.ID, recovery)
}
