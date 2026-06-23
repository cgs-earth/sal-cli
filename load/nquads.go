package load

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	blankNodeLabelRE = regexp.MustCompile(`^[\p{L}\p{N}_][\p{L}\p{N}\p{M}_\-.]*$`)
	langTagRE        = regexp.MustCompile(`^[A-Za-z]{1,8}(-[A-Za-z0-9]{1,8})*(--(ltr|rtl))?$`)
)

type objectKind int

const (
	objectKindIRI objectKind = iota
	objectKindBNode
	objectKindLiteral
)

type triple struct {
	s, p      string
	o         string
	oKind     objectKind
	oDatatype string
}

func parseNQuads(r io.Reader, handle func(triple) error) error {
	br := bufio.NewReader(r)
	lineNum := 0

	for {
		raw, err := br.ReadString('\n')
		if len(raw) > 0 {
			lineNum++
			line := cleanNQuadLine(raw)
			if line != "" {
				t, parseErr := parseNQuadLine(line)
				if parseErr != nil {
					log.Printf("  skipping line %d: %v", lineNum, parseErr)
				} else if err := handle(t); err != nil {
					return err
				}
			}
		}
		if err == nil {
			continue
		}
		if err == io.EOF {
			return nil
		}
		return fmt.Errorf("read nquads: %w", err)
	}
}

func cleanNQuadLine(raw string) string {
	line := strings.TrimSpace(raw)
	if strings.HasPrefix(line, "#") {
		return ""
	}
	return line
}

type nquadLineParser struct {
	line string
	pos  int
}

func parseNQuadLine(line string) (triple, error) {
	p := &nquadLineParser{line: line}

	subj, err := p.readSubject()
	if err != nil {
		return triple{}, err
	}

	pred, err := p.readPredicate()
	if err != nil {
		return triple{}, err
	}

	obj, err := p.readObject()
	if err != nil {
		return triple{}, err
	}

	if err := p.skipGraphLabel(); err != nil {
		return triple{}, err
	}
	if err := p.finishStatement(); err != nil {
		return triple{}, err
	}

	return triple{s: subj, p: pred, o: obj.value, oKind: obj.kind, oDatatype: obj.datatype}, nil
}

func (p *nquadLineParser) readSubject() (string, error) {
	p.skipSpaces()
	if p.done() {
		return "", fmt.Errorf("unexpected end")
	}
	if p.peek("<") {
		return p.readIRI()
	}
	if p.peek("_:") {
		return p.readBNode()
	}
	return "", fmt.Errorf("expected IRI or blank node for subject")
}

func (p *nquadLineParser) readPredicate() (string, error) {
	p.skipSpaces()
	predicate, err := p.readIRI()
	if err != nil {
		return "", fmt.Errorf("predicate: %w", err)
	}
	return predicate, nil
}

type nquadObject struct {
	value    string
	kind     objectKind
	datatype string
}

func (p *nquadLineParser) readObject() (nquadObject, error) {
	p.skipSpaces()
	if p.done() {
		return nquadObject{}, fmt.Errorf("unexpected end")
	}
	if p.peek("<") {
		iri, err := p.readIRI()
		if err != nil {
			return nquadObject{}, err
		}
		return nquadObject{value: iri, kind: objectKindIRI}, nil
	}
	if p.peek("_:") {
		bnode, err := p.readBNode()
		if err != nil {
			return nquadObject{}, err
		}
		return nquadObject{value: bnode, kind: objectKindBNode}, nil
	}
	if p.peek(`"`) {
		value, datatype, err := p.readLiteral()
		if err != nil {
			return nquadObject{}, err
		}
		return nquadObject{value: value, kind: objectKindLiteral, datatype: datatype}, nil
	}
	return nquadObject{}, fmt.Errorf("expected IRI, blank node, or literal for object")
}

func (p *nquadLineParser) skipGraphLabel() error {
	p.skipSpaces()
	if p.done() || p.peek(".") {
		return nil
	}
	if p.peek("<") {
		_, err := p.readIRI()
		return err
	}
	if p.peek("_:") {
		_, err := p.readBNode()
		return err
	}
	return fmt.Errorf("expected IRI or blank node for graph label")
}

func (p *nquadLineParser) finishStatement() error {
	p.skipSpaces()
	if !p.consume(".") {
		return fmt.Errorf("expected '.'")
	}
	p.skipSpaces()
	if !p.done() && !p.peek("#") {
		return fmt.Errorf("unexpected content after '.'")
	}
	return nil
}

func (p *nquadLineParser) readIRI() (string, error) {
	if !p.consume("<") {
		return "", fmt.Errorf("expected '<'")
	}

	iri, err := p.readUntilIRIEnd()
	if err != nil {
		return "", err
	}
	return unescapeIRI(iri)
}

func (p *nquadLineParser) readUntilIRIEnd() (string, error) {
	start := p.pos
	for !p.done() {
		switch p.line[p.pos] {
		case '>':
			iri := p.line[start:p.pos]
			p.pos++
			return iri, nil
		case '\\':
			if p.pos+1 >= len(p.line) {
				return "", fmt.Errorf("unterminated escape in IRI")
			}
			p.pos += 2
		default:
			r, size := utf8.DecodeRuneInString(p.line[p.pos:])
			if r == utf8.RuneError && size == 1 {
				return "", fmt.Errorf("invalid UTF-8 in IRI")
			}
			if isIRISeparator(r) {
				return "", fmt.Errorf("invalid character in IRI")
			}
			p.pos += size
		}
	}
	return "", fmt.Errorf("unterminated IRI")
}

func (p *nquadLineParser) readBNode() (string, error) {
	p.pos += len("_:")
	start := p.pos
	for !p.done() && !isTermDelimiter(p.line[p.pos]) {
		_, size := utf8.DecodeRuneInString(p.line[p.pos:])
		p.pos += size
	}

	label := strings.TrimRight(p.line[start:p.pos], ".")
	p.pos = start + len(label)
	if label == "" {
		return "", fmt.Errorf("empty blank node label")
	}
	if !blankNodeLabelRE.MatchString(label) {
		return "", fmt.Errorf("invalid blank node label %q", label)
	}
	return label, nil
}

func (p *nquadLineParser) readLiteral() (string, string, error) {
	p.pos++
	lexical, err := p.readLiteralLexical()
	if err != nil {
		return "", "", err
	}
	datatype, err := p.skipLiteralSuffix()
	if err != nil {
		return "", "", err
	}
	return lexical, datatype, nil
}

func (p *nquadLineParser) readLiteralLexical() (string, error) {
	var sb strings.Builder
	for !p.done() {
		switch p.line[p.pos] {
		case '\\':
			p.pos++
			if err := p.writeEscapedLiteralChar(&sb); err != nil {
				return "", err
			}
		case '"':
			p.pos++
			return sb.String(), nil
		default:
			r, size := utf8.DecodeRuneInString(p.line[p.pos:])
			if r == utf8.RuneError && size == 1 {
				return "", fmt.Errorf("invalid UTF-8 in literal")
			}
			sb.WriteString(p.line[p.pos : p.pos+size])
			p.pos += size
		}
	}
	return "", fmt.Errorf("unterminated string literal")
}

func (p *nquadLineParser) skipLiteralSuffix() (string, error) {
	if p.consume("@") {
		return "", p.skipLangTag()
	}
	if p.consume("^^") {
		datatype, err := p.readIRI()
		if err != nil {
			return "", fmt.Errorf("datatype: %w", err)
		}
		return datatype, nil
	}
	return "", nil
}

func (p *nquadLineParser) writeEscapedLiteralChar(sb *strings.Builder) error {
	if p.done() {
		return fmt.Errorf("unterminated escape")
	}

	esc := p.line[p.pos]
	p.pos++
	switch esc {
	case 'n':
		sb.WriteByte('\n')
	case 'r':
		sb.WriteByte('\r')
	case 't':
		sb.WriteByte('\t')
	case 'b':
		sb.WriteByte('\b')
	case 'f':
		sb.WriteByte('\f')
	case '\\':
		sb.WriteByte('\\')
	case '"':
		sb.WriteByte('"')
	case 'u':
		r, err := p.readHexRune(4)
		if err != nil {
			return err
		}
		sb.WriteRune(r)
	case 'U':
		r, err := p.readHexRune(8)
		if err != nil {
			return err
		}
		sb.WriteRune(r)
	default:
		return fmt.Errorf("unknown escape \\%c", esc)
	}
	return nil
}

func (p *nquadLineParser) readHexRune(width int) (rune, error) {
	if p.pos+width > len(p.line) {
		return 0, fmt.Errorf("truncated unicode escape")
	}

	code, err := strconv.ParseUint(p.line[p.pos:p.pos+width], 16, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid unicode escape")
	}
	p.pos += width

	r := rune(code)
	if !utf8.ValidRune(r) {
		return 0, fmt.Errorf("unicode escape out of range")
	}
	return r, nil
}

func (p *nquadLineParser) skipLangTag() error {
	start := p.pos
	for !p.done() && !isTermDelimiter(p.line[p.pos]) {
		p.pos++
	}
	if !langTagRE.MatchString(p.line[start:p.pos]) {
		return fmt.Errorf("invalid language tag")
	}
	return nil
}

func (p *nquadLineParser) skipSpaces() {
	for !p.done() && (p.peek(" ") || p.peek("\t")) {
		p.pos++
	}
}

func (p *nquadLineParser) consume(token string) bool {
	if !p.peek(token) {
		return false
	}
	p.pos += len(token)
	return true
}

func (p *nquadLineParser) peek(token string) bool {
	return strings.HasPrefix(p.line[p.pos:], token)
}

func (p *nquadLineParser) done() bool {
	return p.pos >= len(p.line)
}

func unescapeIRI(s string) (string, error) {
	if !strings.ContainsRune(s, '\\') {
		return s, nil
	}

	var sb strings.Builder
	p := &nquadLineParser{line: s}
	for !p.done() {
		if !p.consume(`\`) {
			r, size := utf8.DecodeRuneInString(p.line[p.pos:])
			if r == utf8.RuneError && size == 1 {
				return "", fmt.Errorf("invalid UTF-8 in IRI")
			}
			sb.WriteString(p.line[p.pos : p.pos+size])
			p.pos += size
			continue
		}

		if p.consume("u") {
			r, err := p.readHexRune(4)
			if err != nil {
				return "", err
			}
			sb.WriteRune(r)
			continue
		}
		if p.consume("U") {
			r, err := p.readHexRune(8)
			if err != nil {
				return "", err
			}
			sb.WriteRune(r)
			continue
		}
		return "", fmt.Errorf("unsupported IRI escape")
	}
	return sb.String(), nil
}

func isIRISeparator(r rune) bool {
	return unicode.IsSpace(r)
}

func isTermDelimiter(ch byte) bool {
	return strings.ContainsRune(" \t.", rune(ch))
}
