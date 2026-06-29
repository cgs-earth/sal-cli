package build

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cgs-earth/json-gold/ld"
	salpkg "github.com/cgs-earth/sal/pkg"
	"github.com/stretchr/testify/require"
)

const testSchemaOrgBase = "http://schema.org/"

func TestBuildPathsKeepsExplicitPaths(t *testing.T) {
	called := false
	withFindSALProjectDir(t, func(func() (string, error)) (string, error) {
		called = true
		return "", nil
	})

	paths, err := buildPaths([]string{"one.ttl", "two.jsonld"})

	require.NoError(t, err)
	require.Equal(t, []string{"one.ttl", "two.jsonld"}, paths)
	require.False(t, called)
}

func TestBuildPathsDefaultsToSALProjectDir(t *testing.T) {
	projectDir := filepath.Join(t.TempDir(), "project")
	withFindSALProjectDir(t, func(func() (string, error)) (string, error) {
		return projectDir, nil
	})

	paths, err := buildPaths(nil)

	require.NoError(t, err)
	require.Equal(t, []string{projectDir}, paths)
}

func TestBuildPathsReturnsSALProjectDirError(t *testing.T) {
	withFindSALProjectDir(t, func(func() (string, error)) (string, error) {
		return "", salpkg.ErrSalDirNotFound
	})

	paths, err := buildPaths(nil)

	require.Nil(t, paths)
	require.ErrorIs(t, err, salpkg.ErrSalDirNotFound)
	require.Contains(t, err.Error(), "build: find SAL project directory")
}

func TestRunReportsUndefinedSchemaOrgTermWithLineNumber(t *testing.T) {
	path := filepath.Join("testdata", "incorrect", "name.jsonld")

	err := run([]string{path}, schemaOrgTestLoader{}, schemaOrgVocabularyFetch)
	require.Error(t, err)
	got := err.Error()
	if !strings.Contains(got, path+":4:") {
		t.Fatalf("Run() error = %q, want line 4", got)
	}
	if !strings.Contains(got, "undefined term schema:namee") {
		t.Fatalf("Run() error = %q, want undefined schema:namee", got)
	}
}

func TestRunValidatesSchemaOrgJSONLD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "person.jsonld")
	writeTestFile(t, path, `{
  "@context": "http://schema.org/",
  "@type": "Person",
  "name": "Jane Doe",
  "jobTitle": "Professor",
  "telephone": "(425) 123-4567",
  "url": "http://www.janedoe.com"
}`)

	err := run([]string{path}, schemaOrgTestLoader{}, schemaOrgVocabularyFetch)
	require.NoError(t, err)
}

func TestCollectJSONLDTermsUsesRDFQuadProvenanceLines(t *testing.T) {
	terms, err := collectJSONLDTerms([]byte(`{
  "@context": {
    "name": "http://schema.org/name",
    "knows": {"@id": "http://schema.org/knows", "@type": "@id"}
  },
  "@id": "http://example.com/alice",
  "name": "Alice",
  "knows": {
    "name": {"@value": "Bob"},
    "@id": "http://example.com/bob"
  }
}`), ld.NewDefaultDocumentLoader(nil))
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]int{}
	for _, term := range terms {
		got[term.iri] = term.line
	}

	for iri, line := range map[string]int{
		"http://schema.org/name":  7,
		"http://schema.org/knows": 8,
		"http://example.com/bob":  8,
	} {
		if got[iri] != line {
			t.Fatalf("term %s line = %d, want %d; terms = %#v", iri, got[iri], line, terms)
		}
	}
}

func TestRunReportsUndefinedTermFromArbitraryVocabulary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "thing.jsonld")
	writeTestFile(t, path, `{
  "@context": "https://example.com/context",
  "ex:Missing": "value"
}`)

	err := run(
		[]string{path},
		exampleVocabularyLoader{},
		exampleVocabularyFetch,
	)

	require.Error(t, err)
	got := err.Error()
	if !strings.Contains(got, path+":3:") {
		t.Fatalf("Run() error = %q, want line 3", got)
	}
	if !strings.Contains(got, "undefined term ex:Missing") {
		t.Fatalf("Run() error = %q, want undefined ex:Missing", got)
	}
}

func TestRunValidatesArbitraryVocabularyTerm(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "thing.jsonld")
	writeTestFile(t, path, `{
  "@context": "https://example.com/context",
  "ex:Known": "value"
}`)

	err := run(
		[]string{path},
		exampleVocabularyLoader{},
		exampleVocabularyFetch,
	)

	require.NoError(t, err)
}

func TestRunReportsUndefinedTurtleTermWithLineNumber(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "thing.ttl")
	writeTestFile(t, path, `@prefix ex: <https://example.com/vocab#> .

ex:Known ex:Known ex:Known ,
                   ex:Missing .
`)

	err := run(
		[]string{path},
		exampleVocabularyLoader{},
		exampleVocabularyFetch,
	)

	require.Error(t, err)
	got := err.Error()
	if !strings.Contains(got, path+":4:") {
		t.Fatalf("Run() error = %q, want line 4", got)
	}
	if !strings.Contains(got, "undefined term ex:Missing") {
		t.Fatalf("Run() error = %q, want undefined ex:Missing", got)
	}
}

func TestRunValidatesTurtleTerm(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "thing.ttl")
	writeTestFile(t, path, `@prefix ex: <https://example.com/vocab#> .

ex:Known ex:Known ex:Known .
`)

	err := run(
		[]string{path},
		exampleVocabularyLoader{},
		exampleVocabularyFetch,
	)

	require.NoError(t, err)
}

func TestRunValidatesBuiltinXSDDatatype(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "thing.ttl")
	writeTestFile(t, path, `@prefix xsd: <http://www.w3.org/2001/XMLSchema#> .

[] <https://example.com/prop> "value"^^xsd:string .
`)

	err := run(
		[]string{path},
		exampleVocabularyLoader{},
		func(u string) ([]byte, string, error) { return nil, "", fmt.Errorf("unexpected url %s", u) },
	)

	require.NoError(t, err)
}

func TestRunValidatesJSONLDTestdata(t *testing.T) {
	cases := []struct {
		dir     string
		wantErr bool
	}{
		{dir: filepath.Join("testdata", "correct"), wantErr: false},
		{dir: filepath.Join("testdata", "incorrect"), wantErr: true},
	}

	for _, tc := range cases {
		var paths []string
		err := filepath.WalkDir(tc.dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && strings.EqualFold(filepath.Ext(path), ".jsonld") {
				paths = append(paths, path)
			}
			return nil
		})
		require.NoError(t, err)
		require.NotEmpty(t, paths)

		for _, path := range paths {
			t.Run(filepath.ToSlash(path), func(t *testing.T) {
				err := run([]string{path}, schemaOrgTestLoader{}, schemaOrgVocabularyFetch)
				if tc.wantErr {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
			})
		}
	}
}

func TestRunReportsUnknownXSDDatatypeAsUndefinedTerm(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "thing.ttl")
	writeTestFile(t, path, `@prefix xsd: <http://www.w3.org/2001/XMLSchema#> .

[] <https://example.com/prop> "value"^^xsd:madeUpType .
`)

	err := run(
		[]string{path},
		exampleVocabularyLoader{},
		func(u string) ([]byte, string, error) { return nil, "", fmt.Errorf("unexpected url %s", u) },
	)

	require.Error(t, err)
	got := err.Error()
	if !strings.Contains(got, "undefined term xsd:madeUpType") {
		t.Fatalf("Run() error = %q, want undefined xsd:madeUpType", got)
	}
}

func TestRunValidatesTermFromRDFXMLVocabulary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "person.ttl")
	writeTestFile(t, path, `@prefix ex: <http://example.org/> .

ex:Alice ex:name "Alice" .
`)

	err := run(
		[]string{path},
		exampleVocabularyLoader{},
		func(u string) ([]byte, string, error) {
			if u != "http://example.org/" {
				return nil, "", fmt.Errorf("unexpected url %s", u)
			}
			return []byte(`<?xml version="1.0"?>
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
         xmlns:ex="http://example.org/">
  <ex:Person rdf:about="http://example.org/Alice">
    <ex:name>Alice</ex:name>
  </ex:Person>
</rdf:RDF>`), "application/rdf+xml; qs=0.9", nil
		},
	)

	require.NoError(t, err)
}

func TestRunExpandsInputDirectories(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "person.jsonld"), `{
  "@context": "http://schema.org/",
  "@type": "Person",
  "name": "Jane Doe"
}`)
	writeTestFile(t, filepath.Join(dir, "skip.ttl"), "@prefix ex: <https://example.com/> .\n")

	err := run([]string{dir}, schemaOrgTestLoader{}, schemaOrgVocabularyFetch)
	require.NoError(t, err)
}

func TestExtractRDFXMLVocabularyTermsTypedNode(t *testing.T) {
	terms, err := extractVocabularyTerms("http://example.org/", "application/rdf+xml; qs=0.9", []byte(`<?xml version="1.0"?>
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
         xmlns:ex="http://example.org/">
  <ex:Person rdf:about="http://example.org/Alice">
    <ex:name>Alice</ex:name>
  </ex:Person>
</rdf:RDF>`))
	if err != nil {
		t.Fatal(err)
	}

	for _, term := range []string{
		"http://example.org/Alice",
		"http://example.org/Person",
		"http://example.org/name",
	} {
		if !terms[term] {
			t.Fatalf("expected %s in %#v", term, terms)
		}
	}
}

func TestExtractJSONLDVocabularyTermsUsesGoRDFlibParser(t *testing.T) {
	terms, err := extractVocabularyTerms("https://example.com/vocab/", "application/ld+json", []byte(`{
  "@context": {
    "ex": "https://example.com/vocab/",
    "rdf": "http://www.w3.org/1999/02/22-rdf-syntax-ns#",
    "rdfs": "http://www.w3.org/2000/01/rdf-schema#"
  },
  "@graph": [
    {
      "@id": "ex:Known",
      "@type": "rdfs:Class"
    },
    {
      "@id": "ex:relatedTo",
      "@type": "rdf:Property",
      "rdfs:range": {"@id": "ex:Known"}
    }
  ]
}`))

	require.NoError(t, err)
	require.True(t, terms["https://example.com/vocab/Known"])
	require.True(t, terms["https://example.com/vocab/relatedTo"])
}

func TestExtractTurtleVocabularyTermsQUDTSyntax(t *testing.T) {
	terms, err := extractVocabularyTerms("http://qudt.org/schema/qudt/", "text/turtle; charset=utf-8", []byte(`@prefix rdf: <http://www.w3.org/1999/02/22-rdf-syntax-ns#> .
@prefix rdfs: <http://www.w3.org/2000/01/rdf-schema#> .
@prefix xsd: <http://www.w3.org/2001/XMLSchema#> .
@prefix owl: <http://www.w3.org/2002/07/owl#> .
@prefix dcterms: <http://purl.org/dc/terms/> .
@prefix qudt: <http://qudt.org/schema/qudt/> .
@prefix skos: <http://www.w3.org/2004/02/skos/core#> .

<http://qudt.org/schema/qudt>
  a owl:Ontology ;
  owl:imports <http://www.w3.org/2004/02/skos/core> ;
  rdfs:label "QUDT Schema - Version 3.3.0" .

qudt:AbstractQuantityKind
  a owl:Class ;
  rdfs:subClassOf [
    a owl:Restriction ;
    owl:allValuesFrom qudt:QuantityKind ;
    owl:onProperty skos:broader ;
  ] .

qudt:Aspect
  a owl:Class, qudt:AspectClass ;
  dcterms:description "An aspect is an abstract type class that defines properties that can be reused."^^rdf:HTML .

qudt:hasQuantityKind
  a owl:ObjectProperty .
`))
	if err != nil {
		t.Fatal(err)
	}
	if !terms["http://qudt.org/schema/qudt/hasQuantityKind"] {
		t.Fatalf("expected QUDT hasQuantityKind in %#v", terms)
	}
}

func TestExtractVocabularyTermsExplicitMimeReportsOnlyThatParser(t *testing.T) {
	_, err := extractVocabularyTerms("http://example.org/vocab", "text/turtle; charset=utf-8", []byte(`not valid turtle`))
	if err == nil {
		t.Fatal("expected invalid Turtle to fail")
	}

	got := err.Error()
	if !strings.Contains(got, "turtle:") {
		t.Fatalf("error = %q, want Turtle parse error", got)
	}
	for _, parser := range []string{"json-ld:", "rdfxml:"} {
		if strings.Contains(got, parser) {
			t.Fatalf("error = %q, should not include %s parser error for explicit text/turtle", got, parser)
		}
	}
}

func TestSeedCoreVocabularyCache(t *testing.T) {
	cache := vocabularyCache{
		cacheDir: t.TempDir(),
		cache:    map[string]vocabulary{},
		fetch: func(u string) ([]byte, string, error) {
			switch u {
			case "http://schema.org/", "https://schema.org/":
				return []byte(schemaOrgTurtleVocabulary(u)), "text/turtle", nil
			case "https://www.opengis.net/def/schema/hy_features/hyf":
				return []byte(`@prefix hyf: <https://www.opengis.net/def/schema/hy_features/hyf/> .
hyf:HY_HydrometricFeature a hyf:Class .
hyf:HY_HydroLocation a hyf:Class .
hyf:HY_IndirectPosition a hyf:Class .
hyf:HydroLocationType a hyf:Property .
hyf:containingCatchment a hyf:Property .
hyf:linearElement a hyf:Property .
hyf:referencedPosition a hyf:Property .
`), "text/turtle", nil
			case "http://www.opengis.net/ont/geosparql":
				return []byte(`@prefix gsp: <http://www.opengis.net/ont/geosparql#> .
gsp:hasGeometry a gsp:Property .
gsp:asWKT a gsp:Property .
gsp:crs a gsp:Property .
gsp:sfWithin a gsp:Property .
gsp:wktLiteral a gsp:Class .
`), "text/turtle", nil
			case "http://purl.org/dc/terms/":
				return []byte(`@prefix dc: <http://purl.org/dc/terms/> .
dc:conformsTo a dc:Property .
`), "text/turtle", nil
			default:
				return nil, "", fmt.Errorf("unexpected url %s", u)
			}
		},
	}

	for _, base := range []string{
		"http://schema.org/",
		"https://schema.org/",
		"https://www.opengis.net/def/schema/hy_features/hyf/",
		"http://www.opengis.net/ont/geosparql#",
		"http://purl.org/dc/terms/",
	} {
		if _, err := cache.load(base); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCachedDocumentLoaderPersistsBetweenCalls(t *testing.T) {
	dir := t.TempDir()
	var calls atomic.Int64
	cache := vocabularyCache{
		cacheDir: dir,
		cache:    map[string]vocabulary{},
		failures: map[string]error{},
		fetch: func(u string) ([]byte, string, error) {
			calls.Add(1)
			if u != "https://example.com/vocab" {
				return nil, "", fmt.Errorf("unexpected url %s", u)
			}
			return []byte(`@prefix ex: <https://example.com/vocab#> .
ex:Thing a ex:Thing .`), "text/turtle", nil
		},
	}

	first, err := cache.load("https://example.com/vocab")
	if err != nil {
		t.Fatal(err)
	}
	second, err := cache.load("https://example.com/vocab")
	if err != nil {
		t.Fatal(err)
	}

	if calls.Load() != 1 {
		t.Fatalf("fetch calls = %d, want 1", calls.Load())
	}
	if !first.terms["https://example.com/vocab#Thing"] || !second.terms["https://example.com/vocab#Thing"] {
		t.Fatalf("cache did not retain expected term: %#v %#v", first.terms, second.terms)
	}
}

func TestVocabularyCachePersistsFailuresBetweenCalls(t *testing.T) {
	var calls atomic.Int64
	cache := vocabularyCache{
		cacheDir: t.TempDir(),
		cache:    map[string]vocabulary{},
		failures: map[string]error{},
		fetch: func(u string) ([]byte, string, error) {
			calls.Add(1)
			return nil, "", fmt.Errorf("cannot dereference %s", u)
		},
	}

	for range 2 {
		if _, err := cache.load("https://example.invalid/vocab/"); err == nil {
			t.Fatal("expected vocabulary load to fail")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("fetch calls = %d, want 1", calls.Load())
	}
}

func TestValidateTermsLogsRepeatedVocabularyFailureOnce(t *testing.T) {
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelError})))
	t.Cleanup(func() {
		slog.SetDefault(previousLogger)
	})

	err := validateTerms(
		"input.ttl",
		[]usedTerm{
			{iri: "https://example.invalid/vocab/Missing", line: 1},
			{iri: "https://example.invalid/vocab/Missing", line: 2},
		},
		jsonLDContext{prefixes: map[string]string{"ex": "https://example.invalid/vocab/"}},
		func(u string) ([]byte, string, error) {
			return nil, "", fmt.Errorf("cannot dereference %s", u)
		},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "input.ttl:1: failed to check vocabulary for ex:Missing")
	require.Contains(t, err.Error(), "input.ttl:2: failed to check vocabulary for ex:Missing")
	if got := strings.Count(logs.String(), "Failed to check vocabulary definition"); got != 1 {
		t.Fatalf("logged vocabulary failures = %d, want 1; logs:\n%s", got, logs.String())
	}
}

func TestVocabularyCacheFallsBackToBundledVocabulary(t *testing.T) {
	cache := vocabularyCache{
		cacheDir: t.TempDir(),
		cache:    map[string]vocabulary{},
		failures: map[string]error{},
		fetch: func(u string) ([]byte, string, error) {
			return nil, "", fmt.Errorf("cannot dereference %s", u)
		},
	}

	vocab, err := cache.load("https://schema.org/")
	if err != nil {
		t.Fatal(err)
	}
	if !vocab.terms["https://schema.org/Person"] {
		t.Fatalf("expected bundled schema.org vocabulary to define Person")
	}
	for _, term := range []string{
		"https://schema.org/ImageObject",
		"https://schema.org/MediaObject",
		"https://schema.org/BreadcrumbList",
	} {
		if !vocab.terms[term] {
			t.Fatalf("expected bundled schema.org vocabulary to define %s", term)
		}
	}
}

func TestVocabularyCacheStripsTrailingSlashForOpenGIS(t *testing.T) {
	var calls atomic.Int64
	cache := vocabularyCache{
		cacheDir: t.TempDir(),
		cache:    map[string]vocabulary{},
		failures: map[string]error{},
		fetch: func(u string) ([]byte, string, error) {
			calls.Add(1)
			if u != "https://www.opengis.net/def/schema/hy_features/hyf" {
				return nil, "", fmt.Errorf("unexpected url %s", u)
			}
			return []byte(`@prefix hyf: <https://www.opengis.net/def/schema/hy_features/hyf/> .
hyf:HY_HydrometricFeature a hyf:Class .
`), "text/turtle", nil
		},
	}

	if _, err := cache.load("https://www.opengis.net/def/schema/hy_features/hyf/"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("fetch calls = %d, want 1", calls.Load())
	}
}

func TestPrefixMappedVocabularyFetchOverridesSourceURL(t *testing.T) {
	const source = "https://www.opengis.net/def/schema/hy_features/hyf/"
	const mapped = "https://raw.githubusercontent.com/opengeospatial/HY_Features/refs/heads/master/ontology/ogc_ready/hyf.ttl"

	mappings, err := parsePrefixMaps([]string{
		source,
		mapped,
		"https://schema.org/=https://example.com/schema.jsonld",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := mappings[vocabularyDocumentURL(source)]; got != mapped {
		t.Fatalf("mapping for %s = %s, want %s", source, got, mapped)
	}
	if got := mappings[vocabularyDocumentURL("https://schema.org/")]; got != "https://example.com/schema.jsonld" {
		t.Fatalf("mapping for schema.org = %s, want example.com/schema.jsonld", got)
	}

	var fetched string
	wrapped, err := prefixMappedVocabularyFetch([]string{
		source,
		mapped,
	}, func(u string) ([]byte, string, error) {
		fetched = u
		return []byte("@prefix hyf: <https://www.opengis.net/def/schema/hy_features/hyf/> .\nhyf:HY_HydrometricFeature a hyf:Class .\n"), "text/turtle", nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := wrapped(source); err != nil {
		t.Fatal(err)
	}
	if fetched != mapped {
		t.Fatalf("fetched URL = %s, want %s", fetched, mapped)
	}
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func withFindSALProjectDir(t *testing.T, fn func(func() (string, error)) (string, error)) {
	t.Helper()
	previous := findSALProjectDir
	findSALProjectDir = fn
	t.Cleanup(func() {
		findSALProjectDir = previous
	})
}

type schemaOrgTestLoader struct{}

func (schemaOrgTestLoader) LoadDocument(u string) (*ld.RemoteDocument, error) {
	if u == testSchemaOrgBase || u == "https://schema.org/" {
		return &ld.RemoteDocument{
			DocumentURL: testSchemaOrgBase,
			Document: map[string]any{
				"@context": map[string]any{
					"@vocab": testSchemaOrgBase,
					"schema": testSchemaOrgBase,
				},
				"@graph": []any{
					map[string]any{"@id": "schema:Person"},
					map[string]any{"@id": "schema:jobTitle"},
					map[string]any{"@id": "schema:name"},
					map[string]any{"@id": "schema:telephone"},
					map[string]any{"@id": "schema:url"},
				},
			},
		}, nil
	}
	return ld.NewDefaultDocumentLoader(nil).LoadDocument(u)
}

func schemaOrgVocabularyFetch(u string) ([]byte, string, error) {
	if u != testSchemaOrgBase && u != "https://schema.org/" {
		return nil, "", fmt.Errorf("unexpected url %s", u)
	}
	return []byte(schemaOrgTurtleVocabulary(u)), "text/turtle", nil
}

func schemaOrgTurtleVocabulary(base string) string {
	_ = base
	terms := []string{
		"Person",
		"Place",
		"PropertyValue",
		"GovernmentOrganization",
		"GeoCoordinates",
		"Dataset",
		"DataDownload",
		"identifier",
		"name",
		"provider",
		"geo",
		"subjectOf",
		"description",
		"variableMeasured",
		"temporalCoverage",
		"distribution",
		"contentUrl",
		"encodingFormat",
		"unitText",
		"measurementTechnique",
		"measurementMethod",
		"publisher",
		"jobTitle",
		"telephone",
		"url",
		"propertyID",
		"value",
		"latitude",
		"longitude",
	}
	var b strings.Builder
	b.WriteString("@prefix schema: <https://schema.org/> .\n")
	b.WriteString("@prefix schemah: <http://schema.org/> .\n\n")
	for _, term := range terms {
		fmt.Fprintf(&b, "schema:%s a schema:Class .\n", term)
		fmt.Fprintf(&b, "schemah:%s a schemah:Class .\n", term)
	}
	return b.String()
}

type exampleVocabularyLoader struct{}

func (exampleVocabularyLoader) LoadDocument(u string) (*ld.RemoteDocument, error) {
	if u == "https://example.com/context" {
		return &ld.RemoteDocument{
			DocumentURL: u,
			Document: map[string]any{
				"@context": map[string]any{
					"ex": "https://example.com/vocab#",
				},
			},
		}, nil
	}
	return ld.NewDefaultDocumentLoader(nil).LoadDocument(u)
}

func exampleVocabularyFetch(u string) ([]byte, string, error) {
	if u != "https://example.com/vocab" {
		return nil, "", fmt.Errorf("unexpected url %s", u)
	}
	return []byte(`@prefix ex: <https://example.com/vocab#> .
ex:Known a ex:Class .
`), "text/turtle", nil
}
