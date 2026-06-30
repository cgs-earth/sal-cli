package build

import (
	"testing"

	"github.com/cgs-earth/sal/pkg"
	"github.com/stretchr/testify/require"
	rdflibgo "github.com/tggo/goRDFlib"
)

func TestAllNewTypesHaveRdfClassTypes(t *testing.T) {
	base, err := pkg.DefaultSalBase()
	require.NoError(t, err)

	g := rdflibgo.NewGraph(rdflibgo.WithBase(base))
	thing := rdflibgo.NewURIRefUnsafe(base + "Thing")
	thingType := rdflibgo.NewURIRefUnsafe(base + "ThingType")

	g.Add(thing, rdflibgo.RDF.Type, thingType)
	g.Add(thingType, rdflibgo.RDF.Type, rdflibgo.RDFS.Class)
	g.Add(thingType, rdflibgo.RDFS.SubClassOf, rdflibgo.RDFS.Class)

	require.NoError(t, NewTermsHaveClassDefinitions(g))
}

func TestAllNewTypesHaveRdfClassTypesRequiresTypeForLocalSubjects(t *testing.T) {
	base, err := pkg.DefaultSalBase()
	require.NoError(t, err)

	g := rdflibgo.NewGraph(rdflibgo.WithBase(base))
	thing := rdflibgo.NewURIRefUnsafe(base + "Thing")

	g.Add(thing, rdflibgo.RDFS.Label, rdflibgo.NewLiteral("Thing"))

	err = NewTermsHaveClassDefinitions(g)
	require.Error(t, err)
	var typeDefinitionErr TermLacksTypeDefinitionErr
	require.ErrorAs(t, err, &typeDefinitionErr)
	require.Equal(t, base+"Thing", typeDefinitionErr.iri)
}

func TestAllNewTypesHaveRdfClassTypesRequiresLocalTypesToSubclassRDFSClass(t *testing.T) {
	base, err := pkg.DefaultSalBase()
	require.NoError(t, err)

	g := rdflibgo.NewGraph(rdflibgo.WithBase(base))
	thing := rdflibgo.NewURIRefUnsafe(base + "Thing")
	thingType := rdflibgo.NewURIRefUnsafe(base + "ThingType")

	g.Add(thing, rdflibgo.RDF.Type, thingType)
	g.Add(thingType, rdflibgo.RDF.Type, rdflibgo.RDFS.Class)

	err = NewTermsHaveClassDefinitions(g)
	require.Error(t, err)
	var subclassErr TermIsNotASubClassOfRDFSClassErr
	require.ErrorAs(t, err, &subclassErr)
	require.Equal(t, base+"ThingType", subclassErr.iri)
}

func TestAllNewTypesHaveRdfClassTypesAllowsTransitiveSubclass(t *testing.T) {
	base, err := pkg.DefaultSalBase()
	require.NoError(t, err)

	g := rdflibgo.NewGraph(rdflibgo.WithBase(base))
	thing := rdflibgo.NewURIRefUnsafe(base + "Thing")
	thingType := rdflibgo.NewURIRefUnsafe(base + "ThingType")
	parentType := rdflibgo.NewURIRefUnsafe(base + "ParentType")

	g.Add(thing, rdflibgo.RDF.Type, thingType)
	g.Add(thingType, rdflibgo.RDF.Type, rdflibgo.RDFS.Class)
	g.Add(thingType, rdflibgo.RDFS.SubClassOf, parentType)
	g.Add(parentType, rdflibgo.RDF.Type, rdflibgo.RDFS.Class)
	g.Add(parentType, rdflibgo.RDFS.SubClassOf, rdflibgo.RDFS.Class)

	require.NoError(t, NewTermsHaveClassDefinitions(g))
}
