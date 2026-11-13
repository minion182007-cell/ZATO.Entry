// Команда zato-entry запускает HTTP-сервис ZATO.Entry.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/minion182007-cell/ZATO.Entry/internal/api"
	"github.com/minion182007-cell/ZATO.Entry/internal/store"
	"github.com/minion182007-cell/ZATO.Entry/profiles"
	"github.com/minion182007-cell/ZATO.Entry/web"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	reg, err := profiles.Load()
	if err != nil {
		log.Error("загрузка профилей", "err", err)
		os.Exit(1)
	}

	addr := os.Getenv("ZATO_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	srv := &http.Server{
		Addr: addr,
		Handler: (&api.Server{
			Profiles: reg,
			Plans:    store.NewMemory(10000),
			Static:   web.Files(),
			Now:      time.Now,
			Log:      log,
		}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	log.Info("сервис запущен", "addr", addr, "profiles", len(reg.List()))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("сервер остановлен", "err", err)
		os.Exit(1)
	}
}
