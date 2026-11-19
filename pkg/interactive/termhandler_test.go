package interactive

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ffuf/ffuf/v2/pkg/engine"
	"github.com/ffuf/ffuf/v2/pkg/ffuf"
	"github.com/ffuf/ffuf/v2/pkg/filter"
	"github.com/ffuf/ffuf/v2/pkg/output"
)

// TestResultProbeUsesLineCount guards the fl-filter bug: the probe rebuilt to
// re-evaluate filters against an already-collected result must take ContentLines
// from the result's line count, not its length. Before the fix an in-console
// "fl" (line count) filter compared against ContentLength.
func TestResultProbeUsesLineCount(t *testing.T) {
	res := ffuf.Result{
		StatusCode:    200,
		ContentLength: 4096,
		ContentWords:  100,
		ContentLines:  7,
	}

	probe := resultProbe(res)

	if probe.ContentLines != 7 {
		t.Errorf("resultProbe ContentLines = %d, want 7 (fed ContentLength instead of ContentLines)", probe.ContentLines)
	}
	if probe.ContentLength != 4096 {
		t.Errorf("resultProbe ContentLength = %d, want 4096", probe.ContentLength)
	}
	if probe.ContentWords != 100 {
		t.Errorf("resultProbe ContentWords = %d, want 100", probe.ContentWords)
	}
	if probe.StatusCode != 200 {
		t.Errorf("resultProbe StatusCode = %d, want 200", probe.StatusCode)
	}
}

// recordingOutput wraps the real Stdoutput so the tests can assert on the error
// message an explicit refusal prints, while CurrentResults and
// FilterCurrentResults keep their production behaviour.
type recordingOutput struct {
	*output.Stdoutput
	mu     sync.Mutex
	errors []string
}

func (o *recordingOutput) Error(msg string) {
	o.mu.Lock()
	o.errors = append(o.errors, msg)
	o.mu.Unlock()
}

func (o *recordingOutput) errMessages() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]string, len(o.errors))
	copy(out, o.errors)
	return out
}

// refreshHarness wires the minimal Job an interactive refresh needs: a real
// MatcherManager for the filters and a real Stdoutput holding the collected
// results (so FilterCurrentResults keeps its locked read-filter-write semantics).
func refreshHarness(t *testing.T, results ...ffuf.Result) (*interactive, *recordingOutput, ffuf.MatcherManager) {
	t.Helper()
	mm := filter.NewMatcherManager()
	conf := &ffuf.Config{MatcherManager: mm, FilterMode: "or"}
	rec := &recordingOutput{Stdoutput: output.NewStdoutput(conf)}
	rec.SetCurrentResults(results)
	job := &engine.Job{Config: conf, Output: rec}
	return &interactive{Job: job}, rec, mm
}

func collected(url string, lines, words, length int64) ffuf.Result {
	return ffuf.Result{Url: url, StatusCode: 200, ContentLines: lines, ContentWords: words, ContentLength: length}
}

func keptURLs(results []ffuf.Result) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.Url)
	}
	return out
}

// TestRefreshMatchesScanPathForLineFilter locks the fix's core invariant: a result
// held in the console must be judged exactly as the scan path judged it. All three
// collected results have 7 lines; the decoy ContentLength is 4096, so a refresh
// that re-derived lines from the summary would behave completely differently.
//
// With fl=7 the 7-line result is dropped and the 6/8-line ones survive. With fl=1
// nothing is dropped (every count is > 1). Before the fix the refresh probe had no
// body, the line filter counted the empty body as one line, so fl=7 dropped nothing
// and fl=1 dropped everything - exactly the reported behaviour.
func TestRefreshMatchesScanPathForLineFilter(t *testing.T) {
	results := []ffuf.Result{
		collected("http://x/six", 6, 3, 4096),
		collected("http://x/seven", 7, 3, 4096),
		collected("http://x/eight", 8, 3, 4096),
	}

	i, rec, mm := refreshHarness(t, results...)
	if err := mm.AddFilter("line", "7", false); err != nil {
		t.Fatalf("AddFilter: %v", err)
	}
	i.refreshResults()

	got := keptURLs(rec.GetCurrentResults())
	want := []string{"http://x/six", "http://x/eight"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("after fl=7 kept %v, want %v", got, want)
	}
	if errs := rec.errMessages(); len(errs) != 0 {
		t.Errorf("unexpected refusal for a judgeable refresh: %v", errs)
	}

	// Raising the threshold to 1 must keep everything: no 7-line result is a 1-line
	// result. This is the case that used to clear the whole result set.
	i, rec, mm = refreshHarness(t, results...)
	if err := mm.AddFilter("line", "1", false); err != nil {
		t.Fatalf("AddFilter: %v", err)
	}
	i.refreshResults()
	if got := rec.GetCurrentResults(); len(got) != 3 {
		t.Errorf("after fl=1 kept %d results %v, want all 3", len(got), keptURLs(got))
	}
}

// TestRefreshMatchesScanPathForWordFilter is the word-count counterpart.
func TestRefreshMatchesScanPathForWordFilter(t *testing.T) {
	results := []ffuf.Result{
		collected("http://x/six", 3, 6, 4096),
		collected("http://x/seven", 3, 7, 4096),
		collected("http://x/eight", 3, 8, 4096),
	}

	i, rec, mm := refreshHarness(t, results...)
	if err := mm.AddFilter("word", "7", false); err != nil {
		t.Fatalf("AddFilter: %v", err)
	}
	i.refreshResults()

	got := keptURLs(rec.GetCurrentResults())
	want := []string{"http://x/six", "http://x/eight"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("after fw=7 kept %v, want %v", got, want)
	}

	i, rec, mm = refreshHarness(t, results...)
	if err := mm.AddFilter("word", "1", false); err != nil {
		t.Fatalf("AddFilter: %v", err)
	}
	i.refreshResults()
	if got := rec.GetCurrentResults(); len(got) != 3 {
		t.Errorf("after fw=1 kept %d results %v, want all 3", len(got), keptURLs(got))
	}
}

// TestRefreshBoundaryThresholdAgreesWithScanPath pins the boundary: a threshold
// exactly equal to the recorded count is a hit on both paths (the result is
// filtered out of the refresh view, matching what the scan path would have done).
func TestRefreshBoundaryThresholdAgreesWithScanPath(t *testing.T) {
	results := []ffuf.Result{collected("http://x/boundary", 7, 7, 4096)}

	for _, tc := range []struct {
		name, value string
		dropped     bool
	}{
		{"line", "7", true},
		{"line", "6", false},
		{"line", "8", false},
		{"word", "7", true},
		{"word", "6", false},
		{"word", "8", false},
	} {
		i, rec, mm := refreshHarness(t, results...)
		if err := mm.AddFilter(tc.name, tc.value, false); err != nil {
			t.Fatalf("AddFilter(%s,%s): %v", tc.name, tc.value, err)
		}
		i.refreshResults()
		isDropped := len(rec.GetCurrentResults()) == 0
		if isDropped != tc.dropped {
			t.Errorf("%s=%s: dropped=%t, want %t", tc.name, tc.value, isDropped, tc.dropped)
		}
	}
}

// TestRefreshDoesNotRescoreFromSummary is the "no re-derivation from a summary
// field" assertion: ContentLength (4096) must never be treated as the line or word
// count. The discriminating pair is fl=4096 (must keep - 4096 is the length, not a
// count) against fl=7 (must drop - 7 is the recorded count). A refresh that
// re-derived the count from ContentLength keeps both; one that counts the absent
// body keeps both for the wrong reason; only the shared judgement source drops
// exactly the second.
func TestRefreshDoesNotRescoreFromSummary(t *testing.T) {
	results := []ffuf.Result{collected("http://x/seven", 7, 7, 4096)}

	i, rec, mm := refreshHarness(t, results...)
	if err := mm.AddFilter("line", "4096", false); err != nil {
		t.Fatalf("AddFilter: %v", err)
	}
	i.refreshResults()
	if got := rec.GetCurrentResults(); len(got) != 1 {
		t.Errorf("fl=4096 dropped the result: the refresh re-derived lines from ContentLength instead of the recorded count")
	}

	i, rec, mm = refreshHarness(t, results...)
	if err := mm.AddFilter("line", "7", false); err != nil {
		t.Fatalf("AddFilter: %v", err)
	}
	i.refreshResults()
	if got := rec.GetCurrentResults(); len(got) != 0 {
		t.Errorf("fl=7 kept the result: the refresh did not use the recorded line count")
	}

	i, rec, mm = refreshHarness(t, results...)
	if err := mm.AddFilter("word", "4096", false); err != nil {
		t.Fatalf("AddFilter: %v", err)
	}
	i.refreshResults()
	if got := rec.GetCurrentResults(); len(got) != 1 {
		t.Errorf("fw=4096 dropped the result: the refresh re-derived words from ContentLength instead of the recorded count")
	}

	i, rec, mm = refreshHarness(t, results...)
	if err := mm.AddFilter("word", "7", false); err != nil {
		t.Fatalf("AddFilter: %v", err)
	}
	i.refreshResults()
	if got := rec.GetCurrentResults(); len(got) != 0 {
		t.Errorf("fw=7 kept the result: the refresh did not use the recorded word count")
	}
}

// TestRefreshRefusesWhenCountUnavailable is the -ignore-body case. Such results are
// collected with no body and no recorded count (only the server-declared length),
// so the refresh cannot judge them. It must refuse the whole refresh with a readable
// error and leave the collected results untouched - neither clearing them (the old
// "count the empty body as 1" behaviour) nor pretending they all match.
func TestRefreshRefusesWhenCountUnavailable(t *testing.T) {
	results := []ffuf.Result{
		collected("http://x/a", 0, 0, 1234),
		collected("http://x/b", 0, 0, 1234),
	}

	for _, tc := range []struct{ name, value string }{
		{"line", "1"},
		{"word", "1"},
		{"line", "12"},
		{"word", "1234"},
	} {
		i, rec, mm := refreshHarness(t, results...)
		if err := mm.AddFilter(tc.name, tc.value, false); err != nil {
			t.Fatalf("AddFilter(%s,%s): %v", tc.name, tc.value, err)
		}
		i.refreshResults()

		if got := len(rec.GetCurrentResults()); got != 2 {
			t.Errorf("%s=%s: refresh changed CurrentResults to %d, want it left at 2", tc.name, tc.value, got)
		}
		errs := rec.errMessages()
		if len(errs) == 0 {
			t.Fatalf("%s=%s: refresh silently succeeded over unjudgeable results", tc.name, tc.value)
		}
		msg := errs[0]
		if !strings.Contains(msg, "Cannot refresh results") || !strings.Contains(msg, tc.name) {
			t.Errorf("%s=%s: refusal message is not actionable: %q", tc.name, tc.value, msg)
		}
	}
}

// TestRefreshWithoutBodyIsSafeOnEmptySet is the empty-set case: with no collected
// results a refresh must be a no-op even when the active line/word filter could not
// be evaluated over any result. Nothing to judge means nothing to refuse.
func TestRefreshWithoutBodyIsSafeOnEmptySet(t *testing.T) {
	i, rec, mm := refreshHarness(t)
	if err := mm.AddFilter("line", "1", false); err != nil {
		t.Fatalf("AddFilter: %v", err)
	}
	i.refreshResults()
	if got := rec.GetCurrentResults(); len(got) != 0 {
		t.Errorf("empty-set refresh produced %d results", len(got))
	}
	if errs := rec.errMessages(); len(errs) != 0 {
		t.Errorf("empty-set refresh reported an error: %v", errs)
	}
}

// TestRefreshTimeFilterUsesObservedDuration keeps the same-semantics promise for
// the time filter: the probe carries the duration the result was collected with, so
// a "ft" threshold judges the same number on both paths instead of a zero stand-in.
func TestRefreshTimeFilterUsesObservedDuration(t *testing.T) {
	slow := ffuf.Result{Url: "http://x/slow", StatusCode: 200, Duration: 500 * time.Millisecond}
	fast := ffuf.Result{Url: "http://x/fast", StatusCode: 200, Duration: 10 * time.Millisecond}

	for _, tc := range []struct {
		value string
		kept  []string
	}{
		{">100", []string{"http://x/fast"}},
		{"<100", []string{"http://x/slow"}},
	} {
		i, rec, mm := refreshHarness(t, slow, fast)
		if err := mm.AddFilter("time", tc.value, false); err != nil {
			t.Fatalf("AddFilter(time,%s): %v", tc.value, err)
		}
		i.refreshResults()
		if got := strings.Join(keptURLs(rec.GetCurrentResults()), ","); got != strings.Join(tc.kept, ",") {
			t.Errorf("ft%s: kept %s, want %s", tc.value, got, strings.Join(tc.kept, ","))
		}
	}
}

// TestRefreshNonCountFiltersStillApply confirms the refusal is scoped to the count
// filters: status/size filters over collected results keep working unchanged, which
// is why fs/fc/ft were reported healthy while fl/fw were not.
func TestRefreshNonCountFiltersStillApply(t *testing.T) {
	results := []ffuf.Result{
		{Url: "http://x/200", StatusCode: 200, ContentLength: 10},
		{Url: "http://x/404", StatusCode: 404, ContentLength: 20},
	}
	i, rec, mm := refreshHarness(t, results...)
	if err := mm.AddFilter("status", "404", false); err != nil {
		t.Fatalf("AddFilter: %v", err)
	}
	i.refreshResults()
	got := keptURLs(rec.GetCurrentResults())
	if len(got) != 1 || got[0] != "http://x/200" {
		t.Errorf("after fc=404 kept %v, want [http://x/200]", got)
	}
	if errs := rec.errMessages(); len(errs) != 0 {
		t.Errorf("status filter refresh should not refuse: %v", errs)
	}
}
