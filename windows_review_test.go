package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// windowsReviewEnv exercises the Windows catalog and sources on either host.
func windowsReviewEnv(t *testing.T) junkEnv {
	env := testEnv(t)
	env.rules, env.windowsLayout = windowsJunkRules, true
	env.localAppData = filepath.Join(env.home, "AppData", "Local")
	env.appData = filepath.Join(env.home, "AppData", "Roaming")
	return env
}

// TestWindowsGenericCachesRequireElectronMarker keeps chat archives out of permanent cleanup.
func TestWindowsGenericCachesRequireElectronMarker(t *testing.T) {
	env := windowsReviewEnv(t)
	for _, app := range []string{".purple", "HexChat", "mIRC", "Electron"} {
		for _, cache := range []string{"Cache", "Code Cache", "GPUCache", "logs"} {
			writeFile(t, filepath.Join(env.appData, app, cache, "data"), 100)
		}
	}
	writeFile(t, filepath.Join(env.appData, "Electron", "Local State"), 2)
	specs, _ := expandItems(&env)
	var apps []itemSpec
	for _, spec := range specs {
		if spec.item.Group == string(groupApps) {
			apps = append(apps, spec)
		}
	}
	if len(apps) != 1 || apps[0].item.ID != "app:dir:electron" || len(apps[0].cands) != 4 {
		t.Fatalf("generic caches: %v", itemIDs(apps))
	}
}

// TestWindowsCrashpadMatchedOnce prevents overlapping wildcard targets from doubling capacity.
func TestWindowsCrashpadMatchedOnce(t *testing.T) {
	env := windowsReviewEnv(t)
	writeFile(t, filepath.Join(env.localAppData, "Vivaldi", "User Data", "Crashpad", "pending", "d.dmp"), 8192)
	specs, _ := expandItems(&env)
	for _, spec := range specs {
		if spec.item.ID != "sys.crashdumps" {
			continue
		}
		plan := planItem(context.Background(), spec, env, make(chan struct{}, 2), nil)
		if len(spec.cands) != 1 || plan.item.Size != 8192 {
			t.Fatalf("Crashpad candidates=%d size=%d", len(spec.cands), plan.item.Size)
		}
		return
	}
	t.Fatal("missing Crashpad row")
}

// TestWindowsInstallersExcludeUninstallers keeps portable applications' uninstall programs intact.
func TestWindowsInstallersExcludeUninstallers(t *testing.T) {
	env := windowsReviewEnv(t)
	for _, name := range []string{"uninstall.exe", "unins000.exe", "APP-UNINSTALLER.EXE", "setup.exe", "app-installer.exe"} {
		writeFile(t, filepath.Join(env.home, "Downloads", name), 100)
	}
	specs, _ := expandItems(&env)
	for _, spec := range specs {
		if spec.item.ID != "review.installers" {
			continue
		}
		var names []string
		for _, c := range spec.cands {
			names = append(names, filepath.Base(c.path))
		}
		slices.Sort(names)
		if !slices.Equal(names, []string{"app-installer.exe", "setup.exe"}) {
			t.Fatalf("installers: %q", names)
		}
		return
	}
	t.Fatal("missing installers")
}

// TestExecCommandsUseQuietHelper prevents console flashes from every production command in shared code.
func TestExecCommandsUseQuietHelper(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "_darwin.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		allowed := map[*ast.CallExpr]bool{}
		ast.Inspect(file, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "quietCommand" && len(c.Args) == 1 {
					if inner, ok := c.Args[0].(*ast.CallExpr); ok {
						allowed[inner] = true
					}
				}
			}
			return true
		})
		ast.Inspect(file, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := c.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if ok && pkg.Name == "exec" && (sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext") && !allowed[c] {
				t.Errorf("%s: command must go through quietCommand", fset.Position(c.Pos()))
			}
			return true
		})
	}
}
