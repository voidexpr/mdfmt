package main

import (
	"strings"
	"testing"
)

func TestSanitizeRawHTMLAllowlist(t *testing.T) {
	raw := []byte(`<a href="./full.png" title="Full" onclick="bad()"><img src="./preview.png" alt="Preview &amp; more" width="480" height="bogus" style="display:none"></a>`)
	sanitized, ok := sanitizeRawHTML(raw, nil, false)
	if !ok {
		t.Fatal("safe anchor and image were rejected")
	}
	want := `<a href="./full.png" title="Full"><img src="./preview.png" alt="Preview &amp; more" width="480"></a>`
	if got := string(sanitized); got != want {
		t.Errorf("sanitized HTML:\ngot  %s\nwant %s", got, want)
	}
}

func TestSanitizeRawHTMLRewritesRootURLs(t *testing.T) {
	raw := []byte(`<a href="/guide.md?view=full#top"><img src="/images/preview.png" alt="Preview"></a>`)
	sanitized, ok := sanitizeRawHTML(raw, []string{"plans", "nested"}, true)
	if !ok {
		t.Fatal("safe root-relative HTML was rejected")
	}
	want := `<a href="../../guide.md?view=full#top"><img src="../../images/preview.png" alt="Preview"></a>`
	if got := string(sanitized); got != want {
		t.Errorf("rewritten HTML:\ngot  %s\nwant %s", got, want)
	}
}

func TestSanitizeRawHTMLRejectsUnsafeContent(t *testing.T) {
	tests := []string{
		`<script>alert(1)</script>`,
		`<div>layout injection</div>`,
		`<a href="javascript:alert(1)">bad</a>`,
		`<img src="data:image/svg+xml,bad">`,
		`<img src="file:///etc/passwd">`,
		`<!-- comment -->`,
	}
	for _, source := range tests {
		t.Run(source, func(t *testing.T) {
			if sanitized, ok := sanitizeRawHTML([]byte(source), nil, false); ok {
				t.Errorf("unsafe HTML accepted as %q", sanitized)
			}
		})
	}
}

func TestSanitizeRawHTMLAllowsEscapedTextAndMailLinks(t *testing.T) {
	sanitized, ok := sanitizeRawHTML([]byte(`<a href="mailto:test@example.com">one &lt; two</a>`), nil, false)
	if !ok {
		t.Fatal("safe mail link was rejected")
	}
	got := string(sanitized)
	if !strings.Contains(got, `href="mailto:test@example.com"`) || !strings.Contains(got, `one &lt; two`) {
		t.Errorf("sanitized mail link = %q", got)
	}
}
