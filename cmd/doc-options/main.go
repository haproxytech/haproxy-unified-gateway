// Copyright 2025 HAProxy Technologies LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/renameio"
	"github.com/phuslu/log"
	"gopkg.in/yaml.v3"
)

//revive:disable:line-length-limit

func main() { //revive:disable:cognitive-complexity,unhandled-error,function-length
	defer func() {
		if err := recover(); err != nil {
			log.Error().Err(fmt.Errorf("%v", err)).Msg("")
		}
		log.Info().Str("action", "end").Msg("")
	}()
	log.DefaultLogger = log.Logger{
		Level:      log.PanicLevel,
		TimeFormat: time.DateTime,
		// Caller:     1,
		Writer: &log.ConsoleWriter{
			ColorOutput:    true,
			QuoteString:    false,
			EndWithMessage: true,
		},
	}
	sublogger := log.DefaultLogger
	sublogger.Context = log.NewContext(nil).Str("application", "doc-options").Value()
	log.DefaultLogger = sublogger

	log.Info().Str("action", "start").Msg("")

	documentation := make(map[string]docItem)

	pkgPath := "../../k8s/gate/options"

	fs := token.NewFileSet()
	entries, err := os.ReadDir(pkgPath)
	if err != nil {
		log.Panic().Err(err).Msg("")
	}

	var files []*ast.File
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		f, parseErr := parser.ParseFile(fs, filepath.Join(pkgPath, entry.Name()), nil, parser.ParseComments)
		if parseErr != nil {
			log.Panic().Err(parseErr).Msg("")
		}
		files = append(files, f)
	}

	for _, file := range files {
		// Iterate over the declarations in the file
		for _, decl := range file.Decls {
			// Check if the declaration is a function declaration
			if funcDecl, ok := decl.(*ast.FuncDecl); ok {
				// Check if the function is exported (starts with an uppercase letter)
				if funcDecl.Name.IsExported() {
					if strings.HasPrefix(funcDecl.Name.Name, "Test") {
						continue
					}
					if funcDecl.Type.Results == nil || len(funcDecl.Type.Results.List) != 1 {
						continue
					}
					if result, ok := funcDecl.Type.Results.List[0].Type.(*ast.StarExpr); ok {
						if sel, ok := result.X.(*ast.SelectorExpr); ok {
							if sel.Sel.Name == "Configuration" && sel.X.(*ast.Ident).Name == "config" {
								continue
							}
						}
					}

					args := make([]arguments, 0)
					// Iterate over the parameters of the function
					for _, param := range funcDecl.Type.Params.List {
						// Handle different types of parameter types
						switch t := param.Type.(type) {
						case *ast.Ident:
							args = append(args, arguments{Name: param.Names[0].Name, Type: t.Name})
						case *ast.StarExpr:
							if ident, ok := t.X.(*ast.Ident); ok {
								log.Info().Str("parameter", param.Names[0].Name).Str("value", "*"+ident.Name).Msg("")
								args = append(args, arguments{Name: param.Names[0].Name, Type: "*" + ident.Name})
							}
						case *ast.SelectorExpr:
							log.Info().Str("parameter", param.Names[0].Name).Str("value", t.X.(*ast.Ident).Name+"."+t.Sel.Name).Msg("")
							args = append(args, arguments{Name: param.Names[0].Name, Type: t.X.(*ast.Ident).Name + "." + t.Sel.Name})
						default:
							log.Info().Str("parameter", param.Names[0].Name).Str("value", fmt.Sprintf("%T", t)).Msg("")
							args = append(args, arguments{Name: param.Names[0].Name, Type: fmt.Sprintf("%T", t)})
						}
					}

					documentation[funcDecl.Name.Name] = docItem{
						Name:    funcDecl.Name.Name,
						Args:    args,
						Comment: funcDecl.Doc.Text(),
					}
				}
			}
		}
	}

	// yaml marshall of documentation
	result, err := yaml.Marshal(documentation) //nolint:musttag
	if err != nil {
		log.Panic().Err(err).Msg("")
	}
	err = renameio.WriteFile("../../documentation/controller-options.yaml", result, 0o644)
	if err != nil {
		log.Panic().Err(err).Msg("")
	}

	// now also generate buff file
	buff := strings.Builder{}
	buff.WriteString(`# ![HAProxy](../assets/images/haproxy-weblogo-210x49.png "HAProxy")`)
	buff.WriteRune('\n')
	buff.WriteString(`## Kubernetes Controller`)
	buff.WriteRune('\n')
	buff.WriteRune('\n')
	buff.WriteString(`## Options`)
	buff.WriteRune('\n')
	buff.WriteRune('\n')
	buff.WriteString(`Multiple options can be combined`)
	buff.WriteRune('\n')
	buff.WriteRune('\n')
	buff.WriteString("Example:\n```go\n")
	buff.WriteString("import (\n")
	buff.WriteString("  github.com/haproxytech/haproxy-unified-gateway/k8s/gate\n")
	buff.WriteString("  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options\n")
	buff.WriteString(")\n\n")
	buff.WriteString("// multiple options can be combined\n")
	buff.WriteString("controller, err := controller.New(opt.Option1(arg1), opt.Flag())\n```\n\n")
	buff.WriteString("Available options:\n\n")

	buff.WriteString("| Function | Arguments |\n")
	buff.WriteString("| ---:|:--- |\n")

	keys := slices.Sorted(maps.Keys(documentation))
	for _, key := range keys {
		item := documentation[key]
		buff.WriteString("| " + item.Name + " | ")
		for index, arg := range item.Args {
			if index > 0 {
				buff.WriteString(", ")
			}
			buff.WriteString(fmt.Sprintf("`%s`(%s)", arg.Name, arg.Type))
		}
		buff.WriteString(" |\n")
	}
	buff.WriteRune('\n')
	for _, key := range keys {
		item := documentation[key]
		buff.WriteString(fmt.Sprintf("### %s\n\n", item.Name))
		buff.WriteString(item.Comment)
		buff.WriteRune('\n')
		buff.WriteString("Example:\n```go\n")
		buff.WriteString("import (\n")
		buff.WriteString("  github.com/haproxytech/haproxy-unified-gateway/k8s/gate\n")
		buff.WriteString("  github.com/haproxytech/haproxy-unified-gateway/k8s/gate/options\n")
		buff.WriteString(")\n\n")
		buff.WriteString("controller, err := controller.New(opt." + item.Name + "(")
		for index, arg := range item.Args {
			if index > 0 {
				buff.WriteString(", ")
			}
			buff.WriteString(arg.Name)
		}
		buff.WriteString("))\n```\n")
		buff.WriteRune('\n')
	}
	err = renameio.WriteFile("../../documentation/controller-options.md", []byte(buff.String()), 0o644)
	if err != nil {
		log.Panic().Err(err).Msg("")
	}

	generateLifecycle()
}
