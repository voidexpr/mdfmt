package main

import "testing"

func TestChromeIsDefaultForScheme(t *testing.T) {
	content := []byte(`[
  {"LSHandlerURLScheme":"https","LSHandlerRoleAll":"com.google.chrome"},
  {"LSHandlerURLScheme":"mailto","LSHandlerRoleAll":"com.apple.mail"},
  {"LSHandlerURLScheme":"custom","LSHandlerRoleViewer":"com.google.Chrome"}
]`)
	for _, test := range []struct {
		name   string
		scheme string
		want   bool
	}{
		{name: "HTTPS Chrome", scheme: "https", want: true},
		{name: "HTTP falls back to HTTPS", scheme: "http", want: true},
		{name: "viewer role", scheme: "custom", want: true},
		{name: "other handler", scheme: "mailto", want: false},
		{name: "missing handler", scheme: "unknown", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := chromeIsDefaultForScheme(content, test.scheme); got != test.want {
				t.Errorf("chromeIsDefaultForScheme(..., %q) = %t, want %t", test.scheme, got, test.want)
			}
		})
	}
}

func TestChromeIsDefaultForSchemeUsesLastMatchingHandler(t *testing.T) {
	content := []byte(`[
  {"LSHandlerURLScheme":"https","LSHandlerRoleAll":"com.google.Chrome"},
  {"LSHandlerURLScheme":"https","LSHandlerRoleAll":"com.apple.Safari"}
]`)
	if chromeIsDefaultForScheme(content, "https") {
		t.Fatal("older Chrome handler took precedence over newer Safari handler")
	}
}

func TestChromeIsDefaultForSchemeRejectsInvalidJSON(t *testing.T) {
	if chromeIsDefaultForScheme([]byte("not JSON"), "https") {
		t.Fatal("invalid JSON identified Chrome as the default")
	}
}
