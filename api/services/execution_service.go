package services

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"reconcileos.dev/api/db"

	"github.com/google/uuid"
	postgrest "github.com/supabase-community/postgrest-go"
)

const (
	maxRepoFullNameLength = 255
	maxBotIDLength        = 64
	maxExecutionIDLength  = 64
)

var repoFullNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

type ExecutionService struct {
	clients *db.SupabaseClients
}

type RepoStatus struct {
	OpenCVEs            int        `json:"open_cves"`
	ActiveBots          int        `json:"active_bots"`
	LastExecutionAt     *time.Time `json:"last_execution_at,omitempty"`
	PendingReviews      int        `json:"pending_reviews"`
	LastAttestationHash string     `json:"last_attestation_hash,omitempty"`
	LastAttestationAt   *time.Time `json:"last_attestation_at,omitempty"`
}

type TriggerExecutionInput struct {
	OrgID        string
	BotID        string
	RepoFullName string
	DryRun       bool
}

type ExecutionStatusRecord struct {
	ID             string          `json:"id"`
	Status         string          `json:"status"`
	Result         json.RawMessage `json:"result"`
	RequiresReview bool            `json:"requires_review"`
	StartedAt      *time.Time      `json:"started_at"`
	CompletedAt    *time.Time      `json:"completed_at"`
}

type repoLookupRow struct {
	ID string `json:"id"`
}

type executionLookupRow struct {
	ID string `json:"id"`
}

type attestationSummaryRow struct {
	ArtifactHash string     `json:"artifact_hash"`
	SignedAt     *time.Time `json:"signed_at"`
}

func NewExecutionService(clients *db.SupabaseClients) (*ExecutionService, error) {
	if clients == nil {
		return nil, fmt.Errorf("supabase clients must not be nil")
	}
	return &ExecutionService{clients: clients}, nil
}

func (s *ExecutionService) GetRepoStatus(ctx context.Context, orgID, repoFullName string) (RepoStatus, error) {
	cleanOrgID, cleanRepoFullName, err := validateOrgAndRepo(orgID, repoFullName)
	if err != nil {
		return RepoStatus{}, err
	}

	repoID, err := s.lookupRepoID(ctx, cleanOrgID, cleanRepoFullName)
	if err != nil {
		return RepoStatus{}, err
	}

	openCVEs, err := s.countOpenCVEs(ctx, cleanOrgID, repoID)
	if err != nil {
		return RepoStatus{}, err
	}
	activeBots, err := s.countActiveBots(ctx, cleanOrgID)
	if err != nil {
		return RepoStatus{}, err
	}
	pendingReviews, err := s.countPendingReviews(ctx, cleanOrgID, repoID)
	if err != nil {
		return RepoStatus{}, err
	}
	lastExecutionAt, err := s.lastExecutionAt(ctx, cleanOrgID, repoID)
	if err != nil {
		return RepoStatus{}, err
	}
	lastAttestation, err := s.lastAttestation(ctx, cleanOrgID, repoID)
	if err != nil {
		return RepoStatus{}, err
	}

	return RepoStatus{
		OpenCVEs:            openCVEs,
		ActiveBots:          activeBots,
		LastExecutionAt:     lastExecutionAt,
		PendingReviews:      pendingReviews,
		LastAttestationHash: strings.TrimSpace(lastAttestation.ArtifactHash),
		LastAttestationAt:   lastAttestation.SignedAt,
	}, nil
}

func (s *ExecutionService) TriggerExecution(ctx context.Context, input TriggerExecutionInput) (string, error) {
	cleanOrgID, cleanRepoFullName, err := validateOrgAndRepo(input.OrgID, input.RepoFullName)
	if err != nil {
		return "", err
	}
	cleanBotID := strings.TrimSpace(input.BotID)
	if cleanBotID == "" || len(cleanBotID) > maxBotIDLength {
		return "", fmt.Errorf("bot id is invalid")
	}
	if _, err := uuid.Parse(cleanBotID); err != nil {
		return "", fmt.Errorf("bot id is invalid")
	}

	repoID, err := s.lookupRepoID(ctx, cleanOrgID, cleanRepoFullName)
	if err != nil {
		return "", err
	}

	triggerEvent := map[string]any{
		"event_type": "cli_trigger",
		"dry_run":    input.DryRun,
		"source":     "recos-cli",
	}
	encodedTriggerEvent, err := json.Marshal(triggerEvent)
	if err != nil {
		return "", fmt.Errorf("marshal trigger event: %w", err)
	}

	record := map[string]any{
		"org_id":        cleanOrgID,
		"bot_id":        cleanBotID,
		"repo_id":       repoID,
		"status":        "queued",
		"trigger_event": json.RawMessage(encodedTriggerEvent),
	}
	_, _, err = s.clients.AdminPostgrest().
		From("executions").
		Insert(record, false, "", "", "").
		Execute()
	if err != nil {
		return "", fmt.Errorf("insert execution: %w", err)
	}

	var rows []executionLookupRow
	_, err = s.clients.AdminPostgrest().
		From("executions").
		Select("id", "", false).
		Eq("org_id", cleanOrgID).
		Eq("bot_id", cleanBotID).
		Eq("repo_id", repoID).
		Order("started_at", &postgrest.OrderOpts{Ascending: false}).
		Order("id", &postgrest.OrderOpts{Ascending: false}).
		Limit(1, "").
		ExecuteTo(&rows)
	if err != nil {
		return "", fmt.Errorf("query inserted execution: %w", err)
	}
	if len(rows) != 1 {
		return "", fmt.Errorf("inserted execution not found")
	}
	executionID := strings.TrimSpace(rows[0].ID)
	if executionID == "" {
		return "", fmt.Errorf("inserted execution id is missing")
	}
	return executionID, nil
}

func (s *ExecutionService) GetExecutionStatus(ctx context.Context, orgID, executionID string) (ExecutionStatusRecord, error) {
	cleanOrgID := strings.TrimSpace(orgID)
	cleanExecutionID := strings.TrimSpace(executionID)
	if _, err := uuid.Parse(cleanOrgID); err != nil {
		return ExecutionStatusRecord{}, fmt.Errorf("organization id is invalid")
	}
	if cleanExecutionID == "" || len(cleanExecutionID) > maxExecutionIDLength {
		return ExecutionStatusRecord{}, fmt.Errorf("execution id is invalid")
	}
	if _, err := uuid.Parse(cleanExecutionID); err != nil {
		return ExecutionStatusRecord{}, fmt.Errorf("execution id is invalid")
	}

	var rows []ExecutionStatusRecord
	_, err := s.clients.AdminPostgrest().
		From("executions").
		Select("id,status,result,requires_review,started_at,completed_at", "", false).
		Eq("id", cleanExecutionID).
		Eq("org_id", cleanOrgID).
		Limit(1, "").
		ExecuteTo(&rows)
	if err != nil {
		return ExecutionStatusRecord{}, fmt.Errorf("query execution status: %w", err)
	}
	if len(rows) != 1 {
		return ExecutionStatusRecord{}, fmt.Errorf("execution not found")
	}
	return rows[0], nil
}

func (s *ExecutionService) lookupRepoID(_ context.Context, orgID, repoFullName string) (string, error) {
	var rows []repoLookupRow
	_, err := s.clients.AdminPostgrest().
		From("repos").
		Select("id", "", false).
		Eq("org_id", orgID).
		Eq("github_repo_full_name", repoFullName).
		Eq("active", "true").
		Limit(1, "").
		ExecuteTo(&rows)
	if err != nil {
		return "", fmt.Errorf("query repository: %w", err)
	}
	if len(rows) != 1 {
		return "", fmt.Errorf("repository not found")
	}
	repoID := strings.TrimSpace(rows[0].ID)
	if repoID == "" {
		return "", fmt.Errorf("repository id is missing")
	}
	return repoID, nil
}

func (s *ExecutionService) countOpenCVEs(_ context.Context, orgID, repoID string) (int, error) {
	type eventRow struct {
		ID string `json:"id"`
	}
	var rows []eventRow
	_, err := s.clients.AdminPostgrest().
		From("events").
		Select("id", "", false).
		Eq("org_id", orgID).
		Eq("repo_id", repoID).
		Eq("processed", "false").
		Eq("event_type", "cve_detected").
		Limit(1000, "").
		ExecuteTo(&rows)
	if err != nil {
		return 0, fmt.Errorf("query open cves: %w", err)
	}
	return len(rows), nil
}

func (s *ExecutionService) countActiveBots(_ context.Context, orgID string) (int, error) {
	type installationRow struct {
		ID string `json:"id"`
	}
	var rows []installationRow
	_, err := s.clients.AdminPostgrest().
		From("bot_installations").
		Select("id", "", false).
		Eq("org_id", orgID).
		Eq("active", "true").
		Limit(1000, "").
		ExecuteTo(&rows)
	if err != nil {
		return 0, fmt.Errorf("query active bots: %w", err)
	}
	return len(rows), nil
}

func (s *ExecutionService) countPendingReviews(_ context.Context, orgID, repoID string) (int, error) {
	type pendingRow struct {
		ID string `json:"id"`
	}
	var executions []executionLookupRow
	_, err := s.clients.AdminPostgrest().
		From("executions").
		Select("id", "", false).
		Eq("org_id", orgID).
		Eq("repo_id", repoID).
		Limit(1000, "").
		ExecuteTo(&executions)
	if err != nil {
		return 0, fmt.Errorf("query executions for pending review count: %w", err)
	}
	if len(executions) == 0 {
		return 0, nil
	}
	executionIDs := make([]string, 0, len(executions))
	for _, execution := range executions {
		cleanID := strings.TrimSpace(execution.ID)
		if cleanID != "" {
			executionIDs = append(executionIDs, cleanID)
		}
	}
	if len(executionIDs) == 0 {
		return 0, nil
	}

	var rows []pendingRow
	_, err = s.clients.AdminPostgrest().
		From("review_queue").
		Select("id", "", false).
		Eq("org_id", orgID).
		Eq("status", "pending").
		In("execution_id", executionIDs).
		Limit(1000, "").
		ExecuteTo(&rows)
	if err != nil {
		return 0, fmt.Errorf("query pending reviews: %w", err)
	}
	return len(rows), nil
}

func (s *ExecutionService) lastExecutionAt(_ context.Context, orgID, repoID string) (*time.Time, error) {
	type executionSummary struct {
		CompletedAt *time.Time `json:"completed_at"`
		StartedAt   *time.Time `json:"started_at"`
	}
	var rows []executionSummary
	_, err := s.clients.AdminPostgrest().
		From("executions").
		Select("completed_at,started_at", "", false).
		Eq("org_id", orgID).
		Eq("repo_id", repoID).
		Order("completed_at", &postgrest.OrderOpts{Ascending: false}).
		Order("started_at", &postgrest.OrderOpts{Ascending: false}).
		Limit(1, "").
		ExecuteTo(&rows)
	if err != nil {
		return nil, fmt.Errorf("query last execution timestamp: %w", err)
	}
	if len(rows) != 1 {
		return nil, nil
	}
	if rows[0].CompletedAt != nil {
		return rows[0].CompletedAt, nil
	}
	return rows[0].StartedAt, nil
}

func (s *ExecutionService) lastAttestation(_ context.Context, orgID, repoID string) (attestationSummaryRow, error) {
	var executions []executionLookupRow
	_, err := s.clients.AdminPostgrest().
		From("executions").
		Select("id", "", false).
		Eq("org_id", orgID).
		Eq("repo_id", repoID).
		Limit(1000, "").
		ExecuteTo(&executions)
	if err != nil {
		return attestationSummaryRow{}, fmt.Errorf("query executions for attestation lookup: %w", err)
	}
	if len(executions) == 0 {
		return attestationSummaryRow{}, nil
	}
	executionIDs := make([]string, 0, len(executions))
	for _, execution := range executions {
		cleanID := strings.TrimSpace(execution.ID)
		if cleanID != "" {
			executionIDs = append(executionIDs, cleanID)
		}
	}
	if len(executionIDs) == 0 {
		return attestationSummaryRow{}, nil
	}

	var rows []attestationSummaryRow
	_, err = s.clients.AdminPostgrest().
		From("attestations").
		Select("artifact_hash,signed_at", "", false).
		Eq("org_id", orgID).
		In("execution_id", executionIDs).
		Order("signed_at", &postgrest.OrderOpts{Ascending: false}).
		Limit(1, "").
		ExecuteTo(&rows)
	if err != nil {
		return attestationSummaryRow{}, fmt.Errorf("query latest attestation: %w", err)
	}
	if len(rows) != 1 {
		return attestationSummaryRow{}, nil
	}
	return rows[0], nil
}

func validateOrgAndRepo(orgID, repoFullName string) (string, string, error) {
	cleanOrgID := strings.TrimSpace(orgID)
	cleanRepoFullName := strings.TrimSpace(repoFullName)
	if _, err := uuid.Parse(cleanOrgID); err != nil {
		return "", "", fmt.Errorf("organization id is invalid")
	}
	if cleanRepoFullName == "" || len(cleanRepoFullName) > maxRepoFullNameLength {
		return "", "", fmt.Errorf("repository full name is invalid")
	}
	if !repoFullNamePattern.MatchString(cleanRepoFullName) {
		return "", "", fmt.Errorf("repository full name is invalid")
	}
	return cleanOrgID, cleanRepoFullName, nil
}
