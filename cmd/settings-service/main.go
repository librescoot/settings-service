package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/librescoot/settings-service/internal/config"
	"github.com/librescoot/settings-service/internal/service"
	"github.com/librescoot/settings-service/internal/wireguard"
)

var version = "dev"

// notifyReady sends READY=1 on $NOTIFY_SOCKET (sd_notify protocol). No-op
// when not running under systemd Type=notify. Go's net package maps a
// leading '@' to the abstract socket namespace, matching systemd's encoding.
func notifyReady() {
	socket := os.Getenv("NOTIFY_SOCKET")
	if socket == "" {
		return
	}
	conn, err := net.Dial("unixgram", socket)
	if err != nil {
		log.Printf("sd_notify: dial %s: %v", socket, err)
		return
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("READY=1")); err != nil {
		log.Printf("sd_notify: write: %v", err)
	}
}

func main() {
	showVersion := flag.Bool("version", false, "Print version and exit")
	settingsFile := flag.String("settings-file", "", "Path to settings TOML file (default: /data/settings.toml)")
	wgConfigDir := flag.String("wireguard-config-dir", "", "Path to WireGuard config directory (default: /data/wireguard)")
	schemaPath := flag.String("schema", "/usr/share/settings-service/settings.schema.json", "Path to settings schema file")
	flag.Parse()

	if *showVersion {
		fmt.Printf("settings-service %s\n", version)
		return
	}

	if *settingsFile != "" {
		config.TomlFilePath = *settingsFile
	}
	if *wgConfigDir != "" {
		wireguard.WireGuardConfigDir = *wgConfigDir
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

	svc, err := service.New(redisAddr, *schemaPath)
	if err != nil {
		log.Fatalf("Failed to create settings service: %v", err)
	}
	// Load initial settings from TOML file
	if err := svc.LoadSettingsFromTOML(); err != nil {
		log.Printf("Warning: Failed to load initial settings from TOML: %v", err)
	}

	// Re-apply the service overlay if it was active before reboot. Done after
	// the base load (so capture sees real base values) and before WatchSettings
	// starts so the overlay's own writes hit the no-clobber guard.
	svc.ReapplyOverlayOnBoot()

	// Tell systemd (Type=notify) the settings hash is seeded. Units ordered
	// after this service (e.g. Before=librescoot-vehicle.service) rely on the
	// hash being populated when they start; Redis itself is not persisted.
	notifyReady()

	// Sync WireGuard from /data/wireguard/ to NetworkManager in the
	// background. Blocks until NM is up (backoff) — settings-service must
	// stay startable early for other services even when NM isn't ready.
	wgCtx, wgCancel := context.WithCancel(context.Background())
	wgManager := wireguard.NewManager()
	go func() {
		if err := wgManager.Initialize(wgCtx); err != nil {
			log.Printf("Error syncing WireGuard: %v", err)
		}
	}()

	// Start watching for Redis updates
	go svc.WatchSettings()

	// Consume service:overlay apply/clear commands.
	go svc.RunOverlayConsumer()

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
