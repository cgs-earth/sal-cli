package salmodule

import "fmt"

type SalModuleCmd struct {
	Ontology *ontologyCmd `arg:"subcommand:ontology" help:"Print a sal module's ontology"`
	Run      *runCmd      `arg:"subcommand:run" help:"Run a sal module"`
}

func Run(cmd *SalModuleCmd) error {
	switch {
	case cmd.Ontology != nil:
		return cmd.Ontology.Run()
	case cmd.Run != nil:
		return cmd.Run.Run()
	default:
		return fmt.Errorf("salmodule must be ran with a subcommand")
	}
}
