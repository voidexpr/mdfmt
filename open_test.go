package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseOpenFlagsAllowsInterspersedPaths(t *testing.T) {
	cfg, err := parseOpenFlags([]string{
		"one.md",
		"--bind", "127.0.0.2",
		"two.md",
		"--port", "8642",
		"--root", "./docs",
		"--path-token", "release-token",
		"--chrome",
		"--print-only",
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.paths, ",") != "one.md,two.md" ||
		cfg.bind != "127.0.0.2" ||
		cfg.port != 8642 ||
		!cfg.portSet ||
		cfg.root != "./docs" ||
		cfg.pathToken != "release-token" ||
		!cfg.chrome ||
		!cfg.printOnly {
		t.Errorf("parseOpenFlags() = %#v", cfg)
	}
}

func TestChromeArgumentsFocusServedURL(t *testing.T) {
	targetURL := "http://127.0.0.1:8642/Guide%20one.md"
	got := chromeArguments(targetURL)
	want := []string{
		"--focus=http://127.0.0.1:8642/Guide%20one.md/*",
		targetURL,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("chromeArguments() = %q, want %q", got, want)
	}
}

func TestParseServeFlagsDistinguishesOmittedAndExplicitZeroPort(t *testing.T) {
	omitted, err := parseServeFlags(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if omitted.portSet {
		t.Fatal("omitted --port was marked explicit")
	}

	explicit, err := parseServeFlags([]string{"--port", "0"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !explicit.portSet || explicit.port != 0 {
		t.Errorf("explicit --port 0 parsed as %#v", explicit)
	}
}

func TestOpenURLsUseLongestRootAndEscapeComponents(t *testing.T) {
	parent := t.TempDir()
	outer := filepath.Join(parent, "docs")
	inner := filepath.Join(outer, "private")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	outerFile := writeTestFile(t, outer, "Overview.md", "# Overview\n")
	innerFile := writeTestFile(t, inner, "Guide #1.md", "# Guide\n")
	resolvedOuter, _, err := resolveExisting(outer)
	if err != nil {
		t.Fatal(err)
	}
	resolvedInner, _, err := resolveExisting(inner)
	if err != nil {
		t.Fatal(err)
	}
	registry := portsFile{
		Version: portsFileVersion,
		Roots: map[string]portEntry{
			resolvedOuter: {Port: 8101, PathToken: tokenPointer("outer-token")},
			resolvedInner: {Port: 8102, PathToken: tokenPointer("inner-token")},
		},
	}
	cfg := openConfig{
		paths:     []string{outerFile, innerFile, inner},
		bind:      "127.0.0.2",
		pathToken: pathTokenAuto,
	}

	urls, err := openURLs(cfg, registry)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"http://127.0.0.2:8101/outer-token/Overview.md",
		"http://127.0.0.2:8102/inner-token/Guide%20%231.md",
		"http://127.0.0.2:8102/inner-token/",
	}
	if strings.Join(urls, "\n") != strings.Join(want, "\n") {
		t.Errorf("URLs:\n%s\nwant:\n%s", strings.Join(urls, "\n"), strings.Join(want, "\n"))
	}
}

func TestOpenURLsAllowForcedUnregisteredRootWithPort(t *testing.T) {
	root := t.TempDir()
	source := writeTestFile(t, root, "Read me.md", "# Read me\n")
	urls, err := openURLs(openConfig{
		paths:     []string{source},
		root:      root,
		port:      9000,
		portSet:   true,
		bind:      defaultBind,
		pathToken: pathTokenNone,
	}, portsFile{Version: portsFileVersion, Roots: map[string]portEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := urls[0], "http://127.0.0.1:9000/Read%20me.md"; got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
}

func TestOpenURLsRejectUnmatchedAndUnsupportedPaths(t *testing.T) {
	root := t.TempDir()
	source := writeTestFile(t, root, "notes.txt", "notes\n")
	registry := portsFile{
		Version: portsFileVersion,
		Roots:   map[string]portEntry{root: {Port: 8101}},
	}
	if _, err := openURLs(openConfig{
		paths: []string{source},
		bind:  defaultBind,
	}, registry); err == nil || !strings.Contains(err.Error(), "not a directory or Markdown file") {
		t.Fatalf("unsupported path error = %v", err)
	}

	outside := writeTestFile(t, t.TempDir(), "outside.md", "# Outside\n")
	if _, err := openURLs(openConfig{
		paths: []string{outside},
		bind:  defaultBind,
	}, registry); err == nil || !strings.Contains(err.Error(), "no configured mdfmt root") {
		t.Fatalf("unmatched path error = %v", err)
	}
}

func TestRunOpenPrintOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	source := writeTestFile(t, root, "Guide.md", "# Guide\n")
	resolvedRoot, _, err := resolveExisting(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := writePortsFile(filepath.Join(home, ".mdfmt", "ports.json"), portsFile{
		Version: portsFileVersion,
		Roots:   map[string]portEntry{resolvedRoot: {Port: 8642, PathToken: tokenPointer("remembered-token")}},
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"open", source, "--print-only"}, &stdout, &stderr); err != nil {
		t.Fatalf("run open: %v; stderr=%s", err, stderr.String())
	}
	if got, want := stdout.String(), "http://127.0.0.1:8642/remembered-token/Guide.md\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunOpenWithRootAndPortDoesNotRequireRegistry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".mdfmt"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(home, ".mdfmt", "ports.json"),
		[]byte("not valid JSON"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	source := writeTestFile(t, root, "Guide.md", "# Guide\n")

	var stdout, stderr bytes.Buffer
	if err := run([]string{
		"open",
		source,
		"--root", root,
		"--port", "8642",
		"--path-token", "none",
		"--print-only",
	}, &stdout, &stderr); err != nil {
		t.Fatalf("run open: %v; stderr=%s", err, stderr.String())
	}
	if got, want := stdout.String(), "http://127.0.0.1:8642/Guide.md\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestOpenAutoRequiresRememberedPathToken(t *testing.T) {
	root := t.TempDir()
	source := writeTestFile(t, root, "Guide.md", "# Guide\n")
	resolvedRoot, _, err := resolveExisting(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = openURLs(openConfig{
		paths:     []string{source},
		bind:      defaultBind,
		pathToken: pathTokenAuto,
	}, portsFile{
		Version: portsFileVersion,
		Roots:   map[string]portEntry{resolvedRoot: {Port: 8642}},
	})
	if err == nil || !strings.Contains(err.Error(), "no remembered path token") {
		t.Fatalf("missing token error = %v", err)
	}
}

func TestOpenNoneOverridesRememberedToken(t *testing.T) {
	root := t.TempDir()
	source := writeTestFile(t, root, "Guide.md", "# Guide\n")
	resolvedRoot, _, err := resolveExisting(root)
	if err != nil {
		t.Fatal(err)
	}
	urls, err := openURLs(openConfig{
		paths:     []string{source},
		bind:      defaultBind,
		pathToken: pathTokenNone,
	}, portsFile{
		Version: portsFileVersion,
		Roots:   map[string]portEntry{resolvedRoot: {Port: 8642, PathToken: tokenPointer("remembered")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := urls[0], "http://127.0.0.1:8642/Guide.md"; got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
}

func TestOpenAutoReusesRememberedNone(t *testing.T) {
	root := t.TempDir()
	source := writeTestFile(t, root, "Guide.md", "# Guide\n")
	resolvedRoot, _, err := resolveExisting(root)
	if err != nil {
		t.Fatal(err)
	}
	urls, err := openURLs(openConfig{
		paths:     []string{source},
		bind:      defaultBind,
		pathToken: pathTokenAuto,
	}, portsFile{
		Version: portsFileVersion,
		Roots:   map[string]portEntry{resolvedRoot: {Port: 8642, PathToken: tokenPointer("")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := urls[0], "http://127.0.0.1:8642/Guide.md"; got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
}
