package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// sandboxWindowsTests isolates every Windows test from the real user profile.
func sandboxWindowsTests() (func(), error) {
	if runtime.GOOS != "windows" {
		return func() {}, nil
	}
	if !flag.Parsed() {
		flag.Parse()
	}
	if os.Getenv("CLEANZO_REAL_PROFILE_SCAN") == "1" {
		run := flag.Lookup("test.run")
		if run == nil || run.Value.String() != "^TestWindowsJunkScanReport$" {
			return nil, fmt.Errorf("CLEANZO_REAL_PROFILE_SCAN=1 requires exactly -run '^TestWindowsJunkScanReport$'")
		}
		return func() {}, nil
	}
	dir, err := os.MkdirTemp("", "cleanzo-tests-")
	if err != nil {
		return nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	sandbox, err := filepath.EvalSymlinks(dir)
	if err != nil {
		cleanup()
		return nil, err
	}
	for key, path := range map[string]string{
		"HOME": sandbox, "USERPROFILE": sandbox,
		"APPDATA":      filepath.Join(sandbox, "AppData", "Roaming"),
		"LOCALAPPDATA": filepath.Join(sandbox, "AppData", "Local"),
		"TEMP":         filepath.Join(sandbox, "Temp"), "TMP": filepath.Join(sandbox, "Temp"),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			cleanup()
			return nil, err
		}
		if err := os.Setenv(key, path); err != nil {
			cleanup()
			return nil, err
		}
	}
	for label, path := range map[string]string{"home": homeDir(), "temp": os.TempDir()} {
		if path == "" || !within(resolvePath(path), sandbox) {
			cleanup()
			return nil, fmt.Errorf("%s %q is outside sandbox %q", label, path, sandbox)
		}
	}
	return cleanup, nil
}

// setHome redirects all user directories and discards remembered path reads.
func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
		for key, part := range map[string]string{"APPDATA": "Roaming", "LOCALAPPDATA": "Local"} {
			path := filepath.Join(dir, "AppData", part)
			mustMkdir(t, path)
			t.Setenv(key, path)
		}
	}
	expandMemo.Lock()
	if expandMemo.users > 0 {
		expandMemo.resolved, expandMemo.lstat = map[string]string{}, map[string]lstatResult{}
	}
	expandMemo.Unlock()
}

// slashPathLines finds literal separators in path comparisons and splits.
func slashPathLines(source string) ([]int, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "paths.go", source, 0)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(source, "\n")
	marker := regexp.MustCompile(`// slash-ok: \S.*$`)
	var found []int
	ast.Inspect(file, func(n ast.Node) bool {
		bad := false
		switch n := n.(type) {
		case *ast.BasicLit:
			bad = n.Kind == token.STRING && n.Value == `"~/"`
		case *ast.BinaryExpr:
			for _, operand := range []ast.Expr{n.X, n.Y} {
				if lit, ok := operand.(*ast.BasicLit); ok && lit.Kind == token.STRING && lit.Value == `"/"` && n.Op == token.ADD {
					bad = true
				}
			}
		case *ast.CallExpr:
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "Split" || sel.Sel.Name == "SplitN") && len(n.Args) >= 2 {
				if lit, ok := n.Args[1].(*ast.BasicLit); ok && lit.Value == `"/"` {
					bad = true
				}
			}
		}
		if bad {
			line := fset.Position(n.Pos()).Line
			if !marker.MatchString(lines[line-1]) {
				found = append(found, line)
			}
		}
		return true
	})
	return found, nil
}

// TestNoSlashPathLiterals keeps shared path logic separator-aware.
func TestNoSlashPathLiterals(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == "build" || entry.Name() == "dist" || entry.Name() == "交付" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "_darwin.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines, err := slashPathLines(string(data))
		if err != nil {
			return err
		}
		for _, line := range lines {
			t.Errorf("%s:%d: use filepath separators or end the line with // slash-ok: <reason>", path, line)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestSlashPathLiteralLint checks each forbidden form and documented exceptions.
func TestSlashPathLiteralLint(t *testing.T) {
	for _, expression := range []string{`p+"/"`, `"/"+p`, `"~/"`, `strings.Split(p, "/")`, `strings.SplitN(p, "/", 2)`, `strings.HasPrefix(p, dir+"/")`} {
		for _, marked := range []bool{false, true} {
			source := "package main\nfunc f() { _ = " + expression + " }"
			if marked {
				source += " // slash-ok: URL components"
			}
			lines, err := slashPathLines(source)
			if err != nil || (len(lines) == 0) != marked {
				t.Errorf("%q marked=%v: lines=%v, err=%v", expression, marked, lines, err)
			}
		}
	}
}

// platformFile recognizes GOOS suffixes and GOOS build constraints.
func platformFile(name string, file *ast.File) bool {
	oses := strings.Fields("aix android darwin dragonfly freebsd hurd illumos ios js linux netbsd openbsd plan9 solaris wasip1 windows zos")
	stem := strings.TrimSuffix(filepath.Base(name), ".go")
	for _, os := range oses {
		for _, component := range strings.Split(stem, "_")[1:] {
			if component == os {
				return true
			}
		}
	}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if !strings.HasPrefix(comment.Text, "//go:build ") && !strings.HasPrefix(comment.Text, "// +build ") {
				continue
			}
			for _, tag := range strings.FieldsFunc(comment.Text, func(r rune) bool { return (r < 'a' || r > 'z') && (r < '0' || r > '9') }) {
				for _, os := range oses {
					if tag == os {
						return true
					}
				}
			}
		}
	}
	return false
}

// TestBindingsArePlatformNeutral prevents host-specific bound APIs.
func TestBindingsArePlatformNeutral(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		if !platformFile(path, file) {
			continue
		}
		for _, decl := range file.Decls {
			var names []*ast.Ident
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				names = append(names, decl.Name)
				if decl.Recv != nil {
					ast.Inspect(decl.Recv, func(n ast.Node) bool {
						if id, ok := n.(*ast.Ident); ok && (id.Name == "Cleaner" || id.Name == "LanguageSettings") {
							t.Errorf("%s: bound method %s must be untagged", path, decl.Name)
						}
						return true
					})
				}
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					switch spec := spec.(type) {
					case *ast.TypeSpec:
						names = append(names, spec.Name)
					case *ast.ValueSpec:
						names = append(names, spec.Names...)
					}
				}
			}
			for _, name := range names {
				if name.IsExported() {
					t.Errorf("%s: exported declaration %s must be untagged", path, name)
				}
			}
		}
	}
}

// TestPathHelpers covers containment boundaries and home display round trips.
func TestPathHelpers(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	for _, tc := range []struct {
		path, dir string
		want      bool
	}{
		{home, home, true},
		{filepath.Join(home, "cache", "file"), home, true},
		{home + "-other", home, false},
		{filepath.Join(home, "cache-other"), filepath.Join(home, "cache"), false},
		{filepath.Join(home, "cache"), home + string(filepath.Separator), true},
		{home, filepath.VolumeName(home) + string(filepath.Separator), true},
	} {
		if got := within(tc.path, tc.dir); got != tc.want {
			t.Errorf("within(%q, %q)=%v, want %v", tc.path, tc.dir, got, tc.want)
		}
	}
	for _, tc := range []struct{ short, full string }{
		{"~", home},
		{"~" + string(filepath.Separator) + filepath.Join("cache", "file"), filepath.Join(home, "cache", "file")},
		{home + "-other", home + "-other"},
	} {
		if got := expandHome(tc.short); got != tc.full {
			t.Errorf("expandHome(%q)=%q, want %q", tc.short, got, tc.full)
		}
		if got := shortHome(tc.full); got != tc.short {
			t.Errorf("shortHome(%q)=%q, want %q", tc.full, got, tc.short)
		}
	}
	if got := expandHome("~/cache/file"); got != filepath.Join(home, "cache", "file") {
		t.Errorf("forward-slash home expansion=%q", got)
	}
}

// TestClaimSetNativePaths checks ancestor ownership with native separators.
func TestClaimSetNativePaths(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(a, "b")
	var claims claimSet
	claims.claimKey(b)
	if !claims.claimedKey(b) || !claims.claimedKey(filepath.Join(b, "child")) || claims.claimedKey(a) || claims.claimedKey(filepath.Join(a, "bb")) {
		t.Fatal("claim crossed a directory boundary or lost an ancestor")
	}
	if !claims.insideKey(a) || !claims.insideKey(root) || claims.insideKey(b) || claims.insideKey(filepath.Join(root, "other")) {
		t.Fatal("nested claims did not preserve their directory boundaries")
	}
	copy := claims.clone()
	copy.claimKey(filepath.Join(root, "other"))
	if claims.claimedKey(filepath.Join(root, "other")) || !copy.claimedKey(filepath.Join(root, "other")) {
		t.Fatal("claim snapshots share state")
	}
}
