package handler

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/mediasync"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/internal/service/task"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

type MediaMountDeps struct {
	SettingsStore *store.SettingsStore
	AccountStore  *store.AccountStore
	TasksStore    *store.TasksStore
}

func HandleMediaMountSyncGET(deps MediaMountDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			logger.S().Warnf("[MediaMountSyncGET] ReadSettings failed: %v, using defaults", err)
			settings = &model.Settings{}
		}
		accounts := deps.AccountStore.List()
		var tasks []task.Task
		if deps.TasksStore != nil {
			read, terr := deps.TasksStore.ReadTasks()
			if terr != nil {
				logger.S().Warnf("[MediaMountSyncGET] ReadTasks failed: %v, returning empty list", terr)
			} else {
				tasks = read
			}
		}

		input := mediasync.ComputeInput{
			Settings: settings,
			Accounts: make([]mediasync.AccountName, 0, len(accounts)),
			Tasks:    make([]mediasync.TaskLike, 0, len(tasks)),
		}
		for _, a := range accounts {
			input.Accounts = append(input.Accounts, mediasync.AccountName{Name: a.Name})
		}
		for _, t := range tasks {
			input.Tasks = append(input.Tasks, mediasync.TaskLike{
				ID: t.ID, Account: t.Account,
				StrmPrefix: t.StrmPrefix, Enable302: t.Enable302,
				EnablePathEncoding: t.EnablePathEncoding,
			})
		}

		cr := mediasync.ComputeMediaMountEntries(input)
		added, removed, kept, changed := mediasync.ComputeDiff(settings, cr)
		resp := mediasync.NewDryRunResponse(settings, cr, added, removed, kept, changed)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			log.Printf("[mediaMount] encode response failed: %v", err)
		}
	}
}

func HandleMediaMountSyncPOST(deps MediaMountDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SkipNginxReload bool `json:"skipNginxReload"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}

		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			logger.S().Warnf("[MediaMountSyncPOST] ReadSettings failed: %v, using defaults", err)
			settings = &model.Settings{}
		}
		accounts := deps.AccountStore.List()
		var tasks []task.Task
		if deps.TasksStore != nil {
			read, terr := deps.TasksStore.ReadTasks()
			if terr != nil {
				logger.S().Warnf("[MediaMountSyncPOST] ReadTasks failed: %v, returning empty list", terr)
			} else {
				tasks = read
			}
		}

		input := mediasync.ComputeInput{
			Settings: settings,
			Accounts: make([]mediasync.AccountName, 0, len(accounts)),
			Tasks:    make([]mediasync.TaskLike, 0, len(tasks)),
		}
		for _, a := range accounts {
			input.Accounts = append(input.Accounts, mediasync.AccountName{Name: a.Name})
		}
		for _, t := range tasks {
			input.Tasks = append(input.Tasks, mediasync.TaskLike{
				ID: t.ID, Account: t.Account,
				StrmPrefix: t.StrmPrefix, Enable302: t.Enable302,
				EnablePathEncoding: t.EnablePathEncoding,
			})
		}

		cr := mediasync.ComputeMediaMountEntries(input)
		added, removed, kept, changed := mediasync.ComputeDiff(settings, cr)

		if changed {
			settings.MediaMountPath = cr.FinalPaths
			if err := deps.SettingsStore.SaveSettings(settings); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		var nginxResult mediasync.NginxResult
		if body.SkipNginxReload {
			nginxResult = mediasync.NginxResult{
				Attempted: false, Available: false, OK: true,
				Message: "skipped (skipNginxReload=true)",
			}
		} else {
			nginxResult = mediasync.ReloadNginxIfAvailable()
		}

		resp := mediasync.NewSyncResult(cr, added, removed, kept, changed, nginxResult)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			log.Printf("[mediaMount] encode response failed: %v", err)
		}
	}
}
