#[derive(Debug, Clone)]
pub struct Program {
    pub statements: Vec<Statement>,
}

#[derive(Debug, Clone)]
pub struct BlockStatement {
    pub statements: Vec<Statement>,
}

#[derive(Debug, Clone)]
pub enum Statement {
    Let {
        name: String,
        value: Expression,
    },
    Assign {
        name: String,
        value: Expression,
    },
    Return {
        value: Option<Expression>,
    },
    While {
        condition: Expression,
        body: BlockStatement,
    },
    Function {
        name: String,
        parameters: Vec<String>,
        body: BlockStatement,
    },
    Expression(Expression),
    Block(BlockStatement),
}

#[derive(Debug, Clone)]
pub enum Expression {
    Identifier(String),
    IntegerLiteral(i64),
    FloatLiteral(f64),
    StringLiteral(String),
    BooleanLiteral(bool),
    Prefix {
        operator: String,
        right: Box<Expression>,
    },
    Infix {
        left: Box<Expression>,
        operator: String,
        right: Box<Expression>,
    },
    If {
        condition: Box<Expression>,
        consequence: BlockStatement,
        alternative: Option<BlockStatement>,
    },
    Call {
        function: Box<Expression>,
        arguments: Vec<Expression>,
    },
}
