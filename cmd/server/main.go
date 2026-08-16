package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"batteryops/internal/httpapi"
	"batteryops/internal/notify"
	"batteryops/internal/scheduler"
	"batteryops/internal/service"
	"batteryops/internal/store"
)

// AppConfig is the JSON-deserialisable server configuration.
type AppConfig struct {
	Port                     int `json:"port"`
	ReportSLAMinutes         int `json:"report_sla_minutes"`
	DispatchSLAMinutes       int `json:"dispatch_sla_minutes"`
	TransferTimeoutMinutes   int `json:"transfer_timeout_minutes"`
	SchedulerIntervalSeconds int `json:"scheduler_interval_seconds"`
}

func defaultConfig() AppConfig {
	return AppConfig{
		Port:                     51219,
		ReportSLAMinutes:         15,
		DispatchSLAMinutes:       20,
		TransferTimeoutMinutes:   30,
		SchedulerIntervalSeconds: 30,
	}
}

func loadConfig(path string) AppConfig {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("config: %s not found, using defaults", path)
		return cfg
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Printf("config: failed to parse %s: %v, using defaults", path, err)
		return defaultConfig()
	}
	if cfg.Port == 0 {
		cfg.Port = 51219
	}
	return cfg
}

func main() {
	cfg := loadConfig("config.json")

	st := store.New()
	notifier := notify.NewSMSNotifier()
	svcCfg := service.Config{
		ReportSLA:       time.Duration(cfg.ReportSLAMinutes) * time.Minute,
		DispatchSLA:     time.Duration(cfg.DispatchSLAMinutes) * time.Minute,
		TransferTimeout: time.Duration(cfg.TransferTimeoutMinutes) * time.Minute,
	}
	svc := service.New(st, notifier, svcCfg)

	seedData(svc)

	sched := scheduler.New(svc, time.Duration(cfg.SchedulerIntervalSeconds)*time.Second)
	sched.Start()
	defer sched.Stop()

	server := httpapi.NewServer(svc)
	addr := fmt.Sprintf(":%d", cfg.Port)
	httpServer := &http.Server{
		Addr:         addr,
		Handler:      server.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("battery cabin operations platform listening on %s", addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	log.Println("server stopped")
}

// seedData registers sample cabins and spare parts so the platform is
// immediately usable after startup.
func seedData(svc *service.Service) {
	cabins := []struct {
		id, location string
		tempLimit    float64
	}{
		{"cabin-001", "Ejina Banner Site A", 45.0},
		{"cabin-002", "Ejina Banner Site B", 45.0},
		{"cabin-003", "Ejina Banner Site C", 45.0},
	}
	for _, c := range cabins {
		if _, err := svc.RegisterCabin(c.id, c.location, c.tempLimit); err != nil {
			log.Printf("seed: %v", err)
		}
	}

	parts := []struct {
		id, name, unit string
		stock, safety  int
	}{
		{"part-bms", "BMS Controller", "pcs", 5, 3},
		{"part-fuse", "DC Fuse 1500V", "pcs", 10, 5},
		{"part-cell", "LiFePO4 Cell 280Ah", "pcs", 20, 10},
	}
	for _, p := range parts {
		if _, err := svc.AddSparePart(p.id, p.name, p.unit, p.stock, p.safety); err != nil {
			log.Printf("seed: %v", err)
		}
	}
}
