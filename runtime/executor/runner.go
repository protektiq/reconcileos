package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/reconcileos/reconcileos/runtime/internal/store"
	"github.com/reconcileos/reconcileos/runtime/manifest"
	"gopkg.in/yaml.v3"
)

const (
	maxExecStderrBytes = 64 * 1024
	maxExecStdoutBytes = 256 * 1024
	maxRunTimeout      = 10 * time.Minute
)

type Runner struct {
	store    *store.Store
	logger   *slog.Logger
	interval time.Duration
	lockKey  int64
	tmpRoot  string

	wg          sync.WaitGroup
	cancelMu    sync.Mutex
	activeKills map[string]context.CancelFunc
}

func NewRunner(runtimeStore *store.Store, logger *slog.Logger, interval time.Duration, lockKey int64, tmpRoot string) *Runner {
	return &Runner{
		store:       runtimeStore,
		logger:      logger,
		interval:    interval,
		lockKey:     lockKey,
		tmpRoot:     tmpRoot,
		activeKills: map[string]context.CancelFunc{},
	}
}

func (r *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		if err := r.runBatch(ctx); err != nil {
			r.logger.Error("executor batch failed", "error", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Runner) Drain(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return
	case <-time.After(timeout):
		r.cancelMu.Lock()
		for executionID, cancelFn := range r.activeKills {
			cancelFn()
			r.logger.Warn("force cancelled running execution during drain", "execution_id", executionID)
		}
		r.cancelMu.Unlock()
		<-done
	}
}

func (r *Runner) runBatch(ctx context.Context) error {
	locked, err := r.store.TryLock(ctx, r.lockKey)
	if err != nil {
		return fmt.Errorf("acquire executor lock: %w", err)
	}
	if !locked {
		return nil
	}
	defer func() {
		_, unlockErr := r.store.Unlock(context.Background(), r.lockKey)
		if unlockErr != nil {
			r.logger.Error("release executor lock failed", "error", unlockErr)
		}
	}()

	queued, err := r.store.ListQueuedExecutions(10)
	if err != nil {
		return err
	}

	for _, execution := range queued {
		claimed, claimErr := r.store.ClaimExecution(execution.ID)
		if claimErr != nil {
			r.logger.Error("claim execution failed", "execution_id", execution.ID, "error", claimErr)
			continue
		}
		if !claimed {
			continue
		}

		r.wg.Add(1)
		if runErr := r.executeOne(ctx, execution); runErr != nil {
			r.logger.Error("execution failed", "execution_id", execution.ID, "error", runErr)
		}
		r.wg.Done()
	}

	return nil
}

func (r *Runner) executeOne(ctx context.Context, execution store.ExecutionRecord) error {
	if !json.Valid(execution.TriggerEvent) {
		return r.failExecution(execution.ID, "invalid trigger event json", "", false)
	}

	botData, err := r.store.GetBot(execution.BotID)
	if err != nil {
		return r.failExecution(execution.ID, fmt.Sprintf("load bot failed: %v", err), "", false)
	}
	repoData, err := r.store.GetRepo(execution.RepoID)
	if err != nil {
		return r.failExecution(execution.ID, fmt.Sprintf("load repo failed: %v", err), "", false)
	}

	manifestData, err := parseManifestFromJSON(botData.Manifest)
	if err != nil {
		return r.failExecution(execution.ID, fmt.Sprintf("parse manifest failed: %v", err), "", false)
	}

	binaryPath := strings.TrimSpace(manifestData.Binary)
	if binaryPath == "" || !filepath.IsAbs(binaryPath) {
		return r.failExecution(execution.ID, "manifest binary must be absolute path", "", false)
	}
	fileInfo, statErr := os.Stat(binaryPath)
	if statErr != nil {
		return r.failExecution(execution.ID, fmt.Sprintf("binary stat failed: %v", statErr), "", false)
	}
	if fileInfo.IsDir() || fileInfo.Mode()&0o111 == 0 {
		return r.failExecution(execution.ID, "binary path is not an executable file", "", false)
	}

	workDir := filepath.Join(r.tmpRoot, execution.ID)
	if mkErr := os.MkdirAll(workDir, 0o700); mkErr != nil {
		return r.failExecution(execution.ID, fmt.Sprintf("create workdir failed: %v", mkErr), "", false)
	}

	timeout := maxRunTimeout
	manifestTimeout := time.Duration(manifestData.MaxTimeoutSeconds) * time.Second
	if manifestTimeout > 0 && manifestTimeout < timeout {
		timeout = manifestTimeout
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	r.registerCancel(execution.ID, cancel)
	defer func() {
		cancel()
		r.unregisterCancel(execution.ID)
	}()

	command := exec.CommandContext(runCtx, binaryPath)
	command.Dir = workDir
	command.Env = append(os.Environ(),
		"EXECUTION_ID="+execution.ID,
		"ORG_ID="+execution.OrgID,
		"REPO_FULL_NAME="+repoData.GitHubRepoFullName,
		"TRIGGER_EVENT="+string(execution.TriggerEvent),
	)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	runErr := command.Run()
	stdoutBytes := truncateBytes(stdout.Bytes(), maxExecStdoutBytes)
	stderrBytes := truncateBytes(stderr.Bytes(), maxExecStderrBytes)

	stdoutJSON, parsed := parseJSONOutput(stdoutBytes)
	requiresReview := false
	if parsed {
		if rawFlag, ok := stdoutJSON["requires_review"]; ok {
			flag, okBool := rawFlag.(bool)
			if okBool {
				requiresReview = flag
			}
		}
	}

	if runErr != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return r.failExecution(execution.ID, "execution timed out", string(stderrBytes), requiresReview)
		}
		return r.failExecution(execution.ID, fmt.Sprintf("execution failed: %v", runErr), string(stderrBytes), requiresReview)
	}

	result := map[string]any{
		"stdout": stdoutJSON,
		"stderr": string(stderrBytes),
	}
	if !parsed {
		result["stdout_raw"] = string(stdoutBytes)
	}
	return r.store.FinalizeExecution(execution.ID, "completed", result, requiresReview)
}

func (r *Runner) failExecution(executionID, message, stderr string, requiresReview bool) error {
	result := map[string]any{
		"error":  message,
		"stderr": stderr,
	}
	return r.store.FinalizeExecution(executionID, "failed", result, requiresReview)
}

func (r *Runner) registerCancel(executionID string, cancel context.CancelFunc) {
	r.cancelMu.Lock()
	defer r.cancelMu.Unlock()
	r.activeKills[executionID] = cancel
}

func (r *Runner) unregisterCancel(executionID string) {
	r.cancelMu.Lock()
	defer r.cancelMu.Unlock()
	delete(r.activeKills, executionID)
}

func parseManifestFromJSON(rawJSON []byte) (manifest.BotManifest, error) {
	if !json.Valid(rawJSON) {
		return manifest.BotManifest{}, errors.New("manifest json is invalid")
	}
	var content map[string]any
	if err := json.Unmarshal(rawJSON, &content); err != nil {
		return manifest.BotManifest{}, err
	}
	yamlBytes, err := yaml.Marshal(content)
	if err != nil {
		return manifest.BotManifest{}, err
	}
	return manifest.Parse(yamlBytes)
}

func parseJSONOutput(raw []byte) (map[string]any, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return map[string]any{}, false
	}
	var decoded map[string]any
	if err := json.Unmarshal(trimmed, &decoded); err != nil {
		return map[string]any{}, false
	}
	return decoded, true
}

func truncateBytes(content []byte, maxLen int) []byte {
	if len(content) <= maxLen {
		return content
	}
	return content[:maxLen]
}
