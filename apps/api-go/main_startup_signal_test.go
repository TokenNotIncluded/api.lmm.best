package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// The live migration qualification sends SIGTERM immediately after readiness.
// Keep its ordering requirement deterministic even if a slow runner misses the
// former interval between opening the listener and registering signal.Notify.
func TestShutdownSignalsInstalledBeforeHTTPAcceptsRequests(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var run *ast.FuncDecl
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok && function.Name.Name == "runServer" {
			run = function
		}
	}
	if run == nil {
		t.Fatal("runServer is missing")
	}
	notify, stop, listen := -1, -1, -1
	selector := func(call *ast.CallExpr, receiver, method string) bool {
		selected, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selected.Sel.Name != method {
			return false
		}
		name, ok := selected.X.(*ast.Ident)
		return ok && name.Name == receiver
	}
	for index, statement := range run.Body.List {
		switch node := statement.(type) {
		case *ast.ExprStmt:
			if call, ok := node.X.(*ast.CallExpr); ok && selector(call, "signal", "Notify") {
				notify = index
			}
		case *ast.DeferStmt:
			if selector(node.Call, "signal", "Stop") {
				stop = index
			}
		case *ast.GoStmt:
			ast.Inspect(node, func(candidate ast.Node) bool {
				if call, ok := candidate.(*ast.CallExpr); ok && selector(call, "srv", "ListenAndServe") {
					listen = index
				}
				return true
			})
		}
	}
	if notify < 0 || listen < 0 || notify >= listen {
		t.Fatalf("shutdown signals must be installed before accepting HTTP: notify=%d listen=%d", notify, listen)
	}
	if stop <= notify || stop >= listen {
		t.Fatalf("signal cleanup must be deferred before accepting HTTP: notify=%d stop=%d listen=%d", notify, stop, listen)
	}
}
