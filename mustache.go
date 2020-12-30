//-----------------------------------------------------------------------------
// Copyright (c) 2020 Detlef Stern
//
// This file is part of zettelstore.
//
// Zettelstore is licensed under the latest version of the EUPL (European Union
// Public License). Please see file LICENSE.txt for your rights and obligations
// under this license.
//
// This file was derived from previous work:
// - https://github.com/hoisie/mustache (License: MIT)
//   Copyright (c) 2009 Michael Hoisie
// - https://github.com/cbroglie/mustache (a fork from above code)
//   Starting with commit [f9b4cbf]
//   Does not have an explicit copyright and obviously continues with
//   above MIT license.
// The license text is included in the same directory where this file is
// located. See file LICENSE.
//-----------------------------------------------------------------------------

package template

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// A TagType represents the specific type of mustache tag that a Tag
// represents. The zero TagType is not a valid type.
type TagType uint

// Defines representing the possible Tag types
const (
	Invalid TagType = iota
	Variable
	Section
	InvertedSection
	Partial
)

// Skip all whitespaces apeared after these types of tags until end of line if
// the line only contains a tag and whitespaces.
const (
	SkipWhitespaceTagTypes = "#^/<>=!"
)

func (t TagType) String() string {
	if int(t) < len(tagNames) {
		return tagNames[t]
	}
	return "type" + strconv.Itoa(int(t))
}

var tagNames = []string{
	Invalid:         "Invalid",
	Variable:        "Variable",
	Section:         "Section",
	InvertedSection: "InvertedSection",
	Partial:         "Partial",
}

// Tag represents the different mustache tag types.
//
// Not all methods apply to all kinds of tags. Restrictions, if any, are noted
// in the documentation for each method. Use the Type method to find out the
// type of tag before calling type-specific methods. Calling a method
// inappropriate to the type of tag causes a run time panic.
type Tag interface {
	// Type returns the type of the tag.
	Type() TagType
	// Name returns the name of the tag.
	Name() string
	// Tags returns any child tags. It panics for tag types which cannot contain
	// child tags (i.e. variable tags).
	Tags() []Tag
}

type textElement struct {
	text []byte
}

type varElement struct {
	name string
	raw  bool
}

type sectionElement struct {
	name      string
	inverted  bool
	startline int
	elems     []interface{}
}

type partialElement struct {
	name   string
	indent string
	prov   PartialProvider
}

// Template represents a compiled mustache template
type Template struct {
	data    string
	otag    string
	ctag    string
	p       int
	curline int
	elems   []interface{}
	partial PartialProvider
	errmiss bool // Error when variable is not found?
}

type parseError struct {
	line    int
	message string
}

// Tags returns the mustache tags for the given template
func (tmpl *Template) Tags() []Tag {
	return extractTags(tmpl.elems)
}

func extractTags(elems []interface{}) []Tag {
	tags := make([]Tag, 0, len(elems))
	for _, elem := range elems {
		switch elem := elem.(type) {
		case *varElement:
			tags = append(tags, elem)
		case *sectionElement:
			tags = append(tags, elem)
		case *partialElement:
			tags = append(tags, elem)
		}
	}
	return tags
}

func (e *varElement) Type() TagType {
	return Variable
}

func (e *varElement) Name() string {
	return e.name
}

func (e *varElement) Tags() []Tag {
	panic("mustache: Tags on Variable type")
}

func (e *sectionElement) Type() TagType {
	if e.inverted {
		return InvertedSection
	}
	return Section
}

func (e *sectionElement) Name() string {
	return e.name
}

func (e *sectionElement) Tags() []Tag {
	return extractTags(e.elems)
}

func (e *partialElement) Type() TagType {
	return Partial
}

func (e *partialElement) Name() string {
	return e.name
}

func (e *partialElement) Tags() []Tag {
	return nil
}

func (p parseError) Error() string {
	return fmt.Sprintf("line %d: %s", p.line, p.message)
}

func (tmpl *Template) readString(s string) (string, error) {
	newlines := 0
	for i := tmpl.p; ; i++ {
		//are we at the end of the string?
		if i+len(s) > len(tmpl.data) {
			return tmpl.data[tmpl.p:], io.EOF
		}

		if tmpl.data[i] == '\n' {
			newlines++
		}

		if tmpl.data[i] != s[0] {
			continue
		}

		match := true
		for j := 1; j < len(s); j++ {
			if s[j] != tmpl.data[i+j] {
				match = false
				break
			}
		}

		if match {
			e := i + len(s)
			text := tmpl.data[tmpl.p:e]
			tmpl.p = e

			tmpl.curline += newlines
			return text, nil
		}
	}
}

type textReadingResult struct {
	text          string
	padding       string
	mayStandalone bool
}

func (tmpl *Template) readText() (*textReadingResult, error) {
	pPrev := tmpl.p
	text, err := tmpl.readString(tmpl.otag)
	if err == io.EOF {
		return &textReadingResult{
			text:          text,
			padding:       "",
			mayStandalone: false,
		}, err
	}

	var i int
	for i = tmpl.p - len(tmpl.otag); i > pPrev; i-- {
		if tmpl.data[i-1] != ' ' && tmpl.data[i-1] != '\t' {
			break
		}
	}

	mayStandalone := (i == 0 || tmpl.data[i-1] == '\n')

	if mayStandalone {
		return &textReadingResult{
			text:          tmpl.data[pPrev:i],
			padding:       tmpl.data[i : tmpl.p-len(tmpl.otag)],
			mayStandalone: true,
		}, nil
	}

	return &textReadingResult{
		text:          tmpl.data[pPrev : tmpl.p-len(tmpl.otag)],
		padding:       "",
		mayStandalone: false,
	}, nil
}

type tagReadingResult struct {
	tag        string
	standalone bool
}

func (tmpl *Template) readTag(mayStandalone bool) (*tagReadingResult, error) {
	var text string
	var err error
	if tmpl.p < len(tmpl.data) && tmpl.data[tmpl.p] == '{' {
		text, err = tmpl.readString("}" + tmpl.ctag)
	} else {
		text, err = tmpl.readString(tmpl.ctag)
	}

	if err == io.EOF {
		//put the remaining text in a block
		return nil, parseError{tmpl.curline, "unmatched open tag"}
	}

	text = text[:len(text)-len(tmpl.ctag)]

	//trim the close tag off the text
	tag := strings.TrimSpace(text)
	if len(tag) == 0 {
		return nil, parseError{tmpl.curline, "empty tag"}
	}

	eow := tmpl.p
	for i := tmpl.p; i < len(tmpl.data); i++ {
		if !(tmpl.data[i] == ' ' || tmpl.data[i] == '\t') {
			eow = i
			break
		}
	}

	standalone := true
	if mayStandalone {
		if !strings.Contains(SkipWhitespaceTagTypes, tag[0:1]) {
			standalone = false
		} else {
			if eow == len(tmpl.data) {
				standalone = true
				tmpl.p = eow
			} else if eow < len(tmpl.data) && tmpl.data[eow] == '\n' {
				standalone = true
				tmpl.p = eow + 1
				tmpl.curline++
			} else if eow+1 < len(tmpl.data) && tmpl.data[eow] == '\r' && tmpl.data[eow+1] == '\n' {
				standalone = true
				tmpl.p = eow + 2
				tmpl.curline++
			} else {
				standalone = false
			}
		}
	}

	return &tagReadingResult{
		tag:        tag,
		standalone: standalone,
	}, nil
}

func (tmpl *Template) parsePartial(name, indent string) (*partialElement, error) {
	return &partialElement{
		name:   name,
		indent: indent,
		prov:   tmpl.partial,
	}, nil
}

func (tmpl *Template) parseSection(section *sectionElement) error {
	for {
		textResult, err := tmpl.readText()
		text := textResult.text
		padding := textResult.padding
		mayStandalone := textResult.mayStandalone

		if err == io.EOF {
			//put the remaining text in a block
			return parseError{section.startline, "Section " + section.name + " has no closing tag"}
		}

		// put text into an item
		section.elems = append(section.elems, &textElement{[]byte(text)})

		tagResult, err := tmpl.readTag(mayStandalone)
		if err != nil {
			return err
		}

		if !tagResult.standalone {
			section.elems = append(section.elems, &textElement{[]byte(padding)})
		}

		tag := tagResult.tag
		switch tag[0] {
		case '!':
			//ignore comment
			break
		case '#', '^':
			name := strings.TrimSpace(tag[1:])
			se := sectionElement{name, tag[0] == '^', tmpl.curline, []interface{}{}}
			err := tmpl.parseSection(&se)
			if err != nil {
				return err
			}
			section.elems = append(section.elems, &se)
		case '/':
			name := strings.TrimSpace(tag[1:])
			if name != section.name {
				return parseError{tmpl.curline, "interleaved closing tag: " + name}
			}
			return nil
		case '>':
			name := strings.TrimSpace(tag[1:])
			partial, err := tmpl.parsePartial(name, textResult.padding)
			if err != nil {
				return err
			}
			section.elems = append(section.elems, partial)
		case '=':
			if tag[len(tag)-1] != '=' {
				return parseError{tmpl.curline, "Invalid meta tag"}
			}
			tag = strings.TrimSpace(tag[1 : len(tag)-1])
			newtags := strings.SplitN(tag, " ", 2)
			if len(newtags) == 2 {
				tmpl.otag = newtags[0]
				tmpl.ctag = newtags[1]
			}
		case '{':
			if tag[len(tag)-1] == '}' {
				//use a raw tag
				name := strings.TrimSpace(tag[1 : len(tag)-1])
				section.elems = append(section.elems, &varElement{name, true})
			}
		case '&':
			name := strings.TrimSpace(tag[1:])
			section.elems = append(section.elems, &varElement{name, true})
		default:
			section.elems = append(section.elems, &varElement{tag, false})
		}
	}
}

func (tmpl *Template) parse() error {
	for {
		textResult, err := tmpl.readText()
		text := textResult.text
		padding := textResult.padding
		mayStandalone := textResult.mayStandalone

		if err == io.EOF {
			//put the remaining text in a block
			tmpl.elems = append(tmpl.elems, &textElement{[]byte(text)})
			return nil
		}

		// put text into an item
		tmpl.elems = append(tmpl.elems, &textElement{[]byte(text)})

		tagResult, err := tmpl.readTag(mayStandalone)
		if err != nil {
			return err
		}

		if !tagResult.standalone {
			tmpl.elems = append(tmpl.elems, &textElement{[]byte(padding)})
		}

		tag := tagResult.tag
		switch tag[0] {
		case '!':
			//ignore comment
			break
		case '#', '^':
			name := strings.TrimSpace(tag[1:])
			se := sectionElement{name, tag[0] == '^', tmpl.curline, []interface{}{}}
			err := tmpl.parseSection(&se)
			if err != nil {
				return err
			}
			tmpl.elems = append(tmpl.elems, &se)
		case '/':
			return parseError{tmpl.curline, "unmatched close tag"}
		case '>':
			name := strings.TrimSpace(tag[1:])
			partial, err := tmpl.parsePartial(name, textResult.padding)
			if err != nil {
				return err
			}
			tmpl.elems = append(tmpl.elems, partial)
		case '=':
			if tag[len(tag)-1] != '=' {
				return parseError{tmpl.curline, "Invalid meta tag"}
			}
			tag = strings.TrimSpace(tag[1 : len(tag)-1])
			newtags := strings.SplitN(tag, " ", 2)
			if len(newtags) == 2 {
				tmpl.otag = newtags[0]
				tmpl.ctag = newtags[1]
			}
		case '{':
			//use a raw tag
			if tag[len(tag)-1] == '}' {
				name := strings.TrimSpace(tag[1 : len(tag)-1])
				tmpl.elems = append(tmpl.elems, &varElement{name, true})
			}
		case '&':
			name := strings.TrimSpace(tag[1:])
			tmpl.elems = append(tmpl.elems, &varElement{name, true})
		default:
			tmpl.elems = append(tmpl.elems, &varElement{tag, false})
		}
	}
}

// Evaluate interfaces and pointers looking for a value that can look up the
// name, via a struct field, method, or map key, and return the result of the
// lookup.
func lookup(contextChain []interface{}, name string, errMissing bool) (reflect.Value, error) {
	// dot notation
	if name != "." && strings.Contains(name, ".") {
		parts := strings.SplitN(name, ".", 2)

		v, err := lookup(contextChain, parts[0], errMissing)
		if err != nil {
			return v, err
		}
		return lookup([]interface{}{v}, parts[1], errMissing)
	}

Outer:
	for _, ctx := range contextChain {
		v := ctx.(reflect.Value)
		for v.IsValid() {
			typ := v.Type()
			if n := v.Type().NumMethod(); n > 0 {
				for i := 0; i < n; i++ {
					m := typ.Method(i)
					mtyp := m.Type
					if m.Name == name && mtyp.NumIn() == 1 {
						return v.Method(i).Call(nil)[0], nil
					}
				}
			}
			if name == "." {
				return v, nil
			}
			switch av := v; av.Kind() {
			case reflect.Ptr:
				v = av.Elem()
			case reflect.Interface:
				v = av.Elem()
			case reflect.Struct:
				ret := av.FieldByName(name)
				if ret.IsValid() {
					return ret, nil
				}
				continue Outer
			case reflect.Map:
				ret := av.MapIndex(reflect.ValueOf(name))
				if ret.IsValid() {
					return ret, nil
				}
				continue Outer
			default:
				continue Outer
			}
		}
	}
	if errMissing {
		return reflect.Value{}, fmt.Errorf("Missing variable %q", name)
	}
	return reflect.Value{}, nil
}

func isEmpty(v reflect.Value) bool {
	if !v.IsValid() || v.Interface() == nil {
		return true
	}

	valueInd := indirect(v)
	if !valueInd.IsValid() {
		return true
	}
	switch val := valueInd; val.Kind() {
	case reflect.Array, reflect.Slice:
		return val.Len() == 0
	case reflect.String:
		return len(strings.TrimSpace(val.String())) == 0
	default:
		return valueInd.IsZero()
	}
}

func indirect(v reflect.Value) reflect.Value {
loop:
	for v.IsValid() {
		switch av := v; av.Kind() {
		case reflect.Ptr:
			v = av.Elem()
		case reflect.Interface:
			v = av.Elem()
		default:
			break loop
		}
	}
	return v
}

func (tmpl *Template) renderSection(section *sectionElement, contextChain []interface{}, buf io.Writer) error {
	value, err := lookup(contextChain, section.name, false)
	if err != nil {
		return err
	}
	var context = contextChain[len(contextChain)-1].(reflect.Value)
	var contexts = []interface{}{}
	// if the value is nil, check if it's an inverted section
	isEmpty := isEmpty(value)
	if isEmpty && !section.inverted || !isEmpty && section.inverted {
		return nil
	} else if !section.inverted {
		valueInd := indirect(value)
		switch val := valueInd; val.Kind() {
		case reflect.Slice:
			for i := 0; i < val.Len(); i++ {
				contexts = append(contexts, val.Index(i))
			}
		case reflect.Array:
			for i := 0; i < val.Len(); i++ {
				contexts = append(contexts, val.Index(i))
			}
		case reflect.Map, reflect.Struct:
			contexts = append(contexts, value)
		default:
			contexts = append(contexts, context)
		}
	} else if section.inverted {
		contexts = append(contexts, context)
	}

	chain2 := make([]interface{}, len(contextChain)+1)
	copy(chain2[1:], contextChain)
	//by default we execute the section
	for _, ctx := range contexts {
		chain2[0] = ctx
		for _, elem := range section.elems {
			if err := tmpl.renderElement(elem, chain2, buf); err != nil {
				return err
			}
		}
	}
	return nil
}

func (tmpl *Template) renderElement(element interface{}, contextChain []interface{}, buf io.Writer) error {
	switch elem := element.(type) {
	case *textElement:
		_, err := buf.Write(elem.text)
		return err
	case *varElement:
		val, err := lookup(contextChain, elem.name, tmpl.errmiss)
		if err != nil {
			return err
		}
		if val.IsValid() {
			if elem.raw {
				fmt.Fprint(buf, val.Interface())
			} else {
				s := fmt.Sprint(val.Interface())
				template.HTMLEscape(buf, []byte(s))
			}
		}
	case *sectionElement:
		if err := tmpl.renderSection(elem, contextChain, buf); err != nil {
			return err
		}
	case *partialElement:
		partial, err := getPartials(elem.prov, elem.name, elem.indent)
		if err != nil {
			return err
		}
		if err := partial.renderTemplate(contextChain, buf); err != nil {
			return err
		}
	}
	return nil
}

func (tmpl *Template) renderTemplate(contextChain []interface{}, buf io.Writer) error {
	for _, elem := range tmpl.elems {
		if err := tmpl.renderElement(elem, contextChain, buf); err != nil {
			return err
		}
	}
	return nil
}

// FRender uses the given data source - generally a map or struct - to render
// the compiled template to an io.Writer.
func (tmpl *Template) FRender(out io.Writer, context ...interface{}) error {
	var contextChain []interface{}
	for _, c := range context {
		val := reflect.ValueOf(c)
		contextChain = append(contextChain, val)
	}
	return tmpl.renderTemplate(contextChain, out)
}

// Render uses the given data source - generally a map or struct - to render
// the compiled template and return the output.
func (tmpl *Template) Render(context ...interface{}) (string, error) {
	var buf bytes.Buffer
	err := tmpl.FRender(&buf, context...)
	return buf.String(), err
}

// RenderInLayout uses the given data source - generally a map or struct - to
// render the compiled template and layout "wrapper" template and return the
// output.
func (tmpl *Template) RenderInLayout(layout *Template, context ...interface{}) (string, error) {
	var buf bytes.Buffer
	err := tmpl.FRenderInLayout(&buf, layout, context...)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

// FRenderInLayout uses the given data source - generally a map or struct - to
// render the compiled templated a loayout "wrapper" template to an io.Writer.
func (tmpl *Template) FRenderInLayout(out io.Writer, layout *Template, context ...interface{}) error {
	content, err := tmpl.Render(context...)
	if err != nil {
		return err
	}
	allContext := make([]interface{}, len(context)+1)
	copy(allContext[1:], context)
	allContext[0] = map[string]string{"content": content}
	return layout.FRender(out, allContext...)
}

// ParseString compiles a mustache template string. The resulting output can
// be used to efficiently render the template multiple times with different
// data sources.
func ParseString(data string) (*Template, error) {
	return ParseStringPartials(data, nil)
}

// ParseStringPartials compiles a mustache template string, retrieving any
// required partials from the given provider. The resulting output can be used
// to efficiently render the template multiple times with different data
// sources.
func ParseStringPartials(data string, partials PartialProvider) (*Template, error) {
	if partials == nil {
		partials = &EmptyProvider
	}
	tmpl := Template{data, "{{", "}}", 0, 1, []interface{}{}, partials, false}
	err := tmpl.parse()
	if err != nil {
		return nil, err
	}
	return &tmpl, err
}

// SetErrorOnMissing will produce an error is a variable is not found.
func (tmpl *Template) SetErrorOnMissing() { tmpl.errmiss = true }

// PartialProvider comprises the behaviors required of a struct to be able to
// provide partials to the mustache rendering engine.
type PartialProvider interface {
	// Get accepts the name of a partial and returns the parsed partial, if it
	// could be found; a valid but empty template, if it could not be found; or
	// nil and error if an error occurred (other than an inability to find the
	// partial).
	Get(name string) (string, error)
}

// ErrPartialNotFound is returned if a partial was not found.
type ErrPartialNotFound struct {
	Name string
}

func (err *ErrPartialNotFound) Error() string {
	return "Partial '" + err.Name + "' not found"
}

// StaticProvider implements the PartialProvider interface by providing
// partials drawn from a map, which maps partial name to template contents.
type StaticProvider struct {
	Partials map[string]string
}

// Get accepts the name of a partial and returns the parsed partial.
func (sp *StaticProvider) Get(name string) (string, error) {
	if sp.Partials != nil {
		if data, ok := sp.Partials[name]; ok {
			return data, nil
		}
	}

	return "", &ErrPartialNotFound{name}
}

// emptyProvider will always returns an empty string.
type emptyProvider struct{}

// Get accepts the name of a partial and returns the parsed partial.
func (ep *emptyProvider) Get(name string) (string, error) { return "", nil }

// EmptyProvider is a partial provider that will always return an empty string.
var EmptyProvider emptyProvider

var nonEmptyLine = regexp.MustCompile(`(?m:^(.+)$)`)

func getPartials(partials PartialProvider, name, indent string) (*Template, error) {
	data, err := partials.Get(name)
	if err != nil {
		return nil, err
	}

	// indent non empty lines
	data = nonEmptyLine.ReplaceAllString(data, indent+"$1")
	return ParseStringPartials(data, partials)
}
