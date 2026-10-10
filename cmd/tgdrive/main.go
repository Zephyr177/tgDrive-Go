// Command tgdrive starts the tgDrive server.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Zephyr177/tgDrive-Go/internal/api"
	"github.com/Zephyr177/tgDrive-Go/internal/auth"
	"github.com/Zephyr177/tgDrive-Go/internal/bot"
	"github.com/Zephyr177/tgDrive-Go/internal/config"
	"github.com/Zephyr177/tgDrive-Go/internal/files"
	"github.com/Zephyr177/tgDrive-Go/internal/store"
	"github.com/Zephyr177/tgDrive-Go/internal/ui"
	"github.com/Zephyr177/tgDrive-Go/internal/webdav"
	"github.com/Zephyr177/tgDrive-Go/internal/ws"
)

func main() {
	cfg := config.Load()

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	hub := ws.NewHub()
	botCtl := bot.NewController(st, cfg.BotAPIURL)
	botCtl.AutoLoad()

	fileSvc := files.New(st, botCtl, hub) // botCtl implements telegram.Storage
	authMgr := auth.NewManager(cfg.TokenTimeout)
	webdavHandler := webdav.New(st, fileSvc, log.Default())

	srv := api.NewServer(cfg, st, authMgr, fileSvc, hub, botCtl, webdavHandler, ui.FS())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go botCtl.RunScheduler(ctx)

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 30 * time.Second,
	}
	go func() {
		log.Printf("tgDrive listening on %s (db=%s)", httpServer.Addr, cfg.DBPath)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serve: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	log.Printf("tgDrive stopped")
}
