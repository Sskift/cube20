package manager

import (
	"sort"
	"strings"
	"time"

	"cube20/internal/usage"
)

const UsageEventSchemaVersion = 1

// UsageEventKey is the durable storage partition for usage events:
// account_id, client_id, lease_id, run_id.
type UsageEventKey struct {
	AccountID string `json:"accountId"`
	ClientID  string `json:"clientId,omitempty"`
	LeaseID   string `json:"leaseId,omitempty"`
	RunID     string `json:"runId,omitempty"`
}

type UsageEventContext struct {
	AccountID  string    `json:"accountId"`
	ClientID   string    `json:"clientId,omitempty"`
	LeaseID    string    `json:"leaseId,omitempty"`
	RunID      string    `json:"runId,omitempty"`
	ReportedAt time.Time `json:"reportedAt,omitempty"`
}

type UsageEvent struct {
	SchemaVersion int       `json:"schemaVersion"`
	AccountID     string    `json:"accountId"`
	ClientID      string    `json:"clientId,omitempty"`
	LeaseID       string    `json:"leaseId,omitempty"`
	RunID         string    `json:"runId,omitempty"`
	ReportedAt    time.Time `json:"reportedAt,omitempty"`

	Model     string       `json:"model"`
	LatestAt  string       `json:"latestAt,omitempty"`
	Today     usage.Tokens `json:"today"`
	SevenDays usage.Tokens `json:"sevenDays"`
	AllTime   usage.Tokens `json:"allTime"`

	SummaryStatus       string `json:"summaryStatus,omitempty"`
	SummaryDetail       string `json:"summaryDetail,omitempty"`
	SummaryFilesScanned int    `json:"summaryFilesScanned,omitempty"`
	SummaryEvents       int    `json:"summaryEvents,omitempty"`
	SummaryLatestAt     string `json:"summaryLatestAt,omitempty"`
	SummaryLatestModel  string `json:"summaryLatestModel,omitempty"`
}

type UsageEventModelKey struct {
	AccountID string `json:"accountId"`
	ClientID  string `json:"clientId,omitempty"`
	LeaseID   string `json:"leaseId,omitempty"`
	RunID     string `json:"runId,omitempty"`
	Model     string `json:"model"`
}

func NewUsageEventContext(accountID, clientID, leaseID, runID string, reportedAt time.Time) UsageEventContext {
	return UsageEventContext{
		AccountID:  strings.TrimSpace(accountID),
		ClientID:   strings.TrimSpace(clientID),
		LeaseID:    strings.TrimSpace(leaseID),
		RunID:      strings.TrimSpace(runID),
		ReportedAt: reportedAt,
	}
}

func (context UsageEventContext) Key() UsageEventKey {
	return UsageEventKey{
		AccountID: strings.TrimSpace(context.AccountID),
		ClientID:  strings.TrimSpace(context.ClientID),
		LeaseID:   strings.TrimSpace(context.LeaseID),
		RunID:     strings.TrimSpace(context.RunID),
	}
}

func (event UsageEvent) Key() UsageEventKey {
	return UsageEventKey{
		AccountID: strings.TrimSpace(event.AccountID),
		ClientID:  strings.TrimSpace(event.ClientID),
		LeaseID:   strings.TrimSpace(event.LeaseID),
		RunID:     strings.TrimSpace(event.RunID),
	}
}

func (event UsageEvent) ModelKey() UsageEventModelKey {
	return UsageEventModelKey{
		AccountID: strings.TrimSpace(event.AccountID),
		ClientID:  strings.TrimSpace(event.ClientID),
		LeaseID:   strings.TrimSpace(event.LeaseID),
		RunID:     strings.TrimSpace(event.RunID),
		Model:     usageEventModel(event.Model, ""),
	}
}

func UsageEventsFromSummary(context UsageEventContext, summary usage.Summary) []UsageEvent {
	key := context.Key()
	models := usageEventModels(summary)
	events := make([]UsageEvent, 0, len(models))
	for _, model := range models {
		events = append(events, UsageEvent{
			SchemaVersion:       UsageEventSchemaVersion,
			AccountID:           key.AccountID,
			ClientID:            key.ClientID,
			LeaseID:             key.LeaseID,
			RunID:               key.RunID,
			ReportedAt:          context.ReportedAt,
			Model:               model.Model,
			LatestAt:            model.LatestAt,
			Today:               model.Today,
			SevenDays:           model.SevenDays,
			AllTime:             model.AllTime,
			SummaryStatus:       strings.TrimSpace(summary.Status),
			SummaryDetail:       strings.TrimSpace(summary.Detail),
			SummaryFilesScanned: summary.FilesScanned,
			SummaryEvents:       summary.Events,
			SummaryLatestAt:     strings.TrimSpace(summary.LatestAt),
			SummaryLatestModel:  usageEventModel(summary.LatestModel, ""),
		})
	}
	return events
}

func usageEventRunID(summary usage.Summary, reportedAt time.Time) string {
	if latestAt := strings.TrimSpace(summary.LatestAt); latestAt != "" {
		return latestAt
	}
	if !reportedAt.IsZero() {
		return reportedAt.UTC().Format(time.RFC3339Nano)
	}
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func usageEventModels(summary usage.Summary) []usage.ModelUsage {
	if len(summary.Models) == 0 {
		return []usage.ModelUsage{{
			Model:     usageEventModel(summary.LatestModel, "unknown"),
			Today:     summary.Today,
			SevenDays: summary.SevenDays,
			AllTime:   summary.AllTime,
			LatestAt:  strings.TrimSpace(summary.LatestAt),
		}}
	}

	byModel := map[string]usage.ModelUsage{}
	for _, model := range summary.Models {
		name := usageEventModel(model.Model, "unknown")
		current := byModel[name]
		current.Model = name
		current.Today = addUsageEventTokens(current.Today, model.Today)
		current.SevenDays = addUsageEventTokens(current.SevenDays, model.SevenDays)
		current.AllTime = addUsageEventTokens(current.AllTime, model.AllTime)
		current.LatestAt = latestUsageEventTime(current.LatestAt, model.LatestAt)
		byModel[name] = current
	}

	models := make([]usage.ModelUsage, 0, len(byModel))
	for _, model := range byModel {
		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool {
		return models[i].Model < models[j].Model
	})
	return models
}

func usageEventModel(model, fallback string) string {
	model = strings.TrimSpace(model)
	if model != "" {
		return model
	}
	return strings.TrimSpace(fallback)
}

func addUsageEventTokens(left, right usage.Tokens) usage.Tokens {
	return usage.Tokens{
		Input:       left.Input + right.Input,
		CachedInput: left.CachedInput + right.CachedInput,
		Output:      left.Output + right.Output,
		Total:       left.Total + right.Total,
	}
}

func latestUsageEventTime(left, right string) string {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	leftTime, leftErr := time.Parse(time.RFC3339Nano, left)
	rightTime, rightErr := time.Parse(time.RFC3339Nano, right)
	if leftErr != nil || rightErr != nil {
		if right > left {
			return right
		}
		return left
	}
	if rightTime.After(leftTime) {
		return right
	}
	return left
}
