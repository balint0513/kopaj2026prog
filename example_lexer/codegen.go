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
    if (kopaj_flag[0] == '\0') _kpj_exit(1);
`)
	out.WriteString(cg.mainBody.String())
	out.WriteString("    _kpj_exit(0);\n}\n")

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
				fmt.Fprintf(cg.buf(), "_kpj_exit((int)(%s));\n", convert(valExpr, vt, TInt))
			}
		} else {
			cg.writeIndent()
			if cg.inFunc {
				fmt.Fprintf(cg.buf(), "return 0;\n")
			} else {
				fmt.Fprintf(cg.buf(), "_kpj_exit(0);\n")
			}
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

		fmt.Fprintf(&cg.functions, "static inline int64_t %s(%s) {\n", cg.cFuncName(s.Name.Value), paramList)
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
				return fmt.Sprintf("_kpj_fmod(%s, %s)", convert(left, lt, TFloat), convert(right, rt, TFloat)), TFloat
			}
			return fmt.Sprintf("_kpj_mod(%s, %s)", left, right), TInt
		default:
			if isF {
				return fmt.Sprintf("((int64_t)((%s) %s (%s)))", convert(left, lt, TFloat), e.Operator, convert(right, rt, TFloat)), TInt
			}
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
	case "log", "ln", "exp", "sin", "cos", "tan", "floor", "ceil":
		if len(args) >= 1 {
			fn := ident.Value
			if fn == "ln" {
				fn = "log"
			}
			return fmt.Sprintf("_kpj_%s(%s)", fn, convert(args[0], types[0], TFloat)), TFloat
		}
		return "0.0", TFloat
	case "pow":
		if len(args) >= 2 {
			return fmt.Sprintf("_kpj_pow(%s, %s)", convert(args[0], types[0], TFloat), convert(args[1], types[1], TFloat)), TFloat
		}
		return "0.0", TFloat
	case "prime_pi":
		if len(args) >= 1 {
			return fmt.Sprintf("_kpj_prime_pi(%s)", convert(args[0], types[0], TInt)), TInt
		}
		return "0LL", TInt
	case "is_finite", "isfinite":
		if len(args) >= 1 {
			return fmt.Sprintf("((int64_t)__builtin_isfinite(%s))", convert(args[0], types[0], TFloat)), TInt
		}
		return "0LL", TInt
	case "sqrt":
		if len(args) >= 1 {
			return fmt.Sprintf("_kpj_sqrt(%s)", convert(args[0], types[0], TFloat)), TFloat
		}
		return "0.0", TFloat
	case "print", "println", "print_int", "print_float", "print_str":
		parts := make([]string, 0, 2*len(args)+2)
		for i := range args {
			if i > 0 {
				parts = append(parts, `_kpj_putc(' ')`)
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
		parts = append(parts, `_kpj_putc('\n')`, "0LL")
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
		return fmt.Sprintf("(_kpj_exit(%s), 0LL)", code), TInt
	case "abs":
		if len(args) >= 1 {
			if types[0] == TFloat {
				return fmt.Sprintf("_kpj_fabs(%s)", args[0]), TFloat
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

	// Primary: freestanding static binary (no libc, raw syscalls, single-page ELF, zero startup overhead).
	freestanding := []string{
		"-O3", "-march=x86-64-v2", "-std=gnu11", "-w", "-pipe",
		"-DKPJ_FREESTANDING",
		"-nostdlib", "-static", "-no-pie", "-fno-pie", "-fcf-protection=none",
		"-fno-math-errno", "-fno-trapping-math",
		"-fno-asynchronous-unwind-tables", "-fno-unwind-tables",
		"-fno-stack-protector", "-fomit-frame-pointer", "-fno-plt",
		"-ffunction-sections", "-fdata-sections",
		"-Wl,--gc-sections", "-Wl,--build-id=none", "-Wl,-z,norelro",
		"-Wl,-z,noseparate-code", "-Wl,-z,max-page-size=4096",
		"-s", "-o", outputPath, tmpPath,
	}

	msg, err := runCC(ccBin, freestanding)
	if err != nil {
		// Fallback: standard C runtime linking
		fallback := []string{
			"-O3", "-std=gnu11", "-w", "-pipe",
			"-fno-math-errno", "-fno-asynchronous-unwind-tables", "-fno-stack-protector", "-fomit-frame-pointer",
			"-s", "-o", outputPath, tmpPath, "-lm",
		}
		msg2, err2 := runCC(ccBin, fallback)
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
