package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ---------------------------------------------------------------------------
// Skill updates
// ---------------------------------------------------------------------------
//
// A skill installed from GitHub can be compared with its source and
// upgraded. Where a skill came from is read, never guessed, from:
//
//   cleanzo     Cleanzo's own record of the skills it upgraded
//   skills-cli  ~/.agents/.skill-lock.json, written by `npx skills`: the
//               repository, the SKILL.md path and the folder's git tree hash
//   git         a skill folder that is itself a git clone
//   cc-switch   ~/.cc-switch/cc-switch.db, read only: the repository and
//               the SKILL.md path from its readme URL
//
// A folder's version is its git tree hash, the hash GitHub reports for the
// folder, computed locally the same way. A skill has an update when the
// source's tree differs from the one it was installed at. It is modified
// when the folder no longer matches the tree it was installed at; such a
// skill is upgraded only when the user confirms overwriting it. Without a
// recorded installed tree (CC Switch keeps a hash of its own) the local
// folder is compared with the source directly.
//
// Upgrading downloads the repository archive from codeload.github.com,
// checks that the extracted folder has exactly the tree GitHub reported,
// moves the old folder to the Trash and puts the new one in its place.
// Links to the folder keep working. A git clone is upgraded with a
// fast-forward pull instead. CC Switch's database is never written; after
// an upgrade it may show the skill as changed.

const (
	originCleanzo   = "cleanzo"
	originSkillsCLI = "skills-cli"
	originGit       = "git"
	originCCSwitch  = "cc-switch"
)

// SkillOrigin is where a skill folder came from.
type SkillOrigin struct {
	Kind   string `json:"kind"`   // cleanzo, skills-cli, git, cc-switch
	Repo   string `json:"repo"`   // owner/name on GitHub; "" for non-GitHub git remotes
	Ref    string `json:"ref"`    // branch, or "" for the default branch
	Path   string `json:"path"`   // folder inside the repository; "" for its root
	Remote string `json:"remote"` // git remote URL, for git clones
	Tree   string `json:"tree"`   // git tree hash the folder was installed at; "" when unknown
}

// SkillUpdate is a skill folder's update state, for one entry.
type SkillUpdate struct {
	Path      string      `json:"path"` // the folder (resolved), the one an upgrade replaces
	Name      string      `json:"name"`
	Origin    SkillOrigin `json:"origin"`
	Local     string      `json:"local"`    // tree hash of the folder now
	Remote    string      `json:"remote"`   // tree hash at the source; "" before a check
	Status    string      `json:"status"`   // unknown, current, update, missing, error
	Modified  bool        `json:"modified"` // the folder differs from the tree it was installed at
	Error     string      `json:"error"`
	CheckedAt int64       `json:"checkedAt"`
}

// skillUpdateHTTP fetches from GitHub; tests replace it.
var skillUpdateHTTP = &http.Client{Timeout: 60 * time.Second}

// skillGitHubAPI and skillCodeload are GitHub's endpoints; tests replace them.
var (
	skillGitHubAPI = "https://api.github.com"
	skillCodeload  = "https://codeload.github.com"
)

func init() {
	// Test seam: redirect GitHub to a local server without touching the
	// product code.
	if u := os.Getenv("CLEANZO_GITHUB_API"); u != "" {
		skillGitHubAPI = u
	}
	if u := os.Getenv("CLEANZO_CODELOAD"); u != "" {
		skillCodeload = u
	}
}

// skillGitCommand runs git; tests replace it.
var skillGitCommand = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd.CombinedOutput()
}

// skillArchiveLimit bounds a downloaded repository archive.
const skillArchiveLimit = 256 << 20

var (
	errSkillNoOrigin = errors.New("no-origin")
	errSkillModified = errors.New("modified")
	errSkillMismatch = errors.New("mismatch")
	errSkillNotFound = errors.New("not-found")
	errSkillRate     = errors.New("rate-limited")
)

// ---------------------------------------------------------------------------
// Git tree hashes
// ---------------------------------------------------------------------------

// gitTreeHash computes the hash git gives a folder as a tree: files are
// blobs (100644, or 100755 when executable), links are 120000 with their
// target, folders are subtrees; .git and .DS_Store are left out, as are
// empty folders, which git cannot hold.
func gitTreeHash(dir string, recorded ...map[string]string) (string, error) {
	var modes map[string]string
	if runtime.GOOS == "windows" {
		modes = loadSkillRecords()[resolvePath(dir)].Modes
		if len(recorded) > 0 {
			modes = recorded[0]
		}
	}
	h, empty, err := gitTreeWithModes(dir, dir, modes, runtime.GOOS == "windows")
	if err != nil {
		return "", err
	}
	if empty {
		return "4b825dc642cb6eb9a060e54bf8d69288fbee4904", nil
	}
	return h, nil
}

// gitTreeWithModes uses Git's recorded modes where Windows has no executable permission bit.
func gitTreeWithModes(dir, base string, modes map[string]string, windows bool) (hash string, empty bool, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false, err
	}
	type entry struct {
		mode, name, sortKey string
		hash                []byte
	}
	var list []entry
	for _, e := range entries {
		name := e.Name()
		if name == ".git" || name == ".DS_Store" {
			continue
		}
		p := filepath.Join(dir, name)
		info, err := os.Lstat(p)
		if err != nil {
			return "", false, err
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return "", false, err
			}
			list = append(list, entry{"120000", name, name, gitObject("blob", []byte(target))})
		case info.IsDir():
			sub, subEmpty, err := gitTreeWithModes(p, base, modes, windows)
			if err != nil {
				return "", false, err
			}
			if subEmpty {
				continue
			}
			b, _ := hex.DecodeString(sub)
			list = append(list, entry{"40000", name, name + "/", b}) // slash-ok: Git tree directory sort keys use slashes
		case info.Mode().IsRegular():
			data, err := os.ReadFile(p)
			if err != nil {
				return "", false, err
			}
			mode := "100644"
			if windows {
				rel, _ := filepath.Rel(base, p)
				if modes[filepath.ToSlash(rel)] == "100755" {
					mode = "100755"
				}
			} else if info.Mode()&0o111 != 0 {
				mode = "100755"
			}
			list = append(list, entry{mode, name, name, gitObject("blob", data)})
		}
	}
	if len(list) == 0 {
		return "", true, nil
	}
	sort.Slice(list, func(i, j int) bool { return list[i].sortKey < list[j].sortKey })
	var body []byte
	for _, e := range list {
		body = append(body, e.mode+" "+e.name+"\x00"...)
		body = append(body, e.hash...)
	}
	return hex.EncodeToString(gitObject("tree", body)), false, nil
}

func gitObject(kind string, data []byte) []byte {
	h := sha1.New()
	fmt.Fprintf(h, "%s %d\x00", kind, len(data))
	h.Write(data)
	return h.Sum(nil)
}

// ---------------------------------------------------------------------------
// Origins
// ---------------------------------------------------------------------------

// skillUpdatesPath is Cleanzo's record of the skills it upgraded.
func skillUpdatesPath() string {
	return filepath.Join(filepath.Dir(configPath()), "skill-updates.json")
}

// skillRecord is Cleanzo's record of one upgraded folder.
type skillRecord struct {
	Origin    SkillOrigin       `json:"origin"`
	UpdatedAt int64             `json:"updatedAt"`
	Modes     map[string]string `json:"modes,omitempty"`
}

var skillRecordMu sync.Mutex

func loadSkillRecords() map[string]skillRecord {
	out := map[string]skillRecord{}
	data, err := os.ReadFile(skillUpdatesPath())
	if err == nil {
		_ = json.Unmarshal(data, &out)
	}
	return out
}

func saveSkillRecord(folder string, rec skillRecord) error {
	skillRecordMu.Lock()
	defer skillRecordMu.Unlock()
	all := loadSkillRecords()
	all[folder] = rec
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	p := skillUpdatesPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// skillLock is ~/.agents/.skill-lock.json, as `npx skills` writes it.
type skillLock struct {
	Skills map[string]struct {
		Source          string `json:"source"`
		SourceType      string `json:"sourceType"`
		SourceURL       string `json:"sourceUrl"`
		Ref             string `json:"ref"`
		SkillPath       string `json:"skillPath"`
		SkillFolderHash string `json:"skillFolderHash"`
	} `json:"skills"`
}

func loadSkillLock() map[string]SkillOrigin {
	out := map[string]SkillOrigin{}
	data, err := os.ReadFile(filepath.Join(homeDir(), ".agents/.skill-lock.json"))
	if err != nil {
		return out
	}
	var lock skillLock
	if json.Unmarshal(data, &lock) != nil {
		return out
	}
	for name, s := range lock.Skills {
		repo := githubRepo(s.SourceURL)
		if repo == "" && s.SourceType == "github" {
			repo = cleanRepo(s.Source)
		}
		if repo == "" {
			continue
		}
		out[name] = SkillOrigin{Kind: originSkillsCLI, Repo: repo, Ref: s.Ref, Path: skillFolderOf(s.SkillPath), Tree: s.SkillFolderHash}
	}
	return out
}

// ccSwitchQuery reads CC Switch's database; tests replace it.
var ccSwitchQuery = func(ctx context.Context, db string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, sqliteTool(), "-readonly", "-json", db,
		"select directory, repo_owner, repo_name, repo_branch, readme_url from skills where repo_owner != '' and repo_name != ''").Output()
}

var readmeURL = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/blob/([^/]+)/(.+)$`)

func loadCCSwitch(ctx context.Context) map[string]SkillOrigin {
	out := map[string]SkillOrigin{}
	db := filepath.Join(homeDir(), ".cc-switch/cc-switch.db")
	if !fileExists(db) {
		return out
	}
	data, err := ccSwitchQuery(ctx, db)
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return out
	}
	var rows []struct {
		Directory string `json:"directory"`
		Owner     string `json:"repo_owner"`
		Name      string `json:"repo_name"`
		Branch    string `json:"repo_branch"`
		Readme    string `json:"readme_url"`
	}
	if json.Unmarshal(data, &rows) != nil {
		return out
	}
	for _, r := range rows {
		o := SkillOrigin{Kind: originCCSwitch, Repo: r.Owner + "/" + r.Name, Ref: r.Branch} // slash-ok: GitHub owner/repository identifier
		if m := readmeURL.FindStringSubmatch(r.Readme); m != nil {
			o.Repo, o.Ref, o.Path = m[1]+"/"+m[2], m[3], skillFolderOf(m[4]) // slash-ok: GitHub owner/repository identifier
		}
		if o.Ref == "HEAD" {
			o.Ref = ""
		}
		if r.Directory != "" && !strings.Contains(r.Directory, "/") {
			out[r.Directory] = o
		}
	}
	return out
}

// skillFolderOf turns "skills/x/SKILL.md" into "skills/x".
func skillFolderOf(skillPath string) string {
	p := path.Clean(strings.TrimPrefix(skillPath, "/"))
	if path.Base(p) == "SKILL.md" {
		p = path.Dir(p)
	}
	if p == "." {
		return ""
	}
	return p
}

var githubURL = regexp.MustCompile(`^(?:https?://|git@|ssh://git@)github\.com[/:]([^/]+)/([^/]+?)(?:\.git)?/?$`)

func githubRepo(u string) string {
	if m := githubURL.FindStringSubmatch(strings.TrimSpace(u)); m != nil {
		return m[1] + "/" + m[2] // slash-ok: GitHub owner/repository identifier
	}
	return ""
}

var repoName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func cleanRepo(s string) string {
	s = strings.TrimSuffix(strings.TrimSpace(s), ".git")
	if repoName.MatchString(s) {
		return s
	}
	return ""
}

// gitOrigin reads a git clone's remote and branch.
func gitOrigin(ctx context.Context, dir string) (SkillOrigin, bool) {
	if !fileExists(filepath.Join(dir, ".git")) {
		return SkillOrigin{}, false
	}
	out, err := skillGitCommand(ctx, dir, "remote", "get-url", "origin")
	if err != nil {
		return SkillOrigin{}, false
	}
	remote := strings.TrimSpace(string(out))
	o := SkillOrigin{Kind: originGit, Remote: remote, Repo: githubRepo(remote)}
	if b, err := skillGitCommand(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		if br := strings.TrimSpace(string(b)); br != "HEAD" {
			o.Ref = br
		}
	}
	return o, true
}

// skillOrigins finds where each skill folder came from.
func skillOrigins(ctx context.Context, skills []Skill) map[string]SkillOrigin {
	records := loadSkillRecords()
	lock := loadSkillLock()
	cc := loadCCSwitch(ctx)
	out := map[string]SkillOrigin{}
	for _, s := range skills {
		for _, c := range s.Copies {
			if c.Real == "" || (c.Kind != "dir" && c.Kind != "link") {
				continue
			}
			if _, done := out[c.Real]; done {
				continue
			}
			if o, ok := gitOrigin(ctx, c.Real); ok {
				out[c.Real] = o
				continue
			}
			if r, ok := records[c.Real]; ok && r.Origin.Repo != "" {
				out[c.Real] = r.Origin
				continue
			}
			agents := filepath.Join(homeDir(), ".agents/skills")
			if o, ok := lock[s.Name]; ok && filepath.Dir(c.Real) == resolvePath(agents) {
				out[c.Real] = o
				continue
			}
			if o, ok := cc[filepath.Base(c.Real)]; ok {
				out[c.Real] = o
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Checking
// ---------------------------------------------------------------------------

// githubTree is one repository tree listing, cached for a check.
type githubTree struct {
	sha   map[string]string // folder path -> tree hash; "" is the root
	err   error
	ready chan struct{}
}

type treeCache struct {
	mu    sync.Mutex
	trees map[string]*githubTree
}

func (tc *treeCache) get(ctx context.Context, repo, ref string) (map[string]string, error) {
	key := repo + "@" + ref
	tc.mu.Lock()
	t, ok := tc.trees[key]
	if !ok {
		t = &githubTree{ready: make(chan struct{})}
		tc.trees[key] = t
		tc.mu.Unlock()
		t.sha, t.err = fetchGitHubTree(ctx, repo, ref)
		close(t.ready)
	} else {
		tc.mu.Unlock()
		<-t.ready
	}
	return t.sha, t.err
}

func githubRequest(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Cleanzo")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return skillUpdateHTTP.Do(req)
}

// fetchGitHubTree lists a repository's folders and their tree hashes.
func fetchGitHubTree(ctx context.Context, repo, ref string) (map[string]string, error) {
	if ref == "" {
		ref = "HEAD"
	}
	u := fmt.Sprintf("%s/repos/%s/git/trees/%s?recursive=1", skillGitHubAPI, repo, url.PathEscape(ref))
	resp, err := githubRequest(ctx, u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, errSkillNotFound
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return nil, errSkillRate
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("github %d", resp.StatusCode)
	}
	var body struct {
		SHA  string `json:"sha"`
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&body); err != nil {
		return nil, err
	}
	out := map[string]string{"": body.SHA}
	for _, e := range body.Tree {
		if e.Type == "tree" {
			out[e.Path] = e.SHA
		}
	}
	if body.Truncated {
		out["\x00truncated"] = "1"
	}
	return out, nil
}

// CheckSkillUpdates compares the skills that have an origin with their
// source. Repositories are listed once each; nothing is downloaded.
func (c Cleaner) CheckSkillUpdates(ctx context.Context) ([]SkillUpdate, error) {
	scan, err := c.ScanSkills(ctx)
	if err != nil {
		return nil, err
	}
	return checkSkillUpdates(ctx, scan.Skills, true), nil
}

// ListSkillOrigins reports the skills that have an origin, without going
// to the network.
func (c Cleaner) ListSkillOrigins(ctx context.Context) ([]SkillUpdate, error) {
	scan, err := c.ScanSkills(ctx)
	if err != nil {
		return nil, err
	}
	return checkSkillUpdates(ctx, scan.Skills, false), nil
}

func checkSkillUpdates(ctx context.Context, skills []Skill, online bool) []SkillUpdate {
	origins := skillOrigins(ctx, skills)
	names := map[string]string{}
	for _, s := range skills {
		for _, c := range s.Copies {
			if _, ok := origins[c.Real]; ok {
				names[c.Real] = s.Name
			}
		}
	}
	folders := make([]string, 0, len(origins))
	for f := range origins {
		folders = append(folders, f)
	}
	sort.Strings(folders)
	out := make([]SkillUpdate, len(folders))
	cache := &treeCache{trees: map[string]*githubTree{}}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	now := skillNow().Unix()
	for i, folder := range folders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = checkOne(ctx, folder, names[folder], origins[folder], cache, online, now)
		}()
	}
	wg.Wait()
	return out
}

func checkOne(ctx context.Context, folder, name string, o SkillOrigin, cache *treeCache, online bool, now int64) SkillUpdate {
	u := SkillUpdate{Path: folder, Name: name, Origin: o, Status: "unknown"}
	var modes []map[string]string
	if runtime.GOOS == "windows" && o.Kind == originGit {
		recorded, err := skillIndexModes(ctx, folder)
		if err != nil {
			u.Status, u.Error = "error", err.Error()
			return u
		}
		modes = append(modes, recorded)
	}
	local, err := gitTreeHash(folder, modes...)
	if err != nil {
		u.Status, u.Error = "error", err.Error()
		return u
	}
	u.Local = local
	if o.Tree != "" {
		u.Modified = local != o.Tree
	}
	if !online {
		return u
	}
	u.CheckedAt = now
	if o.Kind == originGit {
		return checkGit(ctx, u)
	}
	if o.Repo == "" {
		return u
	}
	tree, err := cache.get(ctx, o.Repo, o.Ref)
	if err != nil {
		u.Status, u.Error = "error", err.Error()
		return u
	}
	remote, ok := tree[o.Path]
	if !ok {
		if tree["\x00truncated"] != "" {
			u.Status, u.Error = "error", "truncated"
		} else {
			u.Status = "missing"
		}
		return u
	}
	u.Remote = remote
	base := o.Tree
	if base == "" {
		base = local
	}
	if remote == base {
		u.Status = "current"
	} else {
		u.Status = "update"
	}
	return u
}

// checkGit compares a clone's HEAD with its upstream branch on the remote.
// It only asks the remote (`git ls-remote`); nothing in the clone changes.
func checkGit(ctx context.Context, u SkillUpdate) SkillUpdate {
	dir := u.Path
	if out, err := skillGitCommand(ctx, dir, "status", "--porcelain"); err == nil && strings.TrimSpace(string(out)) != "" {
		u.Modified = true
	}
	head, err := skillGitCommand(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		u.Status, u.Error = "error", firstLine(head, err)
		return u
	}
	ref := "HEAD"
	if b, err := skillGitCommand(ctx, dir, "rev-parse", "--abbrev-ref", "@{upstream}"); err == nil {
		// origin/main -> refs/heads/main
		if _, br, ok := strings.Cut(strings.TrimSpace(string(b)), "/"); ok {
			ref = "refs/heads/" + br
		}
	}
	out, err := skillGitCommand(ctx, dir, "ls-remote", "origin", ref)
	if err != nil {
		u.Status, u.Error = "error", firstLine(out, err)
		return u
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		u.Status = "missing"
		return u
	}
	u.Remote = fields[0]
	if strings.TrimSpace(string(head)) == u.Remote {
		u.Status = "current"
	} else {
		u.Status = "update"
	}
	return u
}

func firstLine(out []byte, err error) string {
	s := strings.TrimSpace(string(out))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" && err != nil {
		s = err.Error()
	}
	return s
}

// ---------------------------------------------------------------------------
// Upgrading
// ---------------------------------------------------------------------------

// SkillUpgradeRequest asks to upgrade one folder.
type SkillUpgradeRequest struct {
	Path  string `json:"path"`
	Force bool   `json:"force"` // overwrite local changes
}

// SkillUpgradeResult reports one upgrade.
type SkillUpgradeResult struct {
	Path  string `json:"path"`
	Tree  string `json:"tree"`  // the folder's tree hash after the upgrade
	Error string `json:"error"` // "" on success; no-origin, modified, mismatch, not-found, rate-limited, ...
}

// UpgradeSkills upgrades the requested folders to their source. Each one is
// checked again first; a folder with local changes is upgraded only with
// Force. Archives are downloaded once per repository.
func (c Cleaner) UpgradeSkills(ctx context.Context, reqs []SkillUpgradeRequest) ([]SkillUpgradeResult, error) {
	scan, err := c.ScanSkills(ctx)
	if err != nil {
		return nil, err
	}
	origins := skillOrigins(ctx, scan.Skills)
	cache := &treeCache{trees: map[string]*githubTree{}}
	archives := map[string]skillArchive{} // repo@ref -> extracted root and Git modes
	defer func() {
		for _, dir := range archives {
			os.RemoveAll(filepath.Dir(dir.root))
		}
	}()
	results := []SkillUpgradeResult{}
	for _, r := range reqs {
		if ctx.Err() != nil {
			return results, ctx.Err()
		}
		res := SkillUpgradeResult{Path: r.Path}
		if !filepath.IsAbs(r.Path) {
			res.Error = errSkillNotEntry.Error()
			results = append(results, res)
			continue
		}
		if err := upgradeOne(ctx, r, origins, cache, archives, &res); err != nil {
			res.Error = err.Error()
		}
		results = append(results, res)
	}
	return results, nil
}

func upgradeOne(ctx context.Context, r SkillUpgradeRequest, origins map[string]SkillOrigin, cache *treeCache, archives map[string]skillArchive, res *SkillUpgradeResult) error {
	// Origins are keyed by the resolved folder, as the scan reports it.
	folder := resolvePath(filepath.Clean(r.Path))
	o, ok := origins[folder]
	if !ok {
		return errSkillNoOrigin
	}
	// The folder must still be a skill folder Cleanzo may move.
	if err := skillFolderOK(folder, ctx); err != nil {
		return err
	}
	if skillGuard().inUse(folder) {
		return errSkillInUse
	}
	u := checkOne(ctx, folder, filepath.Base(folder), o, cache, true, skillNow().Unix())
	if u.Status == "error" {
		return errors.New(u.Error)
	}
	if u.Status == "missing" {
		return errSkillNotFound
	}
	if u.Modified && !r.Force {
		return errSkillModified
	}
	if u.Status == "current" && !u.Modified {
		res.Tree = u.Local
		return nil
	}
	if o.Kind == originGit {
		return upgradeGit(ctx, folder, r.Force, res)
	}
	archive, err := extractedRepo(ctx, o.Repo, o.Ref, archives)
	if err != nil {
		return err
	}
	src := filepath.Join(archive.root, filepath.FromSlash(o.Path))
	modes := map[string]string{}
	for name, mode := range archive.modes {
		relative, err := filepath.Rel(src, filepath.Join(archive.root, filepath.FromSlash(name)))
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			modes[filepath.ToSlash(relative)] = mode
		}
	}
	got, err := gitTreeHash(src, modes)
	if err != nil {
		return errSkillNotFound
	}
	if got != u.Remote {
		// The branch moved between listing and download, or the archive
		// lost something (empty folders, export-ignore): don't install it.
		return errSkillMismatch
	}
	if err := swapSkillFolder(folder, src); err != nil {
		return err
	}
	// From now on Cleanzo's record is the folder's origin: it knows the
	// tree the folder was installed at, which CC Switch's record does not.
	o.Tree = got
	o.Kind = originCleanzo
	if runtime.GOOS != "windows" {
		modes = nil
	}
	if err := saveSkillRecord(folder, skillRecord{Origin: o, UpdatedAt: skillNow().Unix(), Modes: modes}); err != nil {
		return err
	}
	res.Tree = got
	return nil
}

// skillFolderOK checks that folder is a real folder that is, or is linked
// from, an entry of a known skills folder, inside the cleanable home.
func skillFolderOK(folder string, contexts ...context.Context) error {
	if !platformRemovalSafe(folder, contexts...) {
		return errSkillNotEntry
	}
	info, err := os.Lstat(folder)
	if err != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return errSkillNotEntry
	}
	if skillEntryOK(folder) == nil {
		return nil
	}
	// A folder outside the skills folders (such as CC Switch's own store)
	// is fine when a skills folder links to it.
	if len(skillLinksTo(resolvePath(folder))) > 0 && safeToClean(folder) {
		return nil
	}
	return errSkillNotEntry
}

// skillArchive retains tar-recorded Git modes without trusting Windows permission bits.
type skillArchive struct {
	root  string
	modes map[string]string
}

// extractedRepo downloads and unpacks a repository archive once.
func extractedRepo(ctx context.Context, repo, ref string, cache map[string]skillArchive) (skillArchive, error) {
	key := repo + "@" + ref
	if dir, ok := cache[key]; ok {
		return dir, nil
	}
	if ref == "" {
		ref = "HEAD"
	}
	tmp, err := os.MkdirTemp("", "cleanzo-skill-")
	if err != nil {
		return skillArchive{}, err
	}
	u := fmt.Sprintf("%s/%s/tar.gz/%s", skillCodeload, repo, url.PathEscape(ref))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		os.RemoveAll(tmp)
		return skillArchive{}, err
	}
	req.Header.Set("User-Agent", "Cleanzo")
	resp, err := skillUpdateHTTP.Do(req)
	if err != nil {
		os.RemoveAll(tmp)
		return skillArchive{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		os.RemoveAll(tmp)
		return skillArchive{}, errSkillNotFound
	}
	if resp.StatusCode != http.StatusOK {
		os.RemoveAll(tmp)
		return skillArchive{}, fmt.Errorf("codeload %d", resp.StatusCode)
	}
	modes := map[string]string{}
	root, err := untarSafeModes(io.LimitReader(resp.Body, skillArchiveLimit), filepath.Join(tmp, "x"), modes)
	if err != nil {
		os.RemoveAll(tmp)
		return skillArchive{}, err
	}
	archive := skillArchive{root: root, modes: modes}
	cache[key] = archive
	return archive, nil
}

// untarSafe unpacks a GitHub archive into dest and returns its top folder.
// Entries that would land outside dest, links that point outside the
// archive, devices and other special files are refused.
func untarSafe(r io.Reader, dest string) (string, error) {
	return untarSafeModes(r, dest, nil)
}

// untarSafeModes retains executable modes from tar before Windows loses that information.
func untarSafeModes(r io.Reader, dest string, modes map[string]string) (string, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}
	tr := tar.NewReader(gz)
	top := ""
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name := path.Clean(h.Name)
		if runtime.GOOS == "windows" && (strings.ContainsAny(h.Name, `\:`) || strings.ContainsAny(h.Linkname, `\:`)) {
			return "", errBlocked
		}
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return "", fmt.Errorf("unsafe path %q", h.Name)
		}
		first := strings.SplitN(name, "/", 2)[0] // slash-ok: tar entry names always use slash-separated components
		if top == "" {
			top = first
		} else if first != top {
			return "", fmt.Errorf("unexpected entry %q", h.Name)
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return "", err
			}
			if modes != nil {
				rel := strings.TrimPrefix(name, top+"/") // slash-ok: tar and Git mode names use forward slashes
				modes[rel] = "100644"
				if h.Mode&0o111 != 0 {
					modes[rel] = "100755"
				}
			}
			mode := os.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				mode = 0o755
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return "", err
			}
			_, err = io.Copy(f, tr)
			f.Close()
			if err != nil {
				return "", err
			}
		case tar.TypeSymlink:
			link := path.Clean(path.Join(path.Dir(name), h.Linkname))
			if path.IsAbs(h.Linkname) || link == ".." || strings.HasPrefix(link, "../") {
				return "", fmt.Errorf("unsafe link %q", h.Name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return "", err
			}
			if err := makeSkillSymlink(h.Linkname, target); err != nil {
				return "", err
			}
		default:
			// Hard links, devices and FIFOs never belong in a skill.
			return "", fmt.Errorf("unsupported entry %q", h.Name)
		}
	}
	if top == "" {
		return "", errors.New("empty archive")
	}
	return filepath.Join(dest, top), nil
}

// swapSkillFolder replaces folder with a copy of src: the copy is made next
// to folder first, then folder goes to the Trash and the copy takes its
// name. If the Trash fails, folder stays as it was.
func swapSkillFolder(folder, src string) error {
	staging := filepath.Join(filepath.Dir(folder), "."+filepath.Base(folder)+".cleanzo-new")
	os.RemoveAll(staging)
	if err := copyTree(src, staging); err != nil {
		os.RemoveAll(staging)
		return err
	}
	if err := trashItem(folder); err != nil {
		os.RemoveAll(staging)
		return err
	}
	return os.Rename(staging, folder)
}

// copyTree copies a folder, keeping links as links and the executable bit.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			l, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return makeSkillSymlink(l, target)
		case info.IsDir():
			return os.MkdirAll(target, 0o755)
		default:
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			defer in.Close()
			mode := os.FileMode(0o644)
			if info.Mode()&0o111 != 0 {
				mode = 0o755
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, in)
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			return err
		}
	})
}

// upgradeGit fetches and fast-forwards a clone. With Force, local changes
// are first saved with `git stash` so they can be restored, never
// discarded. A clone whose history diverged from upstream is refused by
// the fast-forward.
func upgradeGit(ctx context.Context, dir string, force bool, res *SkillUpgradeResult) error {
	if out, err := skillGitCommand(ctx, dir, "fetch", "--quiet", "origin"); err != nil {
		return errors.New(firstLine(out, err))
	}
	if force {
		if out, err := skillGitCommand(ctx, dir, "stash", "push", "--include-untracked", "-m", "cleanzo: before upgrade"); err != nil {
			return errors.New(firstLine(out, err))
		}
	}
	if out, err := skillGitCommand(ctx, dir, "merge", "--ff-only", "@{upstream}"); err != nil {
		return errors.New(firstLine(out, err))
	}
	out, err := skillGitCommand(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	res.Tree = strings.TrimSpace(string(out))
	return nil
}

// skillSymlink lets Windows tests simulate a standard user's missing symlink privilege.
var skillSymlink = os.Symlink

// makeSkillSymlink maps Windows privilege failures to a stable code before the old copy moves.
func makeSkillSymlink(target, path string) error {
	err := skillSymlink(target, path)
	if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
		return errUnsupported
	}
	return err
}

// skillIndexModes reads tracked modes from Git's index, including executable files on Windows.
func skillIndexModes(ctx context.Context, folder string) (map[string]string, error) {
	out, err := skillGitCommand(ctx, folder, "ls-files", "--stage", "-z", "--", ".")
	if err != nil {
		return nil, err
	}
	modes := map[string]string{}
	for _, record := range strings.Split(string(out), "\x00") {
		meta, name, ok := strings.Cut(record, "\t")
		fields := strings.Fields(meta)
		if ok && len(fields) == 3 && fields[2] == "0" {
			modes[name] = fields[0]
		}
	}
	return modes, nil
}
