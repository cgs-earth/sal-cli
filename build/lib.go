package build

import (
	"fmt"
	"strings"
)

type validationError struct {
	Path string
	Line int
	Term string
}

func (e validationError) Error() string {
	return fmt.Sprintf("%s:%d: undefined term %s", e.Path, e.Line, e.Term)
}

type multiError []error

func (e multiError) Error() string {
	var sb strings.Builder
	for i, err := range e {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(err.Error())
	}
	return sb.String()
}
