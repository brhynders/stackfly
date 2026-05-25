package gitmanager

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"stackfly/internal/config"
)

type GitManager struct {
	cfg *config.Config
}

func New(cfg *config.Config) *GitManager {
	return &GitManager{cfg: cfg}
}

func (g *GitManager) RepoPath(appName string) string {
	return filepath.Join(g.cfg.ReposDir, appName+".git")
}

func (g *GitManager) InitRepo(appName string) error {
	repoPath := g.RepoPath(appName)
	cmd := exec.Command("git", "init", "--bare", "-b", "main", repoPath)
	if err := cmd.Run(); err != nil {
		return err
	}
	os.Chmod(repoPath, 0777)
	return filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return os.Chmod(path, 0777)
		}
		return os.Chmod(path, 0666)
	})
}

func (g *GitManager) DeleteRepo(appName string) error {
	return os.RemoveAll(g.RepoPath(appName))
}

func (g *GitManager) InstallHook(appName string) error {
	hookPath := filepath.Join(g.RepoPath(appName), "hooks", "post-receive")

	t, err := template.New("hook").Parse(defaultHookTemplate)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(hookPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer f.Close()

	return t.Execute(f, map[string]any{
		"Host":    g.cfg.Host,
		"Port":    g.cfg.Port,
		"AppName": appName,
	})
}

func (g *GitManager) CloneToWorkdir(appName, deployID string) (string, error) {
	repoPath := g.RepoPath(appName)
	workdir := filepath.Join(g.cfg.BuildsDir, appName, deployID)
	if err := os.MkdirAll(workdir, 0755); err != nil {
		return "", err
	}

	branch := g.detectBranch(repoPath)
	args := []string{"clone"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, repoPath, workdir)

	cmd := exec.Command("git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git clone failed: %s: %w", string(out), err)
	}
	return workdir, nil
}

func (g *GitManager) detectBranch(repoPath string) string {
	for _, branch := range []string{"main", "master"} {
		ref := filepath.Join(repoPath, "refs", "heads", branch)
		if _, err := os.Stat(ref); err == nil {
			return branch
		}
	}
	cmd := exec.Command("git", "--git-dir", repoPath, "branch")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "*"))
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func (g *GitManager) CloneFromURL(gitURL, branch, appName, deployID string) (string, error) {
	workdir := filepath.Join(g.cfg.BuildsDir, appName, deployID)
	if err := os.MkdirAll(workdir, 0755); err != nil {
		return "", err
	}
	args := []string{"clone", "--depth", "1"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, gitURL, workdir)
	cmd := exec.Command("git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git clone failed: %s: %w", string(out), err)
	}
	return workdir, nil
}

func (g *GitManager) GetHeadSHA(appName string) string {
	repoPath := g.RepoPath(appName)
	cmd := exec.Command("git", "--git-dir", repoPath, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (g *GitManager) GetWorkdirSHA(workdir string) string {
	cmd := exec.Command("git", "-C", workdir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func findProjectRoot() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

const defaultHookTemplate = `#!/bin/bash
while read oldrev newrev refname; do
  branch=$(git rev-parse --symbolic --abbrev-ref "$refname")
  if [ "$branch" = "main" ] || [ "$branch" = "master" ]; then
    echo "-----> StackFly: Deploying $branch..."
    curl -sf -X POST "http://{{.Host}}:{{.Port}}/api/apps/{{.AppName}}/deploy" \
      || echo "Deploy trigger failed"
  fi
done
`
