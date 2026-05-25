package models

import "time"

type App struct {
	ID        string
	Name      string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Deployment struct {
	ID        string
	AppID     string
	ImageTag  string
	CommitSHA string
	Status    string
	Log       string
	CreatedAt time.Time
}

type Process struct {
	ID       string
	AppID    string
	Type     string
	Command  string
	Replicas int
}

type EnvVar struct {
	ID    string
	AppID string
	Key   string
	Value string
}

type Domain struct {
	ID    string
	AppID string
	FQDN  string
	SSL   bool
}

type Addon struct {
	ID        string
	AppID     string
	Type      string
	Name      string
	Config    string
	Status    string
	CreatedAt time.Time
}

type BackupConfig struct {
	ID           string
	AddonID      string
	Enabled      bool
	Schedule     string
	Retention    int
	LastBackupAt *time.Time
}

type Backup struct {
	ID        string
	AddonID   string
	S3Key     string
	SizeBytes int64
	Status    string
	CreatedAt time.Time
}

type S3Config struct {
	Endpoint  string
	Bucket    string
	AccessKey string
	SecretKey string
	Region    string
}
