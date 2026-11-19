package management

import (
	"context"
	"errors"
	"time"

	"github.com/masgzy/CloudflareST/internal/integrations"
	"github.com/masgzy/CloudflareST/internal/optimizer"
)

type Schedule struct {
	Enabled         bool       `json:"enabled"`
	Kind            string     `json:"kind"`
	Expression      string     `json:"expression"`
	IntervalMinutes int        `json:"interval_minutes"`
	TimeOfDay       string     `json:"time_of_day"`
	Weekday         int        `json:"weekday"`
	NextRunAt       *time.Time `json:"next_run_at,omitempty"`
}

type Channel struct {
	Enabled      bool   `json:"enabled"`
	ConnectionID string `json:"connection_id"`
}

type Notifications struct {
	Telegram Channel `json:"telegram"`
	Wecom    Channel `json:"wecom"`
}

type Task struct {
	ConfigurationIssue string           `json:"configuration_issue,omitempty"`
	ID                 string           `json:"id"`
	Name               string           `json:"name"`
	Revision           int              `json:"revision"`
	Archived           bool             `json:"archived"`
	Config             optimizer.Config `json:"config"`
	Schedule           Schedule         `json:"schedule"`
	DNSAuto            bool             `json:"dns_auto"`
	Targets            []Target         `json:"targets"`
	Notifications      Notifications    `json:"notifications"`
}

type LastWrite struct {
	TaskID    string    `json:"task_id"`
	RunID     string    `json:"run_id"`
	WrittenAt time.Time `json:"written_at"`
	IP        string    `json:"ip"`
}

type Protection struct {
	Active    bool      `json:"active"`
	Revision  int       `json:"revision"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
	AttemptID string    `json:"attempt_id"`
}

type Target struct {
	integrations.Target
	TargetID       string      `json:"target_id"`
	LastKnownWrite *LastWrite  `json:"last_known_write,omitempty"`
	Protection     *Protection `json:"protection,omitempty"`
}

type Run struct {
	ID               string               `json:"id"`
	TaskID           string               `json:"task_id"`
	TaskName         string               `json:"task_name"`
	Trigger          string               `json:"trigger"`
	Status           string               `json:"status"`
	Stage            string               `json:"stage"`
	SlotBusy         bool                 `json:"slot_busy"`
	QueuedAt         time.Time            `json:"queued_at"`
	StartedAt        *time.Time           `json:"started_at,omitempty"`
	EndedAt          *time.Time           `json:"ended_at,omitempty"`
	RetentionAt      *time.Time           `json:"retention_at,omitempty"`
	DeletedAt        *time.Time           `json:"deleted_at,omitempty"`
	CancelRequested  bool                 `json:"cancel_requested"`
	OperationID      string               `json:"operation_id,omitempty"`
	Reason           string               `json:"reason,omitempty"`
	Snapshot         Task                 `json:"snapshot"`
	Progress         optimizer.Progress   `json:"progress"`
	ProgressRevision int                  `json:"progress_revision"`
	Result           optimizer.Result     `json:"result"`
	Deliveries       []Delivery           `json:"deliveries"`
	Sequence         int64                `json:"sequence"`
	CandidateSummary *CandidateSummary    `json:"candidate_summary,omitempty"`
	StageProgress    []optimizer.Progress `json:"stage_progress,omitempty"`
	UpdatedAt        *time.Time           `json:"updated_at,omitempty"`
	ServerTime       *time.Time           `json:"server_time,omitempty"`
}

type FamilySummary struct {
	Family   string               `json:"family"`
	Count    int                  `json:"count"`
	Eligible int                  `json:"eligible"`
	Best     *optimizer.Candidate `json:"best,omitempty"`
}

type CandidateSummary struct {
	Total    int             `json:"total"`
	Families []FamilySummary `json:"families"`
}

type Delivery struct {
	ID             string                       `json:"id"`
	OperationID    string                       `json:"operation_id,omitempty"`
	Kind           string                       `json:"kind"`
	ConnectionID   string                       `json:"connection_id"`
	Target         *Target                      `json:"target,omitempty"`
	IP             string                       `json:"ip,omitempty"`
	State          string                       `json:"state"`
	Verified       bool                         `json:"verified"`
	NoWrite        bool                         `json:"no_write,omitempty"`
	Reason         string                       `json:"reason,omitempty"`
	Automatic      bool                         `json:"automatic"`
	Cancellation   bool                         `json:"cancellation"`
	PriorAttemptID string                       `json:"prior_attempt_id,omitempty"`
	StartedAt      *time.Time                   `json:"started_at,omitempty"`
	EndedAt        *time.Time                   `json:"ended_at,omitempty"`
	Before         *integrations.DNSObservation `json:"before,omitempty"`
	Readback       *integrations.DNSObservation `json:"readback,omitempty"`
}

type Connection struct {
	ID             string   `json:"id"`
	Kind           string   `json:"kind"`
	Name           string   `json:"name"`
	AccountID      string   `json:"account_id"`
	Revision       int      `json:"revision"`
	CredentialsSet bool     `json:"credentials_set"`
	ReferencedBy   []string `json:"referenced_by"`
	Ciphertext     string   `json:"ciphertext,omitempty"`
}

type Settings struct {
	Timezone    string `json:"timezone"`
	HistoryDays int    `json:"history_days"`
}

type Operation struct {
	RecoveryOperationID string             `json:"recovery_operation_id,omitempty"`
	Recovery            *operationRecovery `json:"recovery,omitempty"`
	PriorOperationID    string             `json:"prior_operation_id,omitempty"`
	RecoveryID          string             `json:"recovery_id,omitempty"`
	ID                  string             `json:"operation_id"`
	ObjectID            string             `json:"object_id,omitempty"`
	State               string             `json:"state"`
	Result              any                `json:"result,omitempty"`
	RequestHash         string             `json:"request_hash,omitempty"`
	CreatedAt           time.Time          `json:"created_at"`
	Action              string             `json:"action,omitempty"`
}

type Log struct {
	ID         string    `json:"id"`
	At         time.Time `json:"at"`
	Actor      string    `json:"actor"`
	Action     string    `json:"action"`
	ActionKind string    `json:"action_kind,omitempty"`
	Object     string    `json:"object"`
	Result     string    `json:"result"`
	Reason     string    `json:"reason,omitempty"`
}

type Engine interface {
	Run(context.Context, optimizer.Config, func(optimizer.Progress)) (optimizer.Result, error)
}

type Provider interface {
	Domains(context.Context, integrations.Connection) ([]string, error)
	Records(context.Context, integrations.Connection, string) ([]integrations.Target, error)
	Update(context.Context, integrations.Connection, integrations.Target, string) integrations.Outcome
	Notify(context.Context, integrations.Connection, string) integrations.Outcome
}

type Config struct {
	DataDir        string
	RuntimeDir     string
	TrustedProxies []string
	Password       string
	SecureCookie   bool
	Engine         Engine
	Provider       Provider
	Now            func() time.Time
}

type apiError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (failure *apiError) Error() string { return failure.Message }

func invalid(message string) error  { return &apiError{400, "invalid_request", message} }
func conflict(message string) error { return &apiError{409, "conflict", message} }

var errNotFound = errors.New("对象不存在")

func active(status string) bool { return status == "queued" || status == "running" }

func isRunCancelled(run Run) bool { return run.CancelRequested || run.Status == "cancelled" }
