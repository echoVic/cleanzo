package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// TrashReminderConfig is independent of junk selection and growth monitoring.
type TrashReminderConfig struct {
	Enabled     bool  `json:"enabled"`
	ThresholdGB int   `json:"thresholdGB"`
	LastPrompt  int64 `json:"-"`
}

type TrashReminderState struct {
	Supported bool                `json:"supported"`
	Config    TrashReminderConfig `json:"config"`
	FDA       bool                `json:"fda"`
}

const trashReminderGB int64 = 1 << 30

var trashReminderConfigMu sync.Mutex

func trashReminderDir() string { return filepath.Dir(configPath()) }
func loadTrashReminder(dir string) TrashReminderConfig {
	c := TrashReminderConfig{Enabled: true, ThresholdGB: 1}
	var saved struct {
		Enabled     *bool `json:"enabled"`
		ThresholdGB int   `json:"thresholdGB"`
		LastPrompt  int64 `json:"lastPrompt"`
	}
	if data, err := os.ReadFile(filepath.Join(dir, "trash-reminder.json")); err == nil && json.Unmarshal(data, &saved) == nil {
		if saved.Enabled != nil {
			c.Enabled = *saved.Enabled
		}
		if validTrashThreshold(saved.ThresholdGB) {
			c.ThresholdGB = saved.ThresholdGB
		}
		c.LastPrompt = saved.LastPrompt
	}
	return c
}

func validTrashThreshold(n int) bool { return slices.Contains([]int{1, 2, 5, 10}, n) }
func saveTrashReminder(dir string, c TrashReminderConfig) error {
	data, err := json.Marshal(struct {
		Enabled     bool  `json:"enabled"`
		ThresholdGB int   `json:"thresholdGB"`
		LastPrompt  int64 `json:"lastPrompt"`
	}{c.Enabled, c.ThresholdGB, c.LastPrompt})
	if err != nil {
		return err
	}
	return writeGrowthFile(filepath.Join(dir, "trash-reminder.json"), data)
}

func trashReminderOn() bool {
	return runtime.GOOS == "darwin" && loadTrashReminder(trashReminderDir()).Enabled
}
func trashReminderDue(c TrashReminderConfig, size int64, now time.Time) bool {
	return c.Enabled && size > int64(c.ThresholdGB)*trashReminderGB && (c.LastPrompt == 0 || now.Sub(time.Unix(c.LastPrompt, 0)) >= 24*time.Hour)
}

func (Cleaner) TrashReminder() TrashReminderState {
	return TrashReminderState{Supported: runtime.GOOS == "darwin", Config: loadTrashReminder(trashReminderDir()), FDA: fullDiskAccess()}
}

// SaveTrashReminder keeps the persisted cooldown, which a page cannot reset.
func (Cleaner) SaveTrashReminder(c TrashReminderConfig) error {
	if runtime.GOOS != "darwin" {
		return codeError{Code: "unsupported"}
	}
	if !validTrashThreshold(c.ThresholdGB) {
		return errors.New("invalid trash reminder threshold")
	}
	growthConfigMu.Lock()
	defer growthConfigMu.Unlock()
	trashReminderConfigMu.Lock()
	c.LastPrompt = loadTrashReminder(trashReminderDir()).LastPrompt
	err := saveTrashReminder(trashReminderDir(), c)
	trashReminderConfigMu.Unlock()
	if err != nil {
		return err
	}
	g := withSystemLoginItem(loadGrowthConfig(growthDir()))
	loginOff := !c.Enabled && !g.Enabled && !menuBarOn() && g.OpenAtLogin
	var saveErr error
	if loginOff {
		g.OpenAtLogin = false
		saveErr = saveGrowthConfig(growthDir(), g)
	}
	applyErr := growthApply(g)
	if loginOff && saveErr == nil {
		growth.changed()
	}
	if saveErr != nil {
		return saveErr
	}
	return applyErr
}

type trashReminderSnapshot struct {
	Size int64
	At   time.Time
	spec itemSpec
}

// Only the user's actual ~/.Trash is eligible; never enumerate unrelated junk.
func scanTrashReminder(ctx context.Context, env junkEnv) (trashReminderSnapshot, error) {
	s := trashReminderSnapshot{At: time.Now()}
	root := filepath.Join(env.home, ".Trash")
	if !env.fda {
		return s, fs.ErrPermission
	}
	info, err := os.Lstat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if !info.IsDir() || info.Mode()&linkModes != 0 {
		return s, fs.ErrPermission
	}
	env.ctx = ctx
	s.spec = itemSpec{item: JunkItem{ID: "sys.trash"}, roots: []string{canonical(root)}}
	env.collectDir(root, nil, 0, claimSet{}, &s.spec)
	plan := planItem(ctx, s.spec, env, make(chan struct{}, scanConcurrency), nil)
	if ctx.Err() != nil {
		return s, ctx.Err()
	}
	if plan.item.Denied || plan.item.ReadErrors > 0 {
		return s, fs.ErrPermission
	}
	s.Size = plan.item.Size
	return s, nil
}

// trashUnchanged reports whether nothing inside a Trash entry changed after
// at. A changed descendant keeps the entire top-level entry recoverable; an
// entry that cannot be read through counts as changed.
func trashUnchanged(ctx context.Context, path string, at time.Time) bool {
	return filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if changedAt(info).After(at) {
			return errors.New("changed")
		}
		return nil
	}) == nil
}

// cleanTrashReminder uses the captured candidates, plus the existing removal
// guards; cleanItem keeps what changed in the Trash after the snapshot.
func cleanTrashReminder(ctx context.Context, env junkEnv, s trashReminderSnapshot, progress func(int64)) CleanResult {
	res := CleanResult{Name: "sys.trash"}
	if ctx.Err() != nil || !env.fda {
		return res
	}
	env.ctx = ctx
	spec := s.spec
	var cleaned int64
	return cleanItem(ctx, spec, env, nil, s.At, cleanHooks{removed: func(_, _ string, b int64) {
		if progress != nil {
			progress(b)
		}
	}}, &cleaned)
}

// The gate is also held by main-window junk cleanup, so a prompt and that
// cleanup cannot delete the same entries concurrently.
var trashCleanupGate sync.Mutex

type trashReminderMonitor struct {
	checking atomic.Bool
	mu       sync.Mutex
	open     bool
	load     func() TrashReminderConfig
	reserve  func(time.Time, int64) bool
	scan     func(context.Context) (trashReminderSnapshot, error)
	show     func(trashReminderSnapshot, func())
}

func (m *trashReminderMonitor) tick(now time.Time) {
	if !m.checking.CompareAndSwap(false, true) {
		return
	}
	defer m.checking.Store(false)
	m.mu.Lock()
	open := m.open
	m.mu.Unlock()
	if open || userScans.Load() > 0 {
		return
	}
	c := m.load()
	if !trashReminderDue(c, int64(c.ThresholdGB)*trashReminderGB+1, now) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := m.scan(ctx)
	if err != nil || userScans.Load() > 0 || !trashReminderDue(c, s.Size, now) || !m.reserve(now, s.Size) {
		return
	}
	m.mu.Lock()
	m.open = true
	m.mu.Unlock()
	m.show(s, func() { m.mu.Lock(); m.open = false; m.mu.Unlock() })
}

var trashReminderStart sync.Once

func startTrashReminder() {
	if runtime.GOOS != "darwin" {
		return
	}
	trashReminderStart.Do(func() {
		m := &trashReminderMonitor{
			load: func() TrashReminderConfig { return loadTrashReminder(trashReminderDir()) },
			reserve: func(now time.Time, size int64) bool {
				trashReminderConfigMu.Lock()
				defer trashReminderConfigMu.Unlock()
				c := loadTrashReminder(trashReminderDir())
				if !trashReminderDue(c, size, now) {
					return false
				}
				c.LastPrompt = now.Unix()
				return saveTrashReminder(trashReminderDir(), c) == nil
			},
			scan: func(ctx context.Context) (trashReminderSnapshot, error) {
				return scanTrashReminder(ctx, trashReminderEnv(false))
			},
			show: showTrashReminder,
		}
		go func() {
			timer := time.NewTimer(time.Minute)
			defer timer.Stop()
			for range timer.C {
				m.tick(time.Now())
				timer.Reset(5 * time.Minute)
			}
		}()
	})
}

func trashReminderEnv(clean bool) junkEnv {
	env := junkEnv{home: homeDir(), tmp: filepath.Clean(os.TempDir()), fda: fullDiskAccess(), guard: &usageGuard{}}
	if clean {
		env.guard = probeUsage()
	}
	return env
}
