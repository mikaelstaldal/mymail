package sanitize

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/html"
)

func countNodes(t *testing.T, h string) int {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(h))
	require.NoError(t, err)
	n := 0
	for range doc.Descendants() {
		n++
	}
	return n
}

// clonesPerRepeat is how many nodes each further "<p>x" adds to the parse of
// prefix: two of its own, plus every formatting element the tree builder
// reconstructs for it.
func clonesPerRepeat(t *testing.T, prefix string, bound func(string) string) int {
	t.Helper()
	const repeats = 50
	short := countNodes(t, bound(prefix+strings.Repeat("<p>x", repeats)))
	long := countNodes(t, bound(prefix+strings.Repeat("<p>x", 2*repeats)))
	return (long-short)/repeats - 2
}

func boundDefault(h string) string { return boundFormatting(h, MaxActiveFormatting) }

func TestBoundFormattingLeavesOrdinaryMarkupAlone(t *testing.T) {
	for _, h := range []string{
		"",
		"plain text",
		`<p><b>bold <i>both</i></b> <a href="https://example.com">link</a></p>`,
		`<table><tr><td><b>unclosed</td><td><i>also</td></tr></table><p><u>x</u></p>`,
		`<b><table><tr><td>cell</td></tr></table></b>`,
		`<a href="https://a.example">one<a href="https://b.example">two`,
		`<p>truncated <b`,
		strings.Repeat("<b>x</b>", 1000),
	} {
		assert.Equal(t, h, boundDefault(h))
	}
}

func TestBoundFormattingDropsBeyondLimitWithTheirEndTags(t *testing.T) {
	var in, want strings.Builder
	for i := range MaxActiveFormatting + 3 {
		fmt.Fprintf(&in, `<b style="color:#%06x">`, i)
		if i < MaxActiveFormatting {
			fmt.Fprintf(&want, `<b style="color:#%06x">`, i)
		}
	}
	in.WriteString("x")
	want.WriteString("x")
	for range MaxActiveFormatting + 3 {
		in.WriteString("</b>")
	}
	for range MaxActiveFormatting {
		want.WriteString("</b>")
	}
	in.WriteString("<i>after</i>")
	want.WriteString("<i>after</i>")
	assert.Equal(t, want.String(), boundDefault(in.String()))
	assert.Equal(t, "<p>x</p>", boundFormatting(`<p><b><a href="x">x</a></b></p>`, 0))
}

// Each prefix leaves formatting elements active by a route that a count of
// start and end tags alone would miss.
func TestBoundFormattingBoundsReconstruction(t *testing.T) {
	repeat := func(n int, f string) string {
		var b strings.Builder
		for i := range n {
			fmt.Fprintf(&b, f, i)
		}
		return b.String()
	}
	for name, prefix := range map[string]string{
		"nested":                 "<p>" + repeat(400, `<b style="color:#%06x">`),
		"end tag out of scope":   repeat(400, `<b style="color:#%06x"><table></b></table>`),
		"cell closed by row":     "<table><tr><td>" + repeat(400, `<tr><b style="color:#%06x"><td></td>`) + "</table>",
		"cell closed by section": "<table><tbody><tr><td>" + repeat(400, `</tbody><b style="color:#%06x"><td></td>`) + "</table>",
		"section end tag ignored": "<table><thead><tr><td><p>" + repeat(400, `<b style="color:#%06x">`) + "</tbody><p>" +
			repeat(400, `<i style="color:#%06x">`),
		"end tag in cell":        repeat(400, `<b style="color:#%06x"><table><tr><td></b></td></tr></table>`),
		"cell without table":     repeat(400, `<td><b style="color:#%06x"></td>`),
		"caption":                repeat(400, `<table><caption></caption><b style="color:#%06x"></table>`),
		"anchors":                repeat(400, `<a href="https://e.example/%d">`),
		"header cell in caption": "<table><caption><p>" + repeat(400, `<th><b style="color:#%06x">`),
		"adoption agency gives up": strings.Repeat(`<b style="color:red">`+strings.Repeat("<div>", 9)+"</b>"+
			strings.Repeat("</div>", 9)+"<p>x", 2) + repeat(MaxActiveFormatting, `<i style="color:#%06x">`),
	} {
		t.Run(name, func(t *testing.T) {
			clean := policy.Sanitize(prefix)
			got := clonesPerRepeat(t, clean, boundDefault)
			assert.LessOrEqual(t, got, MaxActiveFormatting)
		})
	}
}

// TestBoundFormattingNeverUnderCounts generates random markup in the
// sanitizer's element set and checks the parser never reconstructs more than
// MaxActiveFormatting elements. Each prefix is measured where it ends, so
// repeats only see what it leaves popped; the case table above covers the
// constructions one by one.
func TestBoundFormattingNeverUnderCounts(t *testing.T) {
	formatting := []string{"a", "b", "code", "em", "i", "s", "small", "strong", "tt", "u"}
	structure := []string{
		// Reconstruction needs formatting elements popped implicitly, which
		// is mostly what a new <p> does.
		"<p>", "<p>", "<p>", "<p>", "</p>", "<div>", "</div>", "<ul>", "<li>", "</ul>", "<br>", "x",
		"<table>", "</table>", "<tr>", "</tr>", "<td>", "</td>", "<th>", "</th>",
		"<caption>", "</caption>", "<tbody>", "</tbody>", "<thead>", "</thead>",
		"<tfoot>", "</tfoot>", "<colgroup>", "</colgroup>", "<col>",
	}
	rnd := rand.New(rand.NewSource(1))
	iterations := 3000
	if testing.Short() {
		iterations = 300
	}
	for it := range iterations {
		var b strings.Builder
		for i := range 60 {
			f := formatting[rnd.Intn(len(formatting))]
			switch r := rnd.Intn(10); {
			case r < 4:
				if f == "a" {
					fmt.Fprintf(&b, `<a href="https://e.example/%d">`, i)
				} else {
					fmt.Fprintf(&b, `<%s style="color:#%06x">`, f, i)
				}
			case r < 6:
				fmt.Fprintf(&b, "</%s>", f)
			default:
				b.WriteString(structure[rnd.Intn(len(structure))])
			}
		}
		clean := policy.Sanitize(b.String())
		if got := clonesPerRepeat(t, clean, boundDefault); got > MaxActiveFormatting {
			t.Fatalf("iteration %d: %d clones per repeat after %q", it, got, clean)
		}
	}
}

func TestBoundFormattingIsLinear(t *testing.T) {
	h := strings.Repeat(`<b style="color:red">`, 200_000) + strings.Repeat("</b>", 200_000)
	start := time.Now()
	out := boundDefault(h)
	assert.Less(t, time.Since(start), 5*time.Second)
	assert.Equal(t, MaxActiveFormatting, strings.Count(out, "<b"))
}

func TestBoundFormattingTestDetectsUnboundedMarkup(t *testing.T) {
	prefix := policy.Sanitize("<p>" + strings.Repeat(`<b style="color:red">`, 2) + strings.Repeat(`<i style="color:blue">`, 2) +
		`<u style="color:#000001"><u style="color:#000002"><u style="color:#000003"><s style="color:#000004"><s style="color:#000005">`)
	assert.Greater(t, clonesPerRepeat(t, prefix, func(h string) string { return h }), MaxActiveFormatting)
}

func TestResolveCIDBoundsAmplification(t *testing.T) {
	var b strings.Builder
	b.WriteString("<p>")
	for i := range 400 {
		fmt.Fprintf(&b, `<b style="color:#%06x;font-family:Arial,Helvetica,sans-serif">`, i)
	}
	b.WriteString(strings.Repeat("<p>x", 4000))
	in := b.String()
	out, _ := ResolveCID(in, nil, nil)
	assert.LessOrEqual(t, len(out), maxAmplification*len(in))
	assert.Contains(t, out, "<p>x</p>")
	assert.NotContains(t, out, "<b", "falls back to removing formatting")

	ordinary := `<p><b style="color: red">bold</b> and <i>italic</i></p>`
	out, _ = ResolveCID(ordinary, nil, nil)
	assert.Equal(t, ordinary, out)
}

func TestOutgoingSoftWrapJoinIsBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<p class="ql-softwrap-y">`)
	for i := range 400 {
		fmt.Fprintf(&b, `<b style="color:#%06x">`, i)
	}
	b.WriteString(strings.Repeat("<p>x", 4000))
	in := b.String()
	out := OutgoingHTML(in)
	assert.LessOrEqual(t, strings.Count(out, "<b"), MaxActiveFormatting*4001)
	assert.Contains(t, out, "x")
}
