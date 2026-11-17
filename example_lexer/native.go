package main

// In-memory native x86-64 backend.
//
// Instead of generating C and spawning gcc, the compiler emits machine code
// directly and patches it into a prebuilt static runtime ELF (runtime.elf,
// built from runtime.c by gen_runtime.go). The runtime reserves an executable
// area (kpj_code) that main() jumps into. Anything the backend cannot handle
// returns an error so the caller can fall back to the gcc path.

import (
	"fmt"
	"math"
	"math/bits"
	"os"
	"path/filepath"
)

var runtimeELF = []byte(runtimeELFData)

const (
	rAX = 0
	rCX = 1
	rDX = 2
	rSI = 6
	rDI = 7
	r8  = 8
	r9  = 9
)

// condition codes
const (
	ccB  = 2
	ccAE = 3
	ccE  = 4
	ccNE = 5
	ccBE = 6
	ccA  = 7
	ccP  = 10
	ccL  = 12
	ccGE = 13
	ccLE = 14
	ccG  = 15
)

type nativeError struct{ msg string }

type fixup struct {
	pos   int
	label int
}

type dataFixup struct {
	pos int // position of imm64 in code
	off int // offset in data
}

type nvar struct {
	typ CType
	off int
	reg int // callee-saved register holding the variable, or -1
}

var regPool = []int{3, 12, 13, 14, 15} // rbx, r12-r15

type nativeFunc struct {
	label int
	arity int
	stmt  *FunctionStatement
}

type native struct {
	b       []byte
	labels  []int
	fixups  []fixup
	data    []byte
	dataFix []dataFixup
	strLits map[string]int

	funcs map[string]*nativeFunc

	// per-function state
	scopes   []map[string]nvar
	frame    int
	depth    int
	retLabel int
	nreg     int
	savePos  [5]int
}

func (g *native) fail(format string, a ...interface{}) {
	panic(nativeError{fmt.Sprintf(format, a...)})
}

// ---------------------------------------------------------------- emitter

func (g *native) emit(bs ...byte) { g.b = append(g.b, bs...) }

func (g *native) imm32(v int32) {
	g.emit(byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

func (g *native) imm64(v uint64) {
	for i := 0; i < 8; i++ {
		g.emit(byte(v >> (8 * i)))
	}
}

func (g *native) rex(w bool, reg, rm int) {
	v := byte(0x40)
	if w {
		v |= 8
	}
	if reg >= 8 {
		v |= 4
	}
	if rm >= 8 {
		v |= 1
	}
	if v != 0x40 {
		g.emit(v)
	}
}

func (g *native) rr(prefix byte, w bool, opc []byte, reg, rm int) {
	if prefix != 0 {
		g.emit(prefix)
	}
	g.rex(w, reg, rm)
	g.emit(opc...)
	g.emit(0xC0 | byte(reg&7)<<3 | byte(rm&7))
}

func (g *native) mem(prefix byte, w bool, opc []byte, reg int, disp int) {
	if prefix != 0 {
		g.emit(prefix)
	}
	g.rex(w, reg, 0)
	g.emit(opc...)
	g.emit(0x80 | byte(reg&7)<<3 | 5)
	g.imm32(int32(disp))
}

func (g *native) newLabel() int {
	g.labels = append(g.labels, -1)
	return len(g.labels) - 1
}

func (g *native) bind(l int) { g.labels[l] = len(g.b) }

func (g *native) jmp(l int) {
	g.emit(0xE9)
	g.fixups = append(g.fixups, fixup{len(g.b), l})
	g.imm32(0)
}

func (g *native) jcc(cc int, l int) {
	g.emit(0x0F, byte(0x80+cc))
	g.fixups = append(g.fixups, fixup{len(g.b), l})
	g.imm32(0)
}

func (g *native) push(r int) {
	if r >= 8 {
		g.emit(0x41)
	}
	g.emit(byte(0x50 + r&7))
	g.depth++
}

func (g *native) pop(r int) {
	if r >= 8 {
		g.emit(0x41)
	}
	g.emit(byte(0x58 + r&7))
	g.depth--
}

func (g *native) movRI(r int, v int64) {
	switch {
	case v == 0:
		g.rr(0, false, []byte{0x31}, r, r) // xor r32, r32 (clobbers flags)
	case v > 0 && v <= math.MaxUint32:
		if r >= 8 {
			g.emit(0x41)
		}
		g.emit(byte(0xB8 + r&7))
		g.imm32(int32(uint32(v)))
	case v >= math.MinInt32 && v <= math.MaxInt32:
		g.rr(0, true, []byte{0xC7}, 0, r)
		g.imm32(int32(v))
	default:
		v0 := byte(0x48)
		if r >= 8 {
			v0 |= 1
		}
		g.emit(v0, byte(0xB8+r&7))
		g.imm64(uint64(v))
	}
}

func (g *native) movRR(dst, src int) {
	if dst != src {
		g.rr(0, true, []byte{0x89}, src, dst)
	}
}

func (g *native) load(r int, off int)  { g.mem(0, true, []byte{0x8B}, r, -off) }
func (g *native) store(off int, r int) { g.mem(0, true, []byte{0x89}, r, -off) }

func (g *native) alu(opc byte, dst, src int) { g.rr(0, true, []byte{opc}, src, dst) }
func (g *native) aluImm(ext int, dst int, v int32) {
	g.rr(0, true, []byte{0x81}, ext, dst)
	g.imm32(v)
}
func (g *native) imul(dst, src int) { g.rr(0, true, []byte{0x0F, 0xAF}, dst, src) }
func (g *native) neg(r int)         { g.rr(0, true, []byte{0xF7}, 3, r) }
func (g *native) testRR(a, b int)   { g.rr(0, true, []byte{0x85}, b, a) }
func (g *native) shift(ext, r int, n byte) {
	g.rr(0, true, []byte{0xC1}, ext, r)
	g.emit(n)
}

func (g *native) setLabelImm(r int, dataOff int) {
	// mov r64, imm64 (patched later with the data address)
	v0 := byte(0x48)
	if r >= 8 {
		v0 |= 1
	}
	g.emit(v0, byte(0xB8+r&7))
	g.dataFix = append(g.dataFix, dataFixup{len(g.b), dataOff})
	g.imm64(0)
}

// SSE helpers (xmm register numbers 0..1 only)
func (g *native) sse(prefix, op byte, dst, src int) {
	g.rr(prefix, false, []byte{0x0F, op}, dst, src)
}
func (g *native) movqXG(x, r int) { g.rr(0x66, true, []byte{0x0F, 0x6E}, x, r) }
func (g *native) movqGX(r, x int) { g.rr(0x66, true, []byte{0x0F, 0x7E}, x, r) }
func (g *native) movapd(dst, src int) {
	g.rr(0x66, false, []byte{0x0F, 0x28}, dst, src)
}
func (g *native) ucomisd(a, b int) { g.rr(0x66, false, []byte{0x0F, 0x2E}, a, b) }
func (g *native) cvtsi2sd(x, r int) {
	g.rr(0xF2, true, []byte{0x0F, 0x2A}, x, r)
}
func (g *native) cvttsd2si(r, x int) {
	g.rr(0xF2, true, []byte{0x0F, 0x2C}, r, x)
}

// ---------------------------------------------------------------- calls

func (g *native) sym(name string) uint64 {
	a, ok := rtSyms[name]
	if !ok {
		g.fail("missing runtime symbol %s", name)
	}
	return a
}

func (g *native) pad() bool {
	if g.depth%2 != 0 {
		g.emit(0x48, 0x83, 0xEC, 0x08) // sub rsp, 8
		return true
	}
	return false
}

func (g *native) unpad(p bool) {
	if p {
		g.emit(0x48, 0x83, 0xC4, 0x08) // add rsp, 8
	}
}

func (g *native) callRT(name string) {
	target := g.sym(name)
	p := g.pad()
	g.emit(0xE8)
	next := uint64(rtCodeVaddr) + uint64(len(g.b)) + 4
	g.imm32(int32(int64(target) - int64(next)))
	g.unpad(p)
}

func (g *native) callLabel(l int) {
	p := g.pad()
	g.emit(0xE8)
	g.fixups = append(g.fixups, fixup{len(g.b), l})
	g.imm32(0)
	g.unpad(p)
}

// ---------------------------------------------------------------- scopes

func (g *native) pushScope() { g.scopes = append(g.scopes, map[string]nvar{}) }
func (g *native) popScope()  { g.scopes = g.scopes[:len(g.scopes)-1] }

func (g *native) lookup(name string) (nvar, bool) {
	for i := len(g.scopes) - 1; i >= 0; i-- {
		if v, ok := g.scopes[i][name]; ok {
			return v, true
		}
	}
	return nvar{}, false
}

func (g *native) lookupCurrent(name string) (nvar, bool) {
	v, ok := g.scopes[len(g.scopes)-1][name]
	return v, ok
}

func (g *native) alloc(size int) int {
	g.frame += size
	return g.frame
}

func (g *native) declare(name string, t CType) nvar {
	var v nvar
	if t == TInt && g.nreg < len(regPool) {
		v = nvar{t, 0, regPool[g.nreg]}
		g.nreg++
	} else {
		sz := 8
		if t == TStr {
			sz = 16
		}
		v = nvar{t, g.alloc(sz), -1}
	}
	g.scopes[len(g.scopes)-1][name] = v
	return v
}

func (g *native) storeVar(v nvar) {
	switch v.typ {
	case TInt:
		if v.reg >= 0 {
			g.movRR(v.reg, rAX)
			return
		}
		g.store(v.off, rAX)
	case TFloat:
		g.mem(0xF2, false, []byte{0x0F, 0x11}, 0, -v.off)
	case TStr:
		g.store(v.off, rAX)
		g.store(v.off-8, rDX)
	}
}

func (g *native) loadVar(v nvar) {
	switch v.typ {
	case TInt:
		if v.reg >= 0 {
			g.movRR(rAX, v.reg)
			return
		}
		g.load(rAX, v.off)
	case TFloat:
		g.mem(0xF2, false, []byte{0x0F, 0x10}, 0, -v.off)
	case TStr:
		g.load(rAX, v.off)
		g.load(rDX, v.off-8)
	}
}

// ---------------------------------------------------------------- types

type bkind int

const (
	bNone bkind = iota
	bReadInt
	bReadFloat
	bArgInt
	bArgc
	bFloat
	bInt
	bMath1
	bPow
	bPrimePi
	bIsFinite
	bSqrt
	bPrint
	bReadToken
	bReadLine
	bLen
	bUpper
	bLower
	bCharAt
	bSubstr
	bStr
	bExit
	bAbs
	bMin
	bMax
)

var builtins = map[string]bkind{
	"read_int": bReadInt, "scan_int": bReadInt, "input_int": bReadInt, "read": bReadInt,
	"read_float": bReadFloat, "scan_float": bReadFloat, "input_float": bReadFloat, "read_double": bReadFloat,
	"arg_int": bArgInt, "arg": bArgInt, "argc": bArgc, "arg_count": bArgc,
	"float": bFloat, "to_float": bFloat, "int": bInt, "to_int": bInt,
	"log": bMath1, "ln": bMath1, "exp": bMath1, "sin": bMath1, "cos": bMath1, "tan": bMath1,
	"floor": bMath1, "ceil": bMath1,
	"pow": bPow, "prime_pi": bPrimePi, "is_finite": bIsFinite, "isfinite": bIsFinite, "sqrt": bSqrt,
	"print": bPrint, "println": bPrint, "print_int": bPrint, "print_float": bPrint, "print_str": bPrint,
	"read_token": bReadToken, "read_word": bReadToken, "read_str": bReadToken, "read_string": bReadToken,
	"read_line": bReadLine,
	"len":       bLen, "length": bLen, "strlen": bLen,
	"upper": bUpper, "to_upper": bUpper, "lower": bLower, "to_lower": bLower,
	"char_at": bCharAt, "at": bCharAt, "substr": bSubstr, "substring": bSubstr,
	"str": bStr, "to_str": bStr, "exit": bExit, "abs": bAbs, "min": bMin, "max": bMax,
}

var builtinArity = map[bkind]int{
	bReadInt: 0, bReadFloat: 0, bArgInt: 1, bArgc: 0, bFloat: 1, bInt: 1, bMath1: 1, bPow: 2,
	bPrimePi: 1, bIsFinite: 1, bSqrt: 1, bReadToken: 0, bReadLine: 0, bLen: 1, bUpper: 1,
	bLower: 1, bCharAt: 2, bSubstr: 3, bStr: 1, bExit: 1, bAbs: 1, bMin: 2, bMax: 2,
}

func (g *native) inferCall(ce *CallExpression) CType {
	ident, ok := ce.Function.(*Identifier)
	if !ok {
		g.fail("indirect call")
	}
	bk, isB := builtins[ident.Value]
	if !isB {
		return TInt
	}
	switch bk {
	case bReadFloat, bFloat, bMath1, bPow, bSqrt:
		return TFloat
	case bReadToken, bReadLine, bUpper, bLower, bCharAt, bSubstr, bStr:
		return TStr
	case bAbs:
		return g.infer(ce.Arguments[0])
	case bMin, bMax:
		if g.infer(ce.Arguments[0]) == TFloat || g.infer(ce.Arguments[1]) == TFloat {
			return TFloat
		}
		return TInt
	}
	return TInt
}

func isCmp(op string) bool {
	switch op {
	case "==", "!=", "<", ">", "<=", ">=":
		return true
	}
	return false
}

func (g *native) infer(expr Expression) CType {
	switch e := expr.(type) {
	case *IntegerLiteral, *BooleanLiteral:
		return TInt
	case *FloatLiteral:
		return TFloat
	case *StringLiteral:
		return TStr
	case *Identifier:
		v, ok := g.lookup(e.Value)
		if !ok {
			g.fail("undefined variable %s", e.Value)
		}
		return v.typ
	case *PrefixExpression:
		switch e.Operator {
		case "-", "+":
			return g.infer(e.Right)
		case "!":
			return TInt
		}
		g.fail("prefix %s", e.Operator)
	case *InfixExpression:
		lt, rt := g.infer(e.Left), g.infer(e.Right)
		if lt == TStr || rt == TStr {
			if e.Operator == "+" {
				return TStr
			}
			if isCmp(e.Operator) {
				return TInt
			}
			g.fail("string operator %s", e.Operator)
		}
		switch e.Operator {
		case "+", "-", "*", "/", "%":
			if lt == TFloat || rt == TFloat {
				return TFloat
			}
			return TInt
		case "&&", "||":
			return TInt
		}
		if isCmp(e.Operator) {
			return TInt
		}
		g.fail("operator %s", e.Operator)
	case *IfExpression:
		return TInt
	case *CallExpression:
		return g.inferCall(e)
	}
	g.fail("unsupported expression %T", expr)
	return TInt
}

// ---------------------------------------------------------------- values

func (g *native) conv(from, to CType) {
	if from == to {
		return
	}
	if from == TStr || to == TStr {
		g.fail("string conversion")
	}
	if to == TFloat {
		g.cvtsi2sd(0, rAX)
	} else {
		g.cvttsd2si(rAX, 0)
	}
}

func (g *native) evalAs(e Expression, want CType) {
	t := g.genExpr(e)
	g.conv(t, want)
}

func (g *native) pushV(t CType) {
	switch t {
	case TInt:
		g.push(rAX)
	case TFloat:
		g.movqGX(rAX, 0)
		g.push(rAX)
	case TStr:
		g.push(rDX)
		g.push(rAX)
	}
}

// popFloatTo1 pops a pushed float into xmm0 with the current xmm0 moved to xmm1.
func (g *native) popFloatLeft() {
	g.movapd(1, 0)
	g.pop(rAX)
	g.movqXG(0, rAX)
}

func (g *native) strLit(s string) int {
	if off, ok := g.strLits[s]; ok {
		return off
	}
	off := len(g.data)
	g.data = append(g.data, s...)
	g.data = append(g.data, 0)
	g.strLits[s] = off
	return off
}

// genStrExpr evaluates e and converts it to a string in rax:rdx.
func (g *native) genStrExpr(e Expression) {
	t := g.genExpr(e)
	g.toStr(t)
}

func (g *native) toStr(t CType) {
	switch t {
	case TInt:
		g.movRR(rDI, rAX)
		g.callRT("_kpj_str_int")
	case TFloat:
		g.callRT("_kpj_str_float")
	}
}

func isSimpleInt(g *native, e Expression) bool {
	switch x := e.(type) {
	case *IntegerLiteral, *BooleanLiteral:
		return true
	case *Identifier:
		v, ok := g.lookup(x.Value)
		return ok && v.typ == TInt
	}
	return false
}

func litInt(e Expression) (int64, bool) {
	switch x := e.(type) {
	case *IntegerLiteral:
		return x.Value, true
	case *BooleanLiteral:
		if x.Value {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

// intOperands leaves left in rax and, unless the right side is a small
// literal (returned as imm), right in rcx.
func (g *native) intOperands(l, r Expression) (imm int64, isImm bool) {
	if v, ok := litInt(r); ok && v >= math.MinInt32 && v <= math.MaxInt32 {
		g.evalAs(l, TInt)
		return v, true
	}
	if isSimpleInt(g, r) {
		g.evalAs(l, TInt)
		g.genSimpleTo(rCX, r)
		return 0, false
	}
	if isSimpleInt(g, l) {
		// left has no side effects and cannot be changed by evaluating right
		g.evalAs(r, TInt)
		g.movRR(rCX, rAX)
		g.genSimpleTo(rAX, l)
		return 0, false
	}
	g.evalAs(l, TInt)
	g.push(rAX)
	g.evalAs(r, TInt)
	g.movRR(rCX, rAX)
	g.pop(rAX)
	return 0, false
}

func (g *native) genSimpleTo(reg int, e Expression) {
	if v, ok := litInt(e); ok {
		g.movRI(reg, v)
		return
	}
	id := e.(*Identifier)
	v, _ := g.lookup(id.Value)
	if v.reg >= 0 {
		g.movRR(reg, v.reg)
		return
	}
	g.load(reg, v.off)
}

func (g *native) genExpr(expr Expression) CType {
	switch e := expr.(type) {
	case nil:
		g.movRI(rAX, 0)
		return TInt
	case *IntegerLiteral:
		g.movRI(rAX, e.Value)
		return TInt
	case *BooleanLiteral:
		if e.Value {
			g.movRI(rAX, 1)
		} else {
			g.movRI(rAX, 0)
		}
		return TInt
	case *FloatLiteral:
		g.movRI(rAX, int64(math.Float64bits(e.Value)))
		g.movqXG(0, rAX)
		return TFloat
	case *StringLiteral:
		off := g.strLit(e.Value)
		g.setLabelImm(rAX, off)
		g.movRI(rDX, int64(len(e.Value)))
		return TStr
	case *Identifier:
		v, ok := g.lookup(e.Value)
		if !ok {
			g.fail("undefined variable %s", e.Value)
		}
		g.loadVar(v)
		return v.typ
	case *PrefixExpression:
		return g.genPrefix(e)
	case *InfixExpression:
		return g.genInfix(e)
	case *IfExpression:
		return g.genIfExpr(e)
	case *CallExpression:
		return g.genCall(e)
	}
	g.fail("unsupported expression %T", expr)
	return TInt
}

func (g *native) genPrefix(e *PrefixExpression) CType {
	switch e.Operator {
	case "+":
		return g.genExpr(e.Right)
	case "-":
		t := g.genExpr(e.Right)
		switch t {
		case TFloat:
			g.movqGX(rAX, 0)
			g.emit(0x48, 0x0F, 0xBA, 0xF8, 63) // btc rax, 63
			g.movqXG(0, rAX)
			return TFloat
		case TInt:
			g.neg(rAX)
			return TInt
		}
		g.fail("negate string")
	case "!":
		g.genBoolValue(e)
		return TInt
	}
	g.fail("prefix %s", e.Operator)
	return TInt
}

func (g *native) genBoolValue(e Expression) {
	lf := g.newLabel()
	le := g.newLabel()
	g.condJump(e, lf, false)
	g.movRI(rAX, 1)
	g.jmp(le)
	g.bind(lf)
	g.movRI(rAX, 0)
	g.bind(le)
}

func (g *native) genInfix(e *InfixExpression) CType {
	lt, rt := g.infer(e.Left), g.infer(e.Right)
	if lt == TStr || rt == TStr {
		if e.Operator == "+" {
			g.strPair(e.Left, e.Right)
			g.callRT("_kpj_cat")
			return TStr
		}
		g.genBoolValue(e)
		return TInt
	}
	if e.Operator == "&&" || e.Operator == "||" || isCmp(e.Operator) {
		g.genBoolValue(e)
		return TInt
	}
	if lt == TFloat || rt == TFloat {
		g.evalAs(e.Left, TFloat)
		g.pushV(TFloat)
		g.evalAs(e.Right, TFloat)
		g.popFloatLeft() // xmm0 = left, xmm1 = right
		switch e.Operator {
		case "+":
			g.sse(0xF2, 0x58, 0, 1)
		case "-":
			g.sse(0xF2, 0x5C, 0, 1)
		case "*":
			g.sse(0xF2, 0x59, 0, 1)
		case "/":
			g.sse(0xF2, 0x5E, 0, 1)
		case "%":
			g.callRT("kpj_fmod")
		default:
			g.fail("float operator %s", e.Operator)
		}
		return TFloat
	}
	imm, isImm := g.intOperands(e.Left, e.Right)
	switch e.Operator {
	case "+":
		if isImm {
			g.aluImm(0, rAX, int32(imm))
		} else {
			g.alu(0x01, rAX, rCX)
		}
	case "-":
		if isImm {
			g.aluImm(5, rAX, int32(imm))
		} else {
			g.alu(0x29, rAX, rCX)
		}
	case "*":
		if isImm {
			g.movRI(rCX, imm)
		}
		g.imul(rAX, rCX)
	case "/", "%":
		g.genDivMod(e.Operator == "%", imm, isImm)
	default:
		g.fail("operator %s", e.Operator)
	}
	return TInt
}

func (g *native) genDivMod(isMod bool, imm int64, isImm bool) {
	if isImm {
		switch {
		case imm == 0:
			g.movRI(rAX, 0)
			return
		case imm == -1:
			if isMod {
				g.movRI(rAX, 0)
			} else {
				g.neg(rAX)
			}
			return
		case imm == 1:
			if isMod {
				g.movRI(rAX, 0)
			}
			return
		case imm > 1 && imm&(imm-1) == 0 && imm <= 1<<30:
			k := byte(bits.TrailingZeros64(uint64(imm)))
			g.movRR(rDX, rAX)
			g.shift(7, rDX, 63)
			g.shift(5, rDX, 64-k)
			g.alu(0x01, rAX, rDX)
			if isMod {
				g.aluImm(4, rAX, int32(imm-1))
				g.alu(0x29, rAX, rDX)
			} else {
				g.shift(7, rAX, k)
			}
			return
		}
		if imm > 1 {
			g.genMagic(isMod, imm)
			return
		}
		g.movRI(rCX, imm)
	}
	lz := g.newLabel()
	lm1 := g.newLabel()
	le := g.newLabel()
	g.testRR(rCX, rCX)
	g.jcc(ccE, lz)
	g.aluImm(7, rCX, -1)
	g.jcc(ccE, lm1)
	g.emit(0x48, 0x99)                  // cqo
	g.rr(0, true, []byte{0xF7}, 7, rCX) // idiv rcx
	if isMod {
		g.movRR(rAX, rDX)
	}
	g.jmp(le)
	g.bind(lm1)
	if isMod {
		g.movRI(rAX, 0)
	} else {
		g.neg(rAX)
	}
	g.jmp(le)
	g.bind(lz)
	g.movRI(rAX, 0)
	g.bind(le)
}

// magicS64 computes the signed division magic number for d >= 2 (Hacker's Delight).
func magicS64(d int64) (int64, uint) {
	const two63 = uint64(1) << 63
	ad := uint64(d)
	t := two63
	anc := t - 1 - t%ad
	p := uint(63)
	q1 := two63 / anc
	r1 := two63 - q1*anc
	q2 := two63 / ad
	r2 := two63 - q2*ad
	for {
		p++
		q1 *= 2
		r1 *= 2
		if r1 >= anc {
			q1++
			r1 -= anc
		}
		q2 *= 2
		r2 *= 2
		if r2 >= ad {
			q2++
			r2 -= ad
		}
		delta := ad - r2
		if !(q1 < delta || (q1 == delta && r1 == 0)) {
			break
		}
	}
	return int64(q2 + 1), p - 64
}

// genMagic divides/mods rax by constant imm (>=2, not a power of two) without idiv.
func (g *native) genMagic(isMod bool, imm int64) {
	m, sh := magicS64(imm)
	g.movRR(rCX, rAX)
	g.movRI(rSI, m)
	g.rr(0, true, []byte{0xF7}, 5, rSI) // imul rsi -> rdx:rax
	if m < 0 {
		g.alu(0x01, rDX, rCX)
	}
	if sh > 0 {
		g.shift(7, rDX, byte(sh))
	}
	g.movRR(rAX, rDX)
	g.shift(5, rAX, 63)
	g.alu(0x01, rAX, rDX)
	if isMod {
		g.rr(0, true, []byte{0x69}, rAX, rAX) // imul rax, rax, imm32
		g.imm32(int32(imm))
		g.alu(0x29, rCX, rAX)
		g.movRR(rAX, rCX)
	}
}

// strPair evaluates two expressions as strings: left -> rdi:rsi, right -> rdx:rcx.
func (g *native) strPair(l, r Expression) {
	g.genStrExpr(l)
	g.pushV(TStr)
	g.genStrExpr(r)
	g.movRR(rCX, rDX)
	g.movRR(rDX, rAX)
	g.pop(rDI)
	g.pop(rSI)
}

// condJump evaluates e as a condition and jumps to target if its truth value
// equals jumpIfTrue; otherwise falls through.
func (g *native) condJump(expr Expression, target int, jumpIfTrue bool) {
	switch e := expr.(type) {
	case *PrefixExpression:
		if e.Operator == "!" {
			g.condJump(e.Right, target, !jumpIfTrue)
			return
		}
	case *InfixExpression:
		switch {
		case e.Operator == "&&" || e.Operator == "||":
			isAnd := e.Operator == "&&"
			if isAnd != jumpIfTrue {
				// and/jumpIfFalse or or/jumpIfTrue: either operand decides
				g.condJump(e.Left, target, jumpIfTrue)
				g.condJump(e.Right, target, jumpIfTrue)
			} else {
				skip := g.newLabel()
				g.condJump(e.Left, skip, !jumpIfTrue)
				g.condJump(e.Right, target, jumpIfTrue)
				g.bind(skip)
			}
			return
		case isCmp(e.Operator):
			g.cmpJump(e, target, jumpIfTrue)
			return
		}
	}
	t := g.genExpr(expr)
	switch t {
	case TInt:
		g.testRR(rAX, rAX)
		if jumpIfTrue {
			g.jcc(ccNE, target)
		} else {
			g.jcc(ccE, target)
		}
	case TFloat:
		g.sse(0x66, 0x57, 1, 1) // xorpd xmm1, xmm1
		g.ucomisd(0, 1)
		g.floatEqJump(false, target, jumpIfTrue) // truthy == (x != 0)
	default:
		g.fail("string used as condition")
	}
}

// floatEqJump: after ucomisd, jump on equality (eq=true) or inequality.
func (g *native) floatEqJump(eq bool, target int, jumpIfTrue bool) {
	truthWhenEqualFlags := eq
	// condition "true" means: eq -> ZF&&!PF ; ne -> !ZF||PF
	if truthWhenEqualFlags == jumpIfTrue {
		// jump when ZF&&!PF
		skip := g.newLabel()
		g.jcc(ccP, skip)
		g.jcc(ccE, target)
		g.bind(skip)
	} else {
		// jump when !ZF||PF
		g.jcc(ccP, target)
		g.jcc(ccNE, target)
	}
}

func (g *native) cmpJump(e *InfixExpression, target int, jumpIfTrue bool) {
	lt, rt := g.infer(e.Left), g.infer(e.Right)
	switch {
	case lt == TStr || rt == TStr:
		g.strPair(e.Left, e.Right)
		switch e.Operator {
		case "==", "!=":
			g.callRT("_kpj_seq")
			g.testRR(rAX, rAX)
			wantEq := e.Operator == "=="
			if wantEq == jumpIfTrue {
				g.jcc(ccNE, target)
			} else {
				g.jcc(ccE, target)
			}
		default:
			g.callRT("_kpj_scmp")
			g.rr(0, true, []byte{0x63}, rAX, rAX) // movsxd rax, eax
			g.testRR(rAX, rAX)
			cc := map[string]int{"<": ccL, "<=": ccLE, ">": ccG, ">=": ccGE}[e.Operator]
			if !jumpIfTrue {
				cc ^= 1
			}
			g.jcc(cc, target)
		}
	case lt == TFloat || rt == TFloat:
		g.evalAs(e.Left, TFloat)
		g.pushV(TFloat)
		g.evalAs(e.Right, TFloat)
		g.popFloatLeft() // xmm0 = a, xmm1 = b
		switch e.Operator {
		case "<", "<=", ">", ">=":
			if e.Operator == "<" || e.Operator == "<=" {
				g.ucomisd(1, 0)
			} else {
				g.ucomisd(0, 1)
			}
			var tcc, fcc int
			if e.Operator == "<" || e.Operator == ">" {
				tcc, fcc = ccA, ccBE
			} else {
				tcc, fcc = ccAE, ccB
			}
			if jumpIfTrue {
				g.jcc(tcc, target)
			} else {
				g.jcc(fcc, target)
			}
		case "==":
			g.ucomisd(0, 1)
			g.floatEqJump(true, target, jumpIfTrue)
		case "!=":
			g.ucomisd(0, 1)
			g.floatEqJump(false, target, jumpIfTrue)
		}
	default:
		imm, isImm := g.intOperands(e.Left, e.Right)
		if isImm {
			g.aluImm(7, rAX, int32(imm))
		} else {
			g.alu(0x39, rAX, rCX)
		}
		cc := map[string]int{"==": ccE, "!=": ccNE, "<": ccL, "<=": ccLE, ">": ccG, ">=": ccGE}[e.Operator]
		if !jumpIfTrue {
			cc ^= 1
		}
		g.jcc(cc, target)
	}
}

func (g *native) genIfExpr(e *IfExpression) CType {
	tmp := g.alloc(8)
	g.movRI(rAX, 0)
	g.store(tmp, rAX)
	lelse := g.newLabel()
	lend := g.newLabel()
	g.condJump(e.Condition, lelse, false)
	g.pushScope()
	g.genBlockWithResult(e.Consequence, tmp)
	g.popScope()
	if e.Alternative != nil {
		g.jmp(lend)
		g.bind(lelse)
		g.pushScope()
		g.genBlockWithResult(e.Alternative, tmp)
		g.popScope()
		g.bind(lend)
	} else {
		g.bind(lelse)
	}
	g.load(rAX, tmp)
	return TInt
}

func (g *native) genBlockWithResult(block *BlockStatement, tmp int) {
	for i, s := range block.Statements {
		if i == len(block.Statements)-1 {
			if es, ok := s.(*ExpressionStatement); ok && es.Expression != nil {
				g.evalAs(es.Expression, TInt)
				g.store(tmp, rAX)
				continue
			}
		}
		g.genStatement(s)
	}
}

// ---------------------------------------------------------------- calls

func (g *native) genCall(ce *CallExpression) CType {
	ident, ok := ce.Function.(*Identifier)
	if !ok {
		g.fail("indirect call")
	}
	args := ce.Arguments
	bk, isB := builtins[ident.Value]
	if !isB {
		f, ok := g.funcs[ident.Value]
		if !ok || f.arity != len(args) {
			g.fail("call to unknown function %s", ident.Value)
		}
		regs := []int{rDI, rSI, rDX, rCX, r8, r9}
		for _, a := range args {
			g.evalAs(a, TInt)
			g.push(rAX)
		}
		for i := len(args) - 1; i >= 0; i-- {
			g.pop(regs[i])
		}
		g.callLabel(f.label)
		return TInt
	}
	if bk != bPrint {
		if n := builtinArity[bk]; n != len(args) {
			g.fail("bad arity for %s", ident.Value)
		}
	}
	switch bk {
	case bReadInt:
		g.callRT("_kpj_read_int")
		return TInt
	case bReadFloat:
		g.callRT("_kpj_read_float")
		return TFloat
	case bArgInt:
		g.evalAs(args[0], TInt)
		g.movRR(rDI, rAX)
		g.callRT("_kpj_arg_int")
		return TInt
	case bArgc:
		g.callRT("_kpj_argc_fn")
		return TInt
	case bFloat:
		g.evalAs(args[0], TFloat)
		return TFloat
	case bInt:
		g.evalAs(args[0], TInt)
		return TInt
	case bMath1:
		fn := ident.Value
		if fn == "ln" {
			fn = "log"
		}
		g.evalAs(args[0], TFloat)
		g.callRT("kpj_" + fn)
		return TFloat
	case bPow:
		g.evalAs(args[0], TFloat)
		g.pushV(TFloat)
		g.evalAs(args[1], TFloat)
		g.popFloatLeft()
		g.callRT("kpj_pow")
		return TFloat
	case bPrimePi:
		g.evalAs(args[0], TInt)
		g.movRR(rDI, rAX)
		g.callRT("_kpj_prime_pi")
		return TInt
	case bIsFinite:
		g.evalAs(args[0], TFloat)
		g.callRT("kpj_isfinite")
		return TInt
	case bSqrt:
		g.evalAs(args[0], TFloat)
		g.sse(0xF2, 0x51, 0, 0)
		return TFloat
	case bPrint:
		for i, a := range args {
			if i > 0 {
				g.callRT("kpj_put_space")
			}
			t := g.genExpr(a)
			switch t {
			case TInt:
				g.movRR(rDI, rAX)
				g.callRT("_kpj_w_int")
			case TFloat:
				g.callRT("_kpj_w_float")
			case TStr:
				g.movRR(rDI, rAX)
				g.movRR(rSI, rDX)
				g.callRT("_kpj_w_str")
			}
		}
		g.callRT("kpj_put_nl")
		g.movRI(rAX, 0)
		return TInt
	case bReadToken:
		g.callRT("_kpj_read_token")
		return TStr
	case bReadLine:
		g.callRT("_kpj_read_line")
		return TStr
	case bLen:
		if g.genExpr(args[0]) != TStr {
			g.fail("len of non-string")
		}
		g.movRR(rAX, rDX)
		return TInt
	case bUpper, bLower:
		if g.genExpr(args[0]) != TStr {
			g.fail("case of non-string")
		}
		g.movRR(rDI, rAX)
		g.movRR(rSI, rDX)
		if bk == bUpper {
			g.callRT("_kpj_upper")
		} else {
			g.callRT("_kpj_lower")
		}
		return TStr
	case bCharAt:
		if g.genExpr(args[0]) != TStr {
			g.fail("char_at of non-string")
		}
		g.pushV(TStr)
		g.evalAs(args[1], TInt)
		g.movRR(rDX, rAX)
		g.pop(rDI)
		g.pop(rSI)
		g.callRT("_kpj_char_at")
		return TStr
	case bSubstr:
		if g.genExpr(args[0]) != TStr {
			g.fail("substr of non-string")
		}
		g.pushV(TStr)
		g.evalAs(args[1], TInt)
		g.push(rAX)
		g.evalAs(args[2], TInt)
		g.movRR(rCX, rAX)
		g.pop(rDX)
		g.pop(rDI)
		g.pop(rSI)
		g.callRT("_kpj_substr")
		return TStr
	case bStr:
		g.genStrExpr(args[0])
		return TStr
	case bExit:
		g.evalAs(args[0], TInt)
		g.movRR(rDI, rAX)
		g.callRT("kpj_exit")
		g.movRI(rAX, 0)
		return TInt
	case bAbs:
		t := g.genExpr(args[0])
		switch t {
		case TFloat:
			g.movqGX(rAX, 0)
			g.emit(0x48, 0x0F, 0xBA, 0xF0, 63) // btr rax, 63
			g.movqXG(0, rAX)
		case TInt:
			l := g.newLabel()
			g.movRR(rCX, rAX)
			g.neg(rCX)
			g.testRR(rAX, rAX)
			g.jcc(9, l) // jns
			g.movRR(rAX, rCX)
			g.bind(l)
		default:
			g.fail("abs of string")
		}
		return t
	case bMin, bMax:
		t := TInt
		if g.infer(args[0]) == TFloat || g.infer(args[1]) == TFloat {
			t = TFloat
		}
		g.evalAs(args[0], t)
		g.pushV(t)
		g.evalAs(args[1], t)
		done := g.newLabel()
		if t == TInt {
			g.movRR(rCX, rAX)
			g.pop(rAX)
			g.alu(0x39, rAX, rCX)
			if bk == bMin {
				g.jcc(ccL, done)
			} else {
				g.jcc(ccG, done)
			}
			g.movRR(rAX, rCX)
		} else {
			g.popFloatLeft() // xmm0 = a, xmm1 = b
			if bk == bMin {
				g.ucomisd(1, 0)
			} else {
				g.ucomisd(0, 1)
			}
			g.jcc(ccA, done)
			g.movapd(0, 1)
		}
		g.bind(done)
		return t
	}
	g.fail("unsupported builtin %s", ident.Value)
	return TInt
}

// ---------------------------------------------------------------- statements

func (g *native) genBlock(stmts []Statement) {
	g.pushScope()
	for _, s := range stmts {
		g.genStatement(s)
	}
	g.popScope()
}

func (g *native) genStatement(stmt Statement) {
	switch s := stmt.(type) {
	case *LetStatement:
		if info, ok := g.lookupCurrent(s.Name.Value); ok {
			g.evalAs(s.Value, info.typ)
			g.storeVar(info)
		} else {
			t := g.genExpr(s.Value)
			g.storeVar(g.declare(s.Name.Value, t))
		}
	case *AssignStatement:
		if info, ok := g.lookup(s.Name.Value); ok {
			g.evalAs(s.Value, info.typ)
			g.storeVar(info)
		} else {
			t := g.genExpr(s.Value)
			g.storeVar(g.declare(s.Name.Value, t))
		}
	case *ReturnStatement:
		if s.ReturnValue != nil {
			g.evalAs(s.ReturnValue, TInt)
		} else {
			g.movRI(rAX, 0)
		}
		g.jmp(g.retLabel)
	case *WhileStatement:
		ls := g.newLabel()
		le := g.newLabel()
		g.bind(ls)
		g.condJump(s.Condition, le, false)
		g.genBlock(s.Body.Statements)
		g.jmp(ls)
		g.bind(le)
	case *FunctionStatement:
		g.fail("nested function")
	case *ExpressionStatement:
		if s.Expression == nil {
			return
		}
		if ie, ok := s.Expression.(*IfExpression); ok {
			g.genIfStmt(ie)
			return
		}
		g.genExpr(s.Expression)
	case *BlockStatement:
		g.genBlock(s.Statements)
	default:
		g.fail("unsupported statement %T", stmt)
	}
}

func (g *native) genIfStmt(ie *IfExpression) {
	lelse := g.newLabel()
	g.condJump(ie.Condition, lelse, false)
	g.genBlock(ie.Consequence.Statements)
	if ie.Alternative != nil {
		lend := g.newLabel()
		g.jmp(lend)
		g.bind(lelse)
		g.genBlock(ie.Alternative.Statements)
		g.bind(lend)
	} else {
		g.bind(lelse)
	}
}

// ---------------------------------------------------------------- functions

func (g *native) beginFunc() int {
	g.scopes = nil
	g.frame = 0
	g.depth = 0
	g.nreg = 0
	g.retLabel = g.newLabel()
	g.pushScope()
	g.emit(0x55)             // push rbp
	g.emit(0x48, 0x89, 0xE5) // mov rbp, rsp
	g.emit(0x48, 0x81, 0xEC) // sub rsp, imm32
	pos := len(g.b)
	g.imm32(0)
	for k := range regPool {
		off := g.alloc(8)
		g.savePos[k] = len(g.b)
		g.store(off, regPool[k])
	}
	return pos
}

func (g *native) endFunc(patch int) {
	g.movRI(rAX, 0)
	g.bind(g.retLabel)
	for k := 0; k < g.nreg; k++ {
		g.load(regPool[k], 8*(k+1))
	}
	for k := g.nreg; k < len(regPool); k++ {
		copy(g.b[g.savePos[k]:], []byte{0x0F, 0x1F, 0x80, 0, 0, 0, 0}) // 7-byte nop
	}
	g.emit(0xC9, 0xC3) // leave; ret
	size := (g.frame + 15) &^ 15
	v := uint32(size)
	g.b[patch] = byte(v)
	g.b[patch+1] = byte(v >> 8)
	g.b[patch+2] = byte(v >> 16)
	g.b[patch+3] = byte(v >> 24)
}

func (g *native) compile(program *Program) {
	g.strLits = map[string]int{}
	g.funcs = map[string]*nativeFunc{}

	var mainStmts []Statement
	var order []*nativeFunc
	for _, st := range program.Statements {
		if fs, ok := st.(*FunctionStatement); ok {
			if _, dup := g.funcs[fs.Name.Value]; dup {
				g.fail("duplicate function %s", fs.Name.Value)
			}
			if len(fs.Parameters) > 6 {
				g.fail("too many parameters")
			}
			nf := &nativeFunc{label: g.newLabel(), arity: len(fs.Parameters), stmt: fs}
			g.funcs[fs.Name.Value] = nf
			order = append(order, nf)
			continue
		}
		mainStmts = append(mainStmts, st)
	}

	patch := g.beginFunc()
	for _, s := range mainStmts {
		g.genStatement(s)
	}
	g.endFunc(patch)

	regs := []int{rDI, rSI, rDX, rCX, r8, r9}
	for _, nf := range order {
		g.bind(nf.label)
		patch := g.beginFunc()
		for i, p := range nf.stmt.Parameters {
			v := g.declare(p.Value, TInt)
			if v.reg >= 0 {
				g.movRR(v.reg, regs[i])
			} else {
				g.store(v.off, regs[i])
			}
		}
		for _, s := range nf.stmt.Body.Statements {
			g.genStatement(s)
		}
		g.endFunc(patch)
	}
}

// buildNative returns the code bytes (code followed by data) to patch in.
func buildNative(program *Program) (out []byte, err error) {
	g := &native{}
	defer func() {
		if r := recover(); r != nil {
			if ne, ok := r.(nativeError); ok {
				err = fmt.Errorf("native backend: %s", ne.msg)
				return
			}
			panic(r)
		}
	}()
	g.compile(program)

	for _, f := range g.fixups {
		tgt := g.labels[f.label]
		if tgt < 0 {
			return nil, fmt.Errorf("native backend: unbound label")
		}
		rel := int32(tgt - (f.pos + 4))
		g.b[f.pos] = byte(rel)
		g.b[f.pos+1] = byte(rel >> 8)
		g.b[f.pos+2] = byte(rel >> 16)
		g.b[f.pos+3] = byte(rel >> 24)
	}
	// align data
	for len(g.b)%16 != 0 {
		g.b = append(g.b, 0xCC)
	}
	dataBase := uint64(rtCodeVaddr) + uint64(len(g.b))
	for _, d := range g.dataFix {
		v := dataBase + uint64(d.off)
		for i := 0; i < 8; i++ {
			g.b[d.pos+i] = byte(v >> (8 * i))
		}
	}
	out = append(g.b, g.data...)
	if len(out) > rtCodeSize {
		return nil, fmt.Errorf("native backend: program too large")
	}
	return out, nil
}

// CompileNative writes an executable built from the embedded runtime plus
// generated machine code. No external tools are used.
func CompileNative(program *Program, outputPath string) error {
	code, err := buildNative(program)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(outputPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	if _, err = f.Write(runtimeELF[:rtCodeFileOff]); err == nil {
		if _, err = f.Write(code); err == nil {
			_, err = f.Write(runtimeELF[rtCodeFileOff+len(code):])
		}
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Chmod(outputPath, 0755)
}
