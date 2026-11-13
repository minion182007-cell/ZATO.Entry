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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// DATABASE_URL задаёт PostgreSQL; без него данные живут в памяти.
	var st store.Store
	if url := os.Getenv("DATABASE_URL"); url != "" {
		pg, err := store.OpenPostgres(ctx, url)
		if err != nil {
			log.Error("база данных", "err", err)
			os.Exit(1)
		}
		st = pg
		log.Info("хранилище: PostgreSQL")
	} else {
		st = store.NewMemory()
		log.Warn("DATABASE_URL не задан: данные хранятся в памяти и пропадут при перезапуске")
	}
	defer st.Close()

	addr := os.Getenv("ZATO_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	srv := &http.Server{
		Addr: addr,
		Handler: (&api.Server{
			Profiles: reg,
			Store:    st,
			Static:   web.Files(),
			Now:      time.Now,
			Log:      log,
		}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Планы без владельца живут 30 дней.
	go func() {
		tick := time.NewTicker(6 * time.Hour)
		defer tick.Stop()
		for {
			if n, err := st.PurgeAnonymousPlans(ctx, time.Now().AddDate(0, 0, -30)); err != nil {
				log.Error("очистка планов", "err", err)
			} else if n > 0 {
				log.Info("удалены устаревшие планы", "count", n)
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()

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
