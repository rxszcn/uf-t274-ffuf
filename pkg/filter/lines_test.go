package filter

import (
	"errors"
	"strings"
	"testing"

	"github.com/ffuf/ffuf/v2/pkg/ffuf"
)

func TestNewLineFilter(t *testing.T) {
	f, _ := NewLineFilter("200,301,400-410,500")
	linesRepr := f.Repr()
	if !strings.Contains(linesRepr, "200,301,400-410,500") {
		t.Errorf("Word filter was expected to have 4 values")
	}
}

func TestNewLineFilterError(t *testing.T) {
	_, err := NewLineFilter("invalid")
	if err == nil {
		t.Errorf("Was expecting an error from errenous input data")
	}
}

func TestLineFiltering(t *testing.T) {
	f, _ := NewLineFilter("200,301,402-450,500")
	for i, test := range []struct {
		input  int64
		output bool
	}{
		{200, true},
		{301, true},
		{500, true},
		{4, false},
		{444, true},
		{302, false},
		{401, false},
		{402, true},
		{450, true},
		{451, false},
	} {
		var data []string
		for i := int64(0); i < test.input; i++ {
			data = append(data, "A")
		}
		resp := ffuf.Response{Data: []byte(strings.Join(data, "\n"))}
		filterReturn, _ := f.Filter(&resp)
		if filterReturn != test.output {
			t.Errorf("Filter test %d: Was expecing filter return value of %t but got %t", i, test.output, filterReturn)
		}
	}
}

// scanResponse builds the response a scan-path line filter sees: a captured body
// of exactly lines lines (no trailing newline, matching the runner's count) plus
// the count the collector records alongside it.
func scanResponse(lines int64) *ffuf.Response {
	data := make([]string, lines)
	for i := range data {
		data[i] = "A"
	}
	return &ffuf.Response{
		Data:          []byte(strings.Join(data, "\n")),
		ContentLines:  lines,
		ContentLength: 4096, // decoy: the length is not the line count
	}
}

// refreshProbe builds the response the interactive refresh path sees for the same
// collected result: no body at all, only the recorded count (plus the decoy length
// a real collected result also carries).
func refreshProbe(lines int64) *ffuf.Response {
	return &ffuf.Response{ContentLines: lines, ContentLength: 4096}
}

// TestLineFilterScanAndRefreshAgree pins the scan path and the interactive refresh
// path to the same judgement for the same response: the scan side sees the body,
// the refresh side sees only the recorded count, and both must return the same
// boolean for every threshold. This is the invariant the two diverging
// implementations used to break (a 7-line response filtered as 12 by the refresh
// path, which substituted ContentLength for the line count).
func TestLineFilterScanAndRefreshAgree(t *testing.T) {
	const lines = 7
	for _, tc := range []struct {
		threshold string
		want      bool
	}{
		{"7", true},     // exactly the boundary -> hit
		{"6", false},    // one below -> miss
		{"8", false},    // one above -> miss
		{"1", false},    // far below -> miss (the refresh path used to clear everything)
		{"1-10", true},  // range covering the count
		{"6-7", true},   // range whose upper bound is the boundary
		{"4096", false}, // decoy: the recorded ContentLength must not be scored as lines
	} {
		f, _ := NewLineFilter(tc.threshold)
		scan, err := f.Filter(scanResponse(lines))
		if err != nil {
			t.Fatalf("threshold %s: scan path returned error: %v", tc.threshold, err)
		}
		refresh, err := f.Filter(refreshProbe(lines))
		if err != nil {
			t.Fatalf("threshold %s: refresh path returned error: %v", tc.threshold, err)
		}
		if scan != tc.want {
			t.Errorf("threshold %s: scan path = %t, want %t", tc.threshold, scan, tc.want)
		}
		if refresh != scan {
			t.Errorf("threshold %s: refresh path = %t but scan path = %t (paths disagree)", tc.threshold, refresh, scan)
		}
	}
}

// TestLineFilterRefreshWithoutBodyRefuses is the -ignore-body case: a result that
// has no body and no recorded line count cannot be judged. The filter must say so
// with an error instead of the old behaviour, which counted separators in the empty
// body and scored every such result as one line.
func TestLineFilterRefreshWithoutBodyRefuses(t *testing.T) {
	for _, threshold := range []string{"1", "0", "1-100", "12"} {
		f, _ := NewLineFilter(threshold)
		hit, err := f.Filter(&ffuf.Response{})
		if !errors.Is(err, ErrCountUnavailable) {
			t.Errorf("threshold %s: expected ErrCountUnavailable, got err=%v (hit=%t)", threshold, err, hit)
		}
		if hit {
			t.Errorf("threshold %s: a body-less, count-less response must not be judged a hit", threshold)
		}
	}
}

// TestLineFilterScanCountWinsOverRecorded surfaces the resolution order: a live
// body is authoritative even if the recorded count disagrees, because the body is
// what the collector would have counted.
func TestLineFilterScanCountWinsOverRecorded(t *testing.T) {
	resp := scanResponse(7)
	resp.ContentLines = 99
	f, _ := NewLineFilter("7")
	if hit, err := f.Filter(resp); err != nil || !hit {
		t.Errorf("a captured 7-line body must be judged 7 (got hit=%t, err=%v)", hit, err)
	}

	// A captured empty body is one line, matching strings.Split("", "\n").
	empty := &ffuf.Response{Data: []byte{}, ContentLines: 99}
	one, _ := NewLineFilter("1")
	if hit, err := one.Filter(empty); err != nil || !hit {
		t.Errorf("a captured empty body must be judged 1 line (got hit=%t, err=%v)", hit, err)
	}
}
