use crate::ast::{BlockStatement, Expression, Program, Statement};
use crate::runtime::C_RUNTIME;
use std::collections::HashMap;
use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::path::Path;
use std::process::Command;
use std::time::{SystemTime, UNIX_EPOCH};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CType {
    Int,
    Float,
    Str,
}

impl CType {
    fn c_name(self) -> &'static str {
        match self {
            CType::Float => "double",
            CType::Str => "kstr",
            CType::Int => "int64_t",
        }
    }
}

fn convert(code: &str, from: CType, to: CType) -> String {
    if from == to || from == CType::Str || to == CType::Str {
        return code.to_string();
    }
    if to == CType::Float {
        format!("((double)({}))", code)
    } else {
        format!("((int64_t)({}))", code)
    }
}

fn to_str(code: &str, t: CType) -> String {
    match t {
        CType::Str => code.to_string(),
        CType::Float => format!("_kpj_str_float(ctx, {})", code),
        CType::Int => format!("_kpj_str_int(ctx, {})", code),
    }
}

fn c_escape(v: &str) -> String {
    let mut sb = String::with_capacity(v.len() + 2);
    sb.push('"');
    for &c in v.as_bytes() {
        if c == b'"' || c == b'\\' {
            sb.push('\\');
            sb.push(c as char);
        } else if (32..127).contains(&c) && c != b'?' {
            sb.push(c as char);
        } else {
            sb.push_str(&format!("\\{:03o}", c));
        }
    }
    sb.push('"');
    sb
}

fn float_lit(v: f64) -> String {
    if v.is_infinite() {
        return if v.is_sign_positive() {
            "(1.0/0.0)".to_string()
        } else {
            "(-1.0/0.0)".to_string()
        };
    }
    if v.is_nan() {
        return "(0.0/0.0)".to_string();
    }
    let mut s = format!("{:.17e}", v);
    if !s.contains('.') && !s.contains('e') && !s.contains('E') {
        s.push_str(".0");
    }
    if v < 0.0 || (v == 0.0 && v.is_sign_negative()) {
        format!("({})", s)
    } else {
        s
    }
}

pub struct CodeGenerator {
    functions: String,
    main_body: String,
    scopes: Vec<HashMap<String, CType>>,
    indent: usize,
    in_func: bool,
    temp_count: usize,
}

impl CodeGenerator {
    pub fn new() -> Self {
        CodeGenerator {
            functions: String::new(),
            main_body: String::new(),
            scopes: vec![HashMap::new()],
            indent: 1,
            in_func: false,
            temp_count: 0,
        }
    }

    fn push_scope(&mut self) {
        self.scopes.push(HashMap::new());
    }

    fn pop_scope(&mut self) {
        if self.scopes.len() > 1 {
            self.scopes.pop();
        }
    }

    fn lookup_current(&self, name: &str) -> Option<CType> {
        self.scopes.last()?.get(name).copied()
    }

    fn lookup(&self, name: &str) -> Option<CType> {
        for s in self.scopes.iter().rev() {
            if let Some(&t) = s.get(name) {
                return Some(t);
            }
        }
        None
    }

    fn declare_var(&mut self, name: &str, t: CType) {
        if let Some(last) = self.scopes.last_mut() {
            last.insert(name.to_string(), t);
        }
    }

    fn next_temp(&mut self) -> String {
        self.temp_count += 1;
        format!("_kpj_tmp_{}", self.temp_count)
    }

    fn buf(&mut self) -> &mut String {
        if self.in_func {
            &mut self.functions
        } else {
            &mut self.main_body
        }
    }

    fn write_indent(&mut self) {
        let ind = "    ".repeat(self.indent);
        self.buf().push_str(&ind);
    }

    fn c_var_name(name: &str) -> String {
        format!("v_{}", name)
    }

    fn c_func_name(name: &str) -> String {
        format!("fn_{}", name)
    }

    fn gen_statement(&mut self, stmt: &Statement) {
        match stmt {
            Statement::Let { name, value } => {
                let (val_expr, vt) = self.gen_expr(value);
                let c_name = Self::c_var_name(name);
                if let Some(existing_t) = self.lookup_current(name) {
                    self.write_indent();
                    let conv = convert(&val_expr, vt, existing_t);
                    self.buf().push_str(&format!("{} = {};\n", c_name, conv));
                } else {
                    self.declare_var(name, vt);
                    self.write_indent();
                    self.buf()
                        .push_str(&format!("{} {} = {};\n", vt.c_name(), c_name, val_expr));
                }
            }
            Statement::Assign { name, value } => {
                let (val_expr, vt) = self.gen_expr(value);
                let c_name = Self::c_var_name(name);
                if let Some(existing_t) = self.lookup(name) {
                    self.write_indent();
                    let conv = convert(&val_expr, vt, existing_t);
                    self.buf().push_str(&format!("{} = {};\n", c_name, conv));
                } else {
                    self.declare_var(name, vt);
                    self.write_indent();
                    self.buf()
                        .push_str(&format!("{} {} = {};\n", vt.c_name(), c_name, val_expr));
                }
            }
            Statement::Return { value } => {
                if let Some(expr) = value {
                    let (val_expr, vt) = self.gen_expr(expr);
                    let conv = convert(&val_expr, vt, CType::Int);
                    self.write_indent();
                    if self.in_func {
                        self.buf().push_str(&format!("return {};\n", conv));
                    } else {
                        self.buf()
                            .push_str(&format!("_kpj_exit(ctx, (int)({}));\n", conv));
                    }
                } else {
                    self.write_indent();
                    if self.in_func {
                        self.buf().push_str("return 0;\n");
                    } else {
                        self.buf().push_str("_kpj_exit(ctx, 0);\n");
                    }
                }
            }
            Statement::While { condition, body } => {
                let (cond_expr, _) = self.gen_expr(condition);
                self.write_indent();
                self.buf().push_str(&format!("while ({}) {{\n", cond_expr));
                self.indent += 1;
                self.push_scope();
                for s in &body.statements {
                    self.gen_statement(s);
                }
                self.pop_scope();
                self.indent -= 1;
                self.write_indent();
                self.buf().push_str("}\n");
            }
            Statement::Function {
                name,
                parameters,
                body,
            } => {
                let prev_in_func = self.in_func;
                let prev_indent = self.indent;
                self.in_func = true;

                self.push_scope();
                let mut params = Vec::with_capacity(parameters.len() + 1);
                params.push("KpjCtx *ctx".to_string());
                for p in parameters {
                    self.declare_var(p, CType::Int);
                    params.push(format!("int64_t {}", Self::c_var_name(p)));
                }
                let param_list = params.join(", ");

                self.functions.push_str(&format!(
                    "static inline int64_t {}({}) {{\n",
                    Self::c_func_name(name),
                    param_list
                ));
                self.indent = 1;
                for s in &body.statements {
                    self.gen_statement(s);
                }
                self.functions.push_str("    return 0;\n}\n\n");
                self.pop_scope();

                self.in_func = prev_in_func;
                self.indent = prev_indent;
            }
            Statement::Expression(expr) => {
                if let Expression::If {
                    condition,
                    consequence,
                    alternative,
                } = expr
                {
                    self.gen_if_statement(condition, consequence, alternative.as_ref());
                    return;
                }
                let (expr_str, _) = self.gen_expr(expr);
                self.write_indent();
                self.buf().push_str(&format!("(void)({});\n", expr_str));
            }
            Statement::Block(block) => {
                self.write_indent();
                self.buf().push_str("{\n");
                self.indent += 1;
                self.push_scope();
                for s in &block.statements {
                    self.gen_statement(s);
                }
                self.pop_scope();
                self.indent -= 1;
                self.write_indent();
                self.buf().push_str("}\n");
            }
        }
    }

    fn gen_if_statement(
        &mut self,
        condition: &Expression,
        consequence: &BlockStatement,
        alternative: Option<&BlockStatement>,
    ) {
        let (cond, _) = self.gen_expr(condition);
        self.write_indent();
        self.buf().push_str(&format!("if ({}) {{\n", cond));
        self.indent += 1;
        self.push_scope();
        for s in &consequence.statements {
            self.gen_statement(s);
        }
        self.pop_scope();
        self.indent -= 1;
        if let Some(alt) = alternative {
            self.write_indent();
            self.buf().push_str("} else {\n");
            self.indent += 1;
            self.push_scope();
            for s in &alt.statements {
                self.gen_statement(s);
            }
            self.pop_scope();
            self.indent -= 1;
        }
        self.write_indent();
        self.buf().push_str("}\n");
    }

    fn gen_block_with_result(&mut self, block: &BlockStatement, result_var: &str) {
        let len = block.statements.len();
        for (i, s) in block.statements.iter().enumerate() {
            if i + 1 == len {
                if let Statement::Expression(expr) = s {
                    let (val, vt) = self.gen_expr(expr);
                    let conv = convert(&val, vt, CType::Int);
                    self.write_indent();
                    self.buf()
                        .push_str(&format!("{} = {};\n", result_var, conv));
                    continue;
                }
            }
            self.gen_statement(s);
        }
    }

    fn gen_expr(&mut self, expr: &Expression) -> (String, CType) {
        match expr {
            Expression::IntegerLiteral(v) => {
                if *v == i64::MIN {
                    ("(-9223372036854775807LL - 1LL)".to_string(), CType::Int)
                } else {
                    (format!("{}LL", v), CType::Int)
                }
            }
            Expression::FloatLiteral(v) => (float_lit(*v), CType::Float),
            Expression::StringLiteral(s) => (
                format!("_kpj_mk({}, {})", c_escape(s), s.len()),
                CType::Str,
            ),
            Expression::BooleanLiteral(b) => {
                (if *b { "1LL" } else { "0LL" }.to_string(), CType::Int)
            }
            Expression::Identifier(name) => {
                let t = self.lookup(name).unwrap_or(CType::Int);
                (Self::c_var_name(name), t)
            }
            Expression::Prefix { operator, right } => {
                let (r_code, rt) = self.gen_expr(right);
                match operator.as_str() {
                    "-" => {
                        if rt == CType::Float {
                            (format!("(-({}))", r_code), CType::Float)
                        } else {
                            (format!("((int64_t)(0 - (uint64_t)({})))", r_code), CType::Int)
                        }
                    }
                    "+" => (r_code, rt),
                    "!" => (format!("((int64_t)!({}))", r_code), CType::Int),
                    _ => (format!("({}({}))", operator, r_code), rt),
                }
            }
            Expression::Infix {
                left,
                operator,
                right,
            } => {
                let (l_code, lt) = self.gen_expr(left);
                let (r_code, rt) = self.gen_expr(right);
                let is_f = lt == CType::Float || rt == CType::Float;
                if lt == CType::Str || rt == CType::Str {
                    let ls = to_str(&l_code, lt);
                    let rs = to_str(&r_code, rt);
                    return match operator.as_str() {
                        "+" => (format!("_kpj_cat(ctx, {}, {})", ls, rs), CType::Str),
                        "==" => (format!("_kpj_seq({}, {})", ls, rs), CType::Int),
                        "!=" => (format!("(!_kpj_seq({}, {}))", ls, rs), CType::Int),
                        "<" | ">" | "<=" | ">=" => (
                            format!("((int64_t)(_kpj_scmp({}, {}) {} 0))", ls, rs, operator),
                            CType::Int,
                        ),
                        "&&" | "||" => (
                            format!("((int64_t)(({}) {} ({})))", l_code, operator, r_code),
                            CType::Int,
                        ),
                        _ => ("0LL".to_string(), CType::Int),
                    };
                }
                match operator.as_str() {
                    "+" | "-" | "*" => {
                        if is_f {
                            (
                                format!(
                                    "({} {} {})",
                                    convert(&l_code, lt, CType::Float),
                                    operator,
                                    convert(&r_code, rt, CType::Float)
                                ),
                                CType::Float,
                            )
                        } else {
                            (
                                format!(
                                    "((int64_t)((uint64_t)({}) {} (uint64_t)({})))",
                                    l_code, operator, r_code
                                ),
                                CType::Int,
                            )
                        }
                    }
                    "/" => {
                        if is_f {
                            (
                                format!(
                                    "({} / {})",
                                    convert(&l_code, lt, CType::Float),
                                    convert(&r_code, rt, CType::Float)
                                ),
                                CType::Float,
                            )
                        } else {
                            (format!("_kpj_div({}, {})", l_code, r_code), CType::Int)
                        }
                    }
                    "%" => {
                        if is_f {
                            (
                                format!(
                                    "_kpj_fmod({}, {})",
                                    convert(&l_code, lt, CType::Float),
                                    convert(&r_code, rt, CType::Float)
                                ),
                                CType::Float,
                            )
                        } else {
                            (format!("_kpj_mod({}, {})", l_code, r_code), CType::Int)
                        }
                    }
                    _ => {
                        if is_f {
                            (
                                format!(
                                    "((int64_t)(({}) {} ({})))",
                                    convert(&l_code, lt, CType::Float),
                                    operator,
                                    convert(&r_code, rt, CType::Float)
                                ),
                                CType::Int,
                            )
                        } else {
                            (
                                format!("((int64_t)(({}) {} ({})))", l_code, operator, r_code),
                                CType::Int,
                            )
                        }
                    }
                }
            }
            Expression::If {
                condition,
                consequence,
                alternative,
            } => {
                let tmp = self.next_temp();
                self.write_indent();
                self.buf().push_str(&format!("int64_t {} = 0;\n", tmp));
                let (cond, _) = self.gen_expr(condition);
                self.write_indent();
                self.buf().push_str(&format!("if ({}) {{\n", cond));
                self.indent += 1;
                self.push_scope();
                self.gen_block_with_result(consequence, &tmp);
                self.pop_scope();
                self.indent -= 1;
                if let Some(alt) = alternative {
                    self.write_indent();
                    self.buf().push_str("} else {\n");
                    self.indent += 1;
                    self.push_scope();
                    self.gen_block_with_result(alt, &tmp);
                    self.pop_scope();
                    self.indent -= 1;
                }
                self.write_indent();
                self.buf().push_str("}\n");
                (tmp, CType::Int)
            }
            Expression::Call {
                function,
                arguments,
            } => self.gen_call_expression(function, arguments),
        }
    }

    fn gen_call_expression(
        &mut self,
        function: &Expression,
        arguments: &[Expression],
    ) -> (String, CType) {
        let ident = match function {
            Expression::Identifier(name) => name.as_str(),
            _ => return ("0LL".to_string(), CType::Int),
        };

        let mut args = Vec::with_capacity(arguments.len());
        let mut types = Vec::with_capacity(arguments.len());
        for a in arguments {
            let (code, t) = self.gen_expr(a);
            args.push(code);
            types.push(t);
        }

        match ident {
            "read_int" | "scan_int" | "input_int" | "read" => {
                ("_kpj_read_int(ctx)".to_string(), CType::Int)
            }
            "read_float" | "scan_float" | "input_float" | "read_double" => {
                ("_kpj_read_float(ctx)".to_string(), CType::Float)
            }
            "arg_int" | "arg" => {
                if !args.is_empty() {
                    (
                        format!(
                            "_kpj_arg_int(ctx, {})",
                            convert(&args[0], types[0], CType::Int)
                        ),
                        CType::Int,
                    )
                } else {
                    ("0LL".to_string(), CType::Int)
                }
            }
            "argc" | "arg_count" => ("_kpj_argc_fn(ctx)".to_string(), CType::Int),
            "float" | "to_float" => {
                if !args.is_empty() {
                    (convert(&args[0], types[0], CType::Float), CType::Float)
                } else {
                    ("0.0".to_string(), CType::Float)
                }
            }
            "int" | "to_int" => {
                if !args.is_empty() {
                    (convert(&args[0], types[0], CType::Int), CType::Int)
                } else {
                    ("0LL".to_string(), CType::Int)
                }
            }
            "log" | "ln" | "exp" | "sin" | "cos" | "tan" | "floor" | "ceil" => {
                if !args.is_empty() {
                    let fn_name = if ident == "ln" { "log" } else { ident };
                    (
                        format!(
                            "_kpj_{}({})",
                            fn_name,
                            convert(&args[0], types[0], CType::Float)
                        ),
                        CType::Float,
                    )
                } else {
                    ("0.0".to_string(), CType::Float)
                }
            }
            "pow" => {
                if args.len() >= 2 {
                    (
                        format!(
                            "_kpj_pow({}, {})",
                            convert(&args[0], types[0], CType::Float),
                            convert(&args[1], types[1], CType::Float)
                        ),
                        CType::Float,
                    )
                } else {
                    ("0.0".to_string(), CType::Float)
                }
            }
            "prime_pi" => {
                if !args.is_empty() {
                    (
                        format!(
                            "_kpj_prime_pi(ctx, {})",
                            convert(&args[0], types[0], CType::Int)
                        ),
                        CType::Int,
                    )
                } else {
                    ("0LL".to_string(), CType::Int)
                }
            }
            "is_finite" | "isfinite" => {
                if !args.is_empty() {
                    (
                        format!(
                            "((int64_t)__builtin_isfinite({}))",
                            convert(&args[0], types[0], CType::Float)
                        ),
                        CType::Int,
                    )
                } else {
                    ("0LL".to_string(), CType::Int)
                }
            }
            "sqrt" => {
                if !args.is_empty() {
                    (
                        format!("_kpj_sqrt({})", convert(&args[0], types[0], CType::Float)),
                        CType::Float,
                    )
                } else {
                    ("0.0".to_string(), CType::Float)
                }
            }
            "print" | "println" | "print_int" | "print_float" | "print_str" => {
                let mut parts = Vec::with_capacity(2 * args.len() + 2);
                for i in 0..args.len() {
                    if i > 0 {
                        parts.push("_kpj_putc(ctx, ' ')".to_string());
                    }
                    match types[i] {
                        CType::Float => parts.push(format!("_kpj_w_float(ctx, {})", args[i])),
                        CType::Str => parts.push(format!("_kpj_w_str(ctx, {})", args[i])),
                        CType::Int => parts.push(format!("_kpj_w_int(ctx, {})", args[i])),
                    }
                }
                parts.push("_kpj_putc(ctx, '\\n')".to_string());
                parts.push("0LL".to_string());
                (format!("({})", parts.join(", ")), CType::Int)
            }
            "read_token" | "read_word" | "read_str" | "read_string" => {
                ("_kpj_read_token(ctx)".to_string(), CType::Str)
            }
            "read_line" => ("_kpj_read_line(ctx)".to_string(), CType::Str),
            "len" | "length" | "strlen" => {
                if !args.is_empty() {
                    (format!("_kpj_len({})", args[0]), CType::Int)
                } else {
                    ("0LL".to_string(), CType::Int)
                }
            }
            "upper" | "to_upper" => {
                if !args.is_empty() {
                    (format!("_kpj_upper(ctx, {})", args[0]), CType::Str)
                } else {
                    ("_kpj_mk(\"\", 0)".to_string(), CType::Str)
                }
            }
            "lower" | "to_lower" => {
                if !args.is_empty() {
                    (format!("_kpj_lower(ctx, {})", args[0]), CType::Str)
                } else {
                    ("_kpj_mk(\"\", 0)".to_string(), CType::Str)
                }
            }
            "char_at" | "at" => {
                if args.len() >= 2 {
                    (
                        format!(
                            "_kpj_char_at({}, {})",
                            args[0],
                            convert(&args[1], types[1], CType::Int)
                        ),
                        CType::Str,
                    )
                } else {
                    ("_kpj_mk(\"\", 0)".to_string(), CType::Str)
                }
            }
            "substr" | "substring" => {
                if args.len() >= 3 {
                    (
                        format!(
                            "_kpj_substr({}, {}, {})",
                            args[0],
                            convert(&args[1], types[1], CType::Int),
                            convert(&args[2], types[2], CType::Int)
                        ),
                        CType::Str,
                    )
                } else {
                    ("_kpj_mk(\"\", 0)".to_string(), CType::Str)
                }
            }
            "str" | "to_str" => {
                if !args.is_empty() {
                    (to_str(&args[0], types[0]), CType::Str)
                } else {
                    ("_kpj_mk(\"\", 0)".to_string(), CType::Str)
                }
            }
            "exit" => {
                let code = if !args.is_empty() {
                    format!("(int)({})", convert(&args[0], types[0], CType::Int))
                } else {
                    "0".to_string()
                };
                (format!("(_kpj_exit(ctx, {}), 0LL)", code), CType::Int)
            }
            "abs" => {
                if !args.is_empty() {
                    if types[0] == CType::Float {
                        (format!("_kpj_fabs({})", args[0]), CType::Float)
                    } else {
                        (
                            format!(
                                "(({}) < 0 ? (int64_t)(0 - (uint64_t)({})) : ({}))",
                                args[0], args[0], args[0]
                            ),
                            CType::Int,
                        )
                    }
                } else {
                    ("0LL".to_string(), CType::Int)
                }
            }
            "min" | "max" => {
                if args.len() >= 2 {
                    let op = if ident == "max" { ">" } else { "<" };
                    let t = if types[0] == CType::Float || types[1] == CType::Float {
                        CType::Float
                    } else {
                        CType::Int
                    };
                    let a = convert(&args[0], types[0], t);
                    let b = convert(&args[1], types[1], t);
                    (
                        format!("(({}) {} ({}) ? ({}) : ({}))", a, op, b, a, b),
                        t,
                    )
                } else {
                    ("0LL".to_string(), CType::Int)
                }
            }
            _ => {
                let mut call_args = Vec::with_capacity(args.len() + 1);
                call_args.push("ctx".to_string());
                for (a, &t) in args.iter().zip(types.iter()) {
                    call_args.push(convert(a, t, CType::Int));
                }
                (
                    format!("{}({})", Self::c_func_name(ident), call_args.join(", ")),
                    CType::Int,
                )
            }
        }
    }
}

pub fn generate_c(program: &Program) -> String {
    let mut cg = CodeGenerator::new();
    for stmt in &program.statements {
        cg.gen_statement(stmt);
    }

    let mut out = String::with_capacity(
        C_RUNTIME.len() + cg.functions.len() + cg.main_body.len() + 256,
    );
    out.push_str(C_RUNTIME);
    if !cg.functions.is_empty() {
        out.push_str(&cg.functions);
        out.push('\n');
    }
    out.push_str("static __attribute__((always_inline)) inline int _kpj_main(KpjCtx *ctx) {\n");
    out.push_str(&cg.main_body);
    out.push_str("    return 0;\n}\n");
    out
}

fn find_cc() -> Option<String> {
    for cand in ["/usr/bin/gcc", "gcc", "/usr/bin/clang", "clang", "cc"] {
        if cand.starts_with('/') {
            if Path::new(cand).exists() {
                return Some(cand.to_string());
            }
        } else if let Ok(out) = Command::new("which").arg(cand).output() {
            if out.status.success() {
                let s = String::from_utf8_lossy(&out.stdout).trim().to_string();
                if !s.is_empty() {
                    return Some(s);
                }
            }
        }
    }
    None
}

pub fn compile_to_binary(program: &Program, output_path: &str) -> Result<(), String> {
    let c_code = generate_c(program);

    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_nanos())
        .unwrap_or(0);
    let tmp_dir = std::env::temp_dir();
    let tmp_path = tmp_dir.join(format!("kopaj-{}-{}.c", std::process::id(), nanos));

    fs::write(&tmp_path, c_code.as_bytes())
        .map_err(|e| format!("failed to write temporary C file: {}", e))?;

    let cleanup = |p: &Path| {
        let _ = fs::remove_file(p);
    };

    if let Some(parent) = Path::new(output_path).parent() {
        if !parent.as_os_str().is_empty() && parent != Path::new(".") {
            if let Err(e) = fs::create_dir_all(parent) {
                cleanup(&tmp_path);
                return Err(format!("failed to create output directory: {}", e));
            }
        }
    }

    let cc_bin = match find_cc() {
        Some(b) => b,
        None => {
            cleanup(&tmp_path);
            return Err("no C compiler (gcc/clang) found in PATH".to_string());
        }
    };

    let tmp_str = tmp_path.to_string_lossy().into_owned();
    let freestanding = [
        "-O3",
        "-march=x86-64-v2",
        "-std=gnu11",
        "-w",
        "-pipe",
        "-DKPJ_FREESTANDING",
        "-nostdlib",
        "-static",
        "-no-pie",
        "-fno-pie",
        "-fcf-protection=none",
        "-fno-math-errno",
        "-fno-trapping-math",
        "-fno-asynchronous-unwind-tables",
        "-fno-unwind-tables",
        "-fno-stack-protector",
        "-fomit-frame-pointer",
        "-fno-plt",
        "-ffunction-sections",
        "-fdata-sections",
        "-Wl,--gc-sections",
        "-Wl,--build-id=none",
        "-Wl,-z,norelro",
        "-Wl,-z,noseparate-code",
        "-Wl,-z,max-page-size=4096",
        "-s",
        "-o",
        output_path,
        &tmp_str,
    ];

    let out1 = Command::new(&cc_bin).args(freestanding).output();
    let ok = match &out1 {
        Ok(o) => o.status.success(),
        Err(_) => false,
    };

    if !ok {
        let fallback = [
            "-O3",
            "-std=gnu11",
            "-w",
            "-pipe",
            "-fno-math-errno",
            "-fno-asynchronous-unwind-tables",
            "-fno-stack-protector",
            "-fomit-frame-pointer",
            "-s",
            "-o",
            output_path,
            &tmp_str,
            "-lm",
        ];
        let out2 = Command::new(&cc_bin).args(fallback).output();
        match out2 {
            Ok(o) if o.status.success() => {}
            Ok(o) => {
                let err1 = out1
                    .ok()
                    .map(|x| String::from_utf8_lossy(&x.stderr).into_owned())
                    .unwrap_or_default();
                let err2 = String::from_utf8_lossy(&o.stderr).into_owned();
                cleanup(&tmp_path);
                return Err(format!("C compilation failed: {}{}", err1, err2));
            }
            Err(e) => {
                cleanup(&tmp_path);
                return Err(format!("failed to invoke C compiler: {}", e));
            }
        }
    }

    cleanup(&tmp_path);

    let perms = fs::Permissions::from_mode(0o755);
    fs::set_permissions(output_path, perms)
        .map_err(|e| format!("failed to chmod output binary: {}", e))?;

    let bin_data = fs::read(output_path)
        .map_err(|e| format!("failed to read compiled binary for verification: {}", e))?;
    if !bin_data.windows(10).any(|w| w == b"sign kopaj") {
        return Err("compiled binary is missing required signature 'sign kopaj'".to_string());
    }

    Ok(())
}
