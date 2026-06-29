package validate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeTurtleTestFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "data.ttl")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	return path
}

func TestValidateTurtleFileAcceptsDefinedSchemaTerms(t *testing.T) {
	path := writeTurtleTestFile(t, `
		@prefix schema: <https://schema.org/> .

		<person/bob> a schema:Person ;
			schema:name "Jane Doe" ;
			schema:jobTitle "Professor" .
	`)

	_, err := ValidateRDFFile(path, nil, testBase)

	require.NoError(t, err)
}

func TestValidateTurtleFileAcceptsSchemaPropertiesFromVocabulary(t *testing.T) {
	path := writeTurtleTestFile(t, `
		@prefix schema: <https://schema.org/> .
		@prefix xsd: <http://www.w3.org/2001/XMLSchema#> .

		<Alice>
			a schema:Person ;
			schema:name "Alice" ;
			schema:birthDate "1995-01-15"^^xsd:date ;
			schema:email <mailto:alice@example.org> ;
			schema:worksFor <org/acme> .
	`)

	_, err := ValidateRDFFile(path, nil, testBase)

	require.NoError(t, err)
}

func TestValidateTurtleFileRejectsUndefinedSchemaProperty(t *testing.T) {
	path := writeTurtleTestFile(t, `
		@prefix schema: <https://schema.org/> .

		<person/bob> a schema:Person ;
			schema:namee "Jane Doe" ;
			schema:jobTitle "Professor" .
	`)

	_, err := ValidateRDFFile(path, nil, testBase)

	require.Error(t, err)
	require.Contains(t, err.Error(), "undefined term")
	require.Contains(t, err.Error(), "schema:namee")
}

func TestValidateTurtleFileRejectsUndefinedSchemaClass(t *testing.T) {
	path := writeTurtleTestFile(t, `
		@prefix schema: <https://schema.org/> .

		<person/bob> a schema:Persson ;
			schema:name "Jane Doe" .
	`)

	_, err := ValidateRDFFile(path, nil, testBase)

	require.Error(t, err)
	require.Contains(t, err.Error(), "undefined term")
	require.Contains(t, err.Error(), "schema:Persson")
}

func TestValidateTurtleFileAcceptsSPARQLStylePrefix(t *testing.T) {
	path := writeTurtleTestFile(t, `
		PREFIX schema: <https://schema.org/>

		<person/bob> a schema:Person ;
			schema:telephone "(425) 123-4567" .
	`)

	_, err := ValidateRDFFile(path, nil, testBase)

	require.NoError(t, err)
}

func TestValidateTurtleFileSkipsRelativeSubjectUnderBase(t *testing.T) {
	path := writeTurtleTestFile(t, `
		@base <https://example.test/base/> .
		@prefix schema: <https://schema.org/> .

		<relative-person> a schema:Person ;
			schema:name "Jane Doe" .
	`)

	_, err := ValidateRDFFile(path, nil, testBase)

	require.NoError(t, err)
}
