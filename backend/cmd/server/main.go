package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"pixel-agent-office/backend/internal/antigravity"
	"syscall"
	"time"

	"pixel-agent-office/backend/internal/orchestrator"
	"pixel-agent-office/backend/internal/server"
	"pixel-agent-office/backend/internal/store"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	initCtx, initCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer initCancel()
	db, err := store.Open(initCtx, databaseURL)
	if err != nil {
		return fmt.Errorf("database connection failed")
	}
	defer db.Pool.Close()
	if err := db.Migrate(initCtx); err != nil {
		return fmt.Errorf("database migration failed")
	}
	secret, err := server.RandomToken()
	if err != nil {
		return fmt.Errorf("cannot create bootstrap secret")
	}
	host := "127.0.0.1:8080"
	listener, err := net.Listen("tcp", host)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := db.RecoverInterrupted(initCtx); err != nil {
		return fmt.Errorf("cannot recover interrupted tasks")
	}
	recoveryCtx, recoveryCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := orchestrator.RecoverContainers(recoveryCtx, db); err != nil {
		log.Print("Antigravity container recovery deferred; affected projects remain blocked")
	}
	recoveryCancel()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	approvals := orchestrator.NewApprovals(db)
	worker := orchestrator.New(db, orchestrator.ExecuteCodex(approvals, db))
	runtimeCtx, runtimeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	agyRuntime, runtimeErr := antigravity.LoadOAuthRuntime(runtimeCtx)
	runtimeCancel()
	if runtimeErr == nil {
		worker = orchestrator.NewWithAntigravity(db, orchestrator.ExecuteCodex(approvals, db), orchestrator.ExecuteAntigravityOAuth(db, agyRuntime.DaemonID, agyRuntime.ImageID, agyRuntime.Volume))
		worker.SetAntigravityCleanup(orchestrator.AntigravityCleanup(db))
	} else {
		log.Print("Antigravity unavailable; configure the OAuth image and session volume, then restart Backend")
	}
	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.Run(ctx) }()
	handler := server.New(db, host, secret, approvals, worker)
	handler.SetAntigravityAvailable(runtimeErr == nil)
	if path := os.Getenv("FRONTEND_DIR"); path != "" {
		handler.SetFrontend(http.FileServer(http.Dir(path)))
	}
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	serverDone := make(chan error, 1)
	go func() { serverDone <- httpServer.Serve(listener) }()
	fmt.Printf("Local bootstrap secret (expires in 10 minutes): %s\n", secret)
	var result error
	select {
	case <-ctx.Done():
	case err = <-workerDone:
		if err != nil && err != context.Canceled {
			result = fmt.Errorf("task worker stopped")
		}
		workerDone = nil
	case err = <-serverDone:
		if err != nil && err != http.ErrServerClosed {
			result = err
		}
	}
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = httpServer.Shutdown(shutdownCtx)
	if workerDone != nil {
		<-workerDone
	}
	return result
}
