package openrouter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// Options controls how trending models are fetched from OpenRouter.
type Options struct {
	CacheDir  string
	CacheTTL  time.Duration
	FreeCount int
	PaidCount int
	SourceURL string
	Period    string
	Category  string
	UserAgent string
}

// Result contains the ordered free/paid model ids.
type Result struct {
	Free        []string
	Paid        []string
	Source      string
	RetrievedAt time.Time
	FromCache   bool
}

const (
	defaultCacheFilename = "openrouter_rankings_cache.json"
	defaultPeriod        = "week"
	defaultCacheTTL      = 6 * time.Hour
	defaultFreeCount     = 10
	defaultPaidCount     = 10
)

type cachePayload struct {
	RetrievedAt time.Time `json:"retrieved_at"`
	Source      string    `json:"source"`
	Models      []string  `json:"models"`
}

// FetchTrending models with caching and several fallback sources. The function
// never returns duplicate ids inside Free or Paid slices.
func FetchTrending(ctx context.Context, opts Options) (Result, error) {
	if opts.CacheDir == "" {
		return Result{}, errors.New("CacheDir is required")
	}

	if err := os.MkdirAll(opts.CacheDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create cache dir: %w", err)
	}

	if opts.CacheTTL <= 0 {
		opts.CacheTTL = defaultCacheTTL
	}
	if opts.FreeCount <= 0 {
		opts.FreeCount = defaultFreeCount
	}
	if opts.PaidCount <= 0 {
		opts.PaidCount = defaultPaidCount
	}
	if opts.Period == "" {
		opts.Period = defaultPeriod
	}

	cachePath := filepath.Join(opts.CacheDir, defaultCacheFilename)
	if res, ok := loadCache(cachePath, opts.CacheTTL, opts.FreeCount, opts.PaidCount); ok {
		res.FromCache = true
		return res, nil
	}

	models, source, err := downloadRankings(ctx, opts)
	if err != nil {
		return Result{}, err
	}
	models = filterModels(models)

	now := time.Now().UTC()
	_ = saveCache(cachePath, cachePayload{
		RetrievedAt: now,
		Source:      source,
		Models:      models,
	})

	free, paid := partitionModels(models, opts.FreeCount, opts.PaidCount)
	return Result{
		Free:        free,
		Paid:        paid,
		Source:      source,
		RetrievedAt: now,
	}, nil
}

func loadCache(path string, ttl time.Duration, freeCount, paidCount int) (Result, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Result{}, false
	}
	var payload cachePayload
	if err := json.Unmarshal(b, &payload); err != nil {
		return Result{}, false
	}
	if payload.RetrievedAt.IsZero() || time.Since(payload.RetrievedAt) > ttl {
		return Result{}, false
	}
	models := filterModels(payload.Models)
	free, paid := partitionModels(models, freeCount, paidCount)
	return Result{
		Free:        free,
		Paid:        paid,
		Source:      payload.Source,
		RetrievedAt: payload.RetrievedAt,
	}, true
}

func saveCache(path string, payload cachePayload) error {
	tmp := path + ".tmp"
	payload.Models = filterModels(payload.Models)
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func downloadRankings(ctx context.Context, opts Options) ([]string, string, error) {
	sources := buildSources(opts)
	var lastErr error
	for _, src := range sources {
		models, err := fetchAndExtract(ctx, src, opts.UserAgent)
		if err != nil {
			lastErr = err
			continue
		}
		if len(models) == 0 {
			lastErr = fmt.Errorf("zero models from %s", src)
			continue
		}
		return models, src, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no sources attempted")
	}
	return nil, "", lastErr
}

func buildSources(opts Options) []string {
	baseSources := []string{
		"https://openrouter.ai/api/rankings/models",
		"https://openrouter.ai/api/rankings",
		"https://openrouter.ai/rankings.json",
		"https://openrouter.ai/rankings",
		"https://openrouter.wk-xj.com/rankings.json",
		"https://gist.githubusercontent.com/sportsculture/a8f3bac998db4178457d3bd9f0a0d705/raw/openrouter-rankings.json",
	}
	out := make([]string, 0, len(baseSources)+1)
	if strings.TrimSpace(opts.SourceURL) != "" {
		out = append(out, strings.TrimSpace(opts.SourceURL))
	}

	query := url.Values{}
	if opts.Period != "" {
		query.Set("period", opts.Period)
	}
	if opts.Category != "" {
		query.Set("category", opts.Category)
	}

	for _, base := range baseSources {
		if strings.TrimSpace(base) == "" {
			continue
		}
		next := base
		if len(query) > 0 {
			if strings.Contains(base, "?") {
				next = base + "&" + query.Encode()
			} else {
				next = base + "?" + query.Encode()
			}
		}
		out = append(out, next)
	}

	seen := map[string]struct{}{}
	deduped := make([]string, 0, len(out))
	for _, src := range out {
		s := strings.TrimSpace(src)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		deduped = append(deduped, s)
	}
	return deduped
}

func fetchAndExtract(ctx context.Context, src string, userAgent string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return nil, err
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	} else {
		req.Header.Set("User-Agent", "dv/ccr-trending (+https://github.com/discourse/dv)")
	}
	req.Header.Set("Accept", "application/json, text/html;q=0.8")
	req.Header.Set("Referer", "https://github.com/discourse/dv")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, src)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return extractModelIDs(body), nil
}

func extractModelIDs(body []byte) []string {
	var data interface{}
	if err := json.Unmarshal(body, &data); err == nil {
		return collectModelIDs(data)
	}
	// Fallback: attempt to treat payload as plain text.
	text := string(body)
	lines := strings.Split(text, "\n")
	ids := make([]string, 0, len(lines))
	seen := map[string]struct{}{}
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.Contains(ln, " ") {
			continue
		}
		if !strings.Contains(ln, "/") {
			continue
		}
		if _, ok := seen[ln]; ok {
			continue
		}
		seen[ln] = struct{}{}
		ids = append(ids, ln)
	}
	return ids
}

func collectModelIDs(v interface{}) []string {
	out := []string{}
	seen := map[string]struct{}{}
	var walk func(any)
	walk = func(node any) {
		switch t := node.(type) {
		case map[string]any:
			for k, val := range t {
				lk := strings.ToLower(k)
				switch lk {
				case "model", "model_id", "modelid", "slug", "id":
					if s, ok := val.(string); ok {
						addModel(&out, seen, s)
					}
				}
				walk(val)
			}
		case []any:
			for _, item := range t {
				walk(item)
			}
		case string:
			// Some arrays may just be string ids; accept them directly.
			addModel(&out, seen, t)
		}
	}
	walk(v)
	return out
}

func addModel(dest *[]string, seen map[string]struct{}, candidate string) {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return
	}
	if !strings.Contains(candidate, "/") {
		// Not an OpenRouter model id.
		return
	}
	if !isLikelyModelID(candidate) {
		return
	}
	if _, ok := seen[candidate]; ok {
		return
	}
	seen[candidate] = struct{}{}
	*dest = append(*dest, candidate)
}

func partitionModels(models []string, freeCount, paidCount int) ([]string, []string) {
	isFree := func(id string) bool {
		lower := strings.ToLower(id)
		return strings.Contains(lower, ":free")
	}
	free := make([]string, 0, freeCount)
	paid := make([]string, 0, paidCount)
	freeSeen := map[string]struct{}{}
	paidSeen := map[string]struct{}{}

	for _, id := range models {
		if len(free) >= freeCount && len(paid) >= paidCount {
			break
		}
		if strings.TrimSpace(id) == "" || !isLikelyModelID(id) {
			continue
		}
		if isFree(id) {
			if len(free) >= freeCount {
				continue
			}
			if _, ok := freeSeen[id]; ok {
				continue
			}
			freeSeen[id] = struct{}{}
			free = append(free, id)
		} else {
			if len(paid) >= paidCount {
				continue
			}
			if _, ok := paidSeen[id]; ok {
				continue
			}
			paidSeen[id] = struct{}{}
			paid = append(paid, id)
		}
	}
	return free, paid
}

func filterModels(models []string) []string {
	out := make([]string, 0, len(models))
	seen := map[string]struct{}{}
	for _, id := range models {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if !isLikelyModelID(id) {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// ResetCache removes the cached rankings file if it exists.
func ResetCache(cacheDir string) error {
	if strings.TrimSpace(cacheDir) == "" {
		return errors.New("cacheDir is empty")
	}
	path := filepath.Join(cacheDir, defaultCacheFilename)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return os.Remove(path)
}

func isLikelyModelID(id string) bool {
	if strings.ContainsAny(id, "<>\"' \t\r\n") {
		return false
	}
	parts := strings.Split(id, "/")
	if len(parts) != 2 {
		return false
	}
	if !isValidSegment(parts[0], false) {
		return false
	}
	if !isValidSegment(parts[1], true) {
		return false
	}
	return true
}

func isValidSegment(seg string, allowColon bool) bool {
	if seg == "" {
		return false
	}
	for i, r := range seg {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		switch r {
		case '-', '_', '.':
			continue
		case ':':
			if allowColon && i != 0 {
				continue
			}
		}
		return false
	}
	return true
}
