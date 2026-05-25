package builder

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"

	"stackfly/internal/config"
	"stackfly/internal/gitmanager"
	"stackfly/internal/models"
	"stackfly/internal/store"
)

type Builder struct {
	cfg   *config.Config
	store *store.Store
	git   *gitmanager.GitManager
}

func New(cfg *config.Config, store *store.Store, git *gitmanager.GitManager) *Builder {
	return &Builder{cfg: cfg, store: store, git: git}
}

func (b *Builder) Build(ctx context.Context, app *models.App, deploy *models.Deployment, logFn func(string)) error {
	logFn("-----> Cloning source from local repo...")
	workdir, err := b.git.CloneToWorkdir(app.Name, deploy.ID)
	if err != nil {
		return fmt.Errorf("clone failed: %w", err)
	}
	defer os.RemoveAll(workdir)

	sha := b.git.GetWorkdirSHA(workdir)
	if sha != "" {
		deploy.CommitSHA = sha
	}

	return b.buildFromDir(ctx, app, deploy, workdir, logFn)
}

func (b *Builder) BuildFromURL(ctx context.Context, app *models.App, deploy *models.Deployment, gitURL, branch string, logFn func(string)) error {
	logFn(fmt.Sprintf("-----> Cloning from %s...", gitURL))
	workdir, err := b.git.CloneFromURL(gitURL, branch, app.Name, deploy.ID)
	if err != nil {
		return fmt.Errorf("clone failed: %w", err)
	}
	defer os.RemoveAll(workdir)

	sha := b.git.GetWorkdirSHA(workdir)
	if sha != "" {
		deploy.CommitSHA = sha
	}

	return b.buildFromDir(ctx, app, deploy, workdir, logFn)
}

func (b *Builder) buildFromDir(ctx context.Context, app *models.App, deploy *models.Deployment, workdir string, logFn func(string)) error {
	logFn("-----> Parsing Procfile...")
	procMap, err := ParseProcfile(workdir)
	if err != nil {
		return fmt.Errorf("procfile parse failed: %w", err)
	}

	var procs []models.Process
	for ptype, cmd := range procMap {
		procs = append(procs, models.Process{
			AppID:    app.ID,
			Type:     ptype,
			Command:  cmd,
			Replicas: 1,
		})
		logFn(fmt.Sprintf("       Found process: %s -> %s", ptype, cmd))
	}

	if err := b.store.UpsertProcesses(app.ID, procs); err != nil {
		return fmt.Errorf("saving processes failed: %w", err)
	}

	imageTag := fmt.Sprintf("stackfly-%s:%s", app.Name, deploy.ID)
	logFn(fmt.Sprintf("-----> Building image %s with nixpacks...", imageTag))

	cmd := exec.CommandContext(ctx, "nixpacks", "build", workdir, "--name", imageTag)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("nixpacks build failed to start: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		logFn(scanner.Text())
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("nixpacks build failed: %w", err)
	}

	deploy.ImageTag = imageTag
	if err := b.store.UpdateDeployment(deploy.ID, "succeeded", imageTag, ""); err != nil {
		return fmt.Errorf("updating deployment failed: %w", err)
	}

	logFn(fmt.Sprintf("-----> Build succeeded: %s", imageTag))
	return nil
}
