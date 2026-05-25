package deployer

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"

	"stackfly/internal/addon"
	"stackfly/internal/config"
	"stackfly/internal/models"
	"stackfly/internal/proxy"
	"stackfly/internal/store"
)

type Deployer struct {
	cfg   *config.Config
	store *store.Store
	proxy *proxy.Caddy
}

func New(cfg *config.Config, store *store.Store, proxy *proxy.Caddy) *Deployer {
	return &Deployer{cfg: cfg, store: store, proxy: proxy}
}

func (d *Deployer) ReconnectNetworks(ctx context.Context) error {
	ensureNetwork(ctx, d.cfg.DockerNetwork)

	apps, err := d.store.ListApps()
	if err != nil {
		return err
	}
	for _, app := range apps {
		if app.Status != "running" {
			continue
		}
		webContainers := listContainers(ctx, app.Name, "web")
		for _, c := range webContainers {
			exec.CommandContext(ctx, "docker", "network", "connect",
				"--alias", app.Name+"-web",
				d.cfg.DockerNetwork, c).Run()
		}
	}
	return nil
}

func (d *Deployer) Deploy(ctx context.Context, app *models.App, logFn func(string)) error {
	logFn("-----> Deploying application...")

	deploy, err := d.store.GetLatestDeployment(app.ID)
	if err != nil {
		return fmt.Errorf("no deployment found: %w", err)
	}
	if deploy.ImageTag == "" {
		return fmt.Errorf("no built image for deployment %s", deploy.ID)
	}

	procs, err := d.store.GetProcesses(app.ID)
	if err != nil {
		return fmt.Errorf("getting processes: %w", err)
	}
	if len(procs) == 0 {
		procs = []models.Process{{AppID: app.ID, Type: "web", Command: "", Replicas: 1}}
	}

	envVars, _ := d.store.GetEnvVars(app.ID)
	addons, _ := d.store.GetAddons(app.ID)

	env := map[string]string{"PORT": "5000"}
	for _, e := range envVars {
		env[e.Key] = e.Value
	}
	for _, a := range addons {
		for k, v := range addon.GetEnvVars(&a) {
			env[k] = v
		}
	}

	// Run release command before swapping containers
	var releaseCmd string
	var runProcs []models.Process
	for _, p := range procs {
		if p.Type == "release" {
			releaseCmd = p.Command
		} else {
			runProcs = append(runProcs, p)
		}
	}
	procs = runProcs

	appNet := "sf-" + app.Name
	ensureNetwork(ctx, appNet)
	ensureNetwork(ctx, d.cfg.DockerNetwork)

	if releaseCmd != "" {
		logFn(fmt.Sprintf("-----> Running release command: %s", releaseCmd))
		if err := d.runOneOff(ctx, app.Name, deploy.ImageTag, env, appNet, releaseCmd, logFn); err != nil {
			return fmt.Errorf("release command failed: %w", err)
		}
		logFn("-----> Release command succeeded")
	}

	logFn("-----> Removing old containers...")
	d.removeAppContainers(ctx, app.Name)

	for _, proc := range procs {
		for i := 1; i <= proc.Replicas; i++ {
			name := containerName(app.Name, proc.Type, i)
			logFn(fmt.Sprintf("-----> Starting %s...", name))

			args := []string{"run", "-d",
				"--name", name,
				"--label", "stackfly.app=" + app.Name,
				"--label", "stackfly.process=" + proc.Type,
				"--network", appNet,
				"--restart", "unless-stopped",
			}
			for k, v := range env {
				args = append(args, "-e", k+"="+v)
			}

			args = append(args, deploy.ImageTag)
			if proc.Command != "" {
				args = append(args, proc.Command)
			}

			cmd := exec.CommandContext(ctx, "docker", args...)
			if out, err := cmd.CombinedOutput(); err != nil {
				logFn(fmt.Sprintf("       Error: %s", strings.TrimSpace(string(out))))
				return fmt.Errorf("failed to start %s: %w", name, err)
			}

			if proc.Type == "web" {
				exec.CommandContext(ctx, "docker", "network", "connect",
					"--alias", app.Name+"-web",
					d.cfg.DockerNetwork, name).Run()
			}
		}
	}

	for _, a := range addons {
		name := containerName(app.Name, a.Type, 0)
		if isRunning(ctx, name) {
			continue
		}
		logFn(fmt.Sprintf("-----> Starting addon %s...", a.Type))
		d.runAddon(ctx, app.Name, &a, appNet)
	}

	if err := d.store.UpdateAppStatus(app.ID, "running"); err != nil {
		log.Printf("warning: failed to update app status: %v", err)
	}
	if err := d.store.UpdateDeployment(deploy.ID, "deployed", deploy.ImageTag, ""); err != nil {
		log.Printf("warning: failed to update deployment status: %v", err)
	}

	logFn("-----> Updating reverse proxy...")
	if err := d.proxy.Regenerate(ctx); err != nil {
		logFn(fmt.Sprintf("       Warning: proxy update failed: %v", err))
	}

	logFn("-----> Deploy complete!")
	return nil
}

func (d *Deployer) Stop(ctx context.Context, app *models.App) error {
	containers := listContainers(ctx, app.Name, "")
	for _, c := range containers {
		exec.CommandContext(ctx, "docker", "stop", "-t", "10", c).Run()
	}
	d.store.UpdateAppStatus(app.ID, "stopped")
	return d.proxy.Regenerate(ctx)
}

func (d *Deployer) Restart(ctx context.Context, app *models.App) error {
	containers := listContainers(ctx, app.Name, "")
	for _, c := range containers {
		exec.CommandContext(ctx, "docker", "restart", c).Run()
	}
	d.store.UpdateAppStatus(app.ID, "running")
	return nil
}

func (d *Deployer) Redeploy(ctx context.Context, app *models.App, logFn func(string)) error {
	return d.Deploy(ctx, app, logFn)
}

func (d *Deployer) Teardown(ctx context.Context, app *models.App) error {
	// Remove ALL containers for this app (processes + addons)
	all := listContainers(ctx, app.Name, "")
	for _, c := range all {
		exec.CommandContext(ctx, "docker", "rm", "-f", c).Run()
	}
	exec.CommandContext(ctx, "docker", "network", "rm", "sf-"+app.Name).Run()

	// Remove all Docker images for this app
	out, _ := exec.CommandContext(ctx, "docker", "images", "--format", "{{.Repository}}:{{.Tag}}",
		"--filter", "reference=stackfly-"+app.Name).Output()
	for _, img := range strings.Fields(string(out)) {
		exec.CommandContext(ctx, "docker", "rmi", img).Run()
	}

	// Remove addon volumes
	addons, _ := d.store.GetAddons(app.ID)
	for _, a := range addons {
		volName := containerName(app.Name, a.Type, 0) + "-data"
		exec.CommandContext(ctx, "docker", "volume", "rm", "-f", volName).Run()
	}

	d.store.UpdateAppStatus(app.ID, "stopped")
	return d.proxy.Regenerate(ctx)
}

func (d *Deployer) RemoveAddon(ctx context.Context, app *models.App, a *models.Addon) {
	name := containerName(app.Name, a.Type, 0)
	exec.CommandContext(ctx, "docker", "rm", "-f", name).Run()
	exec.CommandContext(ctx, "docker", "volume", "rm", "-f", name+"-data").Run()
}

func (d *Deployer) runOneOff(ctx context.Context, appName, imageTag string, env map[string]string, network, command string, logFn func(string)) error {
	args := []string{"run", "--rm", "--network", network}
	for k, v := range env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, imageTag, command)

	cmd := exec.CommandContext(ctx, "docker", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return err
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
	for scanner.Scan() {
		logFn("       " + scanner.Text())
	}

	return cmd.Wait()
}

func (d *Deployer) removeAppContainers(ctx context.Context, appName string) {
	// Only remove process containers — addons persist across deploys
	args := []string{"ps", "-aq",
		"--filter", "label=stackfly.app=" + appName,
		"--filter", "label=stackfly.process",
	}
	out, err := exec.CommandContext(ctx, "docker", args...).Output()
	if err != nil {
		return
	}
	for _, id := range strings.Fields(string(out)) {
		exec.CommandContext(ctx, "docker", "rm", "-f", id).Run()
	}
}

func (d *Deployer) runAddon(ctx context.Context, appName string, a *models.Addon, network string) {
	svc := addon.GetComposeService(a)
	name := containerName(appName, a.Type, 0)

	image, _ := svc["image"].(string)
	args := []string{"run", "-d",
		"--name", name,
		"--label", "stackfly.app=" + appName,
		"--label", "stackfly.addon=" + a.Type,
		"--network", network,
		"--restart", "unless-stopped",
	}

	if envMap, ok := svc["environment"].(map[string]string); ok {
		for k, v := range envMap {
			args = append(args, "-e", k+"="+v)
		}
	}
	if vols, ok := svc["volumes"].([]string); ok {
		for _, v := range vols {
			args = append(args, "-v", v)
		}
	}

	args = append(args, image)
	exec.CommandContext(ctx, "docker", args...).Run()
}

func containerName(app, process string, n int) string {
	if n == 0 {
		return fmt.Sprintf("sf-%s-%s", app, process)
	}
	return fmt.Sprintf("sf-%s-%s-%d", app, process, n)
}

func listContainers(ctx context.Context, appName, processFilter string) []string {
	args := []string{"ps", "-aq", "--filter", "label=stackfly.app=" + appName}
	if processFilter != "" {
		args = append(args, "--filter", "label=stackfly.process="+processFilter)
	}
	out, err := exec.CommandContext(ctx, "docker", args...).Output()
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}

func isRunning(ctx context.Context, name string) bool {
	out, err := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Running}}", name).Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func ensureNetwork(ctx context.Context, name string) {
	if exec.CommandContext(ctx, "docker", "network", "inspect", name).Run() == nil {
		return
	}
	exec.CommandContext(ctx, "docker", "network", "create", name).Run()
}
