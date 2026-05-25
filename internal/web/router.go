package web

import (
	"io/fs"
	"net/http"
)

func (h *Handler) SetupRoutes() http.Handler {
	mux := http.NewServeMux()

	staticSub, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticSub)))
	mux.HandleFunc("GET /", h.dashboard)
	mux.HandleFunc("GET /monitor", h.showMonitor)
	mux.HandleFunc("GET /monitor/stats", h.monitorStats)
	mux.HandleFunc("GET /system", h.showSystem)
	mux.HandleFunc("POST /system/docker-prune", h.dockerPrune)
	mux.HandleFunc("POST /system/docker-nuke", h.dockerNuke)
	mux.HandleFunc("POST /system/s3", h.saveS3Config)
	mux.HandleFunc("POST /system/s3/test", h.testS3Config)
	mux.HandleFunc("POST /system/update", h.systemUpdate)
	mux.HandleFunc("POST /system/save-repo", h.saveRepo)
	mux.HandleFunc("GET /system/check-update", h.checkUpdate)
	mux.HandleFunc("GET /apps/new", h.showNewApp)
	mux.HandleFunc("POST /apps", h.createApp)
	mux.HandleFunc("GET /apps/{name}", h.showApp)
	mux.HandleFunc("DELETE /apps/{name}", h.deleteApp)

	mux.HandleFunc("POST /apps/{name}/deploy", h.triggerDeploy)
	mux.HandleFunc("POST /apps/{name}/stop", h.stopApp)
	mux.HandleFunc("POST /apps/{name}/restart", h.restartApp)

	mux.HandleFunc("GET /apps/{name}/overview", h.showOverview)
	mux.HandleFunc("GET /apps/{name}/processes", h.showProcesses)
	mux.HandleFunc("POST /apps/{name}/scale", h.scaleProcess)
	mux.HandleFunc("GET /apps/{name}/domains", h.showDomains)
	mux.HandleFunc("POST /apps/{name}/domains", h.addDomain)
	mux.HandleFunc("POST /apps/{name}/domains/{id}/ssl", h.toggleDomainSSL)
	mux.HandleFunc("DELETE /apps/{name}/domains/{id}", h.removeDomain)
	mux.HandleFunc("GET /apps/{name}/env", h.showEnvVars)
	mux.HandleFunc("POST /apps/{name}/env", h.setEnvVar)
	mux.HandleFunc("DELETE /apps/{name}/env/{key}", h.deleteEnvVar)
	mux.HandleFunc("GET /apps/{name}/addons", h.showAddons)
	mux.HandleFunc("POST /apps/{name}/addons", h.provisionAddon)
	mux.HandleFunc("DELETE /apps/{name}/addons/{id}", h.destroyAddon)
	mux.HandleFunc("POST /apps/{name}/addons/{id}/backup-config", h.saveBackupConfig)
	mux.HandleFunc("POST /apps/{name}/addons/{id}/backup-now", h.triggerBackup)
	mux.HandleFunc("GET /apps/{name}/deployments", h.showDeployments)
	mux.HandleFunc("GET /apps/{name}/deployments/{id}/log", h.streamBuildLog)
	mux.HandleFunc("POST /apps/{name}/deployments/{id}/rollback", h.rollbackDeploy)
	mux.HandleFunc("GET /apps/{name}/logs", h.showLogs)
	mux.HandleFunc("GET /apps/{name}/logs/stream", h.streamLogs)
	mux.HandleFunc("GET /apps/{name}/console", h.showConsole)
	mux.HandleFunc("POST /apps/{name}/console/run", h.runCommand)
	mux.HandleFunc("GET /apps/{name}/console/run/{id}/stream", h.streamRun)

	mux.HandleFunc("POST /api/apps/{name}/deploy", h.apiDeploy)

	return mux
}
