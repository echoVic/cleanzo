package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestTrashReminderSettingsAndResidency(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS reminder")
	}
	setHome(t, t.TempDir())
	noMenuBar(t)
	if !residentOn() {
		t.Fatal("default reminder must keep checking without windows")
	}
	if err := saveTrashReminder(trashReminderDir(), TrashReminderConfig{Enabled: true, ThresholdGB: 1, LastPrompt: 123}); err != nil {
		t.Fatal(err)
	}
	g := defaultGrowthConfig()
	g.OpenAtLogin = true
	if err := saveGrowthConfig(growthDir(), g); err != nil {
		t.Fatal(err)
	}
	oldApply, oldChanged, oldLogin := growthApply, growthChanged, growthLoginItem
	t.Cleanup(func() { growthApply, growthChanged, growthLoginItem = oldApply, oldChanged, oldLogin })
	growthLoginItem = func() (bool, bool) { return true, true }
	growthChanged = func(GrowthStatus) {}
	var applied []GrowthConfig
	growthApply = func(g GrowthConfig) error { applied = append(applied, g); return nil }
	if err := (Cleaner{}).SaveTrashReminder(TrashReminderConfig{ThresholdGB: 2, LastPrompt: 999}); err != nil {
		t.Fatal(err)
	}
	c := loadTrashReminder(trashReminderDir())
	if c.Enabled || c.ThresholdGB != 2 || c.LastPrompt != 123 || residentOn() {
		t.Fatalf("saved state: %+v", c)
	}
	if len(applied) != 1 || applied[0].OpenAtLogin || loadGrowthConfig(growthDir()).OpenAtLogin {
		t.Fatal("last resident feature did not clear login")
	}
	if err := (Cleaner{}).SaveTrashReminder(TrashReminderConfig{Enabled: true, ThresholdGB: 3}); err == nil {
		t.Fatal("invalid threshold accepted")
	}
	if got := loadTrashReminder(trashReminderDir()); got != c {
		t.Fatal("invalid save changed configuration")
	}
	if err := (Cleaner{}).SaveTrashReminder(TrashReminderConfig{Enabled: true, ThresholdGB: 5}); err != nil {
		t.Fatal(err)
	}
	if !residentOn() {
		t.Fatal("re-enabled reminder did not restore residency")
	}
	// Removing readings must retain the login item while reminders stay on.
	if err := saveGrowthConfig(growthDir(), g); err != nil {
		t.Fatal(err)
	}
	growthLoginItem = func() (bool, bool) { return true, true }
	stubResident(t)
	if err := (Cleaner{}).SaveMenuBar(nil); err != nil {
		t.Fatal(err)
	}
	if !loadGrowthConfig(growthDir()).OpenAtLogin {
		t.Fatal("removing readings disabled reminder login")
	}
}

func TestTrashReminderConfigAndCooldown(t *testing.T) {
	dir := t.TempDir()
	c := loadTrashReminder(dir)
	if !c.Enabled || c.ThresholdGB != 1 {
		t.Fatalf("defaults: %+v", c)
	}
	now := time.Now()
	if !trashReminderDue(c, gb+1, now) || trashReminderDue(c, gb, now) {
		t.Fatal("must exceed threshold")
	}
	c.LastPrompt = now.Unix()
	if err := saveTrashReminder(dir, c); err != nil {
		t.Fatal(err)
	}
	c = loadTrashReminder(dir)
	if trashReminderDue(c, 2*gb, now.Add(23*time.Hour)) || !trashReminderDue(c, 2*gb, now.Add(24*time.Hour)) {
		t.Fatal("cooldown did not survive reload")
	}
	if trashReminderDue(c, 2*gb, now.Add(-time.Hour)) {
		t.Fatal("clock rollback must not repeat prompt")
	}
	c.Enabled = false
	if trashReminderDue(c, 2*gb, now.Add(48*time.Hour)) {
		t.Fatal("disabled reminder")
	}
}

func TestTrashReminderMonitorSuppressesDuplicatesAndDefers(t *testing.T) {
	c := TrashReminderConfig{Enabled: true, ThresholdGB: 1}
	now := time.Now()
	scans, shown := 0, 0
	var closePopup func()
	m := trashReminderMonitor{
		load: func() TrashReminderConfig { return c },
		scan: func(context.Context) (trashReminderSnapshot, error) {
			scans++
			return trashReminderSnapshot{Size: 2 * gb}, nil
		},
		reserve: func(at time.Time, size int64) bool {
			if !trashReminderDue(c, size, at) {
				return false
			}
			c.LastPrompt = at.Unix()
			return true
		},
		show: func(_ trashReminderSnapshot, closed func()) { shown++; closePopup = closed },
	}
	userScans.Add(1)
	m.tick(now)
	userScans.Add(-1)
	if scans != 0 {
		t.Fatal("scanned during a user operation")
	}
	m.tick(now)
	m.tick(now.Add(48 * time.Hour))
	if shown != 1 || scans != 1 {
		t.Fatal("overlapping prompt")
	}
	closePopup()
	m.tick(now.Add(time.Hour))
	if shown != 1 || scans != 1 {
		t.Fatal("ignored cooldown")
	}
	m.tick(now.Add(24 * time.Hour))
	if shown != 2 {
		t.Fatal("did not resume after cooldown")
	}
	closePopup()
	c.Enabled = false
	m.tick(now.Add(48 * time.Hour))
	if scans != 2 {
		t.Fatal("disabled scan")
	}
}

func TestTrashReminderMonitorRechecksAndSerializes(t *testing.T) {
	for _, mode := range []string{"permission", "save-failed", "disabled-during-scan", "concurrent"} {
		t.Run(mode, func(t *testing.T) {
			c := TrashReminderConfig{Enabled: true, ThresholdGB: 1}
			started, release := make(chan struct{}), make(chan struct{})
			shown, scans := 0, 0
			m := trashReminderMonitor{
				load: func() TrashReminderConfig { return c },
				scan: func(context.Context) (trashReminderSnapshot, error) {
					scans++
					close(started)
					<-release
					if mode == "permission" {
						return trashReminderSnapshot{}, os.ErrPermission
					}
					if mode == "disabled-during-scan" {
						c.Enabled = false
					}
					return trashReminderSnapshot{Size: 2 * gb}, nil
				},
				reserve: func(at time.Time, size int64) bool { return mode != "save-failed" && trashReminderDue(c, size, at) },
				show:    func(_ trashReminderSnapshot, _ func()) { shown++ },
			}
			var wg sync.WaitGroup
			wg.Go(func() { m.tick(time.Now()) })
			<-started
			m.tick(time.Now())
			close(release)
			wg.Wait()
			want := 0
			if mode == "concurrent" {
				want = 1
			}
			if scans != 1 || shown != want {
				t.Fatalf("scans %d, shown %d", scans, shown)
			}
		})
	}
}

func TestTrashReminderCancelBetweenItems(t *testing.T) {
	env := testEnv(t)
	trash := filepath.Join(env.home, ".Trash")
	writeFile(t, filepath.Join(trash, "a.bin"), 100)
	writeFile(t, filepath.Join(trash, "b.bin"), 100)
	s, err := scanTrashReminder(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := cleanTrashReminder(ctx, env, s, func(int64) { cancel() })
	if r.Freed != 100 || exists(filepath.Join(trash, "a.bin")) == exists(filepath.Join(trash, "b.bin")) {
		t.Fatalf("did not stop after one item: %+v", r)
	}
}

func TestTrashReminderRechecksBeforeEachRemoval(t *testing.T) {
	env := testEnv(t)
	trash := filepath.Join(env.home, ".Trash")
	writeFile(t, filepath.Join(trash, "a.bin"), 100)
	writeFile(t, filepath.Join(trash, "b-folder", "nested", "old.bin"), 100)
	s, err := scanTrashReminder(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	fresh := filepath.Join(trash, "b-folder", "nested", "new.bin")
	r := cleanTrashReminder(context.Background(), env, s, func(int64) { writeFile(t, fresh, 100) })
	if r.Freed != 100 || !exists(fresh) {
		t.Fatalf("changed subtree removed: %+v", r)
	}
}

func TestTrashReminderSnapshotLimitsDeletion(t *testing.T) {
	env := testEnv(t)
	trash := filepath.Join(env.home, ".Trash")
	old := filepath.Join(trash, "old.bin")
	changed := filepath.Join(trash, "folder", "nested", "keep.bin")
	outside := filepath.Join(env.home, "Documents", "keep.bin")
	writeFile(t, old, 100)
	writeFile(t, changed, 200)
	writeFile(t, outside, 400)
	s, err := scanTrashReminder(context.Background(), env)
	if err != nil || s.Size != 300 {
		t.Fatalf("snapshot: %+v, %v", s, err)
	}
	time.Sleep(20 * time.Millisecond)
	fresh := filepath.Join(trash, "new.bin")
	writeFile(t, fresh, 500)
	writeFile(t, filepath.Join(filepath.Dir(changed), "new.bin"), 600)
	r := cleanTrashReminder(context.Background(), env, s, nil)
	if r.Freed != 100 || exists(old) || !exists(changed) || !exists(fresh) || !exists(outside) {
		t.Fatalf("scope/cutoff: %+v", r)
	}
}

func TestTrashReminderCancellationAndPermission(t *testing.T) {
	env := testEnv(t)
	p := filepath.Join(env.home, ".Trash", "keep.bin")
	writeFile(t, p, 100)
	s, err := scanTrashReminder(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := cleanTrashReminder(ctx, env, s, nil)
	if r.Freed != 0 || !exists(p) {
		t.Fatalf("cancelled: %+v", r)
	}
	env.fda = false
	if _, err := scanTrashReminder(context.Background(), env); err == nil {
		t.Fatal("permission denied must not produce an actionable snapshot")
	}
}

func TestTrashReminderRejectsSymlinkRoot(t *testing.T) {
	env := testEnv(t)
	outside := filepath.Join(env.home, "Documents")
	writeFile(t, filepath.Join(outside, "keep.bin"), 100)
	if err := os.Symlink(outside, filepath.Join(env.home, ".Trash")); err != nil {
		t.Fatal(err)
	}
	if _, err := scanTrashReminder(context.Background(), env); err == nil {
		t.Fatal("symlink trash root accepted")
	}
}
