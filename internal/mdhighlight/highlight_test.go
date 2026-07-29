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

func containsToken(tokens []chroma.Token, value string, kind chroma.TokenType) bool {
	for _, token := range tokens {
		if token.Value == value && token.Type == kind {
			return true
		}
	}
	return false
}
