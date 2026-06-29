package build

import (
	"bytes"
	"fmt"

	rdflibgo "github.com/tggo/goRDFlib"
	"github.com/tggo/goRDFlib/jsonld"
	"github.com/tggo/goRDFlib/rdfxml"
	"github.com/tggo/goRDFlib/turtle"
)

type RDFFormat string

const (
	RDFXML RDFFormat = "application/rdf+xml"
	TURTLE RDFFormat = "text/turtle"
	JSONLD RDFFormat = "application/ld+json"
)

func parseRdf(body []byte, base string, format RDFFormat) (*rdflibgo.Graph, error) {
	switch format {
	case RDFXML:
		g := rdflibgo.NewGraph(rdflibgo.WithBase(base))
		if err := rdfxml.Parse(g, bytes.NewReader(body), rdfxml.WithBase(base)); err != nil {
			return nil, err
		}
		return g, nil
	case TURTLE:
		g := rdflibgo.NewGraph(rdflibgo.WithBase(base))
		if err := turtle.Parse(g, bytes.NewReader(body), turtle.WithBase(base)); err != nil {
			return nil, err
		}
		return g, nil
	case JSONLD:
		g := rdflibgo.NewGraph(rdflibgo.WithBase(base))
		options := []jsonld.Option{jsonld.WithBase(base), jsonld.WithUnboundedLines()}
		if err := jsonld.Parse(g, bytes.NewReader(body), options...); err != nil {
			return nil, err
		}
		return g, nil
	default:
		return nil, fmt.Errorf("unknown format: %s", format)
	}
}
