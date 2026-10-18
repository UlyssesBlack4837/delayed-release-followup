package releaseflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Infrai interface {
	CreateCron(context.Context, string, string, string) (string, error)
	DeleteCron(context.Context, string, string) error
	Publish(context.Context, string, any, string) error
}

const releaseQueue = "release-operations"

type BuildEvent struct {
	Repository string `json:"repository"`
	CommitSHA  string `json:"commit_sha"`
	Artifact   string `json:"artifact"`
	DelayHours int    `json:"delay_hours"`
}

type ScheduledRelease struct {
	Token     string     `json:"token"`
	JobID     string     `json:"job_id"`
	ExecuteAt time.Time  `json:"execute_at"`
	Build     BuildEvent `json:"build"`
}

type Store struct {
	path string
	mu   sync.Mutex
}

func NewStore(path string) *Store { return &Store{path: path} }

func (s *Store) Put(release ScheduledRelease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.read()
	if err != nil {
		return err
	}
	items[release.Token] = release
	return s.write(items)
}

func (s *Store) Get(token string) (ScheduledRelease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.read()
	if err != nil {
		return ScheduledRelease{}, err
	}
	release, ok := items[token]
	if !ok {
		return ScheduledRelease{}, os.ErrNotExist
	}
	return release, nil
}

func (s *Store) Delete(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.read()
	if err != nil {
		return err
	}
	delete(items, token)
	return s.write(items)
}

func (s *Store) read() (map[string]ScheduledRelease, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]ScheduledRelease), nil
	}
	if err != nil {
		return nil, err
	}
	var items map[string]ScheduledRelease
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("decode release state: %w", err)
	}
	return items, nil
}

func (s *Store) write(items map[string]ScheduledRelease) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	temporary := s.path + ".new"
	if err := os.WriteFile(temporary, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, s.path)
}

type Scheduler struct {
	infrai      Infrai
	store       *Store
	callbackURL string
	now         func() time.Time
	newToken    func() (string, error)
}

func NewScheduler(infrai Infrai, store *Store, callbackURL string) *Scheduler {
	return &Scheduler{infrai: infrai, store: store, callbackURL: callbackURL, now: time.Now, newToken: randomToken}
}

func (s *Scheduler) Schedule(ctx context.Context, build BuildEvent) (ScheduledRelease, error) {
	if build.Repository == "" || build.CommitSHA == "" || build.Artifact == "" {
		return ScheduledRelease{}, errors.New("repository, commit_sha, and artifact are required")
	}
	if build.DelayHours < 1 || build.DelayHours > 168 {
		return ScheduledRelease{}, errors.New("delay_hours must be between 1 and 168")
	}
	token, err := s.newToken()
	if err != nil {
		return ScheduledRelease{}, err
	}
	target := s.now().UTC().Add(time.Duration(build.DelayHours) * time.Hour)
	executeAt := target.Truncate(time.Minute)
	if executeAt.Before(target) {
		executeAt = executeAt.Add(time.Minute)
	}
	callback, err := url.Parse(s.callbackURL)
	if err != nil {
		return ScheduledRelease{}, fmt.Errorf("parse callback URL: %w", err)
	}
	callback.Path = "/internal/release-followup/" + token
	cronExpr := fmt.Sprintf("%d %d %d %d *", executeAt.Minute(), executeAt.Hour(), executeAt.Day(), int(executeAt.Month()))
	jobID, err := s.infrai.CreateCron(ctx, cronExpr, callback.String(), "schedule-"+build.Repository+"-"+build.CommitSHA)
	if err != nil {
		return ScheduledRelease{}, err
	}
	release := ScheduledRelease{Token: token, JobID: jobID, ExecuteAt: executeAt, Build: build}
	if err := s.store.Put(release); err != nil {
		return ScheduledRelease{}, err
	}
	return release, nil
}

func (s *Scheduler) Dispatch(ctx context.Context, token string) error {
	release, err := s.store.Get(token)
	if err != nil {
		return err
	}
	key := release.Build.Repository + "-" + release.Build.CommitSHA
	payload := map[string]any{
		"operation":     "release_artifact",
		"repository":    release.Build.Repository,
		"commit_sha":    release.Build.CommitSHA,
		"artifact":      release.Build.Artifact,
		"scheduled_for": release.ExecuteAt.Format(time.RFC3339),
	}
	if err := s.infrai.Publish(ctx, releaseQueue, payload, "publish-"+key); err != nil {
		return err
	}
	if err := s.infrai.DeleteCron(ctx, release.JobID, "cancel-"+key); err != nil {
		return err
	}
	return s.store.Delete(token)
}

func randomToken() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
