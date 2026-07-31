package repository

import (
	"database/sql/driver"
	"fmt"
	"strings"

	sqlite "modernc.org/sqlite"
)

// SQLite's built-in lower() folds ASCII only, so "Åsa" and "åsa" are different
// strings to it. Anywhere a query is documented as case-insensitive that would
// be a lie for non-ASCII text, and it would also put the SQL apart from the two
// other implementations of the same rule: the filter engine in internal/lda
// (Go's strings.ToLower) and the demo backend in web/ts/demo (JavaScript's
// toLowerCase), both of which are Unicode-aware.
//
// unicode_lower is strings.ToLower exposed to SQL, so those three agree.
// Registration is global to the modernc driver and happens before any
// connection is opened, so every database this package opens has it.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("unicode_lower", 1, unicodeLower)
}

// unicodeLower implements the unicode_lower SQL function. NULL maps to NULL,
// which keeps instr(unicode_lower(col), ?) false for a NULL column rather than
// making it an error.
func unicodeLower(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("unicode_lower: want 1 argument, got %d", len(args))
	}
	switch v := args[0].(type) {
	case nil:
		return nil, nil
	case string:
		return strings.ToLower(v), nil
	case []byte:
		return strings.ToLower(string(v)), nil
	default:
		return nil, fmt.Errorf("unicode_lower: unsupported argument type %T", args[0])
	}
}
