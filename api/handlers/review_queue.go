package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"reconcileos.dev/api/db"
	"reconcileos.dev/api/middleware"
	"reconcileos.dev/api/services"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	postgrest "github.com/supabase-community/postgrest-go"
)

const (
	maxQueueIDLength         = 64
	maxReviewQueueStatusLen  = 32
	maxReviewQueueDiffLength = 2_000_000
	maxReviewSummaryLength   = 20_000
	maxReviewBotNameLength   = 255
	maxReviewRepoNameLength  = 255
	maxReviewPRTitleLength   = 255
	maxReviewBranchLength    = 255
)

type reviewQueueErrorResponse struct {
	Error string `json:"error"`
}

type reviewQueueHandler struct {
	clients            *db.SupabaseClients
	prService          *services.PRService
	attestationService *services.AttestationService
	rekorBaseURL       string
}

type reviewQueueRow struct {
	ID            string     `json:"id"`
	OrgID         string     `json:"org_id"`
	ExecutionID   string     `json:"execution_id"`
	DiffContent   string     `json:"diff_content"`
	ClaudeSummary string     `json:"claude_summary"`
	Status        string     `json:"status"`
	ReviewedBy    string     `json:"reviewed_by"`
	ReviewedAt    *time.Time `json:"reviewed_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

type executionQueueDetails struct {
	ID             string     `json:"id"`
	OrgID          string     `json:"org_id"`
	BotID          string     `json:"bot_id"`
	RepoID         string     `json:"repo_id"`
	Status         string     `json:"status"`
	RequiresReview bool       `json:"requires_review"`
	StartedAt      *time.Time `json:"started_at"`
	CompletedAt    *time.Time `json:"completed_at"`
	Result         any        `json:"result"`
	TriggerEvent   any        `json:"trigger_event"`
}

type botQueueDetails struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type repoQueueDetails struct {
	ID                 string `json:"id"`
	GitHubRepoFullName string `json:"github_repo_full_name"`
}

type reviewQueueItemResponse struct {
	ID            string                `json:"id"`
	Status        string                `json:"status"`
	DiffContent   string                `json:"diff_content"`
	ClaudeSummary string                `json:"claude_summary"`
	CreatedAt     time.Time             `json:"created_at"`
	Execution     executionQueueDetails `json:"execution"`
	Bot           botQueueDetails       `json:"bot"`
	Repo          repoQueueDetails      `json:"repo"`
}

type reviewQueueApproveResponse struct {
	PRURL         string `json:"pr_url"`
	AttestationID string `json:"attestation_id"`
}

func ReviewQueueList(clients *db.SupabaseClients) gin.HandlerFunc {
	handler := &reviewQueueHandler{clients: clients}
	return handler.listPendingQueue()
}

func ReviewQueueGet(clients *db.SupabaseClients) gin.HandlerFunc {
	handler := &reviewQueueHandler{clients: clients}
	return handler.getQueueItem()
}

func ReviewQueueApprove(clients *db.SupabaseClients, prService *services.PRService, attestationService *services.AttestationService, rekorBaseURL string) gin.HandlerFunc {
	handler := &reviewQueueHandler{
		clients:            clients,
		prService:          prService,
		attestationService: attestationService,
		rekorBaseURL:       strings.TrimRight(strings.TrimSpace(rekorBaseURL), "/"),
	}
	return handler.approveQueueItem()
}

func ReviewQueueReject(clients *db.SupabaseClients) gin.HandlerFunc {
	handler := &reviewQueueHandler{clients: clients}
	return handler.rejectQueueItem()
}

func (h *reviewQueueHandler) listPendingQueue() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.clients == nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, reviewQueueErrorResponse{Error: "database client not configured"})
			return
		}
		orgID := strings.TrimSpace(middleware.MustOrgID(c))
		if _, err := uuid.Parse(orgID); err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, reviewQueueErrorResponse{Error: "invalid organization scope"})
			return
		}

		queueRows, err := h.fetchQueueRows(c.Request.Context(), orgID, "pending")
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, reviewQueueErrorResponse{Error: err.Error()})
			return
		}

		items, err := h.buildQueueResponses(c.Request.Context(), orgID, queueRows)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, reviewQueueErrorResponse{Error: err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": items})
	}
}

func (h *reviewQueueHandler) getQueueItem() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.clients == nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, reviewQueueErrorResponse{Error: "database client not configured"})
			return
		}
		orgID := strings.TrimSpace(middleware.MustOrgID(c))
		queueID := strings.TrimSpace(c.Param("id"))
		if err := validateQueueID(queueID); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, reviewQueueErrorResponse{Error: err.Error()})
			return
		}

		row, err := h.fetchQueueRowByID(c.Request.Context(), orgID, queueID)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "not found") {
				c.AbortWithStatusJSON(http.StatusNotFound, reviewQueueErrorResponse{Error: "review queue item not found"})
				return
			}
			c.AbortWithStatusJSON(http.StatusBadRequest, reviewQueueErrorResponse{Error: err.Error()})
			return
		}

		items, err := h.buildQueueResponses(c.Request.Context(), orgID, []reviewQueueRow{row})
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, reviewQueueErrorResponse{Error: err.Error()})
			return
		}
		if len(items) != 1 {
			c.AbortWithStatusJSON(http.StatusNotFound, reviewQueueErrorResponse{Error: "review queue item not found"})
			return
		}
		c.JSON(http.StatusOK, items[0])
	}
}

func (h *reviewQueueHandler) approveQueueItem() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.clients == nil || h.prService == nil || h.attestationService == nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, reviewQueueErrorResponse{Error: "review approval services not configured"})
			return
		}
		orgID := strings.TrimSpace(middleware.MustOrgID(c))
		userID, ok := userIDFromContext(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, reviewQueueErrorResponse{Error: "invalid reviewer identity"})
			return
		}
		queueID := strings.TrimSpace(c.Param("id"))
		if err := validateQueueID(queueID); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, reviewQueueErrorResponse{Error: err.Error()})
			return
		}
		if _, err := uuid.Parse(userID); err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, reviewQueueErrorResponse{Error: "invalid reviewer identity"})
			return
		}

		queueRow, err := h.fetchQueueRowByID(c.Request.Context(), orgID, queueID)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "not found") {
				c.AbortWithStatusJSON(http.StatusNotFound, reviewQueueErrorResponse{Error: "review queue item not found"})
				return
			}
			c.AbortWithStatusJSON(http.StatusBadRequest, reviewQueueErrorResponse{Error: err.Error()})
			return
		}
		if strings.TrimSpace(queueRow.Status) != "pending" {
			c.AbortWithStatusJSON(http.StatusConflict, reviewQueueErrorResponse{Error: "review queue item is no longer pending"})
			return
		}
		if err := validateQueuePayload(queueRow); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, reviewQueueErrorResponse{Error: err.Error()})
			return
		}

		execution, bot, repo, err := h.loadExecutionContext(c.Request.Context(), orgID, queueRow.ExecutionID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, reviewQueueErrorResponse{Error: err.Error()})
			return
		}
		if err := validateExecutionContext(bot, repo); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, reviewQueueErrorResponse{Error: err.Error()})
			return
		}

		now := time.Now().UTC()
		if err := h.updateQueueReviewStatus(c.Request.Context(), orgID, queueID, "approved", userID, now); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, reviewQueueErrorResponse{Error: err.Error()})
			return
		}

		artifactHash := hashDiffContent(queueRow.DiffContent)
		executionUUID, err := uuid.Parse(strings.TrimSpace(queueRow.ExecutionID))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, reviewQueueErrorResponse{Error: "execution id is invalid"})
			return
		}
		attestationRecord, err := h.attestationService.SignAndAttest(c.Request.Context(), executionUUID, artifactHash)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadGateway, reviewQueueErrorResponse{Error: fmt.Sprintf("attestation failed: %v", err)})
			return
		}

		prURL, err := h.prService.CreateRemediationPR(c.Request.Context(), services.RemediationPRInput{
			OrgID:      orgID,
			Repo:       repo.GitHubRepoFullName,
			BranchName: buildQueueBranchName(queueRow.ExecutionID),
			Diff:       queueRow.DiffContent,
			PRTitle:    buildQueuePRTitle(bot.Name, execution.ID),
			Summary:    queueRow.ClaudeSummary,
			Attestation: services.AttestationReceipt{
				RekorLogIndex: attestationRecord.RekorLogIndex,
				RekorURL:      buildRekorEntryURL(h.rekorBaseURL, attestationRecord.RekorUUID),
				BotName:       bot.Name,
				BotVersion:    bot.Version,
			},
		})
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadGateway, reviewQueueErrorResponse{Error: fmt.Sprintf("pull request creation failed: %v", err)})
			return
		}

		c.JSON(http.StatusOK, reviewQueueApproveResponse{
			PRURL:         prURL,
			AttestationID: strings.TrimSpace(attestationRecord.ID),
		})
	}
}

func (h *reviewQueueHandler) rejectQueueItem() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.clients == nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, reviewQueueErrorResponse{Error: "database client not configured"})
			return
		}
		orgID := strings.TrimSpace(middleware.MustOrgID(c))
		userID, ok := userIDFromContext(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, reviewQueueErrorResponse{Error: "invalid reviewer identity"})
			return
		}
		queueID := strings.TrimSpace(c.Param("id"))
		if err := validateQueueID(queueID); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, reviewQueueErrorResponse{Error: err.Error()})
			return
		}
		if _, err := uuid.Parse(userID); err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, reviewQueueErrorResponse{Error: "invalid reviewer identity"})
			return
		}

		queueRow, err := h.fetchQueueRowByID(c.Request.Context(), orgID, queueID)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "not found") {
				c.AbortWithStatusJSON(http.StatusNotFound, reviewQueueErrorResponse{Error: "review queue item not found"})
				return
			}
			c.AbortWithStatusJSON(http.StatusBadRequest, reviewQueueErrorResponse{Error: err.Error()})
			return
		}
		if strings.TrimSpace(queueRow.Status) != "pending" {
			c.AbortWithStatusJSON(http.StatusConflict, reviewQueueErrorResponse{Error: "review queue item is no longer pending"})
			return
		}

		now := time.Now().UTC()
		if err := h.updateQueueReviewStatus(c.Request.Context(), orgID, queueID, "rejected", userID, now); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, reviewQueueErrorResponse{Error: err.Error()})
			return
		}
		if err := h.updateExecutionStatus(c.Request.Context(), orgID, queueRow.ExecutionID, "rejected"); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, reviewQueueErrorResponse{Error: err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

func (h *reviewQueueHandler) fetchQueueRows(_ context.Context, orgID, status string) ([]reviewQueueRow, error) {
	cleanOrgID := strings.TrimSpace(orgID)
	cleanStatus := strings.TrimSpace(status)
	if _, err := uuid.Parse(cleanOrgID); err != nil {
		return nil, fmt.Errorf("organization id is invalid")
	}
	if cleanStatus == "" || len(cleanStatus) > maxReviewQueueStatusLen {
		return nil, fmt.Errorf("queue status filter is invalid")
	}

	var rows []reviewQueueRow
	_, err := h.clients.AdminPostgrest().
		From("review_queue").
		Select("id,org_id,execution_id,diff_content,claude_summary,status,reviewed_by,reviewed_at,created_at", "", false).
		Eq("org_id", cleanOrgID).
		Eq("status", cleanStatus).
		Order("created_at", &postgrest.OrderOpts{Ascending: true}).
		ExecuteTo(&rows)
	if err != nil {
		return nil, fmt.Errorf("query review queue: %w", err)
	}
	return rows, nil
}

func (h *reviewQueueHandler) fetchQueueRowByID(_ context.Context, orgID, queueID string) (reviewQueueRow, error) {
	cleanOrgID := strings.TrimSpace(orgID)
	cleanQueueID := strings.TrimSpace(queueID)
	if _, err := uuid.Parse(cleanOrgID); err != nil {
		return reviewQueueRow{}, fmt.Errorf("organization id is invalid")
	}
	if _, err := uuid.Parse(cleanQueueID); err != nil {
		return reviewQueueRow{}, fmt.Errorf("queue id is invalid")
	}

	var rows []reviewQueueRow
	_, err := h.clients.AdminPostgrest().
		From("review_queue").
		Select("id,org_id,execution_id,diff_content,claude_summary,status,reviewed_by,reviewed_at,created_at", "", false).
		Eq("id", cleanQueueID).
		Eq("org_id", cleanOrgID).
		Limit(1, "").
		ExecuteTo(&rows)
	if err != nil {
		return reviewQueueRow{}, fmt.Errorf("query review queue item: %w", err)
	}
	if len(rows) != 1 {
		return reviewQueueRow{}, fmt.Errorf("review queue item not found")
	}
	return rows[0], nil
}

func (h *reviewQueueHandler) buildQueueResponses(_ context.Context, orgID string, rows []reviewQueueRow) ([]reviewQueueItemResponse, error) {
	if len(rows) == 0 {
		return []reviewQueueItemResponse{}, nil
	}

	executionIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		execID := strings.TrimSpace(row.ExecutionID)
		if _, err := uuid.Parse(execID); err != nil {
			return nil, fmt.Errorf("review queue execution id is invalid")
		}
		executionIDs = append(executionIDs, execID)
	}

	execMap, botMap, repoMap, err := h.loadExecutionContextMaps(orgID, executionIDs)
	if err != nil {
		return nil, err
	}

	items := make([]reviewQueueItemResponse, 0, len(rows))
	for _, row := range rows {
		execDetails, ok := execMap[strings.TrimSpace(row.ExecutionID)]
		if !ok {
			return nil, fmt.Errorf("execution details missing for queue item")
		}
		botDetails, ok := botMap[strings.TrimSpace(execDetails.BotID)]
		if !ok {
			return nil, fmt.Errorf("bot details missing for queue item")
		}
		repoDetails, ok := repoMap[strings.TrimSpace(execDetails.RepoID)]
		if !ok {
			return nil, fmt.Errorf("repo details missing for queue item")
		}
		items = append(items, reviewQueueItemResponse{
			ID:            row.ID,
			Status:        row.Status,
			DiffContent:   row.DiffContent,
			ClaudeSummary: row.ClaudeSummary,
			CreatedAt:     row.CreatedAt,
			Execution:     execDetails,
			Bot:           botDetails,
			Repo:          repoDetails,
		})
	}

	return items, nil
}

func (h *reviewQueueHandler) loadExecutionContext(_ context.Context, orgID, executionID string) (executionQueueDetails, botQueueDetails, repoQueueDetails, error) {
	execMap, botMap, repoMap, err := h.loadExecutionContextMaps(orgID, []string{executionID})
	if err != nil {
		return executionQueueDetails{}, botQueueDetails{}, repoQueueDetails{}, err
	}
	execution, ok := execMap[strings.TrimSpace(executionID)]
	if !ok {
		return executionQueueDetails{}, botQueueDetails{}, repoQueueDetails{}, fmt.Errorf("execution not found")
	}
	bot, ok := botMap[strings.TrimSpace(execution.BotID)]
	if !ok {
		return executionQueueDetails{}, botQueueDetails{}, repoQueueDetails{}, fmt.Errorf("bot not found")
	}
	repo, ok := repoMap[strings.TrimSpace(execution.RepoID)]
	if !ok {
		return executionQueueDetails{}, botQueueDetails{}, repoQueueDetails{}, fmt.Errorf("repo not found")
	}
	return execution, bot, repo, nil
}

func (h *reviewQueueHandler) loadExecutionContextMaps(orgID string, executionIDs []string) (map[string]executionQueueDetails, map[string]botQueueDetails, map[string]repoQueueDetails, error) {
	cleanOrgID := strings.TrimSpace(orgID)
	if _, err := uuid.Parse(cleanOrgID); err != nil {
		return nil, nil, nil, fmt.Errorf("organization id is invalid")
	}
	if len(executionIDs) == 0 {
		return map[string]executionQueueDetails{}, map[string]botQueueDetails{}, map[string]repoQueueDetails{}, nil
	}

	for _, executionID := range executionIDs {
		if _, err := uuid.Parse(strings.TrimSpace(executionID)); err != nil {
			return nil, nil, nil, fmt.Errorf("execution id is invalid")
		}
	}

	var executions []executionQueueDetails
	_, err := h.clients.AdminPostgrest().
		From("executions").
		Select("id,org_id,bot_id,repo_id,status,requires_review,started_at,completed_at,result,trigger_event", "", false).
		Eq("org_id", cleanOrgID).
		In("id", executionIDs).
		ExecuteTo(&executions)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("query executions: %w", err)
	}
	if len(executions) == 0 {
		return nil, nil, nil, fmt.Errorf("executions not found")
	}

	execMap := make(map[string]executionQueueDetails, len(executions))
	botIDs := make([]string, 0, len(executions))
	repoIDs := make([]string, 0, len(executions))
	for _, execution := range executions {
		key := strings.TrimSpace(execution.ID)
		if key == "" {
			return nil, nil, nil, fmt.Errorf("execution id is missing")
		}
		execMap[key] = execution
		botIDs = append(botIDs, strings.TrimSpace(execution.BotID))
		repoIDs = append(repoIDs, strings.TrimSpace(execution.RepoID))
	}

	var bots []botQueueDetails
	_, err = h.clients.AdminPostgrest().
		From("bots").
		Select("id,name,version", "", false).
		Eq("org_id", cleanOrgID).
		In("id", uniqueStrings(botIDs)).
		ExecuteTo(&bots)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("query bots: %w", err)
	}

	var repos []repoQueueDetails
	_, err = h.clients.AdminPostgrest().
		From("repos").
		Select("id,github_repo_full_name", "", false).
		Eq("org_id", cleanOrgID).
		In("id", uniqueStrings(repoIDs)).
		ExecuteTo(&repos)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("query repos: %w", err)
	}

	botMap := make(map[string]botQueueDetails, len(bots))
	for _, bot := range bots {
		botMap[strings.TrimSpace(bot.ID)] = bot
	}
	repoMap := make(map[string]repoQueueDetails, len(repos))
	for _, repo := range repos {
		repoMap[strings.TrimSpace(repo.ID)] = repo
	}

	return execMap, botMap, repoMap, nil
}

func (h *reviewQueueHandler) updateQueueReviewStatus(_ context.Context, orgID, queueID, status, reviewedBy string, reviewedAt time.Time) error {
	cleanOrgID := strings.TrimSpace(orgID)
	cleanQueueID := strings.TrimSpace(queueID)
	cleanStatus := strings.TrimSpace(status)
	cleanReviewedBy := strings.TrimSpace(reviewedBy)

	if _, err := uuid.Parse(cleanOrgID); err != nil {
		return fmt.Errorf("organization id is invalid")
	}
	if _, err := uuid.Parse(cleanQueueID); err != nil {
		return fmt.Errorf("queue id is invalid")
	}
	if _, err := uuid.Parse(cleanReviewedBy); err != nil {
		return fmt.Errorf("reviewer id is invalid")
	}
	if cleanStatus != "approved" && cleanStatus != "rejected" {
		return fmt.Errorf("review status transition is invalid")
	}

	updates := map[string]any{
		"status":      cleanStatus,
		"reviewed_by": cleanReviewedBy,
		"reviewed_at": reviewedAt.Format(time.RFC3339Nano),
	}
	_, _, err := h.clients.AdminPostgrest().
		From("review_queue").
		Update(updates, "", "").
		Eq("id", cleanQueueID).
		Eq("org_id", cleanOrgID).
		Execute()
	if err != nil {
		return fmt.Errorf("update review queue item: %w", err)
	}
	return nil
}

func (h *reviewQueueHandler) updateExecutionStatus(_ context.Context, orgID, executionID, status string) error {
	cleanOrgID := strings.TrimSpace(orgID)
	cleanExecutionID := strings.TrimSpace(executionID)
	cleanStatus := strings.TrimSpace(status)
	if _, err := uuid.Parse(cleanOrgID); err != nil {
		return fmt.Errorf("organization id is invalid")
	}
	if _, err := uuid.Parse(cleanExecutionID); err != nil {
		return fmt.Errorf("execution id is invalid")
	}
	if cleanStatus == "" || len(cleanStatus) > maxReviewQueueStatusLen {
		return fmt.Errorf("execution status is invalid")
	}

	updates := map[string]any{"status": cleanStatus}
	_, _, err := h.clients.AdminPostgrest().
		From("executions").
		Update(updates, "", "").
		Eq("id", cleanExecutionID).
		Eq("org_id", cleanOrgID).
		Execute()
	if err != nil {
		return fmt.Errorf("update execution status: %w", err)
	}
	return nil
}

func validateQueueID(queueID string) error {
	cleanQueueID := strings.TrimSpace(queueID)
	if cleanQueueID == "" || len(cleanQueueID) > maxQueueIDLength {
		return fmt.Errorf("queue id is invalid")
	}
	if _, err := uuid.Parse(cleanQueueID); err != nil {
		return fmt.Errorf("queue id is invalid")
	}
	return nil
}

func hashDiffContent(diff string) string {
	sum := sha256.Sum256([]byte(diff))
	return hex.EncodeToString(sum[:])
}

func buildQueueBranchName(executionID string) string {
	suffix := strings.ReplaceAll(strings.TrimSpace(executionID), "-", "")
	if len(suffix) > 24 {
		suffix = suffix[:24]
	}
	branch := "reconcileos/review-" + suffix
	if len(branch) > maxReviewBranchLength {
		return branch[:maxReviewBranchLength]
	}
	return branch
}

func buildQueuePRTitle(botName, executionID string) string {
	cleanBotName := strings.TrimSpace(botName)
	if cleanBotName == "" || len(cleanBotName) > maxReviewBotNameLength {
		cleanBotName = "ReconcileOS"
	}
	shortExecution := strings.TrimSpace(executionID)
	if len(shortExecution) > 8 {
		shortExecution = shortExecution[:8]
	}
	title := fmt.Sprintf("chore: %s remediation (%s)", cleanBotName, shortExecution)
	if len(title) > maxReviewPRTitleLength {
		return title[:maxReviewPRTitleLength]
	}
	return title
}

func buildRekorEntryURL(rekorBaseURL, rekorUUID string) string {
	cleanBaseURL := strings.TrimRight(strings.TrimSpace(rekorBaseURL), "/")
	cleanUUID := strings.TrimSpace(rekorUUID)
	if cleanBaseURL == "" {
		cleanBaseURL = "https://rekor.sigstore.dev"
	}
	if cleanUUID == "" {
		return cleanBaseURL
	}
	return cleanBaseURL + "/api/v1/log/entries/" + cleanUUID
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		cleanValue := strings.TrimSpace(value)
		if cleanValue == "" {
			continue
		}
		if _, ok := seen[cleanValue]; ok {
			continue
		}
		seen[cleanValue] = struct{}{}
		result = append(result, cleanValue)
	}
	return result
}

func validateQueuePayload(row reviewQueueRow) error {
	cleanDiff := strings.TrimSpace(row.DiffContent)
	cleanSummary := strings.TrimSpace(row.ClaudeSummary)
	if cleanDiff == "" || len(cleanDiff) > maxReviewQueueDiffLength {
		return fmt.Errorf("diff content is invalid")
	}
	if cleanSummary == "" || len(cleanSummary) > maxReviewSummaryLength {
		return fmt.Errorf("claude summary is invalid")
	}
	return nil
}

func validateExecutionContext(bot botQueueDetails, repo repoQueueDetails) error {
	cleanBotName := strings.TrimSpace(bot.Name)
	cleanBotVersion := strings.TrimSpace(bot.Version)
	cleanRepoName := strings.TrimSpace(repo.GitHubRepoFullName)
	if cleanBotName == "" || len(cleanBotName) > maxReviewBotNameLength {
		return fmt.Errorf("bot name is invalid")
	}
	if cleanBotVersion == "" || len(cleanBotVersion) > maxReviewBotNameLength {
		return fmt.Errorf("bot version is invalid")
	}
	if cleanRepoName == "" || len(cleanRepoName) > maxReviewRepoNameLength || !strings.Contains(cleanRepoName, "/") {
		return fmt.Errorf("repository full name is invalid")
	}
	return nil
}

func userIDFromContext(c *gin.Context) (string, bool) {
	rawUserID, ok := c.Get("user_id")
	if !ok {
		return "", false
	}
	userID, ok := rawUserID.(string)
	if !ok {
		return "", false
	}
	userID = strings.TrimSpace(userID)
	if userID == "" || len(userID) > 128 {
		return "", false
	}
	return userID, true
}
