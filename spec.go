package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

type xmlRegistry struct {
	Types      []xmlType      `xml:"types>type"`
	Enums      []xmlEnumSet   `xml:"enums"`
	Commands   []xmlCommand   `xml:"commands>command"`
	Features   []xmlFeature   `xml:"feature"`
	Extensions []xmlExtension `xml:"extensions>extension"`
}

type xmlType struct {
	Name     string `xml:"name,attr"`
	Api      string `xml:"api,attr"`
	Requires string `xml:"requires,attr"`
	Raw      []byte `xml:",innerxml"`
}

type xmlEnumSet struct {
	Enums []xmlEnum `xml:"enum"`
}

type xmlEnum struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
	Api   string `xml:"api,attr"`
}

type xmlCommand struct {
	Prototype xmlProto   `xml:"proto"`
	Api       string     `xml:"api"`
	Params    []xmlParam `xml:"param"`
}

type xmlSignature []byte

type xmlProto struct {
	Raw xmlSignature `xml:",innerxml"`
}

type xmlParam struct {
	Raw xmlSignature `xml:",innerxml"`
}

type xmlFeature struct {
	Api      string       `xml:"api,attr"`
	Number   string       `xml:"number,attr"`
	Requires []xmlRequire `xml:"require"`
	Removes  []xmlRemove  `xml:"remove"`
}

type xmlRequire struct {
	Enums    []xmlEnumRef    `xml:"enum"`
	Commands []xmlCommandRef `xml:"command"`
}

type xmlRemove struct {
	Enums    []xmlEnumRef    `xml:"enum"`
	Commands []xmlCommandRef `xml:"command"`
}

type xmlEnumRef struct {
	Name string `xml:"name,attr"`
}

type xmlCommandRef struct {
	Name string `xml:"name,attr"`
}

type xmlExtension struct {
	Name      string       `xml:"name,attr"`
	Supported string       `xml:"supported,attr"`
	Requires  []xmlRequire `xml:"require"`
	Removes   []xmlRemove  `xml:"remove"`
}

type specRef struct {
	name string
	api  string
}

type specTypedef struct {
	typedef  *Typedef
	ordinal  int    // Relative declaration order of the typedef
	requires string // Optional name of the typedef required for this typedef
}

type specFunctions map[specRef]*Function
type specEnums map[specRef]*Enum
type specTypedefs map[specRef]*specTypedef

type specAddRemSet struct {
	addedCommands   []string
	addedEnums      []string
	removedCommands []string
	removedEnums    []string
}

// A Specification is a parsed version of an XML registry.
type Specification struct {
	Functions  specFunctions
	Enums      specEnums
	Typedefs   specTypedefs
	Features   []SpecificationFeature
	Extensions []SpecificationExtension
}

type SpecificationFeature struct {
	Api     string
	Version Version
	AddRem  specAddRemSet
}

type SpecificationExtension struct {
	Name       string
	ApisRegexp string
	AddRem     specAddRemSet
}

func readSpecFile(file string) (*xmlRegistry, error) {
	var registry xmlRegistry

	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	err = xml.NewDecoder(f).Decode(&registry)
	if err != nil {
		return nil, err
	}

	return &registry, nil
}

func parseFunctions(commands []xmlCommand) (specFunctions, error) {
	functions := make(specFunctions)
	for _, cmd := range commands {
		cmdName, cmdReturnType, err := parseSignature(cmd.Prototype.Raw)
		if err != nil {
			return functions, err
		}

		parameters := make([]Parameter, 0, len(cmd.Params))
		for _, param := range cmd.Params {
			paramName, paramType, err := parseSignature(param.Raw)
			if err != nil {
				return functions, err
			}
			parameter := Parameter{
				Name: paramName,
				Type: paramType}
			parameters = append(parameters, parameter)
		}

		fnRef := specRef{cmdName, cmd.Api}
		functions[fnRef] = &Function{
			Name:       cmdName,
			GoName:     TrimApiPrefix(cmdName),
			Parameters: parameters,
			Return:     cmdReturnType}
	}
	return functions, nil
}

func parseSignature(signature xmlSignature) (string, Type, error) {
	name := ""
	ctype := Type{}

	readingName := false
	readingType := false

	decoder := xml.NewDecoder(bytes.NewBuffer(signature))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return name, ctype, err
		}
		switch t := token.(type) {
		case xml.CharData:
			raw := strings.Trim(string(t), " ")
			if readingName {
				name = raw
			} else if readingType {
				ctype.Name = raw
			} else {
				if strings.Contains(raw, "void") {
					ctype.Name = "void"
				}
				ctype.PointerLevel += strings.Count(raw, "*")
			}
			if !readingName {
				ctype.CDefinition += string(t)
			}
		case xml.StartElement:
			if t.Name.Local == "ptype" {
				readingType = true
			} else if t.Name.Local == "name" {
				readingName = true
			} else {
				return name, ctype, fmt.Errorf("Unexpected signature XML: %s", signature)
			}
		case xml.EndElement:
			if t.Name.Local == "ptype" {
				readingType = false
			} else if t.Name.Local == "name" {
				readingName = false
			}
		}
	}
	return name, ctype, nil
}

func parseEnums(enumSets []xmlEnumSet) (specEnums, error) {
	enums := make(specEnums)
	for _, set := range enumSets {
		for _, enum := range set.Enums {
			enumRef := specRef{enum.Name, enum.Api}
			enums[enumRef] = &Enum{
				Name:   enum.Name,
				GoName: TrimApiPrefix(enum.Name),
				Value:  enum.Value}
		}
	}
	return enums, nil
}

func parseTypedefs(types []xmlType) (specTypedefs, error) {
	typedefs := make(specTypedefs)
	for i, xtype := range types {
		typedef, err := parseTypedef(xtype)
		if err != nil {
			return nil, err
		}
		typedefRef := specRef{typedef.Name, xtype.Api}
		typedefs[typedefRef] = &specTypedef{
			typedef:  typedef,
			ordinal:  i,
			requires: xtype.Requires}
	}
	return typedefs, nil
}

func parseTypedef(xmlType xmlType) (*Typedef, error) {
	typedef := &Typedef{
		Name:        xmlType.Name,
		CDefinition: ""}

	readingName := false
	decoder := xml.NewDecoder(bytes.NewBuffer(xmlType.Raw))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return typedef, err
		}
		switch t := token.(type) {
		case xml.CharData:
			raw := string(t)
			typedef.CDefinition += raw
			if readingName {
				typedef.Name = raw
			}
		case xml.StartElement:
			if t.Name.Local == "name" {
				readingName = true
			} else if t.Name.Local == "apientry" {
				typedef.CDefinition += "APIENTRY"
			} else {
				return typedef, fmt.Errorf("Unexpected typedef XML: %s", xmlType.Raw)
			}
		case xml.EndElement:
			if t.Name.Local == "name" {
				readingName = false
			}
		default:
			return typedef, fmt.Errorf("Unexpected typedef XML: %s", xmlType.Raw)
		}
	}

	return typedef, nil
}

func parseFeatures(xmlFeatures []xmlFeature) ([]SpecificationFeature, error) {
	features := make([]SpecificationFeature, 0, len(xmlFeatures))
	for _, xmlFeature := range xmlFeatures {
		version, err := ParseVersion(xmlFeature.Number)
		if err != nil {
			return features, err
		}
		feature := SpecificationFeature{
			Api:     xmlFeature.Api,
			Version: version,
			AddRem:  parseAddRem(xmlFeature.Requires, xmlFeature.Removes),
		}
		features = append(features, feature)
	}
	return features, nil
}

func parseAddRem(requires []xmlRequire, removes []xmlRemove) specAddRemSet {
	addRem := specAddRemSet{
		addedEnums:      make([]string, 0),
		addedCommands:   make([]string, 0),
		removedEnums:    make([]string, 0),
		removedCommands: make([]string, 0),
	}
	for _, req := range requires {
		for _, cmd := range req.Commands {
			addRem.addedCommands = append(addRem.addedCommands, cmd.Name)
		}
		for _, enum := range req.Enums {
			addRem.addedEnums = append(addRem.addedEnums, enum.Name)
		}
	}
	for _, rem := range removes {
		for _, cmd := range rem.Commands {
			addRem.removedCommands = append(addRem.removedCommands, cmd.Name)
		}
		for _, enum := range rem.Enums {
			addRem.removedEnums = append(addRem.removedEnums, enum.Name)
		}
	}
	return addRem
}

func parseExtensions(xmlExtensions []xmlExtension) ([]SpecificationExtension, error) {
	extensions := make([]SpecificationExtension, 0, len(xmlExtensions))
	for _, xmlExtension := range xmlExtensions {
		if len(xmlExtension.Removes) > 0 {
			return nil, fmt.Errorf("Unexpected extension with removal requirement: %s", xmlExtension)
		}
		extension := SpecificationExtension{
			Name:       xmlExtension.Name,
			ApisRegexp: xmlExtension.Supported,
			AddRem:     parseAddRem(xmlExtension.Requires, xmlExtension.Removes),
		}
		extensions = append(extensions, extension)
	}
	return extensions, nil
}

func (functions specFunctions) get(name, api string) *Function {
	function, ok := functions[specRef{name, api}]
	if ok {
		return function
	}
	return functions[specRef{name, ""}]
}

func (enums specEnums) get(name, api string) *Enum {
	enum, ok := enums[specRef{name, api}]
	if ok {
		return enum
	}
	return enums[specRef{name, ""}]
}

func (typedefs specTypedefs) selectRequired(name, api string, requiredTypedefs []*Typedef) {
	specTypedef, ok := typedefs[specRef{name, api}]
	if !ok {
		specTypedef = typedefs[specRef{name, ""}]
	}
	if specTypedef != nil {
		requiredTypedefs[specTypedef.ordinal] = specTypedef.typedef
		if specTypedef.requires != "" {
			typedefs.selectRequired(specTypedef.requires, api, requiredTypedefs)
		}
	}
}

// NewSpecification creates a new specification based on an XML file.
func NewSpecification(file string) (*Specification, error) {
	registry, err := readSpecFile(file)
	if err != nil {
		return nil, err
	}

	functions, err := parseFunctions(registry.Commands)
	if err != nil {
		return nil, err
	}

	enums, err := parseEnums(registry.Enums)
	if err != nil {
		return nil, err
	}

	typedefs, err := parseTypedefs(registry.Types)
	if err != nil {
		return nil, err
	}

	features, err := parseFeatures(registry.Features)
	if err != nil {
		return nil, err
	}

	extensions, err := parseExtensions(registry.Extensions)
	if err != nil {
		return nil, err
	}

	spec := &Specification{
		Functions:  functions,
		Enums:      enums,
		Typedefs:   typedefs,
		Features:   features,
		Extensions: extensions,
	}
	return spec, nil
}

// HasPackage determines whether the specification can generate the specified package.
func (spec *Specification) HasPackage(pkgSpec PackageSpec) bool {
	for _, feature := range spec.Features {
		if pkgSpec.Api == feature.Api && pkgSpec.Version.Compare(feature.Version) == 0 {
			return true
		}
	}
	return false
}

// ToPackage generates a package from the specification.
func (spec *Specification) ToPackage(pkgSpec PackageSpec) *Package {
	pkg := &Package{
		Api:       pkgSpec.Api,
		Name:      pkgSpec.Api,
		Version:   pkgSpec.Version,
		Typedefs:  make([]*Typedef, len(spec.Typedefs)),
		Enums:     make(map[string]Enum),
		Functions: make(map[string]PackageFunction),
	}

	// Select the commands and enums relevant to the specified API version
	for _, feature := range spec.Features {
		// Skip features from a different API or future version
		if pkg.Api != feature.Api || pkg.Version.Compare(feature.Version) < 0 {
			continue
		}
		for _, cmd := range feature.AddRem.addedCommands {
			pkg.Functions[cmd] = PackageFunction{
				Function:   *spec.Functions.get(cmd, pkg.Api),
				Required:   true,
				Extensions: make([]string, 0),
			}
		}
		for _, enum := range feature.AddRem.addedEnums {
			pkg.Enums[enum] = *spec.Enums.get(enum, pkg.Api)
		}
		for _, cmd := range feature.AddRem.removedCommands {
			delete(pkg.Functions, cmd)
		}
		for _, enum := range feature.AddRem.removedEnums {
			delete(pkg.Enums, enum)
		}

	}

	// Select the extensions compatible with the specified API version
	for _, extension := range spec.Extensions {
		// Whitelist a test extension while working out typing issues
		// TODO Lift this restriction
		if extension.Name != "GL_ARB_compute_shader" && extension.Name != "GL_ARB_vertex_buffer_object" {
			continue
		}
		matched, err := regexp.MatchString(extension.ApisRegexp, pkg.Api)
		if !matched || err != nil {
			continue
		}
		for _, cmd := range extension.AddRem.addedCommands {
			fn, ok := pkg.Functions[cmd]
			if ok {
				fn.Extensions = append(fn.Extensions, extension.Name)
			} else {
				pkg.Functions[cmd] = PackageFunction{
					Function:   *spec.Functions.get(cmd, pkg.Api),
					Required:   false,
					Extensions: []string{TrimApiPrefix(extension.Name)},
				}
			}
		}
		for _, enum := range extension.AddRem.addedEnums {
			pkg.Enums[enum] = *spec.Enums.get(enum, pkg.Api)
		}
	}

	// Add the types necessary to declare the functions
	for _, fn := range pkg.Functions {
		spec.Typedefs.selectRequired(fn.Function.Return.Name, pkg.Api, pkg.Typedefs)
		for _, param := range fn.Function.Parameters {
			spec.Typedefs.selectRequired(param.Type.Name, pkg.Api, pkg.Typedefs)
		}
	}
	typedefCount := 0
	for _, typedef := range pkg.Typedefs {
		if typedef != nil {
			pkg.Typedefs[typedefCount] = typedef
			typedefCount++
		}
	}
	pkg.Typedefs = pkg.Typedefs[:typedefCount]

	return pkg
}
