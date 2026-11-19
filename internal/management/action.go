package management

import (
	"net/http"
	"strings"
)

const actionKindOther = "other"

var actionKinds = map[string]struct{}{
	"task_create":           {},
	"task_update":           {},
	"task_archive":          {},
	"task_restore":          {},
	"schedule_update":       {},
	"run_start":             {},
	"run_cancel":            {},
	"run_finished":          {},
	"dns_delivery":          {},
	"notification_delivery": {},
	"delivery_result":       {},
	"dns_protection_clear":  {},
	"history_delete":        {},
	"history_restore":       {},
	"history_expire":        {},
	"history_purge":         {},
	"connection_create":     {},
	"connection_update":     {},
	"connection_delete":     {},
	"settings_update":       {},
	"schedule_trigger":      {},
	"schedule_skipped":      {},
	"restart_recovery":      {},
	"operation_recovery":    {},
	"other":                 {},
}

func knownActionKind(value string) bool {
	_, ok := actionKinds[value]
	return ok
}

func actionKind(action string) string {
	fields := strings.Fields(strings.TrimSpace(action))
	if len(fields) == 0 {
		return actionKindOther
	}
	if len(fields) == 1 {
		if knownActionKind(fields[0]) {
			return fields[0]
		}
		return actionKindOther
	}
	method, path := fields[0], fields[1]
	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/api/v1/"), "/"), "/")
	switch {
	case method == http.MethodPost && len(parts) == 1 && parts[0] == "tasks":
		return "task_create"
	case method == http.MethodPatch && len(parts) == 2 && parts[0] == "tasks":
		return "task_update"
	case method == http.MethodPost && len(parts) == 3 && parts[0] == "tasks":
		switch parts[2] {
		case "runs":
			return "run_start"
		case "archive":
			return "task_archive"
		case "restore":
			return "task_restore"
		case "schedule-state":
			return "schedule_update"
		}
	case method == http.MethodPost && len(parts) == 3 && parts[0] == "runs":
		switch parts[2] {
		case "cancel":
			return "run_cancel"
		case "restore":
			return "history_restore"
		case "dns-deliveries":
			return "dns_delivery"
		case "notifications":
			return "notification_delivery"
		}
	case method == http.MethodDelete && len(parts) == 2 && parts[0] == "runs":
		return "history_delete"
	case method == http.MethodPost && len(parts) == 4 && parts[0] == "dns-targets" && parts[2] == "auto-write-protection" && parts[3] == "clear":
		return "dns_protection_clear"
	case method == http.MethodPatch && len(parts) == 1 && parts[0] == "settings":
		return "settings_update"
	case len(parts) == 1 && parts[0] == "connections":
		if method == http.MethodPost {
			return "connection_create"
		}
	case len(parts) == 2 && parts[0] == "connections":
		switch method {
		case http.MethodPatch:
			return "connection_update"
		case http.MethodDelete:
			return "connection_delete"
		}
	case method == http.MethodPost && len(parts) == 3 && parts[0] == "operations" && parts[2] == "recovery":
		return "operation_recovery"
	}
	return actionKindOther
}

func logActionMatches(action, filter string) bool {
	if filter == "" {
		return true
	}
	if knownActionKind(filter) {
		return actionKind(action) == filter
	}
	return strings.Contains(action, filter)
}
