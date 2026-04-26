package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/reconcileos/reconcileos/runtime/executor"
	"github.com/reconcileos/reconcileos/runtime/internal/config"
	"github.com/reconcileos/reconcileos/runtime/internal/store"
	"github.com/reconcileos/reconcileos/runtime/queue"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.LoadFromEnv()
	if err != nil {
		logger.Error("load runtime config failed", "error", err)
		os.Exit(1)
	}

	runtimeStore := store.New(cfg.SupabaseURL, cfg.SupabaseServiceKey)
	dispatcher := queue.NewDispatcher(runtimeStore, logger, cfg.DispatcherInterval, cfg.DispatcherLockKey)
	runner := executor.NewRunner(runtimeStore, logger, cfg.ExecutorInterval, cfg.ExecutorLockKey, cfg.TmpRoot)

	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		dispatcher.Run(rootCtx)
	}()
	go func() {
		defer workers.Done()
		runner.Run(rootCtx)
	}()

	stopSignal := make(chan os.Signal, 1)
	signal.Notify(stopSignal, syscall.SIGTERM, syscall.SIGINT)
	<-stopSignal

	logger.Info("shutdown signal received, stopping new intake")
	cancel()

	waitDone := make(chan struct{})
	go func() {
		workers.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
	case <-time.After(30 * time.Second):
		logger.Warn("runtime loops did not stop before drain timeout")
	}

	logger.Info("draining in-progress executions")
	runner.Drain(30 * time.Second)
	logger.Info("runtime shutdown complete")
}
