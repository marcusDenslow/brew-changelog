// Command gen-sources walks every formula reachable via
// `brew info --json=v2 --eval-all`, resolves each to a GitHub repo, probes
// the repo for an in-tree changelog file via the authenticated GitHub
// Contents API, and writes internal/sourcedb/sources.json. Each entry
// stores the literal URL the runtime should fetch plus a "kind" tag so it
// knows how to parse the response.
//
// Auth: token is read from `gh auth token` at startup. The probe uses the
// authenticated 5000/hr core budget — raw.githubusercontent.com anonymous
// probes are NOT used (we exhaust them in minutes).
//
// Usage:
//
//	go run ./cmd/gen-sources                     # full: resolve + probe + write
//	go run ./cmd/gen-sources -report-only        # don't write, print coverage
//	go run ./cmd/gen-sources -no-probe           # skip probe; everything → kind=releases
//	go run ./cmd/gen-sources -workers 8          # tune probe concurrency
//	go run ./cmd/gen-sources -out other.json     # write somewhere else
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marcusDenslow/brew-changelog/internal/sourcedb"
)

var githubRE = regexp.MustCompile(`github\.com/([^/]+)/([^/]+)`)

// changelogCandidatesRoot are root-of-repo filenames we accept, in
// preference order. The Contents API returns the full root in one call;
// we intersect that against this set and pick the first hit.
//
// docs/CHANGELOG variants are NOT probed — they were ~1.5% of historical
// hits and each would cost a second API call per repo. Trade-off favors
// staying inside the 5000/hr budget.
var changelogCandidatesRoot = []string{
	"CHANGELOG.md",
	"CHANGELOG",
	"CHANGES.md",
	"CHANGES",
	"HISTORY.md",
	"HISTORY",
	"NEWS.md",
	"NEWS",
}

const probeUserAgent = "brew-changelog-gen-sources (+https://github.com/marcusDenslow/brew-changelog)"

// repoRef is the intermediate (name, owner, repo) tuple held only during
// generation. It's discarded once the final sourcedb.Source is emitted.
type repoRef struct {
	name  string
	owner string
	repo  string
}

func main() {
	reportOnly := flag.Bool("report-only", false, "print coverage but do not write file")
	out := flag.String("out", "internal/sourcedb/sources.json", "output path for sources map")
	topN := flag.Int("top", 15, "show this many top unresolved hosts")
	noProbe := flag.Bool("no-probe", false, "skip the changelog-file probe pass")
	workers := flag.Int("workers", 4, "concurrent probe workers (auth API is rate-limit constrained)")
	probeTimeout := flag.Duration("probe-timeout", 10*time.Second, "per-request timeout for probes")
	limit := flag.Int("limit", 0, "probe at most N github repos (0 = all); useful for smoke tests")
	flag.Parse()

	fmt.Fprintln(os.Stderr, "running `brew info --json=v2 --eval-all` (slow, ~30s)…")
	raw, err := exec.Command("brew", "info", "--json=v2", "--eval-all").Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "brew info: %v\n", err)
		os.Exit(1)
	}

	var resp struct {
		Formulae []struct {
			Name     string `json:"name"`
			Homepage string `json:"homepage"`
			URLs     struct {
				Stable struct {
					URL string `json:"url"`
				} `json:"stable"`
				Head struct {
					URL string `json:"url"`
				} `json:"head"`
			} `json:"urls"`
		} `json:"formulae"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "parse brew info json: %v\n", err)
		os.Exit(1)
	}

	refs := make(map[string]repoRef, len(resp.Formulae))
	for _, f := range resp.Formulae {
		for _, candidate := range []string{f.Homepage, f.URLs.Head.URL, f.URLs.Stable.URL} {
			m := githubRE.FindStringSubmatch(candidate)
			if m == nil {
				continue
			}
			repo := strings.TrimSuffix(m[2], ".git")
			repo = strings.TrimSuffix(repo, "/")
			refs[f.Name] = repoRef{name: f.Name, owner: m[1], repo: repo}
			break
		}
	}

	total := len(resp.Formulae)
	resolved := len(refs)
	fmt.Fprintf(os.Stderr, "\nresolved: %d / %d formulae (%.1f%%)\n", resolved, total, pctOf(resolved, total))

	printUnresolvedHosts(resp.Formulae, refs, *topN)

	// fileURLs maps formula name → raw URL when a changelog file was found.
	fileURLs := map[string]string{}
	if !*noProbe {
		probeRefs := refs
		if *limit > 0 && *limit < len(refs) {
			probeRefs = pickN(refs, *limit)
			fmt.Fprintf(os.Stderr, "\nlimit=%d → probing only the first %d github repos\n", *limit, len(probeRefs))
		}
		fileURLs = probeAll(probeRefs, *workers, *probeTimeout)
	}

	// Materialize final entries: file hit → kind=file, miss → kind=releases.
	sources := make(map[string]sourcedb.Source, len(refs))
	for name, r := range refs {
		if url, ok := fileURLs[name]; ok {
			sources[name] = sourcedb.Source{Kind: "file", URL: url}
		} else {
			sources[name] = sourcedb.Source{
				Kind: "releases",
				URL:  fmt.Sprintf("https://api.github.com/repos/%s/%s/releases", r.owner, r.repo),
			}
		}
	}

	printKindStats(sources)

	if *reportOnly {
		return
	}

	if err := writeSortedJSON(*out, sources); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", *out, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "\nwrote %s (%d entries)\n", *out, len(sources))
}

// authToken lifts the GitHub token from the gh CLI. Required for the
// Contents API probe path — anonymous raw.githubusercontent.com is rate-
// limited per IP and we will exhaust it on a single run.
func authToken() (string, error) {
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return "", fmt.Errorf("gh auth token failed (run `gh auth login` first): %w", err)
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" {
		return "", fmt.Errorf("gh auth token returned empty — not logged in")
	}
	return tok, nil
}

// probeAll calls api.github.com/repos/{owner}/{repo}/contents for every
// github-mapped formula and returns formula → raw markdown URL for any
// repo whose root contains a known changelog filename. One auth'd request
// per repo. Watches X-RateLimit-Remaining and parks workers when budget
// gets thin.
func probeAll(refs map[string]repoRef, workers int, perReqTimeout time.Duration) map[string]string {
	token, err := authToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "auth: %v\n", err)
		os.Exit(1)
	}

	client := &http.Client{Timeout: perReqTimeout}

	type result struct {
		name string
		url  string
	}
	jobs := make(chan repoRef, workers*2)
	results := make(chan result, workers*2)

	var (
		wg         sync.WaitGroup
		hits       atomic.Int64
		done       atomic.Int64
		errors     atomic.Int64
		hitsByPath sync.Map // path → *atomic.Int64
	)
	addHit := func(path string) {
		v, _ := hitsByPath.LoadOrStore(path, new(atomic.Int64))
		v.(*atomic.Int64).Add(1)
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				rawURL, path, sleepFor, errored := probeContents(client, token, j.owner, j.repo)
				if errored {
					errors.Add(1)
				}
				if rawURL != "" {
					results <- result{name: j.name, url: rawURL}
					hits.Add(1)
					addHit(path)
				}
				done.Add(1)
				if sleepFor > 0 {
					time.Sleep(sleepFor)
				}
			}
		}()
	}

	totalJobs := len(refs)
	fmt.Fprintf(os.Stderr, "\nprobing %d github repos via Contents API (workers=%d)…\n", totalJobs, workers)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				fmt.Fprintf(os.Stderr, "  progress: %d/%d probed, %d hits, %d errors\n",
					done.Load(), totalJobs, hits.Load(), errors.Load())
			}
		}
	}()

	go func() {
		for _, r := range refs {
			jobs <- r
		}
		close(jobs)
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	fileURLs := make(map[string]string, totalJobs/3)
	for r := range results {
		fileURLs[r.name] = r.url
	}
	cancel()

	fmt.Fprintf(os.Stderr, "\nprobe done: %d hits, %d errors (of %d)\n", hits.Load(), errors.Load(), totalJobs)

	type pathCount struct {
		path string
		n    int64
	}
	var counts []pathCount
	hitsByPath.Range(func(k, v any) bool {
		counts = append(counts, pathCount{k.(string), v.(*atomic.Int64).Load()})
		return true
	})
	sort.Slice(counts, func(i, j int) bool { return counts[i].n > counts[j].n })
	fmt.Fprintln(os.Stderr, "\nchangelog path distribution:")
	for _, c := range counts {
		fmt.Fprintf(os.Stderr, "  %5d  %s\n", c.n, c.path)
	}
	return fileURLs
}

// probeContents fetches the repo's root directory listing in one call and
// returns the URL of the first matching changelog filename. sleepFor is
// non-zero when the remaining rate budget dropped below the safety
// threshold — the caller is expected to honor it before issuing more
// requests. errored is true for any non-200, non-404 response (404 means
// "repo missing/private" — expected, not an error).
func probeContents(client *http.Client, token, owner, repo string) (rawURL, path string, sleepFor time.Duration, errored bool) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents", owner, repo)
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return "", "", 0, true
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", probeUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return "", "", 0, true
	}
	defer resp.Body.Close()

	sleepFor = budgetSleep(resp)

	switch resp.StatusCode {
	case http.StatusOK:
		// fall through
	case http.StatusNotFound, http.StatusForbidden:
		// 404 = repo missing/private (skip silently)
		// 403 with X-RateLimit-Remaining=0 = throttled, handled by sleepFor
		return "", "", sleepFor, false
	default:
		return "", "", sleepFor, true
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", sleepFor, true
	}
	var items []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &items); err != nil {
		return "", "", sleepFor, true
	}

	present := map[string]bool{}
	for _, item := range items {
		if item.Type == "file" {
			present[item.Name] = true
		}
	}
	for _, candidate := range changelogCandidatesRoot {
		if present[candidate] {
			return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/HEAD/%s", owner, repo, candidate), candidate, sleepFor, false
		}
	}
	return "", "", sleepFor, false
}

// budgetSleep returns how long the caller should sleep before issuing
// another auth'd request. When X-RateLimit-Remaining drops below the
// safety threshold (50), wait until X-RateLimit-Reset + a small buffer so
// the window has actually flipped.
func budgetSleep(resp *http.Response) time.Duration {
	remStr := resp.Header.Get("X-RateLimit-Remaining")
	resetStr := resp.Header.Get("X-RateLimit-Reset")
	if remStr == "" || resetStr == "" {
		return 0
	}
	rem, err := strconv.Atoi(remStr)
	if err != nil || rem >= 50 {
		return 0
	}
	resetUnix, err := strconv.ParseInt(resetStr, 10, 64)
	if err != nil {
		return 0
	}
	resetAt := time.Unix(resetUnix, 0).Add(10 * time.Second)
	wait := time.Until(resetAt)
	if wait <= 0 {
		return 0
	}
	fmt.Fprintf(os.Stderr, "  rate budget thin (remaining=%d), sleeping %s\n", rem, wait.Round(time.Second))
	return wait
}

func printKindStats(sources map[string]sourcedb.Source) {
	var file, releases, other int
	for _, s := range sources {
		switch s.Kind {
		case "file":
			file++
		case "releases":
			releases++
		default:
			other++
		}
	}
	total := len(sources)
	fmt.Fprintf(os.Stderr, "\nkind distribution (of %d entries):\n", total)
	fmt.Fprintf(os.Stderr, "  %5d  file      (%.1f%%)\n", file, pctOf(file, total))
	fmt.Fprintf(os.Stderr, "  %5d  releases  (%.1f%%)\n", releases, pctOf(releases, total))
	if other > 0 {
		fmt.Fprintf(os.Stderr, "  %5d  (other)\n", other)
	}
}

func printUnresolvedHosts(formulae []struct {
	Name     string `json:"name"`
	Homepage string `json:"homepage"`
	URLs     struct {
		Stable struct {
			URL string `json:"url"`
		} `json:"stable"`
		Head struct {
			URL string `json:"url"`
		} `json:"head"`
	} `json:"urls"`
}, refs map[string]repoRef, topN int) {
	missCounts := map[string]int{}
	for _, f := range formulae {
		if _, ok := refs[f.Name]; ok {
			continue
		}
		host := hostOf(f.Homepage)
		if host == "" {
			host = "(no homepage)"
		}
		missCounts[host]++
	}
	type hostStat struct {
		Host string
		N    int
	}
	stats := make([]hostStat, 0, len(missCounts))
	for h, n := range missCounts {
		stats = append(stats, hostStat{h, n})
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].N > stats[j].N })

	fmt.Fprintf(os.Stderr, "\ntop %d unresolved hosts:\n", topN)
	for i, s := range stats {
		if i >= topN {
			break
		}
		fmt.Fprintf(os.Stderr, "  %5d  %s\n", s.N, s.Host)
	}
}

// writeSortedJSON encodes m with keys in lexical order so re-runs produce
// stable diffs.
func writeSortedJSON(path string, m map[string]sourcedb.Source) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("{\n")
	for i, k := range keys {
		entry, err := json.Marshal(m[k])
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "  %q: %s", k, entry)
		if i < len(keys)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("}\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func hostOf(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	s := rawURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimPrefix(s, "www.")
}

// pickN returns the first N entries from refs in name-sorted order so the
// limited subset is deterministic across runs (useful for smoke tests).
func pickN(refs map[string]repoRef, n int) map[string]repoRef {
	names := make([]string, 0, len(refs))
	for k := range refs {
		names = append(names, k)
	}
	sort.Strings(names)
	if n > len(names) {
		n = len(names)
	}
	out := make(map[string]repoRef, n)
	for _, k := range names[:n] {
		out[k] = refs[k]
	}
	return out
}

func pctOf(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return 100.0 * float64(part) / float64(total)
}
