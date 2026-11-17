package main

import (
	"unicode"
)

// TokenType is Go's standard enum pattern: a distinct integer type with iota constants.
type TokenType int

const (
	// Special tokens
	TOKEN_ILLEGAL TokenType = iota
	TOKEN_EOF

	// Identifiers & literals
	TOKEN_IDENT
	TOKEN_INT

	// Single-character operators & delimiters (easy to add/remove in the map below)
	TOKEN_ASSIGN   // =
	TOKEN_PLUS     // +
	TOKEN_MINUS    // -
	TOKEN_ASTERISK // *
	TOKEN_SLASH    // /
	TOKEN_LPAREN   // (
	TOKEN_RPAREN   // )
	TOKEN_LBRACE   // {
	TOKEN_RBRACE   // }
	TOKEN_SEMI     // ;

	// Multi-character operators
	TOKEN_EQ     // ==
	TOKEN_NOT_EQ // !=

	// Keywords
	TOKEN_LET
	TOKEN_FN
	TOKEN_RETURN
	TOKEN_IF
	TOKEN_ELSE
)

// String implements fmt.Stringer so printing tokens shows readable names instead of raw ints.
func (t TokenType) String() string {
	names := [...]string{
		TOKEN_ILLEGAL:  "ILLEGAL",
		TOKEN_EOF:      "EOF",
		TOKEN_IDENT:    "IDENT",
		TOKEN_INT:      "INT",
		TOKEN_ASSIGN:   "=",
		TOKEN_PLUS:     "+",
		TOKEN_MINUS:    "-",
		TOKEN_ASTERISK: "*",
		TOKEN_SLASH:    "/",
		TOKEN_LPAREN:   "(",
		TOKEN_RPAREN:   ")",
		TOKEN_LBRACE:   "{",
		TOKEN_RBRACE:   "}",
		TOKEN_SEMI:     ";",
		TOKEN_EQ:       "==",
		TOKEN_NOT_EQ:   "!=",
		TOKEN_LET:      "LET",
		TOKEN_FN:       "FN",
		TOKEN_RETURN:   "RETURN",
		TOKEN_IF:       "IF",
		TOKEN_ELSE:     "ELSE",
	}
	if int(t) >= 0 && int(t) < len(names) {
		return names[t]
	}
	return "UNKNOWN"
}

// Lookup tables: edit these two maps on competition day to adapt to the spec's syntax.
var keywords = map[string]TokenType{
	"let":    TOKEN_LET,
	"fn":     TOKEN_FN,
	"return": TOKEN_RETURN,
	"if":     TOKEN_IF,
	"else":   TOKEN_ELSE,
}

var singleCharTokens = map[byte]TokenType{
	'+': TOKEN_PLUS,
	'-': TOKEN_MINUS,
	'*': TOKEN_ASTERISK,
	'/': TOKEN_SLASH,
	'(': TOKEN_LPAREN,
	')': TOKEN_RPAREN,
	'{': TOKEN_LBRACE,
	'}': TOKEN_RBRACE,
	';': TOKEN_SEMI,
}

type Token struct {
	Type    TokenType
	Literal string
	Line    int
}

type Lexer struct {
	input   string
	pos     int  // current index in input
	readPos int  // next index to read
	ch      byte // current char under examination
	line    int
}

func NewLexer(input string) *Lexer {
	l := &Lexer{input: input, line: 1}
	l.readChar()
	return l
}

func (l *Lexer) readChar() {
	if l.readPos >= len(l.input) {
		l.ch = 0 // ASCII NUL represents EOF
	} else {
		l.ch = l.input[l.readPos]
	}
	l.pos = l.readPos
	l.readPos++
}

func (l *Lexer) peekChar() byte {
	if l.readPos >= len(l.input) {
		return 0
	}
	return l.input[l.readPos]
}

func (l *Lexer) NextToken() Token {
	l.skipWhitespace()

	var tok Token
	tok.Line = l.line

	// 1. Handle multi-character operators via 1-char lookahead
	switch l.ch {
	case 0:
		return Token{Type: TOKEN_EOF, Literal: "", Line: l.line}
	case '=':
		if l.peekChar() == '=' {
			ch := l.ch
			l.readChar()
			tok = Token{Type: TOKEN_EQ, Literal: string(ch) + string(l.ch), Line: l.line}
		} else {
			tok = Token{Type: TOKEN_ASSIGN, Literal: string(l.ch), Line: l.line}
		}
		l.readChar()
		return tok
	case '!':
		if l.peekChar() == '=' {
			ch := l.ch
			l.readChar()
			tok = Token{Type: TOKEN_NOT_EQ, Literal: string(ch) + string(l.ch), Line: l.line}
			l.readChar()
			return tok
		}
	}

	// 2. Check table-driven single-character tokens
	if tokType, ok := singleCharTokens[l.ch]; ok {
		tok = Token{Type: tokType, Literal: string(l.ch), Line: l.line}
		l.readChar()
		return tok
	}

	// 3. Identifiers & Keywords
	if isLetter(l.ch) {
		lit := l.readWhile(isLetterOrDigit)
		tokType := TOKEN_IDENT
		if kwType, isKeyword := keywords[lit]; isKeyword {
			tokType = kwType
		}
		return Token{Type: tokType, Literal: lit, Line: tok.Line}
	}

	// 4. Numbers (integers; extend here if floats like 3.14 are needed)
	if isDigit(l.ch) {
		lit := l.readWhile(isDigit)
		return Token{Type: TOKEN_INT, Literal: lit, Line: tok.Line}
	}

	// 5. Fallback for unrecognized characters
	tok = Token{Type: TOKEN_ILLEGAL, Literal: string(l.ch), Line: l.line}
	l.readChar()
	return tok
}

func (l *Lexer) skipWhitespace() {
	for l.ch == ' ' || l.ch == '\t' || l.ch == '\n' || l.ch == '\r' {
		if l.ch == '\n' {
			l.line++
		}
		l.readChar()
	}
}

func (l *Lexer) readWhile(predicate func(byte) bool) string {
	start := l.pos
	for predicate(l.ch) {
		l.readChar()
	}
	return l.input[start:l.pos]
}

func isLetter(ch byte) bool {
	return unicode.IsLetter(rune(ch)) || ch == '_'
}

func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

func isLetterOrDigit(ch byte) bool {
	return isLetter(ch) || isDigit(ch)
}
