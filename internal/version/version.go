// Copyright 2026 The go-ethereum Authors
// This file is part of go-ethereum.

package version

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"unicode"
)

var (
	// Version information set via linker flags.
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

var semverRE = regexp.MustCompile(`(?:^|[^0-9])([0-9]+)\.([0-9]+)\.([0-9]+)`)

// Print writes version information in the Morph CLI format.
func Print(w io.Writer, name string) {
	if w == nil {
		w = os.Stdout
	}
	fmt.Fprintf(w, "%s %s\n", name, Version)
	fmt.Fprintf(w, "Git Commit: %s\n", GitCommit)
	fmt.Fprintf(w, "Build Time: %s\n", BuildTime)
	fmt.Fprintf(w, "Go Version: %s\n", runtime.Version())
	fmt.Fprintf(w, "OS/Arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
}

// PrintIfRequested prints the version when a standard-library flag was set.
func PrintIfRequested(showVersion *bool, name string) bool {
	if showVersion == nil || !*showVersion {
		return false
	}
	Print(os.Stdout, name)
	return true
}

// SemverParts extracts major, minor and patch from a Morph git-describe string.
func SemverParts() (int, int, int) {
	return SemverPartsFor(Version)
}

// SemverPartsFor extracts major, minor and patch from a version string.
func SemverPartsFor(v string) (int, int, int) {
	match := semverRE.FindStringSubmatch(v)
	if len(match) != 4 {
		return 0, 0, 0
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	patch, _ := strconv.Atoi(match[3])
	return major, minor, patch
}

// SemverCode returns the compact version code used in miner extra data.
func SemverCode() uint {
	major, minor, patch := SemverParts()
	return uint(major<<16 | minor<<8 | patch)
}

// BaseSemverFor returns the numeric semver prefix, or 0.0.0 if none is present.
func BaseSemverFor(v string) string {
	major, minor, patch := SemverPartsFor(v)
	return fmt.Sprintf("%d.%d.%d", major, minor, patch)
}

// DebianVersionFor converts a Morph version into a Debian-compatible version.
func DebianVersionFor(v string) string {
	v = strings.TrimPrefix(v, "morph-v")
	if v == "" || v == "dev" {
		return BaseSemverFor(v)
	}
	v = strings.Map(func(r rune) rune {
		switch {
		case r == '-' || r == '_':
			return '+'
		case unicode.IsLetter(r), unicode.IsDigit(r), strings.ContainsRune(".+:~", r):
			return r
		default:
			return '+'
		}
	}, v)
	if v == "" || !unicode.IsDigit([]rune(v)[0]) {
		v = BaseSemverFor(v) + "+" + v
	}
	return v
}

// NSISVersionPartsFor returns major, minor and patch strings for NSIS metadata.
func NSISVersionPartsFor(v string) (string, string, string) {
	major, minor, patch := SemverPartsFor(v)
	return strconv.Itoa(major), strconv.Itoa(minor), strconv.Itoa(patch)
}
