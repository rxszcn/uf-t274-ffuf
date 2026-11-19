package filter

import (
	"bytes"
	"errors"

	"github.com/ffuf/ffuf/v2/pkg/ffuf"
)

// ErrCountUnavailable reports that a response's line or word count cannot be
// resolved. Callers must treat the response as "not judgeable" and keep it, never
// as a count: the whole reason this is an error instead of a number is to stop a
// body-less response from being scored as "one line" / "one word".
var ErrCountUnavailable = errors.New("response body was not captured (-ignore-body) and no collected line/word count is recorded")

var (
	lineSeparator = []byte("\n")
	wordSeparator = []byte(" ")
)

// ResponseLineCount and ResponseWordCount are the single judgement source for the
// line and word counts. Both paths that decide over a response resolve the number
// here, and only here:
//
//   - the scan path, where LineFilter/WordFilter see the live *ffuf.Response with
//     the captured body in Data;
//   - the interactive refresh path, where they see a probe rebuilt from a collected
//     result (pkg/interactive resultProbe) that carries only the counts recorded at
//     collection time.
//
// Resolution order:
//
//  1. A captured body (Data != nil) wins. The count is taken from the same bytes the
//     collector saw, and the separator is counted with bytes.Count(...)+1, which
//     equals len(strings.Split(...)) for a single-byte separator without
//     allocating a []string over the whole body (see runner.SimpleRunner.Execute).
//     A captured empty body counts as one line / one word, exactly as strings.Split
//     yields.
//  2. Without a body, a recorded count (> 0) is reused as-is. A line or word count
//     is always >= 1 for a captured body, so 0 is unambiguously "not recorded"; no
//     other field - ContentLength in particular - is ever substituted for it.
//  3. Otherwise ErrCountUnavailable. That is the -ignore-body case: the body was
//     never captured, so the count is unknown. Returning an error (instead of a
//     fabricated 1, which is what counting separators in an empty body used to
//     yield) keeps such responses from being filtered or matched by accident, and
//     lets the interactive refresh refuse explicitly instead of silently guessing.
func ResponseLineCount(response *ffuf.Response) (int64, error) {
	if response == nil {
		return 0, ErrCountUnavailable
	}
	return responseCount(response, lineSeparator, response.ContentLines)
}

// ResponseWordCount is the word-count counterpart of ResponseLineCount. See there
// for the resolution order.
func ResponseWordCount(response *ffuf.Response) (int64, error) {
	if response == nil {
		return 0, ErrCountUnavailable
	}
	return responseCount(response, wordSeparator, response.ContentWords)
}

// responseCount implements the single resolution rule shared by the line and word
// counts, so the scan path and the refresh path cannot diverge.
func responseCount(response *ffuf.Response, separator []byte, recorded int64) (int64, error) {
	if response.Data != nil {
		return int64(bytes.Count(response.Data, separator)) + 1, nil
	}
	if recorded > 0 {
		return recorded, nil
	}
	return 0, ErrCountUnavailable
}
