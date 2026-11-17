mod ast;
mod codegen;
mod lexer;
mod parser;
mod runtime;

use ast::Program;
use codegen::{compile_to_binary, generate_c};
use lexer::Lexer;
use parser::Parser;
use std::env;
use std::fs;
use std::io::{self, Read};
use std::process;

fn parse_source(source: &str) -> Program {
    let l = Lexer::new(source);
    let mut p = Parser::new(l);
    let program = p.parse_program();
    if !p.errors.is_empty() {
        eprintln!("Parser errors:");
        for msg in &p.errors {
            eprintln!("  - {}", msg);
        }
        process::exit(1);
    }
    program
}

fn read_source_file(path: &str) -> String {
    match fs::read_to_string(path) {
        Ok(s) => s,
        Err(e) => {
            eprintln!("file read error ({}): {}", path, e);
            process::exit(1);
        }
    }
}

fn handle_build(args: &[String]) {
    let mut output_path = String::new();
    let mut source_path = String::new();

    let mut i = 0;
    while i < args.len() {
        if args[i] == "-o" {
            if i + 1 >= args.len() {
                eprintln!("missing output path after -o");
                process::exit(1);
            }
            output_path = args[i + 1].clone();
            i += 2;
        } else {
            if source_path.is_empty() {
                source_path = args[i].clone();
            }
            i += 1;
        }
    }

    if source_path.is_empty() {
        eprintln!("usage: kopajc build -o <output> <source>");
        process::exit(1);
    }
    if output_path.is_empty() {
        output_path = "a.out".to_string();
    }

    let source = read_source_file(&source_path);
    let program = parse_source(&source);

    if let Err(e) = compile_to_binary(&program, &output_path) {
        eprintln!("compile error: {}", e);
        process::exit(1);
    }
}

fn main() {
    let args: Vec<String> = env::args().collect();

    if args.len() > 1 && args[1] == "build" {
        handle_build(&args[2..]);
        return;
    }

    if args.len() > 1 && args[1] == "-o" {
        handle_build(&args[1..]);
        return;
    }

    if args.len() > 2 && args[1] == "emit-c" {
        let source = read_source_file(&args[2]);
        let program = parse_source(&source);
        print!("{}", generate_c(&program));
        return;
    }

    // Direct run mode: compile to temporary executable and run
    let mut rem = &args[1..];
    if !rem.is_empty() && rem[0] == "run" {
        rem = &rem[1..];
    }

    let (source, prog_args) = if !rem.is_empty() {
        (read_source_file(&rem[0]), &rem[1..])
    } else {
        let mut s = String::new();
        if io::stdin().read_to_string(&mut s).is_err() || s.is_empty() {
            eprintln!("usage: kopajc build -o <output> <source>");
            process::exit(1);
        }
        (s, &[][..])
    };

    let program = parse_source(&source);
    let tmp_bin = env::temp_dir().join(format!("kopaj-run-{}", process::id()));
    let tmp_bin_str = tmp_bin.to_string_lossy().into_owned();
    if let Err(e) = compile_to_binary(&program, &tmp_bin_str) {
        eprintln!("compile error: {}", e);
        process::exit(1);
    }
    let status = process::Command::new(&tmp_bin).args(prog_args).status();
    let _ = fs::remove_file(&tmp_bin);
    if let Ok(st) = status {
        process::exit(st.code().unwrap_or(1));
    }
}
