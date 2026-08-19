package mediasync

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/wabisabi926/faststrm/internal/model"
)

type MediaMountSourceTag string

const (
	SourceGlobal302   MediaMountSourceTag = "global_302"
	SourceTask        MediaMountSourceTag = "task"
	SourceLifeMonitor MediaMountSourceTag = "life_monitor"
)

type MediaMountEntry struct {
	Prefix  string              `json:"prefix"`
	Source  MediaMountSourceTag `json:"source"`
	Account string              `json:"account,omitempty"`
	TaskID  string              `json:"taskId,omitempty"`
}

type MediaMountEntryWithLabel struct {
	MediaMountEntry
	SourceLabel string `json:"sourceLabel"`
}

type AccountName struct {
	Name string
}

type ComputeInput struct {
	Settings *model.Settings
	Accounts []AccountName
	Tasks    []TaskLike
}

type TaskLike struct {
	ID                 string
	Account            string
	StrmPrefix         string
	Enable302          bool
	EnablePathEncoding bool
}

type ComputeResult struct {
	Entries    []MediaMountEntry
	FinalPaths []string
}

type NginxResult struct {
	Attempted bool   `json:"attempted"`
	Available bool   `json:"available"`
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
}

type SyncResult struct {
	Changed           bool                      `json:"changed"`
	Added             []string                  `json:"added"`
	Removed           []string                  `json:"removed"`
	Kept              []string                  `json:"kept"`
	Final             []string                  `json:"final"`
	EntriesWithSource []MediaMountEntryWithLabel `json:"entriesWithSource"`
	Nginx             NginxResult               `json:"nginx"`
	Error             string                    `json:"error,omitempty"`
}

type DryRunResponse struct {
	Persisted []string                   `json:"persisted"`
	Computed  []MediaMountEntryWithLabel `json:"computed"`
	Final     []string                   `json:"final"`
	Diff      DiffResult                 `json:"diff"`
}

type DiffResult struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
	Kept    []string `json:"kept"`
	Changed bool     `json:"changed"`
}

func NormalizePrefix(p string) string {
	return strings.TrimRight(strings.TrimSpace(p), "/")
}

func IsValidHTTPPrefix(p string) bool {
	if p == "" {
		return false
	}
	return strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://")
}

func SourceTagLabel(s MediaMountSourceTag) string {
	switch s {
	case SourceGlobal302:
		return "全局 302"
	case SourceTask:
		return "任务"
	case SourceLifeMonitor:
		return "生活事件"
	default:
		return string(s)
	}
}

type ResolvedStrmSettings struct {
	StrmPrefix         string
	EnablePathEncoding bool
	Enable302          bool
}

func resolveStrmSettings(account string, task *TaskLike, settings *model.Settings) ResolvedStrmSettings {
	g := settings

	strmPrefix := g.StrmPrefix
	enablePathEncoding := g.EnablePathEncoding
	enable302 := g.Enable302

	if task != nil {
		if task.StrmPrefix != "" {
			strmPrefix = task.StrmPrefix
		}
		if task.EnablePathEncoding || task.StrmPrefix != "" {
			enablePathEncoding = task.EnablePathEncoding
		}
	}

	if enable302 {
		trimmed := NormalizePrefix(strmPrefix)
		if !strings.HasSuffix(trimmed, "/api/strm") {
			strmPrefix = trimmed + "/api/strm"
		}
	} else if account != "" {
		trimmed := NormalizePrefix(strmPrefix)
		if !strings.HasSuffix(trimmed, "/"+account) {
			strmPrefix = trimmed + "/" + account
		}
	}

	return ResolvedStrmSettings{
		StrmPrefix:         strmPrefix,
		EnablePathEncoding: enablePathEncoding,
		Enable302:          enable302,
	}
}

func collect(entries *[]MediaMountEntry, resultSet *map[string]bool, prefix string, source MediaMountSourceTag, account string, taskID string) {
	p := NormalizePrefix(prefix)
	if !IsValidHTTPPrefix(p) {
		return
	}
	if (*resultSet)[p] {
		return
	}
	(*resultSet)[p] = true
	e := MediaMountEntry{
		Prefix: p,
		Source: source,
	}
	if account != "" {
		e.Account = account
	}
	if taskID != "" {
		e.TaskID = taskID
	}
	*entries = append(*entries, e)
}

func ComputeMediaMountEntries(input ComputeInput) ComputeResult {
	settings := input.Settings
	if settings == nil {
		settings = &model.Settings{}
	}

	entries := []MediaMountEntry{}
	resultSet := map[string]bool{}

	if settings.Enable302 && settings.StrmPrefix != "" {
		for _, acc := range input.Accounts {
			if acc.Name == "" {
				continue
			}
			resolved := resolveStrmSettings(acc.Name, nil, settings)
			collect(&entries, &resultSet, resolved.StrmPrefix, SourceGlobal302, acc.Name, "")
		}
	}

	for _, task := range input.Tasks {
		if task.StrmPrefix == "" {
			continue
		}
		resolved := resolveStrmSettings(task.Account, &task, settings)
		collect(&entries, &resultSet, resolved.StrmPrefix, SourceTask, task.Account, task.ID)
	}

	if settings.LifeMonitor.Accounts != nil {
		lifeOverride := &model.Settings{
			StrmPrefix:         settings.StrmPrefix,
			EnablePathEncoding: settings.LifeMonitor.EnablePathEncoding,
			Enable302:          settings.Enable302,
		}
		for _, accName := range settings.LifeMonitor.Accounts {
			if accName == "" {
				continue
			}
			resolved := resolveStrmSettings(accName, nil, lifeOverride)
			collect(&entries, &resultSet, resolved.StrmPrefix, SourceLifeMonitor, accName, "")
		}
	}

	finalPaths := make([]string, 0, len(resultSet))
	for p := range resultSet {
		finalPaths = append(finalPaths, p)
	}

	return ComputeResult{
		Entries:    entries,
		FinalPaths: finalPaths,
	}
}

func IsNginxAvailable() bool {
	cmd := "nginx"
	if runtime.GOOS == "windows" {
		cmd = "where"
	}
	var args []string
	if runtime.GOOS == "windows" {
		args = []string{"nginx"}
	} else {
		args = []string{"-v", "nginx"}
	}
	c := exec.Command(cmd, args...)
	err := c.Run()
	return err == nil
}

func ReloadNginxIfAvailable() NginxResult {
	if !IsNginxAvailable() {
		return NginxResult{
			Attempted: false,
			Available: false,
			OK:        true,
			Message:   "nginx not found in PATH, skipped reload",
		}
	}

	c := exec.Command("nginx", "-s", "reload")
	output, err := c.CombinedOutput()
	if err != nil {
		return NginxResult{
			Attempted: true,
			Available: true,
			OK:        false,
			Message:   strings.TrimSpace(string(output)),
		}
	}
	return NginxResult{
		Attempted: true,
		Available: true,
		OK:        true,
		Message:   "nginx reloaded successfully",
	}
}

func ComputeDiff(settings *model.Settings, computeResult ComputeResult) (added, removed, kept []string, changed bool) {
	persisted := make([]string, 0)
	for _, p := range settings.MediaMountPath {
		np := NormalizePrefix(p)
		if IsValidHTTPPrefix(np) {
			persisted = append(persisted, np)
		}
	}

	persistedSet := map[string]bool{}
	for _, p := range persisted {
		persistedSet[p] = true
	}
	finalSet := map[string]bool{}
	for _, p := range computeResult.FinalPaths {
		finalSet[p] = true
	}

	for _, p := range computeResult.FinalPaths {
		if !persistedSet[p] {
			added = append(added, p)
		}
	}
	for _, p := range persisted {
		if !finalSet[p] {
			removed = append(removed, p)
		}
	}
	for _, p := range computeResult.FinalPaths {
		if persistedSet[p] {
			kept = append(kept, p)
		}
	}
	changed = len(added) > 0 || len(removed) > 0
	return
}

func ToEntryWithLabels(entries []MediaMountEntry) []MediaMountEntryWithLabel {
	result := make([]MediaMountEntryWithLabel, 0, len(entries))
	for _, e := range entries {
		result = append(result, MediaMountEntryWithLabel{
			MediaMountEntry: e,
			SourceLabel:     SourceTagLabel(e.Source),
		})
	}
	return result
}

func NewSyncResult(computeResult ComputeResult, added, removed, kept []string, changed bool, nginx NginxResult) SyncResult {
	if added == nil {
		added = []string{}
	}
	if removed == nil {
		removed = []string{}
	}
	if kept == nil {
		kept = []string{}
	}
	return SyncResult{
		Changed:           changed,
		Added:             added,
		Removed:           removed,
		Kept:              kept,
		Final:             computeResult.FinalPaths,
		EntriesWithSource: ToEntryWithLabels(computeResult.Entries),
		Nginx:             nginx,
	}
}

func NewDryRunResponse(settings *model.Settings, computeResult ComputeResult, added, removed, kept []string, changed bool) DryRunResponse {
	if added == nil {
		added = []string{}
	}
	if removed == nil {
		removed = []string{}
	}
	if kept == nil {
		kept = []string{}
	}
	persisted := make([]string, 0)
	for _, p := range settings.MediaMountPath {
		np := NormalizePrefix(p)
		if IsValidHTTPPrefix(np) {
			persisted = append(persisted, np)
		}
	}
	return DryRunResponse{
		Persisted: persisted,
		Computed:  ToEntryWithLabels(computeResult.Entries),
		Final:     computeResult.FinalPaths,
		Diff: DiffResult{
			Added:   added,
			Removed: removed,
			Kept:    kept,
			Changed: changed,
		},
	}
}

// unused import guard
var _ = fmt.Sprintf
