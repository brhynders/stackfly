package web

import (
	"bufio"
	"context"
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"stackfly/internal/addon"
	"stackfly/internal/backup"
	"stackfly/internal/builder"
	"stackfly/internal/config"
	"stackfly/internal/deployer"
	"stackfly/internal/gitmanager"
	"stackfly/internal/models"
	"stackfly/internal/monitor"
	"stackfly/internal/proxy"
	"stackfly/internal/store"
)

//go:embed templates templates/partials
var templateFS embed.FS

type Handler struct {
	cfg      *config.Config
	store    *store.Store
	builder  *builder.Builder
	deployer *deployer.Deployer
	git      *gitmanager.GitManager
	proxy    *proxy.Caddy
	addons   *addon.Manager
	mon      *monitor.Monitor
	backups  *backup.Service
	buildLogs sync.Map
}

type PageData struct {
	Title       string
	Flash       string
	FlashType   string
	App         *models.App
	Apps        []models.App
	Processes   []models.Process
	Domains     []models.Domain
	EnvVars     []models.EnvVar
	Addons      []models.Addon
	Deployments []models.Deployment
	Deployment  *models.Deployment
	GitRemote   string
	ActiveTab   string
	AddonTypes  []string
	DeployID    string
	BuildActive bool
	AppName     string
	Stats        *monitor.Stats
	S3           *models.S3Config
	BackupConfig *models.BackupConfig
	Backups      []models.Backup
	Version      string
}

func NewHandler(cfg *config.Config, s *store.Store, b *builder.Builder, d *deployer.Deployer,
	g *gitmanager.GitManager, p *proxy.Caddy, a *addon.Manager, m *monitor.Monitor, bk *backup.Service) *Handler {
	return &Handler{
		cfg:      cfg,
		store:    s,
		builder:  b,
		deployer: d,
		git:      g,
		proxy:    p,
		addons:   a,
		mon:      m,
		backups:  bk,
	}
}

func (h *Handler) funcMap() template.FuncMap {
	return template.FuncMap{
		"timeago": func(t time.Time) string {
			d := time.Since(t)
			switch {
			case d < time.Minute:
				return "just now"
			case d < time.Hour:
				m := int(d.Minutes())
				if m == 1 {
					return "1 minute ago"
				}
				return fmt.Sprintf("%d minutes ago", m)
			case d < 24*time.Hour:
				h := int(d.Hours())
				if h == 1 {
					return "1 hour ago"
				}
				return fmt.Sprintf("%d hours ago", h)
			default:
				days := int(d.Hours() / 24)
				if days == 1 {
					return "1 day ago"
				}
				return fmt.Sprintf("%d days ago", days)
			}
		},
		"upper":    strings.ToUpper,
		"truncate": func(s string, n int) string { if len(s) > n { return s[:n] }; return s },
		"statuscolor": func(s string) string {
			switch s {
			case "running":
				return "green"
			case "building":
				return "yellow"
			case "stopped", "new":
				return "gray"
			case "failed":
				return "red"
			default:
				return "gray"
			}
		},
		"printf": fmt.Sprintf,
		"addonicon": func(s string) string {
			switch s {
			case "postgres":
				return "🐘"
			case "mysql":
				return "🐬"
			case "redis":
				return "⚡"
			case "mongodb":
				return "🍃"
			default:
				return "📦"
			}
		},
	}
}

func (h *Handler) render(w http.ResponseWriter, page string, data PageData) {
	data.Version = h.cfg.Version
	tmpl, err := template.New("").Funcs(h.funcMap()).ParseFS(templateFS, "templates/base.html", "templates/"+page)
	if err != nil {
		log.Printf("template error: %v", err)
		http.Error(w, "template error", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		log.Printf("render error: %v", err)
	}
}

func (h *Handler) renderPartial(w http.ResponseWriter, name string, data PageData) {
	tmpl, err := template.New("").Funcs(h.funcMap()).ParseFS(templateFS, "templates/partials/"+name)
	if err != nil {
		log.Printf("partial template error: %v", err)
		http.Error(w, "template error", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("partial render error: %v", err)
	}
}

func (h *Handler) getApp(w http.ResponseWriter, r *http.Request) *models.App {
	name := r.PathValue("name")
	app, err := h.store.GetAppByName(name)
	if err != nil {
		http.Error(w, "App not found", 404)
		return nil
	}
	return app
}

func (h *Handler) gitRemote(r *http.Request, appName string) string {
	host := r.Host
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}
	return fmt.Sprintf("%s:%s", host, h.git.RepoPath(appName))
}

// --- Dashboard ---

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	apps, err := h.store.ListApps()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	h.render(w, "dashboard.html", PageData{Title: "Dashboard", Apps: apps})
}

// --- Monitor ---

func (h *Handler) showMonitor(w http.ResponseWriter, r *http.Request) {
	stats := h.mon.GetStats()
	h.render(w, "monitor.html", PageData{Title: "Monitor", Stats: &stats})
}

func (h *Handler) monitorStats(w http.ResponseWriter, r *http.Request) {
	stats := h.mon.GetStats()
	h.renderPartial(w, "monitor_stats.html", PageData{Stats: &stats})
}

// --- System ---

func (h *Handler) showSystem(w http.ResponseWriter, r *http.Request) {
	s3 := h.store.GetS3Config()
	repo, _ := h.store.GetSetting("github_repo")
	h.render(w, "system.html", PageData{Title: "System", GitRemote: h.cfg.DataDir, S3: &s3, Flash: repo, Version: h.cfg.Version})
}

func (h *Handler) saveRepo(w http.ResponseWriter, r *http.Request) {
	repo := strings.TrimSpace(r.FormValue("repo"))
	h.store.SetSetting("github_repo", repo)
	w.WriteHeader(204)
}

func (h *Handler) checkUpdate(w http.ResponseWriter, r *http.Request) {
	repo := "brhynders/stackfly"
	current := h.cfg.Version

	cmd := exec.CommandContext(r.Context(), "curl", "-sf",
		fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo))
	out, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(w, `<span class="text-xs text-gray-400">Could not check for updates</span>`)
		return
	}

	// Extract tag_name from JSON
	var latest string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, `"tag_name"`) {
			parts := strings.Split(line, `"`)
			if len(parts) >= 4 {
				latest = parts[3]
			}
			break
		}
	}

	if latest == "" {
		fmt.Fprintf(w, `<span class="text-xs text-gray-400">%s</span>`, current)
		return
	}

	if latest == current {
		fmt.Fprintf(w, `<span class="text-xs text-green-600 font-medium">%s &check;</span>`, current)
	} else {
		fmt.Fprintf(w, `<span class="inline-flex items-center gap-2 text-xs">
			<span class="text-gray-400">%s</span>
			<span class="text-yellow-600 font-medium">&rarr; %s</span>
			<button hx-post="/system/update" hx-target="#update-result" hx-swap="innerHTML"
				hx-confirm="Update to %s? StackFly will restart."
				class="px-2 py-0.5 bg-indigo-600 text-white text-xs font-medium rounded hover:bg-indigo-700">
				Update
			</button>
		</span>`, current, latest, latest)
	}
}

func (h *Handler) saveS3Config(w http.ResponseWriter, r *http.Request) {
	cfg := models.S3Config{
		Endpoint:  strings.TrimSpace(r.FormValue("endpoint")),
		Bucket:    strings.TrimSpace(r.FormValue("bucket")),
		AccessKey: strings.TrimSpace(r.FormValue("access_key")),
		SecretKey: strings.TrimSpace(r.FormValue("secret_key")),
		Region:    strings.TrimSpace(r.FormValue("region")),
	}
	h.store.SetS3Config(cfg)
	fmt.Fprintf(w, `<div class="mt-3 p-3 rounded-md bg-green-50 text-green-700 text-sm">S3 configuration saved.</div>`)
}

func (h *Handler) testS3Config(w http.ResponseWriter, r *http.Request) {
	cfg := h.store.GetS3Config()
	if cfg.Endpoint == "" || cfg.Bucket == "" {
		fmt.Fprintf(w, `<div class="mt-3 p-3 rounded-md bg-red-50 text-red-700 text-sm">Save S3 config first.</div>`)
		return
	}

	useSSL := !strings.Contains(cfg.Endpoint, "localhost") && !strings.Contains(cfg.Endpoint, "127.0.0.1")
	endpoint := strings.TrimPrefix(strings.TrimPrefix(cfg.Endpoint, "https://"), "http://")

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: useSSL,
		Region: cfg.Region,
	})
	if err != nil {
		fmt.Fprintf(w, `<div class="mt-3 p-3 rounded-md bg-red-50 text-red-700 text-sm">Client error: %s</div>`, err)
		return
	}

	exists, err := client.BucketExists(r.Context(), cfg.Bucket)
	if err != nil {
		fmt.Fprintf(w, `<div class="mt-3 p-3 rounded-md bg-red-50 text-red-700 text-sm">Connection failed: %s</div>`, err)
		return
	}
	if !exists {
		fmt.Fprintf(w, `<div class="mt-3 p-3 rounded-md bg-yellow-50 text-yellow-700 text-sm">Connected, but bucket %q not found.</div>`, cfg.Bucket)
		return
	}
	fmt.Fprintf(w, `<div class="mt-3 p-3 rounded-md bg-green-50 text-green-700 text-sm">Connected successfully to bucket %q.</div>`, cfg.Bucket)
}

func (h *Handler) systemUpdate(w http.ResponseWriter, r *http.Request) {
	repo := "brhynders/stackfly"

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	flusher, _ := w.(http.Flusher)
	logLine := func(msg string) {
		fmt.Fprintf(w, `<div class="text-xs text-gray-600 font-mono">%s</div>`, msg)
		if flusher != nil {
			flusher.Flush()
		}
	}

	// 1. Download latest binary
	logLine("Downloading latest binary from " + repo + "...")
	exePath, _ := os.Executable()
	dlCmd := fmt.Sprintf(
		`curl -sf "https://api.github.com/repos/%s/releases/latest" | grep -o 'https://[^"]*stackfly-linux-amd64' | head -1 | xargs curl -fsSL -o %s.new`,
		repo, exePath)
	if out, err := exec.CommandContext(r.Context(), "sh", "-c", dlCmd).CombinedOutput(); err != nil {
		logLine(fmt.Sprintf("Binary download failed: %s", strings.TrimSpace(string(out))))
		return
	}
	if err := os.Chmod(exePath+".new", 0755); err != nil {
		logLine("chmod failed: " + err.Error())
		return
	}
	if err := os.Rename(exePath+".new", exePath); err != nil {
		logLine("replace failed: " + err.Error())
		return
	}
	logLine("Binary updated.")

	// 2. Update Caddy
	logLine("Pulling latest Caddy image...")
	if err := h.proxy.Update(r.Context()); err != nil {
		logLine("Caddy update failed: " + err.Error())
	} else {
		logLine("Caddy updated.")
	}

	// 3. Update nixpacks
	logLine("Updating nixpacks...")
	if out, err := exec.CommandContext(r.Context(), "sh", "-c", "curl -sSL https://nixpacks.com/install.sh | bash -s -- -y").CombinedOutput(); err != nil {
		logLine("Nixpacks update failed: " + strings.TrimSpace(string(out)))
	} else {
		logLine("Nixpacks updated.")
	}

	logLine("")
	logLine("All components updated. Restarting in 2 seconds...")

	// 4. Exit — systemd restarts with the new binary
	go func() {
		time.Sleep(2 * time.Second)
		os.Exit(0)
	}()
}

func (h *Handler) dockerPrune(w http.ResponseWriter, r *http.Request) {
	out, err := exec.CommandContext(r.Context(), "docker", "system", "prune", "-f").CombinedOutput()
	if err != nil {
		fmt.Fprintf(w, `<div class="mt-3 p-3 rounded-md bg-red-50 text-red-700 text-xs font-mono whitespace-pre-wrap">%s</div>`, string(out))
		return
	}
	fmt.Fprintf(w, `<div class="mt-3 p-3 rounded-md bg-green-50 text-green-700 text-xs font-mono whitespace-pre-wrap">%s</div>`, string(out))
}

func (h *Handler) dockerNuke(w http.ResponseWriter, r *http.Request) {
	out, err := exec.CommandContext(r.Context(), "docker", "system", "prune", "-af", "--volumes").CombinedOutput()
	if err != nil {
		fmt.Fprintf(w, `<div class="mt-3 p-3 rounded-md bg-red-50 text-red-700 text-xs font-mono whitespace-pre-wrap">%s</div>`, string(out))
		return
	}
	fmt.Fprintf(w, `<div class="mt-3 p-3 rounded-md bg-green-50 text-green-700 text-xs font-mono whitespace-pre-wrap">%s</div>`, string(out))
}

// --- App CRUD ---

func (h *Handler) showNewApp(w http.ResponseWriter, r *http.Request) {
	h.render(w, "app_new.html", PageData{Title: "New App"})
}

func (h *Handler) createApp(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		h.render(w, "app_new.html", PageData{Title: "New App", Flash: "Name is required", FlashType: "error"})
		return
	}
	name = strings.ToLower(strings.ReplaceAll(name, " ", "-"))

	app, err := h.store.CreateApp(name)
	if err != nil {
		h.render(w, "app_new.html", PageData{Title: "New App", Flash: "App already exists or invalid name", FlashType: "error"})
		return
	}

	if err := h.git.InitRepo(app.Name); err != nil {
		log.Printf("git init failed: %v", err)
	}
	if err := h.git.InstallHook(app.Name); err != nil {
		log.Printf("hook install failed: %v", err)
	}

	http.Redirect(w, r, "/apps/"+app.Name, http.StatusSeeOther)
}

func (h *Handler) showApp(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}

	procs, _ := h.store.GetProcesses(app.ID)
	domains, _ := h.store.GetDomains(app.ID)
	envVars, _ := h.store.GetEnvVars(app.ID)
	addons, _ := h.store.GetAddons(app.ID)
	deploys, _ := h.store.ListDeployments(app.ID)

	var latestDeploy *models.Deployment
	if len(deploys) > 0 {
		latestDeploy = &deploys[0]
	}

	buildActive := latestDeploy != nil && latestDeploy.Status == "building"

	tab := r.URL.Query().Get("tab")
	if tab == "" {
		tab = "overview"
	}

	h.render(w, "app.html", PageData{
		Title:       app.Name,
		App:         app,
		Processes:   procs,
		Domains:     domains,
		EnvVars:     envVars,
		Addons:      addons,
		Deployments: deploys,
		Deployment:  latestDeploy,
		GitRemote:   h.gitRemote(r, app.Name),
		ActiveTab:   tab,
		AddonTypes:  []string{"postgres", "mysql", "redis", "mongodb"},
		BuildActive: buildActive,
		AppName:     app.Name,
	})
}

func (h *Handler) deleteApp(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}

	h.deployer.Teardown(context.Background(), app)
	h.git.DeleteRepo(app.Name)
	h.store.DeleteApp(app.ID)

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// --- Tab Content ---

func (h *Handler) showOverview(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}
	deploy, _ := h.store.GetLatestDeployment(app.ID)
	buildActive := deploy != nil && deploy.Status == "building"

	h.renderPartial(w, "overview.html", PageData{
		App:         app,
		Deployment:  deploy,
		GitRemote:   h.gitRemote(r, app.Name),
		BuildActive: buildActive,
		AppName:     app.Name,
	})
}

func (h *Handler) showProcesses(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}
	procs, _ := h.store.GetProcesses(app.ID)
	h.renderPartial(w, "processes.html", PageData{App: app, Processes: procs, AppName: app.Name})
}

func (h *Handler) showDomains(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}
	domains, _ := h.store.GetDomains(app.ID)
	h.renderPartial(w, "domains.html", PageData{App: app, Domains: domains, AppName: app.Name})
}

func (h *Handler) showEnvVars(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}
	envVars, _ := h.store.GetEnvVars(app.ID)
	h.renderPartial(w, "envvars.html", PageData{App: app, EnvVars: envVars, AppName: app.Name})
}

func (h *Handler) showAddons(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}
	addons, _ := h.store.GetAddons(app.ID)
	h.renderPartial(w, "addons.html", PageData{
		App: app, Addons: addons, AppName: app.Name,
		AddonTypes: []string{"postgres", "mysql", "redis", "mongodb"},
	})
}

func (h *Handler) showDeployments(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}
	deploys, _ := h.store.ListDeployments(app.ID)
	var latest *models.Deployment
	buildActive := false
	if len(deploys) > 0 {
		latest = &deploys[0]
		buildActive = latest.Status == "building"
	}
	h.renderPartial(w, "deployments.html", PageData{
		App: app, Deployments: deploys, Deployment: latest,
		BuildActive: buildActive, AppName: app.Name,
	})
}

// --- Actions ---

func (h *Handler) triggerDeploy(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}

	gitURL := strings.TrimSpace(r.FormValue("git_url"))
	branch := strings.TrimSpace(r.FormValue("branch"))
	if branch == "" {
		branch = "main"
	}

	deploy, err := h.store.CreateDeployment(app.ID, "")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	h.store.UpdateAppStatus(app.ID, "building")

	bl := newBuildLog()
	h.buildLogs.Store(deploy.ID, bl)

	go func() {
		defer func() {
			fullLog := strings.Join(bl.Lines(), "\n")
			bl.Close()
			h.buildLogs.Delete(deploy.ID)

			d, _ := h.store.GetDeployment(deploy.ID)
			if d != nil && d.Status == "building" {
				h.store.UpdateDeployment(deploy.ID, "failed", "", fullLog)
				h.store.UpdateAppStatus(app.ID, "failed")
			} else if d != nil {
				h.store.UpdateDeployment(d.ID, d.Status, d.ImageTag, fullLog)
			}
		}()

		var buildErr error
		if gitURL != "" {
			buildErr = h.builder.BuildFromURL(context.Background(), app, deploy, gitURL, branch, bl.Write)
		} else {
			buildErr = h.builder.Build(context.Background(), app, deploy, bl.Write)
		}

		if buildErr != nil {
			bl.Write(fmt.Sprintf("ERROR: %v", buildErr))
			h.store.UpdateDeployment(deploy.ID, "failed", "", "")
			h.store.UpdateAppStatus(app.ID, "failed")
			return
		}

		if err := h.deployer.Deploy(context.Background(), app, bl.Write); err != nil {
			bl.Write(fmt.Sprintf("ERROR: deploy failed: %v", err))
			h.store.UpdateAppStatus(app.ID, "failed")
		}
	}()

	if r.Header.Get("HX-Request") == "true" {
		deploys, _ := h.store.ListDeployments(app.ID)
		h.renderPartial(w, "deployments.html", PageData{
			App: app, Deployments: deploys, Deployment: deploy,
			BuildActive: true, AppName: app.Name,
		})
		return
	}
	http.Redirect(w, r, "/apps/"+app.Name+"?tab=deployments", http.StatusSeeOther)
}

func (h *Handler) stopApp(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}
	if err := h.deployer.Stop(context.Background(), app); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		app.Status = "stopped"
		h.renderPartial(w, "overview.html", PageData{App: app, GitRemote: h.gitRemote(r, app.Name), AppName: app.Name})
		return
	}
	http.Redirect(w, r, "/apps/"+app.Name, http.StatusSeeOther)
}

func (h *Handler) restartApp(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}
	if err := h.deployer.Restart(context.Background(), app); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		app.Status = "running"
		h.renderPartial(w, "overview.html", PageData{App: app, GitRemote: h.gitRemote(r, app.Name), AppName: app.Name})
		return
	}
	http.Redirect(w, r, "/apps/"+app.Name, http.StatusSeeOther)
}

func (h *Handler) rollbackDeploy(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}
	targetID := r.PathValue("id")
	target, err := h.store.GetDeployment(targetID)
	if err != nil || target.ImageTag == "" {
		http.Error(w, "deployment not found or has no image", 400)
		return
	}

	rollback, err := h.store.CreateDeployment(app.ID, target.CommitSHA)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	shortID := targetID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	h.store.UpdateDeployment(rollback.ID, "deployed", target.ImageTag,
		fmt.Sprintf("Rolled back to deployment %s", shortID))

	h.store.UpdateAppStatus(app.ID, "running")

	go h.deployer.Deploy(context.Background(), app, func(s string) { log.Println(s) })

	deploys, _ := h.store.ListDeployments(app.ID)
	var latest *models.Deployment
	if len(deploys) > 0 {
		latest = &deploys[0]
	}
	h.renderPartial(w, "deployments.html", PageData{
		App: app, Deployments: deploys, Deployment: latest,
		AppName: app.Name,
	})
}

func (h *Handler) scaleProcess(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}
	procType := r.FormValue("type")
	replicas := 1
	fmt.Sscanf(r.FormValue("replicas"), "%d", &replicas)
	if replicas < 0 {
		replicas = 0
	}

	if err := h.store.ScaleProcess(app.ID, procType, replicas); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	go func() {
		h.deployer.Redeploy(context.Background(), app, func(s string) { log.Println(s) })
	}()

	procs, _ := h.store.GetProcesses(app.ID)
	h.renderPartial(w, "processes.html", PageData{App: app, Processes: procs, AppName: app.Name})
}

func (h *Handler) addDomain(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}
	fqdn := strings.TrimSpace(r.FormValue("fqdn"))
	if fqdn == "" {
		http.Error(w, "domain required", 400)
		return
	}
	if err := h.store.AddDomain(app.ID, fqdn); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	go h.proxy.Regenerate(context.Background())

	domains, _ := h.store.GetDomains(app.ID)
	h.renderPartial(w, "domains.html", PageData{App: app, Domains: domains, AppName: app.Name})
}

func (h *Handler) toggleDomainSSL(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}
	id := r.PathValue("id")
	enable := r.FormValue("ssl") == "on"
	h.store.ToggleDomainSSL(id, enable)

	go h.proxy.Regenerate(context.Background())

	domains, _ := h.store.GetDomains(app.ID)
	h.renderPartial(w, "domains.html", PageData{App: app, Domains: domains, AppName: app.Name})
}

func (h *Handler) removeDomain(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}
	id := r.PathValue("id")
	h.store.RemoveDomain(id)

	go h.proxy.Regenerate(context.Background())

	domains, _ := h.store.GetDomains(app.ID)
	h.renderPartial(w, "domains.html", PageData{App: app, Domains: domains, AppName: app.Name})
}

func (h *Handler) setEnvVar(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}
	key := strings.TrimSpace(r.FormValue("key"))
	value := r.FormValue("value")
	if key == "" {
		http.Error(w, "key required", 400)
		return
	}
	h.store.SetEnvVar(app.ID, key, value)

	envVars, _ := h.store.GetEnvVars(app.ID)
	h.renderPartial(w, "envvars.html", PageData{App: app, EnvVars: envVars, AppName: app.Name})
}

func (h *Handler) deleteEnvVar(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}
	key := r.PathValue("key")
	h.store.DeleteEnvVar(app.ID, key)

	envVars, _ := h.store.GetEnvVars(app.ID)
	h.renderPartial(w, "envvars.html", PageData{App: app, EnvVars: envVars, AppName: app.Name})
}

func (h *Handler) provisionAddon(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}
	addonType := r.FormValue("type")
	if _, err := h.addons.Provision(app.ID, app.Name, addonType); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	if app.Status == "running" {
		go h.deployer.Redeploy(context.Background(), app, func(s string) { log.Println(s) })
	}

	addons, _ := h.store.GetAddons(app.ID)
	h.renderPartial(w, "addons.html", PageData{
		App: app, Addons: addons, AppName: app.Name,
		AddonTypes: []string{"postgres", "mysql", "redis", "mongodb"},
	})
}

func (h *Handler) destroyAddon(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}
	id := r.PathValue("id")

	a, _ := h.store.GetAddon(id)
	if a != nil {
		h.deployer.RemoveAddon(context.Background(), app, a)
	}
	h.addons.Destroy(id)

	addons, _ := h.store.GetAddons(app.ID)
	h.renderPartial(w, "addons.html", PageData{
		App: app, Addons: addons, AppName: app.Name,
		AddonTypes: []string{"postgres", "mysql", "redis", "mongodb"},
	})
}

// --- Addon Backups ---

func (h *Handler) saveBackupConfig(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}
	addonID := r.PathValue("id")
	enabled := r.FormValue("enabled") == "on"
	schedule := r.FormValue("schedule")
	retention := 7
	fmt.Sscanf(r.FormValue("retention"), "%d", &retention)
	if retention < 1 {
		retention = 1
	}

	h.store.UpsertBackupConfig(addonID, enabled, schedule, retention)

	addons, _ := h.store.GetAddons(app.ID)
	h.renderPartial(w, "addons.html", PageData{
		App: app, Addons: addons, AppName: app.Name,
		AddonTypes: []string{"postgres", "mysql", "redis", "mongodb"},
	})
}

func (h *Handler) triggerBackup(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}
	addonID := r.PathValue("id")
	a, err := h.store.GetAddon(addonID)
	if err != nil {
		http.Error(w, "addon not found", 404)
		return
	}

	go func() {
		if err := h.backups.RunBackup(context.Background(), a); err != nil {
			log.Printf("manual backup failed: %v", err)
		}
	}()

	addons, _ := h.store.GetAddons(app.ID)
	h.renderPartial(w, "addons.html", PageData{
		App: app, Addons: addons, AppName: app.Name,
		AddonTypes: []string{"postgres", "mysql", "redis", "mongodb"},
	})
}

// --- Console / Run Command ---

func (h *Handler) showConsole(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}
	h.renderPartial(w, "console.html", PageData{App: app, AppName: app.Name})
}

func (h *Handler) runCommand(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}
	command := strings.TrimSpace(r.FormValue("command"))
	if command == "" {
		http.Error(w, "command required", 400)
		return
	}

	deploy, err := h.store.GetLatestDeployment(app.ID)
	if err != nil || deploy.ImageTag == "" {
		http.Error(w, "no deployed image — deploy the app first", 400)
		return
	}

	runID := fmt.Sprintf("%x", time.Now().UnixNano())
	bl := newBuildLog()
	h.buildLogs.Store("run-"+runID, bl)

	envVars, _ := h.store.GetEnvVars(app.ID)
	addons, _ := h.store.GetAddons(app.ID)

	env := map[string]string{"PORT": "5000"}
	for _, e := range envVars {
		env[e.Key] = e.Value
	}
	for _, a := range addons {
		for k, v := range addon.GetEnvVars(&a) {
			env[k] = v
		}
	}

	go func() {
		defer func() {
			bl.Close()
			go func() {
				time.Sleep(60 * time.Second)
				h.buildLogs.Delete("run-" + runID)
			}()
		}()

		args := []string{"run", "--rm",
			"--network", "sf-" + app.Name,
		}
		for k, v := range env {
			args = append(args, "-e", k+"="+v)
		}
		args = append(args, deploy.ImageTag, command)

		bl.Write(fmt.Sprintf("$ %s", command))
		cmd := exec.CommandContext(context.Background(), "docker", args...)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			bl.Write(fmt.Sprintf("Error: %v", err))
			return
		}
		cmd.Stderr = cmd.Stdout

		if err := cmd.Start(); err != nil {
			bl.Write(fmt.Sprintf("Error: %v", err))
			return
		}

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
		for scanner.Scan() {
			bl.Write(scanner.Text())
		}

		if err := cmd.Wait(); err != nil {
			bl.Write(fmt.Sprintf("Exit: %v", err))
		} else {
			bl.Write("Exit: 0")
		}
	}()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<pre id="run-output" class="log bg-gray-900 text-gray-100 rounded-md p-4 overflow-x-auto max-h-96 overflow-y-auto text-xs leading-relaxed mt-3"></pre>
<script>
var src = new EventSource('/apps/%s/console/run/%s/stream');
var el = document.getElementById('run-output');
src.onmessage = function(e) { el.textContent += e.data + '\n'; el.scrollTop = el.scrollHeight; };
src.addEventListener('done', function() { src.close(); });
src.onerror = function() { src.close(); };
</script>`, app.Name, runID)
}

func (h *Handler) streamRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", 500)
		return
	}

	val, ok := h.buildLogs.Load("run-" + runID)
	if !ok {
		fmt.Fprintf(w, "data: Run not found\n\nevent: done\ndata: end\n\n")
		flusher.Flush()
		return
	}

	bl := val.(*buildLog)
	history, ch := bl.Subscribe()

	for _, line := range history {
		fmt.Fprintf(w, "data: %s\n\n", line)
	}
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case line, ok := <-ch:
			if !ok {
				fmt.Fprintf(w, "event: done\ndata: end\n\n")
				flusher.Flush()
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}

// --- Runtime Logs ---

func (h *Handler) showLogs(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}
	procs, _ := h.store.GetProcesses(app.ID)
	h.renderPartial(w, "logs.html", PageData{App: app, Processes: procs, AppName: app.Name})
}

func (h *Handler) streamLogs(w http.ResponseWriter, r *http.Request) {
	app := h.getApp(w, r)
	if app == nil {
		return
	}

	process := r.URL.Query().Get("process")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", 500)
		return
	}

	// Find containers for this app
	args := []string{"ps", "-a", "--format", "{{.Names}}",
		"--filter", "label=stackfly.app=" + app.Name}
	if process != "" {
		args = append(args, "--filter", "label=stackfly.process="+process)
	}
	out, _ := exec.CommandContext(r.Context(), "docker", args...).Output()
	containers := strings.Fields(string(out))
	if len(containers) == 0 {
		fmt.Fprintf(w, "data: No running containers found\n\n")
		flusher.Flush()
		return
	}

	// Start docker logs -f for each container, merge into one stream
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	lines := make(chan string, 256)

	for _, name := range containers {
		name := name
		go func() {
			cmd := exec.CommandContext(ctx, "docker", "logs", "-f", "--tail", "100", "--timestamps", name)
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				return
			}
			cmd.Stderr = cmd.Stdout
			if err := cmd.Start(); err != nil {
				return
			}

			short := strings.TrimPrefix(name, "sf-"+app.Name+"-")

			scanner := bufio.NewScanner(stdout)
			scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
			for scanner.Scan() {
				select {
				case lines <- fmt.Sprintf("[%s] %s", short, scanner.Text()):
				case <-ctx.Done():
					return
				}
			}
			cmd.Wait()
		}()
	}

	for {
		select {
		case line := <-lines:
			fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}

// --- SSE Build Log ---

func (h *Handler) streamBuildLog(w http.ResponseWriter, r *http.Request) {
	deployID := r.PathValue("id")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", 500)
		return
	}

	val, ok := h.buildLogs.Load(deployID)
	if !ok {
		d, err := h.store.GetDeployment(deployID)
		if err != nil {
			fmt.Fprintf(w, "event: error\ndata: deployment not found\n\n")
			flusher.Flush()
			return
		}
		for _, line := range strings.Split(d.Log, "\n") {
			fmt.Fprintf(w, "data: %s\n\n", line)
		}
		fmt.Fprintf(w, "event: done\ndata: %s\n\n", d.Status)
		flusher.Flush()
		return
	}

	bl := val.(*buildLog)
	history, ch := bl.Subscribe()

	for _, line := range history {
		fmt.Fprintf(w, "data: %s\n\n", line)
	}
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case line, ok := <-ch:
			if !ok {
				fmt.Fprintf(w, "event: done\ndata: complete\n\n")
				flusher.Flush()
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}

// --- API ---

func (h *Handler) apiDeploy(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	app, err := h.store.GetAppByName(name)
	if err != nil {
		http.Error(w, "app not found", 404)
		return
	}

	deploy, err := h.store.CreateDeployment(app.ID, "")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	h.store.UpdateAppStatus(app.ID, "building")
	bl := newBuildLog()
	h.buildLogs.Store(deploy.ID, bl)

	go func() {
		defer func() {
			fullLog := strings.Join(bl.Lines(), "\n")
			bl.Close()
			h.buildLogs.Delete(deploy.ID)

			d, _ := h.store.GetDeployment(deploy.ID)
			if d != nil {
				h.store.UpdateDeployment(d.ID, d.Status, d.ImageTag, fullLog)
			}
		}()

		if err := h.builder.Build(context.Background(), app, deploy, bl.Write); err != nil {
			bl.Write(fmt.Sprintf("ERROR: %v", err))
			h.store.UpdateDeployment(deploy.ID, "failed", "", "")
			h.store.UpdateAppStatus(app.ID, "failed")
			return
		}

		if err := h.deployer.Deploy(context.Background(), app, bl.Write); err != nil {
			bl.Write(fmt.Sprintf("ERROR: %v", err))
			h.store.UpdateAppStatus(app.ID, "failed")
		}
	}()

	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"deployment_id": %q}`, deploy.ID)
}

// --- Build Log Broadcaster ---

type buildLog struct {
	mu        sync.RWMutex
	lines     []string
	done      bool
	listeners []chan string
}

func newBuildLog() *buildLog {
	return &buildLog{}
}

func (bl *buildLog) Write(line string) {
	bl.mu.Lock()
	defer bl.mu.Unlock()
	bl.lines = append(bl.lines, line)
	for i := len(bl.listeners) - 1; i >= 0; i-- {
		select {
		case bl.listeners[i] <- line:
		default:
		}
	}
}

func (bl *buildLog) Lines() []string {
	bl.mu.RLock()
	defer bl.mu.RUnlock()
	out := make([]string, len(bl.lines))
	copy(out, bl.lines)
	return out
}

func (bl *buildLog) Subscribe() ([]string, chan string) {
	bl.mu.Lock()
	defer bl.mu.Unlock()
	ch := make(chan string, 256)
	history := make([]string, len(bl.lines))
	copy(history, bl.lines)
	if bl.done {
		close(ch)
	} else {
		bl.listeners = append(bl.listeners, ch)
	}
	return history, ch
}

func (bl *buildLog) Close() {
	bl.mu.Lock()
	defer bl.mu.Unlock()
	bl.done = true
	for _, ch := range bl.listeners {
		close(ch)
	}
	bl.listeners = nil
}
