/*
 * Copyright © 2017 Slizov Vadim <z7zmey@gmail.com>
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gorilla/mux"

	"github.com/cayleygraph/cayley"
	"github.com/cayleygraph/cayley/graph/path"
	"github.com/cayleygraph/cayley/quad"
	"github.com/cayleygraph/cayley/schema"

	"github.com/yookoala/realpath"
)

func Index(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/app", http.StatusMovedPermanently)
}

func GetAll(w http.ResponseWriter, r *http.Request) {
	// Print quads
	fmt.Fprintln(w, "quads:")
	it := store.QuadsAllIterator()
	for it.Next() {
		fmt.Fprintln(w, store.Quad(it.Result()))
	}
}

func GetTree(w http.ResponseWriter, r *http.Request) {
	resp := PathNode{
		"",
		"root",
		make([]*PathNode, 0),
		true,
		true,
	}

	if len(Config.path) == 1 {
		resp = GetPathTree(Config.path[0])
	} else if len(Config.path) > 1 {
		for _, dir := range Config.path {
			dir, err := realpath.Realpath(dir)
			checkErr(err)
			pathNode := GetPathTree(dir)
			resp.Children = append(resp.Children, &pathNode)
		}
	}

	encoder := json.NewEncoder(w)
	encoder.Encode(resp)
}

type umlMethod struct {
	NsName     string
	Name       string
	Visibility string
}

type uml struct {
	Name       string
	Extends    string
	Methods    []umlMethod
	Implements []string
	IsAbstract bool
}

func getCayleyPathForUml(pathes []string) *path.Path {
	if len(pathes) == 0 {
		return cayley.StartPath(store)
	}

	has := []quad.Value{}

	for _, path := range pathes {
		has = append(has, quad.IRI(path))
	}

	pBase := cayley.StartPath(store).
		Has(quad.IRI("ast:file"), has...)

	pImplements := cayley.StartPath(store).
		Out(quad.IRI("ast:implements"), quad.IRI("ast:extends"))

	pParent := pBase.FollowRecursive(pImplements, []string{}).Tag("main").Back("main")

	return pBase.Or(pParent).Unique()
}

func getFilesInPath(path string) []string {
	files := []string{}
	visit := func(path string, info os.FileInfo, err error) error {
		files = append(files, path)
		return nil
	}

	err := filepath.Walk(path, visit)
	if err != nil {
		log.Fatal(err)
	}

	return files
}

func GetUml(w http.ResponseWriter, r *http.Request) {
	resp := make([]uml, 0)
	vars := mux.Vars(r)

	files := getFilesInPath(vars["path"])
	var classes []AstClass
	p := getCayleyPathForUml(files)
	err := schema.LoadPathTo(nil, store, &classes, p)
	checkErr(err)

	for _, astClass := range classes {
		var cls = uml{
			astClass.Name,
			string(astClass.Extends),
			[]umlMethod{},
			[]string{},
			astClass.IsAbstract || astClass.IsInterface,
		}

		for _, implements := range astClass.Implements {
			cls.Implements = append(cls.Implements, string(implements))
		}

		p := cayley.StartPath(store).Has(quad.IRI("ast:class"), quad.IRI(astClass.Name))

		var methods []AstMethod
		err := schema.LoadPathTo(nil, store, &methods, p)
		checkErr(err)

		for _, astMethod := range methods {
			var umlMethod = umlMethod{
				astMethod.ID,
				astMethod.Name,
				astMethod.Visibility,
			}
			cls.Methods = append(cls.Methods, umlMethod)
		}

		resp = append(resp, cls)
	}

	w.Header().Set("Content-Type", "application/json")
	encoder := json.NewEncoder(w)
	encoder.Encode(resp)
}

func GetMethods(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	name := v.Get("name")

	p := cayley.StartPath(store)
	if name != "" {
		p = p.Has(quad.IRI("ast:name"), quad.String(name))
	}

	var methods []AstMethod
	err := schema.LoadPathTo(nil, store, &methods, p)
	checkErr(err)

	encoder := json.NewEncoder(w)
	encoder.Encode(methods)
}

func GetMethodCalls(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	var baseMethod AstMethod
	err := schema.LoadTo(nil, store, &baseMethod, quad.IRI(vars["name"]))
	checkErr(err)

	pCallsAndImplementations := cayley.StartPath(store).
		Out(quad.IRI("ast:calls"), quad.IRI("ast:method_implementation"))

	p := cayley.StartPath(store, quad.IRI(vars["name"])).
		FollowRecursive(pCallsAndImplementations, []string{})

	var methods []AstMethod
	err = schema.LoadPathTo(nil, store, &methods, p)
	checkErr(err)

	methods = append(methods, baseMethod)

	encoder := json.NewEncoder(w)
	encoder.Encode(methods)
}

func GetMethodCode(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	var method AstMethod
	err := schema.LoadTo(nil, store, &method, quad.IRI(vars["name"]))
	checkErr(err)

	var class AstClass
	err = schema.LoadTo(nil, store, &class, method.Class)
	checkErr(err)

	inFile, _ := os.Open(string(class.File))
	defer inFile.Close()
	scanner := bufio.NewScanner(inFile)
	scanner.Split(bufio.ScanLines)

	for i := 1; i < method.StartLine; i++ {
		scanner.Scan()
	}

	for i := method.StartLine; i <= method.EndLine; i++ {
		scanner.Scan()
		w.Write(scanner.Bytes())
		fmt.Fprint(w, "\n")
	}

}

func GetAstData(w http.ResponseWriter, r *http.Request) {
	w.Write(CacheGetAst())
}

func GetAbstractMethods(w http.ResponseWriter, r *http.Request) {
	p := cayley.StartPath(store, quad.IRI("League\\Plates\\Extension\\ExtensionInterface::register")).
		Has(quad.IRI("ast:is_abstract"), quad.Bool(true))

	var methods []AstMethod
	err := schema.LoadPathTo(nil, store, &methods, p)
	checkErr(err)

	encoder := json.NewEncoder(w)
	encoder.Encode(methods)
}

func GetClasses(w http.ResponseWriter, r *http.Request) {
	var classes []AstClass
	err := schema.LoadTo(nil, store, &classes)
	checkErr(err)

	encoder := json.NewEncoder(w)
	encoder.Encode(classes)
}

func GetProperties(w http.ResponseWriter, r *http.Request) {
	var properties []AstProperty
	err := schema.LoadTo(nil, store, &properties)
	checkErr(err)

	encoder := json.NewEncoder(w)
	encoder.Encode(properties)
}
