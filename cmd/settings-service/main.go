package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/librescoot/settings-service/internal/service"
	"github.com/librescoot/settings-service/internal/wireguard"
)

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("settings-service %s\n", version)
		return
	}

	if os.Getenv("JOURNAL_STREAM") != "" {
		log.SetFlags(0)
	} else {
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	}

	log.Printf("librescoot-settings %s starting", version)
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	svc, err := service.New(redisAddr)
	if err != nil {
		log.Fatalf("Failed to create settings service: %v", err)
	}
	// Load initial settings from TOML file
	if err := svc.LoadSettingsFromTOML(); err != nil {
		log.Printf("Warning: Failed to load initial settings from TOML: %v", err)
	}

	// Initialize WireGuard connections
	wgRedis := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	wgCtx, wgCancel := context.WithCancel(context.Background())
	wgManager := wireguard.NewManager(wgRedis)
	go func() {
		defer wgRedis.Close()
		if err := wgManager.Initialize(wgCtx); err != nil {
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

	wgCancel()

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