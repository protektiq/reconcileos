package services

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	maxRepoNameLength    = 200
	maxPRTitleLength     = 256
	maxPRBodyLength      = 20000
	maxBranchLength      = 255
	maxGitHubCodeLength  = 1024
	maxGitHubStateLength = 1024
)

type GitHubService struct {
	appID         string
	privateKeyPEM string
	clientID      string
	clientSecret  string
	apiBaseURL    string
	httpClient    *http.Client
}

type GitHubUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Email string `json:"email"`
}

type githubTokenResponse struct {
	Token string `json:"token"`
}

type githubInstallationResponse struct {
	Account struct {
		Login string `json:"login"`
	} `json:"account"`
}

type githubRepoResponse struct {
	DefaultBranch string `json:"default_branch"`
}

type githubPRRequest struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Head  string `json:"head"`
	Base  string `json:"base"`
}

type githubOAuthTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
}

func NewGitHubService(
	appID string,
	privateKeyPEM string,
	clientID string,
	clientSecret string,
	apiBaseURL string,
) (*GitHubService, error) {
	cleanAppID := strings.TrimSpace(appID)
	cleanPrivateKey := strings.TrimSpace(privateKeyPEM)
	cleanClientID := strings.TrimSpace(clientID)
	cleanClientSecret := strings.TrimSpace(clientSecret)
	cleanAPIBaseURL := strings.TrimSpace(apiBaseURL)

	if cleanAppID == "" {
		return nil, fmt.Errorf("github app ID must not be empty")
	}
	if cleanPrivateKey == "" {
		return nil, fmt.Errorf("github app private key must not be empty")
	}
	if cleanClientID == "" {
		return nil, fmt.Errorf("github client ID must not be empty")
	}
	if cleanClientSecret == "" {
		return nil, fmt.Errorf("github client secret must not be empty")
	}
	if cleanAPIBaseURL == "" {
		return nil, fmt.Errorf("github API base URL must not be empty")
	}
	if _, err := url.ParseRequestURI(cleanAPIBaseURL); err != nil {
		return nil, fmt.Errorf("github API base URL is invalid: %w", err)
	}

	return &GitHubService{
		appID:         cleanAppID,
		privateKeyPEM: cleanPrivateKey,
		clientID:      cleanClientID,
		clientSecret:  cleanClientSecret,
		apiBaseURL:    strings.TrimRight(cleanAPIBaseURL, "/"),
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}, nil
}

func (s *GitHubService) GenerateInstallationToken(installationID int64) (string, error) {
	if installationID <= 0 {
		return "", fmt.Errorf("installation ID must be positive")
	}

	appJWT, err := s.generateAppJWT()
	if err != nil {
		return "", err
	}

	requestURL := fmt.Sprintf("%s/app/installations/%d/access_tokens", s.apiBaseURL, installationID)
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, requestURL, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("build installation token request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+appJWT)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	response, err := s.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("request installation token: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
		return "", fmt.Errorf("installation token request failed: status=%d body=%s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var tokenResponse githubTokenResponse
	if err := json.NewDecoder(response.Body).Decode(&tokenResponse); err != nil {
		return "", fmt.Errorf("decode installation token response: %w", err)
	}
	if strings.TrimSpace(tokenResponse.Token) == "" {
		return "", fmt.Errorf("installation token missing in response")
	}

	return tokenResponse.Token, nil
}

func (s *GitHubService) GetInstallationOrgSlug(installationID int64) (string, error) {
	if installationID <= 0 {
		return "", fmt.Errorf("installation ID must be positive")
	}

	appJWT, err := s.generateAppJWT()
	if err != nil {
		return "", err
	}

	requestURL := fmt.Sprintf("%s/app/installations/%d", s.apiBaseURL, installationID)
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, requestURL, nil)
	if err != nil {
		return "", fmt.Errorf("build installation request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+appJWT)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	response, err := s.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("request installation details: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
		return "", fmt.Errorf("installation details request failed: status=%d body=%s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var installationResponse githubInstallationResponse
	if err := json.NewDecoder(response.Body).Decode(&installationResponse); err != nil {
		return "", fmt.Errorf("decode installation details response: %w", err)
	}

	orgSlug := strings.TrimSpace(installationResponse.Account.Login)
	if orgSlug == "" {
		return "", fmt.Errorf("installation account login is empty")
	}

	return orgSlug, nil
}

func (s *GitHubService) OpenPullRequest(installationID int64, repo, title, body, branch string) error {
	cleanRepo := strings.TrimSpace(repo)
	cleanTitle := strings.TrimSpace(title)
	cleanBody := strings.TrimSpace(body)
	cleanBranch := strings.TrimSpace(branch)

	if installationID <= 0 {
		return fmt.Errorf("installation ID must be positive")
	}
	if cleanRepo == "" || len(cleanRepo) > maxRepoNameLength || !strings.Contains(cleanRepo, "/") {
		return fmt.Errorf("repo must be in owner/name format")
	}
	if cleanTitle == "" || len(cleanTitle) > maxPRTitleLength {
		return fmt.Errorf("pull request title must be 1-%d chars", maxPRTitleLength)
	}
	if len(cleanBody) > maxPRBodyLength {
		return fmt.Errorf("pull request body exceeds %d chars", maxPRBodyLength)
	}
	if cleanBranch == "" || len(cleanBranch) > maxBranchLength {
		return fmt.Errorf("branch must be 1-%d chars", maxBranchLength)
	}

	installationToken, err := s.GenerateInstallationToken(installationID)
	if err != nil {
		return err
	}

	defaultBranch, err := s.getRepositoryDefaultBranch(installationToken, cleanRepo)
	if err != nil {
		return err
	}

	requestBody := githubPRRequest{
		Title: cleanTitle,
		Body:  cleanBody,
		Head:  cleanBranch,
		Base:  defaultBranch,
	}
	bodyJSON, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("marshal pull request payload: %w", err)
	}

	requestURL := fmt.Sprintf("%s/repos/%s/pulls", s.apiBaseURL, cleanRepo)
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, requestURL, bytes.NewReader(bodyJSON))
	if err != nil {
		return fmt.Errorf("build pull request request: %w", err)
	}
	request.Header.Set("Authorization", "token "+installationToken)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("Content-Type", "application/json")

	response, err := s.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("create pull request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusCreated {
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
		return fmt.Errorf("create pull request failed: status=%d body=%s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	return nil
}

func (s *GitHubService) ExchangeOAuthCode(code, state string) (string, error) {
	cleanCode := strings.TrimSpace(code)
	cleanState := strings.TrimSpace(state)

	if cleanCode == "" || len(cleanCode) > maxGitHubCodeLength {
		return "", fmt.Errorf("oauth code must be 1-%d chars", maxGitHubCodeLength)
	}
	if len(cleanState) > maxGitHubStateLength {
		return "", fmt.Errorf("oauth state exceeds %d chars", maxGitHubStateLength)
	}

	values := url.Values{}
	values.Set("client_id", s.clientID)
	values.Set("client_secret", s.clientSecret)
	values.Set("code", cleanCode)
	if cleanState != "" {
		values.Set("state", cleanState)
	}

	request, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"https://github.com/login/oauth/access_token",
		strings.NewReader(values.Encode()),
	)
	if err != nil {
		return "", fmt.Errorf("build oauth token request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := s.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("exchange oauth code: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
		return "", fmt.Errorf("oauth code exchange failed: status=%d body=%s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	var tokenResponse githubOAuthTokenResponse
	if err := json.NewDecoder(response.Body).Decode(&tokenResponse); err != nil {
		return "", fmt.Errorf("decode oauth token response: %w", err)
	}

	token := strings.TrimSpace(tokenResponse.AccessToken)
	if token == "" {
		return "", fmt.Errorf("oauth token response missing access token")
	}

	return token, nil
}

func (s *GitHubService) GetAuthenticatedUser(accessToken string) (GitHubUser, error) {
	cleanAccessToken := strings.TrimSpace(accessToken)
	if cleanAccessToken == "" {
		return GitHubUser{}, fmt.Errorf("access token must not be empty")
	}

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, s.apiBaseURL+"/user", nil)
	if err != nil {
		return GitHubUser{}, fmt.Errorf("build github user request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+cleanAccessToken)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	response, err := s.httpClient.Do(request)
	if err != nil {
		return GitHubUser{}, fmt.Errorf("request github user: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
		return GitHubUser{}, fmt.Errorf("github user request failed: status=%d body=%s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	var user GitHubUser
	if err := json.NewDecoder(response.Body).Decode(&user); err != nil {
		return GitHubUser{}, fmt.Errorf("decode github user response: %w", err)
	}
	if user.ID <= 0 || strings.TrimSpace(user.Login) == "" {
		return GitHubUser{}, fmt.Errorf("github user response missing required fields")
	}

	return user, nil
}

func (s *GitHubService) generateAppJWT() (string, error) {
	privateKey, err := parseRSAPrivateKey(s.privateKeyPEM)
	if err != nil {
		return "", err
	}

	now := time.Now().UTC()
	claims := jwt.RegisteredClaims{
		Issuer:    s.appID,
		IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(now.Add(9 * time.Minute)),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signedToken, err := token.SignedString(privateKey)
	if err != nil {
		return "", fmt.Errorf("sign app jwt: %w", err)
	}

	return signedToken, nil
}

func parseRSAPrivateKey(privateKeyPEM string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return nil, fmt.Errorf("decode github app private key PEM")
	}

	if parsedPKCS1, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return parsedPKCS1, nil
	}

	parsedPKCS8, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse github app private key: %w", err)
	}

	typedKey, ok := parsedPKCS8.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("github app private key is not RSA")
	}

	return typedKey, nil
}

func (s *GitHubService) getRepositoryDefaultBranch(installationToken, repo string) (string, error) {
	requestURL := fmt.Sprintf("%s/repos/%s", s.apiBaseURL, repo)
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, requestURL, nil)
	if err != nil {
		return "", fmt.Errorf("build repository request: %w", err)
	}
	request.Header.Set("Authorization", "token "+installationToken)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	response, err := s.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("request repository details: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
		return "", fmt.Errorf("repository details request failed: status=%d body=%s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	var repoResponse githubRepoResponse
	if err := json.NewDecoder(response.Body).Decode(&repoResponse); err != nil {
		return "", fmt.Errorf("decode repository response: %w", err)
	}
	defaultBranch := strings.TrimSpace(repoResponse.DefaultBranch)
	if defaultBranch == "" {
		return "", fmt.Errorf("repository default branch is empty")
	}

	return defaultBranch, nil
}

func ParseInstallationID(raw string) (int64, error) {
	cleanRaw := strings.TrimSpace(raw)
	if cleanRaw == "" {
		return 0, fmt.Errorf("installation ID must not be empty")
	}

	installationID, err := strconv.ParseInt(cleanRaw, 10, 64)
	if err != nil || installationID <= 0 {
		return 0, fmt.Errorf("installation ID must be a positive integer")
	}

	return installationID, nil
}
