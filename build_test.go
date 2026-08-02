package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseBuildFlags(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    buildConfig
		wantErr string
	}{
		{
			name: "single source with flags after",
			args: []string{"docs", "-o", "site", "--path-token", "token", "--strict", "-q"},
			want: buildConfig{source: "docs", output: "site", pathToken: "token", strict: true, quiet: true},
		},
		{
			name: "collection",
			args: []string{"--mount", "a=/one", "--mount", "nested/b=/two=three", "-o", "site", "--strict-no-dir-index"},
			want: buildConfig{mounts: []string{"a=/one", "nested/b=/two=three"}, output: "site", pathToken: pathTokenNone, strictNoDirIndex: true},
		},
		{name: "missing output", args: []string{"docs"}, wantErr: "--output is required"},
		{name: "mixed forms", args: []string{"docs", "--mount", "a=/one", "-o", "site"}, wantErr: "cannot be combined"},
		{name: "missing source", args: []string{"-o", "site"}, wantErr: "SOURCE_DIR"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseBuildFlags(test.args, io.Discard)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want mention of %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.source != test.want.source || got.output != test.want.output || got.pathToken != test.want.pathToken || got.strict != test.want.strict || got.strictNoDirIndex != test.want.strictNoDirIndex || got.quiet != test.want.quiet || strings.Join(got.mounts, "\x00") != strings.Join(test.want.mounts, "\x00") {
				t.Errorf("config = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestBuildSingleSourceSite(t *testing.T) {
	setBuildTemp(t)
	source := t.TempDir()
	writeTestFile(t, source, "README.md", "# Home\n\n[Guide](guide.md)\n")
	writeTestFile(t, source, "guide.md", "# Guide\n\n[Home](/README.md)\n\n[Plans](/plans/)\n")
	writeTestFile(t, filepath.Join(source, "plans"), "index.md", "# Plans\n\n[Guide](/guide.md)\n")
	writeTestFile(t, filepath.Join(source, "plans"), "detail.markdown", "# Detail\n")
	target := filepath.Join(t.TempDir(), "site")

	output, err := buildStaticSite(buildConfig{source: source, output: target, pathToken: pathTokenNone}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	resolvedTarget, err := resolveProspectivePath(target)
	if err != nil {
		t.Fatal(err)
	}
	if output != resolvedTarget {
		t.Fatalf("output = %q, want %q", output, resolvedTarget)
	}
	for _, name := range []string{
		".mdfmt", "index.html", "README.html", "guide.html", "plans/index.html", "plans/detail.html",
		"_mdfmt/style.css", "_mdfmt/app.js", "_mdfmt/favicon.svg",
	} {
		if _, err := os.Stat(filepath.Join(target, filepath.FromSlash(name))); err != nil {
			t.Errorf("missing generated %s: %v", name, err)
		}
	}
	rootPage := mustRead(t, filepath.Join(target, "index.html"))
	if !strings.Contains(rootPage, `href="README.html"`) || !strings.Contains(rootPage, `href="plans/index.html"`) {
		t.Errorf("root directory links were not materialized:\n%s", rootPage)
	}
	guide := mustRead(t, filepath.Join(target, "guide.html"))
	for _, want := range []string{`href="README.html"`, `href="plans/index.html"`, `href="_mdfmt/style.css"`, `http-equiv="Content-Security-Policy"`, `name="referrer" content="no-referrer"`} {
		if !strings.Contains(guide, want) {
			t.Errorf("guide does not contain %q:\n%s", want, guide)
		}
	}
	plans := mustRead(t, filepath.Join(target, "plans", "index.html"))
	if !strings.Contains(plans, `<h1 id="plans">Plans</h1>`) || strings.Contains(plans, `index.md</a>`) {
		t.Errorf("index.md was not used as the directory landing page:\n%s", plans)
	}
}

func TestBuildTokenizedCollection(t *testing.T) {
	setBuildTemp(t)
	first := t.TempDir()
	second := t.TempDir()
	writeTestFile(t, first, "README.md", "# Alpha\n")
	writeTestFile(t, second, "notes.md", "# Research\n")
	target := filepath.Join(t.TempDir(), "site")
	token := "shared-secret"

	output, err := buildStaticSite(buildConfig{
		mounts: []string{"a=" + first, "work/research=" + second}, output: target, pathToken: token,
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	resolvedTarget, err := resolveProspectivePath(target)
	if err != nil {
		t.Fatal(err)
	}
	if output != filepath.Join(resolvedTarget, token) {
		t.Fatalf("output = %q", output)
	}
	if _, err := os.Stat(filepath.Join(target, "index.html")); !os.IsNotExist(err) {
		t.Fatalf("tokenless root index exists or stat failed unexpectedly: %v", err)
	}
	for _, name := range []string{"index.html", "a/index.html", "a/README.html", "work/research/index.html", "work/research/notes.html", "_mdfmt/style.css"} {
		if _, err := os.Stat(filepath.Join(output, filepath.FromSlash(name))); err != nil {
			t.Errorf("missing collection output %s: %v", name, err)
		}
	}
	hub := mustRead(t, filepath.Join(output, "index.html"))
	if strings.Contains(hub, token) || !strings.Contains(hub, `href="a/index.html"`) || !strings.Contains(hub, `href="work/research/index.html"`) {
		t.Errorf("collection hub has incorrect links or leaked token:\n%s", hub)
	}
	document := mustRead(t, filepath.Join(output, "work", "research", "notes.html"))
	for _, want := range []string{`href="../../_mdfmt/style.css"`, `href="../../index.html"`, `href="../../a/index.html"`, `>a/</a>`, `>work/research/</a>`} {
		if !strings.Contains(document, want) {
			t.Errorf("nested collection page does not contain %q:\n%s", want, document)
		}
	}
	if strings.Contains(document, token) || strings.Contains(document, first) || strings.Contains(document, second) {
		t.Errorf("collection page leaked private path information:\n%s", document)
	}
}

func TestBuildRewritesLinksAndCopiesImages(t *testing.T) {
	setBuildTemp(t)
	source := t.TempDir()
	writeTestFile(t, source, "README.md", "# Home\n")
	writeTestFile(t, filepath.Join(source, "guide"), "page.md", "# Page\n\n[Home](/README.md?view=1#home)\n\n![Logo](../media/logo.png)\n\n<a href=\"/README.md\">Raw home</a>\n<a href=\"/media/logo.png\"><img src=\"/media/logo.png\" alt=\"Logo\"></a>\n")
	image := []byte("\x89PNG\r\n\x1a\nnot-a-real-image-but-static")
	if err := os.MkdirAll(filepath.Join(source, "media"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "media", "logo.png"), image, 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "site")

	if _, err := buildStaticSite(buildConfig{source: source, output: target, pathToken: pathTokenNone}, io.Discard); err != nil {
		t.Fatal(err)
	}
	document := mustRead(t, filepath.Join(target, "guide", "page.html"))
	for _, want := range []string{`href="../README.html?view=1#home"`, `href="../README.html"`, `href="../_mdfmt/media/`, `src="../_mdfmt/media/`} {
		if !strings.Contains(document, want) {
			t.Errorf("rewritten document does not contain %q:\n%s", want, document)
		}
	}
	media, err := filepath.Glob(filepath.Join(target, "_mdfmt", "media", "*.png"))
	if err != nil || len(media) != 1 {
		t.Fatalf("generated media = %v, err = %v", media, err)
	}
	if got, err := os.ReadFile(media[0]); err != nil || !bytes.Equal(got, image) {
		t.Fatalf("copied image differs: err=%v content=%q", err, got)
	}
}

func TestBuildTargetOwnershipAndReplacement(t *testing.T) {
	setBuildTemp(t)
	source := t.TempDir()
	writeTestFile(t, source, "one.md", "# One\n")
	target := t.TempDir()
	writeTestFile(t, target, "personal.txt", "keep")

	if _, err := buildStaticSite(buildConfig{source: source, output: target, pathToken: pathTokenNone}, io.Discard); err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("unowned target error = %v", err)
	}
	if got := mustRead(t, filepath.Join(target, "personal.txt")); got != "keep" {
		t.Fatalf("unowned target was modified: %q", got)
	}

	owned := filepath.Join(t.TempDir(), "owned")
	if _, err := buildStaticSite(buildConfig{source: source, output: owned, pathToken: "first"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(source, "one.md")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, source, "two.md", "# Two\n")
	if _, err := buildStaticSite(buildConfig{source: source, output: owned, pathToken: "second"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(owned, "first")); !os.IsNotExist(err) {
		t.Fatalf("stale token directory remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(owned, "second", "two.html")); err != nil {
		t.Fatal(err)
	}
	if marker := mustRead(t, filepath.Join(owned, ".mdfmt")); marker != buildMarker || strings.Contains(marker, "first") || strings.Contains(marker, "second") {
		t.Fatalf("marker = %q", marker)
	}
}

func TestBuildAutomaticTokenRotates(t *testing.T) {
	setBuildTemp(t)
	source := t.TempDir()
	writeTestFile(t, source, "README.md", "# Home\n")
	target := filepath.Join(t.TempDir(), "site")
	cfg := buildConfig{source: source, output: target, pathToken: pathTokenAuto}

	first, err := buildStaticSite(cfg, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildStaticSite(cfg, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("automatic token was reused: %s", first)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("previous automatic token remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(second, "README.html")); err != nil {
		t.Fatal(err)
	}
}

func TestBuildCollisionDisambiguationAndWarnings(t *testing.T) {
	setBuildTemp(t)
	source := t.TempDir()
	writeTestFile(t, source, "guide.md", "# One\n")
	writeTestFile(t, source, "guide.markdown", "# Two\n")
	target := filepath.Join(t.TempDir(), "site")
	var warnings bytes.Buffer

	if _, err := buildStaticSite(buildConfig{source: source, output: target, pathToken: pathTokenNone}, &warnings); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"guide.md.html", "guide.markdown.html"} {
		if _, err := os.Stat(filepath.Join(target, name)); err != nil {
			t.Errorf("missing disambiguated output %s: %v", name, err)
		}
	}
	if !strings.Contains(warnings.String(), "warning:") || !strings.Contains(warnings.String(), "collision") {
		t.Errorf("warnings = %q", warnings.String())
	}
}

func TestBuildStrictNoDirectoryIndex(t *testing.T) {
	setBuildTemp(t)
	source := t.TempDir()
	writeTestFile(t, source, "index.md", "# Root\n")
	writeTestFile(t, filepath.Join(source, "nested"), "guide.md", "# Guide\n")
	target := filepath.Join(t.TempDir(), "site")

	_, err := buildStaticSite(buildConfig{source: source, output: target, pathToken: pathTokenNone, strictNoDirIndex: true}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "nested/index.md") {
		t.Fatalf("missing-index error = %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target created after failed index validation: %v", err)
	}

	writeTestFile(t, filepath.Join(source, "nested"), "index.md", "# Nested\n")
	if _, err := buildStaticSite(buildConfig{source: source, output: target, pathToken: pathTokenNone, strictNoDirIndex: true}, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestBuildBrokenLinkStrictness(t *testing.T) {
	setBuildTemp(t)
	source := t.TempDir()
	writeTestFile(t, source, "README.md", "# Home\n\n[Missing](missing.md)\n")
	var warnings bytes.Buffer
	if _, err := buildStaticSite(buildConfig{source: source, output: filepath.Join(t.TempDir(), "loose"), pathToken: pathTokenNone}, &warnings); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warnings.String(), "broken local document link") {
		t.Fatalf("warnings = %q", warnings.String())
	}
	target := filepath.Join(t.TempDir(), "strict")
	if _, err := buildStaticSite(buildConfig{source: source, output: target, pathToken: pathTokenNone, strict: true}, io.Discard); err == nil || !strings.Contains(err.Error(), "broken local document link") {
		t.Fatalf("strict broken-link error = %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("strict target created: %v", err)
	}
}

func TestBuildRejectsSourceOutputOverlap(t *testing.T) {
	source := t.TempDir()
	writeTestFile(t, source, "README.md", "# Home\n")
	target, err := validateBuildTargetPath(filepath.Join(source, "public"))
	if err != nil {
		t.Fatal(err)
	}
	mounts, err := resolveBuildMounts(buildConfig{source: source})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateBuildPathRelationships(target, mounts); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("overlap error = %v", err)
	}
}

func TestBuildRejectsSymlinkOutputAndHonorsLock(t *testing.T) {
	setBuildTemp(t)
	source := t.TempDir()
	writeTestFile(t, source, "README.md", "# Home\n")
	link := filepath.Join(t.TempDir(), "site-link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	if _, err := validateBuildTargetPath(link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink output error = %v", err)
	}

	base := filepath.Join(os.TempDir(), "mdfmt")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(base, "build.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "site")
	if _, err := buildStaticSite(buildConfig{source: source, output: target, pathToken: pathTokenNone}, io.Discard); err == nil || !strings.Contains(err.Error(), "another build") {
		t.Fatalf("lock contention error = %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target created during lock contention: %v", err)
	}
}

func TestBuildRejectsEncodedTraversalAndEscapingImage(t *testing.T) {
	setBuildTemp(t)
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	writeTestFile(t, parent, "outside.md", "# Outside\n")
	if err := os.WriteFile(filepath.Join(parent, "outside.png"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, source, "README.md", "# Home\n\n[Outside](%2e%2e/outside.md)\n\n![Secret](../outside.png)\n")
	target := filepath.Join(t.TempDir(), "site")
	_, err := buildStaticSite(buildConfig{source: source, output: target, pathToken: pathTokenNone, strict: true}, io.Discard)
	if err == nil || strings.Count(err.Error(), "escapes its mount") != 2 {
		t.Fatalf("strict escaping-reference error = %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target created after escaping references: %v", err)
	}
}

func TestBuildStrictDiagnosticsLeaveTargetUntouched(t *testing.T) {
	setBuildTemp(t)
	source := t.TempDir()
	writeTestFile(t, source, "guide.md", "# One\n")
	writeTestFile(t, source, "guide.markdown", "# Two\n")
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, ".mdfmt"), []byte(buildMarker), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, target, "old.txt", "old")

	_, err := buildStaticSite(buildConfig{source: source, output: target, pathToken: pathTokenNone, strict: true}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("strict collision error = %v", err)
	}
	if got := mustRead(t, filepath.Join(target, "old.txt")); got != "old" {
		t.Fatalf("target changed after failed build: %q", got)
	}
}

func TestBuildMountValidation(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	tests := []struct {
		mounts []string
		want   string
	}{
		{[]string{"../bad=" + root}, "invalid mount"},
		{[]string{"_mdfmt=" + root}, "invalid mount"},
		{[]string{"a=" + root, "A/b=" + other}, "overlap"},
		{[]string{"a=" + root, "b=" + root}, "source roots"},
	}
	for _, test := range tests {
		if _, err := resolveBuildMounts(buildConfig{mounts: test.mounts}); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("resolveBuildMounts(%v) error = %v, want %q", test.mounts, err, test.want)
		}
	}
}

func TestRunBuildQuiet(t *testing.T) {
	setBuildTemp(t)
	source := t.TempDir()
	writeTestFile(t, source, "README.md", "# Home\n")
	target := filepath.Join(t.TempDir(), "site")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"build", source, "-o", target, "--quiet"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("quiet output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func setBuildTemp(t *testing.T) {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
}
