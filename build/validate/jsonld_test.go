package validate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const testBase = "https://example.test/base/"

func writeJSONLDTestFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "data.jsonld")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	return path
}

func TestValidateJSONLDFileRejectsUndefinedSchemaOrgProperty(t *testing.T) {
	path := writeJSONLDTestFile(t, `{
		"@context": "http://schema.org/",
		"@type": "Person",
		"@id": "Bob",
		"namee": "Jane Doe",
		"jobTitle": "Professor",
		"telephone": "(425) 123-4567",
		"url": "http://www.janedoe.com"
	}`)

	_, err := ValidateRDFFile(path, nil, testBase)

	require.Error(t, err)
	require.Contains(t, err.Error(), "undefined term")
	require.Contains(t, err.Error(), "namee")
}

func TestValidateJSONLDFileAcceptsInlineVocabDefinedSchemaTerms(t *testing.T) {
	path := writeJSONLDTestFile(t, `{
		"@context": {
			"@vocab": "https://schema.org/"
		},
		"@type": "Person",
		"@id": "Bob",
		"name": "Jane Doe",
		"jobTitle": "Professor"
	}`)

	_, err := ValidateRDFFile(path, nil, testBase)

	require.NoError(t, err)
}

func TestValidateJSONLDFileRejectsUndefinedInlineVocabType(t *testing.T) {
	path := writeJSONLDTestFile(t, `{
		"@context": {
			"@vocab": "https://schema.org/"
		},
		"@type": "Persson",
		"@id": "Bob",
		"name": "Jane Doe"
	}`)

	_, err := ValidateRDFFile(path, nil, testBase)

	require.Error(t, err)
	require.Contains(t, err.Error(), "undefined term")
	require.Contains(t, err.Error(), "Persson")
}

func TestValidateJSONLDFileRejectsUndefinedCompactProperty(t *testing.T) {
	path := writeJSONLDTestFile(t, `{
		"@context": {
			"schema": "https://schema.org/"
		},
		"@id": "Bob",
		"@type": "schema:Person",
		"schema:namee": "Jane Doe"
	}`)

	_, err := ValidateRDFFile(path, nil, testBase)

	require.Error(t, err)
	require.Contains(t, err.Error(), "undefined term")
	require.Contains(t, err.Error(), "schema:namee")
}

func TestValidateJSONLDFileSkipsRelativeIDUnderBase(t *testing.T) {
	path := writeJSONLDTestFile(t, `{
		"@context": {
			"@vocab": "https://schema.org/"
		},
		"@id": "relative-person",
		"@type": "Person",
		"name": "Jane Doe"
	}`)

	_, err := ValidateRDFFile(path, nil, testBase)

	require.NoError(t, err)
}

func TestValidateJSONLDFileRejectsLocalIDWithoutType(t *testing.T) {
	path := writeJSONLDTestFile(t, `{
		"@context": "http://schema.org/",
		"@id": "Jane",
		"name": "Jane Doe",
		"jobTitle": "Professor",
		"telephone": "(425) 123-4567",
		"url": "http://www.janedoe.com"
	}`)

	_, err := ValidateRDFFile(path, nil, testBase)

	require.Error(t, err)
	var missingTypeErr missingTypeError
	require.ErrorAs(t, err, &missingTypeErr)
	require.Equal(t, testBase+"Jane", missingTypeErr.IRI)
}
