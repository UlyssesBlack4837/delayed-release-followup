package releaseflow

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type fakeInfrai struct {
	cronExpr   string
	taskURL    string
	deletedJob string
	queue      string
	payload    any
	keys       []string
}

func (f *fakeInfrai) CreateCron(_ context.Context, expr, task, key string) (string, error) {
	f.cronExpr, f.taskURL = expr, task
	f.keys = append(f.keys, key)
	return "job_42", nil
}

func (f *fakeInfrai) DeleteCron(_ context.Context, jobID, key string) error {
	f.deletedJob = jobID
	f.keys = append(f.keys, key)
	return nil
}

func (f *fakeInfrai) Publish(_ context.Context, queue string, payload any, key string) error {
	f.queue = queue
	f.payload = payload
	f.keys = append(f.keys, key)
	return nil
}

func TestScheduleAndDispatchRelease(t *testing.T) {
	tests := []struct {
		name       string
		now        time.Time
		delayHours int
		wantCron   string
		wantTime   string
	}{
		{"same UTC day", time.Date(2026, 9, 5, 10, 17, 45, 0, time.UTC), 4, "18 14 5 9 *", "2026-09-05T14:18:00Z"},
		{"cross month boundary", time.Date(2026, 9, 30, 23, 55, 0, 0, time.UTC), 2, "55 1 1 10 *", "2026-10-01T01:55:00Z"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &fakeInfrai{}
			store := NewStore(filepath.Join(t.TempDir(), "releases.json"))
			scheduler := NewScheduler(api, store, "https://releases.example.com")
			scheduler.now = func() time.Time { return tt.now }
			scheduler.newToken = func() (string, error) { return "token42", nil }
			build := BuildEvent{Repository: "ledger", CommitSHA: "abc123", Artifact: "registry.example.com/ledger:abc123", DelayHours: tt.delayHours}

			release, err := scheduler.Schedule(context.Background(), build)
			if err != nil {
				t.Fatal(err)
			}
			if api.cronExpr != tt.wantCron || release.ExecuteAt.Format(time.RFC3339) != tt.wantTime {
				t.Fatalf("schedule = %q at %s, want %q at %s", api.cronExpr, release.ExecuteAt.Format(time.RFC3339), tt.wantCron, tt.wantTime)
			}
			if api.taskURL != "https://releases.example.com/internal/release-followup/token42" {
				t.Fatalf("task URL = %q", api.taskURL)
			}
			if err := scheduler.Dispatch(context.Background(), "token42"); err != nil {
				t.Fatal(err)
			}

			payload, err := json.Marshal(api.payload)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(payload, &got); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"operation": "release_artifact", "repository": "ledger", "commit_sha": "abc123", "artifact": "registry.example.com/ledger:abc123", "scheduled_for": tt.wantTime}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("payload = %#v, want %#v", got, want)
			}
			if api.deletedJob != "job_42" {
				t.Fatalf("deleted job = %q", api.deletedJob)
			}
			if api.queue != releaseQueue {
				t.Fatalf("queue = %q, want %q", api.queue, releaseQueue)
			}
			wantKeys := []string{"schedule-ledger-abc123", "publish-ledger-abc123", "cancel-ledger-abc123"}
			if !reflect.DeepEqual(api.keys, wantKeys) {
				t.Fatalf("idempotency keys = %#v, want %#v", api.keys, wantKeys)
			}
			if _, err := store.Get("token42"); err == nil {
				t.Fatal("dispatched release remained in local state")
			}
		})
	}
}
