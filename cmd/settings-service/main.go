package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/librescoot/settings-service/internal/service"
	"github.com/librescoot/settings-service/internal/wireguard"
)

func main() {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	svc, err := service.New(redisAddr)
	if err != nil {
		log.Fatalf("Failed to create settings service: %v", err)
	}
	defer svc.Close()

	// Load initial settings from TOML file
	if err := svc.LoadSettingsFromTOML(); err != nil {
		log.Printf("Warning: Failed to load initial settings from TOML: %v", err)
	}

	// Initialize WireGuard connections
	wgManager := wireguard.NewManager()
	go func() {
		if err := wgManager.Initialize(); err != nil {
			log.Printf("Error initializing WireGuard: %v", err)
		}
	}()

	// Start watching for Redis updates
	go svc.WatchSettings()

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Println("Settings service started, waiting for updates...")
	<-sigChan

	log.Println("Shutting down settings service...")
	
	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	done := make(chan bool)
	go func() {
		svc.Close()
		done <- true
	}()
	
	select {
	case <-done:
		log.Println("Settings service stopped gracefully")
	case <-ctx.Done():
		log.Println("Settings service shutdown timeout")
	}
}