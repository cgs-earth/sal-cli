package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/cgs-earth/sal-cli/build/vocab"
)

type vocabularyCache struct {
	cacheDir string
	cache    map[string]vocabulary
	failures map[string]error
	fetch    func(string) ([]byte, string, error)
}

const vocabularyCacheVersion = 9

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

func (c *vocabularyCache) isDefined(iri string, ctx jsonLDContext) (bool, error) {
	if iriWithoutXsdNamepace, found := strings.CutPrefix(iri, xsdNamespaceIRI); found {
		return slices.Contains(xsdBuiltinDatatypeLocalNames, iriWithoutXsdNamepace), nil
	}

	_, base, ok := longestPrefixBase(iri, ctx)
	if !ok {
		return true, nil
	}
	vocab, err := c.load(base)
	if err != nil {
		return false, err
	}
	return vocab.terms[iri], nil
}

func (c *vocabularyCache) load(base string) (vocabulary, error) {
	base = vocabularyDocumentURL(base)
	if vocab, ok := c.cache[base]; ok {
		return vocab, nil
	}
	if c.failures == nil {
		c.failures = map[string]error{}
	}
	if err, ok := c.failures[base]; ok {
		return vocabulary{}, err
	}

	terms, err := c.loadTerms(base)
	if err != nil {
		c.failures[base] = err
		return vocabulary{}, err
	}
	vocab := vocabulary{terms: terms}
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
		terms, err := extractVocabularyTerms(base, contentType, body)
		if err == nil {
			if err := c.storeTermsToDisk(base, terms); err != nil {
				return nil, err
			}
			return terms, nil
		}
		fetchErr = err
	}

	terms, err := loadBundledVocabularyTerms(base)
	if err != nil {
		return nil, fetchErr
	}

	if err := c.storeTermsToDisk(base, terms); err != nil {
		return nil, err
	}
	return terms, nil
}

func loadBundledVocabularyTerms(base string) (map[string]bool, error) {
	body, contentType, ok, err := vocab.Load(base)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, vocab.MissingError(base)
	}
	return extractVocabularyTerms(base, contentType, body)
}
