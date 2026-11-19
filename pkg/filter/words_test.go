package filter

import (
	"errors"
	"strings"
	"testing"

	"github.com/ffuf/ffuf/v2/pkg/ffuf"
)

func TestNewWordFilter(t *testing.T) {
	f, _ := NewWordFilter("200,301,400-410,500")
	wordsRepr := f.Repr()
	if !strings.Contains(wordsRepr, "200,301,400-410,500") {
		t.Errorf("Word filter was expected to have 4 values")
	}
}

func TestNewWordFilterError(t *testing.T) {
	_, err := NewWordFilter("invalid")
	if err == nil {
		t.Errorf("Was expecting an error from errenous input data")
	}
}

func TestWordFiltering(t *testing.T) {
	f, _ := NewWordFilter("200,301,402-450,500")
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
		resp := ffuf.Response{Data: []byte(strings.Join(data, " "))}
		filterReturn, _ := f.Filter(&resp)
		if filterReturn != test.output {
			t.Errorf("Filter test %d: Was expecing filter return value of %t but got %t", i, test.output, filterReturn)
		}
	}
}

// scanWordResponse is the word-count counterpart of scanResponse: a captured body
// of exactly words words plus the recorded count.
func scanWordResponse(words int64) *ffuf.Response {
	data := make([]string, words)
	for i := range data {
		data[i] = "A"
	}
	return &ffuf.Response{
		Data:          []byte(strings.Join(data, " ")),
		ContentWords:  words,
		ContentLength: 4096, // decoy: the length is not the word count
	}
}

// refreshWordProbe is the word-count counterpart of refreshProbe: recorded count
// only, no body (plus the decoy length a real collected result also carries).
func refreshWordProbe(words int64) *ffuf.Response {
	return &ffuf.Response{ContentWords: words, ContentLength: 4096}
}

// TestWordFilterScanAndRefreshAgree mirrors TestLineFilterScanAndRefreshAgree for
// the word count: same response, same thresholds, scan path (body) and refresh path
// (recorded count) must reach the same boolean.
func TestWordFilterScanAndRefreshAgree(t *testing.T) {
	const words = 7
	for _, tc := range []struct {
		threshold string
		want      bool
	}{
		{"7", true},
		{"6", false},
		{"8", false},
		{"1", false},
		{"1-10", true},
		{"6-7", true},
		{"4096", false}, // decoy: the recorded ContentLength must not be scored as words
	} {
		f, _ := NewWordFilter(tc.threshold)
		scan, err := f.Filter(scanWordResponse(words))
		if err != nil {
			t.Fatalf("threshold %s: scan path returned error: %v", tc.threshold, err)
		}
		refresh, err := f.Filter(refreshWordProbe(words))
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

// TestWordFilterRefreshWithoutBodyRefuses is the -ignore-body counterpart for words.
func TestWordFilterRefreshWithoutBodyRefuses(t *testing.T) {
	for _, threshold := range []string{"1", "0", "1-100", "12"} {
		f, _ := NewWordFilter(threshold)
		hit, err := f.Filter(&ffuf.Response{})
		if !errors.Is(err, ErrCountUnavailable) {
			t.Errorf("threshold %s: expected ErrCountUnavailable, got err=%v (hit=%t)", threshold, err, hit)
		}
		if hit {
			t.Errorf("threshold %s: a body-less, count-less response must not be judged a hit", threshold)
		}
	}
}
