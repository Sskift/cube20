package usage

import (
	"bufio"
	"encoding/json"
	"os"
)

// Buffer sizes for the per-line JSONL scanner. A single rollout line can be
// large (a full token_count payload), so we start at 64 KiB and allow up to
// 4 MiB before bufio reports bufio.ErrTooLong.
const (
	scanInitBuf = 64 << 10 // 64 KiB initial buffer
	scanMaxBuf  = 4 << 20  // 4 MiB max token size
)

// scanJSONL is the shared engine for reading a rollout *.jsonl file line by
// line. It opens filePath, scans each line, applies prefilter (when non-nil,
// lines for which it returns false are skipped before any JSON work), unmarshals
// each surviving line into a map and invokes onObj for it. Malformed JSON lines
// are skipped leniently (never fatal), matching the historical behavior of both
// call sites.
//
// A missing/unreadable file is not an error (returns nil) so callers can keep
// scanning the remaining files. Crucially it returns scanner.Err() at the end:
// when a line exceeds scanMaxBuf the scanner stops early with bufio.ErrTooLong,
// and surfacing that lets callers detect a truncated scan instead of silently
// dropping every later record in the file.
func scanJSONL(filePath string, prefilter func(line string) bool, onObj func(obj map[string]any)) error {
	file, err := os.Open(filePath)
	if err != nil {
		// Open errors (missing file, permissions) are intentionally ignored so
		// one bad file does not abort scanning the rest of the corpus.
		return nil
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, scanInitBuf), scanMaxBuf)

	for scanner.Scan() {
		line := scanner.Text()
		if prefilter != nil && !prefilter(line) {
			continue
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			continue
		}
		onObj(value)
	}
	// Surface ErrTooLong (or any read error) so callers can tell a truncated
	// scan from a clean one.
	return scanner.Err()
}
