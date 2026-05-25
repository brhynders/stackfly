package addon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"stackfly/internal/models"
	"stackfly/internal/store"
)

type Manager struct {
	store *store.Store
}

func New(store *store.Store) *Manager {
	return &Manager{store: store}
}

type AddonSpec struct {
	Image   string
	EnvKey  string
	Port    int
	HasAuth bool
}

var Specs = map[string]AddonSpec{
	"postgres": {Image: "postgres:16", EnvKey: "DATABASE_URL", Port: 5432, HasAuth: true},
	"mysql":    {Image: "mysql:8", EnvKey: "DATABASE_URL", Port: 3306, HasAuth: true},
	"redis":    {Image: "redis:7", EnvKey: "REDIS_URL", Port: 6379, HasAuth: false},
	"mongodb":  {Image: "mongo:7", EnvKey: "MONGODB_URL", Port: 27017, HasAuth: true},
}

type AddonConfig struct {
	Image    string `json:"image"`
	Password string `json:"password,omitempty"`
	User     string `json:"user,omitempty"`
	Database string `json:"database,omitempty"`
	Port     int    `json:"port"`
}

func (m *Manager) Provision(appID, appName, addonType string) (*models.Addon, error) {
	spec, ok := Specs[addonType]
	if !ok {
		return nil, fmt.Errorf("unknown addon type: %s", addonType)
	}

	svcName := fmt.Sprintf("%s-%s", appName, addonType)

	cfg := AddonConfig{
		Image:    spec.Image,
		Port:     spec.Port,
		User:     "stackfly",
		Database: "app",
	}
	if spec.HasAuth {
		cfg.Password = randomPassword()
	}

	cfgJSON, _ := json.Marshal(cfg)
	return m.store.CreateAddon(appID, addonType, svcName, string(cfgJSON))
}

func (m *Manager) Destroy(addonID string) error {
	return m.store.DeleteAddon(addonID)
}

func GetEnvVars(addon *models.Addon) map[string]string {
	spec, ok := Specs[addon.Type]
	if !ok {
		return nil
	}

	var cfg AddonConfig
	json.Unmarshal([]byte(addon.Config), &cfg)

	envs := make(map[string]string)
	switch addon.Type {
	case "postgres":
		envs[spec.EnvKey] = fmt.Sprintf("postgresql://%s:%s@%s:%d/%s", cfg.User, cfg.Password, addon.Name, cfg.Port, cfg.Database)
		envs["PGUSER"] = cfg.User
		envs["PGPASSWORD"] = cfg.Password
		envs["PGDATABASE"] = cfg.Database
		envs["PGHOST"] = addon.Name
	case "mysql":
		envs[spec.EnvKey] = fmt.Sprintf("mysql://%s:%s@%s:%d/%s", cfg.User, cfg.Password, addon.Name, cfg.Port, cfg.Database)
	case "redis":
		envs[spec.EnvKey] = fmt.Sprintf("redis://%s:%d", addon.Name, cfg.Port)
	case "mongodb":
		envs[spec.EnvKey] = fmt.Sprintf("mongodb://%s:%s@%s:%d/%s", cfg.User, cfg.Password, addon.Name, cfg.Port, cfg.Database)
	}
	return envs
}

func GetComposeService(addon *models.Addon) map[string]any {
	var cfg AddonConfig
	json.Unmarshal([]byte(addon.Config), &cfg)

	svc := map[string]any{
		"image":   cfg.Image,
		"restart": "unless-stopped",
		"volumes": []string{
			fmt.Sprintf("%s-data:/data", addon.Name),
		},
	}

	env := map[string]string{}
	switch addon.Type {
	case "postgres":
		env["POSTGRES_USER"] = cfg.User
		env["POSTGRES_PASSWORD"] = cfg.Password
		env["POSTGRES_DB"] = cfg.Database
		env["PGDATA"] = "/data/pgdata"
		svc["volumes"] = []string{fmt.Sprintf("%s-data:/data", addon.Name)}
	case "mysql":
		env["MYSQL_ROOT_PASSWORD"] = cfg.Password
		env["MYSQL_USER"] = cfg.User
		env["MYSQL_PASSWORD"] = cfg.Password
		env["MYSQL_DATABASE"] = cfg.Database
		svc["volumes"] = []string{fmt.Sprintf("%s-data:/var/lib/mysql", addon.Name)}
	case "redis":
		svc["volumes"] = []string{fmt.Sprintf("%s-data:/data", addon.Name)}
	case "mongodb":
		env["MONGO_INITDB_ROOT_USERNAME"] = cfg.User
		env["MONGO_INITDB_ROOT_PASSWORD"] = cfg.Password
		env["MONGO_INITDB_DATABASE"] = cfg.Database
		svc["volumes"] = []string{fmt.Sprintf("%s-data:/data/db", addon.Name)}
	}
	if len(env) > 0 {
		svc["environment"] = env
	}

	return svc
}

func randomPassword() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
