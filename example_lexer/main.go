package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	var source string

	if len(os.Args) > 1 {
		data, err := os.ReadFile(os.Args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "file read error: %v\n", err)
			os.Exit(1)
		}
		source = string(data)
	} else if stat, _ := os.Stdin.Stat(); (stat.Mode() & os.ModeCharDevice) == 0 {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "stdin read error: %v\n", err)
			os.Exit(1)
		}
		source = string(data)
	} else {
		source = `
		let x = 10;
		if (x != 20) {
			return x + 5 * 2;
		}
		`
	}

	l := NewLexer(source)
	p := NewParser(l)
	program := p.ParseProgram()

	if len(p.Errors()) > 0 {
		fmt.Fprintln(os.Stderr, "Parser errors:")
		for _, msg := range p.Errors() {
			fmt.Fprintf(os.Stderr, "  - %s\n", msg)
		}
		os.Exit(1)
	}

	env := NewEnvironment()
	evaluated := Eval(program, env)
	if evaluated != nil {
		if evaluated.Type() == OBJ_ERROR {
			fmt.Fprintln(os.Stderr, evaluated.Inspect())
			os.Exit(1)
		}
		fmt.Println(evaluated.Inspect())
	}
}
