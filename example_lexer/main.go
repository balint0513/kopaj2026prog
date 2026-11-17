package main

import (
	"fmt"
	"io"
	"os"
)

func parseSource(source string) *Program {
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
	return program
}

func readSourceFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "file read error (%s): %v\n", path, err)
		os.Exit(1)
	}
	return string(data)
}

func handleBuild(args []string) {
	var outputPath string
	var sourcePath string

	for i := 0; i < len(args); i++ {
		if args[i] == "-o" {
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "missing output path after -o")
				os.Exit(1)
			}
			outputPath = args[i+1]
			i++
		} else if sourcePath == "" {
			sourcePath = args[i]
		}
	}

	if sourcePath == "" {
		fmt.Fprintln(os.Stderr, "usage: kopajc build -o <output> <source>")
		os.Exit(1)
	}
	if outputPath == "" {
		outputPath = "a.out"
	}

	source := readSourceFile(sourcePath)
	program := parseSource(source)

	if err := CompileToBinary(program, outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "compile error: %v\n", err)
		os.Exit(1)
	}
}

func main() {
	// Contest build command: ./kopajc build -o <output> <source>
	if len(os.Args) > 1 && os.Args[1] == "build" {
		handleBuild(os.Args[2:])
		return
	}

	// Also allow ./kopajc -o <output> <source>
	if len(os.Args) > 1 && os.Args[1] == "-o" {
		handleBuild(os.Args[1:])
		return
	}

	// Emit generated C code for inspection: ./kopajc emit-c <source>
	if len(os.Args) > 2 && os.Args[1] == "emit-c" {
		source := readSourceFile(os.Args[2])
		program := parseSource(source)
		fmt.Print(GenerateC(program))
		return
	}

	// Interpreter / direct evaluation mode: ./kopajc [run] <source> [args...]
	var source string
	var progArgs []string

	args := os.Args[1:]
	if len(args) > 0 && args[0] == "run" {
		args = args[1:]
	}

	if len(args) >= 1 {
		source = readSourceFile(args[0])
		progArgs = args[1:]
	} else if stat, _ := os.Stdin.Stat(); (stat.Mode() & os.ModeCharDevice) == 0 {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "stdin read error: %v\n", err)
			os.Exit(1)
		}
		source = string(data)
	} else {
		fmt.Fprintln(os.Stderr, "usage: kopajc build -o <output> <source>")
		os.Exit(1)
	}

	program := parseSource(source)
	env := NewEnvironment()
	env.args = progArgs
	evaluated := Eval(program, env)
	if evaluated != nil {
		if evaluated.Type() == OBJ_ERROR {
			fmt.Fprintln(os.Stderr, evaluated.Inspect())
			os.Exit(1)
		}
		if !env.printed && evaluated.Type() != OBJ_NULL {
			fmt.Println(evaluated.Inspect())
		}
	}
}
