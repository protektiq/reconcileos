package services

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"reconcileos.dev/api/db"

	"github.com/google/uuid"
	postgrest "github.com/supabase-community/postgrest-go"
)

const (
	maxArtifactHashLength      = 64
	maxRekorResponseBytes      = 2 * 1024 * 1024
	maxAttestationPageSize     = 50
	maxAttestationPublicSearch = 100
)

var sha256HexPattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

type AttestationService struct {
	clients      *db.SupabaseClients
	rekorURL     string
	flyOIDCToken string
	httpClient   *http.Client
}

type AttestationRecord struct {
	ID                        string          `json:"id"`
	OrgID                     string          `json:"org_id"`
	ExecutionID               string          `json:"execution_id,omitempty"`
	ArtifactHash              string          `json:"artifact_hash"`
	RekorLogIndex             int64           `json:"rekor_log_index,omitempty"`
	RekorUUID                 string          `json:"rekor_uuid,omitempty"`
	RekorSignedEntryTimestamp string          `json:"rekor_signed_entry_timestamp,omitempty"`
	RekorInclusionProof       json.RawMessage `json:"rekor_inclusion_proof,omitempty"`
	SLSAPredicate             json.RawMessage `json:"slsa_predicate,omitempty"`
	SignedAt                  string          `json:"signed_at,omitempty"`
	Source                    string          `json:"source,omitempty"`
}

type AttestationFilters struct {
	RepoID      string
	StartDate   *time.Time
	EndDate     *time.Time
	ExecutionID string
	Page        int
	PageSize    int
}

type executionContextRecord struct {
	ID           string          `json:"id"`
	OrgID        string          `json:"org_id"`
	BotID        string          `json:"bot_id"`
	RepoID       string          `json:"repo_id"`
	TriggerEvent json.RawMessage `json:"trigger_event"`
	Result       json.RawMessage `json:"result"`
}

type botContextRecord struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type repoContextRecord struct {
	ID                 string `json:"id"`
	GitHubRepoFullName string `json:"github_repo_full_name"`
}

type rekorHashedRekordRequest struct {
	APIVersion string                 `json:"apiVersion"`
	Kind       string                 `json:"kind"`
	Spec       map[string]any         `json:"spec"`
	Extra      map[string]interface{} `json:"-"`
}

type rekorLogEntry struct {
	LogIndex             int64                  `json:"logIndex"`
	IntegratedTime       int64                  `json:"integratedTime"`
	Verification         map[string]any         `json:"verification"`
	SignedEntryTimestamp string                 `json:"signedEntryTimestamp"`
	Body                 string                 `json:"body"`
	Attestation          map[string]interface{} `json:"attestation"`
}

func NewAttestationService(clients *db.SupabaseClients, rekorURL, flyOIDCToken string) (*AttestationService, error) {
	if clients == nil {
		return nil, fmt.Errorf("supabase clients must not be nil")
	}
	cleanRekorURL := strings.TrimSpace(rekorURL)
	if cleanRekorURL == "" {
		return nil, fmt.Errorf("rekor URL must not be empty")
	}
	if _, err := url.ParseRequestURI(cleanRekorURL); err != nil {
		return nil, fmt.Errorf("rekor URL is invalid: %w", err)
	}

	return &AttestationService{
		clients:      clients,
		rekorURL:     strings.TrimRight(cleanRekorURL, "/"),
		flyOIDCToken: strings.TrimSpace(flyOIDCToken),
		httpClient:   &http.Client{Timeout: 20 * time.Second},
	}, nil
}

func (s *AttestationService) SignAndAttest(ctx context.Context, executionID uuid.UUID, artifactHash string) (AttestationRecord, error) {
	if executionID == uuid.Nil {
		return AttestationRecord{}, fmt.Errorf("execution ID must not be empty")
	}
	cleanHash, err := normalizeSHA256Hex(artifactHash)
	if err != nil {
		return AttestationRecord{}, err
	}

	executionRecord, botRecord, repoRecord, err := s.loadExecutionContext(ctx, executionID.String())
	if err != nil {
		return AttestationRecord{}, err
	}

	statement, err := buildSLSAStatement(executionRecord, botRecord, repoRecord, cleanHash, time.Now().UTC())
	if err != nil {
		return AttestationRecord{}, err
	}
	statementBytes, err := json.Marshal(statement)
	if err != nil {
		return AttestationRecord{}, fmt.Errorf("marshal slsa statement: %w", err)
	}

	signature, pubPEM, err := s.signStatement(statementBytes)
	if err != nil {
		return AttestationRecord{}, err
	}

	rekorResult, err := s.submitToRekor(ctx, cleanHash, signature, pubPEM)
	if err != nil {
		return AttestationRecord{}, err
	}

	insertRecord := map[string]any{
		"org_id":                       executionRecord.OrgID,
		"execution_id":                 executionRecord.ID,
		"artifact_hash":                cleanHash,
		"rekor_log_index":              rekorResult.RekorLogIndex,
		"rekor_uuid":                   rekorResult.RekorUUID,
		"rekor_signed_entry_timestamp": rekorResult.RekorSignedEntryTimestamp,
		"rekor_inclusion_proof":        rekorResult.RekorInclusionProof,
		"slsa_predicate":               json.RawMessage(statementBytes),
		"signed_at":                    time.Now().UTC().Format(time.RFC3339Nano),
	}

	_, _, err = s.clients.AdminPostgrest().
		From("attestations").
		Insert(insertRecord, false, "", "", "").
		Execute()
	if err != nil {
		return AttestationRecord{}, fmt.Errorf("insert attestation: %w", err)
	}

	var insertedRows []AttestationRecord
	_, err = s.clients.AdminPostgrest().
		From("attestations").
		Select("id,org_id,execution_id,artifact_hash,rekor_log_index,rekor_uuid,rekor_signed_entry_timestamp,rekor_inclusion_proof,slsa_predicate,signed_at", "", false).
		Eq("execution_id", executionRecord.ID).
		Eq("artifact_hash", cleanHash).
		Order("signed_at", &postgrest.OrderOpts{Ascending: false}).
		Limit(1, "").
		ExecuteTo(&insertedRows)
	if err != nil {
		return AttestationRecord{}, fmt.Errorf("query inserted attestation: %w", err)
	}
	if len(insertedRows) != 1 {
		return AttestationRecord{}, fmt.Errorf("inserted attestation row not found")
	}

	return insertedRows[0], nil
}

func (s *AttestationService) VerifyAttestation(ctx context.Context, artifactHash string) ([]AttestationRecord, error) {
	cleanHash, err := normalizeSHA256Hex(artifactHash)
	if err != nil {
		return nil, err
	}

	uuids, err := s.lookupRekorUUIDsByHash(ctx, cleanHash)
	if err != nil {
		return nil, err
	}
	if len(uuids) == 0 {
		return []AttestationRecord{}, nil
	}

	records := make([]AttestationRecord, 0, len(uuids))
	for _, currentUUID := range uuids {
		entry, err := s.fetchRekorEntry(ctx, currentUUID)
		if err != nil {
			return nil, err
		}
		inclusionProof, _ := json.Marshal(entry.Verification["inclusionProof"])
		signedAt := ""
		if entry.IntegratedTime > 0 {
			signedAt = time.Unix(entry.IntegratedTime, 0).UTC().Format(time.RFC3339Nano)
		}
		records = append(records, AttestationRecord{
			ArtifactHash:              cleanHash,
			RekorUUID:                 currentUUID,
			RekorLogIndex:             entry.LogIndex,
			RekorSignedEntryTimestamp: strings.TrimSpace(entry.SignedEntryTimestamp),
			RekorInclusionProof:       inclusionProof,
			SignedAt:                  signedAt,
			Source:                    "rekor",
		})
	}

	return records, nil
}

func (s *AttestationService) ListAttestationsForOrg(ctx context.Context, orgID string, filters AttestationFilters) ([]AttestationRecord, error) {
	cleanOrgID := strings.TrimSpace(orgID)
	if _, err := uuid.Parse(cleanOrgID); err != nil {
		return nil, fmt.Errorf("org id is invalid")
	}
	pageSize := filters.PageSize
	if pageSize == 0 {
		pageSize = maxAttestationPageSize
	}
	if pageSize != maxAttestationPageSize {
		return nil, fmt.Errorf("page size must be %d", maxAttestationPageSize)
	}
	page := filters.Page
	if page == 0 {
		page = 1
	}
	if page < 1 || page > 100000 {
		return nil, fmt.Errorf("page must be between 1 and 100000")
	}

	query := s.clients.AdminPostgrest().
		From("attestations").
		Select("id,org_id,execution_id,artifact_hash,rekor_log_index,rekor_uuid,rekor_signed_entry_timestamp,rekor_inclusion_proof,slsa_predicate,signed_at", "", false).
		Eq("org_id", cleanOrgID).
		Order("signed_at", &postgrest.OrderOpts{Ascending: false})

	if strings.TrimSpace(filters.ExecutionID) != "" {
		if _, err := uuid.Parse(strings.TrimSpace(filters.ExecutionID)); err != nil {
			return nil, fmt.Errorf("execution_id is invalid")
		}
		query = query.Eq("execution_id", strings.TrimSpace(filters.ExecutionID))
	}
	if filters.StartDate != nil {
		query = query.Gte("signed_at", filters.StartDate.UTC().Format(time.RFC3339Nano))
	}
	if filters.EndDate != nil {
		query = query.Lte("signed_at", filters.EndDate.UTC().Format(time.RFC3339Nano))
	}
	if strings.TrimSpace(filters.RepoID) != "" {
		cleanRepoID := strings.TrimSpace(filters.RepoID)
		if _, err := uuid.Parse(cleanRepoID); err != nil {
			return nil, fmt.Errorf("repo_id is invalid")
		}
		executionIDs, err := s.listExecutionIDsForRepo(cleanOrgID, cleanRepoID)
		if err != nil {
			return nil, err
		}
		if len(executionIDs) == 0 {
			return []AttestationRecord{}, nil
		}
		query = query.In("execution_id", executionIDs)
	}

	from := (page - 1) * pageSize
	to := from + pageSize - 1

	var rows []AttestationRecord
	_, err := query.Range(from, to, "").ExecuteTo(&rows)
	if err != nil {
		return nil, fmt.Errorf("query attestations: %w", err)
	}

	return rows, nil
}

func (s *AttestationService) GetAttestationForOrg(ctx context.Context, orgID, attestationID string) (AttestationRecord, error) {
	_ = ctx
	cleanOrgID := strings.TrimSpace(orgID)
	cleanAttestationID := strings.TrimSpace(attestationID)
	if _, err := uuid.Parse(cleanOrgID); err != nil {
		return AttestationRecord{}, fmt.Errorf("org id is invalid")
	}
	if _, err := uuid.Parse(cleanAttestationID); err != nil {
		return AttestationRecord{}, fmt.Errorf("attestation id is invalid")
	}

	var rows []AttestationRecord
	_, err := s.clients.AdminPostgrest().
		From("attestations").
		Select("id,org_id,execution_id,artifact_hash,rekor_log_index,rekor_uuid,rekor_signed_entry_timestamp,rekor_inclusion_proof,slsa_predicate,signed_at", "", false).
		Eq("id", cleanAttestationID).
		Eq("org_id", cleanOrgID).
		Limit(1, "").
		ExecuteTo(&rows)
	if err != nil {
		return AttestationRecord{}, fmt.Errorf("query attestation: %w", err)
	}
	if len(rows) == 0 {
		return AttestationRecord{}, fmt.Errorf("attestation not found")
	}
	return rows[0], nil
}

func (s *AttestationService) listExecutionIDsForRepo(orgID, repoID string) ([]string, error) {
	type executionIDRow struct {
		ID string `json:"id"`
	}
	var rows []executionIDRow
	_, err := s.clients.AdminPostgrest().
		From("executions").
		Select("id", "", false).
		Eq("org_id", orgID).
		Eq("repo_id", repoID).
		Limit(1000, "").
		ExecuteTo(&rows)
	if err != nil {
		return nil, fmt.Errorf("query executions for repo filter: %w", err)
	}
	result := make([]string, 0, len(rows))
	for _, row := range rows {
		cleanID := strings.TrimSpace(row.ID)
		if cleanID != "" {
			result = append(result, cleanID)
		}
	}
	return result, nil
}

func (s *AttestationService) loadExecutionContext(ctx context.Context, executionID string) (executionContextRecord, botContextRecord, repoContextRecord, error) {
	_ = ctx
	var executions []executionContextRecord
	_, err := s.clients.AdminPostgrest().
		From("executions").
		Select("id,org_id,bot_id,repo_id,trigger_event,result", "", false).
		Eq("id", executionID).
		Limit(1, "").
		ExecuteTo(&executions)
	if err != nil {
		return executionContextRecord{}, botContextRecord{}, repoContextRecord{}, fmt.Errorf("query execution context: %w", err)
	}
	if len(executions) != 1 {
		return executionContextRecord{}, botContextRecord{}, repoContextRecord{}, fmt.Errorf("execution not found")
	}
	executionRecord := executions[0]

	var bots []botContextRecord
	_, err = s.clients.AdminPostgrest().
		From("bots").
		Select("id,version", "", false).
		Eq("id", strings.TrimSpace(executionRecord.BotID)).
		Limit(1, "").
		ExecuteTo(&bots)
	if err != nil {
		return executionContextRecord{}, botContextRecord{}, repoContextRecord{}, fmt.Errorf("query bot context: %w", err)
	}
	if len(bots) != 1 {
		return executionContextRecord{}, botContextRecord{}, repoContextRecord{}, fmt.Errorf("bot not found")
	}

	var repos []repoContextRecord
	_, err = s.clients.AdminPostgrest().
		From("repos").
		Select("id,github_repo_full_name", "", false).
		Eq("id", strings.TrimSpace(executionRecord.RepoID)).
		Limit(1, "").
		ExecuteTo(&repos)
	if err != nil {
		return executionContextRecord{}, botContextRecord{}, repoContextRecord{}, fmt.Errorf("query repo context: %w", err)
	}
	if len(repos) != 1 {
		return executionContextRecord{}, botContextRecord{}, repoContextRecord{}, fmt.Errorf("repo not found")
	}

	return executionRecord, bots[0], repos[0], nil
}

func buildSLSAStatement(execution executionContextRecord, bot botContextRecord, repo repoContextRecord, artifactHash string, now time.Time) (map[string]any, error) {
	repoName := strings.TrimSpace(repo.GitHubRepoFullName)
	if repoName == "" || len(repoName) > 255 {
		return nil, fmt.Errorf("repo full name is invalid")
	}
	eventType := ""
	if len(execution.TriggerEvent) > 0 {
		var event map[string]any
		if json.Unmarshal(execution.TriggerEvent, &event) == nil {
			if typed, ok := event["event_type"].(string); ok {
				eventType = strings.TrimSpace(typed)
			}
		}
	}
	if eventType == "" {
		eventType = "unknown"
	}
	aiAssisted := false
	if len(execution.Result) > 0 {
		var result map[string]any
		if json.Unmarshal(execution.Result, &result) == nil {
			if typed, ok := result["ai_assisted"].(bool); ok {
				aiAssisted = typed
			}
		}
	}

	return map[string]any{
		"_type": "https://in-toto.io/Statement/v0.1",
		"subject": []map[string]any{
			{
				"name": repoName,
				"digest": map[string]string{
					"sha256": artifactHash,
				},
			},
		},
		"predicateType": "https://slsa.dev/provenance/v1",
		"predicate": map[string]any{
			"buildDefinition": map[string]any{
				"buildType": "https://reconcileos.dev/bot/v1",
				"externalParameters": map[string]any{
					"execution_id":       execution.ID,
					"bot_id":             execution.BotID,
					"bot_version":        strings.TrimSpace(bot.Version),
					"org_id":             execution.OrgID,
					"trigger_event_type": eventType,
					"ai_assisted":        aiAssisted,
				},
			},
			"runDetails": map[string]any{
				"builder": map[string]string{
					"id": "https://reconcileos.dev/runtime",
				},
				"metadata": map[string]string{
					"finishedOn": now.UTC().Format(time.RFC3339Nano),
				},
			},
		},
	}, nil
}

func (s *AttestationService) signStatement(statement []byte) ([]byte, []byte, error) {
	if len(statement) == 0 {
		return nil, nil, fmt.Errorf("statement is required")
	}

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate signing key: %w", err)
	}
	signature := ed25519.Sign(privateKey, statement)

	publicKeyDER, err := x509.MarshalPKIXPublicKey(privateKey.Public())
	if err != nil {
		return nil, nil, fmt.Errorf("marshal public key: %w", err)
	}
	publicKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyDER,
	})
	if len(publicKeyPEM) == 0 {
		return nil, nil, fmt.Errorf("encode public key PEM")
	}

	// If a Fly token exists, it is preserved in the service and can be used for
	// OIDC-attached signing flows as Sigstore setup is finalized.
	_ = s.flyOIDCToken

	return signature, publicKeyPEM, nil
}

func (s *AttestationService) submitToRekor(ctx context.Context, artifactHash string, signature, publicKeyPEM []byte) (AttestationRecord, error) {
	requestPayload := rekorHashedRekordRequest{
		APIVersion: "0.0.1",
		Kind:       "hashedrekord",
		Spec: map[string]any{
			"data": map[string]any{
				"hash": map[string]any{
					"algorithm": "sha256",
					"value":     artifactHash,
				},
			},
			"signature": map[string]any{
				"content": base64.StdEncoding.EncodeToString(signature),
				"publicKey": map[string]any{
					"content": base64.StdEncoding.EncodeToString(publicKeyPEM),
				},
			},
		},
	}
	encodedRequest, err := json.Marshal(requestPayload)
	if err != nil {
		return AttestationRecord{}, fmt.Errorf("marshal rekor request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.rekorURL+"/api/v1/log/entries", strings.NewReader(string(encodedRequest)))
	if err != nil {
		return AttestationRecord{}, fmt.Errorf("create rekor request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := s.httpClient.Do(request)
	if err != nil {
		return AttestationRecord{}, fmt.Errorf("submit to rekor: %w", err)
	}
	defer response.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxRekorResponseBytes))
	if readErr != nil {
		return AttestationRecord{}, fmt.Errorf("read rekor response: %w", readErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return AttestationRecord{}, fmt.Errorf("rekor submission failed: status=%d body=%s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var entryMap map[string]rekorLogEntry
	if err := json.Unmarshal(body, &entryMap); err != nil {
		return AttestationRecord{}, fmt.Errorf("decode rekor response: %w", err)
	}
	if len(entryMap) != 1 {
		return AttestationRecord{}, fmt.Errorf("rekor response contained unexpected entries")
	}
	for currentUUID, entry := range entryMap {
		inclusionProof, _ := json.Marshal(entry.Verification["inclusionProof"])
		return AttestationRecord{
			RekorUUID:                 strings.TrimSpace(currentUUID),
			RekorLogIndex:             entry.LogIndex,
			RekorSignedEntryTimestamp: strings.TrimSpace(entry.SignedEntryTimestamp),
			RekorInclusionProof:       inclusionProof,
		}, nil
	}

	return AttestationRecord{}, fmt.Errorf("rekor response missing entry")
}

func (s *AttestationService) lookupRekorUUIDsByHash(ctx context.Context, artifactHash string) ([]string, error) {
	requestURL := fmt.Sprintf("%s/api/v1/index/retrieve?hash=sha256:%s", s.rekorURL, artifactHash)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create rekor index request: %w", err)
	}

	response, err := s.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("query rekor index: %w", err)
	}
	defer response.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxRekorResponseBytes))
	if readErr != nil {
		return nil, fmt.Errorf("read rekor index response: %w", readErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("rekor index query failed: status=%d body=%s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var uuids []string
	if err := json.Unmarshal(body, &uuids); err != nil {
		return nil, fmt.Errorf("decode rekor index response: %w", err)
	}
	if len(uuids) > maxAttestationPublicSearch {
		uuids = uuids[:maxAttestationPublicSearch]
	}
	return uuids, nil
}

func (s *AttestationService) fetchRekorEntry(ctx context.Context, rekorUUID string) (rekorLogEntry, error) {
	cleanUUID := strings.TrimSpace(rekorUUID)
	if cleanUUID == "" || len(cleanUUID) > 255 {
		return rekorLogEntry{}, fmt.Errorf("rekor uuid is invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.rekorURL+"/api/v1/log/entries/"+cleanUUID, nil)
	if err != nil {
		return rekorLogEntry{}, fmt.Errorf("create rekor entry request: %w", err)
	}

	response, err := s.httpClient.Do(request)
	if err != nil {
		return rekorLogEntry{}, fmt.Errorf("fetch rekor entry: %w", err)
	}
	defer response.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxRekorResponseBytes))
	if readErr != nil {
		return rekorLogEntry{}, fmt.Errorf("read rekor entry response: %w", readErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return rekorLogEntry{}, fmt.Errorf("rekor entry fetch failed: status=%d body=%s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var entryMap map[string]rekorLogEntry
	if err := json.Unmarshal(body, &entryMap); err != nil {
		return rekorLogEntry{}, fmt.Errorf("decode rekor entry response: %w", err)
	}
	entry, ok := entryMap[cleanUUID]
	if !ok {
		return rekorLogEntry{}, fmt.Errorf("rekor entry response missing %s", cleanUUID)
	}
	return entry, nil
}

func normalizeSHA256Hex(raw string) (string, error) {
	clean := strings.TrimSpace(raw)
	if clean == "" || len(clean) > maxArtifactHashLength {
		return "", fmt.Errorf("artifact hash must be %d hex characters", maxArtifactHashLength)
	}
	if !sha256HexPattern.MatchString(clean) {
		return "", fmt.Errorf("artifact hash must be a valid sha256 hex digest")
	}
	decoded, err := hex.DecodeString(clean)
	if err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("artifact hash must be a valid sha256 digest")
	}
	return strings.ToLower(clean), nil
}

func ParseAttestationPage(raw string) (int, error) {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return 1, nil
	}
	parsed, err := strconv.Atoi(clean)
	if err != nil || parsed < 1 || parsed > 100000 {
		return 0, fmt.Errorf("page must be between 1 and 100000")
	}
	return parsed, nil
}
