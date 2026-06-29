package validate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	cgsld "github.com/cgs-earth/json-gold/ld"
	"github.com/cgs-earth/sal/build/vocab"
	piprateld "github.com/piprate/json-gold/ld"
	rdflibgo "github.com/tggo/goRDFlib"
	"github.com/tggo/goRDFlib/jsonld"
)

func jsonErrorLine(content []byte, err error) int {
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	var offset int64
	switch {
	case errors.As(err, &syntaxErr):
		offset = syntaxErr.Offset
	case errors.As(err, &typeErr):
		offset = typeErr.Offset
	default:
		return 1
	}
	if offset <= 0 {
		return 1
	}
	return 1 + bytes.Count(content[:min(int(offset), len(content))], []byte("\n"))
}

func parseJSONLDFile(path string, base string) (*rdfDocument, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("build: read %s: %w", path, err)
	}

	var doc any
	if err := json.Unmarshal(content, &doc); err != nil {
		return nil, fmt.Errorf("%s:%d: invalid JSON-LD: %w", path, jsonErrorLine(content, err), err)
	}

	loader := cgsld.NewCachingDocumentLoader(bundledDocumentLoader{next: cgsld.NewDefaultDocumentLoader(nil)})
	ctx, err := collectJSONLDContext(doc, loader)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid JSON-LD: %w", path, err)
	}

	terms, err := collectJSONLDTerms(content, loader)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid JSON-LD: %w", path, err)
	}
	terms = append(terms, collectJSONLDSourceTerms(doc, ctx)...)
	if err := validateJSONLDLocalTypes(path, doc, base); err != nil {
		return nil, err
	}

	g := rdflibgo.NewGraph(rdflibgo.WithBase(base))
	err = jsonld.Parse(g, bytes.NewReader(content), jsonld.WithBase(base), jsonld.WithDocumentLoader(piprateld.NewCachingDocumentLoader(piprateBundledDocumentLoader{next: piprateld.NewDefaultDocumentLoader(nil)})))
	if err != nil {
		return nil, fmt.Errorf("%s: invalid JSON-LD: %w", path, err)
	}

	return &rdfDocument{graph: g, ctx: ctx, terms: terms}, nil
}

type bundledDocumentLoader struct {
	next cgsld.DocumentLoader
}

func (l bundledDocumentLoader) LoadDocument(u string) (*cgsld.RemoteDocument, error) {
	if looksLikeVocabularyBase(u) {
		return &cgsld.RemoteDocument{DocumentURL: u, Document: vocabularyBaseContext(u)}, nil
	}
	body, _, ok, err := vocab.Load(u)
	if err != nil {
		return nil, err
	}
	if ok {
		var doc any
		if err := json.Unmarshal(body, &doc); err != nil {
			return nil, err
		}
		return &cgsld.RemoteDocument{DocumentURL: u, Document: doc}, nil
	}
	return l.next.LoadDocument(u)
}

type piprateBundledDocumentLoader struct {
	next piprateld.DocumentLoader
}

func (l piprateBundledDocumentLoader) LoadDocument(u string) (*piprateld.RemoteDocument, error) {
	if looksLikeVocabularyBase(u) {
		return &piprateld.RemoteDocument{DocumentURL: u, Document: vocabularyBaseContext(u)}, nil
	}
	body, _, ok, err := vocab.Load(u)
	if err != nil {
		return nil, err
	}
	if ok {
		var doc any
		if err := json.Unmarshal(body, &doc); err != nil {
			return nil, err
		}
		return &piprateld.RemoteDocument{DocumentURL: u, Document: doc}, nil
	}
	return l.next.LoadDocument(u)
}

func vocabularyBaseContext(base string) map[string]any {
	return map[string]any{
		"@context": map[string]any{
			"@vocab": base,
		},
	}
}

// collectJSONLDContext walks local and remote JSON-LD contexts and records the
// vocabulary bases that should be used to validate compact terms.
func collectJSONLDContext(doc any, loader cgsld.DocumentLoader) (RdfContext, error) {
	ctx := RdfContext{Prefixes: map[string]string{}}
	if err := collectContextFromNode(doc, &ctx, loader, map[string]bool{}); err != nil {
		return ctx, err
	}
	return ctx, nil
}

func collectContextFromNode(node any, ctx *RdfContext, loader cgsld.DocumentLoader, seen map[string]bool) error {
	switch n := node.(type) {
	case map[string]any:
		if value, ok := n["@context"]; ok {
			if err := readContext(value, ctx, loader, seen); err != nil {
				return err
			}
		}
		for key, value := range n {
			if key != "@context" {
				if err := collectContextFromNode(value, ctx, loader, seen); err != nil {
					return err
				}
			}
		}
	case []any:
		for _, value := range n {
			if err := collectContextFromNode(value, ctx, loader, seen); err != nil {
				return err
			}
		}
	}
	return nil
}

func readContext(value any, ctx *RdfContext, loader cgsld.DocumentLoader, seen map[string]bool) error {
	switch c := value.(type) {
	case string:
		if seen[c] {
			return nil
		}
		seen[c] = true
		if looksLikeVocabularyBase(c) {
			ctx.Vocab = c
		}
		doc, err := loader.LoadDocument(c)
		if err != nil {
			return err
		}
		if remoteCtx, ok := documentContext(doc.Document); ok {
			return readContext(remoteCtx, ctx, loader, seen)
		}
		return readContext(doc.Document, ctx, loader, seen)
	case []any:
		for _, item := range c {
			if err := readContext(item, ctx, loader, seen); err != nil {
				return err
			}
		}
	case map[string]any:
		for key, item := range c {
			switch {
			case key == "@vocab":
				if vocab, ok := item.(string); ok {
					ctx.Vocab = vocab
				}
			case strings.HasPrefix(key, "@"):
				continue
			case !strings.Contains(key, ":"):
				if base, ok := contextTermBase(item); ok {
					ctx.Prefixes[key] = base
				}
			}
		}
	}
	return nil
}

func documentContext(doc any) (any, bool) {
	m, ok := doc.(map[string]any)
	if !ok {
		return nil, false
	}
	ctx, ok := m["@context"]
	return ctx, ok
}

func contextTermBase(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		if looksLikeVocabularyBase(v) {
			return v, true
		}
	case map[string]any:
		id, ok := v["@id"].(string)
		if ok && looksLikeVocabularyBase(id) {
			return id, true
		}
	}
	return "", false
}

// collectJSONLDSourceTerms resolves source-level compact terms so validation
// catches JSON-LD keys that expansion drops because they are not defined.
func collectJSONLDSourceTerms(doc any, ctx RdfContext) []UsedTermsInFile {
	var terms []UsedTermsInFile
	collectJSONLDSourceTermsFromNode(doc, ctx, &terms)
	return terms
}

func collectJSONLDSourceTermsFromNode(node any, ctx RdfContext, terms *[]UsedTermsInFile) {
	switch n := node.(type) {
	case map[string]any:
		for key, value := range n {
			switch {
			case key == "@context":
				continue
			case key == "@type":
				appendJSONLDValueTerms(value, ctx, terms)
			case strings.HasPrefix(key, "@"):
				continue
			default:
				if iri, ok := expandJSONLDTerm(key, ctx); ok {
					*terms = append(*terms, UsedTermsInFile{iri: iri, line: 1})
				}
				collectJSONLDSourceTermsFromNode(value, ctx, terms)
			}
		}
	case []any:
		for _, item := range n {
			collectJSONLDSourceTermsFromNode(item, ctx, terms)
		}
	}
}

func appendJSONLDValueTerms(value any, ctx RdfContext, terms *[]UsedTermsInFile) {
	switch v := value.(type) {
	case string:
		if iri, ok := expandJSONLDTerm(v, ctx); ok {
			*terms = append(*terms, UsedTermsInFile{iri: iri, line: 1})
		}
	case []any:
		for _, item := range v {
			appendJSONLDValueTerms(item, ctx, terms)
		}
	}
}

func expandJSONLDTerm(term string, ctx RdfContext) (string, bool) {
	if strings.Contains(term, "://") {
		return term, true
	}
	if prefix, name, ok := strings.Cut(term, ":"); ok {
		base, ok := ctx.Prefixes[prefix]
		if !ok {
			return "", false
		}
		return base + name, true
	}
	if ctx.Vocab == "" {
		return "", false
	}
	return ctx.Vocab + term, true
}

func validateJSONLDLocalTypes(path string, doc any, base string) error {
	var errs MultiError
	collectMissingJSONLDTypes(path, doc, base, &errs)
	if len(errs) > 0 {
		return errs
	}
	return nil
}

func collectMissingJSONLDTypes(path string, node any, base string, errs *MultiError) {
	switch n := node.(type) {
	case map[string]any:
		if id, ok := n["@id"].(string); ok && isLocalJSONLDID(id, base) && jsonLDNodeNeedsType(n) {
			*errs = append(*errs, missingTypeError{Path: path, Line: 1, IRI: expandJSONLDID(id, base)})
		}
		for key, value := range n {
			if key != "@context" {
				collectMissingJSONLDTypes(path, value, base, errs)
			}
		}
	case []any:
		for _, item := range n {
			collectMissingJSONLDTypes(path, item, base, errs)
		}
	}
}

func jsonLDNodeNeedsType(node map[string]any) bool {
	if _, ok := node["@type"]; ok {
		return false
	}
	for key := range node {
		if key != "@id" && key != "@context" {
			return true
		}
	}
	return false
}

func isLocalJSONLDID(id, base string) bool {
	return !strings.Contains(id, "://") || strings.HasPrefix(id, base)
}

func expandJSONLDID(id, base string) string {
	if strings.Contains(id, "://") {
		return id
	}
	return base + id
}

// collectJSONLDTerms converts JSON-LD to RDF and uses json-gold provenance to
// retain the source line for each IRI-backed term.
func collectJSONLDTerms(content []byte, loader cgsld.DocumentLoader) ([]UsedTermsInFile, error) {
	provenance := map[string]int{}
	processor := cgsld.NewJsonLdProcessor()
	options := cgsld.NewJsonLdOptions("")
	options.DocumentLoader = loader

	addTerm := func(node cgsld.Node, line int) {
		if node == nil {
			return
		}
		if line <= 0 {
			line = 1
		}
		if !cgsld.IsIRI(node) {
			if lit, ok := node.(cgsld.Literal); ok && lit.Datatype != "" && lit.Datatype != rdflibgo.XSDString.Value() {
				provenance[lit.Datatype] = line
			}
			return
		}
		iri := node.GetValue()
		if existing, ok := provenance[iri]; ok && existing <= line {
			return
		}
		provenance[iri] = line
	}

	options.RDFQuadProvenanceCallback = func(quad *cgsld.Quad, prov cgsld.RDFQuadProvenance) {
		addTerm(quad.Subject, prov.SubjectLine)
		addTerm(quad.Predicate, prov.PredicateLine)
		addTerm(quad.Object, prov.ObjectLine)
		addTerm(quad.Graph, prov.GraphLine)
	}

	if _, err := processor.ToRDF(bytes.NewReader(content), options); err != nil {
		return nil, err
	}

	terms := make([]UsedTermsInFile, 0, len(provenance))
	for iri, line := range provenance {
		terms = append(terms, UsedTermsInFile{iri: iri, line: line})
	}
	sort.Slice(terms, func(i, j int) bool {
		if terms[i].line != terms[j].line {
			return terms[i].line < terms[j].line
		}
		return terms[i].iri < terms[j].iri
	})
	return terms, nil
}
