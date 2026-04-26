package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	postgrest "github.com/supabase-community/postgrest-go"
)

const (
	maxRPCBodyBytes     = 32 * 1024
	maxResultPayloadLen = 256 * 1024
)

type EventRecord struct {
	ID        string          `json:"id"`
	OrgID     string          `json:"org_id"`
	RepoID    string          `json:"repo_id"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
}

type ExecutionRecord struct {
	ID           string          `json:"id"`
	OrgID        string          `json:"org_id"`
	BotID        string          `json:"bot_id"`
	RepoID       string          `json:"repo_id"`
	Status       string          `json:"status"`
	TriggerEvent json.RawMessage `json:"trigger_event"`
}

type BotRecord struct {
	ID       string          `json:"id"`
	Manifest json.RawMessage `json:"manifest"`
}

type RepoRecord struct {
	ID                 string `json:"id"`
	GitHubRepoFullName string `json:"github_repo_full_name"`
}

type InstallationRecord struct {
	BotID string `json:"bot_id"`
}

type Store struct {
	baseURL    string
	serviceKey string
	pg         *postgrest.Client
	httpClient *http.Client
}

func New(baseURL, serviceKey string) *Store {
	headers := map[string]string{
		"apikey":        serviceKey,
		"Authorization": "Bearer " + serviceKey,
	}

	return &Store{
		baseURL:    strings.TrimRight(baseURL, "/"),
		serviceKey: serviceKey,
		pg:         postgrest.NewClient(strings.TrimRight(baseURL, "/")+"/rest/v1", "", headers),
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (s *Store) TryLock(ctx context.Context, lockKey int64) (bool, error) {
	return s.callRPCBool(ctx, "runtime_try_lock", map[string]any{"lock_key": lockKey})
}

func (s *Store) Unlock(ctx context.Context, lockKey int64) (bool, error) {
	return s.callRPCBool(ctx, "runtime_unlock", map[string]any{"lock_key": lockKey})
}

func (s *Store) callRPCBool(ctx context.Context, rpcName string, payload map[string]any) (bool, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("marshal rpc payload: %w", err)
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		s.baseURL+"/rest/v1/rpc/"+rpcName,
		bytes.NewReader(encoded),
	)
	if err != nil {
		return false, fmt.Errorf("create rpc request: %w", err)
	}
	request.Header.Set("apikey", s.serviceKey)
	request.Header.Set("Authorization", "Bearer "+s.serviceKey)
	request.Header.Set("Content-Type", "application/json")

	response, err := s.httpClient.Do(request)
	if err != nil {
		return false, fmt.Errorf("execute rpc %s: %w", rpcName, err)
	}
	defer response.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxRPCBodyBytes))
	if readErr != nil {
		return false, fmt.Errorf("read rpc %s response: %w", rpcName, readErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, fmt.Errorf("rpc %s status %d body %s", rpcName, response.StatusCode, string(body))
	}

	var didLock bool
	if err := json.Unmarshal(body, &didLock); err != nil {
		return false, fmt.Errorf("decode rpc %s result: %w", rpcName, err)
	}

	return didLock, nil
}

func (s *Store) ListUnprocessedEvents(limit int) ([]EventRecord, error) {
	if limit <= 0 || limit > 200 {
		return nil, errorsf("event limit must be between 1 and 200")
	}

	var records []EventRecord
	_, err := s.pg.From("events").
		Select("id,org_id,repo_id,event_type,payload", "", false).
		Eq("processed", "false").
		Order("received_at", &postgrest.OrderOpts{Ascending: true}).
		Limit(limit, "").
		ExecuteTo(&records)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	return records, nil
}

func (s *Store) ListActiveInstallations(orgID string) ([]InstallationRecord, error) {
	cleanOrgID := strings.TrimSpace(orgID)
	if cleanOrgID == "" || len(cleanOrgID) > 64 {
		return nil, errorsf("org id is required")
	}

	var rows []InstallationRecord
	_, err := s.pg.From("bot_installations").
		Select("bot_id", "", false).
		Eq("org_id", cleanOrgID).
		Eq("active", "true").
		ExecuteTo(&rows)
	if err != nil {
		return nil, fmt.Errorf("query active installations: %w", err)
	}
	return rows, nil
}

func (s *Store) GetBot(botID string) (BotRecord, error) {
	cleanBotID := strings.TrimSpace(botID)
	if cleanBotID == "" || len(cleanBotID) > 64 {
		return BotRecord{}, errorsf("bot id is required")
	}

	var record BotRecord
	_, err := s.pg.From("bots").
		Select("id,manifest", "", false).
		Eq("id", cleanBotID).
		Limit(1, "").
		Single().
		ExecuteTo(&record)
	if err != nil {
		return BotRecord{}, fmt.Errorf("query bot %s: %w", cleanBotID, err)
	}
	return record, nil
}

func (s *Store) InsertExecutionQueued(orgID, botID, repoID string, triggerEvent json.RawMessage) error {
	if !json.Valid(triggerEvent) {
		return errorsf("trigger event must be valid json")
	}
	record := map[string]any{
		"org_id":        strings.TrimSpace(orgID),
		"bot_id":        strings.TrimSpace(botID),
		"repo_id":       strings.TrimSpace(repoID),
		"status":        "queued",
		"trigger_event": json.RawMessage(triggerEvent),
	}

	_, _, err := s.pg.From("executions").
		Insert(record, false, "", "", "").
		Execute()
	if err != nil {
		return fmt.Errorf("insert queued execution: %w", err)
	}
	return nil
}

func (s *Store) MarkEventProcessed(eventID string) error {
	cleanEventID := strings.TrimSpace(eventID)
	if cleanEventID == "" {
		return errorsf("event id is required")
	}
	update := map[string]any{"processed": true}
	_, _, err := s.pg.From("events").Update(update, "", "").Eq("id", cleanEventID).Execute()
	if err != nil {
		return fmt.Errorf("mark event processed: %w", err)
	}
	return nil
}

func (s *Store) ListQueuedExecutions(limit int) ([]ExecutionRecord, error) {
	if limit <= 0 || limit > 100 {
		return nil, errorsf("execution limit must be between 1 and 100")
	}
	var rows []ExecutionRecord
	_, err := s.pg.From("executions").
		Select("id,org_id,bot_id,repo_id,status,trigger_event", "", false).
		Eq("status", "queued").
		Limit(limit, "").
		ExecuteTo(&rows)
	if err != nil {
		return nil, fmt.Errorf("query queued executions: %w", err)
	}
	return rows, nil
}

func (s *Store) ClaimExecution(executionID string) (bool, error) {
	cleanID := strings.TrimSpace(executionID)
	if cleanID == "" {
		return false, errorsf("execution id is required")
	}

	update := map[string]any{
		"status":     "running",
		"started_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	_, _, err := s.pg.From("executions").
		Update(update, "", "").
		Eq("id", cleanID).
		Eq("status", "queued").
		Execute()
	if err != nil {
		return false, fmt.Errorf("claim execution: %w", err)
	}

	var rows []ExecutionRecord
	_, lookupErr := s.pg.From("executions").
		Select("id,status", "", false).
		Eq("id", cleanID).
		Eq("status", "running").
		Limit(1, "").
		ExecuteTo(&rows)
	if lookupErr != nil {
		return false, fmt.Errorf("verify claimed execution: %w", lookupErr)
	}
	return len(rows) == 1, nil
}

func (s *Store) GetRepo(repoID string) (RepoRecord, error) {
	cleanRepoID := strings.TrimSpace(repoID)
	if cleanRepoID == "" {
		return RepoRecord{}, errorsf("repo id is required")
	}
	var repo RepoRecord
	_, err := s.pg.From("repos").
		Select("id,github_repo_full_name", "", false).
		Eq("id", cleanRepoID).
		Limit(1, "").
		Single().
		ExecuteTo(&repo)
	if err != nil {
		return RepoRecord{}, fmt.Errorf("query repo %s: %w", cleanRepoID, err)
	}
	return repo, nil
}

func (s *Store) FinalizeExecution(executionID, status string, result map[string]any, requiresReview bool) error {
	cleanID := strings.TrimSpace(executionID)
	if cleanID == "" {
		return errorsf("execution id is required")
	}
	if status != "completed" && status != "failed" {
		return errorsf("invalid execution status")
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal execution result: %w", err)
	}
	if len(encoded) > maxResultPayloadLen {
		return errorsf("execution result exceeds max size")
	}

	update := map[string]any{
		"status":          status,
		"result":          json.RawMessage(encoded),
		"requires_review": requiresReview,
		"completed_at":    time.Now().UTC().Format(time.RFC3339Nano),
	}
	_, _, updateErr := s.pg.From("executions").Update(update, "", "").Eq("id", cleanID).Execute()
	if updateErr != nil {
		return fmt.Errorf("finalize execution: %w", updateErr)
	}
	return nil
}

func errorsf(message string) error {
	return fmt.Errorf(message)
}
