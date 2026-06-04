package manager

import (
	"testing"
	"time"

	"cube20/internal/usage"
)

func TestUsageEventsFromSummaryAggregatesModels(t *testing.T) {
	reportedAt := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	context := NewUsageEventContext(" acct-1 ", " client-1 ", " lease-1 ", " run-1 ", reportedAt)
	summary := usage.Summary{
		Status:       "ok",
		Detail:       "parsed",
		FilesScanned: 3,
		Events:       4,
		LatestAt:     "2026-06-04T11:30:00Z",
		LatestModel:  " gpt-5 ",
		Models: []usage.ModelUsage{
			{
				Model:     "gpt-5",
				Today:     usage.Tokens{Input: 10, CachedInput: 2, Output: 3, Total: 13},
				SevenDays: usage.Tokens{Input: 20, CachedInput: 4, Output: 6, Total: 26},
				AllTime:   usage.Tokens{Input: 30, CachedInput: 6, Output: 9, Total: 39},
				LatestAt:  "2026-06-04T10:00:00Z",
			},
			{
				Model:     "o3",
				Today:     usage.Tokens{Input: 1, Output: 2, Total: 3},
				SevenDays: usage.Tokens{Input: 3, Output: 4, Total: 7},
				AllTime:   usage.Tokens{Input: 5, Output: 6, Total: 11},
				LatestAt:  "2026-06-04T09:00:00Z",
			},
			{
				Model:     " gpt-5 ",
				Today:     usage.Tokens{Input: 7, CachedInput: 1, Output: 8, Total: 15},
				SevenDays: usage.Tokens{Input: 11, CachedInput: 2, Output: 12, Total: 23},
				AllTime:   usage.Tokens{Input: 13, CachedInput: 3, Output: 14, Total: 27},
				LatestAt:  "2026-06-04T11:00:00Z",
			},
		},
	}

	events := UsageEventsFromSummary(context, summary)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	gpt := events[0]
	if gpt.Model != "gpt-5" {
		t.Fatalf("expected first model gpt-5, got %q", gpt.Model)
	}
	if gpt.Key() != (UsageEventKey{AccountID: "acct-1", ClientID: "client-1", LeaseID: "lease-1", RunID: "run-1"}) {
		t.Fatalf("unexpected event key: %#v", gpt.Key())
	}
	if gpt.ModelKey().Model != "gpt-5" {
		t.Fatalf("unexpected model key: %#v", gpt.ModelKey())
	}
	if !gpt.ReportedAt.Equal(reportedAt) {
		t.Fatalf("expected reportedAt %s, got %s", reportedAt, gpt.ReportedAt)
	}
	if gpt.SchemaVersion != UsageEventSchemaVersion {
		t.Fatalf("expected schema version %d, got %d", UsageEventSchemaVersion, gpt.SchemaVersion)
	}
	if gpt.Today != (usage.Tokens{Input: 17, CachedInput: 3, Output: 11, Total: 28}) {
		t.Fatalf("unexpected today totals: %#v", gpt.Today)
	}
	if gpt.SevenDays != (usage.Tokens{Input: 31, CachedInput: 6, Output: 18, Total: 49}) {
		t.Fatalf("unexpected seven day totals: %#v", gpt.SevenDays)
	}
	if gpt.AllTime != (usage.Tokens{Input: 43, CachedInput: 9, Output: 23, Total: 66}) {
		t.Fatalf("unexpected all time totals: %#v", gpt.AllTime)
	}
	if gpt.LatestAt != "2026-06-04T11:00:00Z" {
		t.Fatalf("unexpected latestAt: %q", gpt.LatestAt)
	}
	if gpt.SummaryStatus != "ok" || gpt.SummaryDetail != "parsed" || gpt.SummaryFilesScanned != 3 || gpt.SummaryEvents != 4 {
		t.Fatalf("unexpected summary metadata: %#v", gpt)
	}
	if gpt.SummaryLatestAt != "2026-06-04T11:30:00Z" || gpt.SummaryLatestModel != "gpt-5" {
		t.Fatalf("unexpected summary latest metadata: %#v", gpt)
	}

	o3 := events[1]
	if o3.Model != "o3" {
		t.Fatalf("expected second model o3, got %q", o3.Model)
	}
	if o3.Today != (usage.Tokens{Input: 1, Output: 2, Total: 3}) {
		t.Fatalf("unexpected o3 totals: %#v", o3.Today)
	}
}

func TestUsageEventsFromSummaryFallsBackToSummaryTotals(t *testing.T) {
	summary := usage.Summary{
		Status:      "ok",
		LatestAt:    "2026-06-04T11:30:00Z",
		LatestModel: "gpt-5",
		Today:       usage.Tokens{Input: 10, Output: 5, Total: 15},
		SevenDays:   usage.Tokens{Input: 20, Output: 10, Total: 30},
		AllTime:     usage.Tokens{Input: 30, Output: 15, Total: 45},
	}

	events := UsageEventsFromSummary(UsageEventContext{AccountID: "acct-1"}, summary)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	event := events[0]
	if event.Model != "gpt-5" {
		t.Fatalf("expected fallback model gpt-5, got %q", event.Model)
	}
	if event.Today != summary.Today || event.SevenDays != summary.SevenDays || event.AllTime != summary.AllTime {
		t.Fatalf("fallback event did not copy summary totals: %#v", event)
	}
	if event.LatestAt != summary.LatestAt {
		t.Fatalf("expected latestAt %q, got %q", summary.LatestAt, event.LatestAt)
	}
}
