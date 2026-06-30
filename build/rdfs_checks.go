package build

import (
	"fmt"
	"strings"

	"github.com/cgs-earth/sal/build/validate"
	"github.com/cgs-earth/sal/pkg"
	rdflibgo "github.com/tggo/goRDFlib"
)

type TermLacksTypeDefinitionErr struct {
	iri string
}

func (t TermLacksTypeDefinitionErr) Error() string {
	return fmt.Sprintf("%s requires a rdf:type definition but it was not found", t.iri)
}

type TermIsNotASubClassOfRDFSClassErr struct {
	iri string
}

func (t TermIsNotASubClassOfRDFSClassErr) Error() string {
	return fmt.Sprintf("%s must be a subclass of rdfs:Class but the type definition was not a subclass of rdfs:Class", t.iri)
}

// NewTermsHaveClassDefinitions verifies local resources have rdf:type definition triples and
// local rdf:type values are declared as subclasses of rdfs:Class.
func NewTermsHaveClassDefinitions(g *rdflibgo.Graph) error {
	baseForRelativePaths, err := pkg.DefaultSalBase()
	if err != nil {
		return err
	}

	localSubjects := map[string]bool{}
	typedSubjects := map[string]bool{}
	localTypes := map[string]bool{}
	subClasses := map[string][]rdflibgo.URIRef{}

	g.Triples(nil, nil, nil)(func(triple rdflibgo.Triple) bool {
		if subj, ok := triple.Subject.(rdflibgo.URIRef); ok && strings.HasPrefix(subj.Value(), baseForRelativePaths) {
			localSubjects[subj.Value()] = true
		}
		if triple.Predicate.Equal(rdflibgo.RDF.Type) {
			if subj, ok := triple.Subject.(rdflibgo.URIRef); ok {
				typedSubjects[subj.Value()] = true
			}
			if obj, ok := triple.Object.(rdflibgo.URIRef); ok && strings.HasPrefix(obj.Value(), baseForRelativePaths) {
				localTypes[obj.Value()] = true
			}
		}
		if triple.Predicate.Equal(rdflibgo.RDFS.SubClassOf) {
			subj, subjOK := triple.Subject.(rdflibgo.URIRef)
			obj, objOK := triple.Object.(rdflibgo.URIRef)
			if subjOK && objOK {
				subClasses[subj.Value()] = append(subClasses[subj.Value()], obj)
			}
		}
		return true
	})

	var errs validate.MultiError
	for iri := range localSubjects {
		if !typedSubjects[iri] {
			errs = append(errs, TermLacksTypeDefinitionErr{iri: iri})
		}
	}
	for iri := range localTypes {
		// we use an empty map here to start the recursion search. this is
		// essentially an accumulator cache.
		if !isSubClassOfRDFSClass(iri, subClasses, map[string]bool{}) {
			errs = append(errs, TermIsNotASubClassOfRDFSClassErr{iri: iri})
		}
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

// isSubClassOfRDFSClass recursively follows rdfs:subClassOf links until it reaches rdfs:Class.
func isSubClassOfRDFSClass(iri string, subClasses map[string][]rdflibgo.URIRef, seen map[string]bool) bool {
	if seen[iri] {
		return false
	}
	seen[iri] = true
	for _, parent := range subClasses[iri] {
		if parent.Equal(rdflibgo.RDFS.Class) || isSubClassOfRDFSClass(parent.Value(), subClasses, seen) {
			return true
		}
	}
	return false
}
