use crate::ast::{BlockStatement, Expression, Program, Statement};
use crate::lexer::{Lexer, Token, TokenType};

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
pub enum Precedence {
    Lowest = 1,
    LogicalOr,
    LogicalAnd,
    Equals,
    LessGreater,
    Sum,
    Product,
    Prefix,
    Call,
}

fn token_precedence(tt: TokenType) -> Precedence {
    match tt {
        TokenType::Or => Precedence::LogicalOr,
        TokenType::And => Precedence::LogicalAnd,
        TokenType::Eq | TokenType::NotEq => Precedence::Equals,
        TokenType::Lt | TokenType::Gt | TokenType::Lte | TokenType::Gte => Precedence::LessGreater,
        TokenType::Plus | TokenType::Minus => Precedence::Sum,
        TokenType::Slash | TokenType::Asterisk | TokenType::Percent => Precedence::Product,
        TokenType::LParen => Precedence::Call,
        _ => Precedence::Lowest,
    }
}

pub struct Parser<'a> {
    l: Lexer<'a>,
    pub errors: Vec<String>,
    cur_token: Token,
    peek_token: Token,
}

impl<'a> Parser<'a> {
    pub fn new(mut l: Lexer<'a>) -> Self {
        let cur_token = l.next_token();
        let peek_token = l.next_token();
        Parser {
            l,
            errors: Vec::new(),
            cur_token,
            peek_token,
        }
    }

    fn next_token(&mut self) {
        self.cur_token = std::mem::replace(&mut self.peek_token, self.l.next_token());
    }

    fn cur_token_is(&self, t: TokenType) -> bool {
        self.cur_token.token_type == t
    }

    fn peek_token_is(&self, t: TokenType) -> bool {
        self.peek_token.token_type == t
    }

    fn expect_peek(&mut self, t: TokenType) -> bool {
        if self.peek_token_is(t) {
            self.next_token();
            true
        } else {
            self.errors.push(format!(
                "line {}: expected next token to be {}, got {} instead",
                self.peek_token.line, t, self.peek_token.token_type
            ));
            false
        }
    }

    fn peek_precedence(&self) -> Precedence {
        token_precedence(self.peek_token.token_type)
    }

    fn cur_precedence(&self) -> Precedence {
        token_precedence(self.cur_token.token_type)
    }

    pub fn parse_program(&mut self) -> Program {
        let mut statements = Vec::new();
        while !self.cur_token_is(TokenType::Eof) {
            if self.cur_token_is(TokenType::Semi) {
                self.next_token();
                continue;
            }
            if let Some(stmt) = self.parse_statement() {
                statements.push(stmt);
            }
            self.next_token();
        }
        Program { statements }
    }

    fn parse_statement(&mut self) -> Option<Statement> {
        match self.cur_token.token_type {
            TokenType::Let => self.parse_let_statement(),
            TokenType::Return => self.parse_return_statement(),
            TokenType::While => self.parse_while_statement(),
            TokenType::Fn => self.parse_function_statement(),
            TokenType::Ident if self.peek_token_is(TokenType::Assign) => {
                self.parse_assign_statement()
            }
            _ => self.parse_expression_statement(),
        }
    }

    fn parse_let_statement(&mut self) -> Option<Statement> {
        if !self.expect_peek(TokenType::Ident) {
            return None;
        }
        let name = self.cur_token.literal.clone();
        if !self.expect_peek(TokenType::Assign) {
            return None;
        }
        self.next_token();
        let value = self.parse_expression(Precedence::Lowest)?;
        if self.peek_token_is(TokenType::Semi) {
            self.next_token();
        }
        Some(Statement::Let { name, value })
    }

    fn parse_assign_statement(&mut self) -> Option<Statement> {
        let name = self.cur_token.literal.clone();
        self.next_token(); // '='
        self.next_token(); // start of value
        let value = self.parse_expression(Precedence::Lowest)?;
        if self.peek_token_is(TokenType::Semi) {
            self.next_token();
        }
        Some(Statement::Assign { name, value })
    }

    fn parse_return_statement(&mut self) -> Option<Statement> {
        if self.peek_token_is(TokenType::Semi) {
            self.next_token();
            return Some(Statement::Return { value: None });
        }
        if self.peek_token_is(TokenType::RBrace) || self.peek_token_is(TokenType::Eof) {
            return Some(Statement::Return { value: None });
        }
        self.next_token();
        let val = self.parse_expression(Precedence::Lowest)?;
        if self.peek_token_is(TokenType::Semi) {
            self.next_token();
        }
        Some(Statement::Return { value: Some(val) })
    }

    fn parse_while_statement(&mut self) -> Option<Statement> {
        let mut has_paren = false;
        if self.peek_token_is(TokenType::LParen) {
            self.next_token();
            has_paren = true;
        }
        self.next_token();
        let condition = self.parse_expression(Precedence::Lowest)?;
        if has_paren && !self.expect_peek(TokenType::RParen) {
            return None;
        }
        if !self.expect_peek(TokenType::LBrace) {
            return None;
        }
        let body = self.parse_block_statement();
        Some(Statement::While { condition, body })
    }

    fn parse_function_statement(&mut self) -> Option<Statement> {
        if !self.expect_peek(TokenType::Ident) {
            return None;
        }
        let name = self.cur_token.literal.clone();
        if !self.expect_peek(TokenType::LParen) {
            return None;
        }
        let parameters = self.parse_function_parameters()?;
        if !self.expect_peek(TokenType::LBrace) {
            return None;
        }
        let body = self.parse_block_statement();
        Some(Statement::Function {
            name,
            parameters,
            body,
        })
    }

    fn parse_function_parameters(&mut self) -> Option<Vec<String>> {
        let mut params = Vec::new();
        if self.peek_token_is(TokenType::RParen) {
            self.next_token();
            return Some(params);
        }
        self.next_token();
        params.push(self.cur_token.literal.clone());
        while self.peek_token_is(TokenType::Comma) {
            self.next_token();
            self.next_token();
            params.push(self.cur_token.literal.clone());
        }
        if !self.expect_peek(TokenType::RParen) {
            return None;
        }
        Some(params)
    }

    fn parse_expression_statement(&mut self) -> Option<Statement> {
        let expr = self.parse_expression(Precedence::Lowest)?;
        if self.peek_token_is(TokenType::Semi) {
            self.next_token();
        }
        Some(Statement::Expression(expr))
    }

    fn parse_expression(&mut self, precedence: Precedence) -> Option<Expression> {
        let mut left = match self.cur_token.token_type {
            TokenType::Ident => Some(Expression::Identifier(self.cur_token.literal.clone())),
            TokenType::Int => self.parse_integer_literal(),
            TokenType::Float => self.parse_float_literal(),
            TokenType::String => Some(Expression::StringLiteral(self.cur_token.literal.clone())),
            TokenType::True => Some(Expression::BooleanLiteral(true)),
            TokenType::False => Some(Expression::BooleanLiteral(false)),
            TokenType::Minus | TokenType::Plus | TokenType::Bang => self.parse_prefix_expression(),
            TokenType::LParen => self.parse_grouped_expression(),
            TokenType::If => self.parse_if_expression(),
            _ => {
                self.errors.push(format!(
                    "line {}: no prefix parse function for {} ({:?})",
                    self.cur_token.line, self.cur_token.token_type, self.cur_token.literal
                ));
                None
            }
        }?;

        while !self.peek_token_is(TokenType::Semi) && precedence < self.peek_precedence() {
            match self.peek_token.token_type {
                TokenType::Plus
                | TokenType::Minus
                | TokenType::Slash
                | TokenType::Asterisk
                | TokenType::Percent
                | TokenType::Eq
                | TokenType::NotEq
                | TokenType::Lt
                | TokenType::Gt
                | TokenType::Lte
                | TokenType::Gte
                | TokenType::And
                | TokenType::Or => {
                    self.next_token();
                    left = self.parse_infix_expression(left)?;
                }
                TokenType::LParen => {
                    self.next_token();
                    left = self.parse_call_expression(left)?;
                }
                _ => break,
            }
        }

        Some(left)
    }

    fn parse_integer_literal(&mut self) -> Option<Expression> {
        if let Ok(v) = self.cur_token.literal.parse::<i64>() {
            return Some(Expression::IntegerLiteral(v));
        }
        if let Ok(uv) = self.cur_token.literal.parse::<u64>() {
            if uv == 9223372036854775808u64 {
                return Some(Expression::IntegerLiteral(i64::MIN));
            }
        }
        self.errors.push(format!(
            "line {}: could not parse {:?} as 64-bit integer",
            self.cur_token.line, self.cur_token.literal
        ));
        None
    }

    fn parse_float_literal(&mut self) -> Option<Expression> {
        match self.cur_token.literal.parse::<f64>() {
            Ok(v) => Some(Expression::FloatLiteral(v)),
            Err(_) => {
                self.errors.push(format!(
                    "line {}: could not parse {:?} as float",
                    self.cur_token.line, self.cur_token.literal
                ));
                None
            }
        }
    }

    fn parse_prefix_expression(&mut self) -> Option<Expression> {
        let op = self.cur_token.literal.clone();
        self.next_token();
        let right = self.parse_expression(Precedence::Prefix)?;

        if op == "-" {
            if let Expression::IntegerLiteral(v) = right {
                return Some(Expression::IntegerLiteral(if v == i64::MIN {
                    i64::MIN
                } else {
                    v.wrapping_neg()
                }));
            }
            if let Expression::FloatLiteral(v) = right {
                return Some(Expression::FloatLiteral(-v));
            }
        } else if op == "+" {
            if matches!(right, Expression::IntegerLiteral(_) | Expression::FloatLiteral(_)) {
                return Some(right);
            }
        }

        Some(Expression::Prefix {
            operator: op,
            right: Box::new(right),
        })
    }

    fn parse_infix_expression(&mut self, left: Expression) -> Option<Expression> {
        let operator = self.cur_token.literal.clone();
        let precedence = self.cur_precedence();
        self.next_token();
        let right = self.parse_expression(precedence)?;
        Some(Expression::Infix {
            left: Box::new(left),
            operator,
            right: Box::new(right),
        })
    }

    fn parse_grouped_expression(&mut self) -> Option<Expression> {
        self.next_token();
        let exp = self.parse_expression(Precedence::Lowest)?;
        if !self.expect_peek(TokenType::RParen) {
            return None;
        }
        Some(exp)
    }

    fn parse_if_expression(&mut self) -> Option<Expression> {
        let mut has_paren = false;
        if self.peek_token_is(TokenType::LParen) {
            self.next_token();
            has_paren = true;
        }
        self.next_token();
        let condition = self.parse_expression(Precedence::Lowest)?;
        if has_paren && !self.expect_peek(TokenType::RParen) {
            return None;
        }
        if !self.expect_peek(TokenType::LBrace) {
            return None;
        }
        let consequence = self.parse_block_statement();

        let mut alternative = None;
        if self.peek_token_is(TokenType::Else) {
            self.next_token();
            if self.peek_token_is(TokenType::If) {
                self.next_token();
                let nested_if = self.parse_if_expression()?;
                alternative = Some(BlockStatement {
                    statements: vec![Statement::Expression(nested_if)],
                });
            } else {
                if !self.expect_peek(TokenType::LBrace) {
                    return None;
                }
                alternative = Some(self.parse_block_statement());
            }
        }

        Some(Expression::If {
            condition: Box::new(condition),
            consequence,
            alternative,
        })
    }

    fn parse_block_statement(&mut self) -> BlockStatement {
        let mut statements = Vec::new();
        self.next_token();
        while !self.cur_token_is(TokenType::RBrace) && !self.cur_token_is(TokenType::Eof) {
            if self.cur_token_is(TokenType::Semi) {
                self.next_token();
                continue;
            }
            if let Some(stmt) = self.parse_statement() {
                statements.push(stmt);
            }
            self.next_token();
        }
        BlockStatement { statements }
    }

    fn parse_call_expression(&mut self, function: Expression) -> Option<Expression> {
        let arguments = self.parse_call_arguments()?;
        Some(Expression::Call {
            function: Box::new(function),
            arguments,
        })
    }

    fn parse_call_arguments(&mut self) -> Option<Vec<Expression>> {
        let mut args = Vec::new();
        if self.peek_token_is(TokenType::RParen) {
            self.next_token();
            return Some(args);
        }
        self.next_token();
        args.push(self.parse_expression(Precedence::Lowest)?);
        while self.peek_token_is(TokenType::Comma) {
            self.next_token();
            self.next_token();
            args.push(self.parse_expression(Precedence::Lowest)?);
        }
        if !self.expect_peek(TokenType::RParen) {
            return None;
        }
        Some(args)
    }
}
