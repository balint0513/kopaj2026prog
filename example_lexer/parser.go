package main

import (
	"fmt"
	"math"
	"strconv"
)

const (
	_ int = iota
	LOWEST
	LOGICAL_OR  // ||
	LOGICAL_AND // &&
	EQUALS      // == or !=
	LESSGREATER // >, <, >=, <=
	SUM         // + or -
	PRODUCT     // *, / or %
	PREFIX      // -X, +X or !X
	CALL        // fn(X)
)

var precedences = map[TokenType]int{
	TOKEN_OR:       LOGICAL_OR,
	TOKEN_AND:      LOGICAL_AND,
	TOKEN_EQ:       EQUALS,
	TOKEN_NOT_EQ:   EQUALS,
	TOKEN_LT:       LESSGREATER,
	TOKEN_GT:       LESSGREATER,
	TOKEN_LTE:      LESSGREATER,
	TOKEN_GTE:      LESSGREATER,
	TOKEN_PLUS:     SUM,
	TOKEN_MINUS:    SUM,
	TOKEN_SLASH:    PRODUCT,
	TOKEN_ASTERISK: PRODUCT,
	TOKEN_PERCENT:  PRODUCT,
	TOKEN_LPAREN:   CALL,
}

type (
	prefixParseFn func() Expression
	infixParseFn  func(Expression) Expression
	stmtParseFn   func() Statement
)

type Parser struct {
	l      *Lexer
	errors []string

	curToken  Token
	peekToken Token

	stmtParseFns   map[TokenType]stmtParseFn
	prefixParseFns map[TokenType]prefixParseFn
	infixParseFns  map[TokenType]infixParseFn
}

func NewParser(l *Lexer) *Parser {
	p := &Parser{
		l:              l,
		errors:         []string{},
		stmtParseFns:   make(map[TokenType]stmtParseFn),
		prefixParseFns: make(map[TokenType]prefixParseFn),
		infixParseFns:  make(map[TokenType]infixParseFn),
	}

	// Statement starters
	p.stmtParseFns[TOKEN_LET] = p.parseLetStatement
	p.stmtParseFns[TOKEN_RETURN] = p.parseReturnStatement
	p.stmtParseFns[TOKEN_WHILE] = p.parseWhileStatement
	p.stmtParseFns[TOKEN_FN] = p.parseFunctionStatement

	// Prefix expressions
	p.prefixParseFns[TOKEN_IDENT] = p.parseIdentifier
	p.prefixParseFns[TOKEN_INT] = p.parseIntegerLiteral
	p.prefixParseFns[TOKEN_TRUE] = p.parseBooleanLiteral
	p.prefixParseFns[TOKEN_FALSE] = p.parseBooleanLiteral
	p.prefixParseFns[TOKEN_MINUS] = p.parsePrefixExpression
	p.prefixParseFns[TOKEN_PLUS] = p.parsePrefixExpression
	p.prefixParseFns[TOKEN_BANG] = p.parsePrefixExpression
	p.prefixParseFns[TOKEN_LPAREN] = p.parseGroupedExpression
	p.prefixParseFns[TOKEN_IF] = p.parseIfExpression

	// Infix expressions
	p.infixParseFns[TOKEN_PLUS] = p.parseInfixExpression
	p.infixParseFns[TOKEN_MINUS] = p.parseInfixExpression
	p.infixParseFns[TOKEN_SLASH] = p.parseInfixExpression
	p.infixParseFns[TOKEN_ASTERISK] = p.parseInfixExpression
	p.infixParseFns[TOKEN_PERCENT] = p.parseInfixExpression
	p.infixParseFns[TOKEN_EQ] = p.parseInfixExpression
	p.infixParseFns[TOKEN_NOT_EQ] = p.parseInfixExpression
	p.infixParseFns[TOKEN_LT] = p.parseInfixExpression
	p.infixParseFns[TOKEN_GT] = p.parseInfixExpression
	p.infixParseFns[TOKEN_LTE] = p.parseInfixExpression
	p.infixParseFns[TOKEN_GTE] = p.parseInfixExpression
	p.infixParseFns[TOKEN_AND] = p.parseInfixExpression
	p.infixParseFns[TOKEN_OR] = p.parseInfixExpression
	p.infixParseFns[TOKEN_LPAREN] = p.parseCallExpression

	p.nextToken()
	p.nextToken()
	return p
}

func (p *Parser) Errors() []string { return p.errors }

func (p *Parser) nextToken() {
	p.curToken = p.peekToken
	p.peekToken = p.l.NextToken()
}

func (p *Parser) curTokenIs(t TokenType) bool  { return p.curToken.Type == t }
func (p *Parser) peekTokenIs(t TokenType) bool { return p.peekToken.Type == t }

func (p *Parser) expectPeek(t TokenType) bool {
	if p.peekTokenIs(t) {
		p.nextToken()
		return true
	}
	p.errors = append(p.errors, fmt.Sprintf("line %d: expected next token to be %s, got %s instead",
		p.peekToken.Line, t, p.peekToken.Type))
	return false
}

func (p *Parser) peekPrecedence() int {
	if prec, ok := precedences[p.peekToken.Type]; ok {
		return prec
	}
	return LOWEST
}

func (p *Parser) curPrecedence() int {
	if prec, ok := precedences[p.curToken.Type]; ok {
		return prec
	}
	return LOWEST
}

func (p *Parser) ParseProgram() *Program {
	program := &Program{Statements: []Statement{}}

	for !p.curTokenIs(TOKEN_EOF) {
		if p.curTokenIs(TOKEN_SEMI) {
			p.nextToken()
			continue
		}
		stmt := p.parseStatement()
		if stmt != nil {
			program.Statements = append(program.Statements, stmt)
		}
		p.nextToken()
	}
	return program
}

func (p *Parser) parseStatement() Statement {
	if fn, ok := p.stmtParseFns[p.curToken.Type]; ok {
		return fn()
	}
	if p.curTokenIs(TOKEN_IDENT) && p.peekTokenIs(TOKEN_ASSIGN) {
		return p.parseAssignStatement()
	}
	return p.parseExpressionStatement()
}

func (p *Parser) parseLetStatement() Statement {
	stmt := &LetStatement{Token: p.curToken}

	if !p.expectPeek(TOKEN_IDENT) {
		return nil
	}
	stmt.Name = &Identifier{Token: p.curToken, Value: p.curToken.Literal}

	if !p.expectPeek(TOKEN_ASSIGN) {
		return nil
	}

	p.nextToken()
	stmt.Value = p.parseExpression(LOWEST)

	if p.peekTokenIs(TOKEN_SEMI) {
		p.nextToken()
	}
	return stmt
}

func (p *Parser) parseAssignStatement() Statement {
	name := &Identifier{Token: p.curToken, Value: p.curToken.Literal}
	p.nextToken() // advance to '='
	stmt := &AssignStatement{Token: p.curToken, Name: name}

	p.nextToken() // advance to value expression
	stmt.Value = p.parseExpression(LOWEST)

	if p.peekTokenIs(TOKEN_SEMI) {
		p.nextToken()
	}
	return stmt
}

func (p *Parser) parseReturnStatement() Statement {
	stmt := &ReturnStatement{Token: p.curToken}
	if p.peekTokenIs(TOKEN_SEMI) {
		p.nextToken()
		return stmt
	}
	if p.peekTokenIs(TOKEN_RBRACE) || p.peekTokenIs(TOKEN_EOF) {
		return stmt
	}

	p.nextToken()
	stmt.ReturnValue = p.parseExpression(LOWEST)

	if p.peekTokenIs(TOKEN_SEMI) {
		p.nextToken()
	}
	return stmt
}

func (p *Parser) parseWhileStatement() Statement {
	stmt := &WhileStatement{Token: p.curToken}

	hasParen := false
	if p.peekTokenIs(TOKEN_LPAREN) {
		p.nextToken()
		hasParen = true
	}
	p.nextToken()
	stmt.Condition = p.parseExpression(LOWEST)
	if hasParen {
		if !p.expectPeek(TOKEN_RPAREN) {
			return nil
		}
	}
	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}
	stmt.Body = p.parseBlockStatement()
	return stmt
}

func (p *Parser) parseFunctionStatement() Statement {
	stmt := &FunctionStatement{Token: p.curToken}
	if !p.expectPeek(TOKEN_IDENT) {
		return nil
	}
	stmt.Name = &Identifier{Token: p.curToken, Value: p.curToken.Literal}

	if !p.expectPeek(TOKEN_LPAREN) {
		return nil
	}
	stmt.Parameters = p.parseFunctionParameters()

	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}
	stmt.Body = p.parseBlockStatement()
	return stmt
}

func (p *Parser) parseFunctionParameters() []*Identifier {
	identifiers := []*Identifier{}
	if p.peekTokenIs(TOKEN_RPAREN) {
		p.nextToken()
		return identifiers
	}
	p.nextToken()
	ident := &Identifier{Token: p.curToken, Value: p.curToken.Literal}
	identifiers = append(identifiers, ident)

	for p.peekTokenIs(TOKEN_COMMA) {
		p.nextToken()
		p.nextToken()
		ident := &Identifier{Token: p.curToken, Value: p.curToken.Literal}
		identifiers = append(identifiers, ident)
	}

	if !p.expectPeek(TOKEN_RPAREN) {
		return nil
	}
	return identifiers
}

func (p *Parser) parseExpressionStatement() *ExpressionStatement {
	stmt := &ExpressionStatement{Token: p.curToken}
	stmt.Expression = p.parseExpression(LOWEST)

	if p.peekTokenIs(TOKEN_SEMI) {
		p.nextToken()
	}
	return stmt
}

func (p *Parser) parseExpression(precedence int) Expression {
	prefix := p.prefixParseFns[p.curToken.Type]
	if prefix == nil {
		p.errors = append(p.errors, fmt.Sprintf("line %d: no prefix parse function for %s (%q)",
			p.curToken.Line, p.curToken.Type, p.curToken.Literal))
		return nil
	}
	leftExp := prefix()

	for !p.peekTokenIs(TOKEN_SEMI) && precedence < p.peekPrecedence() {
		infix := p.infixParseFns[p.peekToken.Type]
		if infix == nil {
			return leftExp
		}
		p.nextToken()
		leftExp = infix(leftExp)
	}

	return leftExp
}

func (p *Parser) parseIdentifier() Expression {
	return &Identifier{Token: p.curToken, Value: p.curToken.Literal}
}

func (p *Parser) parseIntegerLiteral() Expression {
	val, err := strconv.ParseInt(p.curToken.Literal, 10, 64)
	if err != nil {
		// Handle 9223372036854775808 when preceded by unary '-'
		uval, uerr := strconv.ParseUint(p.curToken.Literal, 10, 64)
		if uerr == nil && uval == 9223372036854775808 {
			return &IntegerLiteral{Token: p.curToken, Value: math.MinInt64}
		}
		p.errors = append(p.errors, fmt.Sprintf("line %d: could not parse %q as 64-bit integer",
			p.curToken.Line, p.curToken.Literal))
		return nil
	}
	return &IntegerLiteral{Token: p.curToken, Value: val}
}

func (p *Parser) parseBooleanLiteral() Expression {
	return &BooleanLiteral{Token: p.curToken, Value: p.curTokenIs(TOKEN_TRUE)}
}

func (p *Parser) parsePrefixExpression() Expression {
	tok := p.curToken
	op := p.curToken.Literal
	p.nextToken()
	right := p.parseExpression(PREFIX)

	// Fold negative integer literal directly so -9223372036854775808 works seamlessly
	if op == "-" {
		if il, ok := right.(*IntegerLiteral); ok {
			if il.Value == math.MinInt64 {
				return il
			}
			return &IntegerLiteral{Token: tok, Value: -il.Value}
		}
	} else if op == "+" {
		if il, ok := right.(*IntegerLiteral); ok {
			return il
		}
	}

	return &PrefixExpression{
		Token:    tok,
		Operator: op,
		Right:    right,
	}
}

func (p *Parser) parseInfixExpression(left Expression) Expression {
	expr := &InfixExpression{
		Token:    p.curToken,
		Operator: p.curToken.Literal,
		Left:     left,
	}
	precedence := p.curPrecedence()
	p.nextToken()
	expr.Right = p.parseExpression(precedence)
	return expr
}

func (p *Parser) parseGroupedExpression() Expression {
	p.nextToken()
	exp := p.parseExpression(LOWEST)
	if !p.expectPeek(TOKEN_RPAREN) {
		return nil
	}
	return exp
}

func (p *Parser) parseIfExpression() Expression {
	expr := &IfExpression{Token: p.curToken}

	hasParen := false
	if p.peekTokenIs(TOKEN_LPAREN) {
		p.nextToken()
		hasParen = true
	}
	p.nextToken()
	expr.Condition = p.parseExpression(LOWEST)

	if hasParen {
		if !p.expectPeek(TOKEN_RPAREN) {
			return nil
		}
	}
	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}
	expr.Consequence = p.parseBlockStatement()

	if p.peekTokenIs(TOKEN_ELSE) {
		p.nextToken()
		if p.peekTokenIs(TOKEN_IF) {
			p.nextToken()
			nestedIf := p.parseIfExpression()
			expr.Alternative = &BlockStatement{
				Token:      p.curToken,
				Statements: []Statement{&ExpressionStatement{Token: p.curToken, Expression: nestedIf}},
			}
			return expr
		}
		if !p.expectPeek(TOKEN_LBRACE) {
			return nil
		}
		expr.Alternative = p.parseBlockStatement()
	}

	return expr
}

func (p *Parser) parseBlockStatement() *BlockStatement {
	block := &BlockStatement{Token: p.curToken, Statements: []Statement{}}
	p.nextToken()

	for !p.curTokenIs(TOKEN_RBRACE) && !p.curTokenIs(TOKEN_EOF) {
		if p.curTokenIs(TOKEN_SEMI) {
			p.nextToken()
			continue
		}
		stmt := p.parseStatement()
		if stmt != nil {
			block.Statements = append(block.Statements, stmt)
		}
		p.nextToken()
	}
	return block
}

func (p *Parser) parseCallExpression(function Expression) Expression {
	exp := &CallExpression{Token: p.curToken, Function: function}
	exp.Arguments = p.parseCallArguments()
	return exp
}

func (p *Parser) parseCallArguments() []Expression {
	args := []Expression{}
	if p.peekTokenIs(TOKEN_RPAREN) {
		p.nextToken()
		return args
	}

	p.nextToken()
	args = append(args, p.parseExpression(LOWEST))

	for p.peekTokenIs(TOKEN_COMMA) {
		p.nextToken()
		p.nextToken()
		args = append(args, p.parseExpression(LOWEST))
	}

	if !p.expectPeek(TOKEN_RPAREN) {
		return nil
	}
	return args
}
