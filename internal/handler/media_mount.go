package handler

import (
	"encoding/json"
	"net/http"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/mediasync"
	"github.com/wabisabi926/faststrm/internal/service/store"
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
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		accounts, err := deps.AccountStore.ReadAccounts()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		tasks, err := deps.TasksStore.ReadTasks()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
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
		json.NewEncoder(w).Encode(resp)
	}
}

func HandleMediaMountSyncPOST(deps MediaMountDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SkipNginxReload bool `json:"skipNginxReload"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)

		settings, err := deps.SettingsStore.ReadSettings()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		accounts, err := deps.AccountStore.ReadAccounts()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		tasks, err := deps.TasksStore.ReadTasks()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
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
		json.NewEncoder(w).Encode(resp)
	}
}

// Ensure unused import is used
var _ = model.DefaultSettings
