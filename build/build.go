package build

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/cgs-earth/json-gold/ld"
	salpkg "github.com/cgs-earth/sal/pkg"
	rdflibgo "github.com/tggo/goRDFlib"
	"github.com/tggo/goRDFlib/jsonld"
	"github.com/tggo/goRDFlib/turtle"
)

type BuildCmd struct {
	Paths      []string `arg:"positional" help:"RDF files to validate"`
	PrefixMaps []string `arg:"--prefix-maps" help:"prefix mappings to apply as source target pairs or source=target entries"`
}

type jsonLDContext struct {
	prefixes map[string]string
	vocab    string
}

type vocabulary struct {
	terms map[string]bool
}

type usedTerm struct {
	iri  string
	line int
}

var findSALProjectDir = salpkg.FindSALProjectDir

// Run validates RDF files for terms that are not defined by their vocabularies.
func Run(cfg *BuildCmd) error {
	if cfg == nil {
		return fmt.Errorf("build: missing arguments")
	}
	paths, err := buildPaths(cfg.Paths)
	if err != nil {
		return err
	}
	fetch := fetchVocabularyDocument
	if len(cfg.PrefixMaps) > 0 {
		fetch, err = prefixMappedVocabularyFetch(cfg.PrefixMaps, fetchVocabularyDocument)
		if err != nil {
			return err
		}
	}
	return run(paths, ld.NewDefaultDocumentLoader(nil), fetch)
}

func buildPaths(paths []string) ([]string, error) {
	if len(paths) > 0 {
		return paths, nil
	}
	projectDir, err := findSALProjectDir(os.UserHomeDir)
	if err != nil {
		return nil, fmt.Errorf("build: find SAL project directory: %w", err)
	}
	return []string{projectDir}, nil
}

func run(paths []string, loader ld.DocumentLoader, vocabFetch func(string) ([]byte, string, error)) error {
	files, err := expandInputs(paths)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no JSON-LD or TTL files found in %s", strings.Join(paths, ", "))
	}

	var errs multiError
	for _, file := range files {
		if err := validateRDFFile(file, loader, vocabFetch); err != nil {
			if nested, ok := err.(multiError); ok {
				errs = append(errs, nested...)
			} else {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) > 0 {
		return errs
	}

	slog.Info("Validated " + fmt.Sprint(len(files)) + " file(s)")
	return nil
}

func expandInputs(paths []string) ([]string, error) {
	var files []string
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("build: %s: %w", path, err)
		}
		if !info.IsDir() {
			if includeRDFInput(path) {
				files = append(files, path)
			}
			continue
		}
		err = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && includeRDFInput(p) {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("build: walk %s: %w", path, err)
		}
	}
	sort.Strings(files)
	return files, nil
}

func includeRDFInput(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".jsonld" || ext == ".json" || ext == ".ttl" || ext == ".turtle"
}

func validateRDFFile(path string, loader ld.DocumentLoader, vocabFetch func(string) ([]byte, string, error)) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ttl", ".turtle":
		return validateTurtleFile(path, vocabFetch)
	default:
		return validateJSONLDFile(path, loader, vocabFetch)
	}
}

func validateJSONLDFile(path string, loader ld.DocumentLoader, vocabFetch func(string) ([]byte, string, error)) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("build: read %s: %w", path, err)
	}

	var doc any
	decoder := json.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&doc); err != nil {
		return fmt.Errorf("%s:%d: invalid JSON-LD: %w", path, jsonErrorLine(content, err), err)
	}

	ctx, err := collectContext(doc, loader)
	if err != nil {
		return fmt.Errorf("%s: load JSON-LD context: %w", path, err)
	}
	terms, err := collectJSONLDTerms(content, loader)
	if err != nil {
		return fmt.Errorf("%s: invalid JSON-LD: %w", path, err)
	}
	return validateTerms(path, terms, ctx, vocabFetch)
}

func validateTurtleFile(path string, vocabFetch func(string) ([]byte, string, error)) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("build: read %s: %w", path, err)
	}

	appendRDFTerm := func(terms *[]usedTerm, term rdflibgo.Term, line int) {
		if uri, ok := term.(rdflibgo.URIRef); ok {
			*terms = append(*terms, usedTerm{iri: uri.Value(), line: line})
		}
	}

	var terms []usedTerm
	g := rdflibgo.NewGraph()
	err = turtle.Parse(g, bytes.NewReader(content), turtle.WithProvenance(
		func(s rdflibgo.Subject, p rdflibgo.URIRef, o rdflibgo.Term, lineNum int) {
			appendRDFTerm(&terms, s, lineNum)
			appendRDFTerm(&terms, p, lineNum)
			appendRDFTerm(&terms, o, lineNum)
			if lit, ok := o.(rdflibgo.Literal); ok {
				datatype := lit.Datatype()
				if datatype.Value() != rdflibgo.XSDString.Value() && datatype.Value() != rdfLangStringIRI {
					appendRDFTerm(&terms, datatype, lineNum)
				}
			}
		}))
	if err != nil {
		return fmt.Errorf("%s: invalid Turtle: %w", path, err)
	}

	ctx := jsonLDContext{prefixes: turtleDeclaredPrefixes(g, content)}
	return validateTerms(path, terms, ctx, vocabFetch)
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

func validateTerms(path string, terms []usedTerm, ctx jsonLDContext, vocabFetch func(string) ([]byte, string, error)) error {
	vocabs := vocabularyCache{
		cacheDir: defaultVocabularyCacheDir(),
		cache:    map[string]vocabulary{},
		failures: map[string]error{},
		fetch:    vocabFetch,
	}

	var errs multiError
	loggedVocabularyErrors := map[string]bool{}
	for _, term := range terms {
		display, ok := displayTerm(term.iri, ctx)
		if !ok {
			continue
		}
		defined, err := vocabs.isDefined(term.iri, ctx)
		if err != nil {
			logKey := term.iri + "\x00" + err.Error()
			if !loggedVocabularyErrors[logKey] {
				slog.Error("Failed to check vocabulary definition", "path", path, "term", term.iri, "error", err)
				loggedVocabularyErrors[logKey] = true
			}
			errs = append(errs, vocabularyLookupError{Path: path, Line: term.line, Term: display, Err: err})
			continue
		}
		if defined {
			continue
		}
		errs = append(errs, validationError{Path: path, Line: term.line, Term: display})
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

func collectContext(doc any, loader ld.DocumentLoader) (jsonLDContext, error) {
	ctx := jsonLDContext{prefixes: map[string]string{}}
	if err := collectContextFromNode(doc, &ctx, loader, map[string]bool{}); err != nil {
		return ctx, err
	}
	return ctx, nil
}

func collectContextFromNode(node any, ctx *jsonLDContext, loader ld.DocumentLoader, seen map[string]bool) error {
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

func readContext(value any, ctx *jsonLDContext, loader ld.DocumentLoader, seen map[string]bool) error {
	switch c := value.(type) {
	case string:
		if seen[c] {
			return nil
		}
		seen[c] = true
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
					ctx.vocab = vocab
				}
			case strings.HasPrefix(key, "@"):
				continue
			case !strings.Contains(key, ":"):
				if base, ok := contextTermBase(item); ok {
					ctx.prefixes[key] = base
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

func collectJSONLDTerms(content []byte, loader ld.DocumentLoader) ([]usedTerm, error) {
	provenance := map[string]int{}
	processor := ld.NewJsonLdProcessor()
	options := ld.NewJsonLdOptions("")
	options.DocumentLoader = loader

	addJSONLDProvenanceTerm := func(provenance map[string]int, node ld.Node, line int) {
		if line <= 0 || !ld.IsIRI(node) {
			return
		}
		iri := node.GetValue()
		if existing, ok := provenance[iri]; ok && existing <= line {
			return
		}
		provenance[iri] = line
	}

	options.RDFQuadProvenanceCallback = func(quad *ld.Quad, prov ld.RDFQuadProvenance) {
		addJSONLDProvenanceTerm(provenance, quad.Subject, prov.SubjectLine)
		addJSONLDProvenanceTerm(provenance, quad.Predicate, prov.PredicateLine)
		addJSONLDProvenanceTerm(provenance, quad.Object, prov.ObjectLine)
		if quad.Graph != nil {
			addJSONLDProvenanceTerm(provenance, quad.Graph, prov.GraphLine)
		}
	}

	if _, err := processor.ToRDF(bytes.NewReader(content), options); err != nil {
		return nil, err
	}

	terms := make([]usedTerm, 0, len(provenance))
	for iri, line := range provenance {
		terms = append(terms, usedTerm{iri: iri, line: line})
	}
	sort.Slice(terms, func(i, j int) bool {
		if terms[i].line != terms[j].line {
			return terms[i].line < terms[j].line
		}
		return terms[i].iri < terms[j].iri
	})
	return terms, nil
}

func displayTerm(iri string, ctx jsonLDContext) (string, bool) {
	prefix, base, ok := longestPrefixBase(iri, ctx)
	if ok && prefix != "" {
		return prefix + ":" + strings.TrimPrefix(iri, base), true
	}
	if ok {
		return iri, true
	}
	for prefix, base := range ctx.prefixes {
		if strings.HasPrefix(iri, base) {
			return prefix + ":" + strings.TrimPrefix(iri, base), true
		}
	}
	return iri, true
}

func longestPrefixBase(iri string, ctx jsonLDContext) (string, string, bool) {
	bestPrefix := ""
	bestBase := ""
	if ctx.vocab != "" && strings.HasPrefix(iri, ctx.vocab) {
		bestBase = ctx.vocab
	}
	for prefix, base := range ctx.prefixes {
		if strings.HasPrefix(iri, base) && len(base) >= len(bestBase) {
			bestPrefix = prefix
			bestBase = base
		}
	}
	return bestPrefix, bestBase, bestBase != ""
}

func vocabularyDocumentURL(base string) string {
	if before, _, ok := strings.Cut(base, "#"); ok {
		return before
	}
	if strings.Contains(base, "opengis.net") && strings.HasSuffix(base, "/") {
		return strings.TrimSuffix(base, "/")
	}
	return base
}

func extractVocabularyTerms(base, contentType string, body []byte) (map[string]bool, error) {
	type parserFn struct {
		name string
		fn   func(string, []byte) (map[string]bool, error)
	}

	mediaType, _, _ := mime.ParseMediaType(contentType)
	jsonLDParser := parserFn{name: "json-ld", fn: extractJSONLDVocabularyTerms}
	turtleParser := parserFn{name: "turtle", fn: extractTurtleVocabularyTerms}
	rdfXMLParser := parserFn{name: "rdfxml", fn: extractRDFXMLVocabularyTerms}

	var parsers []parserFn
	switch {
	case mediaType == "application/ld+json" || mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		parsers = []parserFn{jsonLDParser}
	case mediaType == "text/turtle" || mediaType == "application/n-triples" || mediaType == "application/n-quads":
		parsers = []parserFn{turtleParser}
	case mediaType == "application/rdf+xml" || strings.HasSuffix(mediaType, "+xml"):
		parsers = []parserFn{rdfXMLParser}

	// if it looks like a specific RDF format,
	// you default to parsing that option first,
	// but also fall back to the other options if needed
	// (i.e. if the input has no content type)
	case looksLikeJSON(body):
		parsers = []parserFn{jsonLDParser, turtleParser, rdfXMLParser}
	case strings.Contains(mediaType, "xml"):
		parsers = []parserFn{rdfXMLParser, jsonLDParser, turtleParser}
	case looksLikeTurtle(body):
		parsers = []parserFn{turtleParser, jsonLDParser, rdfXMLParser}
	default:
		parsers = []parserFn{jsonLDParser, turtleParser, rdfXMLParser}
	}

	var errs []string
	for _, parser := range parsers {
		terms, err := parser.fn(base, body)
		if err == nil {
			return terms, nil
		}
		errs = append(errs, parser.name+": "+err.Error())
	}
	return nil, fmt.Errorf("unsupported vocabulary serialization for %s (%s): %s", base, contentType, strings.Join(errs, "; "))
}

func extractJSONLDVocabularyTerms(base string, body []byte) (map[string]bool, error) {
	g := rdflibgo.NewGraph(rdflibgo.WithBase(vocabularyDocumentURL(base)))
	if err := jsonld.Parse(g, bytes.NewReader(body), jsonld.WithUnboundedLines()); err != nil {
		return nil, err
	}
	terms := map[string]bool{}
	g.Namespaces()(func(_ string, ns rdflibgo.URIRef) bool {
		if ns.Value() != "" {
			terms[ns.Value()] = true
		}
		return true
	})
	g.Triples(nil, nil, nil)(func(triple rdflibgo.Triple) bool {
		if subj, ok := triple.Subject.(rdflibgo.URIRef); ok {
			terms[subj.Value()] = true
		}
		if obj, ok := triple.Object.(rdflibgo.URIRef); ok {
			terms[obj.Value()] = true
		}
		return true
	})
	return terms, nil
}

func extractTurtleVocabularyTerms(base string, body []byte) (map[string]bool, error) {
	g := rdflibgo.NewGraph(rdflibgo.WithBase(vocabularyDocumentURL(base)))
	if err := turtle.Parse(g, bytes.NewReader(body)); err != nil {
		return nil, err
	}

	terms := map[string]bool{}
	g.Triples(nil, nil, nil)(func(triple rdflibgo.Triple) bool {
		if subj, ok := triple.Subject.(rdflibgo.URIRef); ok {
			terms[subj.Value()] = true
		}
		return true
	})
	return terms, nil
}

func fetchVocabularyDocument(u string) ([]byte, string, error) {
	req, err := http.NewRequest(http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/ld+json, application/json;q=0.9, text/turtle;q=0.8, application/rdf+xml;q=0.7, text/plain;q=0.6, */*;q=0.1")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, res.Header.Get("Content-Type"), err
	}
	if res.StatusCode != http.StatusOK {
		return nil, res.Header.Get("Content-Type"), fmt.Errorf("bad response status code: %d", res.StatusCode)
	}
	return body, res.Header.Get("Content-Type"), nil
}

func looksLikeTurtle(body []byte) bool {
	s := strings.TrimSpace(string(body))
	return strings.HasPrefix(s, "@prefix") || strings.HasPrefix(s, "PREFIX") || strings.HasPrefix(s, "@base") || strings.HasPrefix(s, "BASE ")
}

func looksLikeVocabularyBase(value string) bool {
	return strings.HasSuffix(value, "/") || strings.HasSuffix(value, "#")
}
