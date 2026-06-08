package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeRawLines writes literal lines (each gets a trailing '\n') so tests can
// craft malformed JSON and lines larger than the scanner buffer.
func writeRawLines(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var buf []byte
	for _, line := range lines {
		buf = append(buf, line...)
		buf = append(buf, '\n')
	}
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// FIX 3: parseTime must accept RFC3339 strings AND numeric unix timestamps.
func TestParseTimeAcceptsStringAndNumeric(t *testing.T) {
	want := time.Unix(1780000000, 0)

	rfc := want.UTC().Format(time.RFC3339Nano)
	if got := parseTime(rfc); !got.Equal(want) {
		t.Errorf("parseTime(RFC3339 %q) = %v, want %v", rfc, got, want)
	}

	if got := parseTime(float64(1780000000)); !got.Equal(want) {
		t.Errorf("parseTime(float64) = %v, want %v", got, want)
	}

	if got := parseTime(json.Number("1780000000")); !got.Equal(want) {
		t.Errorf("parseTime(json.Number) = %v, want %v", got, want)
	}

	if got := parseTime(int64(1780000000)); !got.Equal(want) {
		t.Errorf("parseTime(int64) = %v, want %v", got, want)
	}

	if got := parseTime("not-a-time"); !got.IsZero() {
		t.Errorf("parseTime(garbage) = %v, want zero", got)
	}
	if got := parseTime(nil); !got.IsZero() {
		t.Errorf("parseTime(nil) = %v, want zero", got)
	}
}

// FIX 1/2: scanJSONL must surface scanner.Err() (e.g. bufio.ErrTooLong) instead
// of silently stopping the loop, and a record after an oversized line must not be
// silently dropped without signal. This drives both the shared engine extraction
// and the error-checking fix.
func TestScanJSONLSurfacesOversizedLine(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "rollout-oversized.jsonl")

	// One huge line bigger than scanMaxBuf, then a valid record after it.
	huge := `{"pad":"` + strings.Repeat("x", scanMaxBuf+1024) + `"}`
	good := `{"marker":"after"}`
	writeRawLines(t, path, []string{huge, good})

	var seenAfter bool
	err := scanJSONL(path, nil, func(obj map[string]any) {
		if obj["marker"] == "after" {
			seenAfter = true
		}
	})

	// The current behavior silently swallows the truncation. After the fix the
	// engine must either deliver the later record OR return a non-nil error.
	if err == nil && !seenAfter {
		t.Fatalf("oversized line silently dropped the trailing record: err=%v seenAfter=%v", err, seenAfter)
	}
}

// scanJSONL applies the prefilter and skips lines that don't match, parses each
// matching line, and skips malformed JSON leniently.
func TestScanJSONLPrefilterAndLenientParse(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "rollout-mixed.jsonl")
	writeRawLines(t, path, []string{
		`{"keep":1,"tag":"yes"}`,
		`{"tag":"no"}`,                 // filtered out by prefilter
		`{"keep":2,"tag":"yes" BROKEN`, // malformed -> skipped, not fatal
		`{"keep":3,"tag":"yes"}`,
	})

	var keeps []float64
	err := scanJSONL(path,
		func(line string) bool { return strings.Contains(line, `"yes"`) },
		func(obj map[string]any) {
			if v, ok := obj["keep"].(float64); ok {
				keeps = append(keeps, v)
			}
		},
	)
	if err != nil {
		t.Fatalf("scanJSONL returned error: %v", err)
	}
	if len(keeps) != 2 || keeps[0] != 1 || keeps[1] != 3 {
		t.Fatalf("keeps = %v, want [1 3]", keeps)
	}
}

func TestScanJSONLMissingFileNoError(t *testing.T) {
	err := scanJSONL(filepath.Join(t.TempDir(), "does-not-exist.jsonl"), nil, func(map[string]any) {})
	if err != nil {
		t.Fatalf("scanJSONL(missing file) = %v, want nil (open errors are ignored)", err)
	}
}
