package main

import (
	"unicode"
)

// TokenType represents lexical token types.
type TokenType int

const (
	// Special tokens
	TOKEN_ILLEGAL TokenType = iota
	TOKEN_EOF

	// Identifiers & literals
	TOKEN_IDENT
	TOKEN_INT

	// Operators & delimiters
	TOKEN_ASSIGN   // =
	TOKEN_PLUS     // +
	TOKEN_MINUS    // -
	TOKEN_ASTERISK // *
	TOKEN_SLASH    // /
	TOKEN_PERCENT  // %
	TOKEN_BANG     // !
	TOKEN_LT       // <
	TOKEN_GT       // >
	TOKEN_LPAREN   // (
	TOKEN_RPAREN   // )
	TOKEN_LBRACE   // {
	TOKEN_RBRACE   // }
	TOKEN_COMMA    // ,
	TOKEN_SEMI     // ;

	// Multi-character operators
	TOKEN_EQ     // ==
	TOKEN_NOT_EQ // !=
	TOKEN_LTE    // <=
	TOKEN_GTE    // >=
	TOKEN_AND    // &&
	TOKEN_OR     // ||

	// Keywords
	TOKEN_LET
	TOKEN_FN
	TOKEN_RETURN
	TOKEN_IF
	TOKEN_ELSE
	TOKEN_WHILE
	TOKEN_TRUE
	TOKEN_FALSE
)

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
		TOKEN_PERCENT:  "%",
		TOKEN_BANG:     "!",
		TOKEN_LT:       "<",
		TOKEN_GT:       ">",
		TOKEN_LPAREN:   "(",
		TOKEN_RPAREN:   ")",
		TOKEN_LBRACE:   "{",
		TOKEN_RBRACE:   "}",
		TOKEN_COMMA:    ",",
		TOKEN_SEMI:     ";",
		TOKEN_EQ:       "==",
		TOKEN_NOT_EQ:   "!=",
		TOKEN_LTE:      "<=",
		TOKEN_GTE:      ">=",
		TOKEN_AND:      "&&",
		TOKEN_OR:       "||",
		TOKEN_LET:      "LET",
		TOKEN_FN:       "FN",
		TOKEN_RETURN:   "RETURN",
		TOKEN_IF:       "IF",
		TOKEN_ELSE:     "ELSE",
		TOKEN_WHILE:    "WHILE",
		TOKEN_TRUE:     "TRUE",
		TOKEN_FALSE:    "FALSE",
	}
	if int(t) >= 0 && int(t) < len(names) {
		return names[t]
	}
	return "UNKNOWN"
}

var keywords = map[string]TokenType{
	"let":    TOKEN_LET,
	"var":    TOKEN_LET,
	"fn":     TOKEN_FN,
	"func":   TOKEN_FN,
	"return": TOKEN_RETURN,
	"if":     TOKEN_IF,
	"else":   TOKEN_ELSE,
	"while":  TOKEN_WHILE,
	"true":   TOKEN_TRUE,
	"false":  TOKEN_FALSE,
}

var singleCharTokens = map[byte]TokenType{
	'+': TOKEN_PLUS,
	'-': TOKEN_MINUS,
	'*': TOKEN_ASTERISK,
	'/': TOKEN_SLASH,
	'%': TOKEN_PERCENT,
	'(': TOKEN_LPAREN,
	')': TOKEN_RPAREN,
	'{': TOKEN_LBRACE,
	'}': TOKEN_RBRACE,
	',': TOKEN_COMMA,
	';': TOKEN_SEMI,
}

type Token struct {
	Type    TokenType
	Literal string
	Line    int
}

type Lexer struct {
	input   string
	pos     int
	readPos int
	ch      byte
	line    int
}

func NewLexer(input string) *Lexer {
	l := &Lexer{input: input, line: 1}
	l.readChar()
	return l
}

func (l *Lexer) readChar() {
	if l.readPos >= len(l.input) {
		l.ch = 0
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
	l.skipWhitespaceAndComments()

	var tok Token
	tok.Line = l.line

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
		} else {
			tok = Token{Type: TOKEN_BANG, Literal: string(l.ch), Line: l.line}
		}
		l.readChar()
		return tok
	case '<':
		if l.peekChar() == '=' {
			ch := l.ch
			l.readChar()
			tok = Token{Type: TOKEN_LTE, Literal: string(ch) + string(l.ch), Line: l.line}
		} else {
			tok = Token{Type: TOKEN_LT, Literal: string(l.ch), Line: l.line}
		}
		l.readChar()
		return tok
	case '>':
		if l.peekChar() == '=' {
			ch := l.ch
			l.readChar()
			tok = Token{Type: TOKEN_GTE, Literal: string(ch) + string(l.ch), Line: l.line}
		} else {
			tok = Token{Type: TOKEN_GT, Literal: string(l.ch), Line: l.line}
		}
		l.readChar()
		return tok
	case '&':
		if l.peekChar() == '&' {
			ch := l.ch
			l.readChar()
			tok = Token{Type: TOKEN_AND, Literal: string(ch) + string(l.ch), Line: l.line}
			l.readChar()
			return tok
		}
	case '|':
		if l.peekChar() == '|' {
			ch := l.ch
			l.readChar()
			tok = Token{Type: TOKEN_OR, Literal: string(ch) + string(l.ch), Line: l.line}
			l.readChar()
			return tok
		}
	}

	if tokType, ok := singleCharTokens[l.ch]; ok {
		tok = Token{Type: tokType, Literal: string(l.ch), Line: l.line}
		l.readChar()
		return tok
	}

	if isLetter(l.ch) {
		lit := l.readWhile(isLetterOrDigit)
		tokType := TOKEN_IDENT
		if kwType, isKeyword := keywords[lit]; isKeyword {
			tokType = kwType
		}
		return Token{Type: tokType, Literal: lit, Line: tok.Line}
	}

	if isDigit(l.ch) {
		lit := l.readWhile(isDigit)
		return Token{Type: TOKEN_INT, Literal: lit, Line: tok.Line}
	}

	tok = Token{Type: TOKEN_ILLEGAL, Literal: string(l.ch), Line: l.line}
	l.readChar()
	return tok
}

func (l *Lexer) skipWhitespaceAndComments() {
	for {
		for l.ch == ' ' || l.ch == '\t' || l.ch == '\n' || l.ch == '\r' {
			if l.ch == '\n' {
				l.line++
			}
			l.readChar()
		}

		// Single-line comment: // ... or # ...
		if (l.ch == '/' && l.peekChar() == '/') || l.ch == '#' {
			for l.ch != '\n' && l.ch != 0 {
				l.readChar()
			}
			continue
		}

		// Multi-line comment: /* ... */
		if l.ch == '/' && l.peekChar() == '*' {
			l.readChar() // consume '/'
			l.readChar() // consume '*'
			for l.ch != 0 {
				if l.ch == '\n' {
					l.line++
				}
				if l.ch == '*' && l.peekChar() == '/' {
					l.readChar()
					l.readChar()
					break
				}
				l.readChar()
			}
			continue
		}

		break
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
