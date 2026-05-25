package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"stackfly/internal/models"
	"stackfly/internal/store"
)

var scheduleIntervals = map[string]time.Duration{
	"6h":     6 * time.Hour,
	"12h":    12 * time.Hour,
	"daily":  24 * time.Hour,
	"weekly": 7 * 24 * time.Hour,
}

type Service struct {
	store *store.Store
}

func New(s *store.Store) *Service {
	return &Service{store: s}
}

func (svc *Service) StartScheduler(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			svc.checkAndRun(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (svc *Service) checkAndRun(ctx context.Context) {
	configs, err := svc.store.GetDueBackups()
	if err != nil {
		return
	}

	for _, bc := range configs {
		interval, ok := scheduleIntervals[bc.Schedule]
		if !ok {
			interval = 24 * time.Hour
		}

		if bc.LastBackupAt != nil && time.Since(*bc.LastBackupAt) < interval {
			continue
		}

		addon, err := svc.store.GetAddon(bc.AddonID)
		if err != nil {
			continue
		}

		go func(a *models.Addon, cfg models.BackupConfig) {
			if err := svc.RunBackup(ctx, a); err != nil {
				log.Printf("backup failed for %s: %v", a.Name, err)
			}
		}(addon, bc)
	}
}

func (svc *Service) s3Client(cfg models.S3Config) (*minio.Client, error) {
	useSSL := !strings.Contains(cfg.Endpoint, "localhost") && !strings.Contains(cfg.Endpoint, "127.0.0.1")
	endpoint := strings.TrimPrefix(strings.TrimPrefix(cfg.Endpoint, "https://"), "http://")
	return minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: useSSL,
		Region: cfg.Region,
	})
}

func (svc *Service) RunBackup(ctx context.Context, addon *models.Addon) error {
	s3cfg := svc.store.GetS3Config()
	if s3cfg.Endpoint == "" || s3cfg.Bucket == "" {
		return fmt.Errorf("S3 not configured")
	}

	timestamp := time.Now().UTC().Format("20060102-150405")
	s3Key := fmt.Sprintf("backups/%s/%s-%s.sql.gz", addon.Name, addon.Name, timestamp)

	backup, err := svc.store.CreateBackup(addon.ID, s3Key)
	if err != nil {
		return err
	}

	dumpCmd, err := buildDumpCommand(addon)
	if err != nil {
		svc.store.UpdateBackup(backup.ID, "failed", 0)
		return err
	}

	log.Printf("Starting backup for %s -> s3://%s/%s", addon.Name, s3cfg.Bucket, s3Key)

	client, err := svc.s3Client(s3cfg)
	if err != nil {
		svc.store.UpdateBackup(backup.ID, "failed", 0)
		return fmt.Errorf("s3 client error: %w", err)
	}

	// Stream: dump | gzip | pipe → S3 upload
	// Constant memory regardless of database size.
	cmd := exec.CommandContext(ctx, "sh", "-c", dumpCmd+" | gzip")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		svc.store.UpdateBackup(backup.ID, "failed", 0)
		return err
	}

	pr, pw := io.Pipe()
	var uploadSize int64

	// Count bytes as they pass through
	go func() {
		n, _ := io.Copy(pw, stdout)
		uploadSize = n
		pw.Close()
	}()

	if err := cmd.Start(); err != nil {
		svc.store.UpdateBackup(backup.ID, "failed", 0)
		return fmt.Errorf("dump failed to start: %w", err)
	}

	_, err = client.PutObject(ctx, s3cfg.Bucket, s3Key, pr, -1,
		minio.PutObjectOptions{ContentType: "application/gzip"})
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		svc.store.UpdateBackup(backup.ID, "failed", 0)
		return fmt.Errorf("s3 upload failed: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		svc.store.UpdateBackup(backup.ID, "failed", uploadSize)
		return fmt.Errorf("dump failed: %w", err)
	}

	svc.store.UpdateBackup(backup.ID, "completed", uploadSize)
	svc.store.UpdateLastBackup(addon.ID)

	// Enforce retention — delete old backups from S3 and DB
	bc, _ := svc.store.GetBackupConfig(addon.ID)
	if bc != nil && bc.Retention > 0 {
		oldKeys, _ := svc.store.DeleteOldBackups(addon.ID, bc.Retention)
		for _, key := range oldKeys {
			client.RemoveObject(ctx, s3cfg.Bucket, key, minio.RemoveObjectOptions{})
		}
	}

	log.Printf("Backup completed for %s: %d bytes -> %s", addon.Name, uploadSize, s3Key)
	return nil
}

func buildDumpCommand(addon *models.Addon) (string, error) {
	var cfg struct {
		Password string `json:"password"`
		User     string `json:"user"`
		Database string `json:"database"`
	}
	json.Unmarshal([]byte(addon.Config), &cfg)

	switch addon.Type {
	case "postgres":
		return fmt.Sprintf(`docker exec -e PGPASSWORD=%s %s pg_dump -U %s %s`,
			cfg.Password, addon.Name, cfg.User, cfg.Database), nil
	case "mysql":
		return fmt.Sprintf(`docker exec %s mysqldump -u %s -p%s %s`,
			addon.Name, cfg.User, cfg.Password, cfg.Database), nil
	case "mongodb":
		return fmt.Sprintf(`docker exec %s mongodump --uri="mongodb://%s:%s@localhost:27017/%s" --archive`,
			addon.Name, cfg.User, cfg.Password, cfg.Database), nil
	default:
		return "", fmt.Errorf("backup not supported for addon type: %s", addon.Type)
	}
}
