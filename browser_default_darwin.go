//go:build darwin

package main

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
)

func defaultBrowserIsChrome(targetURL string) bool {
	parsed, err := url.Parse(targetURL)
	if err != nil || parsed.Scheme == "" {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	preferences := filepath.Join(
		home,
		"Library",
		"Preferences",
		"com.apple.LaunchServices",
		"com.apple.launchservices.secure.plist",
	)
	output, err := exec.Command(
		"/usr/bin/plutil",
		"-extract", "LSHandlers",
		"json",
		"-o", "-",
		preferences,
	).Output()
	return err == nil && chromeIsDefaultForScheme(output, parsed.Scheme)
}
