package validate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/cgs-earth/sal/build/vocab"
	rdflibgo "github.com/tggo/goRDFlib"
	"github.com/tggo/goRDFlib/jsonld"
	"github.com/tggo/goRDFlib/rdfxml"
	"github.com/tggo/goRDFlib/turtle"
)

type vocabularyCache struct {
	cacheDir string
	cache    map[string]Vocabulary
	failures map[string]error
	fetch    func(string) ([]byte, string, error)
	base     string
}

const vocabularyCacheVersion = 11

func defaultVocabularyCacheDir() string {
	return filepath.Join("/tmp", "sal", "cache", "vocab")
}

type cachedVocabulary struct {
	Version int      `json:"version"`
	Base    string   `json:"base"`
	Terms   []string `json:"terms"`
}

func (c *vocabularyCache) loadTermsFromDisk(base string) (map[string]bool, error) {
	data, err := os.ReadFile(c.cachePath(base))
	if err != nil {
		return nil, err
	}

	var cached cachedVocabulary
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil, err
	}
	if cached.Version != vocabularyCacheVersion {
		return nil, fmt.Errorf("cache version mismatch")
	}

	terms := make(map[string]bool, len(cached.Terms))
	for _, term := range cached.Terms {
		terms[term] = true
	}
	return terms, nil
}

func (c *vocabularyCache) storeTermsToDisk(base string, terms map[string]bool) error {
	if err := os.MkdirAll(c.cacheDir, 0755); err != nil {
		return err
	}

	list := make([]string, 0, len(terms))
	for term := range terms {
		list = append(list, term)
	}
	sort.Strings(list)

	payload, err := json.Marshal(cachedVocabulary{Version: vocabularyCacheVersion, Base: base, Terms: list})
	if err != nil {
		return err
	}

	tmpPath := c.cachePath(base) + ".tmp"
	if err := os.WriteFile(tmpPath, payload, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, c.cachePath(base)); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func (c *vocabularyCache) cachePath(base string) string {
	sum := sha256.Sum256([]byte(base))
	return filepath.Join(c.cacheDir, hex.EncodeToString(sum[:])+".json")
}

func longestPrefixBase(iri string, ctx RdfContext) (string, string, bool) {
	bestPrefix := ""
	bestBase := ""
	if ctx.Vocab != "" && strings.HasPrefix(iri, ctx.Vocab) {
		bestBase = ctx.Vocab
	}
	for prefix, base := range ctx.Prefixes {
		if strings.HasPrefix(iri, base) && len(base) >= len(bestBase) {
			bestPrefix = prefix
			bestBase = base
		}
	}
	return bestPrefix, bestBase, bestBase != ""
}

func (c *vocabularyCache) isDefined(iri string, ctx RdfContext, replacements map[string]string) (bool, error) {
	if c.base != "" && strings.HasPrefix(iri, c.base) {
		return true, nil
	}
	if iriWithoutXsdNamepace, found := strings.CutPrefix(iri, xsdNamespaceIRI); found {
		return slices.Contains(xsdBuiltinDatatypeLocalNames, iriWithoutXsdNamepace), nil
	}

	_, base, ok := longestPrefixBase(iri, ctx)
	if !ok {
		return true, nil
	}
	lookupIRI := replacementVocabularyTerm(iri, base, replacements)
	base = replacementVocabularyBase(base, replacements)
	vocab, err := c.load(base)
	if err != nil {
		return false, err
	}
	return vocab.terms[lookupIRI], nil
}

func replacementVocabularyBase(base string, replacements map[string]string) string {
	if replacements == nil {
		return base
	}
	if replacement, ok := replacements[base]; ok {
		return replacement
	}
	return base
}

func replacementVocabularyTerm(iri, base string, replacements map[string]string) string {
	if replacements == nil {
		return iri
	}
	replacement, ok := replacements[base]
	if !ok {
		return iri
	}
	return replacement + strings.TrimPrefix(iri, base)
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

func (c *vocabularyCache) load(base string) (Vocabulary, error) {
	base = vocabularyDocumentURL(base)
	if vocab, ok := c.cache[base]; ok {
		return vocab, nil
	}
	if c.failures == nil {
		c.failures = map[string]error{}
	}
	if err, ok := c.failures[base]; ok {
		return Vocabulary{}, err
	}

	terms, err := c.loadTerms(base)
	if err != nil {
		c.failures[base] = err
		return Vocabulary{}, err
	}
	vocab := Vocabulary{terms: terms}
	c.cache[base] = vocab
	return vocab, nil
}

func (c *vocabularyCache) loadTerms(base string) (map[string]bool, error) {
	if terms, err := c.loadTermsFromDisk(base); err == nil {
		return terms, nil
	}

	var fetchErr error
	body, contentType, err := c.fetch(base)
	if err != nil {
		fetchErr = err
	} else {
		terms, _, err := serializeRdfDataAndGetVocab(contentType, body, c.base)
		if err == nil {
			if err := c.storeTermsToDisk(base, terms); err != nil {
				return nil, err
			}
			return terms, nil
		}
		fetchErr = err
	}

	terms, err := loadBundledVocabularyTerms(base, c.base)
	if err != nil {
		return nil, fetchErr
	}

	if err := c.storeTermsToDisk(base, terms); err != nil {
		return nil, err
	}
	return terms, nil
}

func loadBundledVocabularyTerms(base, buildBase string) (map[string]bool, error) {
	body, contentType, ok, err := vocab.Load(base)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, vocab.MissingError(base)
	}
	terms, _, err := serializeRdfDataAndGetVocab(contentType, body, buildBase)
	return terms, err
}

type rdfFormat string

const (
	rdfXMLFormat rdfFormat = "application/rdf+xml"
	turtleFormat rdfFormat = "text/turtle"
	jsonLDFormat rdfFormat = "application/ld+json"
)

// serializeRdfDataAndGetVocab parses a vocabulary document and returns every
// URI-backed term in the resulting graph.
func serializeRdfDataAndGetVocab(contentType string, body []byte, base string) (map[string]bool, *rdflibgo.Graph, error) {
	parsersToTry := []rdfFormat{}

	mediaType, _, _ := mime.ParseMediaType(contentType)
	switch {
	case mediaType == "application/ld+json" || mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		parsersToTry = append(parsersToTry, jsonLDFormat)
	case mediaType == "text/turtle" || mediaType == "application/n-triples" || mediaType == "application/n-quads":
		parsersToTry = append(parsersToTry, turtleFormat)
	case mediaType == "application/rdf+xml" || strings.HasSuffix(mediaType, "+xml") || strings.Contains(mediaType, "xml"):
		parsersToTry = append(parsersToTry, rdfXMLFormat)
	case looksLikeJSON(body):
		parsersToTry = append(parsersToTry, jsonLDFormat)
	case looksLikeTurtle(body):
		parsersToTry = append(parsersToTry, turtleFormat)
	default:
		parsersToTry = append(parsersToTry, rdfXMLFormat, jsonLDFormat, turtleFormat)
	}

	var errs []string
	for _, parser := range parsersToTry {
		graph, err := parseVocabularyRDF(body, base, parser)
		if err == nil {
			return extractVocabularyTermsFromGraph(graph), graph, nil
		}
		errs = append(errs, fmt.Errorf("failed to parse as %s: %w", parser, err).Error())
	}
	return nil, nil, fmt.Errorf("unsupported vocabulary serialization (%s): %s", contentType, strings.Join(errs, "; "))
}

func parseVocabularyRDF(body []byte, base string, format rdfFormat) (*rdflibgo.Graph, error) {
	g := rdflibgo.NewGraph(rdflibgo.WithBase(base))
	switch format {
	case rdfXMLFormat:
		if err := rdfxml.Parse(g, bytes.NewReader(body), rdfxml.WithBase(base)); err != nil {
			return nil, err
		}
	case turtleFormat:
		if err := turtle.Parse(g, bytes.NewReader(body), turtle.WithBase(base)); err != nil {
			return nil, err
		}
	case jsonLDFormat:
		if err := jsonld.Parse(g, bytes.NewReader(body), jsonld.WithBase(base), jsonld.WithUnboundedLines()); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown RDF format: %s", format)
	}
	return g, nil
}

// extractVocabularyTermsFromGraph collects URI terms that a vocabulary defines
// after it has been parsed into an RDF graph.
func extractVocabularyTermsFromGraph(g *rdflibgo.Graph) map[string]bool {
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
		terms[triple.Predicate.Value()] = true
		if obj, ok := triple.Object.(rdflibgo.URIRef); ok {
			terms[obj.Value()] = true
		}
		if lit, ok := triple.Object.(rdflibgo.Literal); ok {
			terms[lit.Datatype().Value()] = true
		}
		return true
	})
	return terms
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

func looksLikeJSON(body []byte) bool {
	for _, b := range body {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '{', '[':
			return true
		default:
			return false
		}
	}
	return false
}

func looksLikeTurtle(body []byte) bool {
	s := strings.TrimSpace(string(body))
	return strings.HasPrefix(s, "@prefix") || strings.HasPrefix(s, "PREFIX") || strings.HasPrefix(s, "@base") || strings.HasPrefix(s, "BASE ")
}

func looksLikeVocabularyBase(value string) bool {
	return strings.HasSuffix(value, "/") || strings.HasSuffix(value, "#")
}
