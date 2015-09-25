package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"text/template"

	"github.com/attic-labs/noms/Godeps/_workspace/src/golang.org/x/tools/imports"
	"github.com/attic-labs/noms/types"

	"github.com/attic-labs/noms/d"
	"github.com/attic-labs/noms/nomdl/parse"
)

var (
	inFlag      = flag.String("in", "", "The name of the noms file to read")
	outFlag     = flag.String("out", "", "The name of the go file to write")
	packageFlag = flag.String("package", "", "The name of the go package to write")
)

const ext = ".noms"

func main() {
	flag.Parse()

	packageName := getGoPackageName()
	if *inFlag != "" {
		out := *outFlag
		if out == "" {
			out = getOutFileName(*inFlag)
		}
		generate(packageName, *inFlag, out)
		return
	}

	// Generate code from all .noms file in the current directory
	nomsFiles, err := filepath.Glob("*" + ext)
	d.Chk.NoError(err)
	for _, n := range nomsFiles {
		generate(packageName, n, getOutFileName(n))
	}
}

func getOutFileName(in string) string {
	return in[:len(in)-len(ext)] + ".go"
}

func getBareFileName(in string) string {
	base := filepath.Base(in)
	return base[:len(base)-len(filepath.Ext(base))]
}

func generate(packageName, in, out string) {
	inFile, err := os.Open(in)
	d.Chk.NoError(err)
	defer inFile.Close()

	var buf bytes.Buffer
	pkg := parse.ParsePackage("", inFile)
	gen := NewCodeGen(&buf, getBareFileName(in), pkg)
	gen.WritePackage(packageName)

	bs, err := imports.Process(out, buf.Bytes(), nil)
	d.Chk.NoError(err)

	outFile, err := os.OpenFile(out, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	d.Chk.NoError(err)
	defer outFile.Close()

	io.Copy(outFile, bytes.NewBuffer(bs))
}

func getGoPackageName() string {
	if *packageFlag != "" {
		return *packageFlag
	}

	// It is illegal to have multiple go files in the same directory with different package names.
	// We can therefore just pick the first one and get the package name from there.
	goFiles, err := filepath.Glob("*.go")
	d.Chk.NoError(err)
	d.Chk.True(len(goFiles) > 0)

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, goFiles[0], nil, parser.PackageClauseOnly)
	d.Chk.NoError(err)
	return f.Name.String()
}

type codeGen struct {
	w         io.Writer
	pkg       parse.Package
	fileid    string
	written   map[string]bool
	templates *template.Template
}

func NewCodeGen(w io.Writer, fileID string, pkg parse.Package) *codeGen {
	gen := &codeGen{w, pkg, fileID, map[string]bool{}, nil}
	gen.templates = gen.readTemplates()
	return gen
}

func (gen *codeGen) readTemplates() *template.Template {
	_, thisfile, _, _ := runtime.Caller(1)
	glob := path.Join(path.Dir(thisfile), "*.tmpl")
	return template.Must(template.New("").Funcs(
		template.FuncMap{
			"defType":        gen.defType,
			"defToValue":     gen.defToValue,
			"valueToDef":     gen.valueToDef,
			"userType":       gen.userType,
			"userToValue":    gen.userToValue,
			"valueToUser":    gen.valueToUser,
			"userZero":       gen.userZero,
			"valueZero":      gen.valueZero,
			"title":          strings.Title,
			"toTypesTypeRef": gen.toTypesTypeRef,
		}).ParseGlob(glob))
}

// Conceptually there are few type spaces here:
//
// - Def - MyStructDef, ListOfBoolDef
// - Native - such as string, uint32
// - Value - the generic types.Value
// - Nom - types.String, types.UInt32, MyStruct, ListOfBool
// - User - User defined structs, enums etc as well as native primitves. This uses Native when possible or Nom if not
//
// These naming conventions are used for the conversion functions available
// in the templates.

func (gen *codeGen) defType(t parse.TypeRef) string {
	t = gen.resolve(t)
	k := t.Desc.Kind()
	switch k {
	case types.BlobKind:
		return "types.Blob"
	case types.BoolKind, types.Float32Kind, types.Float64Kind, types.Int16Kind, types.Int32Kind, types.Int64Kind, types.Int8Kind, types.StringKind, types.UInt16Kind, types.UInt32Kind, types.UInt64Kind, types.UInt8Kind:
		return strings.ToLower(kindToString(k))
	case types.EnumKind:
		return gen.userName(t)
	case types.ListKind, types.MapKind, types.SetKind, types.StructKind:
		return gen.userName(t) + "Def"
	case types.RefKind:
		return "ref.Ref"
	case types.ValueKind:
		return "types.Value"
	case types.TypeRefKind:
		return "types.TypeRef"
	}
	panic("unreachable")
}

func (gen *codeGen) userType(t parse.TypeRef) string {
	t = gen.resolve(t)
	k := t.Desc.Kind()
	switch k {
	case types.BlobKind:
		return "types.Blob"
	case types.BoolKind, types.Float32Kind, types.Float64Kind, types.Int16Kind, types.Int32Kind, types.Int64Kind, types.Int8Kind, types.StringKind, types.UInt16Kind, types.UInt32Kind, types.UInt64Kind, types.UInt8Kind:
		return strings.ToLower(kindToString(k))
	case types.EnumKind, types.ListKind, types.MapKind, types.RefKind, types.SetKind, types.StructKind:
		return gen.userName(t)
	case types.ValueKind:
		return "types.Value"
	case types.TypeRefKind:
		return "types.TypeRef"
	}
	panic("unreachable")
}

func (gen *codeGen) defToValue(val string, t parse.TypeRef) string {
	t = gen.resolve(t)
	switch t.Desc.Kind() {
	case types.BlobKind, types.ValueKind, types.TypeRefKind:
		return val // Blob & Value type has no Def
	case types.BoolKind, types.Float32Kind, types.Float64Kind, types.Int16Kind, types.Int32Kind, types.Int64Kind, types.Int8Kind, types.StringKind, types.UInt16Kind, types.UInt32Kind, types.UInt64Kind, types.UInt8Kind:
		return gen.nativeToValue(val, t)
	case types.EnumKind:
		return fmt.Sprintf("types.Int32(%s)", val)
	case types.ListKind, types.MapKind, types.SetKind, types.StructKind:
		return fmt.Sprintf("%s.New().NomsValue()", val)
	case types.RefKind:
		return fmt.Sprintf("types.Ref{R: %s}", val)
	}
	panic("unreachable")
}

func (gen *codeGen) valueToDef(val string, t parse.TypeRef) string {
	t = gen.resolve(t)
	switch t.Desc.Kind() {
	case types.BlobKind:
		return gen.valueToUser(val, t)
	case types.BoolKind, types.Float32Kind, types.Float64Kind, types.Int16Kind, types.Int32Kind, types.Int64Kind, types.Int8Kind, types.StringKind, types.UInt16Kind, types.UInt32Kind, types.UInt64Kind, types.UInt8Kind:
		return gen.valueToNative(val, t)
	case types.EnumKind:
		return fmt.Sprintf("%s(%s.(types.Int32))", gen.userName(t), val)
	case types.ListKind, types.MapKind, types.SetKind, types.StructKind:
		return fmt.Sprintf("%s.Def()", gen.valueToUser(val, t))
	case types.RefKind:
		return fmt.Sprintf("%s.Ref()", val)
	case types.ValueKind:
		return val // Value type has no Def
	case types.TypeRefKind:
		return gen.valueToUser(val, t)
	}
	panic("unreachable")
}

func kindToString(k types.NomsKind) string {
	switch k {
	case types.BlobKind:
		return "Blob"
	case types.BoolKind:
		return "Bool"
	case types.Float32Kind:
		return "Float32"
	case types.Float64Kind:
		return "Float64"
	case types.Int16Kind:
		return "Int16"
	case types.Int32Kind:
		return "Int32"
	case types.Int64Kind:
		return "Int64"
	case types.Int8Kind:
		return "Int8"
	case types.StringKind:
		return "String"
	case types.UInt16Kind:
		return "UInt16"
	case types.UInt32Kind:
		return "UInt32"
	case types.UInt64Kind:
		return "UInt64"
	case types.UInt8Kind:
		return "UInt8"
	case types.ValueKind:
		return "Value"
	case types.TypeRefKind:
		return "TypeRef"
	case types.ListKind:
		return "List"
	case types.MapKind:
		return "Map"
	case types.RefKind:
		return "Ref"
	case types.SetKind:
		return "Set"
	}
	panic("unreachable")
}

func (gen *codeGen) nativeToValue(val string, t parse.TypeRef) string {
	t = gen.resolve(t)
	k := t.Desc.Kind()
	switch k {
	case types.BoolKind, types.Float32Kind, types.Float64Kind, types.Int16Kind, types.Int32Kind, types.Int64Kind, types.Int8Kind, types.UInt16Kind, types.UInt32Kind, types.UInt64Kind, types.UInt8Kind:
		return fmt.Sprintf("types.%s(%s)", kindToString(k), val)
	case types.EnumKind:
		return fmt.Sprintf("types.Int32(%s)", val)
	case types.StringKind:
		return "types.NewString(" + val + ")"
	}
	panic("unreachable")
}

func (gen *codeGen) valueToNative(val string, t parse.TypeRef) string {
	k := t.Desc.Kind()
	switch k {
	case types.EnumKind:
		return fmt.Sprintf("%s(%s.(types.Int32))", gen.userType(t), val)
	case types.BoolKind, types.Float32Kind, types.Float64Kind, types.Int16Kind, types.Int32Kind, types.Int64Kind, types.Int8Kind, types.UInt16Kind, types.UInt32Kind, types.UInt64Kind, types.UInt8Kind:
		n := kindToString(k)
		return fmt.Sprintf("%s(%s.(types.%s))", strings.ToLower(n), val, n)
	case types.StringKind:
		return val + ".(types.String).String()"
	}
	panic("unreachable")
}

func (gen *codeGen) userToValue(val string, t parse.TypeRef) string {
	t = gen.resolve(t)
	k := t.Desc.Kind()
	switch k {
	case types.BlobKind, types.ValueKind, types.TypeRefKind:
		return val
	case types.BoolKind, types.EnumKind, types.Float32Kind, types.Float64Kind, types.Int16Kind, types.Int32Kind, types.Int64Kind, types.Int8Kind, types.StringKind, types.UInt16Kind, types.UInt32Kind, types.UInt64Kind, types.UInt8Kind:
		return gen.nativeToValue(val, t)
	case types.ListKind, types.MapKind, types.RefKind, types.SetKind, types.StructKind:
		return fmt.Sprintf("%s.NomsValue()", val)
	}
	panic("unreachable")
}

func (gen *codeGen) valueToUser(val string, t parse.TypeRef) string {
	t = gen.resolve(t)
	k := t.Desc.Kind()
	switch k {
	case types.BlobKind:
		return fmt.Sprintf("%s.(types.Blob)", val)
	case types.BoolKind, types.EnumKind, types.Float32Kind, types.Float64Kind, types.Int16Kind, types.Int32Kind, types.Int64Kind, types.Int8Kind, types.StringKind, types.UInt16Kind, types.UInt32Kind, types.UInt64Kind, types.UInt8Kind:
		return gen.valueToNative(val, t)
	case types.ListKind, types.MapKind, types.RefKind, types.SetKind, types.StructKind:
		return fmt.Sprintf("%sFromVal(%s)", gen.userName(t), val)
	case types.ValueKind:
		return val
	case types.TypeRefKind:
		return fmt.Sprintf("%s.(types.TypeRef)", val)
	}
	panic("unreachable")
}

func (gen *codeGen) userZero(t parse.TypeRef) string {
	t = gen.resolve(t)
	k := t.Desc.Kind()
	switch k {
	case types.BlobKind:
		return "types.NewEmptyBlob()"
	case types.BoolKind:
		return "false"
	case types.EnumKind:
		return fmt.Sprintf("%s(0)", gen.userType(t))
	case types.Float32Kind, types.Float64Kind, types.Int16Kind, types.Int32Kind, types.Int64Kind, types.Int8Kind, types.UInt16Kind, types.UInt32Kind, types.UInt64Kind, types.UInt8Kind:
		return fmt.Sprintf("%s(0)", strings.ToLower(kindToString(k)))
	case types.ListKind, types.MapKind, types.SetKind:
		return fmt.Sprintf("New%s()", gen.userType(t))
	case types.RefKind:
		return fmt.Sprintf("%s{ref.Ref{}}", gen.userType(t))
	case types.StringKind:
		return `""`
	case types.ValueKind:
		// TODO: This is where a null Value would have been useful.
		return "types.Bool(false)"
	case types.TypeRefKind:
		return "types.TypeRef{}"
	}
	panic("unreachable")
}

func (gen *codeGen) valueZero(t parse.TypeRef) string {
	t = gen.resolve(t)
	k := t.Desc.Kind()
	switch k {
	case types.BlobKind:
		return "types.NewEmptyBlob()"
	case types.BoolKind:
		return "types.Bool(false)"
	case types.EnumKind:
		return "types.Int32(0)"
	case types.Float32Kind, types.Float64Kind, types.Int16Kind, types.Int32Kind, types.Int64Kind, types.Int8Kind, types.UInt16Kind, types.UInt32Kind, types.UInt64Kind, types.UInt8Kind:
		return fmt.Sprintf("types.%s(0)", kindToString(k))
	case types.ListKind:
		return "types.NewList()"
	case types.MapKind:
		return "types.NewMap()"
	case types.RefKind:
		return "types.Ref{R: ref.Ref{}}"
	case types.SetKind:
		return "types.NewSet()"
	case types.StringKind:
		return `types.NewString("")`
	case types.StructKind:
		return fmt.Sprintf("New%s().NomsValue()", gen.userName(t))
	case types.ValueKind:
		// TODO: This is where a null Value would have been useful.
		return "types.Bool(false)"
	case types.TypeRefKind:
		return "types.TypeRef{}"
	}
	panic("unreachable")
}

func (gen *codeGen) userName(t parse.TypeRef) string {
	t = gen.resolve(t)
	k := t.Desc.Kind()
	switch k {
	case types.BlobKind, types.BoolKind, types.Float32Kind, types.Float64Kind, types.Int16Kind, types.Int32Kind, types.Int64Kind, types.Int8Kind, types.StringKind, types.UInt16Kind, types.UInt32Kind, types.UInt64Kind, types.UInt8Kind, types.ValueKind, types.TypeRefKind:
		return kindToString(k)
	case types.EnumKind:
		return t.Name
	case types.ListKind:
		return fmt.Sprintf("ListOf%s", gen.userName(t.Desc.(parse.CompoundDesc).ElemTypes[0]))
	case types.MapKind:
		elemTypes := t.Desc.(parse.CompoundDesc).ElemTypes
		return fmt.Sprintf("MapOf%sTo%s", gen.userName(elemTypes[0]), gen.userName(elemTypes[1]))
	case types.RefKind:
		return fmt.Sprintf("RefOf%s", gen.userName(t.Desc.(parse.CompoundDesc).ElemTypes[0]))
	case types.SetKind:
		return fmt.Sprintf("SetOf%s", gen.userName(t.Desc.(parse.CompoundDesc).ElemTypes[0]))
	case types.StructKind:
		// We get an empty name when we have a struct that is used as union
		if t.Name == "" {
			union := t.Desc.(parse.StructDesc).Union
			s := "__unionOf"
			for i, f := range union.Choices {
				if i > 0 {
					s += "And"
				}
				s += strings.Title(f.Name) + "Of" + gen.userName(f.T)
			}
			return s
		}
		return t.Name
	}
	panic("unreachable")
}

func (gen *codeGen) toTypesTypeRef(t parse.TypeRef) string {
	if t.IsUnresolved() {
		// needs to be pkgRef
		return fmt.Sprintf(`types.MakeTypeRef("%s", types.Ref{})`, t.Name)
	}
	if types.IsPrimitiveKind(t.Desc.Kind()) {
		return fmt.Sprintf("types.MakePrimitiveTypeRef(types.%sKind)", kindToString(t.Desc.Kind()))
	}
	switch desc := t.Desc.(type) {
	case parse.CompoundDesc:
		typerefs := make([]string, len(desc.ElemTypes))
		for i, t := range desc.ElemTypes {
			typerefs[i] = gen.toTypesTypeRef(t)
		}
		return fmt.Sprintf(`types.MakeCompoundTypeRef("%s", types.%sKind, %s)`, t.Name, kindToString(t.Desc.Kind()), strings.Join(typerefs, ", "))
	case parse.EnumDesc:
		return fmt.Sprintf(`types.MakeEnumTypeRef("%s", %s)`, t.Name, strings.Join(desc.IDs, ", "))
	case parse.StructDesc:
		flatten := func(f []parse.Field) string {
			out := make([]string, 0, len(f))
			for _, field := range f {
				out = append(out, fmt.Sprintf(`types.Field{"%s", %s, %t},`, field.Name, gen.toTypesTypeRef(field.T), field.Optional))
			}
			return strings.Join(out, "\n")
		}
		fields := "nil"
		choices := "nil"
		if len(desc.Fields) != 0 {
			fields = fmt.Sprintf("[]types.Field{%s})", flatten(desc.Fields))
		}
		if desc.Union != nil {
			choices = fmt.Sprintf("[]types.Field{%s}", flatten(desc.Union.Choices))
		}
		return fmt.Sprintf(`types.MakeStructTypeRef("%s", %s, %s)`, t.Name, fields, choices)
	default:
		d.Chk.Fail("Unknown TypeDesc.", "%#v (%T)", desc, desc)
	}
	panic("ain't done")
}

func (gen *codeGen) resolve(t parse.TypeRef) parse.TypeRef {
	if !t.IsUnresolved() {
		return t
	}
	return gen.pkg.NamedTypes[t.Name]
}

func (gen *codeGen) WritePackage(packageName string) {
	gen.pkg.Name = packageName
	data := struct {
		HasTypes   bool
		FileID     string
		Name       string
		NamedTypes map[string]parse.TypeRef
	}{
		len(gen.pkg.NamedTypes) > 0,
		gen.fileid,
		gen.pkg.Name,
		gen.pkg.NamedTypes,
	}
	err := gen.templates.ExecuteTemplate(gen.w, "header.tmpl", data)
	d.Exp.NoError(err)

	for _, t := range gen.pkg.UsingDeclarations {
		gen.write(t)
	}

	names := make([]string, 0, len(gen.pkg.NamedTypes))
	for n, _ := range gen.pkg.NamedTypes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		gen.write(gen.pkg.NamedTypes[n])
	}
}

func (gen *codeGen) write(t parse.TypeRef) {
	t = gen.resolve(t)
	if gen.written[gen.userName(t)] {
		return
	}
	k := t.Desc.Kind()
	switch k {
	case types.BlobKind, types.BoolKind, types.Float32Kind, types.Float64Kind, types.Int16Kind, types.Int32Kind, types.Int64Kind, types.Int8Kind, types.StringKind, types.UInt16Kind, types.UInt32Kind, types.UInt64Kind, types.UInt8Kind, types.ValueKind, types.TypeRefKind:
		return
	case types.EnumKind:
		gen.writeEnum(t)
	case types.ListKind:
		gen.writeList(t)
	case types.MapKind:
		gen.writeMap(t)
	case types.RefKind:
		gen.writeRef(t)
	case types.SetKind:
		gen.writeSet(t)
	case types.StructKind:
		gen.writeStruct(t)
	default:
		panic("unreachable")
	}
}

func (gen *codeGen) writeTemplate(tmpl string, t parse.TypeRef, data interface{}) {
	err := gen.templates.ExecuteTemplate(gen.w, tmpl, data)
	d.Exp.NoError(err)
	gen.written[gen.userName(t)] = true
}

func (gen *codeGen) writeStruct(t parse.TypeRef) {
	desc := t.Desc.(parse.StructDesc)
	data := struct {
		FileID        string
		PackageName   string
		Name          string
		Fields        []parse.Field
		Choices       []parse.Field
		HasUnion      bool
		UnionZeroType parse.TypeRef
		CanUseDef     bool
	}{
		gen.fileid,
		gen.pkg.Name,
		gen.userName(t),
		desc.Fields,
		nil,
		desc.Union != nil,
		parse.TypeRef{Desc: parse.PrimitiveDesc(types.UInt32Kind)},
		gen.canUseDef(t),
	}
	if data.HasUnion {
		data.Choices = desc.Union.Choices
		data.UnionZeroType = data.Choices[0].T
	}
	gen.writeTemplate("struct.tmpl", t, data)
	for _, f := range desc.Fields {
		gen.write(f.T)
	}
	if data.HasUnion {
		for _, f := range desc.Union.Choices {
			gen.write(f.T)
		}
	}
}

func (gen *codeGen) writeList(t parse.TypeRef) {
	elemTypes := t.Desc.(parse.CompoundDesc).ElemTypes
	data := struct {
		Name      string
		ElemType  parse.TypeRef
		CanUseDef bool
	}{
		gen.userName(t),
		elemTypes[0],
		gen.canUseDef(t),
	}
	gen.writeTemplate("list.tmpl", t, data)
	gen.write(elemTypes[0])
}

func (gen *codeGen) writeMap(t parse.TypeRef) {
	elemTypes := t.Desc.(parse.CompoundDesc).ElemTypes
	data := struct {
		Name      string
		KeyType   parse.TypeRef
		ValueType parse.TypeRef
		CanUseDef bool
	}{
		gen.userName(t),
		elemTypes[0],
		elemTypes[1],
		gen.canUseDef(t),
	}
	gen.writeTemplate("map.tmpl", t, data)
	gen.write(elemTypes[0])
	gen.write(elemTypes[1])
}

func (gen *codeGen) writeRef(t parse.TypeRef) {
	elemTypes := t.Desc.(parse.CompoundDesc).ElemTypes
	data := struct {
		Name     string
		ElemType parse.TypeRef
	}{
		gen.userName(t),
		elemTypes[0],
	}
	gen.writeTemplate("ref.tmpl", t, data)
	gen.write(elemTypes[0])
}

func (gen *codeGen) writeSet(t parse.TypeRef) {
	elemTypes := t.Desc.(parse.CompoundDesc).ElemTypes
	data := struct {
		Name      string
		ElemType  parse.TypeRef
		CanUseDef bool
	}{
		gen.userName(t),
		elemTypes[0],
		gen.canUseDef(t),
	}
	gen.writeTemplate("set.tmpl", t, data)
	gen.write(elemTypes[0])
}

func (gen *codeGen) writeEnum(t parse.TypeRef) {
	data := struct {
		Name string
		Ids  []string
	}{
		t.Name,
		t.Desc.(parse.EnumDesc).IDs,
	}
	gen.writeTemplate("enum.tmpl", t, data)
}

func (gen *codeGen) canUseDef(t parse.TypeRef) bool {
	cache := map[string]bool{}

	var rec func(t parse.TypeRef) bool
	rec = func(t parse.TypeRef) bool {
		t = gen.resolve(t)
		switch t.Desc.Kind() {
		case types.ListKind:
			return rec(t.Desc.(parse.CompoundDesc).ElemTypes[0])
		case types.SetKind:
			elemType := t.Desc.(parse.CompoundDesc).ElemTypes[0]
			return !gen.containsNonComparable(elemType) && rec(elemType)
		case types.MapKind:
			elemTypes := t.Desc.(parse.CompoundDesc).ElemTypes
			return !gen.containsNonComparable(elemTypes[0]) && rec(elemTypes[0]) && rec(elemTypes[1])
		case types.StructKind:
			userName := gen.userName(t)
			if b, ok := cache[userName]; ok {
				return b
			}
			cache[userName] = true
			for _, f := range t.Desc.(parse.StructDesc).Fields {
				if f.T.Equals(t) || !rec(f.T) {
					cache[userName] = false
					return false
				}
			}
			return true
		default:
			return true
		}
	}

	return rec(t)
}

// We use a go map as the def for Set and Map. These cannot have a key that is a
// Set, Map or a List because slices and maps are not comparable in go.
func (gen *codeGen) containsNonComparable(t parse.TypeRef) bool {
	cache := map[string]bool{}

	var rec func(t parse.TypeRef) bool
	rec = func(t parse.TypeRef) bool {
		t = gen.resolve(t)
		switch t.Desc.Kind() {
		case types.ListKind, types.MapKind, types.SetKind:
			return true
		case types.StructKind:
			// Only structs can be recursive
			userName := gen.userName(t)
			if b, ok := cache[userName]; ok {
				return b
			}
			// If we get here in a recursive call we will mark it as not having a non comparable value. If it does then that will
			// get handled higher up in the call chain.
			cache[userName] = false
			for _, f := range t.Desc.(parse.StructDesc).Fields {
				if rec(f.T) {
					cache[userName] = true
					return true
				}
			}
			return cache[userName]
		default:
			return false
		}
	}

	return rec(t)
}