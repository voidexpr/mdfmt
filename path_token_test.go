package main

import (
	"regexp"
	"testing"
)

func TestGeneratePathToken(t *testing.T) {
	first, err := generatePathToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := generatePathToken()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("two generated path tokens are identical")
	}
	if matched := regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(first); !matched {
		t.Errorf("generated token = %q, want 128-bit lowercase hexadecimal", first)
	}
}

func TestValidatePathTokenOption(t *testing.T) {
	for _, accepted := range []string{pathTokenAuto, pathTokenNone, "release_2026-safe"} {
		if err := validatePathTokenOption(accepted); err != nil {
			t.Errorf("valid option %q rejected: %v", accepted, err)
		}
	}
	for _, rejected := range []string{"", "has/slash", "has space", ".", string(make([]byte, 129))} {
		if err := validatePathTokenOption(rejected); err == nil {
			t.Errorf("invalid option %q accepted", rejected)
		}
	}
}

func TestServedURL(t *testing.T) {
	if got, want := servedURL("secret", []string{"a b", "guide.md"}, false), "/secret/a%20b/guide.md"; got != want {
		t.Errorf("served URL = %q, want %q", got, want)
	}
	if got, want := servedURL("secret", nil, true), "/secret/"; got != want {
		t.Errorf("served root URL = %q, want %q", got, want)
	}
	if got, want := servedURL("", []string{"guide.md"}, false), "/guide.md"; got != want {
		t.Errorf("unprefixed URL = %q, want %q", got, want)
	}
}

func TestServePathTokenModes(t *testing.T) {
	remembered := portEntry{PathToken: tokenPointer("remembered-token")}
	if got, err := servePathToken(pathTokenAuto, remembered, true); err != nil || got != "remembered-token" {
		t.Errorf("auto remembered token = %q, %v", got, err)
	}
	if got, err := servePathToken(pathTokenNone, remembered, true); err != nil || got != "" {
		t.Errorf("none token = %q, %v", got, err)
	}
	if got, err := servePathToken("custom-token", remembered, true); err != nil || got != "custom-token" {
		t.Errorf("custom token = %q, %v", got, err)
	}
	generated, err := servePathToken(pathTokenAuto, portEntry{PathToken: tokenPointer("")}, true)
	if err != nil {
		t.Fatal(err)
	}
	if matched := regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(generated); !matched {
		t.Errorf("auto after none generated %q", generated)
	}
}
