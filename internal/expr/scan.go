package expr

import "context"

// The tokenizer, for the Vega-Lite compiler: it reads expressions to find
// the fields they depend on (upstream's getDependentFields parses them with
// vega-expression, whose tokenizer this is), with a grammar of its own that
// takes chains of operators at any length. Sharing the tokenizer keeps the
// two from drifting (C47 of the 2026-10-18 audit: the compiler's own lexer
// refused identifiers vega-expression accepts and read literals otherwise).

// TokenKind is the kind of a Token.
type TokenKind uint8

const (
	TokBool    = TokenKind(tBool)
	TokEOF     = TokenKind(tEOF)
	TokIdent   = TokenKind(tIdent)
	TokKeyword = TokenKind(tKeyword)
	TokNull    = TokenKind(tNull)
	TokNum     = TokenKind(tNum)
	TokPunct   = TokenKind(tPunct)
	TokString  = TokenKind(tString)
)

// Token is one token of an expression as Parse reads it.
type Token struct {
	Kind TokenKind
	// Text is an identifier's, keyword's or punctuator's text, the value of
	// a string literal, and "true", "false" or "null".
	Text string
	// Num is a numeric literal's value.
	Num float64
	// Octal marks a legacy octal numeric literal or a string with an octal
	// escape, which the grammar refuses as a literal ("Octal literals are
	// not allowed in strict mode.").
	Octal bool
	// Start and End are the token's byte offsets in the source.
	Start, End int
}

// Scanner reads an expression's tokens as Parse does. Its methods panic
// with a *SyntaxError (Parse's, with the same message) on an illegal token;
// a caller recovers it where it recovers its own parse errors.
type Scanner struct{ p parser }

// NewScanner is a Scanner of src for the call of ctx, positioned before the
// first token. Unlike Parse it takes a source of any length: upstream's
// tokenizer does, and the caller meters its reading token by token.
func NewScanner(ctx context.Context, src string) *Scanner {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Scanner{p: parser{src: src, ctx: ctx}}
}

// Next reads the next token.
func (s *Scanner) Next() Token {
	t := s.p.advance()
	s.p.idx = t.end
	return Token{Kind: TokenKind(t.kind), Text: t.val, Num: t.num, Octal: t.octal, Start: t.start, End: t.end}
}

// RegExp reads a regular expression literal from offset start (the '/' a
// primary expression begins with, which Next read as a punctuator), and
// validates and compiles it as Parse does. It returns the literal's source.
func (s *Scanner) RegExp(start int) string {
	s.p.idx = start
	_, raw := s.p.scanRegExp()
	return raw
}

// IsPrimaryIdentifier reports whether vega-expression reads a token that
// starts a primary expression as an identifier: an identifier, and any token
// whose value names a member of its legalKeywords table, a plain object
// ({if: 1}), so "if" and Object.prototype's members too (a string literal
// "toString" is read as the identifier toString).
func IsPrimaryIdentifier(t Token) bool {
	switch t.Kind {
	case TokIdent:
		return true
	case TokKeyword, TokString:
		return t.Text == "if" || objectPrototypeMembers[t.Text]
	}
	return false
}

// objectPrototypeMembers are the names a plain object inherits.
var objectPrototypeMembers = map[string]bool{
	"__defineGetter__": true, "__defineSetter__": true, "__lookupGetter__": true,
	"__lookupSetter__": true, "__proto__": true, "constructor": true, "hasOwnProperty": true,
	"isPrototypeOf": true, "propertyIsEnumerable": true, "toLocaleString": true,
	"toString": true, "valueOf": true,
}
