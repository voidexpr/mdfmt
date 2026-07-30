package mdhighlight

import (
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

func TestQeylanLexerRegistrationAndTokens(t *testing.T) {
	_ = Extension()

	for _, alias := range []string{"qeylan", "qy"} {
		if lexer := lexers.Get(alias); lexer == nil || lexer.Config().Name != "Qeylan" {
			t.Fatalf("lexer %q = %#v, want Qeylan", alias, lexer)
		}
	}
	for _, filename := range []string{"example.qy", "example.qeylan"} {
		if lexer := lexers.Match(filename); lexer == nil || lexer.Config().Name != "Qeylan" {
			t.Fatalf("lexer for %q = %#v, want Qeylan", filename, lexer)
		}
	}

	source := "let value = 12days\nif ready? call(Type, $rows, @macro, #tag)\n\"escaped\\\"quote\" // note\n"
	iterator, err := lexers.Get("qeylan").Tokenise(nil, source)
	if err != nil {
		t.Fatal(err)
	}
	tokens := iterator.Tokens()
	wants := []struct {
		value string
		kind  chroma.TokenType
	}{
		{"let", chroma.KeywordDeclaration},
		{"12days", chroma.LiteralNumber},
		{"?", chroma.Operator},
		{"call", chroma.NameFunction},
		{"Type", chroma.NameClass},
		{"$rows", chroma.NameBuiltin},
		{"@macro", chroma.NameDecorator},
		{"#tag", chroma.NameTag},
		{`\"`, chroma.LiteralStringEscape},
		{"// note", chroma.CommentSingle},
	}
	for _, want := range wants {
		if !containsToken(tokens, want.value, want.kind) {
			t.Errorf("Qeylan tokens do not contain %q as %s:\n%#v", want.value, want.kind, tokens)
		}
	}
}

func TestQeylanStringInterpolation(t *testing.T) {
	_ = Extension()

	tests := []struct {
		name   string
		source string
		wants  []struct {
			value string
			kind  chroma.TokenType
		}
		forbids []struct {
			value string
			kind  chroma.TokenType
		}
	}{
		{
			name:   "single quoted strings do not interpolate",
			source: "'plain @{ let bad = call() } @if no'\n",
			wants: []struct {
				value string
				kind  chroma.TokenType
			}{
				{"plain @{ let bad = call() } @if no", chroma.LiteralStringSingle},
			},
			forbids: []struct {
				value string
				kind  chroma.TokenType
			}{
				{"@{", chroma.LiteralStringInterpol},
				{"let", chroma.KeywordDeclaration},
				{"call", chroma.NameFunction},
			},
		},
		{
			name: "double quoted strings interpolate Qeylan",
			source: `"before
@{ let total = outer({value: call(12days)})
}
  @if ready? call(Type)
  @render(call(Type))
after @if stays text
"
`,
			wants: []struct {
				value string
				kind  chroma.TokenType
			}{
				{"@{", chroma.LiteralStringInterpol},
				{"let", chroma.KeywordDeclaration},
				{"outer", chroma.NameFunction},
				{"value", chroma.Name},
				{"call", chroma.NameFunction},
				{"12days", chroma.LiteralNumber},
				{"}", chroma.LiteralStringInterpol},
				{"@", chroma.LiteralStringInterpol},
				{"if", chroma.Keyword},
				{"render", chroma.NameFunction},
				{"Type", chroma.NameClass},
				{"after ", chroma.LiteralStringDouble},
				{"@", chroma.LiteralStringDouble},
				{"if stays text", chroma.LiteralStringDouble},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			iterator, err := lexers.Get("qeylan").Tokenise(nil, test.source)
			if err != nil {
				t.Fatal(err)
			}
			tokens := iterator.Tokens()
			if got := chroma.Stringify(tokens...); got != test.source {
				t.Fatalf("token text = %q, want %q", got, test.source)
			}
			for _, token := range tokens {
				if token.Type == chroma.Error {
					t.Fatalf("unexpected error token %q:\n%#v", token.Value, tokens)
				}
			}
			for _, want := range test.wants {
				if !containsToken(tokens, want.value, want.kind) {
					t.Errorf("Qeylan tokens do not contain %q as %s:\n%#v", want.value, want.kind, tokens)
				}
			}
			for _, forbid := range test.forbids {
				if containsToken(tokens, forbid.value, forbid.kind) {
					t.Errorf("Qeylan tokens unexpectedly contain %q as %s:\n%#v", forbid.value, forbid.kind, tokens)
				}
			}
		})
	}
}

func containsToken(tokens []chroma.Token, value string, kind chroma.TokenType) bool {
	for _, token := range tokens {
		if token.Value == value && token.Type == kind {
			return true
		}
	}
	return false
}
