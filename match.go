package coalition

import (
	"context"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/agnivade/levenshtein"
	"github.com/bobg/htree"
	"golang.org/x/net/html"
)

// MatchDomain matches ref,
// a reference string containing an organization name,
// against domain.
// It simply calls Match on a Matcher with the default configuration.
// See Matcher.Match.
func MatchDomain(ref, domain string) (float32, error) {
	return defaultMatcher.Match(ref, domain)
}

type testType int

const (
	testNone testType = iota

	// RootPhrase tests whether the normalized root phrase of the input appears in the domain name.
	testRootPhrase

	// AnyRootWord tests whether any word of the normalized root phrase of the input appears in the domain name.
	// Only runs when RootPhrase does not pass.
	testAnyRootWord

	// MisspelledRootPhrase tests whether the normalized root phrase of the input appears in misspelled form in the domain name.
	// Only runs when RootPhrase does not pass.
	testMisspelledRootPhrase

	// SignificantAffixes tests whether non-ignorable affixes appear in the domain name.
	// This is a negative test: passing subtracts from the overall score.
	testSignificantAffixes

	// WebPageRef tests whether the normalized root phrase of the input appears on the home page for the domain.
	testWebPageRef
)

// Matcher is a configuration object for performing matches.
// It specifies the tests to run and the score to be applied for each passing test.
// It also specifies a source for stop words.
type Matcher struct {
	Scores map[testType]int
	Stop   Stopper
}

var defaultMatcher = Matcher{
	Scores: map[testType]int{
		testRootPhrase:           50,
		testAnyRootWord:          5,
		testMisspelledRootPhrase: 5,
		testSignificantAffixes:   -10,
		testWebPageRef:           50,
	},
	Stop: defaultStopper,
}

// NewMatcher returns a new Matcher with default score values.
// It does this by making a copy of defaultMatcher.
// The copy is deep so callers are free to modify the result without affecting defaultMatcher.
func NewMatcher() Matcher {
	result := defaultMatcher // makes a copy, but with a reference to the same Scores map
	result.Scores = make(map[testType]int)
	for k, v := range defaultMatcher.Scores {
		result.Scores[k] = v
	}
	return result
}

// Match matches ref,
// a reference string containing an organization name,
// against domain.
// It reports the likelihood
// (as a float in [0.0..1.0])
// that the domain belongs to the organization.
func (m Matcher) Match(ref, domain string) (float32, error) {
	score, err := m.doMatch(ref, domain)
	if err != nil {
		return 0, err
	}

	// Compute the min and max possible scores.
	var min, max int

	for _, v := range m.Scores {
		if v < 0 {
			min += v
		} else {
			max += v
		}
	}

	// Map score from that range to [0..1].
	return float32(score-min) / float32(max-min), nil
}

func (m Matcher) doMatch(ref, domain string) (int, error) {
	norm := m.normalizedRootPhrase(ref)

	domain = strings.ToLower(domain)
	// TODO: lop off TLD(s) from domain,
	// and uninteresting subdomains.
	// (E.g. in foo.coalitioninc.com we only care about coalitioninc.)
	// Need to recognize that in something like coalition.github.io
	// we might care about coalition or we might care about github.

	// The normalized root phrase as a single string.
	joined := strings.Join(norm, "")

	// Make a copy of norm that contains only significant words
	// (so {"sanford", "and", "son"} becomes {"sanford", "son"}).
	var significantNorm []string
	for _, word := range norm {
		if !m.Stop.IsStopWord(word) {
			significantNorm = append(significantNorm, word)
		}
	}

	// Now make a regex that matches the significant words of norm,
	// in sequence,
	// plus anything between them
	// (so "sanford and son" or "sanford & son" or "sanford, son" etc).
	// Note: the strings in norm don't need quoting with regexp.QuoteMeta
	// because they contain only letters and no metacharacters.
	re, err := regexp.Compile(strings.Join(norm, "(.*)"))
	if err != nil { // should be impossible
		return 0, err
	}

	// min and max hold the lowest and highest possible scores,
	// for mapping to [0..1] at the end.

	var score int

	passed := make(map[testType]bool)

	// RootPhrase test.
	if v := m.Scores[testRootPhrase]; v != 0 {
		if strings.Contains(domain, joined) {
			score += v
			passed[testRootPhrase] = true
		}
	}

	// AnyRootWord test.
	if v := m.Scores[testAnyRootWord]; !passed[testRootPhrase] && v != 0 {
		for _, word := range norm {
			if strings.Contains(domain, word) {
				score += v
				passed[testAnyRootWord] = true
				break
			}
		}
	}

	// MisspelledRootPhrase test.
	if v := m.Scores[testMisspelledRootPhrase]; !passed[testRootPhrase] && v != 0 {
		// Check each substring of domain whose length is in [len(joined)-2..len(joined)+2]
		// looking for ones with a Levenshtein edit distance of 1 or 2 away from joined.
		// (An edit distance of 0 is an exact match which is covered by the testRootPhrase case.)
		found := false
		for start := 0; !found && start < len(domain)-len(joined)+2; start++ {
			for l := -2; l <= 2; l++ {
				end := start + len(joined) + l
				if end > len(domain) {
					break
				}
				substr := domain[start:end]
				if d := levenshtein.ComputeDistance(joined, substr); d == 1 || d == 2 {
					found = true
					break
				}
			}
		}
		if found {
			score += v
			passed[testMisspelledRootPhrase] = true
		}
	}

	// SignificantAffixes test.
	if v := m.Scores[testSignificantAffixes]; v != 0 {
		if m.doSignificantAffixesTest(domain, re) {
			score += v
			passed[testSignificantAffixes] = true
		}
	}

	if v := m.Scores[testWebPageRef]; v != 0 {
		// Note: if domain is normalized in some way (see notes above),
		// we want the unmodified domain here.
		found, err := doWebPageRefTest(domain, re)
		if err != nil {
			return 0, err
		}
		if found {
			score += v
			passed[testWebPageRef] = true
		}
	}

	return score, nil
}

// This normalizes an input string like "The Genco Olive Oil Company, LLP"
// to a "root phrase" like {"genco", "olive", "oil"}.
// It does this by downcasing everything,
// collapsing some punctuation (e.g. apostrophes),
// splitting into words (on whitespace and other punctuation),
// and removing stop words from the left and right ends.
// TODO: Map Unicode letters with diacritics to plain letters where possible. (See https://blog.golang.org/normalization.)
func (m Matcher) normalizedRootPhrase(inp string) []string {
	inp = strings.ToLower(inp)

	// Collapse apostrophes, so "Tom's of Maine" does not become {"tom", "s", "of", "maine"}. (TODO: Anything else?)
	inp = strings.ReplaceAll(inp, "'", "")
	inp = strings.ReplaceAll(inp, "’", "")

	norm := strings.FieldsFunc(inp, func(r rune) bool {
		return !unicode.IsLetter(r)
	})
	for len(norm) > 1 {
		if m.Stop.IsStopWord(norm[0]) {
			norm = norm[1:]
			continue
		}
		if m.Stop.IsStopWord(norm[len(norm)-1]) {
			norm = norm[:len(norm)-1]
			continue
		}
		break
	}
	return norm
}

func (m Matcher) doSignificantAffixesTest(domain string, re *regexp.Regexp) bool {
	domainParts := strings.Split(domain, ".")
	for _, part := range domainParts {
		indexes := re.FindStringSubmatchIndex(part)
		if len(indexes) == 0 {
			continue
		}
		if prefix := part[:indexes[0]]; prefix != "" && !m.Stop.IsStopWord(prefix) {
			return true
		}
		if suffix := part[indexes[1]:]; suffix != "" && !m.Stop.IsStopWord(suffix) {
			return true
		}
		for i := 2; i < len(indexes); i += 2 {
			interiorWord := part[indexes[i]:indexes[i+1]]
			if !m.Stop.IsStopWord(interiorWord) {
				return true
			}
		}
	}
	return false
}

func doWebPageRefTest(domain string, re *regexp.Regexp) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // arbitrary timeout
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "http://"+domain, nil) // TODO: try other URLs in the same domain, like /about
	if err != nil {
		return false, err
	}
	client := new(http.Client)
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	ctField := resp.Header.Get("Content-Type")
	contentType, _, err := mime.ParseMediaType(ctField)
	if err != nil {
		return false, err
	}
	if contentType != "text/html" {
		return false, nil
	}

	// The body is HTML. Parse it and walk it looking for a match against re.
	tree, err := html.Parse(resp.Body)
	if err != nil {
		return false, err
	}

	// This comes from my htree package. It extracts plain text from HTML.
	// See https://godoc.org/github.com/bobg/htree#Text.
	text, err := htree.Text(tree)
	if err != nil {
		return false, err
	}

	return re.MatchString(text), nil // TODO: inspect submatches for significant words.
}
