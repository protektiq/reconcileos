package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	defaultAnthropicBaseURL   = "https://api.anthropic.com"
	defaultAnthropicModel     = "claude-sonnet-4-20250514"
	anthropicVersion          = "2023-06-01"
	anthropicMaxTokens        = 1000
	maxTriageFieldLength      = 512
	maxTriageContextLength    = 4000
	maxDependencyTreeItems    = 200
	maxAnthropicResponseBytes = 65536
	maxDiffLength             = 100000
	maxRecipesApplied         = 100
	maxRecipeNameLength       = 200
	maxReasoningLength        = 1200
)

var cvePattern = regexp.MustCompile(`^CVE-\d{4}-\d{4,}$`)

type TriageInput struct {
	CVEID            string   `json:"cve_id"`
	CVSSScore        float64  `json:"cvss_score"`
	CVSSVector       string   `json:"cvss_vector"`
	AffectedPackage  string   `json:"affected_package"`
	AffectedVersion  string   `json:"affected_version"`
	OrgID            string   `json:"org_id"`
	RepoFullName     string   `json:"repo_full_name"`
	DependencyTree   []string `json:"dependency_tree"`
	TechStackContext string   `json:"tech_stack_context"`
}

type TriageResult struct {
	ContextualExploitability float64 `json:"contextual_exploitability"`
	BlastRadius              string  `json:"blast_radius"`
	RecommendedAction        string  `json:"recommended_action"`
	Reasoning                string  `json:"reasoning"`
	PriorityRank             string  `json:"priority_rank"`
}

type TriageService struct {
	apiKey     string
	baseURL    string
	model      string
	httpClient *http.Client
}

type anthropicMessagesRequest struct {
	Model     string             `json:"model"`
	System    string             `json:"system"`
	MaxTokens int                `json:"max_tokens"`
	Metadata  map[string]string  `json:"metadata"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicMessagesResponse struct {
	Content []anthropicContentBlock `json:"content"`
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func NewTriageService(apiKey, baseURL, model string) (*TriageService, error) {
	cleanAPIKey := strings.TrimSpace(apiKey)
	cleanBaseURL := strings.TrimSpace(baseURL)
	cleanModel := strings.TrimSpace(model)

	if cleanAPIKey == "" {
		return nil, fmt.Errorf("anthropic API key must not be empty")
	}
	if cleanBaseURL == "" {
		cleanBaseURL = defaultAnthropicBaseURL
	}
	if _, err := url.ParseRequestURI(cleanBaseURL); err != nil {
		return nil, fmt.Errorf("anthropic base URL is invalid: %w", err)
	}
	if cleanModel == "" {
		cleanModel = defaultAnthropicModel
	}

	return &TriageService{
		apiKey:  cleanAPIKey,
		baseURL: strings.TrimRight(cleanBaseURL, "/"),
		model:   cleanModel,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}, nil
}

func (s *TriageService) ScoreCVE(ctx context.Context, input TriageInput) (TriageResult, error) {
	normalizedInput, err := normalizeTriageInput(input)
	if err != nil {
		return TriageResult{}, err
	}

	systemPrompt := "You are a security triage assistant for a software engineering organization.\nYour job is to assess the real-world exploitability of a CVE in context.\nAlways respond in valid JSON only. No preamble. No markdown."
	userPrompt := fmt.Sprintf(
		"Assess this CVE for the given codebase context:\nCVE: %s | CVSS: %s (%s)\nAffected: %s@%s\nBlast radius: %s\nTech stack: %s\n\nRespond with JSON:\n{\n  \"contextual_exploitability\": <0-10 float>,\n  \"blast_radius\": \"LOW|MEDIUM|HIGH|CRITICAL\",\n  \"recommended_action\": \"auto-patch|human-review|accept-risk\",\n  \"reasoning\": \"<2-3 sentence plain English explanation>\",\n  \"priority_rank\": \"P1|P2|P3|P4\"\n}",
		normalizedInput.CVEID,
		strconv.FormatFloat(normalizedInput.CVSSScore, 'f', -1, 64),
		normalizedInput.CVSSVector,
		normalizedInput.AffectedPackage,
		normalizedInput.AffectedVersion,
		strings.Join(normalizedInput.DependencyTree, ", "),
		normalizedInput.TechStackContext,
	)

	body, err := s.invokeClaude(ctx, normalizedInput.OrgID, systemPrompt, userPrompt)
	if err != nil {
		return TriageResult{}, err
	}

	result, err := parseTriageResult(body)
	if err != nil {
		return TriageResult{}, err
	}

	return result, nil
}

func (s *TriageService) GeneratePRSummary(ctx context.Context, diff string, recipesApplied []string, cveID string) (string, error) {
	cleanDiff := strings.TrimSpace(diff)
	cleanCVEID := strings.ToUpper(strings.TrimSpace(cveID))
	if cleanDiff == "" || len(cleanDiff) > maxDiffLength {
		return "", fmt.Errorf("diff must be 1-%d chars", maxDiffLength)
	}
	if !cvePattern.MatchString(cleanCVEID) {
		return "", fmt.Errorf("cve_id must be in CVE-YYYY-NNNN format")
	}
	if len(recipesApplied) == 0 || len(recipesApplied) > maxRecipesApplied {
		return "", fmt.Errorf("recipes_applied must contain 1-%d items", maxRecipesApplied)
	}

	normalizedRecipes := make([]string, 0, len(recipesApplied))
	for _, recipe := range recipesApplied {
		cleanRecipe := strings.TrimSpace(recipe)
		if cleanRecipe == "" || len(cleanRecipe) > maxRecipeNameLength {
			return "", fmt.Errorf("recipe name must be 1-%d chars", maxRecipeNameLength)
		}
		normalizedRecipes = append(normalizedRecipes, cleanRecipe)
	}

	orgID, err := orgIDFromContext(ctx)
	if err != nil {
		return "", err
	}

	systemPrompt := "You are an engineering security assistant. Return plain text only."
	userPrompt := fmt.Sprintf(
		"Create a 3-5 sentence PR summary for CVE %s.\nRecipes applied: %s\n\nUnified diff:\n%s\n\nThe summary must include: what changed, why, what to test, and any breaking change risk.",
		cleanCVEID,
		strings.Join(normalizedRecipes, ", "),
		cleanDiff,
	)

	summaryText, err := s.invokeClaude(ctx, orgID, systemPrompt, userPrompt)
	if err != nil {
		return "", err
	}

	cleanSummary := strings.TrimSpace(summaryText)
	if cleanSummary == "" {
		return "", fmt.Errorf("claude summary response is empty")
	}

	return "⚠️ AI-assisted summary — review before merging\n\n" + cleanSummary, nil
}

func orgIDFromContext(ctx context.Context) (string, error) {
	rawOrgID := strings.TrimSpace(fmt.Sprint(ctx.Value("org_id")))
	if rawOrgID == "" || rawOrgID == "<nil>" {
		return "", fmt.Errorf("org_id missing from context")
	}
	if _, err := uuid.Parse(rawOrgID); err != nil {
		return "", fmt.Errorf("org_id in context is invalid")
	}
	return rawOrgID, nil
}

func normalizeTriageInput(input TriageInput) (TriageInput, error) {
	input.CVEID = strings.ToUpper(strings.TrimSpace(input.CVEID))
	input.CVSSVector = strings.TrimSpace(input.CVSSVector)
	input.AffectedPackage = strings.TrimSpace(input.AffectedPackage)
	input.AffectedVersion = strings.TrimSpace(input.AffectedVersion)
	input.OrgID = strings.TrimSpace(input.OrgID)
	input.RepoFullName = strings.TrimSpace(input.RepoFullName)
	input.TechStackContext = strings.TrimSpace(input.TechStackContext)

	if !cvePattern.MatchString(input.CVEID) {
		return TriageInput{}, fmt.Errorf("cve_id must be in CVE-YYYY-NNNN format")
	}
	if input.CVSSScore < 0 || input.CVSSScore > 10 {
		return TriageInput{}, fmt.Errorf("cvss_score must be between 0 and 10")
	}
	if input.CVSSVector == "" || len(input.CVSSVector) > maxTriageFieldLength {
		return TriageInput{}, fmt.Errorf("cvss_vector must be 1-%d chars", maxTriageFieldLength)
	}
	if input.AffectedPackage == "" || len(input.AffectedPackage) > maxTriageFieldLength {
		return TriageInput{}, fmt.Errorf("affected_package must be 1-%d chars", maxTriageFieldLength)
	}
	if input.AffectedVersion == "" || len(input.AffectedVersion) > maxTriageFieldLength {
		return TriageInput{}, fmt.Errorf("affected_version must be 1-%d chars", maxTriageFieldLength)
	}
	if _, err := uuid.Parse(input.OrgID); err != nil {
		return TriageInput{}, fmt.Errorf("org_id must be a valid UUID")
	}
	if input.RepoFullName == "" || len(input.RepoFullName) > maxTriageFieldLength || !strings.Contains(input.RepoFullName, "/") {
		return TriageInput{}, fmt.Errorf("repo_full_name must be in owner/name format")
	}
	if len(input.DependencyTree) > maxDependencyTreeItems {
		return TriageInput{}, fmt.Errorf("dependency_tree supports up to %d items", maxDependencyTreeItems)
	}
	for idx, dep := range input.DependencyTree {
		cleanDep := strings.TrimSpace(dep)
		if cleanDep == "" || len(cleanDep) > maxTriageFieldLength {
			return TriageInput{}, fmt.Errorf("dependency_tree[%d] must be 1-%d chars", idx, maxTriageFieldLength)
		}
		input.DependencyTree[idx] = cleanDep
	}
	if input.TechStackContext == "" || len(input.TechStackContext) > maxTriageContextLength {
		return TriageInput{}, fmt.Errorf("tech_stack_context must be 1-%d chars", maxTriageContextLength)
	}

	return input, nil
}

func parseTriageResult(rawResponse string) (TriageResult, error) {
	jsonObject, err := extractJSONObject(rawResponse)
	if err != nil {
		return TriageResult{}, err
	}

	var result TriageResult
	if err := json.Unmarshal([]byte(jsonObject), &result); err != nil {
		return TriageResult{}, fmt.Errorf("decode triage response json: %w", err)
	}

	result.BlastRadius = strings.ToUpper(strings.TrimSpace(result.BlastRadius))
	result.RecommendedAction = strings.TrimSpace(result.RecommendedAction)
	result.PriorityRank = strings.ToUpper(strings.TrimSpace(result.PriorityRank))
	result.Reasoning = strings.TrimSpace(result.Reasoning)

	if result.ContextualExploitability < 0 || result.ContextualExploitability > 10 {
		return TriageResult{}, fmt.Errorf("contextual_exploitability must be between 0 and 10")
	}
	switch result.BlastRadius {
	case "LOW", "MEDIUM", "HIGH", "CRITICAL":
	default:
		return TriageResult{}, fmt.Errorf("blast_radius must be LOW, MEDIUM, HIGH, or CRITICAL")
	}
	switch result.RecommendedAction {
	case "auto-patch", "human-review", "accept-risk":
	default:
		return TriageResult{}, fmt.Errorf("recommended_action must be auto-patch, human-review, or accept-risk")
	}
	if result.Reasoning == "" || len(result.Reasoning) > maxReasoningLength {
		return TriageResult{}, fmt.Errorf("reasoning must be 1-%d chars", maxReasoningLength)
	}
	switch result.PriorityRank {
	case "P1", "P2", "P3", "P4":
	default:
		return TriageResult{}, fmt.Errorf("priority_rank must be P1, P2, P3, or P4")
	}

	return result, nil
}

func extractJSONObject(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("claude response is empty")
	}

	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start < 0 || end < 0 || end <= start {
		return "", fmt.Errorf("claude response did not contain a JSON object")
	}

	return strings.TrimSpace(trimmed[start : end+1]), nil
}

func (s *TriageService) invokeClaude(ctx context.Context, orgID, systemPrompt, userPrompt string) (string, error) {
	reqPayload := anthropicMessagesRequest{
		Model:     s.model,
		System:    systemPrompt,
		MaxTokens: anthropicMaxTokens,
		Metadata: map[string]string{
			"org_id": orgID,
		},
		Messages: []anthropicMessage{
			{
				Role:    "user",
				Content: userPrompt,
			},
		},
	}
	bodyJSON, err := json.Marshal(reqPayload)
	if err != nil {
		return "", fmt.Errorf("marshal anthropic request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/v1/messages", bytes.NewReader(bodyJSON))
	if err != nil {
		return "", fmt.Errorf("build anthropic request: %w", err)
	}
	request.Header.Set("x-api-key", s.apiKey)
	request.Header.Set("anthropic-version", anthropicVersion)
	request.Header.Set("content-type", "application/json")

	response, err := s.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("request anthropic: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
		return "", fmt.Errorf("anthropic request failed: status=%d body=%s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxAnthropicResponseBytes))
	if err != nil {
		return "", fmt.Errorf("read anthropic response: %w", err)
	}

	var messageResponse anthropicMessagesResponse
	if err := json.Unmarshal(responseBody, &messageResponse); err != nil {
		return "", fmt.Errorf("decode anthropic response: %w", err)
	}
	for _, part := range messageResponse.Content {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			return part.Text, nil
		}
	}

	return "", fmt.Errorf("anthropic response missing text content")
}
