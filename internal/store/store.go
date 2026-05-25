package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"stackfly/internal/models"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migration failed: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS apps (
			id TEXT PRIMARY KEY,
			name TEXT UNIQUE NOT NULL,
			status TEXT NOT NULL DEFAULT 'new',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS deployments (
			id TEXT PRIMARY KEY,
			app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
			image_tag TEXT DEFAULT '',
			commit_sha TEXT DEFAULT '',
			status TEXT NOT NULL DEFAULT 'building',
			log TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS processes (
			id TEXT PRIMARY KEY,
			app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
			type TEXT NOT NULL,
			command TEXT NOT NULL,
			replicas INTEGER NOT NULL DEFAULT 1,
			UNIQUE(app_id, type)
		);

		CREATE TABLE IF NOT EXISTS env_vars (
			id TEXT PRIMARY KEY,
			app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			UNIQUE(app_id, key)
		);

		CREATE TABLE IF NOT EXISTS domains (
			id TEXT PRIMARY KEY,
			app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
			fqdn TEXT UNIQUE NOT NULL,
			ssl INTEGER NOT NULL DEFAULT 0
		);

		CREATE TABLE IF NOT EXISTS addons (
			id TEXT PRIMARY KEY,
			app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
			type TEXT NOT NULL,
			name TEXT NOT NULL,
			config TEXT DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'provisioned',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
	`)
	if err != nil {
		return err
	}

	s.db.Exec("ALTER TABLE domains ADD COLUMN ssl INTEGER NOT NULL DEFAULT 0")

	s.db.Exec(`CREATE TABLE IF NOT EXISTS backup_configs (
		id TEXT PRIMARY KEY,
		addon_id TEXT UNIQUE NOT NULL REFERENCES addons(id) ON DELETE CASCADE,
		enabled INTEGER NOT NULL DEFAULT 0,
		schedule TEXT NOT NULL DEFAULT 'daily',
		retention INTEGER NOT NULL DEFAULT 7,
		last_backup_at DATETIME
	)`)

	s.db.Exec(`CREATE TABLE IF NOT EXISTS backups (
		id TEXT PRIMARY KEY,
		addon_id TEXT NOT NULL REFERENCES addons(id) ON DELETE CASCADE,
		s3_key TEXT NOT NULL,
		size_bytes INTEGER DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'running',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)

	return nil
}

// --- Apps ---

func (s *Store) CreateApp(name string) (*models.App, error) {
	id := generateID()
	now := time.Now()
	_, err := s.db.Exec(
		"INSERT INTO apps (id, name, status, created_at, updated_at) VALUES (?, ?, 'new', ?, ?)",
		id, name, now, now,
	)
	if err != nil {
		return nil, err
	}
	return &models.App{ID: id, Name: name, Status: "new", CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) GetApp(id string) (*models.App, error) {
	a := &models.App{}
	err := s.db.QueryRow(
		"SELECT id, name, status, created_at, updated_at FROM apps WHERE id = ?", id,
	).Scan(&a.ID, &a.Name, &a.Status, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Store) GetAppByName(name string) (*models.App, error) {
	a := &models.App{}
	err := s.db.QueryRow(
		"SELECT id, name, status, created_at, updated_at FROM apps WHERE name = ?", name,
	).Scan(&a.ID, &a.Name, &a.Status, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Store) ListApps() ([]models.App, error) {
	rows, err := s.db.Query("SELECT id, name, status, created_at, updated_at FROM apps ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var apps []models.App
	for rows.Next() {
		var a models.App
		if err := rows.Scan(&a.ID, &a.Name, &a.Status, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		apps = append(apps, a)
	}
	return apps, nil
}

func (s *Store) UpdateAppStatus(id, status string) error {
	_, err := s.db.Exec("UPDATE apps SET status = ?, updated_at = ? WHERE id = ?", status, time.Now(), id)
	return err
}

func (s *Store) DeleteApp(id string) error {
	_, err := s.db.Exec("DELETE FROM apps WHERE id = ?", id)
	return err
}

// --- Deployments ---

func (s *Store) CreateDeployment(appID, commitSHA string) (*models.Deployment, error) {
	id := generateID()
	now := time.Now()
	_, err := s.db.Exec(
		"INSERT INTO deployments (id, app_id, commit_sha, status, created_at) VALUES (?, ?, ?, 'building', ?)",
		id, appID, commitSHA, now,
	)
	if err != nil {
		return nil, err
	}
	return &models.Deployment{ID: id, AppID: appID, CommitSHA: commitSHA, Status: "building", CreatedAt: now}, nil
}

func (s *Store) GetDeployment(id string) (*models.Deployment, error) {
	d := &models.Deployment{}
	err := s.db.QueryRow(
		"SELECT id, app_id, image_tag, commit_sha, status, log, created_at FROM deployments WHERE id = ?", id,
	).Scan(&d.ID, &d.AppID, &d.ImageTag, &d.CommitSHA, &d.Status, &d.Log, &d.CreatedAt)
	if err != nil {
		return nil, err
	}
	return d, nil
}

func (s *Store) ListDeployments(appID string) ([]models.Deployment, error) {
	rows, err := s.db.Query(
		"SELECT id, app_id, image_tag, commit_sha, status, log, created_at FROM deployments WHERE app_id = ? ORDER BY created_at DESC LIMIT 20",
		appID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var deps []models.Deployment
	for rows.Next() {
		var d models.Deployment
		if err := rows.Scan(&d.ID, &d.AppID, &d.ImageTag, &d.CommitSHA, &d.Status, &d.Log, &d.CreatedAt); err != nil {
			return nil, err
		}
		deps = append(deps, d)
	}
	return deps, nil
}

func (s *Store) UpdateDeployment(id, status, imageTag, log string) error {
	_, err := s.db.Exec(
		"UPDATE deployments SET status = ?, image_tag = ?, log = ? WHERE id = ?",
		status, imageTag, log, id,
	)
	return err
}

func (s *Store) GetLatestDeployment(appID string) (*models.Deployment, error) {
	d := &models.Deployment{}
	err := s.db.QueryRow(
		"SELECT id, app_id, image_tag, commit_sha, status, log, created_at FROM deployments WHERE app_id = ? ORDER BY created_at DESC LIMIT 1",
		appID,
	).Scan(&d.ID, &d.AppID, &d.ImageTag, &d.CommitSHA, &d.Status, &d.Log, &d.CreatedAt)
	if err != nil {
		return nil, err
	}
	return d, nil
}

// --- Processes ---

func (s *Store) SetProcesses(appID string, procs []models.Process) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM processes WHERE app_id = ?", appID); err != nil {
		return err
	}

	for _, p := range procs {
		id := generateID()
		replicas := p.Replicas
		if replicas < 1 {
			replicas = 1
		}
		if _, err := tx.Exec(
			"INSERT INTO processes (id, app_id, type, command, replicas) VALUES (?, ?, ?, ?, ?)",
			id, appID, p.Type, p.Command, replicas,
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) UpsertProcesses(appID string, procs []models.Process) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, p := range procs {
		var existingID string
		err := tx.QueryRow("SELECT id FROM processes WHERE app_id = ? AND type = ?", appID, p.Type).Scan(&existingID)
		if err == nil {
			if _, err := tx.Exec("UPDATE processes SET command = ? WHERE id = ?", p.Command, existingID); err != nil {
				return err
			}
		} else {
			id := generateID()
			replicas := p.Replicas
			if replicas < 1 {
				replicas = 1
			}
			if _, err := tx.Exec(
				"INSERT INTO processes (id, app_id, type, command, replicas) VALUES (?, ?, ?, ?, ?)",
				id, appID, p.Type, p.Command, replicas,
			); err != nil {
				return err
			}
		}
	}

	// Remove processes no longer in the Procfile
	typeList := ""
	args := []any{appID}
	for i, p := range procs {
		if i > 0 {
			typeList += ","
		}
		typeList += "?"
		args = append(args, p.Type)
	}
	if len(procs) > 0 {
		if _, err := tx.Exec("DELETE FROM processes WHERE app_id = ? AND type NOT IN ("+typeList+")", args...); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) GetProcesses(appID string) ([]models.Process, error) {
	rows, err := s.db.Query(
		"SELECT id, app_id, type, command, replicas FROM processes WHERE app_id = ? ORDER BY type",
		appID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var procs []models.Process
	for rows.Next() {
		var p models.Process
		if err := rows.Scan(&p.ID, &p.AppID, &p.Type, &p.Command, &p.Replicas); err != nil {
			return nil, err
		}
		procs = append(procs, p)
	}
	return procs, nil
}

func (s *Store) ScaleProcess(appID, procType string, replicas int) error {
	res, err := s.db.Exec("UPDATE processes SET replicas = ? WHERE app_id = ? AND type = ?", replicas, appID, procType)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("process type %q not found for app", procType)
	}
	return nil
}

// --- EnvVars ---

func (s *Store) SetEnvVar(appID, key, value string) error {
	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO env_vars (id, app_id, key, value) VALUES (?, ?, ?, ?)
		 ON CONFLICT(app_id, key) DO UPDATE SET value = excluded.value`,
		id, appID, key, value,
	)
	return err
}

func (s *Store) DeleteEnvVar(appID, key string) error {
	_, err := s.db.Exec("DELETE FROM env_vars WHERE app_id = ? AND key = ?", appID, key)
	return err
}

func (s *Store) GetEnvVars(appID string) ([]models.EnvVar, error) {
	rows, err := s.db.Query(
		"SELECT id, app_id, key, value FROM env_vars WHERE app_id = ? ORDER BY key",
		appID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var vars []models.EnvVar
	for rows.Next() {
		var v models.EnvVar
		if err := rows.Scan(&v.ID, &v.AppID, &v.Key, &v.Value); err != nil {
			return nil, err
		}
		vars = append(vars, v)
	}
	return vars, nil
}

// --- Domains ---

func (s *Store) AddDomain(appID, fqdn string) error {
	id := generateID()
	_, err := s.db.Exec("INSERT INTO domains (id, app_id, fqdn) VALUES (?, ?, ?)", id, appID, fqdn)
	return err
}

func (s *Store) RemoveDomain(id string) error {
	_, err := s.db.Exec("DELETE FROM domains WHERE id = ?", id)
	return err
}

func (s *Store) GetDomains(appID string) ([]models.Domain, error) {
	rows, err := s.db.Query("SELECT id, app_id, fqdn, ssl FROM domains WHERE app_id = ? ORDER BY fqdn", appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var doms []models.Domain
	for rows.Next() {
		var d models.Domain
		if err := rows.Scan(&d.ID, &d.AppID, &d.FQDN, &d.SSL); err != nil {
			return nil, err
		}
		doms = append(doms, d)
	}
	return doms, nil
}

func (s *Store) ToggleDomainSSL(id string, ssl bool) error {
	_, err := s.db.Exec("UPDATE domains SET ssl = ? WHERE id = ?", ssl, id)
	return err
}

func (s *Store) GetAllDomainsGrouped() (map[string][]models.Domain, error) {
	rows, err := s.db.Query(`
		SELECT d.id, d.app_id, d.fqdn, d.ssl, a.name
		FROM domains d JOIN apps a ON a.id = d.app_id
		WHERE a.status = 'running'
		ORDER BY d.fqdn
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string][]models.Domain)
	for rows.Next() {
		var d models.Domain
		var appName string
		if err := rows.Scan(&d.ID, &d.AppID, &d.FQDN, &d.SSL, &appName); err != nil {
			return nil, err
		}
		result[appName] = append(result[appName], d)
	}
	return result, nil
}

// --- Addons ---

func (s *Store) CreateAddon(appID, addonType, name, config string) (*models.Addon, error) {
	id := generateID()
	now := time.Now()
	_, err := s.db.Exec(
		"INSERT INTO addons (id, app_id, type, name, config, status, created_at) VALUES (?, ?, ?, ?, ?, 'provisioned', ?)",
		id, appID, addonType, name, config, now,
	)
	if err != nil {
		return nil, err
	}
	return &models.Addon{ID: id, AppID: appID, Type: addonType, Name: name, Config: config, Status: "provisioned", CreatedAt: now}, nil
}

func (s *Store) GetAddons(appID string) ([]models.Addon, error) {
	rows, err := s.db.Query(
		"SELECT id, app_id, type, name, config, status, created_at FROM addons WHERE app_id = ? ORDER BY created_at",
		appID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var addons []models.Addon
	for rows.Next() {
		var a models.Addon
		if err := rows.Scan(&a.ID, &a.AppID, &a.Type, &a.Name, &a.Config, &a.Status, &a.CreatedAt); err != nil {
			return nil, err
		}
		addons = append(addons, a)
	}
	return addons, nil
}

func (s *Store) DeleteAddon(id string) error {
	_, err := s.db.Exec("DELETE FROM addons WHERE id = ?", id)
	return err
}

func (s *Store) GetAddon(id string) (*models.Addon, error) {
	a := &models.Addon{}
	err := s.db.QueryRow(
		"SELECT id, app_id, type, name, config, status, created_at FROM addons WHERE id = ?", id,
	).Scan(&a.ID, &a.AppID, &a.Type, &a.Name, &a.Config, &a.Status, &a.CreatedAt)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// --- Settings ---

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}

func (s *Store) GetSetting(key string) (string, error) {
	var value string
	err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	return value, err
}

// --- S3 Config ---

func (s *Store) GetS3Config() models.S3Config {
	get := func(k string) string { v, _ := s.GetSetting(k); return v }
	return models.S3Config{
		Endpoint:  get("s3_endpoint"),
		Bucket:    get("s3_bucket"),
		AccessKey: get("s3_access_key"),
		SecretKey: get("s3_secret_key"),
		Region:    get("s3_region"),
	}
}

func (s *Store) SetS3Config(c models.S3Config) error {
	for k, v := range map[string]string{
		"s3_endpoint":   c.Endpoint,
		"s3_bucket":     c.Bucket,
		"s3_access_key": c.AccessKey,
		"s3_secret_key": c.SecretKey,
		"s3_region":     c.Region,
	} {
		if err := s.SetSetting(k, v); err != nil {
			return err
		}
	}
	return nil
}

// --- Backup Configs ---

func (s *Store) GetBackupConfig(addonID string) (*models.BackupConfig, error) {
	bc := &models.BackupConfig{}
	err := s.db.QueryRow(
		"SELECT id, addon_id, enabled, schedule, retention, last_backup_at FROM backup_configs WHERE addon_id = ?", addonID,
	).Scan(&bc.ID, &bc.AddonID, &bc.Enabled, &bc.Schedule, &bc.Retention, &bc.LastBackupAt)
	if err != nil {
		return nil, err
	}
	return bc, nil
}

func (s *Store) UpsertBackupConfig(addonID string, enabled bool, schedule string, retention int) error {
	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO backup_configs (id, addon_id, enabled, schedule, retention) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(addon_id) DO UPDATE SET enabled = excluded.enabled, schedule = excluded.schedule, retention = excluded.retention`,
		id, addonID, enabled, schedule, retention,
	)
	return err
}

func (s *Store) UpdateLastBackup(addonID string) error {
	_, err := s.db.Exec("UPDATE backup_configs SET last_backup_at = CURRENT_TIMESTAMP WHERE addon_id = ?", addonID)
	return err
}

func (s *Store) GetDueBackups() ([]models.BackupConfig, error) {
	rows, err := s.db.Query(`
		SELECT bc.id, bc.addon_id, bc.enabled, bc.schedule, bc.retention, bc.last_backup_at
		FROM backup_configs bc
		JOIN addons a ON a.id = bc.addon_id
		WHERE bc.enabled = 1
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var configs []models.BackupConfig
	for rows.Next() {
		var bc models.BackupConfig
		if err := rows.Scan(&bc.ID, &bc.AddonID, &bc.Enabled, &bc.Schedule, &bc.Retention, &bc.LastBackupAt); err != nil {
			return nil, err
		}
		configs = append(configs, bc)
	}
	return configs, nil
}

// --- Backups ---

func (s *Store) CreateBackup(addonID, s3Key string) (*models.Backup, error) {
	id := generateID()
	now := time.Now()
	_, err := s.db.Exec(
		"INSERT INTO backups (id, addon_id, s3_key, status, created_at) VALUES (?, ?, ?, 'running', ?)",
		id, addonID, s3Key, now,
	)
	if err != nil {
		return nil, err
	}
	return &models.Backup{ID: id, AddonID: addonID, S3Key: s3Key, Status: "running", CreatedAt: now}, nil
}

func (s *Store) UpdateBackup(id, status string, sizeBytes int64) error {
	_, err := s.db.Exec("UPDATE backups SET status = ?, size_bytes = ? WHERE id = ?", status, sizeBytes, id)
	return err
}

func (s *Store) ListBackups(addonID string) ([]models.Backup, error) {
	rows, err := s.db.Query(
		"SELECT id, addon_id, s3_key, size_bytes, status, created_at FROM backups WHERE addon_id = ? ORDER BY created_at DESC LIMIT 50",
		addonID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var backups []models.Backup
	for rows.Next() {
		var b models.Backup
		if err := rows.Scan(&b.ID, &b.AddonID, &b.S3Key, &b.SizeBytes, &b.Status, &b.CreatedAt); err != nil {
			return nil, err
		}
		backups = append(backups, b)
	}
	return backups, nil
}

func (s *Store) DeleteOldBackups(addonID string, keep int) ([]string, error) {
	rows, err := s.db.Query(
		"SELECT id, s3_key FROM backups WHERE addon_id = ? AND status = 'completed' ORDER BY created_at DESC LIMIT -1 OFFSET ?",
		addonID, keep,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	var ids []string
	for rows.Next() {
		var id, key string
		rows.Scan(&id, &key)
		keys = append(keys, key)
		ids = append(ids, id)
	}
	for _, id := range ids {
		s.db.Exec("DELETE FROM backups WHERE id = ?", id)
	}
	return keys, nil
}
