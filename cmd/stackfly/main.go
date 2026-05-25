package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"stackfly/internal/addon"
	"stackfly/internal/backup"
	"stackfly/internal/builder"
	"stackfly/internal/config"
	"stackfly/internal/deployer"
	"stackfly/internal/gitmanager"
	"stackfly/internal/monitor"
	"stackfly/internal/proxy"
	"stackfly/internal/store"
	"stackfly/internal/web"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "update" {
		runUpdate()
		return
	}

	homeDir, _ := os.UserHomeDir()
	defaultDataDir := filepath.Join(homeDir, ".stackfly")

	dataDir := flag.String("data-dir", defaultDataDir, "Data directory")
	port := flag.Int("port", 3000, "HTTP port")
	bind := flag.String("bind", "", "Bind address (default: auto-detect Tailscale IP)")
	flag.Parse()

	cfg, err := config.New(*dataDir, *port)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	s, err := store.New(cfg.DBPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer s.Close()

	host := *bind
	if host == "" {
		host = detectTailscaleIP()
	}
	cfg.Host = host
	cfg.Version = version

	git := gitmanager.New(cfg)
	prx := proxy.New(cfg, s)
	bld := builder.New(cfg, s, git)
	dep := deployer.New(cfg, s, prx)
	adm := addon.New(s)

	mon := monitor.New()
	bkp := backup.New(s)
	handler := web.NewHandler(cfg, s, bld, dep, git, prx, adm, mon, bkp)
	router := handler.SetupRoutes()

	listenAddr := fmt.Sprintf("%s:%d", host, cfg.Port)
	srv := &http.Server{
		Addr:         listenAddr,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		ctx := context.Background()
		if err := prx.EnsureRunning(ctx); err != nil {
			log.Printf("Warning: could not start Caddy: %v", err)
		}
		if err := dep.ReconnectNetworks(ctx); err != nil {
			log.Printf("Warning: could not reconnect networks: %v", err)
		}
	}()

	backupCtx, backupCancel := context.WithCancel(context.Background())
	go bkp.StartScheduler(backupCtx)

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("StackFly running on http://%s", listenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	<-done
	log.Println("Shutting down...")
	backupCancel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}

func runUpdate() {
	updateFlags := flag.NewFlagSet("update", flag.ExitOnError)
	dataDir := updateFlags.String("data-dir", "", "Data directory")
	updateFlags.Parse(os.Args[2:])

	if *dataDir == "" {
		homeDir, _ := os.UserHomeDir()
		*dataDir = filepath.Join(homeDir, ".stackfly")
	}

	cfg, err := config.New(*dataDir, 0)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	s, err := store.New(cfg.DBPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	prx := proxy.New(cfg, s)

	fmt.Println("==> Updating Caddy...")
	if err := prx.Update(ctx); err != nil {
		log.Fatalf("caddy update failed: %v", err)
	}
	fmt.Println("    Done")

	fmt.Println("==> Updating nixpacks...")
	cmd := exec.Command("bash", "-c", "curl -sSL https://nixpacks.com/install.sh | bash -s -- -y")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("nixpacks update failed: %v", err)
	}
	fmt.Println("    Done")

	fmt.Println("")
	fmt.Println("All components updated.")
}

func detectTailscaleIP() string {
	out, err := exec.Command("tailscale", "ip", "-4").Output()
	if err != nil {
		log.Println("Tailscale not detected, binding to 127.0.0.1")
		return "127.0.0.1"
	}
	ip := strings.TrimSpace(string(out))
	log.Printf("Detected Tailscale IP: %s", ip)
	return ip
}

