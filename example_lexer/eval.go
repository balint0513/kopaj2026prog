package main

import (
	"fmt"
	"os"
	"strconv"
)

type ObjectType string

const (
	OBJ_INTEGER      ObjectType = "INTEGER"
	OBJ_BOOLEAN      ObjectType = "BOOLEAN"
	OBJ_NULL         ObjectType = "NULL"
	OBJ_RETURN_VALUE ObjectType = "RETURN_VALUE"
	OBJ_FUNCTION     ObjectType = "FUNCTION"
	OBJ_ERROR        ObjectType = "ERROR"
)

type Object interface {
	Type() ObjectType
	Inspect() string
}

type Integer struct {
	Value int64
}

func (i *Integer) Type() ObjectType { return OBJ_INTEGER }
func (i *Integer) Inspect() string  { return fmt.Sprintf("%d", i.Value) }

type Boolean struct {
	Value bool
}

func (b *Boolean) Type() ObjectType { return OBJ_BOOLEAN }
func (b *Boolean) Inspect() string  { return fmt.Sprintf("%t", b.Value) }

type Null struct{}

func (n *Null) Type() ObjectType { return OBJ_NULL }
func (n *Null) Inspect() string  { return "null" }

type ReturnValue struct {
	Value Object
}

func (rv *ReturnValue) Type() ObjectType { return OBJ_RETURN_VALUE }
func (rv *ReturnValue) Inspect() string  { return rv.Value.Inspect() }

type Function struct {
	Parameters []*Identifier
	Body       *BlockStatement
	Env        *Environment
}

func (f *Function) Type() ObjectType { return OBJ_FUNCTION }
func (f *Function) Inspect() string  { return "fn(...)" }

type Error struct {
	Message string
}

func (e *Error) Type() ObjectType { return OBJ_ERROR }
func (e *Error) Inspect() string  { return "ERROR: " + e.Message }

var (
	NULL  = &Null{}
	TRUE  = &Boolean{Value: true}
	FALSE = &Boolean{Value: false}
)

type Environment struct {
	store   map[string]Object
	outer   *Environment
	args    []string
	argIdx  int
	printed bool
}

func NewEnvironment() *Environment {
	return &Environment{
		store:  make(map[string]Object),
		argIdx: 0,
	}
}

func NewEnclosedEnvironment(outer *Environment) *Environment {
	env := NewEnvironment()
	env.outer = outer
	return env
}

func (e *Environment) Root() *Environment {
	cur := e
	for cur.outer != nil {
		cur = cur.outer
	}
	return cur
}

func (e *Environment) Get(name string) (Object, bool) {
	obj, ok := e.store[name]
	if !ok && e.outer != nil {
		return e.outer.Get(name)
	}
	return obj, ok
}

func (e *Environment) Set(name string, val Object) Object {
	e.store[name] = val
	return val
}

func (e *Environment) Assign(name string, val Object) Object {
	if _, ok := e.store[name]; ok {
		e.store[name] = val
		return val
	}
	if e.outer != nil {
		return e.outer.Assign(name, val)
	}
	e.store[name] = val
	return val
}

func Eval(node Node, env *Environment) Object {
	switch node := node.(type) {
	case *Program:
		return evalProgram(node, env)
	case *BlockStatement:
		return evalBlockStatement(node, env)
	case *ExpressionStatement:
		return Eval(node.Expression, env)
	case *LetStatement:
		val := Eval(node.Value, env)
		if isError(val) {
			return val
		}
		env.Set(node.Name.Value, val)
		return val
	case *AssignStatement:
		val := Eval(node.Value, env)
		if isError(val) {
			return val
		}
		env.Assign(node.Name.Value, val)
		return val
	case *ReturnStatement:
		if node.ReturnValue == nil {
			return &ReturnValue{Value: &Integer{Value: 0}}
		}
		val := Eval(node.ReturnValue, env)
		if isError(val) {
			return val
		}
		return &ReturnValue{Value: val}
	case *WhileStatement:
		return evalWhileStatement(node, env)
	case *FunctionStatement:
		fn := &Function{
			Parameters: node.Parameters,
			Body:       node.Body,
			Env:        env,
		}
		env.Set(node.Name.Value, fn)
		return NULL
	case *IntegerLiteral:
		return &Integer{Value: node.Value}
	case *BooleanLiteral:
		return nativeBoolToBooleanObject(node.Value)
	case *Identifier:
		return evalIdentifier(node, env)
	case *PrefixExpression:
		right := Eval(node.Right, env)
		if isError(right) {
			return right
		}
		return evalPrefixExpression(node.Operator, right)
	case *InfixExpression:
		left := Eval(node.Left, env)
		if isError(left) {
			return left
		}
		right := Eval(node.Right, env)
		if isError(right) {
			return right
		}
		return evalInfixExpression(node.Operator, left, right)
	case *IfExpression:
		return evalIfExpression(node, env)
	case *CallExpression:
		return evalCallExpression(node, env)
	}

	return newError("unhandled AST node type: %T", node)
}

func evalProgram(program *Program, env *Environment) Object {
	var result Object = NULL
	for _, stmt := range program.Statements {
		result = Eval(stmt, env)
		switch res := result.(type) {
		case *ReturnValue:
			return res.Value
		case *Error:
			return res
		}
	}
	return result
}

func evalBlockStatement(block *BlockStatement, env *Environment) Object {
	var result Object = NULL
	for _, stmt := range block.Statements {
		result = Eval(stmt, env)
		if result != nil {
			rt := result.Type()
			if rt == OBJ_RETURN_VALUE || rt == OBJ_ERROR {
				return result
			}
		}
	}
	return result
}

func evalWhileStatement(ws *WhileStatement, env *Environment) Object {
	var result Object = NULL
	for {
		cond := Eval(ws.Condition, env)
		if isError(cond) {
			return cond
		}
		if !isTruthy(cond) {
			break
		}
		result = Eval(ws.Body, NewEnclosedEnvironment(env))
		if result != nil && (result.Type() == OBJ_RETURN_VALUE || result.Type() == OBJ_ERROR) {
			return result
		}
	}
	return result
}

func evalIdentifier(node *Identifier, env *Environment) Object {
	if val, ok := env.Get(node.Value); ok {
		return val
	}
	return newError("identifier not found: %s", node.Value)
}

func evalPrefixExpression(operator string, right Object) Object {
	switch operator {
	case "-":
		if right.Type() != OBJ_INTEGER {
			return newError("unknown operator: -%s", right.Type())
		}
		val := right.(*Integer).Value
		return &Integer{Value: -val}
	case "+":
		if right.Type() != OBJ_INTEGER {
			return newError("unknown operator: +%s", right.Type())
		}
		return right
	case "!":
		return nativeBoolToBooleanObject(!isTruthy(right))
	default:
		return newError("unknown operator: %s%s", operator, right.Type())
	}
}

func evalInfixExpression(operator string, left, right Object) Object {
	switch {
	case operator == "&&":
		return nativeBoolToBooleanObject(isTruthy(left) && isTruthy(right))
	case operator == "||":
		return nativeBoolToBooleanObject(isTruthy(left) || isTruthy(right))
	case left.Type() == OBJ_INTEGER && right.Type() == OBJ_INTEGER:
		return evalIntegerInfixExpression(operator, left, right)
	case operator == "==":
		return nativeBoolToBooleanObject(left == right)
	case operator == "!=":
		return nativeBoolToBooleanObject(left != right)
	case left.Type() != right.Type():
		return newError("type mismatch: %s %s %s", left.Type(), operator, right.Type())
	default:
		return newError("unknown operator: %s %s %s", left.Type(), operator, right.Type())
	}
}

func evalIntegerInfixExpression(operator string, left, right Object) Object {
	leftVal := left.(*Integer).Value
	rightVal := right.(*Integer).Value

	switch operator {
	case "+":
		return &Integer{Value: leftVal + rightVal}
	case "-":
		return &Integer{Value: leftVal - rightVal}
	case "*":
		return &Integer{Value: leftVal * rightVal}
	case "/":
		if rightVal == 0 {
			return newError("division by zero")
		}
		return &Integer{Value: leftVal / rightVal}
	case "%":
		if rightVal == 0 {
			return newError("modulo by zero")
		}
		return &Integer{Value: leftVal % rightVal}
	case "<":
		return nativeBoolToBooleanObject(leftVal < rightVal)
	case ">":
		return nativeBoolToBooleanObject(leftVal > rightVal)
	case "<=":
		return nativeBoolToBooleanObject(leftVal <= rightVal)
	case ">=":
		return nativeBoolToBooleanObject(leftVal >= rightVal)
	case "==":
		return nativeBoolToBooleanObject(leftVal == rightVal)
	case "!=":
		return nativeBoolToBooleanObject(leftVal != rightVal)
	default:
		return newError("unknown operator: %s %s %s", left.Type(), operator, right.Type())
	}
}

func evalIfExpression(ie *IfExpression, env *Environment) Object {
	condition := Eval(ie.Condition, env)
	if isError(condition) {
		return condition
	}

	if isTruthy(condition) {
		return Eval(ie.Consequence, NewEnclosedEnvironment(env))
	} else if ie.Alternative != nil {
		return Eval(ie.Alternative, NewEnclosedEnvironment(env))
	}
	return NULL
}

func evalCallExpression(ce *CallExpression, env *Environment) Object {
	ident, ok := ce.Function.(*Identifier)
	if !ok {
		return newError("not a function: %s", ce.Function.String())
	}

	args := make([]Object, len(ce.Arguments))
	for i, a := range ce.Arguments {
		val := Eval(a, env)
		if isError(val) {
			return val
		}
		args[i] = val
	}

	root := env.Root()

	switch ident.Value {
	case "read_int", "scan_int", "input_int", "read":
		var val int64
		if stat, err := os.Stdin.Stat(); err == nil && (stat.Mode()&os.ModeCharDevice) == 0 {
			if _, err := fmt.Fscan(os.Stdin, &val); err == nil {
				root.argIdx++
				return &Integer{Value: val}
			}
		}
		if root.argIdx < len(root.args) {
			parsed, err := strconv.ParseInt(root.args[root.argIdx], 10, 64)
			root.argIdx++
			if err == nil {
				return &Integer{Value: parsed}
			}
		}
		if _, err := fmt.Fscan(os.Stdin, &val); err == nil {
			return &Integer{Value: val}
		}
		return &Integer{Value: 0}

	case "arg_int", "arg":
		if len(args) >= 1 && args[0].Type() == OBJ_INTEGER {
			idx := int(args[0].(*Integer).Value)
			if idx >= 1 && idx <= len(root.args) {
				parsed, _ := strconv.ParseInt(root.args[idx-1], 10, 64)
				return &Integer{Value: parsed}
			}
		}
		return &Integer{Value: 0}

	case "argc", "arg_count":
		return &Integer{Value: int64(len(root.args))}

	case "print", "println", "print_int":
		root.printed = true
		var last Object = &Integer{Value: 0}
		for _, a := range args {
			fmt.Println(a.Inspect())
			last = a
		}
		return last

	case "exit":
		code := 0
		if len(args) >= 1 && args[0].Type() == OBJ_INTEGER {
			code = int(args[0].(*Integer).Value)
		}
		os.Exit(code)
		return NULL
	}

	fnObj, ok := env.Get(ident.Value)
	if !ok {
		return newError("function not found: %s", ident.Value)
	}
	fn, ok := fnObj.(*Function)
	if !ok {
		return newError("%s is not a function", ident.Value)
	}
	fnEnv := NewEnclosedEnvironment(fn.Env)
	for i, param := range fn.Parameters {
		if i < len(args) {
			fnEnv.Set(param.Value, args[i])
		} else {
			fnEnv.Set(param.Value, &Integer{Value: 0})
		}
	}
	evaluated := Eval(fn.Body, fnEnv)
	if rv, ok := evaluated.(*ReturnValue); ok {
		return rv.Value
	}
	return evaluated
}

func isTruthy(obj Object) bool {
	switch obj {
	case NULL, FALSE:
		return false
	default:
		if i, ok := obj.(*Integer); ok && i.Value == 0 {
			return false
		}
		return true
	}
}

func nativeBoolToBooleanObject(input bool) *Boolean {
	if input {
		return TRUE
	}
	return FALSE
}

func newError(format string, a ...any) *Error {
	return &Error{Message: fmt.Sprintf(format, a...)}
}

func isError(obj Object) bool {
	return obj != nil && obj.Type() == OBJ_ERROR
}
