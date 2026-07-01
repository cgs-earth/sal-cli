package salmodule

import (
	_ "embed"
	"fmt"
)

//go:embed sal_ontology.ttl
var salOntology string

type ontologyCmd struct {
}

func (cmd *ontologyCmd) Run() error {
	fmt.Print(salOntology)
	return nil
}
