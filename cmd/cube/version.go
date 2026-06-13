package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

var (
	buildVersion = "dev"
	buildCommit  = ""
	buildDate    = ""
)

func printVersion() error {
	info := versionInfo()
	fmt.Printf("version: %s\n", info.Version)
	fmt.Printf("commit: %s\n", emptyDash(info.Commit))
	fmt.Printf("buildDate: %s\n", emptyDash(info.BuildDate))
	fmt.Printf("go: %s %s/%s\n", info.GoVersion, info.GOOS, info.GOARCH)
	return nil
}

type cubeVersionInfo struct {
	Version   string
	Commit    string
	BuildDate string
	GoVersion string
	GOOS      string
	GOARCH    string
}

func versionInfo() cubeVersionInfo {
	info := cubeVersionInfo{
		Version:   firstNonEmptyString(buildVersion, "dev"),
		Commit:    strings.TrimSpace(buildCommit),
		BuildDate: strings.TrimSpace(buildDate),
		GoVersion: runtime.Version(),
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
	}
	if build, ok := debug.ReadBuildInfo(); ok {
		if info.Version == "dev" && strings.TrimSpace(build.Main.Version) != "" && build.Main.Version != "(devel)" {
			info.Version = build.Main.Version
		}
		for _, setting := range build.Settings {
			switch setting.Key {
			case "vcs.revision":
				if info.Commit == "" {
					info.Commit = shortRevision(setting.Value)
				}
			case "vcs.time":
				if info.BuildDate == "" {
					info.BuildDate = setting.Value
				}
			}
		}
	}
	return info
}

func shortRevision(revision string) string {
	revision = strings.TrimSpace(revision)
	if len(revision) <= 12 {
		return revision
	}
	return revision[:12]
}
