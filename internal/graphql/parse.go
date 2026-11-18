// Package graphql is the forge's GraphQL API at POST /api/graphql: a small
// hand-rolled executor (no external dependency, so the binary stays small)
// for the documents the suite sends — repository, issue and pull-request
// queries and mutations, Relay global ids, cursor pagination, introspection
// and operation batching. Unknown fields are reported the way GraphQL
// requires: HTTP 200 with an errors array naming the field.
package graphql

import (
	"fmt"
	"strconv"
	"strings"
)

// value is one parsed GraphQL value literal.
type value struct {
	kind string // "null", "bool", "int", "float", "string", "enum", "var", "list", "object"
	b    bool
	i    int64
	f    float64
	s    string
	list []*value
	obj  map[string]*value
}

// selection is one field, fragment spread or inline fragment in a selection
// set.
type selection struct {
	alias      string
	name       string // the field name; "" for a fragment
	args       map[string]*value
	children   []*selection
	typeCond   string // inline fragment's type condition, "" when none
	fragName   string // fragment spread's name, "" when not a spread
	directives []directive
}

type directive struct {
	name string
	args map[string]*value
}

// operation is one query or mutation in a document.
type operation struct {
	typ      string // "query" or "mutation"
	name     string
	varDefs  map[string]*value // variable name -> default value (nil when none)
	varTypes map[string]string
	sels     []*selection
}

// document is a parsed GraphQL document: its operations and its named
// fragments.
type document struct {
	ops   []*operation
	frags map[string]*selection // fragment name -> the fragment's selection set root
}

// token is one lexical unit of a GraphQL document.
type token struct {
	kind string // "name", "int", "float", "string", "punct", "eof"
	text string
}

func lex(src string) ([]token, error) {
	var out []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',':
			i++
		case c == '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '{' || c == '}' || c == '(' || c == ')' || c == '[' || c == ']' ||
			c == ':' || c == '!' || c == '=' || c == '$' || c == '@' || c == '|' || c == '&':
			out = append(out, token{kind: "punct", text: string(c)})
			i++
		case c == '.' && i+2 < len(src) && src[i+1] == '.' && src[i+2] == '.':
			out = append(out, token{kind: "punct", text: "..."})
			i += 3
		case c == '"':
			if i+2 < len(src) && src[i+1] == '"' && src[i+2] == '"' {
				end := strings.Index(src[i+3:], `"""`)
				if end < 0 {
					return nil, fmt.Errorf("graphql: unterminated block string")
				}
				out = append(out, token{kind: "string", text: src[i+3 : i+3+end]})
				i += 3 + end + 3
			} else {
				j := i + 1
				var sb strings.Builder
				for j < len(src) && src[j] != '"' {
					if src[j] == '\\' && j+1 < len(src) {
						j++
						switch src[j] {
						case 'n':
							sb.WriteByte('\n')
						case 't':
							sb.WriteByte('\t')
						case 'r':
							sb.WriteByte('\r')
						case '"':
							sb.WriteByte('"')
						case '\\':
							sb.WriteByte('\\')
						case '/':
							sb.WriteByte('/')
						default:
							sb.WriteByte(src[j])
						}
						j++
						continue
					}
					sb.WriteByte(src[j])
					j++
				}
				if j >= len(src) {
					return nil, fmt.Errorf("graphql: unterminated string")
				}
				out = append(out, token{kind: "string", text: sb.String()})
				i = j + 1
			}
		case c == '-' || (c >= '0' && c <= '9'):
			j := i
			if src[j] == '-' {
				j++
			}
			for j < len(src) && src[j] >= '0' && src[j] <= '9' {
				j++
			}
			isFloat := false
			if j < len(src) && src[j] == '.' {
				isFloat = true
				j++
				for j < len(src) && src[j] >= '0' && src[j] <= '9' {
					j++
				}
			}
			if j < len(src) && (src[j] == 'e' || src[j] == 'E') {
				isFloat = true
				j++
				if j < len(src) && (src[j] == '+' || src[j] == '-') {
					j++
				}
				for j < len(src) && src[j] >= '0' && src[j] <= '9' {
					j++
				}
			}
			kind := "int"
			if isFloat {
				kind = "float"
			}
			out = append(out, token{kind: kind, text: src[i:j]})
			i = j
		case c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			j := i
			for j < len(src) && (src[j] == '_' || (src[j] >= 'a' && src[j] <= 'z') ||
				(src[j] >= 'A' && src[j] <= 'Z') || (src[j] >= '0' && src[j] <= '9')) {
				j++
			}
			out = append(out, token{kind: "name", text: src[i:j]})
			i = j
		default:
			return nil, fmt.Errorf("graphql: unexpected character %q", c)
		}
	}
	out = append(out, token{kind: "eof"})
	return out, nil
}

// parser is a recursive-descent parser over the lexed tokens.
type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }
func (p *parser) at(text string) bool {
	t := p.peek()
	return t.kind == "punct" && t.text == text
}
func (p *parser) expect(text string) error {
	if !p.at(text) {
		return fmt.Errorf("graphql: expected %q, got %q", text, p.peek().text)
	}
	p.next()
	return nil
}
func (p *parser) expectName() (string, error) {
	t := p.next()
	if t.kind != "name" {
		return "", fmt.Errorf("graphql: expected a name, got %q", t.text)
	}
	return t.text, nil
}

func parseDocument(src string) (*document, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	doc := &document{frags: map[string]*selection{}}
	for p.peek().kind != "eof" {
		if p.at("...") {
			return nil, fmt.Errorf("graphql: a fragment cannot stand alone")
		}
		t := p.peek()
		if t.kind == "name" && t.text == "fragment" {
			if err := p.parseFragment(doc); err != nil {
				return nil, err
			}
			continue
		}
		op, err := p.parseOperation()
		if err != nil {
			return nil, err
		}
		doc.ops = append(doc.ops, op)
	}
	if len(doc.ops) == 0 {
		return nil, fmt.Errorf("graphql: no operation in the document")
	}
	return doc, nil
}

func (p *parser) parseFragment(doc *document) error {
	p.next() // "fragment"
	name, err := p.expectName()
	if err != nil {
		return err
	}
	on, err := p.expectName()
	if err != nil {
		return err
	}
	if on != "on" {
		return fmt.Errorf("graphql: expected \"on\" after a fragment name")
	}
	typeCond, err := p.expectName()
	if err != nil {
		return err
	}
	sels, err := p.parseSelectionSet()
	if err != nil {
		return err
	}
	doc.frags[name] = &selection{typeCond: typeCond, children: sels}
	return nil
}

func (p *parser) parseOperation() (*operation, error) {
	op := &operation{typ: "query", varDefs: map[string]*value{}, varTypes: map[string]string{}}
	t := p.peek()
	if t.kind == "name" && (t.text == "query" || t.text == "mutation" || t.text == "subscription") {
		op.typ = p.next().text
		if p.peek().kind == "name" {
			op.name = p.next().text
		}
	} else if t.kind == "punct" && t.text == "{" {
		op.typ = "query"
	} else {
		return nil, fmt.Errorf("graphql: expected an operation, got %q", t.text)
	}
	if p.at("(") {
		if err := p.parseVarDefs(op); err != nil {
			return nil, err
		}
	}
	sels, err := p.parseSelectionSet()
	if err != nil {
		return nil, err
	}
	op.sels = sels
	return op, nil
}

func (p *parser) parseVarDefs(op *operation) error {
	if err := p.expect("("); err != nil {
		return err
	}
	for !p.at(")") {
		if p.at("$") {
			p.next()
		} else {
			return fmt.Errorf("graphql: expected a variable in the variable definitions")
		}
		name, err := p.expectName()
		if err != nil {
			return err
		}
		if err := p.expect(":"); err != nil {
			return err
		}
		typ, err := p.parseType()
		if err != nil {
			return err
		}
		op.varTypes[name] = typ
		if p.at("=") {
			p.next()
			v, err := p.parseValue()
			if err != nil {
				return err
			}
			op.varDefs[name] = v
		} else {
			op.varDefs[name] = nil
		}
	}
	return p.expect(")")
}

// parseType reads a type reference ("String!", "[Int]", "CreateIssueInput")
// and returns it as text; the executor only needs the name for coercion.
func (p *parser) parseType() (string, error) {
	var sb strings.Builder
	for {
		t := p.peek()
		if t.kind == "name" {
			sb.WriteString(p.next().text)
		} else if t.kind == "punct" && (t.text == "[" || t.text == "]" || t.text == "!") {
			sb.WriteString(p.next().text)
		} else {
			break
		}
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("graphql: expected a type")
	}
	return sb.String(), nil
}

func (p *parser) parseSelectionSet() ([]*selection, error) {
	if err := p.expect("{"); err != nil {
		return nil, err
	}
	var out []*selection
	for !p.at("}") {
		if p.peek().kind == "eof" {
			return nil, fmt.Errorf("graphql: unterminated selection set")
		}
		sel, err := p.parseSelection()
		if err != nil {
			return nil, err
		}
		out = append(out, sel)
	}
	if err := p.expect("}"); err != nil {
		return nil, err
	}
	return out, nil
}

func (p *parser) parseSelection() (*selection, error) {
	if p.at("...") {
		p.next()
		t := p.peek()
		if t.kind == "name" && t.text == "on" {
			p.next()
			typeCond, err := p.expectName()
			if err != nil {
				return nil, err
			}
			sel := &selection{typeCond: typeCond}
			if err := p.parseDirectives(sel); err != nil {
				return nil, err
			}
			children, err := p.parseSelectionSet()
			if err != nil {
				return nil, err
			}
			sel.children = children
			return sel, nil
		}
		name, err := p.expectName()
		if err != nil {
			return nil, err
		}
		sel := &selection{fragName: name}
		if err := p.parseDirectives(sel); err != nil {
			return nil, err
		}
		return sel, nil
	}
	name, err := p.expectName()
	if err != nil {
		return nil, err
	}
	sel := &selection{name: name, alias: name}
	if p.at(":") {
		p.next()
		field, err := p.expectName()
		if err != nil {
			return nil, err
		}
		sel.name = field
	}
	if p.at("(") {
		args, err := p.parseArguments()
		if err != nil {
			return nil, err
		}
		sel.args = args
	}
	if err := p.parseDirectives(sel); err != nil {
		return nil, err
	}
	if p.at("{") {
		children, err := p.parseSelectionSet()
		if err != nil {
			return nil, err
		}
		sel.children = children
	}
	return sel, nil
}

func (p *parser) parseDirectives(sel *selection) error {
	for p.at("@") {
		p.next()
		name, err := p.expectName()
		if err != nil {
			return err
		}
		d := directive{name: name}
		if p.at("(") {
			args, err := p.parseArguments()
			if err != nil {
				return err
			}
			d.args = args
		}
		sel.directives = append(sel.directives, d)
	}
	return nil
}

func (p *parser) parseArguments() (map[string]*value, error) {
	if err := p.expect("("); err != nil {
		return nil, err
	}
	args := map[string]*value{}
	for !p.at(")") {
		name, err := p.expectName()
		if err != nil {
			return nil, err
		}
		if err := p.expect(":"); err != nil {
			return nil, err
		}
		v, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		args[name] = v
	}
	if err := p.expect(")"); err != nil {
		return nil, err
	}
	return args, nil
}

func (p *parser) parseValue() (*value, error) {
	t := p.next()
	switch t.kind {
	case "int":
		n, err := strconv.ParseInt(t.text, 10, 64)
		if err != nil {
			return nil, err
		}
		return &value{kind: "int", i: n}, nil
	case "float":
		f, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			return nil, err
		}
		return &value{kind: "float", f: f}, nil
	case "string":
		return &value{kind: "string", s: t.text}, nil
	case "name":
		switch t.text {
		case "true":
			return &value{kind: "bool", b: true}, nil
		case "false":
			return &value{kind: "bool", b: false}, nil
		case "null":
			return &value{kind: "null"}, nil
		default:
			return &value{kind: "enum", s: t.text}, nil
		}
	case "punct":
		switch t.text {
		case "$":
			name, err := p.expectName()
			if err != nil {
				return nil, err
			}
			return &value{kind: "var", s: name}, nil
		case "[":
			v := &value{kind: "list"}
			for !p.at("]") {
				item, err := p.parseValue()
				if err != nil {
					return nil, err
				}
				v.list = append(v.list, item)
			}
			if err := p.expect("]"); err != nil {
				return nil, err
			}
			return v, nil
		case "{":
			v := &value{kind: "object", obj: map[string]*value{}}
			for !p.at("}") {
				name, err := p.expectName()
				if err != nil {
					return nil, err
				}
				if err := p.expect(":"); err != nil {
					return nil, err
				}
				item, err := p.parseValue()
				if err != nil {
					return nil, err
				}
				v.obj[name] = item
			}
			if err := p.expect("}"); err != nil {
				return nil, err
			}
			return v, nil
		}
	}
	return nil, fmt.Errorf("graphql: unexpected value %q", t.text)
}
