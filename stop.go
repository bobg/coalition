package coalition

// TODO: some "stop words" only work as prefixes (like "the"),
// some only as suffixes (like "inc"),
// and some only as infixes (like "and").
// Make the logic reflect this.

// Stopper can report whether a string is a "stop word."
type Stopper interface {
	// IsStopWord reports whether the given string is a stop word.
	IsStopWord(string) bool
}

type simpleStopper map[string]bool

var defaultStopper = simpleStopper{
	"the": true,
	"inc": true,
	"co":  true,
	"llc": true,
	"get": true,
	"try": true,
	"and": true,
}

func (s simpleStopper) IsStopWord(inp string) bool {
	return s[inp]
}
