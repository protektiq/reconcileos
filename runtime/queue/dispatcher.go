package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/reconcileos/reconcileos/runtime/internal/store"
	"github.com/reconcileos/reconcileos/runtime/manifest"
	"gopkg.in/yaml.v3"
)

type Dispatcher struct {
	store    *store.Store
	logger   *slog.Logger
	interval time.Duration
	lockKey  int64
}

func NewDispatcher(runtimeStore *store.Store, logger *slog.Logger, interval time.Duration, lockKey int64) *Dispatcher {
	return &Dispatcher{
		store:    runtimeStore,
		logger:   logger,
		interval: interval,
		lockKey:  lockKey,
	}
}

func (d *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()

	for {
		if err := d.dispatchBatch(ctx); err != nil {
			d.logger.Error("dispatcher batch failed", "error", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *Dispatcher) dispatchBatch(ctx context.Context) error {
	locked, err := d.store.TryLock(ctx, d.lockKey)
	if err != nil {
		return fmt.Errorf("acquire dispatcher lock: %w", err)
	}
	if !locked {
		return nil
	}
	defer func() {
		_, unlockErr := d.store.Unlock(context.Background(), d.lockKey)
		if unlockErr != nil {
			d.logger.Error("release dispatcher lock failed", "error", unlockErr)
		}
	}()

	events, err := d.store.ListUnprocessedEvents(50)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := d.dispatchEvent(event); err != nil {
			d.logger.Error("dispatch event failed", "event_id", event.ID, "error", err)
			continue
		}
		if markErr := d.store.MarkEventProcessed(event.ID); markErr != nil {
			d.logger.Error("mark event processed failed", "event_id", event.ID, "error", markErr)
		}
	}
	return nil
}

func (d *Dispatcher) dispatchEvent(event store.EventRecord) error {
	eventType := strings.TrimSpace(event.EventType)
	if eventType == "" {
		return fmt.Errorf("event_type is empty")
	}
	if len(eventType) > 128 {
		return fmt.Errorf("event_type exceeds max length")
	}
	if !json.Valid(event.Payload) {
		return fmt.Errorf("event payload is invalid json")
	}

	installations, err := d.store.ListActiveInstallations(event.OrgID)
	if err != nil {
		return err
	}
	for _, installation := range installations {
		botRecord, botErr := d.store.GetBot(installation.BotID)
		if botErr != nil {
			d.logger.Error("fetch bot failed", "bot_id", installation.BotID, "error", botErr)
			continue
		}
		if len(botRecord.Manifest) == 0 || !json.Valid(botRecord.Manifest) {
			d.logger.Error("bot manifest missing or invalid", "bot_id", botRecord.ID)
			continue
		}

		manifestYAML, convertErr := jsonManifestToYAML(botRecord.Manifest)
		if convertErr != nil {
			d.logger.Error("convert bot manifest failed", "bot_id", botRecord.ID, "error", convertErr)
			continue
		}

		parsed, parseErr := manifest.Parse(manifestYAML)
		if parseErr != nil {
			d.logger.Error("parse bot manifest failed", "bot_id", botRecord.ID, "error", parseErr)
			continue
		}
		if !eventMatches(parsed, eventType) {
			continue
		}

		if insertErr := d.store.InsertExecutionQueued(event.OrgID, botRecord.ID, event.RepoID, event.Payload); insertErr != nil {
			d.logger.Error("enqueue execution failed", "event_id", event.ID, "bot_id", botRecord.ID, "error", insertErr)
		}
	}

	return nil
}

func eventMatches(manifestData manifest.BotManifest, eventType string) bool {
	for _, trigger := range manifestData.Triggers {
		if strings.EqualFold(strings.TrimSpace(trigger), strings.TrimSpace(eventType)) {
			return true
		}
	}
	return false
}

func jsonManifestToYAML(rawJSON []byte) ([]byte, error) {
	var content map[string]any
	if err := json.Unmarshal(rawJSON, &content); err != nil {
		return nil, fmt.Errorf("decode manifest json: %w", err)
	}
	converted, err := yaml.Marshal(content)
	if err != nil {
		return nil, fmt.Errorf("encode manifest yaml: %w", err)
	}
	return converted, nil
}
