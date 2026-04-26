package services

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"reconcileos.dev/api/db"

	"github.com/google/uuid"
)

const (
	maxPRSummaryLength = 20000
	maxBotFieldLength  = 128
)

var diffFilePattern = regexp.MustCompile(`(?m)^\+\+\+ b/(.+)$`)

type AttestationReceipt struct {
	RekorLogIndex int64  `json:"rekor_log_index"`
	RekorURL      string `json:"rekor_url"`
	BotName       string `json:"bot_name"`
	BotVersion    string `json:"bot_version"`
}

type RemediationPRInput struct {
	OrgID       string             `json:"org_id"`
	Repo        string             `json:"repo"`
	BranchName  string             `json:"branch_name"`
	Diff        string             `json:"diff"`
	PRTitle     string             `json:"pr_title"`
	Summary     string             `json:"summary"`
	Attestation AttestationReceipt `json:"attestation"`
}

type PRService struct {
	clients       *db.SupabaseClients
	githubService *GitHubService
}

type githubInstallationRecord struct {
	InstallationID int64 `json:"installation_id"`
}

func NewPRService(clients *db.SupabaseClients, githubService *GitHubService) (*PRService, error) {
	if clients == nil {
		return nil, fmt.Errorf("supabase clients must not be nil")
	}
	if githubService == nil {
		return nil, fmt.Errorf("github service must not be nil")
	}
	return &PRService{
		clients:       clients,
		githubService: githubService,
	}, nil
}

func (s *PRService) CreateRemediationPR(ctx context.Context, input RemediationPRInput) error {
	normalized, err := normalizeRemediationPRInput(input)
	if err != nil {
		return err
	}

	installationID, err := lookupInstallationIDByOrg(ctx, s.clients, normalized.OrgID)
	if err != nil {
		return fmt.Errorf("resolve github installation for org: %w", err)
	}

	changedFiles := extractChangedFilesFromDiff(normalized.Diff)
	prBody := buildRemediationPRBody(normalized.Summary, normalized.Attestation, changedFiles)

	if err := s.githubService.OpenPullRequest(
		installationID,
		normalized.Repo,
		normalized.PRTitle,
		prBody,
		normalized.BranchName,
	); err != nil {
		return fmt.Errorf("open remediation pull request: %w", err)
	}

	return nil
}

func normalizeRemediationPRInput(input RemediationPRInput) (RemediationPRInput, error) {
	input.OrgID = strings.TrimSpace(input.OrgID)
	input.Repo = strings.TrimSpace(input.Repo)
	input.BranchName = strings.TrimSpace(input.BranchName)
	input.Diff = strings.TrimSpace(input.Diff)
	input.PRTitle = strings.TrimSpace(input.PRTitle)
	input.Summary = strings.TrimSpace(input.Summary)
	input.Attestation.RekorURL = strings.TrimSpace(input.Attestation.RekorURL)
	input.Attestation.BotName = strings.TrimSpace(input.Attestation.BotName)
	input.Attestation.BotVersion = strings.TrimSpace(input.Attestation.BotVersion)

	if _, err := uuid.Parse(input.OrgID); err != nil {
		return RemediationPRInput{}, fmt.Errorf("org_id must be a valid UUID")
	}
	if input.Repo == "" || len(input.Repo) > maxRepoNameLength || !strings.Contains(input.Repo, "/") {
		return RemediationPRInput{}, fmt.Errorf("repo must be in owner/name format")
	}
	if input.BranchName == "" || len(input.BranchName) > maxBranchLength {
		return RemediationPRInput{}, fmt.Errorf("branch_name must be 1-%d chars", maxBranchLength)
	}
	if input.Diff == "" || len(input.Diff) > maxDiffLength {
		return RemediationPRInput{}, fmt.Errorf("diff must be 1-%d chars", maxDiffLength)
	}
	if input.PRTitle == "" || len(input.PRTitle) > maxPRTitleLength {
		return RemediationPRInput{}, fmt.Errorf("pr_title must be 1-%d chars", maxPRTitleLength)
	}
	if input.Summary == "" || len(input.Summary) > maxPRSummaryLength {
		return RemediationPRInput{}, fmt.Errorf("summary must be 1-%d chars", maxPRSummaryLength)
	}
	if input.Attestation.RekorLogIndex < 0 {
		return RemediationPRInput{}, fmt.Errorf("rekor_log_index must be non-negative")
	}
	if input.Attestation.RekorURL == "" || len(input.Attestation.RekorURL) > maxTriageContextLength {
		return RemediationPRInput{}, fmt.Errorf("rekor_url must be 1-%d chars", maxTriageContextLength)
	}
	if input.Attestation.BotName == "" || len(input.Attestation.BotName) > maxBotFieldLength {
		return RemediationPRInput{}, fmt.Errorf("bot_name must be 1-%d chars", maxBotFieldLength)
	}
	if input.Attestation.BotVersion == "" || len(input.Attestation.BotVersion) > maxBotFieldLength {
		return RemediationPRInput{}, fmt.Errorf("bot_version must be 1-%d chars", maxBotFieldLength)
	}

	return input, nil
}

func buildRemediationPRBody(summary string, receipt AttestationReceipt, changedFiles []string) string {
	lines := []string{
		"## ReconcileOS Automated Remediation",
		strings.TrimSpace(summary),
		"---",
		"### Attestation",
		"Rekor log index: " + strconv.FormatInt(receipt.RekorLogIndex, 10) + " | [View proof](" + receipt.RekorURL + ")",
		"Bot: " + receipt.BotName + "@" + receipt.BotVersion,
		"⚠️ AI-assisted — review before merging",
		"---",
		"### Changes",
	}

	if len(changedFiles) == 0 {
		lines = append(lines, "- (no file paths parsed from diff)")
	} else {
		for _, file := range changedFiles {
			lines = append(lines, "- "+file)
		}
	}

	return strings.Join(lines, "\n")
}

func extractChangedFilesFromDiff(diff string) []string {
	matches := diffFilePattern.FindAllStringSubmatch(diff, -1)
	unique := map[string]struct{}{}
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		cleanFile := strings.TrimSpace(match[1])
		if cleanFile == "" {
			continue
		}
		unique[cleanFile] = struct{}{}
	}

	changed := make([]string, 0, len(unique))
	for file := range unique {
		changed = append(changed, file)
	}
	sort.Strings(changed)
	return changed
}

func lookupInstallationIDByOrg(_ context.Context, clients *db.SupabaseClients, orgID string) (int64, error) {
	var installations []githubInstallationRecord
	_, err := clients.AdminPostgrest().
		From("github_installations").
		Select("installation_id", "", false).
		Eq("org_id", orgID).
		Limit(2, "").
		ExecuteTo(&installations)
	if err != nil {
		return 0, fmt.Errorf("lookup github installation for org: %w", err)
	}
	if len(installations) == 0 {
		return 0, fmt.Errorf("installation not found")
	}
	if len(installations) > 1 {
		return 0, fmt.Errorf("multiple installations found for org")
	}

	installationID := installations[0].InstallationID
	if installationID <= 0 {
		return 0, fmt.Errorf("installation_id is invalid")
	}

	return installationID, nil
}
