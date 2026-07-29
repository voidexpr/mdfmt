package main

import (
	"bytes"
	"html"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestParseSaveFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want saveConfig
	}{
		{"flags before source", []string{"--hash", "--quiet", "notes.txt"},
			saveConfig{source: "notes.txt", hash: true, quiet: true}},
		{"flags after source", []string{"notes.txt", "--hash", "--quiet"},
			saveConfig{source: "notes.txt", hash: true, quiet: true}},
		{"short flags around source", []string{"-q", "notes.txt", "-o", "out"},
			saveConfig{source: "notes.txt", output: "out", quiet: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseSaveFlags(tt.args, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if cfg != tt.want {
				t.Errorf("cfg = %#v, want %#v", cfg, tt.want)
			}
		})
	}
}

func TestSaveAcceptsAnyRegularSourceExtension(t *testing.T) {
	root := t.TempDir()
	source := writeTestFile(t, root, "draft.future-format", "# Draft\n\nHello **there**.\n")
	target := filepath.Join(root, "preview")

	output, err := saveStandalone(saveConfig{source: source, output: target})
	if err != nil {
		t.Fatal(err)
	}
	if output != target {
		t.Fatalf("output = %q, want exact target %q", output, target)
	}
	html := mustRead(t, output)
	for _, want := range []string{`<h1 id="draft">Draft</h1>`, "<strong>there</strong>", "draft.future-format"} {
		if !strings.Contains(html, want) {
			t.Errorf("standalone output does not contain %q:\n%s", want, html)
		}
	}
}

func TestStandaloneOutputResolution(t *testing.T) {
	root := t.TempDir()
	source := writeTestFile(t, root, "release.notes", "# Release\n")
	outputDirectory := t.TempDir()

	tests := []struct {
		name string
		cfg  saveConfig
		// want is the expected output path; an empty want expects an error,
		// optionally matching wantErr.
		want    string
		wantErr string
	}{
		{
			name: "existing directory uses readable name",
			cfg:  saveConfig{source: source, output: outputDirectory},
			want: filepath.Join(outputDirectory, "release.html"),
		},
		{
			name: "explicit filename is exact",
			cfg:  saveConfig{source: source, output: filepath.Join(outputDirectory, "sent-document")},
			want: filepath.Join(outputDirectory, "sent-document"),
		},
		{
			name: "missing target parent is rejected",
			cfg:  saveConfig{source: source, output: filepath.Join(root, "missing", "preview.html")},
		},
		{
			name:    "directory source is rejected",
			cfg:     saveConfig{source: root, output: filepath.Join(outputDirectory, "bad.html")},
			wantErr: "not a regular file",
		},
		{
			name: "missing source is rejected",
			cfg:  saveConfig{source: filepath.Join(root, "missing-source"), output: filepath.Join(outputDirectory, "bad.html")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := saveStandalone(tt.cfg)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("output %q was accepted", output)
				}
				if tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if output != tt.want {
				t.Errorf("output = %q, want %q", output, tt.want)
			}
		})
	}
}

func TestDefaultTemporaryOutput(t *testing.T) {
	tempRoot := t.TempDir()
	t.Setenv("TMPDIR", tempRoot)
	source := writeTestFile(t, t.TempDir(), "editor-draft", "# Editor Draft\n")

	output, err := saveStandalone(saveConfig{source: source})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(tempRoot, "mdfmt", "editor-draft.html")
	if output != want {
		t.Errorf("output = %q, want %q", output, want)
	}
	info, err := os.Stat(filepath.Dir(output))
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatalf("default output parent is not a directory: %s", filepath.Dir(output))
	}
}

func TestHashedOutputNaming(t *testing.T) {
	root := t.TempDir()
	first := writeTestFile(t, filepath.Join(root, "one"), "same-name.md", "# One\n")
	second := writeTestFile(t, filepath.Join(root, "two"), "same-name.md", "# Two\n")
	outputDirectory := t.TempDir()

	firstOutput, err := saveStandalone(saveConfig{source: first, output: outputDirectory, hash: true})
	if err != nil {
		t.Fatal(err)
	}
	repeatedOutput, err := saveStandalone(saveConfig{source: first, output: outputDirectory, hash: true})
	if err != nil {
		t.Fatal(err)
	}
	secondOutput, err := saveStandalone(saveConfig{source: second, output: outputDirectory, hash: true})
	if err != nil {
		t.Fatal(err)
	}
	if firstOutput != repeatedOutput {
		t.Errorf("hashed name is unstable: %q != %q", firstOutput, repeatedOutput)
	}
	if firstOutput == secondOutput {
		t.Errorf("different canonical paths collided: %q", firstOutput)
	}
	pattern := regexp.MustCompile(`^[0-9a-f]{24}\.html$`)
	if !pattern.MatchString(filepath.Base(firstOutput)) {
		t.Errorf("hashed filename = %q", filepath.Base(firstOutput))
	}
	if strings.Contains(filepath.Base(firstOutput), "same-name") {
		t.Errorf("hashed filename exposes the source name: %q", filepath.Base(firstOutput))
	}

	explicit := filepath.Join(outputDirectory, "chosen.html")
	if _, err := saveStandalone(saveConfig{source: first, output: explicit, hash: true}); err == nil {
		t.Fatal("--hash with an explicit output filename was accepted")
	}
}

func TestStandaloneAtomicOverwriteAndIdentityRejection(t *testing.T) {
	root := t.TempDir()
	source := writeTestFile(t, root, "draft.md", "# First\n")
	target := filepath.Join(root, "draft.html")
	if err := os.WriteFile(target, []byte("old partial data"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := saveStandalone(saveConfig{source: source, output: target}); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, target); strings.Contains(got, "old partial data") || !strings.Contains(got, "<h1") {
		t.Errorf("target was not replaced cleanly:\n%s", got)
	}
	matches, err := filepath.Glob(filepath.Join(root, ".mdfmt-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("atomic write left temporary files: %v", matches)
	}

	if _, err := saveStandalone(saveConfig{source: source, output: source}); err == nil {
		t.Fatal("source/output identity was accepted")
	}
}

func TestStandaloneRenderingPrivacyAndFeatures(t *testing.T) {
	root := t.TempDir()
	sourceContent := "# Guide\n\n<script>alert(1)</script>\n\n```python\ndef greet(name):\n    return name\n```\n\n## Install\n\n### Options\n\n#### Detail\n\n# Appendix\n\n## More\n"
	source := writeTestFile(t, root, "private-draft.md", sourceContent)
	modified := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(source, modified, modified); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}

	output, err := renderStandalone([]byte(sourceContent), filepath.Base(source), info)
	if err != nil {
		t.Fatal(err)
	}
	document := string(output)
	for _, want := range []string{
		`<style>`,
		`<script>`,
		`http-equiv="Content-Security-Policy"`,
		`style-src &#39;sha256-`,
		`script-src &#39;sha256-`,
		`class="layout no-nav"`,
		`class="toc-top" href="" data-top`,
		`href="#install"`,
		`href="#options"`,
		`href="#detail"`,
		`href="#appendix"`,
		`href="#more"`,
		`id="install"`,
		`id="options"`,
		`id="detail"`,
		`id="appendix"`,
		`private-draft.md <span class="breadcrumb-meta">(3h ago,`,
		modified.Local().Format("Jan 2, 2006 3:04 PM"),
		humanSize(int64(len(sourceContent))),
		`location.pathname + location.search`,
		`class="highlight" data-language="python"`,
		`<pre class="chroma"><code>`,
		`class="k">def</span>`,
		`.chroma .k { color:`,
		`@media (prefers-color-scheme: dark)`,
	} {
		if !strings.Contains(document, want) {
			t.Errorf("standalone output does not contain %q:\n%s", want, document)
		}
	}
	for _, forbidden := range []string{
		root,
		filepath.Dir(source),
		`<link rel="stylesheet"`,
		`<script src=`,
		`<base`,
		`<script>alert(1)</script>`,
		`raw HTML omitted`,
		`class="left-sidebar"`,
		`style="`,
	} {
		if strings.Contains(document, forbidden) {
			t.Errorf("standalone output contains forbidden value %q", forbidden)
		}
	}
	if strings.Count(document, `<h1 id=`) != 2 {
		t.Errorf("all h1 headings were not retained:\n%s", document)
	}

	style := textBetween(t, document, "<style>", "</style>")
	script := textBetween(t, document, "<script>", "</script>")
	cspMatch := regexp.MustCompile(`http-equiv="Content-Security-Policy" content="([^"]+)"`).FindStringSubmatch(document)
	if len(cspMatch) != 2 {
		t.Fatal("standalone output has no readable CSP meta value")
	}
	gotCSP := html.UnescapeString(cspMatch[1])
	if wantCSP := standaloneCSP([]byte(style), []byte(script)); gotCSP != wantCSP {
		t.Errorf("CSP does not authorize the exact inline assets:\ngot  %s\nwant %s", gotCSP, wantCSP)
	}
}

func TestRunSavePrintsAbsolutePathAndQuietSuppressesIt(t *testing.T) {
	root := t.TempDir()
	source := writeTestFile(t, root, "draft", "# Draft\n")
	target := filepath.Join(root, "preview.html")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"save", source, "-o", target}, &stdout, &stderr); err != nil {
		t.Fatalf("run save: %v; stderr=%s", err, stderr.String())
	}
	if stdout.String() != target+"\n" {
		t.Errorf("stdout = %q, want %q", stdout.String(), target+"\n")
	}

	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"save", "-q", source, "-o", target}, &stdout, &stderr); err != nil {
		t.Fatalf("run quiet save: %v; stderr=%s", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("quiet stdout = %q", stdout.String())
	}
}

func textBetween(t *testing.T, document, start, end string) string {
	t.Helper()
	startIndex := strings.Index(document, start)
	if startIndex < 0 {
		t.Fatalf("document does not contain %q", start)
	}
	startIndex += len(start)
	endIndex := strings.Index(document[startIndex:], end)
	if endIndex < 0 {
		t.Fatalf("document does not contain %q after %q", end, start)
	}
	return document[startIndex : startIndex+endIndex]
}
