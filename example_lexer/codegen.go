package main

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// CType is the static type of an expression/variable in generated C.
type CType int

const (
	TInt CType = iota
	TFloat
	TStr
)

func (t CType) cName() string {
	switch t {
	case TFloat:
		return "double"
	case TStr:
		return "kstr"
	}
	return "int64_t"
}

func convert(code string, from, to CType) string {
	if from == to || from == TStr || to == TStr {
		return code
	}
	if to == TFloat {
		return fmt.Sprintf("((double)(%s))", code)
	}
	return fmt.Sprintf("((int64_t)(%s))", code)
}

type varInfo struct {
	typ CType
}

type CodeGenerator struct {
	functions bytes.Buffer
	mainBody  bytes.Buffer
	scopes    []map[string]varInfo
	indent    int
	inFunc    bool
	tempCount int
}

func NewCodeGenerator() *CodeGenerator {
	return &CodeGenerator{
		scopes: []map[string]varInfo{make(map[string]varInfo)},
		indent: 1,
	}
}

func (cg *CodeGenerator) pushScope() { cg.scopes = append(cg.scopes, make(map[string]varInfo)) }
func (cg *CodeGenerator) popScope() {
	if len(cg.scopes) > 1 {
		cg.scopes = cg.scopes[:len(cg.scopes)-1]
	}
}

func (cg *CodeGenerator) lookupCurrent(name string) (varInfo, bool) {
	v, ok := cg.scopes[len(cg.scopes)-1][name]
	return v, ok
}

func (cg *CodeGenerator) lookup(name string) (varInfo, bool) {
	for i := len(cg.scopes) - 1; i >= 0; i-- {
		if v, ok := cg.scopes[i][name]; ok {
			return v, true
		}
	}
	return varInfo{}, false
}

func (cg *CodeGenerator) declareVar(name string, t CType) {
	cg.scopes[len(cg.scopes)-1][name] = varInfo{typ: t}
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

// Runtime: no stdio on the hot path. Input is slurped with read(2), output is
// buffered and flushed once with write(2).
const cRuntime = `#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <string.h>
#include <math.h>
#include <errno.h>
#include <unistd.h>

// Required competition binary signature (Rule 1: survives -O2 and stripping)
static const char kopaj_flag[] __attribute__((used)) = "sign kopaj";

static int _kpj_argc = 0;
static char **_kpj_argv = NULL;
static int _kpj_nread = 0;

static char _kpj_out[1 << 16];
static size_t _kpj_outn = 0;

static void _kpj_flush(void) {
    size_t off = 0;
    while (off < _kpj_outn) {
        ssize_t w = write(1, _kpj_out + off, _kpj_outn - off);
        if (w < 0) { if (errno == EINTR) continue; break; }
        if (w == 0) break;
        off += (size_t)w;
    }
    _kpj_outn = 0;
}

static inline void _kpj_put(const char *s, size_t n) {
    if (n > sizeof(_kpj_out) - _kpj_outn) {
        _kpj_flush();
        if (n > sizeof(_kpj_out)) {
            size_t off = 0;
            while (off < n) {
                ssize_t w = write(1, s + off, n - off);
                if (w < 0) { if (errno == EINTR) continue; break; }
                if (w == 0) break;
                off += (size_t)w;
            }
            return;
        }
    }
    memcpy(_kpj_out + _kpj_outn, s, n);
    _kpj_outn += n;
}

static char *_kpj_in = NULL;
static size_t _kpj_inlen = 0, _kpj_inpos = 0;
static int _kpj_loaded = 0;

static void _kpj_load(void) {
    _kpj_loaded = 1;
    size_t cap = 1 << 16;
    _kpj_in = (char *)malloc(cap + 1);
    if (!_kpj_in) { _kpj_in = (char *)""; return; }
    if (isatty(0)) { _kpj_in[0] = 0; return; }
    for (;;) {
        if (_kpj_inlen == cap) {
            cap <<= 1;
            char *n = (char *)realloc(_kpj_in, cap + 1);
            if (!n) break;
            _kpj_in = n;
        }
        ssize_t r = read(0, _kpj_in + _kpj_inlen, cap - _kpj_inlen);
        if (r < 0) { if (errno == EINTR) continue; break; }
        if (r == 0) break;
        _kpj_inlen += (size_t)r;
    }
    _kpj_in[_kpj_inlen] = 0;
}

static size_t _kpj_toklen = 0;

static const char *_kpj_tok(void) {
    if (!_kpj_loaded) _kpj_load();
    while (_kpj_inpos < _kpj_inlen && (unsigned char)_kpj_in[_kpj_inpos] <= ' ') _kpj_inpos++;
    if (_kpj_inpos >= _kpj_inlen) return NULL;
    const char *s = _kpj_in + _kpj_inpos;
    while (_kpj_inpos < _kpj_inlen && (unsigned char)_kpj_in[_kpj_inpos] > ' ') _kpj_inpos++;
    _kpj_toklen = (size_t)(_kpj_in + _kpj_inpos - s);
    return s;
}

static const char *_kpj_next(void) {
    _kpj_nread++;
    const char *s = _kpj_tok();
    if (!s && _kpj_nread < _kpj_argc) { s = _kpj_argv[_kpj_nread]; _kpj_toklen = strlen(s); }
    return s;
}

static inline int64_t _kpj_parse_int(const char *s) {
    if (!s) return 0;
    int neg = 0;
    if (*s == '-') { neg = 1; s++; } else if (*s == '+') s++;
    uint64_t u = 0;
    while (*s >= '0' && *s <= '9') { u = u * 10 + (uint64_t)(*s - '0'); s++; }
    return (int64_t)(neg ? (0 - u) : u);
}


typedef struct { const char *p; int64_t n; } kstr;

static inline kstr _kpj_mk(const char *p, int64_t n) { kstr s; s.p = p; s.n = n; return s; }

static kstr _kpj_read_token(void) {
    const char *s = _kpj_next();
    if (!s) return _kpj_mk("", 0);
    return _kpj_mk(s, (int64_t)_kpj_toklen);
}

static kstr _kpj_read_line(void) {
    if (!_kpj_loaded) _kpj_load();
    while (_kpj_inpos < _kpj_inlen && (_kpj_in[_kpj_inpos] == ' ' || _kpj_in[_kpj_inpos] == '\t')) _kpj_inpos++;
    size_t st = _kpj_inpos;
    while (_kpj_inpos < _kpj_inlen && _kpj_in[_kpj_inpos] != '\n') _kpj_inpos++;
    size_t en = _kpj_inpos;
    if (_kpj_inpos < _kpj_inlen) _kpj_inpos++;
    while (en > st && (_kpj_in[en - 1] == '\r' || _kpj_in[en - 1] == ' ' || _kpj_in[en - 1] == '\t')) en--;
    return _kpj_mk(_kpj_in + st, (int64_t)(en - st));
}

static inline int64_t _kpj_len(kstr s) { return s.n; }

static kstr _kpj_upper(kstr s) {
    char *b = (char *)malloc((size_t)s.n + 1);
    for (int64_t i = 0; i < s.n; i++) { char c = s.p[i]; b[i] = (c >= 'a' && c <= 'z') ? (char)(c - 32) : c; }
    b[s.n] = 0;
    return _kpj_mk(b, s.n);
}

static kstr _kpj_lower(kstr s) {
    char *b = (char *)malloc((size_t)s.n + 1);
    for (int64_t i = 0; i < s.n; i++) { char c = s.p[i]; b[i] = (c >= 'A' && c <= 'Z') ? (char)(c + 32) : c; }
    b[s.n] = 0;
    return _kpj_mk(b, s.n);
}

static inline kstr _kpj_char_at(kstr s, int64_t i) {
    if (i < 0 || i >= s.n) return _kpj_mk("", 0);
    return _kpj_mk(s.p + i, 1);
}

static inline kstr _kpj_substr(kstr s, int64_t st, int64_t len) {
    if (st < 0) st = 0;
    if (st > s.n) st = s.n;
    if (len < 0) len = 0;
    if (len > s.n - st) len = s.n - st;
    return _kpj_mk(s.p + st, len);
}

static kstr _kpj_cat(kstr a, kstr b) {
    char *r = (char *)malloc((size_t)(a.n + b.n) + 1);
    memcpy(r, a.p, (size_t)a.n);
    memcpy(r + a.n, b.p, (size_t)b.n);
    r[a.n + b.n] = 0;
    return _kpj_mk(r, a.n + b.n);
}

static inline int _kpj_scmp(kstr a, kstr b) {
    int64_t m = a.n < b.n ? a.n : b.n;
    int c = m ? memcmp(a.p, b.p, (size_t)m) : 0;
    if (c) return c;
    return a.n < b.n ? -1 : (a.n > b.n ? 1 : 0);
}

static inline int64_t _kpj_seq(kstr a, kstr b) {
    return a.n == b.n && (a.n == 0 || memcmp(a.p, b.p, (size_t)a.n) == 0);
}

static kstr _kpj_str_int(int64_t v) {
    char *b = (char *)malloc(24);
    int n = snprintf(b, 24, "%lld", (long long)v);
    return _kpj_mk(b, n);
}

static kstr _kpj_str_float(double v) {
    char *b = (char *)malloc(40);
    int n = snprintf(b, 40, "%.17g", v);
    return _kpj_mk(b, n);
}

static inline void _kpj_w_str(kstr s) { _kpj_put(s.p, (size_t)s.n); }

static int64_t _kpj_read_int(void) { return _kpj_parse_int(_kpj_next()); }

static double _kpj_read_float(void) {
    const char *s = _kpj_next();
    if (!s) return 0.0;
    return strtod(s, NULL);
}

static int64_t _kpj_arg_int(int64_t idx) {
    if (idx >= 0 && idx < (int64_t)_kpj_argc) return _kpj_parse_int(_kpj_argv[idx]);
    return 0;
}

static int64_t _kpj_argc_fn(void) { return (int64_t)(_kpj_argc > 0 ? _kpj_argc - 1 : 0); }

static void _kpj_w_int(int64_t v) {
    char b[24];
    char *e = b + sizeof(b);
    uint64_t u = v < 0 ? (0 - (uint64_t)v) : (uint64_t)v;
    do { *--e = (char)('0' + u % 10); u /= 10; } while (u);
    if (v < 0) *--e = '-';
    _kpj_put(e, (size_t)(b + sizeof(b) - e));
}

static void _kpj_w_float(double v) {
    if (v == 0) v = 0.0;
    char b[64];
    int n = 0;
    for (int prec = 15; prec <= 17; prec++) {
        n = snprintf(b, sizeof(b) - 1, "%.*g", prec, v);
        if (prec == 17 || strtod(b, NULL) == v) break;
    }
    _kpj_put(b, (size_t)n);
}

static inline int64_t _kpj_div(int64_t a, int64_t b) {
    if (b == 0) return 0;
    if (b == -1) return (int64_t)(0 - (uint64_t)a);
    return a / b;
}

static inline int64_t _kpj_mod(int64_t a, int64_t b) {
    if (b == 0 || b == -1) return 0;
    return a % b;
}

`

func GenerateC(program *Program) string {
	cg := NewCodeGenerator()

	for _, stmt := range program.Statements {
		cg.genStatement(stmt)
	}

	var out bytes.Buffer
	out.WriteString(cRuntime)

	if cg.functions.Len() > 0 {
		out.WriteString(cg.functions.String())
		out.WriteString("\n")
	}

	out.WriteString(`int main(int argc, char **argv) {
    _kpj_argc = argc;
    _kpj_argv = argv;
    atexit(_kpj_flush);
    if (kopaj_flag[0] == '\0') return 1;
`)
	out.WriteString(cg.mainBody.String())
	out.WriteString("    return 0;\n}\n")

	return out.String()
}

func (cg *CodeGenerator) cVarName(name string) string  { return "v_" + name }
func (cg *CodeGenerator) cFuncName(name string) string { return "fn_" + name }

func (cg *CodeGenerator) genStatement(stmt Statement) {
	switch s := stmt.(type) {
	case *LetStatement:
		valExpr, vt := cg.genExpr(s.Value)
		cName := cg.cVarName(s.Name.Value)
		if info, ok := cg.lookupCurrent(s.Name.Value); ok {
			cg.writeIndent()
			fmt.Fprintf(cg.buf(), "%s = %s;\n", cName, convert(valExpr, vt, info.typ))
		} else {
			cg.declareVar(s.Name.Value, vt)
			cg.writeIndent()
			fmt.Fprintf(cg.buf(), "%s %s = %s;\n", vt.cName(), cName, valExpr)
		}

	case *AssignStatement:
		valExpr, vt := cg.genExpr(s.Value)
		cName := cg.cVarName(s.Name.Value)
		if info, ok := cg.lookup(s.Name.Value); ok {
			cg.writeIndent()
			fmt.Fprintf(cg.buf(), "%s = %s;\n", cName, convert(valExpr, vt, info.typ))
		} else {
			cg.declareVar(s.Name.Value, vt)
			cg.writeIndent()
			fmt.Fprintf(cg.buf(), "%s %s = %s;\n", vt.cName(), cName, valExpr)
		}

	case *ReturnStatement:
		if s.ReturnValue != nil {
			valExpr, vt := cg.genExpr(s.ReturnValue)
			cg.writeIndent()
			if cg.inFunc {
				fmt.Fprintf(cg.buf(), "return %s;\n", convert(valExpr, vt, TInt))
			} else {
				fmt.Fprintf(cg.buf(), "return (int)(%s);\n", convert(valExpr, vt, TInt))
			}
		} else {
			cg.writeIndent()
			fmt.Fprintf(cg.buf(), "return 0;\n")
		}

	case *WhileStatement:
		condExpr, _ := cg.genExpr(s.Condition)
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

		params := make([]string, len(s.Parameters))
		cg.pushScope()
		for i, p := range s.Parameters {
			cg.declareVar(p.Value, TInt)
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
		exprStr, _ := cg.genExpr(s.Expression)
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
	cond, _ := cg.genExpr(ie.Condition)
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

func floatLit(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "(1.0/0.0)"
	case math.IsInf(v, -1):
		return "(-1.0/0.0)"
	case math.IsNaN(v):
		return "(0.0/0.0)"
	}
	s := strconv.FormatFloat(v, 'g', 17, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	if v < 0 || (v == 0 && math.Signbit(v)) {
		return "(" + s + ")"
	}
	return s
}

// genExpr returns the C expression and its static type.
func (cg *CodeGenerator) genExpr(expr Expression) (string, CType) {
	if expr == nil {
		return "0LL", TInt
	}
	switch e := expr.(type) {
	case *IntegerLiteral:
		if e.Value == math.MinInt64 {
			return "(-9223372036854775807LL - 1LL)", TInt
		}
		return fmt.Sprintf("%dLL", e.Value), TInt

	case *FloatLiteral:
		return floatLit(e.Value), TFloat

	case *StringLiteral:
		return fmt.Sprintf("_kpj_mk(%s, %d)", cEscape(e.Value), len(e.Value)), TStr

	case *BooleanLiteral:
		if e.Value {
			return "1LL", TInt
		}
		return "0LL", TInt

	case *Identifier:
		t := TInt
		if info, ok := cg.lookup(e.Value); ok {
			t = info.typ
		}
		return cg.cVarName(e.Value), t

	case *PrefixExpression:
		right, rt := cg.genExpr(e.Right)
		switch e.Operator {
		case "-":
			if rt == TFloat {
				return fmt.Sprintf("(-(%s))", right), TFloat
			}
			return fmt.Sprintf("((int64_t)(0 - (uint64_t)(%s)))", right), TInt
		case "+":
			return right, rt
		case "!":
			return fmt.Sprintf("((int64_t)!(%s))", right), TInt
		default:
			return fmt.Sprintf("(%s(%s))", e.Operator, right), rt
		}

	case *InfixExpression:
		left, lt := cg.genExpr(e.Left)
		right, rt := cg.genExpr(e.Right)
		isF := lt == TFloat || rt == TFloat
		if lt == TStr || rt == TStr {
			ls, rs := toStr(left, lt), toStr(right, rt)
			switch e.Operator {
			case "+":
				return fmt.Sprintf("_kpj_cat(%s, %s)", ls, rs), TStr
			case "==":
				return fmt.Sprintf("_kpj_seq(%s, %s)", ls, rs), TInt
			case "!=":
				return fmt.Sprintf("(!_kpj_seq(%s, %s))", ls, rs), TInt
			case "<", ">", "<=", ">=":
				return fmt.Sprintf("((int64_t)(_kpj_scmp(%s, %s) %s 0))", ls, rs, e.Operator), TInt
			case "&&", "||":
				return fmt.Sprintf("((int64_t)((%s) %s (%s)))", left, e.Operator, right), TInt
			}
			return "0LL", TInt
		}
		switch e.Operator {
		case "+", "-", "*":
			if isF {
				return fmt.Sprintf("(%s %s %s)", convert(left, lt, TFloat), e.Operator, convert(right, rt, TFloat)), TFloat
			}
			return fmt.Sprintf("((int64_t)((uint64_t)(%s) %s (uint64_t)(%s)))", left, e.Operator, right), TInt
		case "/":
			if isF {
				return fmt.Sprintf("(%s / %s)", convert(left, lt, TFloat), convert(right, rt, TFloat)), TFloat
			}
			return fmt.Sprintf("_kpj_div(%s, %s)", left, right), TInt
		case "%":
			if isF {
				return fmt.Sprintf("fmod(%s, %s)", convert(left, lt, TFloat), convert(right, rt, TFloat)), TFloat
			}
			return fmt.Sprintf("_kpj_mod(%s, %s)", left, right), TInt
		default:
			return fmt.Sprintf("((int64_t)((%s) %s (%s)))", left, e.Operator, right), TInt
		}

	case *IfExpression:
		tmp := cg.nextTemp()
		cg.writeIndent()
		fmt.Fprintf(cg.buf(), "int64_t %s = 0;\n", tmp)
		cond, _ := cg.genExpr(e.Condition)
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
		return tmp, TInt

	case *CallExpression:
		return cg.genCallExpression(e)
	}
	return "0LL", TInt
}

func (cg *CodeGenerator) genBlockWithResult(block *BlockStatement, resultVar string) {
	for i, s := range block.Statements {
		if i == len(block.Statements)-1 {
			if es, ok := s.(*ExpressionStatement); ok && es.Expression != nil {
				val, vt := cg.genExpr(es.Expression)
				cg.writeIndent()
				fmt.Fprintf(cg.buf(), "%s = %s;\n", resultVar, convert(val, vt, TInt))
				continue
			}
		}
		cg.genStatement(s)
	}
}

func (cg *CodeGenerator) genCallExpression(ce *CallExpression) (string, CType) {
	ident, ok := ce.Function.(*Identifier)
	if !ok {
		return "0LL", TInt
	}

	args := make([]string, len(ce.Arguments))
	types := make([]CType, len(ce.Arguments))
	for i, a := range ce.Arguments {
		args[i], types[i] = cg.genExpr(a)
	}

	switch ident.Value {
	case "read_int", "scan_int", "input_int", "read":
		return "_kpj_read_int()", TInt
	case "read_float", "scan_float", "input_float", "read_double":
		return "_kpj_read_float()", TFloat
	case "arg_int", "arg":
		if len(args) >= 1 {
			return fmt.Sprintf("_kpj_arg_int(%s)", convert(args[0], types[0], TInt)), TInt
		}
		return "0LL", TInt
	case "argc", "arg_count":
		return "_kpj_argc_fn()", TInt
	case "float", "to_float":
		if len(args) >= 1 {
			return convert(args[0], types[0], TFloat), TFloat
		}
		return "0.0", TFloat
	case "int", "to_int":
		if len(args) >= 1 {
			return convert(args[0], types[0], TInt), TInt
		}
		return "0LL", TInt
	case "sqrt":
		if len(args) >= 1 {
			return fmt.Sprintf("sqrt(%s)", convert(args[0], types[0], TFloat)), TFloat
		}
		return "0.0", TFloat
	case "print", "println", "print_int", "print_float", "print_str":
		parts := make([]string, 0, 2*len(args)+2)
		for i := range args {
			if i > 0 {
				parts = append(parts, `_kpj_put(" ", 1)`)
			}
			switch types[i] {
			case TFloat:
				parts = append(parts, fmt.Sprintf("_kpj_w_float(%s)", args[i]))
			case TStr:
				parts = append(parts, fmt.Sprintf("_kpj_w_str(%s)", args[i]))
			default:
				parts = append(parts, fmt.Sprintf("_kpj_w_int(%s)", args[i]))
			}
		}
		parts = append(parts, `_kpj_put("\n", 1)`, "0LL")
		return "(" + strings.Join(parts, ", ") + ")", TInt
	case "read_token", "read_word", "read_str", "read_string":
		return "_kpj_read_token()", TStr
	case "read_line":
		return "_kpj_read_line()", TStr
	case "len", "length", "strlen":
		if len(args) >= 1 {
			return fmt.Sprintf("_kpj_len(%s)", args[0]), TInt
		}
		return "0LL", TInt
	case "upper", "to_upper":
		if len(args) >= 1 {
			return fmt.Sprintf("_kpj_upper(%s)", args[0]), TStr
		}
	case "lower", "to_lower":
		if len(args) >= 1 {
			return fmt.Sprintf("_kpj_lower(%s)", args[0]), TStr
		}
	case "char_at", "at":
		if len(args) >= 2 {
			return fmt.Sprintf("_kpj_char_at(%s, %s)", args[0], convert(args[1], types[1], TInt)), TStr
		}
	case "substr", "substring":
		if len(args) >= 3 {
			return fmt.Sprintf("_kpj_substr(%s, %s, %s)", args[0], convert(args[1], types[1], TInt), convert(args[2], types[2], TInt)), TStr
		}
	case "str", "to_str":
		if len(args) >= 1 {
			return toStr(args[0], types[0]), TStr
		}
	case "exit":
		code := "0"
		if len(args) >= 1 {
			code = fmt.Sprintf("(int)(%s)", convert(args[0], types[0], TInt))
		}
		return fmt.Sprintf("(exit(%s), 0LL)", code), TInt
	case "abs":
		if len(args) >= 1 {
			if types[0] == TFloat {
				return fmt.Sprintf("fabs(%s)", args[0]), TFloat
			}
			return fmt.Sprintf("((%s) < 0 ? (int64_t)(0 - (uint64_t)(%s)) : (%s))", args[0], args[0], args[0]), TInt
		}
		return "0LL", TInt
	case "min", "max":
		if len(args) >= 2 {
			op := "<"
			if ident.Value == "max" {
				op = ">"
			}
			t := TInt
			if types[0] == TFloat || types[1] == TFloat {
				t = TFloat
			}
			a, b := convert(args[0], types[0], t), convert(args[1], types[1], t)
			return fmt.Sprintf("((%s) %s (%s) ? (%s) : (%s))", a, op, b, a, b), t
		}
		return "0LL", TInt
	default:
		ints := make([]string, len(args))
		for i := range args {
			ints[i] = convert(args[i], types[i], TInt)
		}
		return fmt.Sprintf("%s(%s)", cg.cFuncName(ident.Value), strings.Join(ints, ", ")), TInt
	}
	return "0LL", TInt
}

func runCC(ccBin string, args []string) (string, error) {
	cmd := exec.Command(ccBin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stderr.String(), err
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

	var ccBin string
	for _, candidate := range []string{"gcc", "/usr/bin/gcc", "clang", "/usr/bin/clang", "cc"} {
		if path, err := exec.LookPath(candidate); err == nil {
			ccBin = path
			break
		}
	}
	if ccBin == "" {
		return fmt.Errorf("no C compiler (gcc/clang) found in PATH")
	}

	// Fast-start flags: static link (no dynamic loader), strip, no unwind tables.
	base := []string{"-O2", "-std=gnu11", "-w", "-pipe",
		"-fno-asynchronous-unwind-tables", "-fno-stack-protector", "-fomit-frame-pointer",
		"-fno-plt", "-o", outputPath, tmpPath, "-lm"}
	fast := append([]string{"-static", "-s", "-Wl,-z,norelro", "-Wl,--build-id=none"}, base...)

	msg, err := runCC(ccBin, fast)
	if err != nil {
		// Fallback: dynamic link
		msg2, err2 := runCC(ccBin, base)
		if err2 != nil {
			return fmt.Errorf("C compilation failed (%v): %s%s", err2, msg, msg2)
		}
	}

	if err := os.Chmod(outputPath, 0755); err != nil {
		return fmt.Errorf("failed to chmod output binary: %w", err)
	}

	binData, err := os.ReadFile(outputPath)
	if err != nil {
		return fmt.Errorf("failed to read compiled binary for verification: %w", err)
	}
	if !bytes.Contains(binData, []byte("sign kopaj")) {
		return fmt.Errorf("compiled binary is missing required signature 'sign kopaj'")
	}

	return nil
}

func toStr(code string, t CType) string {
	switch t {
	case TStr:
		return code
	case TFloat:
		return fmt.Sprintf("_kpj_str_float(%s)", code)
	}
	return fmt.Sprintf("_kpj_str_int(%s)", code)
}

func cEscape(v string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c == '"' || c == '\\':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		case c >= 32 && c < 127 && c != '?':
			sb.WriteByte(c)
		default:
			fmt.Fprintf(&sb, "\\%03o", c)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}
