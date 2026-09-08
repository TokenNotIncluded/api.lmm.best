// Package assistantcontracts derives assistant request documentation from the
// same controller declarations used by the HTTP API. It is a build-time tool;
// production binaries never read source files to discover permissions or APIs.
package assistantcontracts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

const module = "github.com/LIghtJUNction/api.lmm.best/"

type source struct {
	pkg     string
	imports map[string]string
}

type definition struct {
	expr   ast.Expr
	source *source
}
type function struct {
	decl   *ast.FuncDecl
	source *source
}
type generator struct {
	root      string
	types     map[string]definition
	functions map[string]function
	loaded    map[string]bool
}

// Generate returns deterministic JSON for all statically registered controller
// handlers. The runtime route registry independently restricts discovery and
// execution to the authenticated administrator routes.
func Generate(root string) ([]byte, error) {
	g := &generator{root: root, types: map[string]definition{}, functions: map[string]function{}, loaded: map[string]bool{}}
	if err := g.load("controller"); err != nil {
		return nil, err
	}
	routes, err := parser.ParseDir(token.NewFileSet(), filepath.Join(root, "router"), productionFile, 0)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, pkg := range routes {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(node ast.Node) bool {
				if selector, ok := node.(*ast.SelectorExpr); ok {
					if receiver, ok := selector.X.(*ast.Ident); ok && receiver.Name == "controller" {
						names[selector.Sel.Name] = true
					}
				}
				return true
			})
		}
	}
	contracts := map[string]any{}
	for name := range names {
		fn, ok := g.functions["controller."+name]
		if !ok {
			continue
		}
		c := &contract{body: map[string]any{}, query: map[string]any{}, path: map[string]any{}, seen: map[string]bool{}}
		g.inspect(fn, c, nil)
		g.refine(name, c)
		status := "derived"
		if c.unknown {
			status = "partial"
		}
		entry := map[string]any{"contract_status": status, "query_schema": object(c.query), "path_schema": object(c.path)}
		if c.hasBody {
			entry["body_schema"] = c.body
		} else {
			entry["body_schema"] = nil
		}
		if len(c.notes) > 0 {
			entry["request_notes"] = c.notes
		}
		if len(c.path) > 0 {
			keys := make([]string, 0, len(c.path))
			for key := range c.path {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			entry["path_schema"].(map[string]any)["required"] = keys
		}
		if c.unknown {
			entry["limitations"] = "Some request fields could not be derived. Do not guess payloads; use the management page or an operation with a complete contract."
		}
		contracts[name] = entry
	}
	// One operation per line keeps generated diffs reviewable without a very
	// large pretty-printed tree for every nested request model.
	keys := make([]string, 0, len(contracts))
	for key := range contracts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var output bytes.Buffer
	output.WriteString("{\n")
	for i, key := range keys {
		encodedKey, _ := json.Marshal(key)
		encodedValue, err := json.Marshal(contracts[key])
		if err != nil {
			return nil, err
		}
		output.WriteString("  ")
		output.Write(encodedKey)
		output.WriteString(": ")
		output.Write(encodedValue)
		if i < len(keys)-1 {
			output.WriteByte(',')
		}
		output.WriteByte('\n')
	}
	output.WriteString("}\n")
	return output.Bytes(), nil
}

func productionFile(info os.FileInfo) bool {
	return strings.HasSuffix(info.Name(), ".go") && !strings.HasSuffix(info.Name(), "_test.go")
}
func object(properties map[string]any) map[string]any {
	return map[string]any{"type": "object", "properties": properties}
}

func (g *generator) load(pkg string) error {
	if g.loaded[pkg] {
		return nil
	}
	g.loaded[pkg] = true
	files, err := parser.ParseDir(token.NewFileSet(), filepath.Join(g.root, filepath.FromSlash(pkg)), productionFile, parser.ParseComments)
	if err != nil {
		return err
	}
	for _, parsed := range files {
		for _, file := range parsed.Files {
			src := &source{pkg: pkg, imports: map[string]string{}}
			for _, imp := range file.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				name := filepath.Base(path)
				if imp.Name != nil {
					name = imp.Name.Name
				}
				src.imports[name] = path
			}
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						if typ, ok := spec.(*ast.TypeSpec); ok {
							g.types[pkg+"."+typ.Name.Name] = definition{typ.Type, src}
						}
					}
				case *ast.FuncDecl:
					if d.Recv == nil {
						g.functions[pkg+"."+d.Name.Name] = function{d, src}
					}
				}
			}
		}
	}
	return nil
}

type contract struct {
	body, query, path map[string]any
	hasBody, unknown  bool
	seen              map[string]bool
	notes             []string
}

func literal(expr ast.Expr) string {
	if s, ok := expr.(*ast.BasicLit); ok && s.Kind == token.STRING {
		value, _ := strconv.Unquote(s.Value)
		return value
	}
	return ""
}
func ident(expr ast.Expr) string {
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}
func render(expr ast.Expr) string {
	var b bytes.Buffer
	_ = format.Node(&b, token.NewFileSet(), expr)
	return b.String()
}

func (g *generator) inspect(fn function, c *contract, constants map[string]string) {
	encodedConstants, _ := json.Marshal(constants)
	key := fn.source.pkg + "." + fn.decl.Name.Name + string(encodedConstants)
	if c.seen[key] {
		return
	}
	c.seen[key] = true
	vars := map[string]definition{}
	rangeKeys := map[string][]string{}
	contextNames := map[string]bool{}
	for _, field := range fn.decl.Type.Params.List {
		if strings.HasSuffix(render(field.Type), "gin.Context") {
			for _, name := range field.Names {
				contextNames[name.Name] = true
			}
		}
	}
	ast.Inspect(fn.decl.Body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.ValueSpec:
			for i, name := range n.Names {
				if n.Type != nil {
					vars[name.Name] = definition{n.Type, fn.source}
				} else if i < len(n.Values) {
					if typ := expressionType(n.Values[i]); typ != nil {
						vars[name.Name] = definition{typ, fn.source}
					}
				}
			}
		case *ast.AssignStmt:
			for i, lhs := range n.Lhs {
				if i < len(n.Rhs) {
					if typ := expressionType(n.Rhs[i]); typ != nil {
						vars[ident(lhs)] = definition{typ, fn.source}
					}
				}
			}
		case *ast.TypeSpec:
			vars[n.Name.Name] = definition{n.Type, fn.source}
		case *ast.RangeStmt:
			if composite, ok := n.X.(*ast.CompositeLit); ok {
				if _, ok := composite.Type.(*ast.MapType); ok {
					for _, elt := range composite.Elts {
						if pair, ok := elt.(*ast.KeyValueExpr); ok {
							if key := literal(pair.Key); key != "" {
								rangeKeys[ident(n.Key)] = append(rangeKeys[ident(n.Key)], key)
							}
						}
					}
				}
			}
		}
		return true
	})
	// Track bytes read from the request so a second typed decode (UpdateChannel)
	// contributes its real schema without treating upstream response JSON as input.
	rawVars := map[string]bool{}
	decoderVars := map[string]bool{}
	bodyVars := map[string]bool{}
	ast.Inspect(fn.decl.Body, func(node ast.Node) bool {
		if assign, ok := node.(*ast.AssignStmt); ok {
			for _, rhs := range assign.Rhs {
				if call, ok := rhs.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "NewDecoder" && len(call.Args) > 0 && strings.Contains(render(call.Args[0]), ".Request.Body") && len(assign.Lhs) > 0 {
						decoderVars[ident(assign.Lhs[0])] = true
					}
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "GetRawData" && contextNames[ident(sel.X)] && len(assign.Lhs) > 0 {
						rawVars[ident(assign.Lhs[0])] = true
					}
				}
			}
		}
		return true
	})
	ast.Inspect(fn.decl.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		var body ast.Expr
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			name := sel.Sel.Name
			if contextNames[ident(sel.X)] {
				switch name {
				case "Query", "DefaultQuery", "GetQuery", "Param", "QueryArray", "GetQueryArray":
					if len(call.Args) == 0 {
						return true
					}
					key := literal(call.Args[0])
					if key == "" {
						key = constants[ident(call.Args[0])]
					}
					if key == "" {
						if keys := rangeKeys[ident(call.Args[0])]; len(keys) > 0 {
							for _, key := range keys {
								c.query[key] = map[string]any{"type": "string"}
							}
							return true
						}
						c.unknown = true
						return true
					}
					field := map[string]any{"type": "string"}
					if strings.HasSuffix(name, "Array") {
						field = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
					}
					if name == "DefaultQuery" && len(call.Args) > 1 {
						field["default"] = literal(call.Args[1])
					}
					if name == "Param" {
						c.path[key] = field
					} else {
						c.query[key] = field
					}
				case "ShouldBindJSON", "BindJSON", "ShouldBind", "Bind", "ShouldBindBodyWith":
					if len(call.Args) > 0 {
						body = call.Args[0]
					}
				case "PostForm", "FormFile", "MultipartForm", "QueryMap", "GetQueryMap":
					c.unknown = true
				}
			}
			if (name == "DecodeJson" || name == "UnmarshalBodyReusable") && len(call.Args) > 1 && (contextNames[ident(call.Args[0])] || strings.Contains(render(call.Args[0]), ".Request.Body")) {
				body = call.Args[1]
			}
			if name == "Unmarshal" && len(call.Args) > 1 && rawVars[ident(call.Args[0])] {
				body = call.Args[1]
			}
			if name == "Decode" && len(call.Args) > 0 && (strings.Contains(render(sel.X), ".Request.Body") || decoderVars[ident(sel.X)]) {
				body = call.Args[0]
			}
			// Follow local package helpers that receive the request context, which
			// includes the shared pagination function and controller input decoders.
			if importPath := fn.source.imports[ident(sel.X)]; strings.HasPrefix(importPath, module) && hasContextArg(call, contextNames) {
				pkg := strings.TrimPrefix(importPath, module)
				_ = g.load(pkg)
				if child, found := g.functions[pkg+"."+name]; found {
					g.inspect(child, c, callConstants(child, call, constants))
				}
			}
		} else if name := ident(call.Fun); name != "" && hasContextArg(call, contextNames) {
			if name == "decodeStrictJSONRequest" && len(call.Args) > 1 {
				body = call.Args[1]
			} else if child, found := g.functions[fn.source.pkg+"."+name]; found {
				g.inspect(child, c, callConstants(child, call, constants))
			}
		}
		if body != nil {
			if unary, ok := body.(*ast.UnaryExpr); ok {
				body = unary.X
			}
			bodyVars[ident(body)] = true
			def, found := vars[ident(body)]
			if !found {
				if typ := expressionType(body); typ != nil {
					def = definition{typ, fn.source}
					found = true
				}
			}
			if !found {
				c.unknown = true
				return true
			}
			schema, complete := g.schema(def, vars, map[string]bool{})
			if !complete {
				c.unknown = true
			}
			// A typed decode is more informative than a parallel map decode.
			if !c.hasBody || lenProperties(schema) > lenProperties(c.body) {
				c.body = schema
			}
			c.hasBody = true
		}
		return true
	})
	if len(rawVars) > 0 && !c.hasBody {
		c.unknown = true
	}
	// Source switch cases are useful examples for action/mode selectors. They
	// are not enums: a default branch can accept values beyond explicit cases.
	properties, _ := c.body["properties"].(map[string]any)
	ast.Inspect(fn.decl.Body, func(node ast.Node) bool {
		branch, ok := node.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		selector, ok := branch.Tag.(*ast.SelectorExpr)
		if !ok || !bodyVars[ident(selector.X)] {
			return true
		}
		for name, value := range properties {
			if !strings.EqualFold(strings.ReplaceAll(name, "_", ""), selector.Sel.Name) {
				continue
			}
			field, ok := value.(map[string]any)
			if !ok || field["type"] != "string" {
				continue
			}
			var examples []string
			for _, clause := range branch.Body.List {
				for _, expr := range clause.(*ast.CaseClause).List {
					if value := literal(expr); value != "" {
						examples = append(examples, value)
					}
				}
			}
			if len(examples) > 0 {
				field["examples"] = examples
			}
		}
		return true
	})
}

func hasContextArg(call *ast.CallExpr, names map[string]bool) bool {
	for _, arg := range call.Args {
		if names[ident(arg)] {
			return true
		}
	}
	return false
}
func callConstants(fn function, call *ast.CallExpr, parent map[string]string) map[string]string {
	values := map[string]string{}
	index := 0
	for _, field := range fn.decl.Type.Params.List {
		for _, name := range field.Names {
			if index < len(call.Args) {
				value := literal(call.Args[index])
				if value == "" {
					value = parent[ident(call.Args[index])]
				}
				if value != "" {
					values[name.Name] = value
				}
			}
			index++
		}
	}
	return values
}
func lenProperties(schema map[string]any) int {
	props, _ := schema["properties"].(map[string]any)
	return len(props)
}
func expressionType(expr ast.Expr) ast.Expr {
	switch e := expr.(type) {
	case *ast.CompositeLit:
		return e.Type
	case *ast.UnaryExpr:
		return expressionType(e.X)
	case *ast.CallExpr:
		if ident(e.Fun) == "new" && len(e.Args) == 1 {
			return e.Args[0]
		}
	}
	return nil
}

func (g *generator) schema(def definition, locals map[string]definition, seen map[string]bool) (map[string]any, bool) {
	primitive := func(typ string) (map[string]any, bool) { return map[string]any{"type": typ}, true }
	switch typ := def.expr.(type) {
	case *ast.StarExpr:
		schema, ok := g.schema(definition{typ.X, def.source}, locals, seen)
		return map[string]any{"anyOf": []any{schema, map[string]any{"type": "null"}}}, ok
	case *ast.Ident:
		switch typ.Name {
		case "string":
			return primitive("string")
		case "bool":
			return primitive("boolean")
		case "float32", "float64":
			return primitive("number")
		case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "byte", "rune":
			return primitive("integer")
		case "any":
			return map[string]any{}, true
		}
		key := def.source.pkg + "." + typ.Name
		if seen[key] {
			return map[string]any{"description": "Recursive value: " + key}, false
		}
		seen[key] = true
		defer delete(seen, key)
		if local, ok := locals[typ.Name]; ok {
			return g.schema(local, locals, seen)
		}
		if next, ok := g.types[key]; ok {
			return g.schema(next, nil, seen)
		}
	case *ast.SelectorExpr:
		pkg := def.source.imports[ident(typ.X)]
		if pkg == "gorm.io/gorm" && typ.Sel.Name == "DeletedAt" {
			return map[string]any{"anyOf": []any{map[string]any{"type": "string", "format": "date-time"}, map[string]any{"type": "null"}}, "readOnly": true}, true
		}
		if (pkg == "encoding/json" || pkg == module+"common") && typ.Sel.Name == "RawMessage" {
			return map[string]any{"description": "JSON value"}, true
		}
		if pkg == "time" && typ.Sel.Name == "Time" {
			return map[string]any{"type": "string", "format": "date-time"}, true
		}
		if strings.HasPrefix(pkg, module) {
			localPkg := strings.TrimPrefix(pkg, module)
			_ = g.load(localPkg)
			return g.schema(definition{ast.NewIdent(typ.Sel.Name), &source{pkg: localPkg}}, nil, seen)
		}
	case *ast.ArrayType:
		if id, ok := typ.Elt.(*ast.Ident); ok && id.Name == "byte" {
			return map[string]any{"type": "string", "contentEncoding": "base64"}, true
		}
		item, ok := g.schema(definition{typ.Elt, def.source}, locals, seen)
		return map[string]any{"type": "array", "items": item}, ok
	case *ast.MapType:
		value, ok := g.schema(definition{typ.Value, def.source}, locals, seen)
		return map[string]any{"type": "object", "additionalProperties": value}, ok
	case *ast.InterfaceType:
		return map[string]any{}, true
	case *ast.StructType:
		properties := map[string]any{}
		required := []string{}
		complete := true
		for _, field := range typ.Fields.List {
			tag := reflect.StructTag("")
			if field.Tag != nil {
				value, _ := strconv.Unquote(field.Tag.Value)
				tag = reflect.StructTag(value)
			}
			jsonName := strings.Split(tag.Get("json"), ",")[0]
			if jsonName == "-" {
				continue
			}
			if len(field.Names) > 0 && !ast.IsExported(field.Names[0].Name) {
				continue
			}
			value, ok := g.schema(definition{field.Type, def.source}, locals, seen)
			complete = complete && ok
			if len(field.Names) == 0 && jsonName == "" {
				if inner, ok := value["properties"].(map[string]any); ok {
					for key, item := range inner {
						properties[key] = item
					}
					if req, ok := value["required"].([]string); ok {
						required = append(required, req...)
					}
				} else {
					complete = false
				}
				continue
			}
			comment := strings.TrimSpace(field.Doc.Text() + field.Comment.Text())
			if comment != "" {
				value["description"] = comment
			}
			for _, name := range field.Names {
				key := jsonName
				if key == "" {
					key = name.Name
				}
				properties[key] = value
				for _, rule := range strings.Split(tag.Get("binding"), ",") {
					if rule == "required" {
						required = append(required, key)
					}
				}
			}
		}
		schema := object(properties)
		if len(required) > 0 {
			sort.Strings(required)
			schema["required"] = required
		}
		return schema, complete
	}
	return map[string]any{"description": "Unresolved Go request type: " + render(def.expr)}, false
}

// Write refreshes the checked-in data file from a Go module root.
func Write(root string) error {
	data, err := Generate(root)
	if err != nil {
		return fmt.Errorf("generate assistant contracts: %w", err)
	}
	return os.WriteFile(filepath.Join(root, "controller", "assistant_admin_operation_contracts.json"), data, 0644)
}
