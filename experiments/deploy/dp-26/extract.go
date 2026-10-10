//go:build ignore

// Extract unchanged declarations for a source-slice test, not a full host build.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 3 {
		panic("usage: extract FILE NAME[,NAME...]")
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	fs := token.NewFileSet()
	file, err := parser.ParseFile(fs, os.Args[1], data, parser.ParseComments)
	if err != nil {
		panic(err)
	}
	wanted := map[string]bool{}
	for _, n := range strings.Split(os.Args[2], ",") {
		wanted[n] = true
	}
	for _, d := range file.Decls {
		var name string
		switch x := d.(type) {
		case *ast.FuncDecl:
			name = x.Name.Name
		case *ast.GenDecl:
			if len(x.Specs) == 1 {
				if t, ok := x.Specs[0].(*ast.TypeSpec); ok {
					name = t.Name.Name
				}
			}
		}
		if wanted[name] {
			fmt.Printf("%s\n\n", data[fs.Position(d.Pos()).Offset:fs.Position(d.End()).Offset])
			delete(wanted, name)
		}
	}
	if len(wanted) > 0 {
		panic(fmt.Sprint("missing declarations: ", wanted))
	}
}
