package main

import (
	"strings"
	"testing"
)

func TestRunVersionPrintsRevisionAndGoVersion(t *testing.T) {
	oldVersion := buildVersion
	oldCommit := buildCommit
	oldDate := buildDate
	t.Cleanup(func() {
		buildVersion = oldVersion
		buildCommit = oldCommit
		buildDate = oldDate
	})
	buildVersion = "test-version"
	buildCommit = "abc1234"
	buildDate = "2026-06-13T16:00:00+08:00"

	out := captureStdout(t, func() {
		if err := run([]string{"version"}); err != nil {
			t.Fatalf("run version: %v", err)
		}
	})

	for _, want := range []string{
		"version: test-version\n",
		"commit: abc1234\n",
		"buildDate: 2026-06-13T16:00:00+08:00\n",
		"go: ",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("version output = %q, want %q", out, want)
		}
	}
}
