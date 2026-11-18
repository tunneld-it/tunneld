package pid

import "errors"

// Background is unsupported on Windows, as -d is: there is
// no session for a run to be left running in.
func Background(string, []string, []string) (Status, error) {
	return Status{}, errors.ErrUnsupported
}
