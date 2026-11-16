package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"huashan/internal/api"
	"huashan/internal/auth"
	"huashan/internal/database"
	dockerruntime "huashan/internal/runtime"
	"huashan/internal/storage"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	cfg, err := loadConfig("config.ini")
	if err != nil {
		return err
	}
	root, err := storage.Open(cfg.Root)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := storage.CheckDiskBudget(root.Path, 0, cfg.MinFreeBytes); err != nil {
		return err
	}
	db, err := database.Open(root.DatabasePath(), root.PlatformID)
	if err != nil {
		return err
	}
	defer db.Close()
	service := auth.New(db)
	if err := initializeAdmin(context.Background(), service, cfg); err != nil {
		return err
	}
	cfg.Password = ""
	runner, err := dockerruntime.NewManager(db, root.Path, root.PlatformID, cfg.DockerEndpoint)
	if err != nil {
		return err
	}
	runner.PublicHost = cfg.RuntimePublicHost
	reconcile := func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := runner.Reconcile(cleanup); err != nil {
			log.Printf("runtime cleanup: %v", err)
		}
	}
	reconcile()
	configFile, err := filepath.Abs("config.ini")
	if err != nil {
		return err
	}
	handler := api.New(service, root, api.Config{ConfigFile: configFile, Listen: cfg.Listen, Runtime: runner, SecureCookie: cfg.SecureCookie, AllowedOrigin: cfg.AllowedOrigin, DockerEndpoint: cfg.DockerEndpoint, MinFreeBytes: cfg.MinFreeBytes})
	server := &http.Server{Addr: cfg.Listen, Handler: withDownloadTimeout(handler), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 2 * time.Minute, WriteTimeout: 2 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errs := make(chan error, 1)
	go func() {
		log.Printf("Huashan listening on %s; root=%s", cfg.Listen, root.Path)
		errs <- server.ListenAndServe()
	}()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	runtimeTicker := time.NewTicker(time.Minute)
	defer runtimeTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return server.Shutdown(shutdown)
		case err := <-errs:
			if err == http.ErrServerClosed {
				return nil
			}
			return err
		case <-runtimeTicker.C:
			reconcile()
		case <-ticker.C:
			if _, err = db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at<=?", time.Now().UnixMilli()); err != nil {
				log.Printf("session cleanup failed: %v", err)
			}
		}
	}
}

func initializeAdmin(ctx context.Context, service *auth.Service, cfg config) error {
	var count int
	if err := service.DB.QueryRowContext(ctx, "SELECT count(*) FROM users").Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return nil
	}
	if len(cfg.Password) < 12 || len(cfg.Password) > 128 {
		return fmt.Errorf("首次启动请设置 HUASHAN_ADMIN_PASSWORD（12–128 字节）；INI password 仅作兼容")
	}
	if _, err := service.CreateUser(ctx, "", cfg.Username, "管理员", cfg.Password, "ADMIN", true); err != nil {
		return fmt.Errorf("初始化管理员失败: %w", err)
	}
	return nil
}
