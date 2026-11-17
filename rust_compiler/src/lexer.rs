#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum TokenType {
    Illegal,
    Eof,

    Ident,
    Int,
    Float,
    String,

    Assign,   // =
    Plus,     // +
    Minus,    // -
    Asterisk, // *
    Slash,    // /
    Percent,  // %
    Bang,     // !
    Lt,       // <
    Gt,       // >
    LParen,   // (
    RParen,   // )
    LBrace,   // {
    RBrace,   // }
    Comma,    // ,
    Semi,     // ;

    Eq,    // ==
    NotEq, // !=
    Lte,   // <=
    Gte,   // >=
    And,   // &&
    Or,    // ||

    Let,
    Fn,
    Return,
    If,
    Else,
    While,
    True,
    False,
}

impl std::fmt::Display for TokenType {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        let s = match self {
            TokenType::Illegal => "ILLEGAL",
            TokenType::Eof => "EOF",
            TokenType::Ident => "IDENT",
            TokenType::Int => "INT",
            TokenType::Float => "FLOAT",
            TokenType::String => "STRING",
            TokenType::Assign => "=",
            TokenType::Plus => "+",
            TokenType::Minus => "-",
            TokenType::Asterisk => "*",
            TokenType::Slash => "/",
            TokenType::Percent => "%",
            TokenType::Bang => "!",
            TokenType::Lt => "<",
            TokenType::Gt => ">",
            TokenType::LParen => "(",
            TokenType::RParen => ")",
            TokenType::LBrace => "{",
            TokenType::RBrace => "}",
            TokenType::Comma => ",",
            TokenType::Semi => ";",
            TokenType::Eq => "==",
            TokenType::NotEq => "!=",
            TokenType::Lte => "<=",
            TokenType::Gte => ">=",
            TokenType::And => "&&",
            TokenType::Or => "||",
            TokenType::Let => "LET",
            TokenType::Fn => "FN",
            TokenType::Return => "RETURN",
            TokenType::If => "IF",
            TokenType::Else => "ELSE",
            TokenType::While => "WHILE",
            TokenType::True => "TRUE",
            TokenType::False => "FALSE",
        };
        write!(f, "{}", s)
    }
}

#[derive(Debug, Clone)]
pub struct Token {
    pub token_type: TokenType,
    pub literal: String,
    pub line: usize,
}

pub struct Lexer<'a> {
    input: &'a [u8],
    pos: usize,
    read_pos: usize,
    ch: u8,
    line: usize,
}

impl<'a> Lexer<'a> {
    pub fn new(input: &'a str) -> Self {
        let mut l = Lexer {
            input: input.as_bytes(),
            pos: 0,
            read_pos: 0,
            ch: 0,
            line: 1,
        };
        l.read_char();
        l
    }

    fn read_char(&mut self) {
        if self.read_pos >= self.input.len() {
            self.ch = 0;
        } else {
            self.ch = self.input[self.read_pos];
        }
        self.pos = self.read_pos;
        self.read_pos += 1;
    }

    fn peek_char(&self) -> u8 {
        if self.read_pos >= self.input.len() {
            0
        } else {
            self.input[self.read_pos]
        }
    }

    fn skip_whitespace_and_comments(&mut self) {
        loop {
            while matches!(self.ch, b' ' | b'\t' | b'\n' | b'\r') {
                if self.ch == b'\n' {
                    self.line += 1;
                }
                self.read_char();
            }

            if (self.ch == b'/' && self.peek_char() == b'/') || self.ch == b'#' {
                while self.ch != b'\n' && self.ch != 0 {
                    self.read_char();
                }
                continue;
            }

            if self.ch == b'/' && self.peek_char() == b'*' {
                self.read_char();
                self.read_char();
                while self.ch != 0 {
                    if self.ch == b'\n' {
                        self.line += 1;
                    }
                    if self.ch == b'*' && self.peek_char() == b'/' {
                        self.read_char();
                        self.read_char();
                        break;
                    }
                    self.read_char();
                }
                continue;
            }

            break;
        }
    }

    fn read_string(&mut self) -> Token {
        let line = self.line;
        let mut sb = Vec::new();
        self.read_char(); // consume opening '"'
        while self.ch != b'"' && self.ch != 0 {
            if self.ch == b'\\' {
                self.read_char();
                match self.ch {
                    b'n' => sb.push(b'\n'),
                    b't' => sb.push(b'\t'),
                    b'r' => sb.push(b'\r'),
                    b'0' => sb.push(0),
                    other => sb.push(other),
                }
            } else {
                if self.ch == b'\n' {
                    self.line += 1;
                }
                sb.push(self.ch);
            }
            self.read_char();
        }
        if self.ch == b'"' {
            self.read_char();
        }
        Token {
            token_type: TokenType::String,
            literal: String::from_utf8_lossy(&sb).into_owned(),
            line,
        }
    }

    pub fn next_token(&mut self) -> Token {
        self.skip_whitespace_and_comments();
        let line = self.line;

        match self.ch {
            0 => {
                return Token {
                    token_type: TokenType::Eof,
                    literal: String::new(),
                    line,
                }
            }
            b'"' => return self.read_string(),
            b'=' => {
                if self.peek_char() == b'=' {
                    self.read_char();
                    self.read_char();
                    return Token {
                        token_type: TokenType::Eq,
                        literal: "==".to_string(),
                        line,
                    };
                }
                self.read_char();
                return Token {
                    token_type: TokenType::Assign,
                    literal: "=".to_string(),
                    line,
                };
            }
            b'!' => {
                if self.peek_char() == b'=' {
                    self.read_char();
                    self.read_char();
                    return Token {
                        token_type: TokenType::NotEq,
                        literal: "!=".to_string(),
                        line,
                    };
                }
                self.read_char();
                return Token {
                    token_type: TokenType::Bang,
                    literal: "!".to_string(),
                    line,
                };
            }
            b'<' => {
                if self.peek_char() == b'=' {
                    self.read_char();
                    self.read_char();
                    return Token {
                        token_type: TokenType::Lte,
                        literal: "<=".to_string(),
                        line,
                    };
                }
                self.read_char();
                return Token {
                    token_type: TokenType::Lt,
                    literal: "<".to_string(),
                    line,
                };
            }
            b'>' => {
                if self.peek_char() == b'=' {
                    self.read_char();
                    self.read_char();
                    return Token {
                        token_type: TokenType::Gte,
                        literal: ">=".to_string(),
                        line,
                    };
                }
                self.read_char();
                return Token {
                    token_type: TokenType::Gt,
                    literal: ">".to_string(),
                    line,
                };
            }
            b'&' if self.peek_char() == b'&' => {
                self.read_char();
                self.read_char();
                return Token {
                    token_type: TokenType::And,
                    literal: "&&".to_string(),
                    line,
                };
            }
            b'|' if self.peek_char() == b'|' => {
                self.read_char();
                self.read_char();
                return Token {
                    token_type: TokenType::Or,
                    literal: "||".to_string(),
                    line,
                };
            }
            _ => {}
        }

        let single = match self.ch {
            b'+' => Some(TokenType::Plus),
            b'-' => Some(TokenType::Minus),
            b'*' => Some(TokenType::Asterisk),
            b'/' => Some(TokenType::Slash),
            b'%' => Some(TokenType::Percent),
            b'(' => Some(TokenType::LParen),
            b')' => Some(TokenType::RParen),
            b'{' => Some(TokenType::LBrace),
            b'}' => Some(TokenType::RBrace),
            b',' => Some(TokenType::Comma),
            b';' => Some(TokenType::Semi),
            _ => None,
        };
        if let Some(tt) = single {
            let lit = (self.ch as char).to_string();
            self.read_char();
            return Token {
                token_type: tt,
                literal: lit,
                line,
            };
        }

        if is_letter(self.ch) {
            let start = self.pos;
            while is_letter_or_digit(self.ch) {
                self.read_char();
            }
            let lit = String::from_utf8_lossy(&self.input[start..self.pos]).into_owned();
            let tt = match lit.as_str() {
                "let" | "var" => TokenType::Let,
                "fn" | "func" => TokenType::Fn,
                "return" => TokenType::Return,
                "if" => TokenType::If,
                "else" => TokenType::Else,
                "while" => TokenType::While,
                "true" => TokenType::True,
                "false" => TokenType::False,
                _ => TokenType::Ident,
            };
            return Token {
                token_type: tt,
                literal: lit,
                line,
            };
        }

        if is_digit(self.ch) {
            let start = self.pos;
            while is_digit(self.ch) {
                self.read_char();
            }
            let mut is_float = false;
            if self.ch == b'.' && is_digit(self.peek_char()) {
                is_float = true;
                self.read_char();
                while is_digit(self.ch) {
                    self.read_char();
                }
            }
            if self.ch == b'e' || self.ch == b'E' {
                let n1 = self.peek_char();
                let n2 = if self.read_pos + 1 < self.input.len() {
                    self.input[self.read_pos + 1]
                } else {
                    0
                };
                if is_digit(n1) || ((n1 == b'+' || n1 == b'-') && is_digit(n2)) {
                    is_float = true;
                    self.read_char();
                    if self.ch == b'+' || self.ch == b'-' {
                        self.read_char();
                    }
                    while is_digit(self.ch) {
                        self.read_char();
                    }
                }
            }
            let lit = String::from_utf8_lossy(&self.input[start..self.pos]).into_owned();
            return Token {
                token_type: if is_float { TokenType::Float } else { TokenType::Int },
                literal: lit,
                line,
            };
        }

        let lit = (self.ch as char).to_string();
        self.read_char();
        Token {
            token_type: TokenType::Illegal,
            literal: lit,
            line,
        }
    }
}

#[inline]
fn is_letter(ch: u8) -> bool {
    ch.is_ascii_alphabetic() || ch == b'_'
}

#[inline]
fn is_digit(ch: u8) -> bool {
    ch.is_ascii_digit()
}

#[inline]
fn is_letter_or_digit(ch: u8) -> bool {
    is_letter(ch) || is_digit(ch)
}
