package main

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type CodeGenerator struct {
	functions bytes.Buffer
	mainBody  bytes.Buffer
	scopes    []map[string]bool
	indent    int
	inFunc    bool
	tempCount int
}

func NewCodeGenerator() *CodeGenerator {
	return &CodeGenerator{
		scopes: []map[string]bool{make(map[string]bool)},
		indent: 1,
	}
}

func (cg *CodeGenerator) pushScope() {
	cg.scopes = append(cg.scopes, make(map[string]bool))
}

func (cg *CodeGenerator) popScope() {
	if len(cg.scopes) > 1 {
		cg.scopes = cg.scopes[:len(cg.scopes)-1]
	}
}

func (cg *CodeGenerator) isDeclaredInCurrentScope(name string) bool {
	return cg.scopes[len(cg.scopes)-1][name]
}

func (cg *CodeGenerator) isDeclaredAnywhere(name string) bool {
	for i := len(cg.scopes) - 1; i >= 0; i-- {
		if cg.scopes[i][name] {
			return true
		}
	}
	return false
}

func (cg *CodeGenerator) declareVar(name string) {
	cg.scopes[len(cg.scopes)-1][name] = true
}

func (cg *CodeGenerator) nextTemp() string {
	cg.tempCount++
	return fmt.Sprintf("_kpj_tmp_%d", cg.tempCount)
}

func (cg *CodeGenerator) buf() *bytes.Buffer {
	if cg.inFunc {
		return &cg.functions
	}
	return &cg.mainBody
}

func (cg *CodeGenerator) writeIndent() {
	cg.buf().WriteString(strings.Repeat("    ", cg.indent))
}

func GenerateC(program *Program) string {
	cg := NewCodeGenerator()

	for _, stmt := range program.Statements {
		cg.genStatement(stmt)
	}

	var out bytes.Buffer
	out.WriteString(`#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>
#include <inttypes.h>
#include <unistd.h>

// Required competition binary signature flag (Rule 1: survives -O2 and stripping)
static const char kopaj_flag[] __attribute__((used)) = "sign kopaj";

static int _kpj_argc = 0;
static char **_kpj_argv = NULL;
static int _kpj_arg_idx = 1;

static int64_t _kpj_read_int(void) {
    long long val = 0;
    if (!isatty(0)) {
        if (scanf("%lld", &val) == 1) {
            _kpj_arg_idx++;
            return (int64_t)val;
        }
    }
    if (_kpj_arg_idx < _kpj_argc) {
        return (int64_t)strtoll(_kpj_argv[_kpj_arg_idx++], NULL, 10);
    }
    if (scanf("%lld", &val) == 1) {
        return (int64_t)val;
    }
    return 0;
}

static int64_t _kpj_arg_int(int64_t idx) {
    if (idx >= 0 && idx < (int64_t)_kpj_argc) {
        return (int64_t)strtoll(_kpj_argv[idx], NULL, 10);
    }
    return 0;
}

static int64_t _kpj_argc_fn(void) {
    return (int64_t)(_kpj_argc > 0 ? _kpj_argc - 1 : 0);
}

static int64_t _kpj_print_int(int64_t val) {
    printf("%lld\n", (long long)val);
    return val;
}

static int64_t _kpj_div(int64_t a, int64_t b) {
    if (b == 0) return 0;
    if (a == INT64_MIN && b == -1) return INT64_MIN;
    return a / b;
}

static int64_t _kpj_mod(int64_t a, int64_t b) {
    if (b == 0) return 0;
    if (a == INT64_MIN && b == -1) return 0;
    return a % b;
}

`)

	if cg.functions.Len() > 0 {
		out.WriteString(cg.functions.String())
		out.WriteString("\n")
	}

	out.WriteString(`int main(int argc, char **argv) {
    _kpj_argc = argc;
    _kpj_argv = argv;
    if (kopaj_flag[0] == '\0') return 1;
`)
	out.WriteString(cg.mainBody.String())
	out.WriteString("    return 0;\n}\n")

	return out.String()
}

func (cg *CodeGenerator) cVarName(name string) string {
	return "v_" + name
}

func (cg *CodeGenerator) cFuncName(name string) string {
	return "fn_" + name
}

func (cg *CodeGenerator) genStatement(stmt Statement) {
	switch s := stmt.(type) {
	case *LetStatement:
		valExpr := cg.genExpression(s.Value)
		cName := cg.cVarName(s.Name.Value)
		cg.writeIndent()
		if cg.isDeclaredInCurrentScope(s.Name.Value) {
			fmt.Fprintf(cg.buf(), "%s = %s;\n", cName, valExpr)
		} else {
			cg.declareVar(s.Name.Value)
			fmt.Fprintf(cg.buf(), "int64_t %s = %s;\n", cName, valExpr)
		}

	case *AssignStatement:
		valExpr := cg.genExpression(s.Value)
		cName := cg.cVarName(s.Name.Value)
		cg.writeIndent()
		if cg.isDeclaredAnywhere(s.Name.Value) {
			fmt.Fprintf(cg.buf(), "%s = %s;\n", cName, valExpr)
		} else {
			cg.declareVar(s.Name.Value)
			fmt.Fprintf(cg.buf(), "int64_t %s = %s;\n", cName, valExpr)
		}

	case *ReturnStatement:
		if s.ReturnValue != nil {
			valExpr := cg.genExpression(s.ReturnValue)
			cg.writeIndent()
			if cg.inFunc {
				fmt.Fprintf(cg.buf(), "return %s;\n", valExpr)
			} else {
				fmt.Fprintf(cg.buf(), "return (int)(%s);\n", valExpr)
			}
		} else {
			cg.writeIndent()
			fmt.Fprintf(cg.buf(), "return 0;\n")
		}

	case *WhileStatement:
		condExpr := cg.genExpression(s.Condition)
		cg.writeIndent()
		fmt.Fprintf(cg.buf(), "while (%s) {\n", condExpr)
		cg.indent++
		cg.pushScope()
		for _, bodyStmt := range s.Body.Statements {
			cg.genStatement(bodyStmt)
		}
		cg.popScope()
		cg.indent--
		cg.writeIndent()
		cg.buf().WriteString("}\n")

	case *FunctionStatement:
		prevInFunc := cg.inFunc
		prevIndent := cg.indent
		cg.inFunc = true
		cg.indent = 0

		params := make([]string, len(s.Parameters))
		cg.pushScope()
		for i, p := range s.Parameters {
			cg.declareVar(p.Value)
			params[i] = "int64_t " + cg.cVarName(p.Value)
		}
		paramList := strings.Join(params, ", ")
		if paramList == "" {
			paramList = "void"
		}

		fmt.Fprintf(&cg.functions, "static int64_t %s(%s) {\n", cg.cFuncName(s.Name.Value), paramList)
		cg.indent = 1
		for _, bodyStmt := range s.Body.Statements {
			cg.genStatement(bodyStmt)
		}
		cg.functions.WriteString("    return 0;\n}\n\n")
		cg.popScope()

		cg.inFunc = prevInFunc
		cg.indent = prevIndent

	case *ExpressionStatement:
		if s.Expression == nil {
			return
		}
		if ie, ok := s.Expression.(*IfExpression); ok {
			cg.genIfStatement(ie)
			return
		}
		exprStr := cg.genExpression(s.Expression)
		cg.writeIndent()
		fmt.Fprintf(cg.buf(), "(void)(%s);\n", exprStr)

	case *BlockStatement:
		cg.writeIndent()
		cg.buf().WriteString("{\n")
		cg.indent++
		cg.pushScope()
		for _, bs := range s.Statements {
			cg.genStatement(bs)
		}
		cg.popScope()
		cg.indent--
		cg.writeIndent()
		cg.buf().WriteString("}\n")
	}
}

func (cg *CodeGenerator) genIfStatement(ie *IfExpression) {
	cond := cg.genExpression(ie.Condition)
	cg.writeIndent()
	fmt.Fprintf(cg.buf(), "if (%s) {\n", cond)
	cg.indent++
	cg.pushScope()
	for _, s := range ie.Consequence.Statements {
		cg.genStatement(s)
	}
	cg.popScope()
	cg.indent--
	if ie.Alternative != nil {
		cg.writeIndent()
		cg.buf().WriteString("} else {\n")
		cg.indent++
		cg.pushScope()
		for _, s := range ie.Alternative.Statements {
			cg.genStatement(s)
		}
		cg.popScope()
		cg.indent--
	}
	cg.writeIndent()
	cg.buf().WriteString("}\n")
}

func (cg *CodeGenerator) genExpression(expr Expression) string {
	if expr == nil {
		return "0LL"
	}
	switch e := expr.(type) {
	case *IntegerLiteral:
		if e.Value == math.MinInt64 {
			return "(-9223372036854775807LL - 1LL)"
		}
		return fmt.Sprintf("%dLL", e.Value)

	case *BooleanLiteral:
		if e.Value {
			return "1LL"
		}
		return "0LL"

	case *Identifier:
		return cg.cVarName(e.Value)

	case *PrefixExpression:
		right := cg.genExpression(e.Right)
		switch e.Operator {
		case "-":
			return fmt.Sprintf("(-(%s))", right)
		case "+":
			return fmt.Sprintf("(+(%s))", right)
		case "!":
			return fmt.Sprintf("(!(%s))", right)
		default:
			return fmt.Sprintf("(%s(%s))", e.Operator, right)
		}

	case *InfixExpression:
		left := cg.genExpression(e.Left)
		right := cg.genExpression(e.Right)
		switch e.Operator {
		case "+":
			return fmt.Sprintf("(int64_t)((uint64_t)(%s) + (uint64_t)(%s))", left, right)
		case "-":
			return fmt.Sprintf("(int64_t)((uint64_t)(%s) - (uint64_t)(%s))", left, right)
		case "*":
			return fmt.Sprintf("(int64_t)((uint64_t)(%s) * (uint64_t)(%s))", left, right)
		case "/":
			return fmt.Sprintf("_kpj_div(%s, %s)", left, right)
		case "%":
			return fmt.Sprintf("_kpj_mod(%s, %s)", left, right)
		default:
			return fmt.Sprintf("((%s) %s (%s))", left, e.Operator, right)
		}

	case *IfExpression:
		tmp := cg.nextTemp()
		cg.writeIndent()
		fmt.Fprintf(cg.buf(), "int64_t %s = 0;\n", tmp)
		cond := cg.genExpression(e.Condition)
		cg.writeIndent()
		fmt.Fprintf(cg.buf(), "if (%s) {\n", cond)
		cg.indent++
		cg.pushScope()
		cg.genBlockWithResult(e.Consequence, tmp)
		cg.popScope()
		cg.indent--
		if e.Alternative != nil {
			cg.writeIndent()
			cg.buf().WriteString("} else {\n")
			cg.indent++
			cg.pushScope()
			cg.genBlockWithResult(e.Alternative, tmp)
			cg.popScope()
			cg.indent--
		}
		cg.writeIndent()
		cg.buf().WriteString("}\n")
		return tmp

	case *CallExpression:
		return cg.genCallExpression(e)
	}
	return "0LL"
}

func (cg *CodeGenerator) genBlockWithResult(block *BlockStatement, resultVar string) {
	for i, s := range block.Statements {
		if i == len(block.Statements)-1 {
			if es, ok := s.(*ExpressionStatement); ok && es.Expression != nil {
				val := cg.genExpression(es.Expression)
				cg.writeIndent()
				fmt.Fprintf(cg.buf(), "%s = %s;\n", resultVar, val)
				continue
			}
		}
		cg.genStatement(s)
	}
}

func (cg *CodeGenerator) genCallExpression(ce *CallExpression) string {
	ident, ok := ce.Function.(*Identifier)
	if !ok {
		return "0LL"
	}

	args := make([]string, len(ce.Arguments))
	for i, a := range ce.Arguments {
		args[i] = cg.genExpression(a)
	}

	switch ident.Value {
	case "read_int", "scan_int", "input_int", "read":
		return "_kpj_read_int()"
	case "arg_int", "arg":
		if len(args) >= 1 {
			return fmt.Sprintf("_kpj_arg_int(%s)", args[0])
		}
		return "0LL"
	case "argc", "arg_count":
		return "_kpj_argc_fn()"
	case "print", "println", "print_int":
		if len(args) == 0 {
			return `(printf("\n"), 0LL)`
		}
		if len(args) == 1 {
			return fmt.Sprintf("_kpj_print_int(%s)", args[0])
		}
		var parts []string
		for _, a := range args {
			parts = append(parts, fmt.Sprintf("_kpj_print_int(%s)", a))
		}
		return "(" + strings.Join(parts, ", ") + ")"
	case "exit":
		code := "0"
		if len(args) >= 1 {
			code = fmt.Sprintf("(int)(%s)", args[0])
		}
		return fmt.Sprintf("(exit(%s), 0LL)", code)
	case "abs":
		if len(args) >= 1 {
			return fmt.Sprintf("((%s) < 0 ? -(%s) : (%s))", args[0], args[0], args[0])
		}
		return "0LL"
	case "min":
		if len(args) >= 2 {
			return fmt.Sprintf("((%s) < (%s) ? (%s) : (%s))", args[0], args[1], args[0], args[1])
		}
		return "0LL"
	case "max":
		if len(args) >= 2 {
			return fmt.Sprintf("((%s) > (%s) ? (%s) : (%s))", args[0], args[1], args[0], args[1])
		}
		return "0LL"
	default:
		return fmt.Sprintf("%s(%s)", cg.cFuncName(ident.Value), strings.Join(args, ", "))
	}
}

func CompileToBinary(program *Program, outputPath string) error {
	cCode := GenerateC(program)

	tmpFile, err := os.CreateTemp("", "kopaj-*.c")
	if err != nil {
		return fmt.Errorf("failed to create temporary C file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.WriteString(cCode); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write temporary C file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temporary C file: %w", err)
	}

	if dir := filepath.Dir(outputPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create output directory: %w", err)
		}
	}

	compilers := []string{"gcc", "/usr/bin/gcc", "clang", "/usr/bin/clang", "cc"}
	var ccBin string
	for _, candidate := range compilers {
		if path, err := exec.LookPath(candidate); err == nil {
			ccBin = path
			break
		}
	}
	if ccBin == "" {
		return fmt.Errorf("no C compiler (gcc/clang) found in PATH")
	}

	cmd := exec.Command(ccBin, "-O2", "-std=c11", "-o", outputPath, tmpPath, "-lm")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("C compilation failed (%v): %s", err, stderr.String())
	}

	if err := os.Chmod(outputPath, 0755); err != nil {
		return fmt.Errorf("failed to chmod output binary: %w", err)
	}

	// Verify "sign kopaj" signature is present in output executable
	binData, err := os.ReadFile(outputPath)
	if err != nil {
		return fmt.Errorf("failed to read compiled binary for verification: %w", err)
	}
	if !bytes.Contains(binData, []byte("sign kopaj")) {
		return fmt.Errorf("compiled binary is missing required signature 'sign kopaj'")
	}

	return nil
}
