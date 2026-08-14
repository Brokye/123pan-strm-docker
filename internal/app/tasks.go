package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

type App struct {
	cfg         *Config
	tasks       map[string]*TaskState
	mu          sync.Mutex
	cron        *cron.Cron
	cronMu      sync.Mutex
	cronEntries map[string]cron.EntryID
}

func NewApp(cfg *Config) *App {
	return &App{
		cfg:         cfg,
		tasks:       map[string]*TaskState{},
		cronEntries: map[string]cron.EntryID{},
	}
}

func randTaskID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (a *App) cleanupTasks() {
	now := time.Now().Unix()
	a.mu.Lock()
	defer a.mu.Unlock()
	for tid, t := range a.tasks {
		if now-t.Updated > 3600 {
			delete(a.tasks, tid)
		}
	}
}

func (a *App) startTask(name string, run func(emit func(map[string]any))) string {
	taskID := randTaskID()
	state := &TaskState{
		ID:       taskID,
		Name:     name,
		State:    "running",
		Progress: 0,
		Message:  "准备中...",
		Updated:  time.Now().Unix(),
	}
	a.mu.Lock()
	a.tasks[taskID] = state
	a.mu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				a.mu.Lock()
				state.State = "error"
				state.Error = fmtErrMsg(r)
				state.Message = fmtErrMsg(r)
				state.Updated = time.Now().Unix()
				a.mu.Unlock()
				if os.Getenv("PAN123_DEBUG") != "" {
					buf := make([]byte, 4096)
					n := runtime.Stack(buf, false)
					fmt.Fprintf(os.Stderr, "task %s panic: %v\n%s\n", name, r, buf[:n])
				}
			}
		}()
		emit := func(upd map[string]any) {
			a.mu.Lock()
			defer a.mu.Unlock()
			if state.State == "cancelled" {
				return
			}
			if v, ok := upd["message"].(string); ok {
				state.Message = v
			}
			if v, ok := upd["progress"].(int); ok {
				state.Progress = v
			}
			if v, ok := upd["result"]; ok {
				state.Result = v
			}
			state.Updated = time.Now().Unix()
		}
		run(emit)
		a.mu.Lock()
		state.State = "done"
		state.Progress = 100
		state.Updated = time.Now().Unix()
		a.mu.Unlock()
	}()
	return taskID
}

func (a *App) getTask(taskID string) *TaskState {
	a.cleanupTasks()
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tasks[taskID]
}

func fmtErrMsg(r any) string {
	if e, ok := r.(error); ok {
		return e.Error()
	}
	if s, ok := r.(string); ok {
		return s
	}
	return "unknown error"
}
