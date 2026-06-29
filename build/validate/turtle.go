package validate

import (
	"bytes"
	"fmt"
	"os"
	"regexp"

	rdflibgo "github.com/tggo/goRDFlib"
	"github.com/tggo/goRDFlib/turtle"
)

func turtleDeclaredPrefixNames(content []byte) map[string]bool {
	declared := map[string]bool{}
	for _, pattern := range []*regexp.Regexp{
		regexp.MustCompile(`(?im)^\s*@prefix\s+([A-Za-z][A-Za-z0-9_.-]*|):\s*<[^>]*>\s*\.`),
		regexp.MustCompile(`(?im)^\s*PREFIX\s+([A-Za-z][A-Za-z0-9_.-]*|):\s*<[^>]*>`),
	} {
		for _, match := range pattern.FindAllSubmatch(content, -1) {
			declared[string(match[1])] = true
		}
	}
	return declared
}

func turtleDeclaredPrefixes(g *rdflibgo.Graph, content []byte) map[string]string {
	declared := turtleDeclaredPrefixNames(content)
	prefixes := map[string]string{}
	g.Namespaces()(func(prefix string, ns rdflibgo.URIRef) bool {
		if declared[prefix] {
			prefixes[prefix] = ns.Value()
		}
		return true
	})
	return prefixes
}

func parseTurtleFile(path string, base string) (*rdfDocument, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("build: read %s: %w", path, err)
	}

	appendRDFTerm := func(terms *[]UsedTermsInFile, term rdflibgo.Term, line int) {
		if uri, ok := term.(rdflibgo.URIRef); ok {
			*terms = append(*terms, UsedTermsInFile{iri: uri.Value(), line: line})
		}
	}

	var terms []UsedTermsInFile
	g := rdflibgo.NewGraph(rdflibgo.WithBase(base))
	err = turtle.Parse(g, bytes.NewReader(content), turtle.WithProvenance(
		func(s rdflibgo.Subject, p rdflibgo.URIRef, o rdflibgo.Term, lineNum int) {
			appendRDFTerm(&terms, s, lineNum)
			appendRDFTerm(&terms, p, lineNum)
			appendRDFTerm(&terms, o, lineNum)
			if lit, ok := o.(rdflibgo.Literal); ok {
				datatype := lit.Datatype()
				if datatype.Value() != rdflibgo.XSDString.Value() {
					appendRDFTerm(&terms, datatype, lineNum)
				}
			}
		}), turtle.WithBase(base))
	if err != nil {
		return nil, fmt.Errorf("%s: invalid Turtle: %w", path, err)
	}

	ctx := RdfContext{Prefixes: turtleDeclaredPrefixes(g, content)}
	return &rdfDocument{graph: g, ctx: ctx, terms: terms}, nil
}
